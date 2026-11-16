package hook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	return listenAndInject(cfg, out, true, 0) // an explicit press: the microphone waits
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
	case "quit":
		return Quit(args[1:], out)
	}
	return nil // a click anywhere else on the status bar
}

// Quit ends the session, the agent included, the way `pair stop` does. It
// takes two presses within a few seconds: one key or click must not end
// a session by accident.
func Quit(args []string, out io.Writer) error {
	cfg := voiceConfig(args)
	if !quitWantsStop(cfg.RunDir, time.Now()) {
		if tmux.HasSession(cfg.Session) {
			tmux.Message(cfg.Session, "⏻ press quit again within 4 seconds to end the session")
		}
		fmt.Fprintln(out, "press quit again to end the session")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// `pair stop` from the repository, told which session this is. The
	// hook runs from the tmux server, not from inside the session, so it
	// survives the session it ends.
	stop := exec.Command(exe, "stop")
	stop.Dir = cfg.Root
	stop.Env = append(os.Environ(), "PAIR_SESSION="+cfg.Session, "PAIR_TMUX_SOCKET="+tmux.Socket)
	result, err := stop.CombinedOutput()
	fmt.Fprint(out, string(result))
	return err
}

// quitMarker records a press of quit; a second one within quitWindow ends
// the session.
const (
	quitMarker = "quit.press"
	quitWindow = 4 * time.Second
)

// quitWantsStop records a press and reports whether it is the second
// within the window.
func quitWantsStop(dir string, now time.Time) bool {
	marker := filepath.Join(dir, quitMarker)
	if fi, err := os.Stat(marker); err == nil && now.Sub(fi.ModTime()) < quitWindow && now.After(fi.ModTime()) {
		_ = os.Remove(marker)
		return true
	}
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(marker, nil, 0o600)
	_ = os.Chtimes(marker, now, now)
	return false
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

// Interrupt is the interrupt key, in two stages. While the agent speaks,
// one press only skips the sentence: the agent goes on working. A second
// press within interruptWindow, or a press when nothing is being spoken,
// stops the agent mid-turn and hands the microphone to the engineer: kill
// the speech, press the agent's interrupt key, wait for the prompt, then
// listen. That microphone closes by itself when nothing is said into it.
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
	log := events.Open(cfg.StateDir)
	switch decideInterrupt(cfg.RunDir, speech.Speaking(cfg.RunDir), time.Now()) {
	case interruptEcho:
		return nil
	case interruptSkip:
		speech.KillSpeech(cfg.RunDir) // the speaker's own process puts the state back to working
		_ = log.Append("skip", nil)
		if tmux.HasSession(cfg.Session) {
			tmux.Message(cfg.Session, "🔇 skipped · "+cfg.Keys.Interrupt+" again to interrupt the turn")
		}
		fmt.Fprintln(out, "skipped")
		return nil
	}
	speech.KillSpeech(cfg.RunDir)
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
	return listenAndInject(cfg, out, false, interruptLeadIn)
}

// interruptDecision is what a press of the interrupt key asks for.
type interruptDecision int

const (
	interruptEcho interruptDecision = iota // the key is held down: nothing
	interruptSkip                          // silence the sentence; the agent goes on
	interruptTurn                          // cancel the agent's turn, then listen
)

const (
	skipMarker      = "interrupt.press"
	interruptWindow = 2 * time.Second        // a second press this soon after a skip cancels the turn
	interruptRepeat = 500 * time.Millisecond // presses closer than this are one key held down
	interruptLeadIn = 5 * time.Second        // a microphone opened by the interrupt key closes after this much silence
)

// decideInterrupt records a press of the interrupt key and says what it
// asks for. The two presses that cancel a turn come from two processes,
// so the first is remembered in a file: its name's time is the last
// press, the key's echoes included, and its content the press that
// skipped the sentence.
func decideInterrupt(dir string, speaking bool, now time.Time) interruptDecision {
	marker := filepath.Join(dir, skipMarker)
	if fi, err := os.Stat(marker); err == nil && now.After(fi.ModTime()) {
		first := fi.ModTime()
		if data, err := os.ReadFile(marker); err == nil {
			if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data))); err == nil {
				first = t
			}
		}
		switch {
		case now.Sub(fi.ModTime()) < interruptRepeat:
			_ = os.Chtimes(marker, now, now)
			return interruptEcho
		case now.Sub(first) < interruptWindow:
			_ = os.Remove(marker)
			return interruptTurn
		}
	}
	if !speaking {
		_ = os.Remove(marker)
		return interruptTurn
	}
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(marker, []byte(now.Format(time.RFC3339Nano)+"\n"), 0o600)
	_ = os.Chtimes(marker, now, now)
	return interruptSkip
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
// agent's input as a new prompt. With leadIn, a microphone nobody speaks
// into closes after that long; zero waits for the first word.
func listenAndInject(cfg config.Config, out io.Writer, answerAsk bool, leadIn time.Duration) error {
	speech.KillSpeech(cfg.RunDir)
	log := events.Open(cfg.StateDir)
	listener, err := speech.NewListener(cfg, false) // the caller holds the lock
	if err != nil {
		return err
	}
	if vl, ok := listener.(*speech.VoiceListener); ok { // a muted session has no microphone to close
		vl.Rec.LeadIn = leadIn
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
			status.ListeningDone(cfg, tmux.StateAsking, "press "+cfg.Keys.Talk+" or click talk to answer")
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
			sent(cfg)
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
		sent(cfg)
	}
	fmt.Fprintln(out, heard.Text)
	return nil
}

