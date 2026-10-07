package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	pair "github.com/fabioaraujo121/pair"
	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/fetch"
	"github.com/fabioaraujo121/pair/internal/speech"
)

// Voice is `pair voice`: which voice speaks, and how to get a better one.
func Voice(args []string, w io.Writer) error {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "", "status":
		return voiceStatus(w)
	case "setup", "install":
		if len(args) == 0 || args[0] != "kokoro" {
			return fmt.Errorf("usage: pair voice setup kokoro")
		}
		return setupKokoro(w)
	case "use":
		return voiceUse(args, w)
	case "list", "ls":
		return voiceList(w)
	case "test", "say":
		return voiceTest(strings.Join(args, " "), w)
	default:
		return fmt.Errorf("usage: pair voice [status | setup kokoro | use <say|kokoro> [voice] | list | test [text]]")
	}
}

func voiceStatus(w io.Writer) error {
	cfg := config.Load("")
	k := speech.NewKokoro(cfg)
	installed, why := k.Installed()
	switch cfg.TTS {
	case "kokoro":
		fmt.Fprintf(w, "speaking with Kokoro, voice %s, speed %.2g\n", k.Voice, k.Speed)
		if !installed {
			fmt.Fprintf(w, "! not usable: %s\n  until then the system voice %s speaks\n", why, cfg.Voice)
		}
	default:
		fmt.Fprintf(w, "speaking with the system voice %s at %d words per minute\n", cfg.Voice, cfg.Rate)
		if installed {
			fmt.Fprintln(w, "Kokoro is installed: `pair voice use kokoro` switches to it")
		} else {
			fmt.Fprintln(w, "for a more natural voice: `pair voice setup kokoro` (free, runs on this Mac, about 500 MB)")
		}
	}
	fmt.Fprintln(w, "\npair voice test          hear it\npair voice list          the voices available\npair voice use kokoro am_michael     choose one")
	return nil
}

// setupKokoro installs the engine in ~/.pair/kokoro and switches to it.
func setupKokoro(w io.Writer) error {
	cfg := config.Load("")
	dir := speech.KokoroDir(cfg)
	uv, err := exec.LookPath("uv")
	if err != nil {
		return fmt.Errorf("Kokoro needs a recent Python, which `uv` provides. Install it with `brew install uv`, then run `pair voice setup kokoro` again")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	run := func(what string, name string, args ...string) error {
		fmt.Fprintf(w, "• %s\n", what)
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Stdout, cmd.Stderr = w, w
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		return nil
	}
	python := filepath.Join(dir, "venv", "bin", "python")
	if _, err := os.Stat(python); err != nil {
		if err := run("creating a Python 3.12 environment", uv, "venv", "--quiet", "--python", "3.12", "venv"); err != nil {
			return err
		}
	}
	// The packages are pinned with the hashes of their wheels: what gets
	// installed is what was tested, whatever the index serves today.
	requirements := filepath.Join(dir, "requirements.txt")
	if err := os.WriteFile(requirements, []byte(pair.KokoroRequirements), 0o644); err != nil {
		return err
	}
	if err := run("installing kokoro-onnx (pinned)", uv, "pip", "install", "--quiet", "--python", python, "--require-hashes", "-r", requirements); err != nil {
		return err
	}
	for _, f := range []string{cfg.KokoroModel, "voices-v1.0.bin"} {
		pin, err := fetch.Find(fetch.Kokoro, f)
		if err != nil {
			return fmt.Errorf("%w: put the file in %s yourself, or use one of the pinned models", err, dir)
		}
		path := filepath.Join(dir, f)
		if pin.SizeMatches(path) {
			continue
		}
		_ = os.Remove(path) // another size: not the pinned file
		fmt.Fprintf(w, "• downloading %s (%d MB, sha256 %s…)\n", f, pin.Size>>20, pin.SHA256[:12])
		if err := pin.Download(w, path); err != nil {
			return err
		}
	}
	k := speech.NewKokoro(cfg)
	if _, err := k.WriteWorker(); err != nil {
		return err
	}
	fmt.Fprintln(w, "• loading the model")
	start := time.Now()
	voices, err := k.Voices()
	if err != nil {
		return err
	}
	defer k.Close()
	fmt.Fprintf(w, "✓ Kokoro is ready: %d voices, loaded in %s\n", len(voices), time.Since(start).Round(10*time.Millisecond))

	if err := config.SaveUser(map[string]string{"PAIR_TTS": "kokoro", "PAIR_KOKORO_VOICE": k.Voice}); err != nil {
		return err
	}
	fmt.Fprintf(w, "✓ pair now speaks with Kokoro, voice %s (%s)\n", k.Voice, config.UserConfigPath())
	fmt.Fprintln(w, "\nhear it: pair voice test      other voices: pair voice list      back to the system voice: pair voice use say")
	fmt.Fprintln(w, "A session that is running keeps its voice until it is restarted (pair stop, then pair --continue).")
	return nil
}

func voiceUse(args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pair voice use <say|kokoro> [voice]")
	}
	cfg := config.Load("")
	values := map[string]string{"PAIR_TTS": args[0]}
	switch args[0] {
	case "say":
		if len(args) > 1 {
			values["PAIR_VOICE"] = strings.Join(args[1:], " ")
		}
	case "kokoro":
		k := speech.NewKokoro(cfg)
		if ok, why := k.Installed(); !ok {
			return fmt.Errorf("%s", why)
		}
		if len(args) > 1 {
			voices, err := k.Voices()
			k.Close()
			if err != nil {
				return err
			}
			if !contains(voices, args[1]) {
				return fmt.Errorf("Kokoro has no voice %q (see `pair voice list`)", args[1])
			}
			values["PAIR_KOKORO_VOICE"] = args[1]
		}
	default:
		return fmt.Errorf("unknown voice engine %q (say, kokoro)", args[0])
	}
	if err := config.SaveUser(values); err != nil {
		return err
	}
	fmt.Fprintf(w, "✓ %s updated\n", config.UserConfigPath())
	return voiceStatus(w)
}

