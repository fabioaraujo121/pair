// Package mcpserver is `pair mcp`: the stdio MCP server Claude Code talks to
// for narrate, show and ask.
//
// All speech goes through one mutex so nothing ever overlaps or records
// while speaking. Handlers watch ctx: when the engineer presses Escape,
// Claude Code cancels the request, the context is cancelled within about
// 100 ms, and the running `say` is terminated here, because Claude Code
// never kills the server or its children.
package mcpserver

import (
	"context"
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
	Cfg     config.Config
	Log     *events.Log
	Speaker speech.Speaker
	Nvim    Pointer
	Version string
	Vocab   map[string]string

	speech sync.Mutex
}

// New wires the default implementations: `say`, the Neovim RPC client and
// the event log in the repository's .pair directory.
func New(cfg config.Config, version string) *Server {
	var speaker speech.Speaker = speech.NewSay(cfg.Voice, cfg.Rate)
	if cfg.Mute {
		speaker = speech.Silent{}
	}
	return &Server{
		Cfg:     cfg,
		Log:     events.Open(cfg.StateDir),
		Speaker: speaker,
		Nvim:    nvim.New(cfg.Sock),
		Version: version,
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
		Description: "Ask the engineer one question aloud and wait for the spoken answer. One question per decision point, recommended option first, under 200 characters. Record the answer in PAIRING.md under Decisions.",
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
	rec, err := s.speak(ctx, in.Text)
	_ = s.Log.Append("narrate", rec)
	_ = s.Log.Session("🗣 " + strings.TrimSpace(in.Text))
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
		if _, err := s.speak(ctx, in.Note); err != nil {
			return nil, nil, err
		}
	}
	if nvErr != nil {
		return nil, nil, fmt.Errorf("show: Neovim is not reachable at %s (is the pair session running?): %v", s.Cfg.Sock, nvErr)
	}
	return text(fmt.Sprintf("shown %s:%d-%d", rel, start, end)), nil, nil
}

// ask speaks the question. Recording the answer arrives in milestone 3; until
// then the result says so explicitly instead of pretending to have listened.
func (s *Server) ask(ctx context.Context, _ *mcp.CallToolRequest, in AskInput) (*mcp.CallToolResult, any, error) {
	if err := checkLength("ask", in.Question); err != nil {
		return nil, nil, err
	}
	rec, err := s.speak(ctx, in.Question)
	_ = s.Log.Append("ask", rec)
	_ = s.Log.Session("? " + strings.TrimSpace(in.Question))
	if err != nil {
		return nil, nil, err
	}
	return text("(no answer: voice input is not enabled yet. Take the recommended option, say so in one narrate, and record the decision in PAIRING.md under Decisions.)"), nil, nil
}

// speak normalizes and speaks under the speech mutex, returning the event
// payload to log.
func (s *Server) speak(ctx context.Context, raw string) (map[string]any, error) {
	spoken := speech.Normalize(raw, s.Vocab)
	s.speech.Lock()
	defer s.speech.Unlock()
	start := time.Now()
	err := s.Speaker.Speak(ctx, spoken)
	rec := map[string]any{
		"text":   raw,
		"spoken": spoken,
		"chars":  utf8.RuneCountInString(raw),
		"ms":     time.Since(start).Milliseconds(),
	}
	if err != nil {
		if ctx.Err() != nil {
			rec["interrupted"] = true
			return rec, fmt.Errorf("interrupted by the engineer")
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
