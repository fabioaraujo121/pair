package cli

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/fetch"
	"github.com/fabioaraujo121/pair/internal/speech"
)

// fakeTools puts stand-ins for every tool `pair setup` looks for on PATH,
// and nothing else: the real tmux, curl and sox are out of reach. Each
// fake appends its arguments to <dir>/<name>.calls; `say` also keeps what
// it was given to speak in say.txt, `rec` copies $FAKE_REC_WAV to the
// output file it was asked for, `whisper-cli` prints $FAKE_TRANSCRIPT.
func fakeTools(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("FAKE_DIR", dir)
	record := `printf '%s\n' "$*" >> "$FAKE_DIR/$(basename "$0").calls"` + "\n"
	scripts := map[string]string{
		"claude":      record,
		"git":         record,
		"afplay":      record,
		"tmux":        record + "exit 1\n",
		"curl":        record + "exit 1\n",
		"nvim":        record + "echo 'NVIM v0.11.0'\n",
		"say":         record + `cat >> "$FAKE_DIR/say.txt"; echo >> "$FAKE_DIR/say.txt"` + "\n",
		"whisper-cli": record + "echo 'ggml_metal_init: noise' >&2\nprintf '\\n %s\\n' \"$FAKE_TRANSCRIPT\"\n",
		"rec": record + `for a in "$@"; do case "$a" in *.wav) out="$a";; esac; done
if [ -n "$FAKE_REC_WAV" ]; then cp "$FAKE_REC_WAV" "$out"; fi
`,
	}
	for name, body := range scripts {
		// PATH holds only the fakes: the scripts need the system's own tools.
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nPATH=/usr/bin:/bin\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// pinnedModel puts a file of the default model's pinned size in the
// isolated home: a sparse file, so it costs no disk and no download.
func pinnedModel(t *testing.T, home string) string {
	t.Helper()
	pin, err := fetch.Find(fetch.Whisper, config.DefaultWhisperModel)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".pair", "models", pin.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(pin.Size); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return path
}

func calls(dir, name string) string {
	data, _ := os.ReadFile(filepath.Join(dir, name+".calls"))
	return string(data)
}

// A script, a hook, a CI job: no terminal. Setup does what needs no one at
// the keyboard, says what it skipped, and touches neither tmux nor the
// network nor the working directory. Running it again changes nothing.
func TestSetupWithoutATerminal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PAIR_SKIP_MIC", "1")
	cwd := t.TempDir()
	t.Chdir(cwd)
	bins := fakeTools(t)
	pinnedModel(t, home)

	for run := 1; run <= 2; run++ {
		var out bytes.Buffer
		if err := Setup(&out); err != nil {
			t.Fatalf("run %d: %v\n%s", run, err, out.String())
		}
		text := out.String()
		for _, want := range []string{
			"✓ settings", "✓ pair.nvim", "✓ hotkeys", "✓ claude", "✓ tmux", "✓ whisper-cli", "✓ nvim",
			"✓ whisper model", "! mic and voice  skipped: PAIR_SKIP_MIC is set",
			"ready: cd into a repository and run pair. Press F5 to talk (fn F5 on a laptop). For a more natural voice: pair voice setup kokoro",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("run %d: output lacks %q:\n%s", run, want, text)
			}
		}
		if strings.Contains(text, "✗") || strings.Contains(text, "downloading") {
			t.Errorf("run %d: unexpected failure or download:\n%s", run, text)
		}
	}
	for _, p := range []string{filepath.Join(home, ".pair", "nvim", "lua", "pair", "init.lua"), filepath.Join(home, ".pair", "hammerspoon.lua")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("not installed: %s", p)
		}
	}
	if calls(bins, "tmux") != "" || calls(bins, "curl") != "" || calls(bins, "rec") != "" || calls(bins, "say") != "" {
		t.Fatalf("setup ran tmux, curl, rec or say without a terminal: tmux=%q curl=%q rec=%q say=%q",
			calls(bins, "tmux"), calls(bins, "curl"), calls(bins, "rec"), calls(bins, "say"))
	}
	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		t.Fatalf("the working directory was written to: %v", entries)
	}

	// A missing required tool stops it, with the one line that installs it.
	_ = os.Remove(filepath.Join(bins, "rec"))
	_ = os.Remove(filepath.Join(bins, "whisper-cli"))
	var out bytes.Buffer
	err := Setup(&out)
	if err == nil || !strings.Contains(err.Error(), "brew install sox whisper.cpp") {
		t.Fatalf("missing tools: err=%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "✗ rec") || strings.Contains(out.String(), "whisper model") {
		t.Fatalf("setup must report the missing tool and stop there:\n%s", out.String())
	}
}

// At a terminal, with the tools faked: the microphone is tested, the voice
// speaks, and what the fake whisper "hears" is said back and printed.
func TestSetupListensAndSaysBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	bins := fakeTools(t)
	pinnedModel(t, home)
	atTerminal = func() bool { return true }
	t.Cleanup(func() { atTerminal = stdinIsTerminal })

	// Quiet enough for the ambient check (under the 1% silence level),
	// loud enough to count as speech for the listener.
	n := 16000 * 3 / 2
	samples := make([]int16, n)
	for i := range samples {
		samples[i] = int16(0.01 * 32767 * math.Sin(2*math.Pi*440*float64(i)/16000))
	}
	wav := filepath.Join(bins, "voice.wav")
	if err := speech.WriteWAV(wav, 16000, samples); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_REC_WAV", wav)
	t.Setenv("FAKE_TRANSCRIPT", "Obra Flow")

	var out bytes.Buffer
	if err := Setup(&out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	text := out.String()
	for _, want := range []string{"✓ microphone", "✓ voice          system voice Samantha", "✓ read-back      I heard: Obra Flow", "ready:"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	spoken, _ := os.ReadFile(filepath.Join(bins, "say.txt"))
	for _, want := range []string{"This is the voice I will use.", "Say your project's name after the tone.", "I heard: Obra Flow"} {
		if !strings.Contains(string(spoken), want) {
			t.Errorf("not spoken: %q; say got:\n%s", want, spoken)
		}
	}
	if !strings.Contains(calls(bins, "say"), "-v Samantha") {
		t.Errorf("say was not given the configured voice: %q", calls(bins, "say"))
	}
	// Two recordings: the one-second test, then the read-back, in 16-bit.
	if rec := calls(bins, "rec"); strings.Count(rec, "\n") != 2 || !strings.Contains(rec, "trim 0 1") || !strings.Contains(rec, "silence 1 0.1") || strings.Count(rec, "-b 16") != 2 {
		t.Errorf("rec calls:\n%s", rec)
	}
	if !strings.Contains(calls(bins, "whisper-cli"), filepath.Join(home, ".pair", "models", config.DefaultWhisperModel)) {
		t.Errorf("whisper-cli was not given the default model: %q", calls(bins, "whisper-cli"))
	}
	if calls(bins, "tmux") != "" {
		t.Fatalf("setup ran tmux: %q", calls(bins, "tmux"))
	}
	if _, err := os.Stat(filepath.Join(home, ".pair", "setup-input.wav")); err == nil {
		t.Fatal("the read-back recording was kept")
	}

	// Nothing said: the reason is printed, and setup still finishes.
	t.Setenv("FAKE_TRANSCRIPT", "Thank you.") // whisper's stock phrase for silence
	out.Reset()
	if err := Setup(&out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "! read-back      nothing heard: whisper returned a stock phrase") || !strings.Contains(out.String(), "ready:") {
		t.Fatalf("a silent read-back must be reported and not fail setup:\n%s", out.String())
	}
}