func voiceList(w io.Writer) error {
	cfg := config.Load("")
	if cfg.TTS != "kokoro" {
		fmt.Fprintln(w, "system voices (`say -v '?'`); Enhanced and Premium ones are downloaded in System Settings → Accessibility → Spoken Content → Manage Voices")
		cmd := exec.Command("say", "-v", "?")
		cmd.Stdout = w
		return cmd.Run()
	}
	k := speech.NewKokoro(cfg)
	voices, err := k.Voices()
	if err != nil {
		return err
	}
	defer k.Close()
	groups := []struct{ prefix, name string }{
		{"a", "American English"}, {"b", "British English"}, {"p", "Brazilian Portuguese"}, {"e", "Spanish"},
		{"f", "French"}, {"i", "Italian"}, {"j", "Japanese"}, {"z", "Mandarin"}, {"h", "Hindi"},
	}
	fmt.Fprintln(w, "The second letter is f for a female voice, m for a male one. Current:", k.Voice)
	for _, g := range groups {
		var names []string
		for _, v := range voices {
			if strings.HasPrefix(v, g.prefix) {
				names = append(names, v)
			}
		}
		if len(names) > 0 {
			fmt.Fprintf(w, "\n%s\n  %s\n", g.name, strings.Join(names, "  "))
		}
	}
	fmt.Fprintln(w, "\nchoose one: pair voice use kokoro <voice>      hear it: pair voice test")
	return nil
}

func voiceTest(text string, w io.Writer) error {
	cfg := config.Load("")
	if strings.TrimSpace(text) == "" {
		text = "I'll add the validation in the model, because the controller accepts empty emails. Then I'll run the tests."
		if cfg.Lang == "pt" {
			text = "Vou adicionar a validação no modelo, porque hoje ele aceita um endereço vazio. Depois rodo os testes."
		}
	}
	speaker, err := speech.NewSpeaker(cfg)
	if err != nil {
		return err
	}
	if k, ok := speaker.(*speech.Kokoro); ok {
		k.Warn = func(msg string) { fmt.Fprintln(w, "!", msg) }
		defer k.Close()
		loadStart := time.Now()
		k.Warm()
		fmt.Fprintf(w, "Kokoro, voice %s (model ready in %s)\n", k.Voice, time.Since(loadStart).Round(10*time.Millisecond))
	} else {
		fmt.Fprintf(w, "system voice %s\n", cfg.Voice)
	}
	start := time.Now()
	err = speaker.Speak(context.Background(), speech.Normalize(text, speech.VocabularyFor(cfg.Lang)))
	fmt.Fprintf(w, "spoken in %s\n", time.Since(start).Round(100*time.Millisecond))
	return err
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
