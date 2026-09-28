package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/events"
	"github.com/fabioaraujo121/pair/internal/hook"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

// Start creates the tmux session or attaches to it when it already exists.
//
// The screen answers three questions: is Claude running (status bar), which
// files is it changing (Neovim, the main area) and why (the activity log).
// Claude Code's own pane is a strip under the editor: it is where prompts
// are typed and permissions answered, not what one watches.
//
//	┌──────────────────────────────┬──────────────────┐
//	│ neovim: tree | editor        │ activity log     │
//	│              | PAIRING.md    │ narrations, files│
//	├──────────────────────────────┤ changed, diffs   │
//	│ claude                       │                  │
//	└──────────────────────────────┴──────────────────┘
func Start() error {
	cfg := config.Load("")
	for _, bin := range []string{"tmux", "nvim", "claude"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s is not on PATH; run `pair doctor`", bin)
		}
	}
	if config.SockTooLong(cfg.Sock) {
		return fmt.Errorf("PAIR_SOCK is %d bytes; macOS allows %d. Use a shorter path or 127.0.0.1:<port>", len(cfg.Sock), config.MaxSockPath)
	}
	log := events.Open(cfg.StateDir)
	if f, err := os.OpenFile(log.SessionLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		_ = f.Close()
	}
	pluginDir, err := InstallPlugin(cfg.UserDir)
	if err != nil {
		return err
	}

	if hasSession(cfg.Session) {
		return attach(cfg.Session)
	}
	hook.ResetState(cfg.StateDir) // fresh baseline for hand-edit detection
	if err := createSession(cfg, pluginDir, log); err != nil {
		return err
	}
	_ = log.Append("session_start", map[string]any{"root": cfg.Root, "sock": cfg.Sock, "lang": cfg.Lang, "voice": cfg.Voice})
	_ = log.Session("● pair session started in " + tildePath(cfg.Root))
	_ = log.Session(fmt.Sprintf("  speaking %s with voice %s. The microphone is closed until you press F5 or click 🎙 talk.", cfg.Lang, cfg.Voice))
	return attach(cfg.Session)
}

func createSession(cfg config.Config, pluginDir string, log *events.Log) error {
	if !config.IsTCP(cfg.Sock) {
		_ = os.Remove(cfg.Sock) // stale socket from a previous session
	}
	s := cfg.Session
	exe, _ := os.Executable()
	cols, lines := terminalSize()
	fail := func(err error) error {
		_, _ = tmuxRun("kill-session", "-t", "="+s)
		return err
	}

	// The session environment reaches every pane, so `claude` is started by
	// name and any shell wrapper the user has around it still applies.
	editor, err := tmuxRun("new-session", "-d", "-P", "-F", "#{pane_id}", "-s", s, "-n", "pair",
		"-x", cols, "-y", lines, "-c", cfg.Root,
		"-e", "PAIR_SOCK="+cfg.Sock, "-e", "PAIR_ROOT="+cfg.Root, "-e", "PAIR_SESSION="+s,
		"-e", "PAIR_TMUX_SOCKET="+tmux.Socket, "-e", "CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS=0")
	if err != nil {
		return err
	}
	logPane, err := tmuxRun("split-window", "-h", "-P", "-F", "#{pane_id}", "-l", cfg.LogWidth, "-t", editor, "-c", cfg.Root)
	if err != nil {
		return fail(err)
	}
	claude, err := tmuxRun("split-window", "-v", "-P", "-F", "#{pane_id}", "-l", cfg.ClaudeHeight, "-t", editor, "-c", cfg.Root)
	if err != nil {
		return fail(err)
	}

	steps := [][]string{
		{"set-environment", "-t", "=" + s, "PAIR_PANE_NVIM", editor},
		{"set-environment", "-t", "=" + s, "PAIR_PANE_LOG", logPane},
		{"set-environment", "-t", "=" + s, "PAIR_PANE_CLAUDE", claude},
		{"set-option", "-p", "-t", editor, "@pair_name", "files: what Claude reads and changes"},
		{"set-option", "-p", "-t", logPane, "@pair_name", "activity: what Claude does and why"},
		{"set-option", "-p", "-t", claude, "@pair_name", "claude: type here, or F5 to talk · F7 full screen"},
	}
	steps = append(steps, sessionChrome(exe, claude)...)
	for _, args := range steps {
		if _, err := tmuxRun(args...); err != nil {
			return fail(err)
		}
	}
	tmux.SetState(s, tmux.StateIdle, "")

	nvimCmd := "clear; nvim --cmd " + shellQuote("set rtp^="+pluginDir) +
		" --cmd " + shellQuote("let g:pair_bin='"+exe+"' | let g:pair_root='"+cfg.Root+"'") +
		" --listen " + shellQuote(cfg.Sock) +
		" -c " + shellQuote("lua require('pair').layout()")
	tailCmd := "clear; " + shellQuote(exe) + " log --follow"
	claudeCmd := "clear; claude" + os.Getenv("PAIR_CLAUDE_ARGS")

	keys := [][]string{
		{"send-keys", "-t", editor, nvimCmd, "Enter"},
		{"send-keys", "-t", logPane, tailCmd, "Enter"},
		{"send-keys", "-t", claude, claudeCmd, "Enter"},
		{"select-pane", "-t", claude},
	}
	for _, args := range keys {
		if _, err := tmuxRun(args...); err != nil {
			return err
		}
	}
	return nil
}

