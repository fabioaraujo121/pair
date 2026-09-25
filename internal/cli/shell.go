package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

// shellQuote wraps s in single quotes for a POSIX shell, unless it is a
// plain word that needs none.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`&|;<>()*?[]#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// tmux runs one tmux command and returns its trimmed output.
func tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func hasSession(name string) bool {
	_, err := tmux("has-session", "-t", "="+name)
	return err == nil
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
