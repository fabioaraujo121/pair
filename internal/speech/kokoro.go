package speech

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	pair "github.com/fabioaraujo121/pair"
	"github.com/fabioaraujo121/pair/internal/config"
)

// Kokoro speaks with the Kokoro neural voices, locally.
//
// A Python worker keeps the model loaded and writes one WAV file per
// sentence; this side plays them in order, so the first sentence is heard
// while the second is still being synthesized, and so the engineer can
// interrupt: the player is a process that gets SIGTERM. Whatever goes
// wrong (not installed, worker dead, synthesis error), the sentence is
// spoken by the Fallback instead: a narration is never lost.
type Kokoro struct {
	Dir      string   // installation: venv, model, voices, worker.py
	Model    string   // file name of the model inside Dir
	Voice    string   // af_heart, am_michael, pf_dora, ...
	Speed    float64  // 1.0 is the natural pace
	Player   string   // afplay
	OutDir   string   // where sentence files go
	StateDir string   // the session's state directory, to track the player
	Command  []string // how to start the worker; derived from Dir when empty
	Fallback Speaker
	Warn     func(string) // told once why the fallback is in use

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	msgs    chan workerMsg
	voices  []string
	nextID  int
	warned  bool
	started bool
}

// workerMsg is one line from the worker.
type workerMsg struct {
	Ready   *bool    `json:"ready"`
	Voices  []string `json:"voices"`
	ID      int      `json:"id"`
	Chunk   int      `json:"chunk"`
	Path    string   `json:"path"`
	Seconds float64  `json:"seconds"`
	Done    bool     `json:"done"`
	Error   string   `json:"error"`
}

// TTSOutDir is where sentence files are written.
func TTSOutDir() string { return filepath.Join(os.TempDir(), "pair-tts") }

// KokoroDir is the default installation directory.
func KokoroDir(cfg config.Config) string { return filepath.Join(cfg.UserDir, "kokoro") }

// NewKokoro builds the speaker from the configuration.
func NewKokoro(cfg config.Config) *Kokoro {
	voice := cfg.KokoroVoice
	if voice == "" {
		voice = DefaultKokoroVoice(cfg.Lang)
	}
	return &Kokoro{
		Dir:      KokoroDir(cfg),
		Model:    cfg.KokoroModel,
		Voice:    voice,
		Speed:    cfg.KokoroSpeed,
		Player:   "afplay",
		OutDir:   TTSOutDir(),
		StateDir: cfg.StateDir,
		Fallback: &Say{Voice: cfg.Voice, Rate: cfg.Rate, StateDir: cfg.StateDir},
	}
}

// DefaultKokoroVoice is the voice used for a spoken language when none is
// configured.
func DefaultKokoroVoice(lang string) string {
	switch strings.ToLower(lang) {
	case "pt":
		return "pf_dora"
	case "es":
		return "ef_dora"
	case "fr":
		return "ff_siwis"
	case "it":
		return "if_sara"
	case "ja":
		return "jf_alpha"
	case "zh":
		return "zf_xiaobei"
	case "hi":
		return "hf_alpha"
	default:
		return "af_heart"
	}
}

// KokoroLanguage is the pronunciation language of a voice, given by the
// first letter of its name.
func KokoroLanguage(voice string) string {
	if voice == "" {
		return "en-us"
	}
	switch voice[0] {
	case 'b':
		return "en-gb"
	case 'p':
		return "pt-br"
	case 'e':
		return "es"
	case 'f':
		return "fr-fr"
	case 'i':
		return "it"
	case 'j':
		return "ja"
	case 'z':
		return "cmn"
	case 'h':
		return "hi"
	default:
		return "en-us"
	}
}

