package speech

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pair "github.com/fabioaraujo121/pair"
	"github.com/fabioaraujo121/pair/internal/testenv"
)

// A stand-in for the worker, speaking its protocol with the system
// Python. Each "sentence file" holds the sentence itself, so the stand-in
// for the player can record what was played, in order.
const fakeWorker = `
import sys, json, os, time
ok = os.environ.get("FAKE_READY", "1") == "1"
print(json.dumps({"ready": ok, "voices": ["af_heart", "pf_dora"], "error": "model file is corrupt"}), flush=True)
if not ok:
    sys.exit(1)
for line in sys.stdin:
    req = json.loads(line)
    open(os.path.join(os.environ["FAKE_DIR"], "request.json"), "w").write(line)
    if os.environ.get("FAKE_ERROR"):
        print(json.dumps({"id": req["id"], "done": True, "error": "synthesis failed"}), flush=True)
        continue
    parts = [p for p in req["text"].replace(". ", ".|").split("|") if p]
    for i, p in enumerate(parts):
        if os.path.exists(os.path.join(req["dir"], "%s.cancel" % req["id"])):
            break
        time.sleep(float(os.environ.get("FAKE_SYNTH", "0")))
        path = os.path.join(req["dir"], "%s-%d.wav" % (req["id"], i))
        open(path, "w").write(p)
        print(json.dumps({"id": req["id"], "chunk": i, "path": path, "seconds": 1.0}), flush=True)
    print(json.dumps({"id": req["id"], "done": True}), flush=True)
`

const fakePlayer = `#!/bin/sh
cat "$1" >> "$FAKE_DIR/played"
echo >> "$FAKE_DIR/played"
if [ -n "$FAKE_PLAY" ]; then sleep "$FAKE_PLAY"; fi
`

type recordingSpeaker struct{ spoken []string }

func (r *recordingSpeaker) Speak(_ context.Context, t string) error {
	r.spoken = append(r.spoken, t)
	return nil
}

func fakeKokoro(t *testing.T) (*Kokoro, *recordingSpeaker, string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	t.Setenv("FAKE_DIR", dir)
	worker := filepath.Join(dir, "worker.py")
	player := filepath.Join(dir, "player")
	_ = os.WriteFile(worker, []byte(fakeWorker), 0o644)
	_ = os.WriteFile(player, []byte(fakePlayer), 0o755)
	fb := &recordingSpeaker{}
	k := &Kokoro{
		Voice:    "am_michael",
		Speed:    1.1,
		Player:   player,
		OutDir:   filepath.Join(dir, "pair-tts"),
		Command:  []string{python, "-u", worker},
		Fallback: fb,
	}
	t.Cleanup(k.Close)
	return k, fb, dir
}

func played(t *testing.T, dir string) []string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(dir, "played"))
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestKokoroPlaysSentencesInOrder(t *testing.T) {
	k, fb, dir := fakeKokoro(t)
	if err := k.Speak(context.Background(), "I will add the validation in the model. The controller accepts empty emails."); err != nil {
		t.Fatal(err)
	}
	got := played(t, dir)
	if len(got) != 2 || got[0] != "I will add the validation in the model." || got[1] != "The controller accepts empty emails." {
		t.Fatalf("played: %q", got)
	}
	req, _ := os.ReadFile(filepath.Join(dir, "request.json"))
	for _, want := range []string{`"voice":"am_michael"`, `"lang":"en-us"`, `"speed":1.1`} {
		if !strings.Contains(string(req), want) {
			t.Fatalf("request %s lacks %s", req, want)
		}
	}
	// The sentence files are removed once played.
	if left, _ := filepath.Glob(filepath.Join(k.OutDir, "*.wav")); len(left) != 0 {
		t.Fatalf("files left behind: %v", left)
	}
	// The worker stays up for the next sentence.
	if err := k.Speak(context.Background(), "Done."); err != nil {
		t.Fatal(err)
	}
	if got := played(t, dir); len(got) != 3 || got[2] != "Done." {
		t.Fatalf("second call: %q", got)
	}
	if len(fb.spoken) != 0 {
		t.Fatalf("the fallback voice spoke: %q", fb.spoken)
	}
	if v, err := k.Voices(); err != nil || len(v) != 2 {
		t.Fatalf("voices: %v %v", v, err)
	}
}

