package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/nvim"
	"github.com/fabioaraujo121/pair/internal/speech"
)

// DoctorOptions selects the optional, slower or interactive checks.
type DoctorOptions struct {
	DownloadModel bool // --fix: download the whisper model when missing
	MicTest       bool // --mic: record one second to check microphone access
}

type check struct {
	name     string
	required bool
	ok       bool
	detail   string
}

// Doctor checks the dependencies and configuration and reports each one. It
// returns an error when a required check fails.
func Doctor(w io.Writer, opts DoctorOptions) error {
	cfg := config.Load("")
	var checks []check

	bin := func(name string, required bool, purpose string) bool {
		path, err := exec.LookPath(name)
		if err != nil {
			checks = append(checks, check{name, required, false, "not found on PATH (" + purpose + ")"})
			return false
		}
		checks = append(checks, check{name, required, true, path})
		return true
	}
	bin("claude", true, "the agent")
	bin("tmux", true, "layout")
	bin("git", true, "diffs")
	bin("say", true, "text to speech")
	bin("afplay", false, "chimes")
	hasRec := bin("rec", true, "microphone, brew install sox")
	hasWhisper := bin("whisper-cli", true, "speech to text, brew install whisper.cpp")
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

	checks = append(checks, modelCheck(w, cfg, opts, hasWhisper))

	if hasRec && (opts.MicTest || (stdinIsTerminal() && os.Getenv("PAIR_SKIP_MIC") == "")) {
		checks = append(checks, micCheck(w, cfg))
	} else if hasRec {
		checks = append(checks, check{"microphone", false, true, "not tested (run `pair doctor --mic` once to grant access)"})
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

	if claude, err := exec.LookPath("claude"); err == nil {
		get := claudeCmd(claude, "mcp", "get", "pair")
		get.Dir = cfg.Root
		if out, err := get.CombinedOutput(); err == nil {
			first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
			checks = append(checks, check{"mcp server", false, true, first})
		} else {
			checks = append(checks, check{"mcp server", false, false, "\"pair\" not registered for this project (run `pair init`)"})
		}
	}

	if v := os.Getenv("CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS"); v != "" && v != "0" {
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

// modelCheck reports the whisper model and downloads it with --fix.
func modelCheck(w io.Writer, cfg config.Config, opts DoctorOptions, hasWhisper bool) check {
	if fi, err := os.Stat(cfg.WhisperModel); err == nil {
		return check{"whisper model", true, true, fmt.Sprintf("%s (%.1f GB)", cfg.WhisperModel, float64(fi.Size())/1e9)}
	}
	url := "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/" + filepath.Base(cfg.WhisperModel)
	if !opts.DownloadModel {
		return check{"whisper model", hasWhisper, false, fmt.Sprintf("missing: %s\n                 run `pair doctor --fix` to download it (about 1.6 GB) from %s", cfg.WhisperModel, url)}
	}
	fmt.Fprintf(w, "downloading %s\n  to %s\n", url, cfg.WhisperModel)
	if err := os.MkdirAll(filepath.Dir(cfg.WhisperModel), 0o755); err != nil {
		return check{"whisper model", true, false, err.Error()}
	}
	tmp := cfg.WhisperModel + ".part"
	cmd := exec.Command("curl", "-L", "--fail", "--progress-bar", "-o", tmp, url)
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmp)
		return check{"whisper model", true, false, "download failed: " + err.Error()}
	}
	if err := os.Rename(tmp, cfg.WhisperModel); err != nil {
		return check{"whisper model", true, false, err.Error()}
	}
	fi, _ := os.Stat(cfg.WhisperModel)
	return check{"whisper model", true, true, fmt.Sprintf("downloaded %s (%.1f GB)", cfg.WhisperModel, float64(fi.Size())/1e9)}
}

// micCheck records one second through `rec` with a hard cap. The first
// run makes macOS show the microphone permission dialog for the terminal
// app; a missing file afterwards means access was denied or never granted.
func micCheck(w io.Writer, cfg config.Config) check {
	fmt.Fprintln(w, "testing the microphone for one second (macOS may ask for permission)…")
	tmp := filepath.Join(os.TempDir(), "pair-mic-test.wav")
	_ = os.Remove(tmp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "rec", "-q", "-r", "16000", "-c", "1", tmp, "trim", "0", "1")
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	_ = cmd.Run()
	audio, err := speech.AnalyzeWAV(tmp)
	_ = os.Remove(tmp)
	if err != nil || audio.Seconds < 0.5 {
		return check{"microphone", true, false, "no audio captured. Grant microphone access to your terminal app under System Settings → Privacy & Security → Microphone (or reset with `tccutil reset Microphone`), then run `pair doctor --mic` again"}
	}
	level := audio.RMS * 100
	detail := fmt.Sprintf("ok, ambient level %.2f%% of full scale", level)
	if level > 1 {
		return check{"microphone", false, false, detail + fmt.Sprintf(": above the %s silence threshold, recordings would never stop. Lower the noise or set PAIR_SILENCE_LEVEL=%.0f%%", cfg.SilenceLevel, level*2)}
	}
	return check{"microphone", true, true, detail}
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
