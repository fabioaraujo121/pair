package hook

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fabioaraujo121/pair/internal/agent"
	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/events"
	"github.com/fabioaraujo121/pair/internal/speech"
	"github.com/fabioaraujo121/pair/internal/status"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

// Voice subcommands are started by a keymap or a global hotkey, not by
// the agent, so they read no stdin. They find the repository through
// --root, the tmux session's PAIR_ROOT, or the working directory.

// Listen is push-to-talk: silence any speech, record until the engineer
// pauses, transcribe, and type the transcript into the agent's pane.
func Listen(args []string, out io.Writer) error {
	cfg := voiceConfig(args)
	wantsStop := speech.PressWantsStop(cfg.RunDir, time.Now())
	lock, busy := micLock(cfg, out)
	if busy {
		// The microphone is open: a new press of the key, or a click on
		// the button that now reads "stop", ends the recording. What was
		// said is transcribed and sent by the command that is recording.
		if wantsStop {
			speech.KillRecording(cfg.RunDir)
			fmt.Fprintln(out, "(recording stopped)")
		}
		return nil
	}
	defer lock.Release()
	return listenAndInject(cfg, out, true)
}

// Click handles a click on one of the status bar buttons.
func Click(args []string, out io.Writer) error {
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "talk":
		return Listen(args[1:], out)
	case "stop":
		return Interrupt(args[1:], out)
	case "agent", "claude": // "claude" was the button's name when Claude Code was the only agent
		return ToggleAgent(args[1:], out)
	}
	return nil // a click anywhere else on the status bar
}

// ToggleAgent shows the agent's pane under the editor, or hides it again.
// From then on the pane is the engineer's: pair no longer hides it by
// itself.
func ToggleAgent(args []string, out io.Writer) error {
	for _, a := range args {
		if a == "--if-waiting" {
			return showAgentIfWaiting(voiceConfig(args), out)
		}
	}
	cfg := voiceConfig(args)
	_ = os.Remove(filepath.Join(cfg.StateDir, "claude_autoshown"))
	visible, err := tmux.ToggleAgent(cfg.Session, cfg.ClaudeHeight)
	if err != nil {
		return err
	}
	who := strings.ToLower(status.Who(cfg))
	if visible {
		fmt.Fprintln(out, who+" shown")
	} else {
		fmt.Fprintln(out, who+" hidden")
	}
	return nil
}

// Interrupt stops the agent mid-turn and hands the microphone to the
// engineer: kill `say`, press the agent's interrupt key, wait for the
// prompt, then Listen.
func Interrupt(args []string, out io.Writer) error {
	cfg := voiceConfig(args)
	// The lock comes first: a key that auto-repeats must not send the
	// interrupt key several times (two Escapes in a row open a menu in
	// Claude Code and edit the last message in Codex).
	lock, busy := micLock(cfg, out)
	if busy {
		return nil
	}
	defer lock.Release()
	speech.KillSpeech(cfg.RunDir)
	log := events.Open(cfg.StateDir)
	if tmux.HasSession(cfg.Session) {
		ag, _ := agent.For(cfg.Agent)
		if err := tmux.SendKeys(cfg.Session, ag.InterruptKeys()...); err != nil {
			return err
		}
		time.Sleep(400 * time.Millisecond) // the input is back ~0.3 s after the key
	}
	_ = log.Append("interrupt", nil)
	_ = log.Session("✋ interrupted")
	speech.ClearPendingAsk(cfg.StateDir) // the question was cancelled with the turn
	return listenAndInject(cfg, out, false)
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
		return nil, true
	}
	return lock, false
}

