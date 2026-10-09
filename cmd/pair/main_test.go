package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fabioaraujo121/pair/internal/testenv"
)

func TestMain(m *testing.M) {
	cleanup := testenv.Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func TestNothingThatGoesWrongInAHookGetsOut(t *testing.T) {
	var report bytes.Buffer
	guard(func() error { panic("index out of range") }, &report)
	guard(func() error { return errors.New("no editor") }, &report)
	guard(func() error { var m map[string]int; m["x"] = 1; return nil }, &report)
	if report.Len() != 0 {
		t.Fatalf("silent unless asked: %q", report.String())
	}
	t.Setenv("PAIR_DEBUG", "1")
	guard(func() error { panic("index out of range") }, &report)
	guard(func() error { return errors.New("no editor") }, &report)
	if got := report.String(); !strings.Contains(got, "panic: index out of range") || !strings.Contains(got, "no editor") {
		t.Fatalf("PAIR_DEBUG must report: %q", got)
	}
}

// Claude Code reads a hook's exit code: 2 blocks the tool call. Whatever a
// hook is given, the binary exits 0 and prints nothing that is not meant
// for Claude Code.
func TestHooksAlwaysExitZero(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	bin := filepath.Join(t.TempDir(), "pair")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	inputs := map[string]string{
		"nothing":        "",
		"not JSON":       "\x00\xff{{{ not json",
		"wrong types":    `{"tool_input": 42, "cwd": ["x"], "tool_response": "text"}`,
		"no such file":   `{"cwd": "` + repo + `", "tool_name": "Edit", "tool_input": {"file_path": "/no/such/file.rb", "old_string": "x"}}`,
		"no such cwd":    `{"cwd": "/no/such/directory", "prompt": "hello"}`,
		"negative lines": `{"cwd": "` + repo + `", "tool_name": "Edit", "tool_input": {"file_path": "` + repo + `/a.rb"}, "tool_response": {"structuredPatch": [{"oldStart": -5, "oldLines": -1, "newStart": -5, "newLines": 99999999, "lines": ["+x"]}]}}`,
	}
	for _, hook := range []string{"session", "pre_edit", "post_edit", "prompt", "stop", "notify", "activity", "no_such_hook"} {
		for what, input := range inputs {
			for _, active := range []string{"PAIR_ACTIVE=1", "PAIR_ACTIVE="} {
				cmd := exec.Command(bin, "hook", hook)
				cmd.Dir = repo
				cmd.Env = append(os.Environ(), active, "PAIR_SOCK="+filepath.Join(repo, "no.sock"), "PAIR_MUTE=1")
				cmd.Stdin = strings.NewReader(input)
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				if err := cmd.Run(); err != nil {
					t.Errorf("pair hook %s (%s, %s): %v\n%s", hook, what, active, err, stderr.String())
				}
				if stderr.Len() != 0 {
					t.Errorf("pair hook %s (%s, %s) wrote to stderr: %s", hook, what, active, stderr.String())
				}
				if out := strings.TrimSpace(stdout.String()); out != "" && !strings.HasPrefix(out, "{") {
					t.Errorf("pair hook %s (%s, %s) printed %q", hook, what, active, out)
				}
			}
		}
	}
	// Outside a pairing session a hook leaves no trace in the repository.
	outside := t.TempDir()
	cmd := exec.Command(bin, "hook", "prompt")
	cmd.Dir = outside
	cmd.Env = append(os.Environ(), "PAIR_ACTIVE=")
	cmd.Stdin = strings.NewReader(`{"cwd": "` + outside + `", "prompt": "hello"}`)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, ".pair")); err == nil {
		t.Fatal("a hook outside a pairing session wrote to the repository")
	}
}
