package speech

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Vou criar a **migration** no `users_controller` e abrir o PR.", "Vou criar a migration no users controller e abrir o pull request."},
		{"# Plano\n- primeiro item\n- segundo item\n", "Plano primeiro item segundo item"},
		{"Veja [a doc](https://x.dev) e o ActiveRecord::Base.", "Veja a doc e o Active Record::Base."},
		{"código:\n```go\nfunc x() {}\n```\nfim", "código: fim"},
		{"> citação _leve_ e __forte__", "citação leve e forte"},
		{"isValidEmail vira emailLooksValid", "is Valid Email vira email Looks Valid"},
		{"   muitos    espaços\n\n\nlinhas  ", "muitos espaços linhas"},
	}
	for _, c := range cases {
		if got := Normalize(c.in, nil); got != c.want {
			t.Errorf("Normalize(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
	if got := Normalize("o DB e o ObraFlow", map[string]string{"ObraFlow": "Obra Flow", "DB": "banco"}); got != "o banco e o Obra Flow" {
		t.Errorf("extra vocabulary: %q", got)
	}
}

// fakeSay writes a `say` stand-in that records its arguments and stdin and
// optionally sleeps (FAKE_SAY_SLEEP seconds) before writing a done marker.
func fakeSay(t *testing.T) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "say")
	script := `#!/bin/sh
printf '%s\n' "$*" > "$FAKE_SAY_DIR/args"
cat > "$FAKE_SAY_DIR/text"
if [ -n "$FAKE_SAY_SLEEP" ]; then sleep "$FAKE_SAY_SLEEP"; fi
echo done > "$FAKE_SAY_DIR/done"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_SAY_DIR", dir)
	return bin, dir
}

func TestSaySpeaksTextOnStdinAndBlocks(t *testing.T) {
	bin, dir := fakeSay(t)
	t.Setenv("FAKE_SAY_SLEEP", "0.3")
	s := &Say{Voice: "Luciana", Rate: 190, Bin: bin}
	start := time.Now()
	if err := s.Speak(context.Background(), "olá mundo"); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 250*time.Millisecond {
		t.Fatal("Speak returned before say finished")
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if strings.TrimSpace(string(args)) != "-v Luciana -r 190" {
		t.Fatalf("args = %q", args)
	}
	text, _ := os.ReadFile(filepath.Join(dir, "text"))
	if string(text) != "olá mundo" {
		t.Fatalf("text = %q", text)
	}
	if _, err := os.Stat(filepath.Join(dir, "done")); err != nil {
		t.Fatal("done marker missing")
	}
	if err := s.Speak(context.Background(), "   "); err != nil {
		t.Fatalf("blank text must be a no-op: %v", err)
	}
}

func TestSayIsInterruptedByContext(t *testing.T) {
	bin, dir := fakeSay(t)
	t.Setenv("FAKE_SAY_SLEEP", "5")
	s := &Say{Bin: bin}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := s.Speak(ctx, "uma narração longa")
	if err == nil || ctx.Err() == nil {
		t.Fatalf("expected a context error, got %v", err)
	}
	if time.Since(start) > 4*time.Second { // the stand-in would speak for 5 s
		t.Fatalf("Speak took %s after cancellation", time.Since(start))
	}
	if _, err := os.Stat(filepath.Join(dir, "done")); err == nil {
		t.Fatal("say was not stopped: done marker written")
	}
}

func TestOnlyTheSessionsOwnSpeechIsSilenced(t *testing.T) {
	bin, _ := fakeSay(t)
	t.Setenv("FAKE_SAY_SLEEP", "5")
	mine, other := t.TempDir(), t.TempDir()
	type result struct {
		who string
		err error
	}
	done := make(chan result, 2)
	for who, dir := range map[string]string{"mine": mine, "other": other} {
		go func() {
			done <- result{who, (&Say{Bin: bin, StateDir: dir}).Speak(context.Background(), "a long sentence")}
		}()
	}
	// Wait until both are tracked, then silence one session.
	deadline := time.Now().Add(3 * time.Second)
	for {
		a, _ := filepath.Glob(filepath.Join(mine, "run", "speech-*.pid"))
		b, _ := filepath.Glob(filepath.Join(other, "run", "speech-*.pid"))
		if len(a) == 1 && len(b) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the speech processes were not tracked")
		}
		time.Sleep(20 * time.Millisecond)
	}
	KillSpeech(mine)
	select {
	case r := <-done:
		if r.who != "mine" || r.err != ErrInterrupted {
			t.Fatalf("%s ended with %v; only mine must be interrupted", r.who, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("my speech was not silenced")
	}
	select {
	case r := <-done:
		t.Fatalf("the other session was disturbed: %s %v", r.who, r.err)
	case <-time.After(400 * time.Millisecond):
	}
	KillSpeech(other) // end the test promptly
	<-done
	if left, _ := filepath.Glob(filepath.Join(mine, "run", "*.pid")); len(left) != 0 {
		t.Fatalf("pid files left: %v", left)
	}
}

func TestPressingTalkAgainStopsTheRecording(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 28, 16, 32, 0, 0, time.Local)
	mark := func(name string, at time.Time) {
		path := filepath.Join(dir, "run", name)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte("123"), 0o644)
		_ = os.Chtimes(path, at, at)
	}

	// Nothing is recording: a press starts, it never stops.
	if PressWantsStop(dir, now) {
		t.Fatal("no recording to stop")
	}

	// The recording began with that press; the key is held, so it repeats.
	mark("record-123.pid", now)
	for _, ms := range []int{500, 550, 600, 650, 1200, 1250, 3000, 3050} {
		if PressWantsStop(dir, now.Add(time.Duration(ms)*time.Millisecond)) && ms != 1200 && ms != 3000 {
			t.Fatalf("a repeat %d ms in was taken for a new press", ms)
		}
	}

	// A press of its own, well after the last one, stops.
	mark("talk.press", now.Add(3050*time.Millisecond))
	if !PressWantsStop(dir, now.Add(5*time.Second)) {
		t.Fatal("pressing talk again must stop the recording")
	}

	// Too soon after the recording began: not yet.
	fresh := t.TempDir()
	dir = fresh
	mark("record-9.pid", now)
	if PressWantsStop(dir, now.Add(300*time.Millisecond)) {
		t.Fatal("a recording that has just begun is not stopped")
	}
}