// sent is the soft tick that says the words went through. The agent's
// own first sentence, a few seconds later, is the acknowledgement that
// means something; a canned phrase in its voice sounded like a machine.
func sent(cfg config.Config) {
	if !cfg.Mute && cfg.Cue {
		chime("Pop")
	}
}

// greetedMarker, under ~/.pair, says the greeting has been spoken on this
// machine.
const greetedMarker = "greeted"

// WantsGreeting reports whether the first-session sentence is still to be
// spoken: never before on this machine, not turned off, not muted.
func WantsGreeting(cfg config.Config) bool {
	if !cfg.Greeting || cfg.Mute {
		return false
	}
	_, err := os.Stat(filepath.Join(cfg.UserDir, greetedMarker))
	return err != nil
}

// Greeting is the one sentence pair says for itself, at the first session
// ever: which key to press. The agent's canned phrases were taken out for
// sounding like a machine; this is not the agent, and it is said once.
func Greeting(talk string) string {
	return "Press " + talk + ", or fn " + talk + " on a laptop, and tell me what to do."
}

// Greet speaks the greeting, once per machine, from a process of its own
// so the start it belongs to goes on meanwhile. It holds no microphone
// lock and leaves the status alone: it is not the agent speaking. The
// marker is written after the sentence, so a start that dies before it
// tries again next time; a sentence cut short by the talk key counts as
// heard.
func Greet(args []string, out io.Writer) error {
	cfg := voiceConfig(args)
	if !WantsGreeting(cfg) {
		return nil
	}
	speaker, err := speech.NewSpeaker(cfg)
	if err != nil {
		return err
	}
	text := Greeting(cfg.Keys.Talk)
	log := events.Open(cfg.StateDir)
	_ = log.Append("greeting", map[string]any{"text": text})
	_ = log.Session("pair: press " + cfg.Keys.Talk + " to talk")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := speaker.Speak(ctx, text); err != nil && !errors.Is(err, speech.ErrInterrupted) {
		return err
	}
	_ = os.MkdirAll(cfg.UserDir, 0o755)
	if err := os.WriteFile(filepath.Join(cfg.UserDir, greetedMarker), []byte(time.Now().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Fprintln(out, text)
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