// Installed reports whether the engine can be started, and what is missing
// when it cannot.
func (k *Kokoro) Installed() (bool, string) {
	if len(k.Command) > 0 {
		return true, ""
	}
	for _, f := range []string{filepath.Join("venv", "bin", "python"), k.modelFile(), "voices-v1.0.bin"} {
		if _, err := os.Stat(filepath.Join(k.Dir, f)); err != nil {
			return false, f + " is missing from " + k.Dir + " (run `pair voice setup kokoro`)"
		}
	}
	return true, ""
}

func (k *Kokoro) modelFile() string {
	if k.Model != "" {
		return k.Model
	}
	return "kokoro-v1.0.onnx"
}

// WriteWorker writes the worker script into the installation directory.
func (k *Kokoro) WriteWorker() (string, error) {
	path := filepath.Join(k.Dir, "worker.py")
	if cur, err := os.ReadFile(path); err == nil && string(cur) == pair.KokoroWorker {
		return path, nil
	}
	if err := os.MkdirAll(k.Dir, 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(pair.KokoroWorker), 0o755)
}

// Warm starts the worker ahead of the first sentence.
func (k *Kokoro) Warm() {
	k.mu.Lock()
	defer k.mu.Unlock()
	_ = k.start()
}

// Voices lists the voices of the engine.
func (k *Kokoro) Voices() ([]string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.start(); err != nil {
		return nil, err
	}
	return k.voices, nil
}

// Close stops the worker.
func (k *Kokoro) Close() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.stop()
}

// start launches the worker and waits until the model is loaded. The
// caller holds k.mu.
func (k *Kokoro) start() error {
	if k.cmd != nil {
		return nil
	}
	if ok, why := k.Installed(); !ok {
		return errors.New(why)
	}
	argv := k.Command
	if len(argv) == 0 {
		script, err := k.WriteWorker()
		if err != nil {
			return err
		}
		argv = []string{filepath.Join(k.Dir, "venv", "bin", "python"), "-u", script,
			filepath.Join(k.Dir, k.modelFile()), filepath.Join(k.Dir, "voices-v1.0.bin")}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // a terminal's Ctrl-C must not reach it
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{w: &stderr, left: 4000}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the Kokoro worker: %w", err)
	}
	msgs := make(chan workerMsg, 64)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<16), 1<<22)
		for sc.Scan() {
			var m workerMsg
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				msgs <- m
			}
		}
		close(msgs)
		_ = cmd.Wait()
	}()
	k.cmd, k.stdin, k.msgs = cmd, stdin, msgs

	select {
	case m, open := <-msgs:
		switch {
		case !open:
			k.stop()
			return fmt.Errorf("the Kokoro worker exited: %s", lastLine(stderr.String()))
		case m.Ready == nil || !*m.Ready:
			k.stop()
			return fmt.Errorf("the Kokoro worker could not load the model: %s", m.Error)
		}
		k.voices = m.Voices
	case <-time.After(45 * time.Second):
		k.stop()
		return errors.New("the Kokoro worker did not get ready in 45 s")
	}
	k.started = true
	return nil
}

// stop ends the worker. The caller holds k.mu.
func (k *Kokoro) stop() {
	if k.stdin != nil {
		_ = k.stdin.Close()
	}
	if k.cmd != nil && k.cmd.Process != nil {
		_ = k.cmd.Process.Signal(syscall.SIGTERM)
	}
	k.cmd, k.stdin, k.msgs = nil, nil, nil
}

// Speak synthesizes and plays text, blocking until it has been heard or
// ctx is done.
func (k *Kokoro) Speak(ctx context.Context, text string) error {
	return k.Prepare(ctx, text).Play(ctx)
}

