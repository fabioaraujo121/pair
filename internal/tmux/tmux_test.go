package tmux_test

import (
	"os"
	"strings"
	"testing"

	"github.com/fabioaraujo121/pair/internal/testenv"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

func TestMain(m *testing.M) {
	cleanup := testenv.Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func TestTheTestsHaveATmuxServerOfTheirOwn(t *testing.T) {
	if tmux.Socket == "pair" || !strings.HasPrefix(tmux.Socket, "pairtest-") {
		t.Fatalf("the tests would talk to the engineer's tmux server: socket %q", tmux.Socket)
	}
	if got := strings.Join(tmux.Args("ls"), " "); got != "-L "+tmux.Socket+" ls" {
		t.Fatalf("args: %s", got)
	}
}

// The detail of a state is a file name, a command, a sentence: text that
// pair did not write. In the status bar it must stay text.
func TestStatusTextIsOneLineOfPlainText(t *testing.T) {
	got := tmux.RenderState(tmux.StateWorking, "running #(touch /tmp/owned) #{pane_pid}\nsecond line\x1b]0;title\x07\ttab", "Codex", "")
	if strings.ContainsAny(got, "\n\r\t\x1b\x07") {
		t.Fatalf("control characters in the status bar: %q", got)
	}
	if strings.Contains(got, " #(") || strings.Contains(got, " #{") {
		t.Fatalf("tmux would read a format in: %q", got)
	}
	if !strings.Contains(got, "##(touch") || !strings.Contains(got, "##{pane_pid}") {
		t.Fatalf("the text itself must be kept: %q", got)
	}
	if !strings.Contains(got, "Codex is working") {
		t.Fatalf("the agent is named: %q", got)
	}

	long := tmux.RenderState(tmux.StateWorking, strings.Repeat("ação ", 40), "", "")
	if !strings.Contains(long, "…") || strings.Contains(long, "�") || !utf8Valid(long) {
		t.Fatalf("a long detail is cut between characters, not inside one: %q", long)
	}
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "") == s }

func TestWithoutASessionTmuxIsNotTouched(t *testing.T) {
	// No server runs on the tests' socket: these must return, quietly.
	tmux.SetState("", tmux.StateIdle, "", "", "")
	tmux.Message("", "hello")
	if tmux.HasSession("") || tmux.HasSession("no-such-session") {
		t.Fatal("no session exists")
	}
	if tmux.AgentVisible("") {
		t.Fatal("nothing is visible without a session")
	}
	if err := tmux.SendPrompt("", "text", 0); err != nil {
		t.Fatal(err)
	}
}

func TestTheStatusNamesTheTalkKey(t *testing.T) {
	if got := tmux.RenderState(tmux.StateIdle, "", "", "F9"); !strings.Contains(got, "F9 to talk") {
		t.Fatalf("idle: %q", got)
	}
	if got := tmux.RenderState(tmux.StateListening, "", "", ""); !strings.Contains(got, "press F5") {
		t.Fatalf("listening, default key: %q", got)
	}
}