// waitFor polls until cond holds, failing the test after a generous delay:
// these tests check what happened, not how fast the machine was.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestKokoroSynthesizesWhileTheAttentionSoundPlays(t *testing.T) {
	k, _, dir := fakeKokoro(t)
	t.Setenv("FAKE_SYNTH", "0.2")

	job := k.Prepare(context.Background(), "One sentence to say.")
	// Nothing has asked for the sentence to be played yet: if its file
	// appears, synthesis is running on its own, which is the point.
	waitFor(t, "the sentence to be synthesized before Play", func() bool {
		files, _ := filepath.Glob(filepath.Join(k.OutDir, "*-0.wav"))
		return len(files) == 1
	})
	if got := played(t, dir); len(got) != 0 {
		t.Fatalf("played before Play was called: %q", got)
	}
	if err := job.Play(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := played(t, dir); len(got) != 1 || got[0] != "One sentence to say." {
		t.Fatalf("played: %q", got)
	}
}

func TestKokoroIsInterrupted(t *testing.T) {
	k, _, dir := fakeKokoro(t)
	t.Setenv("FAKE_PLAY", "30")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- k.Speak(ctx, "A long first sentence. A second one that must never be heard.")
	}()
	// Interrupt once the first sentence is being played.
	waitFor(t, "the first sentence to start playing", func() bool { return len(played(t, dir)) == 1 })
	cancel()
	select {
	case err := <-result:
		if err != ErrInterrupted {
			t.Fatalf("want ErrInterrupted, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the speech did not stop when interrupted")
	}
	if got := played(t, dir); len(got) != 1 {
		t.Fatalf("only the first sentence may have started: %q", got)
	}
	// The next sentence is not disturbed by what was left of the first.
	t.Setenv("FAKE_PLAY", "")
	if err := k.Speak(context.Background(), "Back to work."); err != nil {
		t.Fatal(err)
	}
	got := played(t, dir)
	if got[len(got)-1] != "Back to work." {
		t.Fatalf("after the interruption: %q", got)
	}
	for _, g := range got {
		if strings.Contains(g, "never be heard") {
			t.Fatalf("the interrupted sentence was played later: %q", got)
		}
	}
}

