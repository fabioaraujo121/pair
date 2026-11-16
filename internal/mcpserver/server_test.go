package mcpserver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/events"
	"github.com/fabioaraujo121/pair/internal/nvim"
	"github.com/fabioaraujo121/pair/internal/speech"
)

type fakeSpeaker struct {
	mu     sync.Mutex
	spoken []string
	delay  time.Duration
	active int
	maxAct int
}

func (f *fakeSpeaker) Speak(ctx context.Context, text string) error {
	f.mu.Lock()
	f.active++
	if f.active > f.maxAct {
		f.maxAct = f.active
	}
	f.spoken = append(f.spoken, text)
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.active--; f.mu.Unlock() }()
	select {
	case <-time.After(f.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type fakeListener struct {
	heard speech.Heard
	err   error
	calls int
}

func (f *fakeListener) Listen(ctx context.Context) (speech.Heard, error) {
	f.calls++
	if f.err != nil {
		return speech.Heard{}, f.err
	}
	return f.heard, nil
}

type fakePointer struct {
	calls []nvim.Shown
	err   error
}

func (f *fakePointer) Show(_ context.Context, file string, s, e int) (nvim.Shown, error) {
	sh := nvim.Shown{File: file, Start: s, End: e}
	f.calls = append(f.calls, sh)
	return sh, f.err
}

// connect wires a Server to a client over in-memory transports.
func connect(t *testing.T, s *Server) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Run(ctx, st) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := client.Connect(ctx, ct, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close(); cancel() })
	return sess
}

func call(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var out []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			out = append(out, tc.Text)
		}
	}
	return strings.Join(out, "\n"), res.IsError
}

func newTestServer(t *testing.T) (*Server, *fakeSpeaker, *fakePointer, string) {
	t.Helper()
	root := t.TempDir()
	sp := &fakeSpeaker{delay: 20 * time.Millisecond}
	np := &fakePointer{}
	s := &Server{
		Cfg:      config.Config{Root: root, StateDir: filepath.Join(root, ".pair"), Sock: "/nonexistent.sock"},
		Log:      events.Open(filepath.Join(root, ".pair")),
		Speaker:  sp,
		Listener: &fakeListener{heard: speech.Heard{Text: "Solid Queue.", Seconds: 1.4, RMS: 0.1}},
		Nvim:     np,
		Version:  "test",
	}
	return s, sp, np, root
}

func TestToolsAreListedWithSchemas(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	sess := connect(t, s)
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		names[tool.Name] = tool
	}
	for _, want := range []string{"narrate", "show", "ask"} {
		if names[want] == nil {
			t.Fatalf("tool %s missing from %v", want, names)
		}
		if d := names[want].Description; !strings.Contains(d, "120") || !strings.Contains(d, "six seconds") {
			t.Fatalf("%s description must state the length target: %q", want, d)
		}
	}
}

func TestNarrateSpeaksNormalizedTextAndLogs(t *testing.T) {
	s, sp, _, root := newTestServer(t)
	sess := connect(t, s)
	out, isErr := call(t, sess, "narrate", map[string]any{"text": "Vou mexer no `users_controller` e abrir o PR."})
	if isErr || out != "spoken" {
		t.Fatalf("narrate = %q, err=%v", out, isErr)
	}
	if len(sp.spoken) != 1 || sp.spoken[0] != "Vou mexer no users controller e abrir o pull request." {
		t.Fatalf("spoken = %q", sp.spoken)
	}
	ev, _ := os.ReadFile(filepath.Join(root, ".pair", "events.jsonl"))
	if !strings.Contains(string(ev), `"type":"narrate"`) || !strings.Contains(string(ev), `"chars":45`) {
		t.Fatalf("event log:\n%s", ev)
	}
	log, _ := os.ReadFile(filepath.Join(root, ".pair", "session.log"))
	if !strings.Contains(string(log), "🗣 Vou mexer") {
		t.Fatalf("session log:\n%s", log)
	}
}

