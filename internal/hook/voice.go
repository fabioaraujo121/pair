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
	return listenAndInject(cfg, out)
}

// Interrupt stops Claude mid-turn and hands the microphone to the engineer:
// kill `say`, send Escape, wait for the prompt, then Listen.
func Interrupt(args []string, out io.Writer) error {
	cfg := voiceConfig(args)
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

func listenAndInject(cfg config.Config, out io.Writer) error {
	speech.KillSpeech()
	log := events.Open(cfg.StateDir)
	var listener speech.Listener = speech.NewVoiceListener(cfg)
	if cfg.Mute {
		listener = speech.Deaf{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.AskMax+30)*time.Second)
	defer cancel()
	heard, err := listener.Listen(ctx)
	payload := map[string]any{"heard": heard}
	if err != nil {
		payload["error"] = err.Error()
		_ = log.Append("listen", payload)
		return err
	}
	_ = log.Append("listen", payload)
	if heard.Text == "" {
		_ = log.Session("🎙 (nothing heard: " + heard.Dropped + ")")
		fmt.Fprintf(out, "(nothing heard: %s)\n", heard.Dropped)
		return nil
	}
	_ = log.Session("🎙 " + heard.Text)
	if tmux.HasSession(cfg.Session) {
		if err := tmux.SendPrompt(cfg.Session, heard.Text); err != nil {
			return fmt.Errorf("transcribed %q but could not send it to Claude: %v", heard.Text, err)
		}
	}
	fmt.Fprintln(out, heard.Text)
	return nil
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
