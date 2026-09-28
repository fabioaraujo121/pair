// Package mcpserver is `pair mcp`: the stdio MCP server Claude Code talks to
// for narrate, show and ask.
//
// All speech and listening goes through one mutex so nothing ever overlaps
// or records while speaking. Handlers watch ctx: when the engineer presses Escape,
// Claude Code cancels the request, the context is cancelled within about
// 100 ms, and the running `say` is terminated here, because Claude Code
// never kills the server or its children.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/events"
	"github.com/fabioaraujo121/pair/internal/nvim"
	"github.com/fabioaraujo121/pair/internal/speech"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

// MaxChars is the ceiling for narrate, ask and show notes. It is a safety
// net, not the pacing guard: the CLAUDE.md block asks for under 200.
const MaxChars = 400

// Pointer is the part of the Neovim client show() needs.
type Pointer interface {
	Show(ctx context.Context, file string, start, end int) (nvim.Shown, error)
}

// Server holds the wiring for the three tools.
type Server struct {
	Cfg      config.Config
	Log      *events.Log
	Speaker  speech.Speaker
	Listener speech.Listener
	Nvim     Pointer
	Version  string
	Vocab    map[string]string

	// Cue calls for the engineer's attention before speech: pairing has
	// long quiet stretches, and a sentence that starts unannounced is half
	// missed. It runs before every question and before a narration that
	// follows CueIdle of silence. nil disables it.
	Cue     func(ctx context.Context, kind string)
	CueIdle time.Duration
	// State reports what is happening to the status bar. nil disables it.
	State func(state, detail string)

	speech     sync.Mutex // serializes speaking and listening: never both at once
	lastSpeech time.Time
}

// New wires the adapters selected by the configuration (PAIR_TTS,
// PAIR_STT), the Neovim RPC client and the event log in the repository's
// .pair directory.
func New(cfg config.Config, version string) (*Server, error) {
	speaker, err := speech.NewSpeaker(cfg)
	if err != nil {
		return nil, err
	}
	listener, err := speech.NewListener(cfg)
	if err != nil {
		return nil, err
	}
	srv := &Server{
		Cfg:      cfg,
		Log:      events.Open(cfg.StateDir),
		Speaker:  speaker,
		Listener: listener,
		Nvim:     nvim.New(cfg.Sock),
		Version:  version,
		Vocab:    speech.VocabularyFor(cfg.Lang),
		CueIdle:  time.Duration(cfg.CueIdle) * time.Second,
		State:    func(state, detail string) { tmux.SetState(cfg.Session, state, detail) },
	}
	if cfg.Cue && !cfg.Mute {
		srv.Cue = AttentionSound
	}
	return srv, nil
}

// Kinds of speech, for the cue and the status bar.
const (
	KindNarrate = "narrate"
	KindAsk     = "ask"
)

// AttentionSound is the default cue: a distinct system sound for
// narrations and for questions, then a beat of silence before the voice.
func AttentionSound(ctx context.Context, kind string) {
	sound := "/System/Library/Sounds/Ping.aiff"
	if kind == KindAsk {
		sound = "/System/Library/Sounds/Hero.aiff"
	}
	speech.PlaySound(ctx, sound)
	select {
	case <-time.After(350 * time.Millisecond):
	case <-ctx.Done():
	}
}

func (s *Server) setState(state, detail string) {
	if s.State != nil {
		s.State(state, detail)
	}
}

// NarrateInput is the argument of the narrate tool.
type NarrateInput struct {
	Text string `json:"text" jsonschema:"What to say aloud: one or two short plain-speech sentences, under 200 characters, no markdown or code"`
}

