package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/events"
)

// Start creates the tmux session (Claude Code left, Neovim top right, log
// pane bottom right) or attaches to it when it already exists.
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
	if err := createSession(cfg, pluginDir, log); err != nil {
		return err
	}
	_ = log.Append("session_start", map[string]any{"root": cfg.Root, "sock": cfg.Sock})
	_ = log.Session("● pair session started in " + cfg.Root)
	return attach(cfg.Session)
}

func createSession(cfg config.Config, pluginDir string, log *events.Log) error {
	if !config.IsTCP(cfg.Sock) {
		_ = os.Remove(cfg.Sock) // stale socket from a previous session
	}
	s := cfg.Session
	cols, lines := terminalSize()
	steps := [][]string{
		{"new-session", "-d", "-s", s, "-n", "main", "-x", cols, "-y", lines, "-c", cfg.Root},
		{"set-environment", "-t", s, "PAIR_SOCK", cfg.Sock},
		{"set-environment", "-t", s, "CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS", "0"},
		{"split-window", "-h", "-l", "50%", "-t", s + ":0.0", "-c", cfg.Root},
		{"split-window", "-v", "-l", "30%", "-t", s + ":0.1", "-c", cfg.Root},
	}
	for _, args := range steps {
		if _, err := tmux(args...); err != nil {
			_, _ = tmux("kill-session", "-t", "="+s)
			return err
		}
	}

	scratch := ""
	if _, err := os.Stat(filepath.Join(cfg.Root, "PAIRING.md")); err == nil {
		scratch = " PAIRING.md"
	}
	nvimCmd := "nvim --cmd " + shellQuote("set rtp^="+pluginDir) + " --listen " + shellQuote(cfg.Sock) + scratch
	tailCmd := "tail -n 40 -f " + shellQuote(log.SessionLogPath())
	claudeCmd := "env PAIR_SOCK=" + shellQuote(cfg.Sock) + " CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS=0 claude" + os.Getenv("PAIR_CLAUDE_ARGS")

	keys := [][]string{
		{"send-keys", "-t", s + ":0.1", nvimCmd, "Enter"},
		{"send-keys", "-t", s + ":0.2", tailCmd, "Enter"},
		{"send-keys", "-t", s + ":0.0", claudeCmd, "Enter"},
		{"select-pane", "-t", s + ":0.0"},
	}
	for _, args := range keys {
		if _, err := tmux(args...); err != nil {
			return err
		}
	}
	return nil
}

// attach hands the terminal to tmux: switch-client from inside tmux, exec
// attach from a plain terminal, and just print instructions otherwise.
func attach(session string) error {
	if os.Getenv("TMUX") != "" {
		_, err := tmux("switch-client", "-t", "="+session)
		return err
	}
	if !stdinIsTerminal() {
		fmt.Printf("session %q is running; attach with: tmux attach -t %s\n", session, session)
		return nil
	}
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	return syscall.Exec(tmuxBin, []string{"tmux", "attach", "-t", "=" + session}, os.Environ())
}

// Stop kills the tmux session and any speech or recording still running.
func Stop() error {
	cfg := config.Load("")
	if !hasSession(cfg.Session) {
		fmt.Printf("no tmux session named %q\n", cfg.Session)
	} else if _, err := tmux("kill-session", "-t", "="+cfg.Session); err != nil {
		return err
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
