package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fabioaraujo121/pair/internal/agent"
	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/events"
)

type promptOut struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func newRepo(t *testing.T) (*Handler, string) {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := execGit(root, args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(root, "app.rb"), []byte("class App\n  def run\n    1\n  end\nend\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "lib.rb"), []byte("module Lib\nend\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".pair/\nPAIRING.md\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "PAIRING.md"), []byte("# PAIRING\n\n## Decisions\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "init")
	h := &Handler{
		Cfg:  config.Config{Root: root, StateDir: filepath.Join(root, ".pair"), DiffLimit: 200, Session: "no-such-session"},
		Log:  events.Open(filepath.Join(root, ".pair")),
		Nvim: &fakeNvim{},
	}
	return h, root
}

func prompt(t *testing.T, h *Handler) string {
	t.Helper()
	var buf bytes.Buffer
	h.Stdout = &buf
	if err := h.Prompt(&agent.Event{Hook: "prompt", Prompt: "next"}); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		return ""
	}
	var out promptOut
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid hook JSON: %v\n%s", err, buf.String())
	}
	if out.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Fatalf("hookEventName = %q", out.HookSpecificOutput.HookEventName)
	}
	return out.HookSpecificOutput.AdditionalContext
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPromptReportsOnlyHandEditsOnce(t *testing.T) {
	h, root := newRepo(t)
	app := filepath.Join(root, "app.rb")

	// First prompt: silent baseline, state created.
	if ctx := prompt(t, h); ctx != "" {
		t.Fatalf("first prompt must baseline silently, got:\n%s", ctx)
	}
	if !loadState(h.Cfg.StateDir).Baselined {
		t.Fatal("state not baselined")
	}

	// Nothing changed: nothing reported.
	if ctx := prompt(t, h); ctx != "" {
		t.Fatalf("clean tree reported:\n%s", ctx)
	}

	// Engineer edits a tracked file by hand.
	write(t, app, "class App\n  def run\n    42\n  end\nend\n")
	ctx := prompt(t, h)
	if !strings.Contains(ctx, "Engineer changed since last turn") || !strings.Contains(ctx, "### app.rb") ||
		!strings.Contains(ctx, "-    1") || !strings.Contains(ctx, "+    42") {
		t.Fatalf("hand edit not reported:\n%s", ctx)
	}
	if strings.Contains(ctx, ".pair/") {
		t.Fatalf("snapshot paths leaked:\n%s", ctx)
	}
	// Reported once.
	if ctx := prompt(t, h); ctx != "" {
		t.Fatalf("same edit reported twice:\n%s", ctx)
	}

	// Claude edits the file (post_edit snapshots it): not reported.
	write(t, app, "class App\n  def run\n    42\n  end\n\n  def stop; end\nend\n")
	if err := h.PostEdit(edit(app)); err != nil {
		t.Fatal(err)
	}
	if ctx := prompt(t, h); ctx != "" {
		t.Fatalf("Claude's own edit reported as the engineer's:\n%s", ctx)
	}

	// Engineer edits on top of Claude's version: only that hunk shows up.
	write(t, app, "class App\n  def run\n    42\n  end\n\n  def stop; puts 'bye'; end\nend\n")
	ctx = prompt(t, h)
	if !strings.Contains(ctx, "+  def stop; puts 'bye'; end") || strings.Contains(ctx, "+    42") {
		t.Fatalf("expected only the engineer's hunk on top of Claude's snapshot:\n%s", ctx)
	}
}

