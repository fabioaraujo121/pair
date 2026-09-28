package speech

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fabioaraujo121/pair/internal/config"
)

// ErrInterrupted is returned when speech or recording was cut short by the
// engineer (context cancelled, or `say` terminated by `pair hook
// interrupt`).
var ErrInterrupted = errors.New("interrupted by the engineer")

// Heard is the outcome of one listening round. Text is empty when nothing
// usable was heard; Dropped then says why.
type Heard struct {
	Text    string  `json:"text"`
	Seconds float64 `json:"seconds"`
	RMS     float64 `json:"rms"`
	Capped  bool    `json:"capped,omitempty"`  // PAIR_ASK_MAX fired
	Dropped string  `json:"dropped,omitempty"` // why Text is empty
	Raw     string  `json:"raw,omitempty"`     // transcript before filtering
}

// Listener records the engineer until silence and returns the transcript.
type Listener interface {
	Listen(ctx context.Context) (Heard, error)
}

// Deaf never hears anything; used with PAIR_MUTE and in tests.
type Deaf struct{}

// Listen returns an empty result immediately.
func (Deaf) Listen(context.Context) (Heard, error) { return Heard{Dropped: "muted"}, nil }

// Recorder captures the microphone with sox `rec` until trailing silence.
type Recorder struct {
	StateDir     string        // the session's state directory, to track the process
	Bin          string        // defaults to "rec"
	Rate         int           // 16000
	SilenceLevel string        // "1%"
	SilenceSecs  string        // "1.5"
	Max          time.Duration // hard cap, enforced with SIGTERM so the WAV stays valid
}

// Recording describes what Record produced.
type Recording struct {
	Path   string
	Audio  Audio
	Capped bool
	Exists bool
}

// Record writes a mono 16 kHz WAV to path, stopping at the first pause of
// SilenceSecs or at Max. A cancelled ctx stops it and returns
// ErrInterrupted.
func (r *Recorder) Record(ctx context.Context, path string) (Recording, error) {
	bin := r.Bin
	if bin == "" {
		bin = "rec"
	}
	rate := r.Rate
	if rate == 0 {
		rate = 16000
	}
	level, secs := r.SilenceLevel, r.SilenceSecs
	if level == "" {
		level = config.DefaultSilenceLevel
	}
	if secs == "" {
		secs = config.DefaultSilenceSecs
	}
	max := r.Max
	if max <= 0 {
		max = time.Duration(config.DefaultAskMax) * time.Second
	}
	_ = os.Remove(path)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)

	rctx, cancel := context.WithTimeout(ctx, max)
	defer cancel()
	// -b 16 -e signed-integer: CoreAudio delivers 32-bit samples and sox
	// would write a 32-bit file, which whisper-cli cannot read.
	args := []string{"-q", "-r", strconv.Itoa(rate), "-c", "1", "-b", "16", "-e", "signed-integer", path,
		"silence", "1", "0.1", level, "1", secs, level}
	cmd := exec.CommandContext(rctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	runErr := runTracked(cmd, r.StateDir, trackRecord)

	if ctx.Err() != nil {
		return Recording{Path: path}, ErrInterrupted
	}
	rec := Recording{Path: path, Capped: rctx.Err() != nil}
	audio, err := AnalyzeWAV(path)
	if err == nil {
		rec.Exists, rec.Audio = true, audio
	}
	if runErr != nil && !rec.Capped && !rec.Exists {
		return rec, fmt.Errorf("%s: %v: %s", bin, runErr, strings.TrimSpace(stderr.String()))
	}
	return rec, nil
}

// Whisper transcribes WAV files with whisper.cpp's whisper-cli.
type Whisper struct {
	Bin     string // defaults to "whisper-cli"
	Model   string
	Lang    string
	Prompt  string // vocabulary hint
	Threads int
}

// Transcribe returns the cleaned transcript of a 16 kHz mono WAV.
func (w *Whisper) Transcribe(ctx context.Context, wav string) (string, error) {
	bin := w.Bin
	if bin == "" {
		bin = "whisper-cli"
	}
	args := []string{"-m", w.Model, "-nt", "-np"}
	if w.Lang != "" {
		args = append(args, "-l", w.Lang)
	}
	if w.Prompt != "" {
		args = append(args, "--prompt", w.Prompt)
	}
	if w.Threads > 0 {
		args = append(args, "-t", strconv.Itoa(w.Threads))
	}
	args = append(args, "-f", wav)
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr // -np still prints ggml/Metal lines on stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ErrInterrupted
		}
		return "", fmt.Errorf("%s: %v: %s", bin, err, lastLine(stderr.String()))
	}
	return CleanTranscript(stdout.String()), nil
}

var multiSpace = regexp.MustCompile(`\s+`)