func TestKokoroFallsBackToTheSystemVoice(t *testing.T) {
	var warnings []string
	fb := &recordingSpeaker{}

	// Not installed.
	k := &Kokoro{Dir: t.TempDir(), Fallback: fb, Warn: func(m string) { warnings = append(warnings, m) }}
	for _, s := range []string{"First.", "Second."} {
		if err := k.Speak(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	if len(fb.spoken) != 2 || len(warnings) != 1 || !strings.Contains(warnings[0], "pair voice setup kokoro") {
		t.Fatalf("spoken=%q warnings=%q", fb.spoken, warnings)
	}

	// The model does not load.
	k2, fb2, _ := fakeKokoro(t)
	t.Setenv("FAKE_READY", "0")
	if err := k2.Speak(context.Background(), "Still heard."); err != nil {
		t.Fatal(err)
	}
	if len(fb2.spoken) != 1 || fb2.spoken[0] != "Still heard." {
		t.Fatalf("model failure: %q", fb2.spoken)
	}

	// Synthesis fails for one sentence.
	t.Setenv("FAKE_READY", "1")
	k3, fb3, dir := fakeKokoro(t)
	t.Setenv("FAKE_ERROR", "1")
	if err := k3.Speak(context.Background(), "Heard anyway."); err != nil {
		t.Fatal(err)
	}
	if len(fb3.spoken) != 1 || len(played(t, dir)) != 0 {
		t.Fatalf("synthesis failure: fallback=%q played=%q", fb3.spoken, played(t, dir))
	}
}

func TestKokoroVoiceAndLanguage(t *testing.T) {
	for lang, voice := range map[string]string{"en": "af_heart", "pt": "pf_dora", "es": "ef_dora", "xx": "af_heart"} {
		if got := DefaultKokoroVoice(lang); got != voice {
			t.Errorf("DefaultKokoroVoice(%s) = %s", lang, got)
		}
	}
	for voice, lang := range map[string]string{"af_heart": "en-us", "bm_george": "en-gb", "pf_dora": "pt-br", "ef_dora": "es", "": "en-us"} {
		if got := KokoroLanguage(voice); got != lang {
			t.Errorf("KokoroLanguage(%s) = %s", voice, got)
		}
	}
}

// TestRealKokoro runs the installed engine: it synthesizes a narration and
// checks the audio that would be played.
//
// It is asked for with PAIR_REAL_AUDIO=1. The installation is only read:
// the worker script runs from a temporary directory.
func TestRealKokoro(t *testing.T) {
	if !testenv.RealAudio {
		t.Skip("set PAIR_REAL_AUDIO=1 to run the installed engine")
	}
	dir := t.TempDir()
	t.Setenv("FAKE_DIR", dir)
	keeper := filepath.Join(dir, "player")
	_ = os.WriteFile(keeper, []byte("#!/bin/sh\ncp \"$1\" \"$FAKE_DIR/$(basename \"$1\")\"\n"), 0o755)
	installed := &Kokoro{Dir: filepath.Join(testenv.RealHome, ".pair", "kokoro")}
	if ok, why := installed.Installed(); !ok {
		t.Skip("Kokoro is not installed: " + why)
	}
	worker := filepath.Join(dir, "worker.py")
	if err := os.WriteFile(worker, []byte(pair.KokoroWorker), 0o644); err != nil {
		t.Fatal(err)
	}
	k := &Kokoro{Voice: "af_heart", Speed: 1, Player: keeper, OutDir: filepath.Join(dir, "pair-tts"),
		Command: []string{filepath.Join(installed.Dir, "venv", "bin", "python"), "-u", worker,
			filepath.Join(installed.Dir, installed.modelFile()), filepath.Join(installed.Dir, "voices-v1.0.bin")}}
	defer k.Close()
	k.Warm()
	start := time.Now()
	if err := k.Speak(context.Background(), "I'll add the validation in the model. The controller accepts empty emails."); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.wav"))
	if len(files) != 2 {
		t.Fatalf("want two sentence files, got %v", files)
	}
	total := 0.0
	for _, f := range files {
		a, err := AnalyzeWAV(f)
		if err != nil || a.Seconds < 1 || a.RMS < 0.01 {
			t.Fatalf("%s: %+v %v", f, a, err)
		}
		total += a.Seconds
	}
	t.Logf("%.1fs of speech synthesized in %s", total, time.Since(start).Round(10*time.Millisecond))
}

// An interruption that arrives when the worker has already finished
// leaves its marker behind. Requests are numbered, and the marker must not
// silence the one that gets that number later.
func TestALeftoverInterruptionDoesNotSilenceTheNextSentence(t *testing.T) {
	k, fb, dir := fakeKokoro(t)
	if err := os.MkdirAll(k.OutDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"1", "2", "3"} {
		_ = os.WriteFile(filepath.Join(k.OutDir, id+".cancel"), nil, 0o600)
	}
	for _, text := range []string{"First sentence.", "Second sentence.", "Third sentence."} {
		if err := k.Speak(context.Background(), text); err != nil {
			t.Fatal(err)
		}
	}
	got := played(t, dir)
	if len(got) != 3 || got[0] != "First sentence." || got[2] != "Third sentence." {
		t.Fatalf("sentences were dropped: %q", got)
	}
	if len(fb.spoken) != 0 {
		t.Fatalf("the fallback voice spoke: %q", fb.spoken)
	}
}

func TestPlayingTwiceDoesNotBreakTheVoice(t *testing.T) {
	k, _, dir := fakeKokoro(t)
	job := k.Prepare(context.Background(), "Only once.")
	if err := job.Play(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		_ = job.Play(context.Background()) // must not unlock what it does not hold
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a second Play never returned")
	}
	if err := k.Speak(context.Background(), "Still working."); err != nil {
		t.Fatal(err)
	}
	if got := played(t, dir); len(got) < 2 || got[len(got)-1] != "Still working." {
		t.Fatalf("played: %q", got)
	}
}

func TestEveryProcessHasItsOwnDirectoryForSound(t *testing.T) {
	if got := TTSOutDir(); filepath.Base(got) != strconv.Itoa(os.Getpid()) || filepath.Base(filepath.Dir(got)) != "pair-tts" {
		t.Fatalf("TTSOutDir() = %s", got)
	}
}