func TestPromptReportsNewIgnoredAndDeletedFiles(t *testing.T) {
	h, root := newRepo(t)
	prompt(t, h) // baseline

	// New untracked file.
	write(t, filepath.Join(root, "notes.rb"), "# todo\n")
	// Scratchpad (gitignored) edited by the engineer.
	write(t, filepath.Join(root, "PAIRING.md"), "# PAIRING\n\n## Decisions\n- use Solid Queue\n")
	ctx := prompt(t, h)
	if !strings.Contains(ctx, "### notes.rb") || !strings.Contains(ctx, "+# todo") {
		t.Fatalf("untracked file not reported:\n%s", ctx)
	}
	if !strings.Contains(ctx, "### PAIRING.md") || !strings.Contains(ctx, "+- use Solid Queue") {
		t.Fatalf("PAIRING.md edit not reported:\n%s", ctx)
	}

	// Deleting a known file is reported as deleted.
	_ = os.Remove(filepath.Join(root, "notes.rb"))
	ctx = prompt(t, h)
	if !strings.Contains(ctx, "### notes.rb (deleted)") {
		t.Fatalf("deletion not reported:\n%s", ctx)
	}
	if ctx := prompt(t, h); ctx != "" {
		t.Fatalf("deletion reported twice:\n%s", ctx)
	}
}

func TestRenderChangesRespectsLimit(t *testing.T) {
	big := strings.Repeat("+line\n", 50)
	changes := []Change{
		{Path: "a.rb", Diff: "--- a/a.rb\n+++ b/a.rb\n" + big, Lines: 52},
		{Path: "b.rb", Diff: "--- a/b.rb\n+++ b/b.rb\n" + big, Lines: 52},
	}
	text, truncated := RenderChanges(changes, 60)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if strings.Count(text, "+line") > 60 {
		t.Fatalf("limit not respected: %d lines", strings.Count(text, "+line"))
	}
	if !strings.Contains(text, "### b.rb") || !(strings.Contains(text, "omitted") || strings.Contains(text, "truncated")) {
		t.Fatalf("second file must still be named and the cut announced:\n%s", text)
	}
	// With no budget left, the second file is named but its diff omitted.
	text, _ = RenderChanges(changes, 52)
	if !strings.Contains(text, "### b.rb") || !strings.Contains(text, "omitted") {
		t.Fatalf("zero budget:\n%s", text)
	}
	text, truncated = RenderChanges(changes[:1], 200)
	if truncated || !strings.Contains(text, "```diff") {
		t.Fatalf("small diff must render whole: %v\n%s", truncated, text)
	}
}

