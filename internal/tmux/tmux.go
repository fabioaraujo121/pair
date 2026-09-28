// Package tmux wraps the handful of tmux commands pair uses, shared by the
// CLI (session layout) and the hooks (prompt injection, interrupt).
//
// pair runs on its own tmux server (`tmux -L pair`), so the key bindings,
// mouse mode and status line it sets never touch the user's default server.
package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Socket is the name of pair's tmux server socket (PAIR_TMUX_SOCKET).
var Socket = socketName()

func socketName() string {
	if v := os.Getenv("PAIR_TMUX_SOCKET"); v != "" {
		return v
	}
	return "pair"
}

// Args prefixes a tmux command line with the server selection.
func Args(args ...string) []string {
	return append([]string{"-L", Socket}, args...)
}

// Run executes one tmux command on pair's server and returns its trimmed
// output.
func Run(args ...string) (string, error) {
	out, err := exec.Command("tmux", Args(args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// HasSession reports whether a session with exactly this name exists.
func HasSession(name string) bool {
	if name == "" {
		return false
	}
	_, err := Run("has-session", "-t", "="+name)
	return err == nil
}

// InsideServer reports whether the current process runs inside pair's own
// tmux server ($TMUX is "<socket path>,<pid>,<session>").
func InsideServer() bool {
	path := strings.SplitN(os.Getenv("TMUX"), ",", 2)[0]
	return path != "" && strings.HasSuffix(path, "/"+Socket)
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

// Pane roles, stored as PAIR_PANE_<role> in the session environment when the
// session is created. Pane indexes depend on the layout; pane ids do not.
const (
	RoleClaude = "CLAUDE"
	RoleEditor = "NVIM"
	RoleLog    = "LOG"
)

// Pane returns the tmux target of a pane by role.
func Pane(session, role string) string {
	if id := Environment(session, "PAIR_PANE_"+role); id != "" {
		return id
	}
	switch role { // sessions created before panes were tracked by id
	case RoleClaude:
		return session + ":0.0"
	case RoleEditor:
		return session + ":0.1"
	default:
		return session + ":0.2"
	}
}

// ClaudePane is the target of the pane running Claude Code.
func ClaudePane(session string) string { return Pane(session, RoleClaude) }

// Message flashes a line in the session's status bar for a few seconds.
func Message(session, text string) {
	if session == "" {
		return
	}
	_, _ = Run("display-message", "-t", ClaudePane(session), "-d", "3000", text)
}

// States shown in the status bar, so a glance tells what is going on.
const (
	StateIdle      = "idle"
	StateWorking   = "working"
	StateSpeaking  = "speaking"
	StateAsking    = "asking"
	StateListening = "listening"
	StateAttention = "attention"
)

// RenderState returns the styled status text for a state.
func RenderState(state, detail string) string {
	style, text := "fg=black,bg=yellow", "● Claude is working"
	switch state {
	case StateIdle:
		style, text = "fg=black,bg=green", "○ your turn: F5 to talk"
	case StateSpeaking:
		style, text = "fg=black,bg=cyan", "🗣 Claude is speaking"
	case StateAsking:
		style, text = "fg=white,bg=magenta", "❓ Claude is asking you"
	case StateListening:
		style, text = "fg=white,bg=red", "🎙 listening: speak, then pause"
	case StateAttention:
		style, text = "fg=white,bg=red", "⚠ Claude needs you (F7)"
	}
	if detail = strings.TrimSpace(detail); detail != "" {
		if len(detail) > 60 {
			detail = detail[:57] + "…"
		}
		text += ": " + strings.ReplaceAll(detail, "#", "##")
	}
	return "#[" + style + "] " + text + " #[default]"
}

// SetState updates the session's status indicator. It is a no-op without a
// session name, so code running outside a pair session never touches tmux.
func SetState(session, state, detail string) {
	if session == "" {
		return
	}
	_, _ = Run("set-option", "-t", "="+session+":", "@pair_state", RenderState(state, detail))
}

// SendText types text into the Claude Code pane in literal mode (key names
// and a leading dash in the text are not interpreted) and submits it when
// submit is true; Enter goes in a second call, as validated.
func SendText(session, text string, submit bool) error {
	if strings.TrimSpace(text) == "" || session == "" {
		return nil
	}
	pane := ClaudePane(session)
	if _, err := Run("send-keys", "-t", pane, "-l", "--", text); err != nil {
		return err
	}
	if !submit {
		return nil
	}
	_, err := Run("send-keys", "-t", pane, "Enter")
	return err
}

// SendPrompt types text and submits it.
func SendPrompt(session, text string) error { return SendText(session, text, true) }

// SendEscape interrupts the running Claude Code turn. Escape, never C-c: a
// double C-c would exit Claude Code.
func SendEscape(session string) error {
	if session == "" {
		return nil
	}
	_, err := Run("send-keys", "-t", ClaudePane(session), "Escape")
	return err
}