// listenAndInject records once and routes the transcript: to the question
// the agent is waiting on when there is one (answerAsk), otherwise into the
// agent's input as a new prompt.
func listenAndInject(cfg config.Config, out io.Writer, answerAsk bool) error {
	speech.KillSpeech(cfg.RunDir)
	log := events.Open(cfg.StateDir)
	listener, err := speech.NewListener(cfg, false) // the caller holds the lock
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.AskMax+30)*time.Second)
	defer cancel()
	inSession := tmux.HasSession(cfg.Session)
	before := status.Read(cfg.StateDir)
	if inSession {
		status.Set(cfg, tmux.StateListening, "")
	}
	heard, err := listener.Listen(ctx)
	_, asked := speech.PendingAskFor(cfg.StateDir, time.Duration(cfg.AskWait+60)*time.Second)
	asked = asked && answerAsk
	if inSession {
		// The microphone closes: show what the agent reported while it
		// was open, or else where things stand.
		switch {
		case asked && heard.Text == "":
			status.ListeningDone(cfg, tmux.StateAsking, "press F5 or click talk to answer")
		case asked:
			status.ListeningDone(cfg, tmux.StateWorking, "")
		case before.State != tmux.StateIdle && before.State != tmux.StateListening:
			status.ListeningDone(cfg, before.State, before.Detail) // the agent is still at what it was doing
		default:
			status.ListeningDone(cfg, tmux.StateIdle, "")
		}
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
	if asked && heard.Text != "" {
		payload["answers"] = true
	}
	_ = log.Append("listen", payload)
	if heard.Text == "" {
		_ = log.Session("🎙 (nothing sent: " + heard.Dropped + ")")
		if inSession {
			tmux.Message(cfg.Session, "🎙 nothing sent: "+heard.Dropped)
			if heard.Dropped != "same sentence as a moment ago, not sent twice" {
				acknowledge(cfg, speech.AckMissed)
			}
		}
		fmt.Fprintf(out, "(nothing sent: %s)\n", heard.Dropped)
		return nil
	}
	if asked {
		// The agent is waiting on a question: this is its answer, not a prompt.
		if err := speech.DeliverAnswer(cfg.StateDir, heard); err != nil {
			return err
		}
		if inSession {
			tmux.Message(cfg.Session, "👤 answered: "+heard.Text)
			acknowledge(cfg, speech.AckAnswer)
		}
		fmt.Fprintln(out, heard.Text)
		return nil
	}
	_ = log.Session("🎙 " + heard.Text)
	if inSession {
		ag, _ := agent.For(cfg.Agent)
		if err := tmux.SendPrompt(cfg.Session, heard.Text, ag.SubmitDelay()); err != nil {
			return fmt.Errorf("transcribed %q but could not send it to %s: %v", heard.Text, status.Who(cfg), err)
		}
		tmux.Message(cfg.Session, "🎙 sent: "+heard.Text)
		acknowledge(cfg, speech.AckPrompt)
	}
	fmt.Fprintln(out, heard.Text)
	return nil
}

// acknowledge says, in the agent's voice, that the words arrived: the
// gap before the agent's first sentence is the longest silence of a
// session. It is spoken after the transcript is on its way, so it delays
// nothing, and it is tracked like any speech, so F5 or F6 cut it short.
func acknowledge(cfg config.Config, kind speech.AckKind) {
	if !cfg.Ack || cfg.Mute {
		return
	}
	speaker, err := speech.NewSpeaker(cfg)
	if err != nil {
		return
	}
	if closer, ok := speaker.(interface{ Close() }); ok {
		defer closer.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = speech.Acknowledge(ctx, speaker, cfg.RunDir, cfg.Lang, kind)
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
	_ = os.WriteFile(path, []byte(now.Format(time.RFC3339Nano)+"\n"+text), 0o600)
	return same
}

// voiceConfig finds the session a voice command is for: the one named by
// --root, the one the command was started from (a key pressed inside a
// session), the one of the working directory when it is running, else the
// session used last (a global hotkey pressed outside any terminal).
func voiceConfig(args []string) config.Config {
	for i := 0; i < len(args); i++ {
		if args[i] == "--root" && i+1 < len(args) {
			return config.Load(filepath.Clean(strings.TrimSpace(args[i+1])))
		}
	}
	if name := os.Getenv("PAIR_SESSION"); name != "" {
		if root := tmux.Environment(name, "PAIR_ROOT"); root != "" {
			return config.Load(root)
		}
	}
	here := config.Load("")
	if tmux.HasSession(here.Session) {
		return here
	}
	if name := tmux.RecentSession(); name != "" {
		if root := tmux.Environment(name, "PAIR_ROOT"); root != "" {
			return config.Load(root)
		}
	}
	return here
}

// showAgentIfWaiting opens the agent's pane when it sits on a dialog that
// no hook announces: the workspace trust question of a repository opened
// for the first time. Without this the session would look idle for ever,
// with the question out of sight.
func showAgentIfWaiting(cfg config.Config, out io.Writer) error {
	if cfg.Session == "" || tmux.AgentVisible(cfg.Session) {
		return nil
	}
	screen, err := tmux.Run("capture-pane", "-p", "-t", tmux.AgentPane(cfg.Session))
	if err != nil {
		return err
	}
	ag, err := agent.For(cfg.Agent)
	if err != nil {
		return err
	}
	if !ag.WaitingOnDialog(screen) {
		return nil
	}
	if err := tmux.ShowAgent(cfg.Session, cfg.ClaudeHeight); err != nil {
		return err
	}
	status.Set(cfg, tmux.StateAttention, "answer "+ag.Title()+"'s question below")
	_ = events.Open(cfg.StateDir).Session("⚠ " + ag.Title() + " is asking something before it can start: see its pane below")
	fmt.Fprintln(out, strings.ToLower(ag.Short())+" shown: it is waiting on a dialog")
	return nil
}
