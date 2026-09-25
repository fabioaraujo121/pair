package hook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/events"
	"github.com/fabioaraujo121/pair/internal/nvim"
)

// Verbatim PreToolUse Edit stdin captured from Claude Code 2.1.282
// (validation item 1), path shortened.
const editJSON = `{"session_id":"ad2fdeeb-812c-487b-b6fd-bd853f8d17f0","transcript_path":"/Users/x/.claude/projects/p/ad2fdeeb.jsonl","cwd":"/tmp/proj","prompt_id":"83a13549-c692-45c5-b5e1-6a5862d76019","permission_mode":"acceptEdits","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/tmp/proj/sample.txt","old_string":"beta line two","new_string":"BETA LINE TWO","replace_all":false},"tool_use_id":"toolu_01KQPnYchuLFoLnTR4ND6oDL"}`

const writeJSON = `{"session_id":"s","cwd":"/tmp/proj","hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"/tmp/proj/new.txt","content":"hello\n"},"tool_use_id":"t"}`

func TestParseEdit(t *testing.T) {
	in, err := Parse(strings.NewReader(editJSON))
	if err != nil {
		t.Fatal(err)
	}
	if in.ToolName != "Edit" || in.CWD != "/tmp/proj" || in.HookEventName != "PreToolUse" {
		t.Fatalf("top-level fields: %+v", in)
	}
	ti := in.ToolInput
	if ti.FilePath != "/tmp/proj/sample.txt" || ti.OldString != "beta line two" || ti.NewString != "BETA LINE TWO" || ti.ReplaceAll {
		t.Fatalf("tool_input: %+v", ti)
	}
}

func TestParseWriteAndEmpty(t *testing.T) {
	in, err := Parse(strings.NewReader(writeJSON))
	if err != nil {
		t.Fatal(err)
	}
	if in.ToolName != "Write" || in.ToolInput.Content != "hello\n" {
		t.Fatalf("write: %+v", in)
	}
	if in, err := Parse(strings.NewReader("")); err != nil || in == nil {
		t.Fatalf("empty stdin must parse to an empty input: %v", err)
	}
	if _, err := Parse(strings.NewReader("{not json")); err == nil {
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

type fakeNvim struct {
	shows   []nvim.Shown
	reloads int
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

	in := &Input{ToolName: "Edit", ToolInput: ToolInput{FilePath: "sample.txt", OldString: "beta line two"}}
	if err := h.PreEdit(in); err != nil {
		t.Fatal(err)
	}
	if len(fn.shows) != 1 || fn.shows[0].File != file || fn.shows[0].Start != 2 || fn.shows[0].End != 2 {
		t.Fatalf("shows = %+v", fn.shows)
	}

	// Write to a file that does not exist yet: nothing to point at, no RPC.
	in = &Input{ToolName: "Write", ToolInput: ToolInput{FilePath: filepath.Join(root, "new.txt"), Content: "x"}}
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
	if err := h.PostEdit(&Input{ToolName: "Edit", ToolInput: ToolInput{FilePath: file}}); err != nil {
		t.Fatal(err)
	}
	if fn.reloads != 1 {
		t.Fatalf("reloads = %d", fn.reloads)
	}
	log, _ := os.ReadFile(filepath.Join(root, ".pair", "session.log"))
	if !strings.Contains(string(log), "TWO") {
		t.Fatalf("diff not rendered into session.log:\n%s", log)
	}

	// Untracked file: diffed against /dev/null.
	newFile := filepath.Join(root, "fresh.txt")
	_ = os.WriteFile(newFile, []byte("brand new\n"), 0o644)
	if err := h.PostEdit(&Input{ToolName: "Write", ToolInput: ToolInput{FilePath: newFile}}); err != nil {
		t.Fatal(err)
	}
	log, _ = os.ReadFile(filepath.Join(root, ".pair", "session.log"))
	if !strings.Contains(string(log), "brand new") {
		t.Fatalf("untracked diff not rendered:\n%s", log)
	}
}