func TestPointBuildsTheReference(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	err := Point([]string{"--file", filepath.Join(root, "app/models/user.rb"), "--start", "12", "--end", "20", "--note", "essa validação", "--root", root}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "Look at app/models/user.rb:12-20: essa validação" {
		t.Fatalf("got %q", got)
	}
	out.Reset()
	_ = Point([]string{"--file", "x.go", "--start", "7", "--root", root}, &out)
	if got := strings.TrimSpace(out.String()); got != "Look at x.go:7" {
		t.Fatalf("got %q", got)
	}
	if err := Point([]string{"--file", "x.go"}, &out); err == nil {
		t.Fatal("missing --start must be an error")
	}
}

func TestRepeatedTranscriptIsNotSentTwice(t *testing.T) {
	cfg := config.Config{UserDir: t.TempDir()}
	if repeated(cfg, "Where did we stop?") {
		t.Fatal("first time is not a repeat")
	}
	if !repeated(cfg, "Where did we stop?") {
		t.Fatal("the same sentence right away is a repeat")
	}
	if repeated(cfg, "And the call to action?") {
		t.Fatal("a different sentence is not a repeat")
	}
}

// state.json is a file in the repository. Whatever paths it lists, pair
// reads, copies and reports only files of the repository.
func TestPathsOutsideTheRepositoryAreIgnored(t *testing.T) {
	h, root := newRepo(t)
	prompt(t, h) // baseline

	outside := filepath.Join(t.TempDir(), "id_notes.txt")
	write(t, outside, "something private, outside the repository\n")
	link := filepath.Join(root, "linked.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	st := loadState(h.Cfg.StateDir)
	st.Files[outside] = "0000"                                            // forged: "Claude saw this file"
	st.Files[filepath.Join(root, "..", filepath.Base(root)+"x")] = "0000" // a sibling of the repository
	st.Files[filepath.Join(h.Cfg.StateDir, "events.jsonl")] = "0000"      // pair's own files
	if err := saveState(h.Cfg.StateDir, st); err != nil {
		t.Fatal(err)
	}

	ctx := prompt(t, h)
	if strings.Contains(ctx, "something private") || strings.Contains(ctx, "id_notes") || strings.Contains(ctx, "events.jsonl") {
		t.Fatalf("a file outside the repository reached the context:\n%s", ctx)
	}
	if strings.Contains(ctx, "linked.txt") {
		t.Fatalf("a link leading out of the repository was followed:\n%s", ctx)
	}
	snaps, _ := filepath.Glob(filepath.Join(h.Cfg.StateDir, snapDir, "*"))
	for _, s := range snaps {
		if data, _ := os.ReadFile(s); strings.Contains(string(data), "something private") {
			t.Fatalf("a file outside the repository was copied to %s", s)
		}
	}

	// PostEdit on a file outside the repository keeps no copy of it either.
	if err := h.PostEdit(edit(outside)); err != nil {
		t.Fatal(err)
	}
	if _, tracked := loadState(h.Cfg.StateDir).Files[outside]; tracked {
		t.Fatal("a file outside the repository is tracked")
	}
}

func TestSecretsAreReportedByNameOnly(t *testing.T) {
	h, root := newRepo(t)
	env := filepath.Join(root, ".env")
	key := filepath.Join(root, "config", "master.key")
	_ = os.MkdirAll(filepath.Dir(key), 0o755)
	write(t, env, "STRIPE_KEY=sk_live_before\n")
	prompt(t, h) // baseline: .env is untracked, so it is a candidate

	write(t, env, "STRIPE_KEY=sk_live_AFTER\n")
	write(t, key, "0123456789abcdef-the-master-key\n")
	ctx := prompt(t, h)
	if !strings.Contains(ctx, ".env") || !strings.Contains(ctx, "withheld") {
		t.Fatalf("the change must be reported by name:\n%s", ctx)
	}
	if strings.Contains(ctx, "sk_live") || strings.Contains(ctx, "the-master-key") {
		t.Fatalf("a secret reached the model's context:\n%s", ctx)
	}
	snaps, _ := filepath.Glob(filepath.Join(h.Cfg.StateDir, snapDir, "*"))
	for _, s := range snaps {
		data, _ := os.ReadFile(s)
		if strings.Contains(string(data), "sk_live") || strings.Contains(string(data), "the-master-key") {
			t.Fatalf("a secret was copied to %s", s)
		}
	}
	// Reported once, like any other change.
	if again := prompt(t, h); again != "" {
		t.Fatalf("reported twice:\n%s", again)
	}

	for name, want := range map[string]bool{
		".env": true, ".env.production": true, "server.pem": true, "id_ed25519": true, "aws_credentials.json": true,
		"secrets.yml": true, "app.rb": false, "environment.rb": false, "keyboard.go": false, "README.md": false,
	} {
		if secretName(name) != want {
			t.Errorf("secretName(%q) = %v", name, !want)
		}
	}
}

func TestFilesTheRepositoryIgnoresAreWithheld(t *testing.T) {
	h, root := newRepo(t)
	write(t, filepath.Join(root, ".gitignore"), ".pair/\nPAIRING.md\nlocal.yml\n")
	local := filepath.Join(root, "local.yml")
	write(t, local, "token: before\n")
	prompt(t, h)
	// Claude edited the ignored file, then the engineer did.
	if err := h.PostEdit(&agent.Event{Hook: "post_edit", Tool: "Write", Kind: agent.KindWrite, Files: []agent.File{{Path: local, Content: "token: before\n"}}}); err != nil {
		t.Fatal(err)
	}
	if fileExists(snapPath(h.Cfg.StateDir, local)) {
		t.Fatal("an ignored file was copied")
	}
	write(t, local, "token: after\n")
	ctx := prompt(t, h)
	if !strings.Contains(ctx, "local.yml") || strings.Contains(ctx, "token:") {
		t.Fatalf("ignored file:\n%s", ctx)
	}

	// The scratchpad is ignored by choice, and is the exception.
	write(t, filepath.Join(root, "PAIRING.md"), "# PAIRING\n\n## Decisions\n- use Sidekiq\n")
	if ctx = prompt(t, h); !strings.Contains(ctx, "+- use Sidekiq") {
		t.Fatalf("the scratchpad must be shown in full:\n%s", ctx)
	}
}

func TestFileNamesThatAreNotASCII(t *testing.T) {
	h, root := newRepo(t)
	prompt(t, h)
	name := "relatório de março.md"
	write(t, filepath.Join(root, name), "primeira linha\n")
	ctx := prompt(t, h)
	if !strings.Contains(ctx, "### "+name) || !strings.Contains(ctx, "+primeira linha") {
		t.Fatalf("the file was not found under its own name:\n%s", ctx)
	}
}

func TestDiffLinesThatLookLikeHeadersAreKept(t *testing.T) {
	h, root := newRepo(t)
	sql := filepath.Join(root, "query.sql")
	write(t, sql, "-- old comment\nSELECT 1;\n++ odd line\n")
	if out, err := execGit(root, "add", "."); err != nil {
		t.Fatal(err, out)
	}
	if out, err := execGit(root, "commit", "-q", "-m", "sql"); err != nil {
		t.Fatal(err, out)
	}
	prompt(t, h)
	write(t, sql, "-- new comment\nSELECT 1;\n++ odder line\n")
	ctx := prompt(t, h)
	for _, want := range []string{"--- old comment", "+-- new comment", "-++ odd line", "+++ odder line"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("the diff lost %q:\n%s", want, ctx)
		}
	}
	if strings.Contains(ctx, snapDir+"/") || strings.Contains(ctx, h.Cfg.StateDir) {
		t.Fatalf("a snapshot path reached the context:\n%s", ctx)
	}
	if got := trimDiffHeader("diff --git a/x b/x\nBinary files differ\n", "x"); got != "" {
		t.Fatalf("no hunk, no body: %q", got)
	}
}

func TestLargeFilesAreNotReadAtEveryPrompt(t *testing.T) {
	h, root := newRepo(t)
	big := filepath.Join(root, "dump.sql")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), maxSnap+10), 0o644); err != nil {
		t.Fatal(err)
	}
	id, data, large, err := identity(big)
	if err != nil || !large || data != nil || !strings.HasPrefix(id, "large:") {
		t.Fatalf("a large file is identified without being read: %q %d %v %v", id, len(data), large, err)
	}
	prompt(t, h)
	if fileExists(snapPath(h.Cfg.StateDir, big)) {
		t.Fatal("a large file was copied")
	}
	if ctx := prompt(t, h); ctx != "" {
		t.Fatalf("nothing changed:\n%s", ctx)
	}
	f, _ := os.OpenFile(big, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("more")
	_ = f.Close()
	if ctx := prompt(t, h); !strings.Contains(ctx, "dump.sql") || !strings.Contains(ctx, "too large") {
		t.Fatalf("a change to a large file is reported by name:\n%s", ctx)
	}
	if _, _, _, err := identity(root); err == nil {
		t.Fatal("a directory is not a file")
	}
}

func TestWhatPairKeepsIsPrivate(t *testing.T) {
	h, root := newRepo(t)
	write(t, filepath.Join(root, "app.rb"), "class App\nend\n")
	prompt(t, h)
	if err := h.PostEdit(edit(filepath.Join(root, "app.rb"))); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"state.json", "events.jsonl", "session.log", filepath.Join(snapDir, filepath.Base(snapPath("", filepath.Join(root, "app.rb"))))} {
		fi, err := os.Stat(filepath.Join(h.Cfg.StateDir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is readable by others: %v", name, fi.Mode().Perm())
		}
	}
	events.Private(h.Cfg.StateDir)
	if fi, _ := os.Stat(h.Cfg.StateDir); fi.Mode().Perm() != 0o700 {
		t.Errorf(".pair is open to others: %v", fi.Mode().Perm())
	}
	if out, err := execGit(root, "check-ignore", "-q", ".pair/last_input.wav"); err != nil {
		t.Errorf(".pair must be ignored by git on its own: %v %s", err, out)
	}
}
