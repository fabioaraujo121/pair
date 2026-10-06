package cli

import (
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"

	"github.com/fabioaraujo121/pair/internal/shell"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

// Thin seams over the tmux and shell packages, so tests can read them.
func tmuxRun(args ...string) (string, error) { return tmux.Run(args...) }
func hasSession(name string) bool            { return tmux.HasSession(name) }
func shellQuote(s string) string             { return shell.Quote(s) }

// stdinIsTerminal reports whether pair was started from an interactive shell.
func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// terminalSize returns the current terminal's columns and lines as strings
// for tmux's -x/-y, falling back to a comfortable default.
func terminalSize() (cols, lines string) {
	cols, lines = "200", "50"
	if !stdinIsTerminal() {
		return
	}
	if out, err := exec.Command("stty", "size").Output(); err == nil {
		if f := strings.Fields(string(out)); len(f) == 2 && f[0] != "0" && f[1] != "0" {
			return f[1], f[0]
		}
	}
	return
}