// Prepare starts synthesizing text and returns at once. Whatever is done
// between Prepare and Play (the attention sound) costs no time: the first
// sentence is being synthesized meanwhile.
func (k *Kokoro) Prepare(_ context.Context, text string) Prepared {
	job := &kokoroJob{k: k, text: text}
	if strings.TrimSpace(text) == "" {
		job.empty = true
		return job
	}
	k.mu.Lock() // released by Play
	job.locked = true
	if err := k.start(); err != nil {
		job.failed = err
		return job
	}
	k.nextID++
	job.id = k.nextID
	_ = os.MkdirAll(k.OutDir, 0o755)
	speed := k.Speed
	if speed <= 0 {
		speed = 1
	}
	req, _ := json.Marshal(map[string]any{
		"id": job.id, "text": text, "voice": k.Voice, "lang": KokoroLanguage(k.Voice), "speed": speed, "dir": k.OutDir,
	})
	if _, err := k.stdin.Write(append(req, '\n')); err != nil {
		k.stop()
		job.failed = fmt.Errorf("the Kokoro worker is gone: %w", err)
	}
	return job
}

type kokoroJob struct {
	k      *Kokoro
	id     int
	text   string
	empty  bool
	locked bool
	failed error
}

// Play plays the sentences as the worker delivers them.
func (j *kokoroJob) Play(ctx context.Context) error {
	if j.empty {
		return nil
	}
	k := j.k
	if j.locked {
		defer k.mu.Unlock()
	}
	if j.failed != nil {
		return k.fallback(ctx, j.text, j.failed)
	}
	played := 0
	for {
		select {
		case <-ctx.Done():
			j.cancel()
			return ErrInterrupted
		case <-time.After(40 * time.Second):
			k.stop()
			if played > 0 {
				return errors.New("the Kokoro worker stopped answering")
			}
			return k.fallback(ctx, j.text, errors.New("the Kokoro worker stopped answering"))
		case m, open := <-k.msgs:
			switch {
			case !open:
				k.stop()
				if played > 0 {
					return errors.New("the Kokoro worker exited while speaking")
				}
				return k.fallback(ctx, j.text, errors.New("the Kokoro worker exited"))
			case m.ID != j.id:
				if m.Path != "" {
					_ = os.Remove(m.Path) // left over from an interrupted sentence
				}
			case m.Done && m.Error != "" && played == 0:
				return k.fallback(ctx, j.text, errors.New(m.Error))
			case m.Done && m.Error != "":
				return errors.New("kokoro: " + m.Error)
			case m.Done:
				return nil
			default:
				err := k.play(ctx, m.Path)
				_ = os.Remove(m.Path)
				if err != nil {
					j.cancel()
					return err
				}
				played++
			}
		}
	}
}

// cancel tells the worker to stop after the sentence it is on.
func (j *kokoroJob) cancel() {
	_ = os.WriteFile(filepath.Join(j.k.OutDir, fmt.Sprintf("%d.cancel", j.id)), nil, 0o644)
}

// play plays one file and waits. SIGTERM on the player, from ctx or from
// an interrupt, ends it as ErrInterrupted.
func (k *Kokoro) play(ctx context.Context, path string) error {
	player := k.Player
	if player == "" {
		player = "afplay"
	}
	cmd := exec.CommandContext(ctx, player, path)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = time.Second
	if err := runTracked(cmd, k.StateDir, trackSpeech); err != nil {
		if ctx.Err() != nil || terminated(err) {
			return ErrInterrupted
		}
		return fmt.Errorf("%s: %v", player, err)
	}
	return nil
}

// fallback speaks with the other voice and says once why.
func (k *Kokoro) fallback(ctx context.Context, text string, why error) error {
	if !k.warned && k.Warn != nil {
		k.warned = true
		k.Warn("Kokoro voice unavailable, using the system voice: " + why.Error())
	}
	if k.Fallback == nil {
		return why
	}
	return k.Fallback.Speak(ctx, text)
}

// limitedWriter keeps the first bytes of the worker's stderr.
type limitedWriter struct {
	w    io.Writer
	left int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.left > 0 {
		n := len(p)
		if n > l.left {
			n = l.left
		}
		_, _ = l.w.Write(p[:n])
		l.left -= n
	}
	return len(p), nil
}