// CleanTranscript trims whisper's leading blank line and space and collapses
// whitespace.
func CleanTranscript(s string) string {
	return strings.TrimSpace(multiSpace.ReplaceAllString(s, " "))
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Hallucination detection: whisper never returns an empty transcript for
// silence; it invents closing phrases instead (validated: "Obrigado.",
// "Transcrição e Legendas por…", "E aí", lone ".").
var (
	hallucinationWords = map[string]bool{
		"obrigado": true, "obrigada": true, "tchau": true, "valeu": true, "e ai": true,
		"ok": true, "hmm": true, "hum": true, "you": true, "thank you": true, "thanks": true, "bye": true,
	}
	hallucinationSubstrings = []string{"legendas", "amara.org", "transcrição", "transcricao", "subtitles", "thanks for watching", "thank you for watching"}
	nonLetters              = regexp.MustCompile(`[^\p{L}\p{N} ]+`)
)

// IsHallucination reports whether a transcript is one of whisper's stock
// inventions for silence rather than something the engineer said.
func IsHallucination(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.NewReplacer("í", "i", "ã", "a", "ç", "c", "é", "e").Replace(t)
	for _, sub := range hallucinationSubstrings {
		if strings.Contains(t, sub) {
			return true
		}
	}
	t = strings.TrimSpace(multiSpace.ReplaceAllString(nonLetters.ReplaceAllString(t, " "), " "))
	if t == "" {
		return true
	}
	return hallucinationWords[t]
}

// Thresholds below which a recording is "(no answer)" before transcription.
const (
	MinSeconds = 0.3
	MinRMS     = 0.003
)

// VoiceListener is the default Listener: chime, record, analyze, transcribe,
// filter.
type VoiceListener struct {
	Rec     *Recorder
	STT     *Whisper
	WavPath string
	Chime   func(ctx context.Context)

	// LockPath, when set, makes Listen take the cross-process microphone
	// lock. LockWait chooses between waiting for it (a question Claude
	// asked) and giving up at once.
	LockPath string
	LockWait bool
}

// MicLockPath is the lock shared by every pair process of this user.
func MicLockPath(cfg config.Config) string {
	return filepath.Join(cfg.UserDir, "mic.lock")
}

// NewVoiceListener wires rec + whisper-cli from the configuration.
func NewVoiceListener(cfg config.Config) *VoiceListener {
	return &VoiceListener{
		Rec: &Recorder{
			StateDir:     cfg.StateDir,
			Rate:         16000,
			SilenceLevel: cfg.SilenceLevel,
			SilenceSecs:  cfg.SilenceSecs,
			Max:          time.Duration(cfg.AskMax) * time.Second,
		},
		STT: &Whisper{
			Model:  cfg.WhisperModel,
			Lang:   cfg.Lang,
			Prompt: VocabularyPrompt(cfg.StateDir),
		},
		WavPath: filepath.Join(cfg.StateDir, "last_input.wav"),
		Chime:   func(ctx context.Context) { PlaySound(ctx, "/System/Library/Sounds/Tink.aiff") },
	}
}

// Listen runs one round. An empty Text with a Dropped reason is a normal
// outcome ("(no answer)"); errors are failures or interruption.
func (l *VoiceListener) Listen(ctx context.Context) (Heard, error) {
	if l.LockPath != "" {
		lock, err := AcquireMic(ctx, l.LockPath, l.LockWait)
		if errors.Is(err, ErrMicBusy) {
			return Heard{Dropped: "another recording is in progress"}, nil
		}
		if err != nil {
			return Heard{}, err
		}
		defer lock.Release()
	}
	if l.Chime != nil {
		l.Chime(ctx)
	}
	rec, err := l.Rec.Record(ctx, l.WavPath)
	if err != nil {
		return Heard{}, err
	}
	h := Heard{Seconds: rec.Audio.Seconds, RMS: rec.Audio.RMS, Capped: rec.Capped}
	switch {
	case !rec.Exists:
		h.Dropped = "no audio file (microphone unavailable or permission denied)"
		return h, nil
	case rec.Audio.Bits != 16:
		h.Dropped = fmt.Sprintf("recording is %d-bit, whisper needs 16-bit", rec.Audio.Bits)
		return h, nil
	case rec.Audio.Seconds < MinSeconds:
		h.Dropped = fmt.Sprintf("recording too short (%.2fs)", rec.Audio.Seconds)
		return h, nil
	case rec.Audio.RMS < MinRMS:
		h.Dropped = fmt.Sprintf("recording is silence (rms %.4f)", rec.Audio.RMS)
		return h, nil
	}
	text, err := l.STT.Transcribe(ctx, l.WavPath)
	if err != nil {
		return h, err
	}
	h.Raw = text
	if strings.TrimSpace(text) == "" {
		h.Dropped = "nothing intelligible in the recording"
		return h, nil
	}
	if IsHallucination(text) {
		h.Dropped = "whisper returned a stock phrase for silence (\"" + text + "\")"
		return h, nil
	}
	h.Text = text
	return h, nil
}

// DefaultVocabulary for the whisper prompt: Rails and pairing jargon that
// otherwise transcribes as Portuguese look-alikes.
var DefaultSTTVocabulary = []string{
	"Rails", "Ruby", "ActiveRecord", "PostgreSQL", "SQLite", "Sidekiq", "Solid Queue", "Solid Cache",
	"Kamal", "Hotwire", "Turbo", "Stimulus", "Tailwind", "migration", "controller", "model", "view",
	"RSpec", "Minitest", "Docker", "Claude Code", "Neovim", "tmux", "pull request", "deploy",
	"commit", "branch", "merge", "rebase", "endpoint", "JSON", "API", "PAIRING.md",
}

// VocabularyPrompt builds the whisper prompt from the defaults plus the
// non-empty lines of <stateDir>/vocab.txt.
func VocabularyPrompt(stateDir string) string {
	words := append([]string{}, DefaultSTTVocabulary...)
	if data, err := os.ReadFile(filepath.Join(stateDir, "vocab.txt")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if l := strings.TrimSpace(line); l != "" && !strings.HasPrefix(l, "#") {
				words = append(words, l)
			}
		}
	}
	return strings.Join(words, ", ")
}

// PlaySound plays a short sound synchronously (afplay), giving up after 2 s.
func PlaySound(ctx context.Context, path string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "afplay", path).Run()
}
