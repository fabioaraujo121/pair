package cli

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fabioaraujo121/pair/internal/agent"
	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/session"
)

// What pair types into a pane is read by an interactive shell. Whatever a
// value holds, the shell must give back that value and do nothing else.
func TestShellQuoteSurvivesAShell(t *testing.T) {
	dir := t.TempDir()
	canary := filepath.Join(dir, "canary")
	values := []string{
		"plain", "with space", "it's", `say "hi"`, "$(touch " + canary + ")", "`touch " + canary + "`",
		"a;touch " + canary, "a|b", "a&b", "!!", "{a,b}", "~", "*", "a\nb", "--name=x", "-rf", "é", "",
	}
	for _, v := range values {
		if v == "" {
			if shellQuote(v) != "''" {
				t.Errorf("the empty string must stay an argument: %q", shellQuote(v))
			}
			continue
		}
		out, err := exec.Command("/bin/sh", "-c", "printf %s "+shellQuote(v)).Output()
		if err != nil || string(out) != v {
			t.Errorf("shellQuote(%q) = %s gave back %q (%v)", v, shellQuote(v), out, err)
		}
	}
	if _, err := os.Stat(canary); err == nil {
		t.Fatal("a quoted value was run as a command")
	}
	for _, plain := range []string{"/usr/local/bin/pair", "PAIR_ACTIVE=1", "a-b_c.d"} {
		if shellQuote(plain) != plain {
			t.Errorf("%q needs no quotes, got %s", plain, shellQuote(plain))
		}
	}
}

func TestClaudeArgumentsComeFromFilesAndAreNotTrusted(t *testing.T) {
	cfg := config.Config{Root: t.TempDir(), RunDir: filepath.Join(t.TempDir(), "run"), AskWait: 180, AskMax: 60}
	ag := agent.Claude{}
	id := "0b6f4c1e-8a0d-4e55-9c1f-3a2b1c0d9e8f"
	args := func(m session.Meta, continued bool) string {
		t.Helper()
		got, err := agentArgs(cfg, ag, "/opt/pair", m, continued)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	session_ := " --settings " + filepath.Join(cfg.RunDir, "claude-settings.json") + " --mcp-config " + filepath.Join(cfg.RunDir, "claude-mcp.json")

	if got := args(session.Meta{Conversation: id}, true); got != session_+" --resume "+id {
		t.Fatalf("a conversation id must be resumed: %q", got)
	}
	// session.json is a file in the repository: anything can be in it.
	for _, forged := range []string{"x; touch /tmp/owned", "--dangerously-skip-permissions", "$(id)", id + " --extra", ""} {
		got := args(session.Meta{Conversation: forged}, true)
		if got != session_+" --continue" {
			t.Errorf("claude_session_id %q reached the command line: %q", forged, got)
		}
	}
	if got := args(session.Meta{Conversation: id}, false); got != session_ {
		t.Fatalf("a new session resumes nothing: %q", got)
	}
	// The session files are written, outside the repository.
	if _, err := os.Stat(filepath.Join(cfg.RunDir, "claude-settings.json")); err != nil {
		t.Fatal("the settings file was not written")
	}
	if entries, _ := os.ReadDir(cfg.Root); len(entries) != 0 {
		t.Fatalf("the repository was written to: %v", entries)
	}

	// The name is one argument, whatever it holds.
	for _, name := range []string{"fix the login", "--dangerously-skip-permissions", "it's; rm -rf ~", "$(id)"} {
		got := args(session.Meta{Name: name}, false)
		out, err := exec.Command("/bin/sh", "-c", `for a in`+got+`; do printf '%s\n' "$a"; done`).Output()
		if err != nil {
			t.Fatalf("%q: %v", got, err)
		}
		lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
		if lines[len(lines)-1] != "--name="+name {
			t.Errorf("name %q became the arguments %q", name, lines)
		}
	}

	cfg.ClaudeArgs = "--model opus"
	if got := args(session.Meta{}, false); got != session_+" --model opus" {
		t.Fatalf("the engineer's own arguments: %q", got)
	}
}

func TestOnlyASocketIsRemoved(t *testing.T) {
	// A short directory: a unix socket path has a small limit.
	dir, err := os.MkdirTemp("/tmp", "pairsock")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	file := filepath.Join(dir, "notes.txt")
	_ = os.WriteFile(file, []byte("the engineer's notes"), 0o644)
	removeSocket(file)
	if _, err := os.Stat(file); err != nil {
		t.Fatal("a regular file was removed as if it were the editor's socket")
	}

	link := filepath.Join(dir, "link")
	_ = os.Symlink(file, link)
	removeSocket(link)
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("a link was removed")
	}

	sock := filepath.Join(dir, "nvim.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if ul, ok := l.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	_ = l.Close()
	if _, err := os.Lstat(sock); err != nil {
		t.Skip("the socket did not outlive its listener")
	}
	removeSocket(sock)
	if _, err := os.Lstat(sock); err == nil {
		t.Fatal("a stale socket must be removed")
	}

	removeSocket("127.0.0.1:4711") // an address, not a path: nothing to do
}

func TestVimQuotingOfPaths(t *testing.T) {
	if got := vimString("/Users/o'neil/app"); got != "'/Users/o''neil/app'" {
		t.Fatalf("vimString: %s", got)
	}
	if got := vimOption(`/My Projects/a,b|c`); got != `/My\ Projects/a\,b\|c` {
		t.Fatalf("vimOption: %s", got)
	}
}
