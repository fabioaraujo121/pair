package speech

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sine(rate int, seconds, amplitude float64) []int16 {
	n := int(float64(rate) * seconds)
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(amplitude * 32767 * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
	}
	return out
}

func TestAnalyzeWAV(t *testing.T) {
	dir := t.TempDir()
	tone := filepath.Join(dir, "tone.wav")
	if err := WriteWAV(tone, 16000, sine(16000, 2, 0.5)); err != nil {
		t.Fatal(err)
	}
	a, err := AnalyzeWAV(tone)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(a.Seconds-2) > 0.01 || a.Rate != 16000 || math.Abs(a.RMS-0.5/math.Sqrt2) > 0.01 {
		t.Fatalf("tone: %+v", a)
	}
	silence := filepath.Join(dir, "silence.wav")
	_ = WriteWAV(silence, 16000, make([]int16, 8000))
	a, _ = AnalyzeWAV(silence)
	if a.Seconds != 0.5 || a.RMS != 0 {
		t.Fatalf("silence: %+v", a)
	}
	// truncated data chunk (recorder killed mid-write) still parses
	data, _ := os.ReadFile(tone)
	_ = os.WriteFile(tone, data[:len(data)/2], 0o644)
	a, err = AnalyzeWAV(tone)
	if err != nil || math.Abs(a.Seconds-1) > 0.01 {
		t.Fatalf("truncated: %+v %v", a, err)
	}
	if _, err := AnalyzeWAV(filepath.Join(dir, "missing.wav")); err == nil {
		t.Fatal("missing file must error")
	}
}

// wavBytes builds a mono WAV with an arbitrary encoding. extensible wraps
// the format in WAVE_FORMAT_EXTENSIBLE, the way sox writes 32-bit files.
func wavBytes(rate, bits, format int, extensible bool, payload []byte) []byte {
	fmtSize := 16
	tag := format
	if extensible {
		fmtSize, tag = 40, wavExtensible
	}
	buf := make([]byte, 0, 28+fmtSize+len(payload))
	le16 := func(v int) []byte { b := make([]byte, 2); binary.LittleEndian.PutUint16(b, uint16(v)); return b }
	le32 := func(v int) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, uint32(v)); return b }
	buf = append(buf, "RIFF"...)
	buf = append(buf, le32(20+fmtSize+len(payload))...)
	buf = append(buf, "WAVEfmt "...)
	buf = append(buf, le32(fmtSize)...)
	buf = append(buf, le16(tag)...)
	buf = append(buf, le16(1)...)
	buf = append(buf, le32(rate)...)
	buf = append(buf, le32(rate*bits/8)...)
	buf = append(buf, le16(bits/8)...)
	buf = append(buf, le16(bits)...)
	if extensible {
		buf = append(buf, le16(22)...)   // cbSize
		buf = append(buf, le16(bits)...) // valid bits
		buf = append(buf, le32(4)...)    // channel mask
		buf = append(buf, le16(format)...)
		buf = append(buf, make([]byte, 14)...) // rest of the SubFormat GUID
	}
	buf = append(buf, "data"...)
	buf = append(buf, le32(len(payload))...)
	return append(buf, payload...)
}