// sessionChrome returns the options and key bindings of pair's tmux server:
// mouse, labelled panes, a status line with the current state and the keys.
// They are global to the server, which is pair's own.
func sessionChrome(exe, claudePane string) [][]string {
	listen := shellQuote(exe) + " hook listen >/dev/null 2>&1"
	interrupt := shellQuote(exe) + " hook interrupt >/dev/null 2>&1"
	return [][]string{
		{"set-option", "-g", "mouse", "on"},
		// Full colour for Neovim's syntax highlighting.
		{"set-option", "-g", "default-terminal", "tmux-256color"},
		{"set-option", "-as", "terminal-features", ",*:RGB"},
		{"set-option", "-g", "escape-time", "10"},
		{"set-option", "-g", "focus-events", "on"},
		{"set-option", "-g", "pane-border-status", "top"},
		{"set-option", "-g", "pane-border-format", " #{@pair_name} "},
		{"set-option", "-g", "status-interval", "1"},
		{"set-option", "-g", "status-left-length", "90"},
		{"set-option", "-g", "status-left", " pair #{@pair_state} "},
		{"set-option", "-g", "status-right-length", "90"},
		// Buttons: clicking one runs `pair hook click <name>`.
		{"set-option", "-g", "status-right", "#[range=user|talk]#[fg=black,bg=white] 🎙 talk F5 #[default]#[norange] " +
			"#[range=user|stop]#[fg=black,bg=white] ✋ interrupt F6 #[default]#[norange] " +
			"#[range=user|claude]#[fg=black,bg=white] claude F7 #[default]#[norange] "},
		{"bind-key", "-n", "MouseDown1Status", "run-shell", "-b", shellQuote(exe) + " hook click '#{mouse_status_range}' >/dev/null 2>&1"},
		{"set-option", "-g", "window-status-current-format", ""},
		{"set-option", "-g", "window-status-format", ""},
		// Voice keys work from any pane, with and without the prefix.
		{"bind-key", "-n", "F5", "run-shell", "-b", listen},
		{"bind-key", "-n", "F6", "run-shell", "-b", interrupt},
		{"bind-key", "t", "run-shell", "-b", listen},
		{"bind-key", "i", "run-shell", "-b", interrupt},
		// F7 brings Claude's pane to full screen and back, for permission
		// prompts and long answers.
		{"bind-key", "-n", "F7", "resize-pane", "-Z", "-t", claudePane},
	}
}

// attach hands the terminal to tmux: switch-client from inside pair's
// server, exec attach from a plain terminal, and print instructions
// otherwise.
func attach(session string) error {
	manual := fmt.Sprintf("tmux -L %s attach -t %s", tmux.Socket, session)
	if os.Getenv("TMUX") != "" {
		if tmux.InsideServer() {
			_, err := tmuxRun("switch-client", "-t", "="+session)
			return err
		}
		fmt.Printf("session %q is running on pair's own tmux server. You are inside another tmux; open a plain terminal and run `pair`, or nest with: TMUX= %s\n", session, manual)
		return nil
	}
	if !stdinIsTerminal() {
		fmt.Printf("session %q is running; attach with: %s\n", session, manual)
		return nil
	}
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	argv := append([]string{"tmux"}, tmux.Args("attach", "-t", "="+session)...)
	return syscall.Exec(tmuxBin, argv, os.Environ())
}

// Stop kills the tmux session and any speech or recording still running.
func Stop() error {
	cfg := config.Load("")
	switch {
	case hasSession(cfg.Session):
		if _, err := tmuxRun("kill-session", "-t", "="+cfg.Session); err != nil {
			return err
		}
	case stopLegacySession(cfg.Session):
		fmt.Println("stopped a session created by an earlier version of pair")
	default:
		fmt.Printf("no pair session named %q\n", cfg.Session)
	}
	if !config.IsTCP(cfg.Sock) {
		_ = os.Remove(cfg.Sock)
	}
	for _, p := range []string{"say", "rec"} {
		_ = exec.Command("pkill", "-x", p).Run()
	}
	_ = events.Open(cfg.StateDir).Session("○ pair session stopped")
	return nil
}

// stopLegacySession kills a session that an earlier version created on the
// user's default tmux server. It only touches a session that carries
// pair's PAIR_SOCK variable.
func stopLegacySession(session string) bool {
	out, err := exec.Command("tmux", "show-environment", "-t", "="+session, "PAIR_SOCK").Output()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(out)), "PAIR_SOCK=") {
		return false
	}
	return exec.Command("tmux", "kill-session", "-t", "="+session).Run() == nil
}

// tildePath abbreviates the home directory for display.
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}
