package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	pair "github.com/fabioaraujo121/pair"
	"github.com/fabioaraujo121/pair/internal/agent"
	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/speech"
)

// readBackMax caps the read-back recording: someone who stays quiet should
// not wait the minute a question is allowed to.
const readBackMax = 15 * time.Second

// Setup is `pair setup`: from an installed binary to the first word heard,
// in one command. It keeps what `pair init` keeps under ~/.pair, checks the
// tools, downloads the whisper model, opens the microphone once so macOS
// asks for access, speaks a sentence with the configured voice, then
// listens for one and says it back: microphone, whisper and the voice
// proven in one go. Each step prints one line with the doctor's marks.
// Running it again is safe; it needs no tmux session and starts none.
func Setup(w io.Writer) error {
	cfg := config.Load("")
	ag, err := agent.For(cfg.Agent)
	if err != nil {
		return err
	}
	failed := false
	report := func(c check) {
		if printCheck(w, c) {
			failed = true
		}
	}

	// 1. What `pair init` with no arguments does: the settings as they are,
	// the Neovim plugin, the hotkey file.
	exe, err := pairBinary()
	if err != nil {
		return err
	}
	pluginDir, hsPath, err := installUserFiles(cfg.UserDir, exe)
	if err != nil {
		return err
	}
	report(check{"settings", false, true, fmt.Sprintf("pairing with %s, speaking %s (change with `pair init --agent codex --lang pt`)", ag.Title(), cfg.Lang)})
	report(check{"pair.nvim", false, true, tildePath(pluginDir) + " (pair loads it; no Neovim configuration needed)"})
	report(check{"hotkeys", false, true, tildePath(hsPath) + " (optional, for Hammerspoon)"})
	if left := ag.Leftovers(cfg.Root); len(left) > 0 {
		report(check{"repository", false, false, "an earlier version of pair wrote " + strings.Join(left, ", ") + ": run `pair uninstall` here"})
	}

	// 2. The tools. Nothing else can be tested without a required one.
	checks, _, hasWhisper := toolChecks(ag)
	missing := false
	for _, c := range checks {
		if printCheck(w, c) {
			missing = true
		}
	}
	if missing {
		msg := "install what is missing, then run `pair setup` again"
		if line := brewLine(checks); line != "" {
			msg += ":\n  " + line
		}
		return fmt.Errorf("%s", msg)
	}

	// 3. The whisper model: the pinned download, kept only when verified.
	model := modelCheck(w, cfg, DoctorOptions{DownloadModel: true}, hasWhisper)
	report(model)

	// 4 to 6 need someone at the terminal: macOS asks for microphone access
	// with a dialog, and a sentence has to be said.
	if why := skipMic(); why != "" {
		report(check{"mic and voice", false, false, "skipped: " + why + ". From a terminal, `pair setup` also asks for microphone access, speaks, and listens once"})
		return finishSetup(w, cfg, failed)
	}

	// 4. The microphone: one second, so the permission dialog shows now.
	mic := micCheck(w, cfg)
	report(mic)

	// 5. The voice.
	speaker, engine, done, err := openSpeaker(cfg, w)
	if err != nil {
		report(check{"voice", true, false, err.Error()})
		return finishSetup(w, cfg, true)
	}
	defer done()
	say := func(text string) error {
		return speaker.Speak(context.Background(), speech.Normalize(text, speech.VocabularyFor(cfg.Lang)))
	}
	if err := say(phrase(cfg.Lang, "This is the voice I will use. You can change it with pair voice.", "Esta é a voz que vou usar. Você pode mudá-la com pair voice.")); err != nil {
		report(check{"voice", true, false, engine + ": " + err.Error()})
	} else {
		report(check{"voice", false, true, engine + " (change it with `pair voice`)"})
	}

	// 6. The read-back.
	switch {
	case !model.ok:
		report(check{"read-back", false, false, "skipped: no whisper model to transcribe with"})
	case !mic.ok && mic.required:
		report(check{"read-back", false, false, "skipped: the microphone test failed"})
	default:
		report(readBack(w, cfg, say))
	}
	return finishSetup(w, cfg, failed)
}

// readBack asks for the project's name, listens the way a session does
// (cue, record until a pause, transcribe, drop silence and whisper's stock
// phrases) and says back what it heard.
func readBack(w io.Writer, cfg config.Config, say func(string) error) check {
	_ = say(phrase(cfg.Lang, "Say your project's name after the tone.", "Diga o nome do seu projeto depois do sinal."))
	listener, err := speech.NewListener(cfg, true)
	if err != nil {
		return check{"read-back", true, false, err.Error()}
	}
	wav := filepath.Join(cfg.UserDir, "setup-input.wav")
	if l, ok := listener.(*speech.VoiceListener); ok {
		// Setup runs anywhere, a home directory included: the recording
		// goes under ~/.pair, not into a .pair/ of the working directory.
		l.WavPath = wav
		l.Rec.Max = readBackMax
	}
	defer os.Remove(wav)
	fmt.Fprintln(w, "listening… (the recording ends when you pause)")
	ctx, cancel := context.WithTimeout(context.Background(), readBackMax+30*time.Second)
	defer cancel()
	heard, err := listener.Listen(ctx)
	switch {
	case err != nil:
		return check{"read-back", true, false, err.Error()}
	case heard.Text == "" && heard.Capped:
		return check{"read-back", false, false, fmt.Sprintf("nothing heard in %s: speak after the tone, then pause", readBackMax)}
	case heard.Text == "":
		return check{"read-back", false, false, "nothing heard: " + heard.Dropped}
	}
	_ = say(phrase(cfg.Lang, "I heard: ", "Eu ouvi: ") + heard.Text)
	return check{"read-back", false, true, "I heard: " + heard.Text}
}

// finishSetup says what comes next, or what is left to fix.
func finishSetup(w io.Writer, cfg config.Config, failed bool) error {
	if failed {
		return fmt.Errorf("not ready yet: fix what is marked ✗ above and run `pair setup` again")
	}
	line := "ready: cd into a repository and run pair. Press F5 to talk (fn F5 on a laptop)."
	if cfg.TTS != "kokoro" {
		line += " For a more natural voice: pair voice setup kokoro"
	}
	fmt.Fprintln(w, "\n"+line)
	return nil
}

// phrase picks the sentence for the spoken language; English otherwise.
func phrase(lang, en, pt string) string {
	if lang == "pt" {
		return pt
	}
	return en
}

// pairBinary is the path of the running pair, symlinks resolved, for the
// files that have to run it later.
func pairBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// installUserFiles writes what pair keeps under ~/.pair for every session:
// the Neovim plugin and the Hammerspoon hotkey file, which runs the given
// pair binary. Both are overwritten so they track the binary.
func installUserFiles(userDir, exe string) (pluginDir, hsPath string, err error) {
	pluginDir, err = InstallPlugin(userDir)
	if err != nil {
		return "", "", err
	}
	hsPath = filepath.Join(userDir, "hammerspoon.lua")
	hs := strings.ReplaceAll(pair.HammerspoonTemplate, "{{PAIR_BIN}}", exe)
	if err := os.WriteFile(hsPath, []byte(hs), 0o644); err != nil {
		return "", "", err
	}
	return pluginDir, hsPath, nil
}
