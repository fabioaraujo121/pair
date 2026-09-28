package hook

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/events"
	"github.com/fabioaraujo121/pair/internal/speech"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

// Voice subcommands are started by a keymap or a global hotkey, not by
// Claude Code, so they read no stdin. They find the repository through
// --root, the tmux session's PAIR_ROOT, or the working directory.

// Listen is push-to-talk: silence any speech, record until the engineer
// pauses, transcribe, and type the transcript into the Claude Code pane.
func Listen(args []string, out io.Writer) error {
	cfg := voiceConfig(args)
	lock, busy := micLock(cfg, out)
	if busy {
		return nil
	}
	defer lock.Release()
	return listenAndInject(cfg, out)
}

// Interrupt stops Claude mid-turn and hands the microphone to the engineer:
// kill `say`, send Escape, wait for the prompt, then Listen.
func Interrupt(args []string, out io.Writer) error {
	cfg := voiceConfig(args)
	// The lock comes first: a key that auto-repeats must not send Escape
	// several times (two in a row open Claude Code's rewind menu).
	lock, busy := micLock(cfg, out)
	if busy {
		return nil
	}
	defer lock.Release()
	speech.KillSpeech()
	log := events.Open(cfg.StateDir)
	if tmux.HasSession(cfg.Session) {
		if err := tmux.SendEscape(cfg.Session); err != nil {
			return err
		}
		time.Sleep(400 * time.Millisecond) // the input is back ~0.3 s after Escape
	}
	_ = log.Append("interrupt", nil)
	_ = log.Session("✋ interrupted")
	return listenAndInject(cfg, out)
}

// micLock takes the cross-process microphone lock without waiting. A press
// that arrives while a recording is running (key auto-repeat, an impatient
// second tap) is ignored.
func micLock(cfg config.Config, out io.Writer) (*speech.MicLock, bool) {
	if cfg.Mute {
		return nil, false
	}
	lock, err := speech.AcquireMic(context.Background(), speech.MicLockPath(cfg), false)
	if err != nil {
		fmt.Fprintln(out, "(already listening)")
		return nil, true
	}
	return lock, false
}

func listenAndInject(cfg config.Config, out io.Writer) error {
	speech.KillSpeech()
	log := events.Open(cfg.StateDir)
	listener, err := speech.NewListener(cfg, false) // the caller holds the lock
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.AskMax+30)*time.Second)
	defer cancel()
	inSession := tmux.HasSession(cfg.Session)
	if inSession {
		tmux.SetState(cfg.Session, tmux.StateListening, "")
	}
	heard, err := listener.Listen(ctx)
	if inSession {
		tmux.SetState(cfg.Session, tmux.StateIdle, "")
	}
	payload := map[string]any{"heard": heard}
	if err != nil {
		payload["error"] = err.Error()
		_ = log.Append("listen", payload)
		if inSession {
			tmux.Message(cfg.Session, "🎙 could not listen: "+err.Error())
		}
		return err
	}
	if heard.Text != "" && repeated(cfg, heard.Text) {
		heard.Dropped, heard.Text = "same sentence as a moment ago, not sent twice", ""
		payload["heard"] = heard
	}
	_ = log.Append("listen", payload)
	if heard.Text == "" {
		_ = log.Session("🎙 (nothing sent: " + heard.Dropped + ")")
		if inSession {
			tmux.Message(cfg.Session, "🎙 nothing sent: "+heard.Dropped)
		}
		fmt.Fprintf(out, "(nothing sent: %s)\n", heard.Dropped)
		return nil
	}
	_ = log.Session("🎙 " + heard.Text)
	if inSession {
		if err := tmux.SendPrompt(cfg.Session, heard.Text); err != nil {
			return fmt.Errorf("transcribed %q but could not send it to Claude: %v", heard.Text, err)
		}
		tmux.Message(cfg.Session, "🎙 sent: "+heard.Text)
	}
	fmt.Fprintln(out, heard.Text)
	return nil
}

// repeatWindow is how long an identical transcript counts as a repeat.
const repeatWindow = 5 * time.Second

// repeated reports whether text equals the previous transcript heard
// within repeatWindow, and records text as the latest one.
func repeated(cfg config.Config, text string) bool {
	path := filepath.Join(cfg.UserDir, "last_heard")
	now := time.Now()
	same := false
	if data, err := os.ReadFile(path); err == nil {
		if ts, prev, ok := strings.Cut(string(data), "\n"); ok {
			if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				same = prev == text && now.Sub(t) < repeatWindow
			}
		}
	}
	_ = os.MkdirAll(cfg.UserDir, 0o755)
	_ = os.WriteFile(path, []byte(now.Format(time.RFC3339Nano)+"\n"+text), 0o644)
	return same
}

// voiceConfig resolves the repository for a voice command.
func voiceConfig(args []string) config.Config {
	root := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--root" && i+1 < len(args) {
			root = args[i+1]
		}
	}
	if root == "" {
		session := os.Getenv("PAIR_SESSION")
		if session == "" {
			session = config.DefaultSession
		}
		if r := tmux.Environment(session, "PAIR_ROOT"); r != "" {
			root = r
		}
	}
	if root == "" {
		root, _ = os.Getwd()
	}
	root = filepath.Clean(strings.TrimSpace(root))
	return config.Load(root)
}