// ShowInput is the argument of the show tool.
type ShowInput struct {
	File      string `json:"file" jsonschema:"Path of the file to point at, absolute or relative to the repository root"`
	StartLine int    `json:"start_line" jsonschema:"First line to highlight, 1-based"`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"Last line to highlight, 1-based; defaults to start_line"`
	Note      string `json:"note,omitempty" jsonschema:"Optional short sentence spoken aloud while pointing"`
}

// AskInput is the argument of the ask tool.
type AskInput struct {
	Question string `json:"question" jsonschema:"The question to ask aloud: one decision, recommended option first, under 200 characters"`
}

// MCP builds the SDK server with the tools registered.
func (s *Server) MCP() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "pair", Version: s.Version}, nil)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "narrate",
		Description: "Speak aloud to the engineer you are pairing with, before a change: what you are changing and why. Blocks until the sentence has been spoken. Plain speech in the session language, no markdown, no code. Max 400 characters, aim under 200.",
	}, s.narrate)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "show",
		Description: "Point at code in the engineer's Neovim without editing: jumps to the file and highlights start_line to end_line. An optional note is spoken aloud. Use it before discussing or changing a range.",
	}, s.show)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ask",
		Description: "Ask the engineer one question aloud, then listen: a chime plays, the microphone records until 1.5 s of silence, and the transcript is returned (or \"(no answer)\"). One question per decision point, recommended option first, under 200 characters. Record the answer in PAIRING.md under Decisions.",
	}, s.ask)
	return srv
}

// Run serves over the given transport until the session ends.
func (s *Server) Run(ctx context.Context, t mcp.Transport) error {
	return s.MCP().Run(ctx, t)
}

func (s *Server) narrate(ctx context.Context, _ *mcp.CallToolRequest, in NarrateInput) (*mcp.CallToolResult, any, error) {
	if err := checkLength("narrate", in.Text); err != nil {
		return nil, nil, err
	}
	_ = s.Log.Session("🗣 " + strings.TrimSpace(in.Text))
	rec, err := s.speak(ctx, in.Text, KindNarrate)
	_ = s.Log.Append("narrate", rec)
	if err != nil {
		return nil, nil, err
	}
	return text("spoken"), nil, nil
}

func (s *Server) show(ctx context.Context, _ *mcp.CallToolRequest, in ShowInput) (*mcp.CallToolResult, any, error) {
	if in.File == "" {
		return nil, nil, fmt.Errorf("show: file is required")
	}
	if in.Note != "" {
		if err := checkLength("show note", in.Note); err != nil {
			return nil, nil, err
		}
	}
	start, end := in.StartLine, in.EndLine
	if start < 1 {
		start = 1
	}
	if end < start {
		end = start
	}
	file := in.File
	if !filepath.IsAbs(file) {
		file = filepath.Join(s.Cfg.Root, file)
	}
	file = filepath.Clean(file)

	shown, nvErr := s.Nvim.Show(ctx, file, start, end)
	payload := map[string]any{"file": file, "start": start, "end": end, "note": in.Note}
	if nvErr != nil {
		payload["nvim_error"] = nvErr.Error()
	} else {
		payload["shown"] = shown
	}
	_ = s.Log.Append("show", payload)
	rel := file
	if r, err := filepath.Rel(s.Cfg.Root, file); err == nil && !strings.HasPrefix(r, "..") {
		rel = r
	}
	line := fmt.Sprintf("→ %s:%d-%d", rel, start, end)
	if in.Note != "" {
		line += "  " + in.Note
	}
	_ = s.Log.Session(line)

	if in.Note != "" {
		if _, err := s.speak(ctx, in.Note, KindNarrate); err != nil {
			return nil, nil, err
		}
	}
	if nvErr != nil {
		return nil, nil, fmt.Errorf("show: Neovim is not reachable at %s (is the pair session running?): %v", s.Cfg.Sock, nvErr)
	}
	return text(fmt.Sprintf("shown %s:%d-%d", rel, start, end)), nil, nil
}

// ask speaks the question, then listens. Speaking and listening happen
// under the same lock so the microphone never records the speaker.
func (s *Server) ask(ctx context.Context, _ *mcp.CallToolRequest, in AskInput) (*mcp.CallToolResult, any, error) {
	if err := checkLength("ask", in.Question); err != nil {
		return nil, nil, err
	}
	s.speech.Lock()
	defer s.speech.Unlock()
	defer s.setState(tmux.StateWorking, "")
	_ = s.Log.Session("❓ " + strings.TrimSpace(in.Question))
	rec, err := s.speakLocked(ctx, in.Question, KindAsk)
	if err != nil {
		_ = s.Log.Append("ask", rec)
		return nil, nil, err
	}
	s.setState(tmux.StateListening, "")
	start := time.Now()
	heard, lerr := s.Listener.Listen(ctx)
	rec["answer"] = heard
	rec["listen_ms"] = time.Since(start).Milliseconds()
	if lerr != nil {
		if errors.Is(lerr, speech.ErrInterrupted) || ctx.Err() != nil {
			rec["interrupted"] = true
			_ = s.Log.Append("ask", rec)
			_ = s.Log.Session("👤 (interrupted)")
			return nil, nil, speech.ErrInterrupted
		}
		rec["error"] = lerr.Error()
		_ = s.Log.Append("ask", rec)
		_ = s.Log.Session("👤 (listen failed: " + lerr.Error() + ")")
		return nil, nil, fmt.Errorf("ask: could not listen: %v", lerr)
	}
	_ = s.Log.Append("ask", rec)
	if heard.Text == "" {
		_ = s.Log.Session("👤 (no answer: " + heard.Dropped + ")")
		return text("(no answer)"), nil, nil
	}
	_ = s.Log.Session("👤 " + heard.Text)
	return text(heard.Text), nil, nil
}

// speak normalizes and speaks under the speech mutex, returning the event
// payload to log.
func (s *Server) speak(ctx context.Context, raw, kind string) (map[string]any, error) {
	s.speech.Lock()
	defer s.speech.Unlock()
	defer s.setState(tmux.StateWorking, "")
	return s.speakLocked(ctx, raw, kind)
}

func (s *Server) speakLocked(ctx context.Context, raw, kind string) (map[string]any, error) {
	spoken := speech.Normalize(raw, s.Vocab)
	state := tmux.StateSpeaking
	if kind == KindAsk {
		state = tmux.StateAsking
	}
	s.setState(state, "")
	cued := false
	if s.Cue != nil && (kind == KindAsk || s.lastSpeech.IsZero() || time.Since(s.lastSpeech) > s.CueIdle) {
		s.Cue(ctx, kind)
		cued = true
	}
	start := time.Now()
	err := s.Speaker.Speak(ctx, spoken)
	s.lastSpeech = time.Now()
	rec := map[string]any{
		"text":   raw,
		"spoken": spoken,
		"chars":  utf8.RuneCountInString(raw),
		"ms":     time.Since(start).Milliseconds(),
		"cued":   cued,
	}
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, speech.ErrInterrupted) {
			rec["interrupted"] = true
			return rec, speech.ErrInterrupted
		}
		rec["error"] = err.Error()
		return rec, err
	}
	return rec, nil
}

func checkLength(what, s string) error {
	n := utf8.RuneCountInString(s)
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s: text is empty", what)
	}
	if n > MaxChars {
		return fmt.Errorf("%s: text is %d characters; keep it under %d (aim under 200: one or two short sentences) and call again with a shorter version", what, n, MaxChars)
	}
	return nil
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