func TestNarrateRejectsLongAndEmptyText(t *testing.T) {
	s, sp, _, _ := newTestServer(t)
	sess := connect(t, s)
	out, isErr := call(t, sess, "narrate", map[string]any{"text": strings.Repeat("a", 401)})
	if !isErr || !strings.Contains(out, "401 characters") || !strings.Contains(out, "under 400") {
		t.Fatalf("long text: isErr=%v out=%q", isErr, out)
	}
	if _, isErr := call(t, sess, "narrate", map[string]any{"text": "  "}); !isErr {
		t.Fatal("empty text must be rejected")
	}
	if _, isErr := call(t, sess, "narrate", map[string]any{"text": strings.Repeat("é", 400)}); isErr {
		t.Fatal("400 runes (not bytes) must be accepted")
	}
	if len(sp.spoken) != 1 {
		t.Fatalf("only the valid narration may be spoken: %d", len(sp.spoken))
	}
}

func TestShowPointsAndSpeaksNote(t *testing.T) {
	s, sp, np, root := newTestServer(t)
	sess := connect(t, s)
	out, isErr := call(t, sess, "show", map[string]any{"file": "app/models/user.rb", "start_line": 12, "end_line": 20, "note": "olha a validação aqui"})
	if isErr {
		t.Fatalf("show error: %s", out)
	}
	if out != "shown app/models/user.rb:12-20" {
		t.Fatalf("show = %q", out)
	}
	want := filepath.Join(root, "app/models/user.rb")
	if len(np.calls) != 1 || np.calls[0].File != want || np.calls[0].Start != 12 || np.calls[0].End != 20 {
		t.Fatalf("pointer calls = %+v", np.calls)
	}
	if len(sp.spoken) != 1 || sp.spoken[0] != "olha a validação aqui" {
		t.Fatalf("note not spoken: %q", sp.spoken)
	}
	// end_line omitted defaults to start_line; no note speaks nothing
	call(t, sess, "show", map[string]any{"file": "/abs/x.go", "start_line": 3})
	if np.calls[1].File != "/abs/x.go" || np.calls[1].End != 3 || len(sp.spoken) != 1 {
		t.Fatalf("second show: %+v spoken=%d", np.calls[1], len(sp.spoken))
	}
	// Neovim unreachable: error result, but the note was still spoken
	np.err = errors.New("dial: no such file")
	out, isErr = call(t, sess, "show", map[string]any{"file": "x.go", "start_line": 1, "note": "nota"})
	if !isErr || !strings.Contains(out, "not reachable") || len(sp.spoken) != 2 {
		t.Fatalf("unreachable nvim: isErr=%v out=%q spoken=%d", isErr, out, len(sp.spoken))
	}
}

func TestAskSpeaksThenListens(t *testing.T) {
	s, sp, _, root := newTestServer(t)
	sess := connect(t, s)
	out, isErr := call(t, sess, "ask", map[string]any{"question": "Uso Sidekiq ou Solid Queue? Recomendo Solid Queue."})
	if isErr || out != "Solid Queue." {
		t.Fatalf("ask = %q err=%v", out, isErr)
	}
	if len(sp.spoken) != 1 {
		t.Fatalf("question not spoken: %q", sp.spoken)
	}
	log, _ := os.ReadFile(filepath.Join(root, ".pair", "session.log"))
	if !strings.Contains(string(log), "❓ Uso Sidekiq") || !strings.Contains(string(log), "👤 Solid Queue.") {
		t.Fatalf("session log:\n%s", log)
	}
	ev, _ := os.ReadFile(filepath.Join(root, ".pair", "events.jsonl"))
	if !strings.Contains(string(ev), `"type":"ask"`) || !strings.Contains(string(ev), `"seconds":1.4`) {
		t.Fatalf("events:\n%s", ev)
	}

	s.Listener = &fakeListener{heard: speech.Heard{Dropped: "recording is silence (rms 0.0001)"}}
	out, isErr = call(t, sess, "ask", map[string]any{"question": "Continuo?"})
	if isErr || out != "(no answer)" {
		t.Fatalf("no answer: %q err=%v", out, isErr)
	}
	log, _ = os.ReadFile(filepath.Join(root, ".pair", "session.log"))
	if !strings.Contains(string(log), "(no answer: recording is silence") {
		t.Fatalf("session log after silence:\n%s", log)
	}

	s.Listener = &fakeListener{err: errors.New("rec: no such device")}
	out, isErr = call(t, sess, "ask", map[string]any{"question": "Continuo?"})
	if !isErr || !strings.Contains(out, "could not listen") {
		t.Fatalf("listen failure: %q err=%v", out, isErr)
	}

	s.Listener = &fakeListener{err: speech.ErrInterrupted}
	out, isErr = call(t, sess, "ask", map[string]any{"question": "Continuo?"})
	if !isErr || !strings.Contains(out, "interrupted") {
		t.Fatalf("interrupted: %q err=%v", out, isErr)
	}
}

