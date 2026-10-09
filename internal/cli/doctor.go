package cli

import (
	"bytes"
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

	"github.com/fabioaraujo121/pair/internal/agent"
	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/fetch"
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
	ag, _ := agent.For(cfg.Agent)
	bin(ag.Binary(), true, "the agent, "+ag.Title())
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
			detail := cfg.Voice + ", language " + cfg.Lang
			if cfg.Voice == "Luciana" {
				detail += " (stock voice is the basic tier; download \"Luciana (Enhanced)\" under System Settings → Accessibility → Spoken Content → Manage Voices)"
			}
			checks = append(checks, check{"voice", false, true, detail})
		} else {
			checks = append(checks, check{"voice", false, false, cfg.Voice + " not installed; list voices with `say -v '?'`, choose one with `pair init --voice <name>`, or download it under System Settings → Accessibility → Spoken Content"})
		}
	}

	if cfg.TTS == "kokoro" {
		k := speech.NewKokoro(cfg)
		if ok, why := k.Installed(); ok {
			checks = append(checks, check{"kokoro voice", false, true, k.Voice + " in " + k.Dir})
		} else {
			checks = append(checks, check{"kokoro voice", false, false, why + "; the system voice speaks meanwhile"})
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

	if left := ag.Leftovers(cfg.Root); len(left) > 0 {
		checks = append(checks, check{"repository", true, false, "an earlier version of pair wrote " + strings.Join(left, ", ") + ": run `pair uninstall`"})
	} else {
		checks = append(checks, check{"repository", false, true, "nothing of pair's in it; hooks and the MCP server are given to " + ag.Title() + " at `pair`"})
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

// modelCheck reports the whisper model and downloads it with --fix. Only
// a pinned model is downloaded, and it is kept only when it is the file
// the pin describes.
func modelCheck(w io.Writer, cfg config.Config, opts DoctorOptions, hasWhisper bool) check {
	name := filepath.Base(cfg.WhisperModel)
	pin, pinErr := fetch.Find(fetch.Whisper, name)
	if fi, err := os.Stat(cfg.WhisperModel); err == nil {
		detail := fmt.Sprintf("%s (%.1f GB)", cfg.WhisperModel, float64(fi.Size())/1e9)
		if pinErr == nil && !pin.SizeMatches(cfg.WhisperModel) {
			return check{"whisper model", hasWhisper, false, detail + fmt.Sprintf(": not the size of the pinned %s (%d bytes); remove it and run `pair doctor --fix`", name, pin.Size)}
		}
		return check{"whisper model", true, true, detail}
	}
	if pinErr != nil {
		return check{"whisper model", hasWhisper, false, fmt.Sprintf("missing: %s\n                 pair has no pinned download for %s: put the file there yourself, or use the default model", cfg.WhisperModel, name)}
	}
	if !opts.DownloadModel {
		return check{"whisper model", hasWhisper, false, fmt.Sprintf("missing: %s\n                 run `pair doctor --fix` to download it (about 1.6 GB) from %s", cfg.WhisperModel, pin.URL)}
	}
	fmt.Fprintf(w, "downloading %s\n  to %s\n  (sha256 %s…)\n", pin.URL, cfg.WhisperModel, pin.SHA256[:12])
	if err := pin.Download(w, cfg.WhisperModel); err != nil {
		return check{"whisper model", true, false, "download failed: " + err.Error()}
	}
	fi, _ := os.Stat(cfg.WhisperModel)
	return check{"whisper model", true, true, fmt.Sprintf("downloaded and verified %s (%.1f GB)", cfg.WhisperModel, float64(fi.Size())/1e9)}
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
	// Same format options as the real recorder: 16 kHz mono 16-bit.
	cmd := exec.CommandContext(ctx, "rec", "-r", "16000", "-c", "1", "-b", "16", "-e", "signed-integer", tmp, "trim", "0", "1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start).Round(100 * time.Millisecond)
	timedOut := ctx.Err() != nil
	audio, err := speech.AnalyzeWAV(tmp)
	_ = os.Remove(tmp)
	if err != nil || audio.Seconds < 0.5 {
		// Say what actually happened instead of assuming a permission problem.
		var why []string
		switch {
		case timedOut:
			why = append(why, fmt.Sprintf("rec did not finish a 1 s recording within %s (no audio arrived from the input device)", elapsed))
		case runErr != nil:
			why = append(why, fmt.Sprintf("rec failed after %s: %v", elapsed, runErr))
		}
		if err != nil {
			why = append(why, "no usable WAV ("+err.Error()+")")
		} else {
			why = append(why, fmt.Sprintf("only %.2f s recorded", audio.Seconds))
		}
		if msg := tailLines(stderr.String(), 4); msg != "" {
			why = append(why, "sox said: "+msg)
		}
		return check{"microphone", true, false, "no audio captured: " + strings.Join(why, "; ") +
			".\n                 If your terminal app is off under System Settings → Privacy & Security → Microphone, enable it and restart the terminal." +
			"\n                 If it is on, check System Settings → Sound → Input, and reproduce with: rec -V3 -r 16000 -c 1 /tmp/t.wav trim 0 1"}
	}
	if audio.Bits != 16 {
		return check{"microphone", true, false, fmt.Sprintf("rec wrote a %d-bit file although 16-bit was requested; whisper-cli needs 16-bit (sox said: %s)", audio.Bits, tailLines(stderr.String(), 2))}
	}
	if audio.RMS < 1e-5 {
		// macOS hands out digital silence instead of an error when the app
		// owning the terminal has no microphone access.
		return check{"microphone", true, false, "the input delivered pure digital silence. macOS does this when the terminal app lacks microphone access (System Settings → Privacy & Security → Microphone, then restart the terminal), when the input volume is zero, or when the wrong device is selected under System Settings → Sound → Input"}
	}
	level := audio.RMS * 100
	detail := fmt.Sprintf("ok, ambient level %.2f%% of full scale", level)
	if level > 1 {
		return check{"microphone", false, false, detail + fmt.Sprintf(": above the %s silence threshold, recordings would never stop. Lower the noise or set PAIR_SILENCE_LEVEL=%.0f%%", cfg.SilenceLevel, level*2)}
	}
	return check{"microphone", true, true, detail}
}

// tailLines returns the last n non-empty lines of s joined with " | ".
func tailLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
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