// TestAnalyzeWAVOtherEncodings covers the file sox really writes from the
// macOS microphone when no sample size is requested: 32-bit extensible.
func TestAnalyzeWAVOtherEncodings(t *testing.T) {
	dir := t.TempDir()
	const rate, n = 48000, 48000
	amp := 0.25
	want := amp / math.Sqrt2

	int32s := make([]byte, 4*n)
	int24s := make([]byte, 3*n)
	floats := make([]byte, 4*n)
	for i := 0; i < n; i++ {
		v := amp * math.Sin(2*math.Pi*440*float64(i)/rate)
		binary.LittleEndian.PutUint32(int32s[4*i:], uint32(int32(v*2147483647)))
		s24 := int32(v * 8388607)
		int24s[3*i], int24s[3*i+1], int24s[3*i+2] = byte(s24), byte(s24>>8), byte(s24>>16)
		binary.LittleEndian.PutUint32(floats[4*i:], math.Float32bits(float32(v)))
	}
	cases := []struct {
		name       string
		bits, tag  int
		extensible bool
		payload    []byte
	}{
		{"int32 extensible (sox from CoreAudio)", 32, wavPCM, true, int32s},
		{"int32 plain", 32, wavPCM, false, int32s},
		{"int24", 24, wavPCM, false, int24s},
		{"float32", 32, wavFloat, false, floats},
	}
	for _, c := range cases {
		path := filepath.Join(dir, "x.wav")
		if err := os.WriteFile(path, wavBytes(rate, c.bits, c.tag, c.extensible, c.payload), 0o644); err != nil {
			t.Fatal(err)
		}
		a, err := AnalyzeWAV(path)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if a.Bits != c.bits || a.Rate != rate || math.Abs(a.Seconds-1) > 0.01 || math.Abs(a.RMS-want) > 0.005 {
			t.Fatalf("%s: %+v, want 1 s at rms %.3f", c.name, a, want)
		}
	}
	// A 32-bit recording is analyzable but must not reach whisper.
	path := filepath.Join(dir, "rec32.wav")
	_ = os.WriteFile(path, wavBytes(16000, 32, wavPCM, true, int32s[:4*16000]), 0o644)
	t.Setenv("FAKE_DIR", dir)
	t.Setenv("FAKE_REC_WAV", path)
	l := &VoiceListener{Rec: &Recorder{Bin: fakeRec(t, dir), Max: 5 * time.Second}, STT: &Whisper{Bin: "/nonexistent"}, WavPath: filepath.Join(dir, "in.wav")}
	h, err := l.Listen(context.Background())
	if err != nil || h.Text != "" || !strings.Contains(h.Dropped, "32-bit") {
		t.Fatalf("32-bit recording: %+v %v", h, err)
	}
}

func TestIsHallucination(t *testing.T) {
	yes := []string{"", ".", "Obrigado.", "obrigada", "Transcrição e Legendas por Quintena Coelho", "Legendas pela comunidade Amara.org", "E aí", "Tchau!", "Thank you.", " ok "}
	no := []string{"Pode seguir com a opção A.", "Usa Sidekiq.", "não, prefiro o Solid Queue", "obrigado, mas prefiro a segunda opção"}
	for _, s := range yes {
		if !IsHallucination(s) {
			t.Errorf("%q should be a hallucination", s)
		}
	}
	for _, s := range no {
		if IsHallucination(s) {
			t.Errorf("%q should be kept", s)
		}
	}
}

func TestCleanTranscript(t *testing.T) {
	if got := CleanTranscript("\n Pode seguir   com a opção A.\n"); got != "Pode seguir com a opção A." {
		t.Fatalf("got %q", got)
	}
}

