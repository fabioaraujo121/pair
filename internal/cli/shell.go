package cli

import (
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"

	"github.com/fabioaraujo121/pair/internal/tmux"
)

// tmux and hasSession keep the call sites short.
func tmuxRun(args ...string) (string, error) { return tmux.Run(args...) }
func hasSession(name string) bool            { return tmux.HasSession(name) }

// shellQuote wraps s in single quotes for a POSIX shell, unless it is a
// plain word that needs none.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`&|;<>()*?[]#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// stdinIsTerminal reports whether pair was started from an interactive shell.
func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// terminalSize returns the current terminal's columns and lines, or a
// sensible default when there is no terminal.
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
