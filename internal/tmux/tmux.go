// Package tmux wraps the handful of tmux commands pair uses, shared by the
// CLI (session layout) and the hooks (prompt injection, interrupt).
package tmux

import (
	"fmt"
	"os/exec"
	"strings"
)

// Run executes one tmux command and returns its trimmed output.
func Run(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// HasSession reports whether a session with exactly this name exists.
func HasSession(name string) bool {
	_, err := Run("has-session", "-t", "="+name)
	return err == nil
}

// Environment returns a variable from the session's environment, or "".
func Environment(session, name string) string {
	out, err := Run("show-environment", "-t", "="+session, name)
	if err != nil {
		return ""
	}
	if i := strings.IndexByte(out, '='); i >= 0 {
		return out[i+1:]
	}
	return ""
}

// ClaudePane is the target of the pane running Claude Code.
func ClaudePane(session string) string { return session + ":0.0" }

// SendPrompt types text into the Claude Code pane and submits it. Literal
// mode keeps key names and a leading dash in the text from being
// interpreted; Enter goes in a second call, as validated.
func SendPrompt(session, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	pane := ClaudePane(session)
	if _, err := Run("send-keys", "-t", pane, "-l", "--", text); err != nil {
		return err
	}
	_, err := Run("send-keys", "-t", pane, "Enter")
	return err
}

// SendEscape interrupts the running Claude Code turn. Escape, never C-c: a
// double C-c would exit Claude Code.
func SendEscape(session string) error {
	_, err := Run("send-keys", "-t", ClaudePane(session), "Escape")
	return err
}
