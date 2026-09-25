package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/nvim"
)

type check struct {
	name     string
	required bool
	ok       bool
	detail   string
}

// Doctor checks the dependencies and configuration and reports each one. It
// returns an error when a required check fails.
func Doctor(w io.Writer) error {
	cfg := config.Load("")
	var checks []check

	bin := func(name string, required bool, purpose string) {
		path, err := exec.LookPath(name)
		if err != nil {
			checks = append(checks, check{name, required, false, "not found on PATH (" + purpose + ")"})
			return
		}
		checks = append(checks, check{name, required, true, path})
	}
	bin("claude", true, "the agent")
	bin("tmux", true, "layout")
	bin("git", true, "diffs")
	bin("say", true, "text to speech")
	bin("afplay", false, "chimes")
	bin("rec", false, "microphone, from sox (milestone 3)")
	bin("whisper-cli", false, "speech to text, from whisper.cpp (milestone 3)")
	bin("delta", false, "pretty diffs")

	if v, err := nvimVersion(); err != nil {
		checks = append(checks, check{"nvim", true, false, "not found on PATH"})
	} else {
		major, minor := parseVersion(v)
		ok := major > 0 || minor >= 10
		checks = append(checks, check{"nvim", true, ok, v + " (need 0.10+)"})
	}

	if config.SockTooLong(cfg.Sock) {
		checks = append(checks, check{"socket path", true, false, fmt.Sprintf("%d bytes, max %d: %s", len(cfg.Sock), config.MaxSockPath, cfg.Sock)})
	} else {
		checks = append(checks, check{"socket path", true, true, cfg.Sock})
	}

	if out, err := exec.Command("say", "-v", "?").Output(); err == nil {
		voices := string(out)
		if strings.Contains(voices, cfg.Voice+" ") {
			detail := cfg.Voice
			if cfg.Voice == "Luciana" {
				detail += " (stock voice is the basic tier; download \"Luciana (Enhanced)\" under System Settings → Accessibility → Spoken Content → Manage Voices)"
			}
			checks = append(checks, check{"voice", false, true, detail})
		} else {
			checks = append(checks, check{"voice", false, false, cfg.Voice + " not installed; see System Settings → Accessibility → Spoken Content"})
		}
	}

	if fi, err := os.Stat(cfg.WhisperModel); err == nil {
		checks = append(checks, check{"whisper model", false, true, fmt.Sprintf("%s (%.1f GB)", cfg.WhisperModel, float64(fi.Size())/1e9)})
	} else {
		checks = append(checks, check{"whisper model", false, false, cfg.WhisperModel + " missing (milestone 3 downloads it)"})
	}

	if hasSession(cfg.Session) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		v, err := nvim.New(cfg.Sock).Ping(ctx)
		cancel()
		if err != nil {
			checks = append(checks, check{"neovim rpc", false, false, err.Error()})
		} else {
			checks = append(checks, check{"neovim rpc", false, true, v})
		}
	} else {
		checks = append(checks, check{"tmux session", false, true, fmt.Sprintf("%q not running (start with `pair`)", cfg.Session)})
	}

	if os.Getenv("CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS") != "" && os.Getenv("CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS") != "0" {
		checks = append(checks, check{"auto-background", false, false, "CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS is set in your shell; pair start overrides it to 0 for the session"})
	}

	failed := false
	for _, c := range checks {
		mark := "✓"
		if !c.ok {
			mark = "✗"
			if !c.required {
				mark = "!"
			} else {
				failed = true
			}
		}
		fmt.Fprintf(w, "%s %-14s %s\n", mark, c.name, c.detail)
	}
	fmt.Fprintf(w, "\nrepo root: %s\n", cfg.Root)
	if failed {
		return fmt.Errorf("required checks failed")
	}
	return nil
}

func nvimVersion() (string, error) {
	out, err := exec.Command("nvim", "--version").Output()
	if err != nil {
		return "", err
	}
	first := strings.SplitN(string(out), "\n", 2)[0]
	return strings.TrimSpace(strings.TrimPrefix(first, "NVIM ")), nil
}

// parseVersion reads "v0.12.5" (or "0.12.5-dev") into major and minor.
func parseVersion(v string) (int, int) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0, 0
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(strings.SplitN(parts[1], "-", 2)[0])
	return major, minor
}