// fakeRec writes a `rec` stand-in that copies a fixture WAV to the requested
// path (FAKE_REC_WAV) after an optional sleep (FAKE_REC_SLEEP), recording
// its arguments.
func fakeRec(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "rec")
	script := `#!/bin/sh
printf '%s\n' "$*" > "$FAKE_DIR/rec.args"
out="${10}"
if [ -n "$FAKE_REC_SLEEP" ]; then sleep "$FAKE_REC_SLEEP"; fi
if [ -n "$FAKE_REC_WAV" ]; then cp "$FAKE_REC_WAV" "$out"; fi
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func fakeWhisper(t *testing.T, dir, transcript string) string {
	t.Helper()
	bin := filepath.Join(dir, "whisper-cli")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$FAKE_DIR/whisper.args\"\necho 'ggml_metal_init: noise' >&2\nprintf '\\n %s\\n' \"$FAKE_TRANSCRIPT\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_TRANSCRIPT", transcript)
	return bin
}

func TestRecorderArgsAndCap(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAKE_DIR", dir)
	speech := filepath.Join(dir, "speech.wav")
	_ = WriteWAV(speech, 16000, sine(16000, 1.2, 0.3))
	t.Setenv("FAKE_REC_WAV", speech)
	r := &Recorder{Bin: fakeRec(t, dir), Max: 10 * time.Second}
	out := filepath.Join(dir, "out.wav")
	rec, err := r.Record(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Exists || rec.Capped || math.Abs(rec.Audio.Seconds-1.2) > 0.01 {
		t.Fatalf("recording: %+v", rec)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "rec.args"))
	want := "-q -r 16000 -c 1 -b 16 -e signed-integer " + out + " silence 1 0.1 1% 1 1.5 1%"
	if strings.TrimSpace(string(args)) != want {
		t.Fatalf("rec args\n got %q\nwant %q", strings.TrimSpace(string(args)), want)
	}

	// The cap fires: SIGTERM, Capped=true, whatever was written is used.
	t.Setenv("FAKE_REC_SLEEP", "5")
	t.Setenv("FAKE_REC_WAV", "")
	r.Max = 300 * time.Millisecond
	start := time.Now()
	rec, err = r.Record(context.Background(), out)
	if err != nil {
		t.Fatalf("capped recording must not error: %v", err)
	}
	if !rec.Capped || rec.Exists || time.Since(start) > 3*time.Second {
		t.Fatalf("cap: %+v after %s", rec, time.Since(start))
	}

	// Interrupted by the engineer.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	r.Max = 10 * time.Second
	if _, err := r.Record(ctx, out); err != ErrInterrupted {
		t.Fatalf("want ErrInterrupted, got %v", err)
	}
}

func TestWhisperArgsAndCleaning(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAKE_DIR", dir)
	w := &Whisper{Bin: fakeWhisper(t, dir, "Pode seguir com a opção A."), Model: "/m/model.bin", Lang: "pt", Prompt: "Rails, Sidekiq"}
	text, err := w.Transcribe(context.Background(), "/tmp/x.wav")
	if err != nil {
		t.Fatal(err)
	}
	if text != "Pode seguir com a opção A." {
		t.Fatalf("transcript %q", text)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "whisper.args"))
	if strings.TrimSpace(string(args)) != "-m /m/model.bin -nt -np -l pt --prompt Rails, Sidekiq -f /tmp/x.wav" {
		t.Fatalf("whisper args %q", args)
	}
}

func TestVoiceListenerDecidesFromAudio(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAKE_DIR", dir)
	speechWav := filepath.Join(dir, "speech.wav")
	_ = WriteWAV(speechWav, 16000, sine(16000, 1.5, 0.2))
	shortWav := filepath.Join(dir, "short.wav")
	_ = WriteWAV(shortWav, 16000, sine(16000, 0.1, 0.2))
	quietWav := filepath.Join(dir, "quiet.wav")
	_ = WriteWAV(quietWav, 16000, sine(16000, 2, 0.001))

	l := &VoiceListener{
		Rec:     &Recorder{Bin: fakeRec(t, dir), Max: 5 * time.Second},
		STT:     &Whisper{Bin: fakeWhisper(t, dir, "Usa o Solid Queue."), Model: "m"},
		WavPath: filepath.Join(dir, "last_input.wav"),
	}
	ctx := context.Background()

	t.Setenv("FAKE_REC_WAV", speechWav)
	h, err := l.Listen(ctx)
	if err != nil || h.Text != "Usa o Solid Queue." || h.Dropped != "" {
		t.Fatalf("speech: %+v %v", h, err)
	}

	t.Setenv("FAKE_REC_WAV", shortWav)
	h, _ = l.Listen(ctx)
	if h.Text != "" || !strings.Contains(h.Dropped, "too short") {
		t.Fatalf("short: %+v", h)
	}

	t.Setenv("FAKE_REC_WAV", quietWav)
	h, _ = l.Listen(ctx)
	if h.Text != "" || !strings.Contains(h.Dropped, "silence") {
		t.Fatalf("quiet: %+v", h)
	}

	t.Setenv("FAKE_REC_WAV", "")
	h, _ = l.Listen(ctx)
	if h.Text != "" || !strings.Contains(h.Dropped, "no audio file") {
		t.Fatalf("missing: %+v", h)
	}

	t.Setenv("FAKE_REC_WAV", speechWav)
	t.Setenv("FAKE_TRANSCRIPT", "Obrigado.")
	h, _ = l.Listen(ctx)
	if h.Text != "" || !strings.Contains(h.Dropped, "stock phrase") || h.Raw != "Obrigado." {
		t.Fatalf("hallucination: %+v", h)
	}
}

func TestVocabularyPrompt(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "vocab.txt"), []byte("# project terms\nObraFlow\n\nfuteflow\n"), 0o644)
	p := VocabularyPrompt(dir)
	if !strings.HasPrefix(p, "Rails, Ruby") || !strings.HasSuffix(p, "ObraFlow, futeflow") {
		t.Fatalf("prompt %q", p)
	}
}

// TestRealWhisperOnSynthesizedSpeech runs the actual toolchain when it is
// installed: `say` writes a 16 kHz WAV, whisper-cli transcribes it.
func TestRealWhisperOnSynthesizedSpeech(t *testing.T) {
	if os.Getenv("PAIR_SKIP_REAL_AUDIO") != "" {
		t.Skip("PAIR_SKIP_REAL_AUDIO set")
	}
	home, _ := os.UserHomeDir()
	model := filepath.Join(home, ".pair", "models", "ggml-large-v3-turbo.bin")
	if _, err := os.Stat(model); err != nil {
		t.Skip("whisper model not downloaded")
	}
	for _, bin := range []string{"say", "whisper-cli"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	wav := filepath.Join(t.TempDir(), "sample.wav")
	say := exec.Command("say", "-v", "Luciana", "-r", "190", "--file-format=WAVE", "--data-format=LEI16@16000", "-o", wav,
		"Pode seguir com o Sidekiq e abrir o pull request.")
	if out, err := say.CombinedOutput(); err != nil {
		t.Skipf("say failed: %v %s", err, out)
	}
	a, err := AnalyzeWAV(wav)
	if err != nil || a.Seconds < 1 || a.RMS < MinRMS {
		t.Fatalf("synthesized audio unusable: %+v %v", a, err)
	}
	w := &Whisper{Model: model, Lang: "pt", Prompt: VocabularyPrompt(t.TempDir())}
	start := time.Now()
	text, err := w.Transcribe(context.Background(), wav)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("transcribed %.1fs of audio in %s: %q", a.Seconds, time.Since(start), text)
	low := strings.ToLower(text)
	if !strings.Contains(low, "sidekiq") || !strings.Contains(low, "pull request") {
		t.Fatalf("transcript missing expected terms: %q", text)
	}
	if IsHallucination(text) {
		t.Fatalf("real speech flagged as hallucination: %q", text)
	}
}

func TestMicLockIsExclusiveAcrossHolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mic.lock")
	ctx := context.Background()
	first, err := AcquireMic(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireMic(ctx, path, false); err != ErrMicBusy {
		t.Fatalf("second holder must be refused, got %v", err)
	}
	// A waiter gets the lock once it is released.
	got := make(chan error, 1)
	go func() {
		l, err := AcquireMic(ctx, path, true)
		if err == nil {
			l.Release()
		}
		got <- err
	}()
	time.Sleep(150 * time.Millisecond)
	first.Release()
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("waiter: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter never got the lock")
	}
	// A waiter gives up when its context ends.
	held, _ := AcquireMic(ctx, path, false)
	defer held.Release()
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, err := AcquireMic(short, path, true); err != ErrInterrupted {
		t.Fatalf("want ErrInterrupted, got %v", err)
	}
}

// Ten listeners started at once, as a key that auto-repeats does: exactly
// one records, the others are refused.
func TestConcurrentListenersRecordOnce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAKE_DIR", dir)
	wav := filepath.Join(dir, "speech.wav")
	_ = WriteWAV(wav, 16000, sine(16000, 1, 0.2))
	t.Setenv("FAKE_REC_WAV", wav)
	t.Setenv("FAKE_REC_SLEEP", "0.4")
	recBin, sttBin := fakeRec(t, dir), fakeWhisper(t, dir, "Where did we stop?")

	type result struct {
		h   Heard
		err error
	}
	results := make(chan result, 10)
	for i := 0; i < 10; i++ {
		go func() {
			l := &VoiceListener{
				Rec:      &Recorder{Bin: recBin, Max: 5 * time.Second},
				STT:      &Whisper{Bin: sttBin, Model: "m"},
				WavPath:  filepath.Join(dir, "last_input.wav"),
				LockPath: filepath.Join(dir, "mic.lock"),
			}
			h, err := l.Listen(context.Background())
			results <- result{h, err}
		}()
	}
	heard, refused := 0, 0
	for i := 0; i < 10; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		switch {
		case r.h.Text == "Where did we stop?":
			heard++
		case strings.Contains(r.h.Dropped, "another recording"):
			refused++
		}
	}
	if heard != 1 || refused != 9 {
		t.Fatalf("heard=%d refused=%d, want 1 and 9", heard, refused)
	}
}