func TestAttentionCueBeforeSpeech(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	var cues, states []string
	s.Cue = func(_ context.Context, kind string) { cues = append(cues, kind) }
	s.CueIdle = 200 * time.Millisecond
	s.State = func(state, _ string) { states = append(states, state) }
	sess := connect(t, s)

	call(t, sess, "narrate", map[string]any{"text": "primeira"}) // first speech: cue
	call(t, sess, "narrate", map[string]any{"text": "segunda"})  // right after: no cue
	if len(cues) != 1 || cues[0] != KindNarrate {
		t.Fatalf("cues after two back-to-back narrations = %v", cues)
	}
	time.Sleep(250 * time.Millisecond)
	call(t, sess, "narrate", map[string]any{"text": "terceira"}) // after a quiet stretch: cue
	if len(cues) != 2 {
		t.Fatalf("cue expected after the idle gap: %v", cues)
	}
	call(t, sess, "ask", map[string]any{"question": "Continuo?"}) // questions always cue
	if len(cues) != 3 || cues[2] != KindAsk {
		t.Fatalf("ask must always cue: %v", cues)
	}
	want := "speaking working speaking working speaking working asking listening working"
	if got := strings.Join(states, " "); got != want {
		t.Fatalf("states\n got %s\nwant %s", got, want)
	}
}

