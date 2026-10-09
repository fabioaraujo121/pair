package hook

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fabioaraujo121/pair/internal/agent"
	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/events"
	"github.com/fabioaraujo121/pair/internal/nvim"
)

// Verbatim PreToolUse Edit stdin captured from Claude Code 2.1.282
// (validation item 1), path shortened.
const editJSON = `{"session_id":"ad2fdeeb-812c-487b-b6fd-bd853f8d17f0","transcript_path":"/Users/x/.claude/projects/p/ad2fdeeb.jsonl","cwd":"/tmp/proj","prompt_id":"83a13549-c692-45c5-b5e1-6a5862d76019","permission_mode":"acceptEdits","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/tmp/proj/sample.txt","old_string":"beta line two","new_string":"BETA LINE TWO","replace_all":false},"tool_use_id":"toolu_01KQPnYchuLFoLnTR4ND6oDL"}`

const writeJSON = `{"session_id":"s","cwd":"/tmp/proj","hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"/tmp/proj/new.txt","content":"hello\n"},"tool_use_id":"t"}`

// parse reads Claude Code's hook JSON through its adapter.
func parse(t *testing.T, hook, js string) *agent.Event {
	t.Helper()
	ev, err := agent.Claude{}.ParseHook(hook, []byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestParseEdit(t *testing.T) {
	ev := parse(t, "pre_edit", editJSON)
	if ev.Tool != "Edit" || ev.CWD != "/tmp/proj" || ev.Kind != agent.KindEdit || ev.Conversation != "ad2fdeeb-812c-487b-b6fd-bd853f8d17f0" {
		t.Fatalf("top-level fields: %+v", ev)
	}
	if len(ev.Files) != 1 || ev.Files[0].Path != "/tmp/proj/sample.txt" || ev.Files[0].Old != "beta line two" || ev.Files[0].New != "BETA LINE TWO" {
		t.Fatalf("files: %+v", ev.Files)
	}
}

func TestParseWriteAndEmpty(t *testing.T) {
	ev := parse(t, "pre_edit", writeJSON)
	if ev.Tool != "Write" || ev.Kind != agent.KindWrite || len(ev.Files) != 1 || ev.Files[0].Content != "hello\n" {
		t.Fatalf("write: %+v", ev)
	}
	if ev := parse(t, "prompt", ""); ev == nil || ev.Hook != "prompt" {
		t.Fatalf("empty stdin must parse to an empty event: %+v", ev)
	}
	if _, err := (agent.Claude{}).ParseHook("prompt", []byte("{not json")); err == nil {
		t.Fatal("invalid JSON must error")
	}
}

func TestLocateRange(t *testing.T) {
	content := "alpha\nbeta line two\ngamma\ndelta\n"
	cases := []struct {
		old        string
		start, end int
		found      bool
	}{
		{"beta line two", 2, 2, true},
		{"alpha", 1, 1, true},
		{"gamma\ndelta", 3, 4, true},
		{"gamma\ndelta\n", 3, 4, true},
		{"beta line two\ngamma\ndelta\n", 2, 4, true},
		{"missing", 1, 1, false},
		{"", 1, 1, false},
	}
	for _, c := range cases {
		s, e, f := LocateRange(content, c.old)
		if s != c.start || e != c.end || f != c.found {
			t.Errorf("LocateRange(%q) = %d,%d,%v want %d,%d,%v", c.old, s, e, f, c.start, c.end, c.found)
		}
	}
}

// edit is the event of an Edit tool call on file, without patch data.
func edit(file string) *agent.Event {
	return &agent.Event{Hook: "post_edit", Tool: "Edit", Kind: agent.KindEdit, Files: []agent.File{{Path: file}}}
}

type fakeNvim struct {
	shows   []nvim.Shown
	reloads int
	diffs   []string
}

func (f *fakeNvim) Diff(_ context.Context, file, marks string) error {
	f.diffs = append(f.diffs, file+" "+marks)
	return nil
}

func (f *fakeNvim) Show(_ context.Context, file string, s, e int) (nvim.Shown, error) {
	sh := nvim.Shown{File: file, Start: s, End: e}
	f.shows = append(f.shows, sh)
	return sh, nil
}
func (f *fakeNvim) Reload(context.Context) error { f.reloads++; return nil }

func TestPreEditPointsAtOldString(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(file, []byte("alpha\nbeta line two\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fn := &fakeNvim{}
	h := &Handler{Cfg: config.Config{Root: root, StateDir: filepath.Join(root, ".pair")}, Log: events.Open(filepath.Join(root, ".pair")), Nvim: fn}

	in := &agent.Event{Hook: "pre_edit", Tool: "Edit", Kind: agent.KindEdit, Files: []agent.File{{Path: "sample.txt", Old: "beta line two"}}}
	if err := h.PreEdit(in); err != nil {
		t.Fatal(err)
	}
	if len(fn.shows) != 1 || fn.shows[0].File != file || fn.shows[0].Start != 2 || fn.shows[0].End != 2 {
		t.Fatalf("shows = %+v", fn.shows)
	}

	// Write to a file that does not exist yet: nothing to point at, no RPC.
	in = &agent.Event{Hook: "pre_edit", Tool: "Write", Kind: agent.KindWrite, Files: []agent.File{{Path: filepath.Join(root, "new.txt"), Content: "x"}}}
	if err := h.PreEdit(in); err != nil {
		t.Fatal(err)
	}
	if len(fn.shows) != 1 {
		t.Fatalf("Write of a new file must not call Show: %+v", fn.shows)
	}

	data, _ := os.ReadFile(filepath.Join(root, ".pair", "events.jsonl"))
	if got := strings.Count(string(data), "\n"); got != 2 {
		t.Fatalf("want 2 events, got %d:\n%s", got, data)
	}
	log, _ := os.ReadFile(filepath.Join(root, ".pair", "session.log"))
	if !strings.Contains(string(log), "→ sample.txt:2-2") || !strings.Contains(string(log), "+ new.txt (new file)") {
		t.Fatalf("session.log:\n%s", log)
	}
}

func TestPostEditReloadsAndRendersDiff(t *testing.T) {
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
	file := filepath.Join(root, "sample.txt")
	_ = os.WriteFile(file, []byte("one\ntwo\n"), 0o644)
	run("add", "sample.txt")
	run("commit", "-q", "-m", "init")
	_ = os.WriteFile(file, []byte("one\nTWO\n"), 0o644)

	fn := &fakeNvim{}
	h := &Handler{Cfg: config.Config{Root: root, StateDir: filepath.Join(root, ".pair"), Session: "no-such-session"}, Log: events.Open(filepath.Join(root, ".pair")), Nvim: fn}
	if err := h.PostEdit(edit(file)); err != nil {
		t.Fatal(err)
	}
	if fn.reloads != 1 {
		t.Fatalf("reloads = %d", fn.reloads)
	}
	log, _ := os.ReadFile(filepath.Join(root, ".pair", "session.log"))
	if !strings.Contains(string(log), "✎ sample.txt (+1 −1)") {
		t.Fatalf("summary not written to session.log:\n%s", log)
	}
	ev, _ := os.ReadFile(filepath.Join(root, ".pair", "events.jsonl"))
	if !strings.Contains(string(ev), `"added":1`) || !strings.Contains(string(ev), `"removed":1`) {
		t.Fatalf("counts missing from the event:\n%s", ev)
	}

	// Untracked file: diffed against /dev/null.
	newFile := filepath.Join(root, "fresh.txt")
	_ = os.WriteFile(newFile, []byte("brand new\n"), 0o644)
	if err := h.PostEdit(&agent.Event{Hook: "post_edit", Tool: "Write", Kind: agent.KindWrite, Files: []agent.File{{Path: newFile, Content: "brand new\n"}}}); err != nil {
		t.Fatal(err)
	}
	log, _ = os.ReadFile(filepath.Join(root, ".pair", "session.log"))
	if !strings.Contains(string(log), "✎ fresh.txt (+1 −0)") {
		t.Fatalf("new file not summarized:\n%s", log)
	}
}

func TestActivityDescribesWhatClaudeIsDoing(t *testing.T) {
	root := t.TempDir()
	h := &Handler{Cfg: config.Config{Root: root, StateDir: filepath.Join(root, ".pair")}, Log: events.Open(filepath.Join(root, ".pair")), Nvim: &fakeNvim{}}
	for _, js := range []string{
		`{"tool_name":"Read","tool_input":{"file_path":"` + root + `/app/x.rb"}}`,
		`{"tool_name":"Bash","tool_input":{"command":"bin/rails test","description":"Run the test suite"}}`,
		`{"tool_name":"Grep","tool_input":{"pattern":"valid\\?"}}`,
		`{"tool_name":"mcp__pair__narrate","tool_input":{}}`,
	} {
		if err := h.Activity(parse(t, "activity", js)); err != nil {
			t.Fatal(err)
		}
	}
	// It reports to the status bar only: nothing is logged.
	if _, err := os.Stat(filepath.Join(root, ".pair", "events.jsonl")); err == nil {
		t.Fatal("the activity hook must not write events")
	}
}

func TestNotifyRaisesAttentionForPermissionPrompts(t *testing.T) {
	root := t.TempDir()
	h := &Handler{Cfg: config.Config{Root: root, StateDir: filepath.Join(root, ".pair")}, Log: events.Open(filepath.Join(root, ".pair")), Nvim: &fakeNvim{}}
	in := parse(t, "notify", `{"hook_event_name":"Notification","message":"Claude needs your permission to use Bash","notification_type":"permission_prompt"}`)
	if in.Message == "" || in.Idle {
		t.Fatalf("notification fields not parsed: %+v", in)
	}
	// An idle reminder is logged as an event but raises nothing.
	_ = h.Notify(parse(t, "notify", `{"message":"Claude is waiting for your input","notification_type":"idle_prompt"}`))
	if log, _ := os.ReadFile(filepath.Join(root, ".pair", "session.log")); strings.Contains(string(log), "needs you") {
		t.Fatalf("idle reminder must not alert:\n%s", log)
	}
}

func TestMarksFromClaudeCodePatch(t *testing.T) {
	// A change in the middle of a file: two lines replaced by three.
	hunks := []Hunk{{OldStart: 8, OldLines: 5, NewStart: 8, NewLines: 6, Lines: []string{
		" ", "   def valid?", "-    !name.empty?", "-    true", "+    return false if name.empty?", "+", "+    email.include?(\"@\")", "   end",
	}}}
	m := MarksFromHunks(hunks)
	if len(m.Added) != 3 || m.Added[0] != 10 || m.Added[2] != 12 {
		t.Fatalf("added = %v", m.Added)
	}
	if len(m.Removed) != 1 || m.Removed[0].At != 10 || len(m.Removed[0].Lines) != 2 || m.Removed[0].Lines[0] != "    !name.empty?" {
		t.Fatalf("removed = %+v", m.Removed)
	}
	if m.summary() != "+3 −2" {
		t.Fatalf("summary = %s", m.summary())
	}
	// Pure deletion at the end of a hunk.
	m = MarksFromHunks([]Hunk{{NewStart: 3, Lines: []string{" keep", "-gone", "-gone too"}}})
	if len(m.Added) != 0 || len(m.Removed) != 1 || m.Removed[0].At != 4 {
		t.Fatalf("deletion: %+v", m)
	}
}

func TestParseUnifiedDiff(t *testing.T) {
	diff := "diff --git a/x b/x\nindex 1..2 100644\n--- a/x\n+++ b/x\n@@ -2 +2,2 @@ ctx\n-two\n+TWO\n+two and a half\n@@ -9,0 +11 @@\n+new tail\n\\ No newline at end of file\n"
	hunks := ParseUnified(diff)
	if len(hunks) != 2 || hunks[0].NewStart != 2 || hunks[1].NewStart != 11 {
		t.Fatalf("hunks = %+v", hunks)
	}
	m := MarksFromHunks(hunks)
	if len(m.Added) != 3 || m.Added[0] != 2 || m.Added[1] != 3 || m.Added[2] != 11 || len(m.Removed) != 1 || m.Removed[0].At != 2 {
		t.Fatalf("marks = %+v", m)
	}
}

func TestPostEditDrawsTheChangeInTheEditor(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "user.rb")
	_ = os.WriteFile(file, []byte("class User\n  def valid?\n    email.include?(\"@\")\n  end\nend\n"), 0o644)
	fn := &fakeNvim{}
	h := &Handler{Cfg: config.Config{Root: root, StateDir: filepath.Join(root, ".pair")}, Log: events.Open(filepath.Join(root, ".pair")), Nvim: fn}
	in := parse(t, "post_edit", `{"tool_name":"Edit","tool_input":{"file_path":"`+file+`"},"tool_response":{"filePath":"`+file+`","structuredPatch":[{"oldStart":1,"oldLines":5,"newStart":1,"newLines":5,"lines":[" class User","   def valid?","-    true","+    email.include?(\"@\")","   end"," end"]}]}}`)
	if err := h.PostEdit(in); err != nil {
		t.Fatal(err)
	}
	if len(fn.diffs) != 1 || !strings.Contains(fn.diffs[0], `"added":[3]`) || !strings.Contains(fn.diffs[0], `"at":3`) || !strings.Contains(fn.diffs[0], "    true") {
		t.Fatalf("diff sent to the editor: %v", fn.diffs)
	}
	ev, _ := os.ReadFile(filepath.Join(root, ".pair", "events.jsonl"))
	if !strings.Contains(string(ev), `"drawn":"+1 −1"`) {
		t.Fatalf("event:\n%s", ev)
	}

	// A new file created by Write: every line is new.
	fresh := filepath.Join(root, "fresh.rb")
	_ = os.WriteFile(fresh, []byte("a\nb\nc\n"), 0o644)
	in = parse(t, "post_edit", `{"tool_name":"Write","tool_input":{"file_path":"`+fresh+`"},"tool_response":{"type":"create","filePath":"`+fresh+`","structuredPatch":[]}}`)
	_ = h.PostEdit(in)
	if len(fn.diffs) != 2 || !strings.Contains(fn.diffs[1], `"added":[1,2,3]`) {
		t.Fatalf("new file: %v", fn.diffs)
	}
}

func TestHooksDoNothingOutsideAPairSession(t *testing.T) {
	root := t.TempDir()
	_ = os.Mkdir(filepath.Join(root, ".git"), 0o755)
	input := `{"session_id":"other-conversation","cwd":"` + root + `","hook_event_name":"UserPromptSubmit","prompt":"hello"}`

	t.Setenv("PAIR_ACTIVE", "")
	var out strings.Builder
	if err := Run("prompt", "", strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".pair")); err == nil {
		t.Fatal("an agent started outside pair must leave no trace")
	}

	t.Setenv("PAIR_ACTIVE", "1")
	t.Setenv("PAIR_SESSION", "no-such-session")
	if err := Run("prompt", "claude", strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".pair", "events.jsonl")); err != nil {
		t.Fatal("inside a session the prompt is logged")
	}
}

// Codex changes several files in one call. The first is pointed at, every
// one is drawn and logged, a removed file is only logged.
func TestAnEditOfSeveralFiles(t *testing.T) {
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
	user, old := filepath.Join(root, "user.rb"), filepath.Join(root, "old.rb")
	_ = os.WriteFile(user, []byte("class User\n  def valid?\n    true\n  end\nend\n"), 0o644)
	_ = os.WriteFile(old, []byte("old\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "init")

	patch := "*** Begin Patch\n*** Update File: user.rb\n@@ def valid?\n   def valid?\n-    true\n+    email.include?(\"@\")\n   end\n*** Add File: email.rb\n+class Email\n+end\n*** Delete File: old.rb\n*** End Patch\n"
	before := parseAs(t, agent.Codex{}, "pre_edit", `{"tool_name":"apply_patch","cwd":"`+root+`","tool_input":{"command":`+jsonString(patch)+`}}`)
	fn := &fakeNvim{}
	h := &Handler{Cfg: config.Config{Root: root, StateDir: filepath.Join(root, ".pair"), Agent: "codex"}, Log: events.Open(filepath.Join(root, ".pair")), Nvim: fn, Agent: agent.Codex{}}
	if err := h.PreEdit(before); err != nil {
		t.Fatal(err)
	}
	if len(fn.shows) != 1 || fn.shows[0].File != user || fn.shows[0].Start != 2 || fn.shows[0].End != 4 {
		t.Fatalf("the first file is pointed at, by the text of its first hunk: %+v", fn.shows)
	}
	log, _ := os.ReadFile(filepath.Join(root, ".pair", "session.log"))
	for _, want := range []string{"→ user.rb:2-4", "+ email.rb (new file)", "− old.rb (removed)"} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("session.log lacks %q:\n%s", want, log)
		}
	}

	// The agent applied it.
	_ = os.WriteFile(user, []byte("class User\n  def valid?\n    email.include?(\"@\")\n  end\nend\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "email.rb"), []byte("class Email\nend\n"), 0o644)
	_ = os.Remove(old)
	after := parseAs(t, agent.Codex{}, "post_edit", `{"tool_name":"apply_patch","cwd":"`+root+`","tool_input":{"command":`+jsonString(patch)+`},"tool_response":"Success. Updated the following files:\nM user.rb\nA email.rb\nD old.rb"}`)
	if err := h.PostEdit(after); err != nil {
		t.Fatal(err)
	}
	if fn.reloads != 1 || len(fn.diffs) != 2 {
		t.Fatalf("one reload, one drawing per file that exists: %d %v", fn.reloads, fn.diffs)
	}
	if !strings.Contains(fn.diffs[0], `"added":[3]`) || !strings.Contains(fn.diffs[1], `"added":[1,2]`) {
		t.Fatalf("drawings: %v", fn.diffs)
	}
	log, _ = os.ReadFile(filepath.Join(root, ".pair", "session.log"))
	for _, want := range []string{"✎ user.rb (+1 −1)", "✎ email.rb (+2 −0)", "− old.rb (removed)"} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("session.log lacks %q:\n%s", want, log)
		}
	}
	// Both files are remembered as the agent left them.
	st := loadState(h.Cfg.StateDir)
	if _, ok := st.Files[user]; !ok {
		t.Fatal("user.rb not snapshotted")
	}
	if _, ok := st.Files[filepath.Join(root, "email.rb")]; !ok {
		t.Fatal("email.rb not snapshotted")
	}
}

func parseAs(t *testing.T, a agent.Agent, hook, js string) *agent.Event {
	t.Helper()
	ev, err := a.ParseHook(hook, []byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func jsonString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

// The pairing instructions reach the agent through its session hook, as
// context, and only inside a pair session: nothing is written into the
// repository for them.
func TestTheSessionHookCarriesTheInstructions(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	h := &Handler{Cfg: config.Config{Root: root, StateDir: filepath.Join(root, ".pair"), Lang: "pt"}, Log: events.Open(filepath.Join(root, ".pair")), Nvim: &fakeNvim{}, Agent: agent.Codex{}, Stdout: &out}
	if err := h.Session(&agent.Event{Hook: "session", Conversation: "s1"}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, `"hookEventName":"SessionStart"`) || !strings.Contains(text, "Speak Portuguese") || !strings.Contains(text, ".pair/PAIRING.md") {
		t.Fatalf("session output:\n%s", text)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", ".claude", ".codex"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			t.Fatalf("%s was written", name)
		}
	}
}
