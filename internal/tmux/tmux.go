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
	RoleClaude   = "CLAUDE"
	RoleEditor   = "NVIM"
	RoleLog      = "LOG"
	RolePresence = "PRESENCE"
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
	_, _ = Run("display-message", "-t", Pane(session, RoleEditor), "-d", "3000", text)
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
		style, text = "fg=white,bg=red", "⚠ Claude needs you"
	}
	if detail = strings.TrimSpace(detail); detail != "" {
		if len(detail) > 60 {
			detail = detail[:57] + "…"
		}
		text += ": " + strings.ReplaceAll(detail, "#", "##")
	}
	return "#[" + style + "] " + text + " #[default]"
}

// StateColour is the colour that stands for a state, on the pane borders
// and in the presence animation.
func StateColour(state string) string {
	switch state {
	case StateIdle:
		return "green"
	case StateSpeaking:
		return "cyan"
	case StateAsking:
		return "magenta"
	case StateListening, StateAttention:
		return "red"
	default:
		return "yellow"
	}
}

// SetState updates the session's status indicator and tints every pane
// border with the colour of the state, so the state is visible from
// anywhere on the screen. It is a no-op without a session name, so code
// running outside a pair session never touches tmux.
func SetState(session, state, detail string) {
	if session == "" {
		return
	}
	colour := StateColour(state)
	window := "=" + session + ":" + MainWindow // this session only: others have their own state
	_, _ = Run(
		"set-option", "-t", "="+session+":", "@pair_state", RenderState(state, detail), ";",
		"set-option", "-w", "-t", window, "pane-border-style", "fg="+colour, ";",
		"set-option", "-w", "-t", window, "pane-active-border-style", "fg="+colour+",bold",
	)
}

// MainWindow is the name of the window that holds the editor and the
// activity column.
const MainWindow = "pair"

// RecentSession returns the pair session used last among those running,
// for commands that come from outside any session: a global hotkey.
func RecentSession() string {
	out, err := Run("list-sessions", "-F", "#{session_activity} #{session_name}")
	if err != nil {
		return ""
	}
	best, bestAt := "", ""
	for _, line := range strings.Split(out, "\n") {
		at, name, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || Environment(name, "PAIR_ROOT") == "" {
			continue
		}
		if len(at) > len(bestAt) || (len(at) == len(bestAt) && at > bestAt) {
			best, bestAt = name, at
		}
	}
	return best
}

// Sessions returns the running pair sessions and the repository of each.
func Sessions() map[string]string {
	out, err := Run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil
	}
	found := map[string]string{}
	for _, name := range strings.Split(out, "\n") {
		if name = strings.TrimSpace(name); name != "" {
			if root := Environment(name, "PAIR_ROOT"); root != "" {
				found[name] = root
			}
		}
	}
	return found
}

// Claude Code's pane is hidden by default: it lives in a background window
// and is brought into the main window, under the editor, on demand.

// ClaudeVisible reports whether Claude's pane is in the main window.
func ClaudeVisible(session string) bool {
	if session == "" {
		return false
	}
	claude, err := Run("display-message", "-p", "-t", ClaudePane(session), "#{window_id}")
	if err != nil {
		return false
	}
	main, err := Run("display-message", "-p", "-t", Pane(session, RoleEditor), "#{window_id}")
	return err == nil && claude == main
}

// ShowClaude brings Claude's pane under the editor, with the keyboard in
// it.
func ShowClaude(session, height string) error {
	if session == "" || ClaudeVisible(session) {
		return nil
	}
	if height == "" {
		height = "45%"
	}
	claude := ClaudePane(session)
	if _, err := Run("join-pane", "-v", "-l", height, "-s", claude, "-t", Pane(session, RoleEditor)); err != nil {
		return err
	}
	_, err := Run("select-pane", "-t", claude)
	return err
}

// HideClaude sends Claude's pane back to its background window. Claude Code
// keeps running there.
func HideClaude(session string) error {
	if session == "" || !ClaudeVisible(session) {
		return nil
	}
	if _, err := Run("break-pane", "-d", "-s", ClaudePane(session), "-n", "claude"); err != nil {
		return err
	}
	_, err := Run("select-pane", "-t", Pane(session, RoleEditor))
	return err
}

// ToggleClaude shows Claude's pane when it is hidden and hides it when it
// is shown. It reports whether the pane is visible afterwards.
func ToggleClaude(session, height string) (bool, error) {
	if ClaudeVisible(session) {
		return false, HideClaude(session)
	}
	return true, ShowClaude(session, height)
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
