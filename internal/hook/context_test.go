package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if err := h.Prompt(&Input{Prompt: "next"}); err != nil {
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
	if err := h.PostEdit(&Input{ToolName: "Edit", ToolInput: ToolInput{FilePath: app}}); err != nil {
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