func TestAskWithAClosedMicrophoneWaitsForTheEngineer(t *testing.T) {
	s, sp, _, root := newTestServer(t)
	listener := &fakeListener{heard: speech.Heard{Text: "must not be used"}}
	s.Listener = listener
	s.AskManual, s.AskWait = true, 3*time.Second
	var states []string
	s.State = func(state, detail string) { states = append(states, strings.TrimSpace(state+" "+detail)) }
	sess := connect(t, s)
	stateDir := filepath.Join(root, ".pair")

	// The engineer opens the microphone a moment later: push-to-talk sees
	// the pending question and delivers the answer.
	go func() {
		for i := 0; i < 100; i++ {
			if p, ok := speech.PendingAskFor(stateDir, time.Minute); ok && p.Question != "" {
				_ = speech.DeliverAnswer(stateDir, speech.Heard{Text: "Solid Queue.", Seconds: 1.2, RMS: 0.1})
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	out, isErr := call(t, sess, "ask", map[string]any{"question": "Sidekiq or Solid Queue? I recommend Solid Queue."})
	if isErr || out != "Solid Queue." {
		t.Fatalf("ask = %q err=%v", out, isErr)
	}
	if listener.calls != 0 {
		t.Fatal("the server must not open the microphone by itself")
	}
	if len(sp.spoken) != 1 {
		t.Fatalf("question not spoken: %v", sp.spoken)
	}
	if _, ok := speech.PendingAskFor(stateDir, time.Minute); ok {
		t.Fatal("pending question not cleared")
	}
	if got := strings.Join(states, " | "); !strings.Contains(got, "asking press F5 or click talk to answer") {
		t.Fatalf("states = %s", got)
	}

	// Nobody answers: "(no answer)" after the wait.
	s.AskWait = 300 * time.Millisecond
	out, isErr = call(t, sess, "ask", map[string]any{"question": "Continue?"})
	if isErr || out != "(no answer)" {
		t.Fatalf("timeout: %q err=%v", out, isErr)
	}
	log, _ := os.ReadFile(filepath.Join(stateDir, "session.log"))
	if !strings.Contains(string(log), "nobody answered within") || !strings.Contains(string(log), "the microphone is closed") {
		t.Fatalf("session log:\n%s", log)
	}
}

func TestOutsideAPairSessionTheToolsStaySilent(t *testing.T) {
	s, sp, np, root := newTestServer(t)
	s.Inactive = true
	listener := &fakeListener{heard: speech.Heard{Text: "must not be used"}}
	s.Listener = listener
	sess := connect(t, s)
	for name, args := range map[string]map[string]any{
		"narrate": {"text": "I am about to change the model."},
		"show":    {"file": "app/x.rb", "start_line": 1},
		"ask":     {"question": "Sidekiq or Solid Queue?"},
	} {
		out, isErr := call(t, sess, name, args)
		if isErr || !strings.Contains(out, "no pair session is running") {
			t.Fatalf("%s: %q err=%v", name, out, isErr)
		}
	}
	if len(sp.spoken) != 0 || len(np.calls) != 0 || listener.calls != 0 {
		t.Fatalf("spoke %d, pointed %d, listened %d times", len(sp.spoken), len(np.calls), listener.calls)
	}
	if _, err := os.Stat(filepath.Join(root, ".pair", "events.jsonl")); err == nil {
		t.Fatal("nothing may be logged outside a session")
	}
}

func TestSpeechIsSerialized(t *testing.T) {
	s, sp, _, _ := newTestServer(t)
	sp.delay = 60 * time.Millisecond
	sess := connect(t, s)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			call(t, sess, "narrate", map[string]any{"text": "frase"})
		}()
	}
	wg.Wait()
	if sp.maxAct != 1 {
		t.Fatalf("speech overlapped: max concurrent = %d", sp.maxAct)
	}
	if len(sp.spoken) != 4 {
		t.Fatalf("spoken = %d", len(sp.spoken))
	}
}

func TestClientCancellationInterruptsSpeech(t *testing.T) {
	s, sp, _, root := newTestServer(t)
	sp.delay = 5 * time.Second
	sess := connect(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "narrate", Arguments: map[string]any{"text": "longa"}})
	if err == nil {
		t.Fatal("expected the call to fail after cancellation")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("cancellation took %s", time.Since(start))
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		ev, _ := os.ReadFile(filepath.Join(root, ".pair", "events.jsonl"))
		if strings.Contains(string(ev), `"interrupted":true`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("handler was not interrupted; events:\n%s", ev)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestStdioBinaryWithFakeSay builds the real binary and drives `pair mcp`
// over stdio with a fake `say` on PATH: the integration test the PRD asks
// for.
func TestStdioBinaryWithFakeSay(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	repo := filepath.Join(filepath.Dir(thisFile), "..", "..")
	bin := filepath.Join(t.TempDir(), "pair")
	build := exec.Command("go", "build", "-o", bin, "./cmd/pair")
	build.Dir = repo
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	fakeDir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$FAKE_SAY_DIR/args\"\ncat > \"$FAKE_SAY_DIR/text\"\n"
	if err := os.WriteFile(filepath.Join(fakeDir, "say"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cmd := exec.Command(bin, "mcp", "--root", root)
	cmd.Env = append(os.Environ(), "PATH="+fakeDir+":"+os.Getenv("PATH"), "FAKE_SAY_DIR="+fakeDir, "PAIR_VOICE=Luciana", "PAIR_RATE=190", "PAIR_ACTIVE=1", "PAIR_CUE=off",
		// The machine's own settings must not reach the test.
		"HOME="+t.TempDir(), "PAIR_TTS=say", "PAIR_STT=none", "PAIR_SESSION=", "PAIR_MUTE=")
	cmd.Stderr = os.Stderr

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "narrate", Arguments: map[string]any{"text": "Vou criar a migration e abrir o PR."}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || res.Content[0].(*mcp.TextContent).Text != "spoken" {
		t.Fatalf("narrate over stdio: %+v", res)
	}
	text, _ := os.ReadFile(filepath.Join(fakeDir, "text"))
	if string(text) != "Vou criar a migration e abrir o pull request." {
		t.Fatalf("fake say received %q", text)
	}
	args, _ := os.ReadFile(filepath.Join(fakeDir, "args"))
	if strings.TrimSpace(string(args)) != "-v Luciana -r 190" {
		t.Fatalf("fake say args %q", args)
	}
	ev, _ := os.ReadFile(filepath.Join(root, ".pair", "events.jsonl"))
	if !strings.Contains(string(ev), `"type":"narrate"`) {
		t.Fatalf("events not written under --root:\n%s", ev)
	}
}
