// Package logview renders the session's event log as a conversation: who
// said what and which files changed, wrapped at word boundaries for the
// width of the pane. It is what the activity column shows (`pair log`).
package logview

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fabioaraujo121/pair/internal/replay"
)

// Speakers.
const (
	You    = "you"
	Claude = "claude"
	System = ""
)

// Entry is one thing to show.
type Entry struct {
	Time    time.Time
	Speaker string
	Label   string // header when it differs from the speaker, e.g. "claude asks"
	Text    string // prose, wrapped
	Card    string // one-line fact, not wrapped: a file, a pointer
	Dim     bool   // de-emphasized (system notes)
}

// Builder turns events into entries, remembering enough to drop what would
// read as a repetition: a spoken prompt is logged both by the microphone
// and by Claude Code, an answer both by the microphone and by the question.
type Builder struct {
	Root string // repository root, to show relative paths

	lastHeard     string
	lastHeardAt   time.Time
	questionOpen  bool
	questionAnswd bool
}

// Add converts one event. ok is false when the event shows nothing.
func (b *Builder) Add(e replay.Event) (entries []Entry, ok bool) {
	p := e.Payload
	at := e.TS
	one := func(en Entry) ([]Entry, bool) { en.Time = at; return []Entry{en}, true }

	switch e.Type {
	case "session_start", "session_continue":
		what := "session started"
		if e.Type == "session_continue" {
			what = "session continued, Claude remembers the conversation above"
		}
		if name := str(p, "name"); name != "" && name != str(p, "id") {
			what = "“" + name + "” · " + what
		}
		return one(Entry{Dim: true, Text: fmt.Sprintf("%s · speaking %s, voice %s · the microphone is closed until you press F5 or click talk", what, str(p, "lang"), str(p, "voice"))})

	case "listen":
		h, _ := p["heard"].(map[string]any)
		text := str(h, "text")
		if text == "" {
			if d := str(h, "dropped"); d != "" && d != "muted" {
				return one(Entry{Dim: true, Text: "microphone: " + d})
			}
			return nil, false
		}
		b.lastHeard, b.lastHeardAt = text, at
		if p["answers"] == true {
			b.questionAnswd = true
			return one(Entry{Speaker: You, Label: "you answer", Text: text})
		}
		return one(Entry{Speaker: You, Text: text})

	case "prompt":
		var out []Entry
		text := str(p, "text")
		spoken := text != "" && strings.HasPrefix(b.lastHeard, strings.TrimSuffix(text, "…")) && at.Sub(b.lastHeardAt) < 15*time.Second
		if text != "" && !spoken {
			out = append(out, Entry{Time: at, Speaker: You, Text: text})
		}
		if ch, isList := p["changes"].([]any); isList && len(ch) > 0 {
			names := make([]string, len(ch))
			for i, c := range ch {
				names[i] = fmt.Sprint(c)
			}
			out = append(out, Entry{Time: at, Speaker: You, Card: "✎ you edited " + strings.Join(names, ", ")})
		}
		return out, len(out) > 0

	case "point":
		return one(Entry{Speaker: You, Card: "👉 " + b.ref(p), Text: str(p, "note")})
	case "interrupt":
		return one(Entry{Speaker: You, Card: "✋ interrupted"})

	case "narrate":
		return one(Entry{Speaker: Claude, Text: str(p, "text")})
	case "show":
		return one(Entry{Speaker: Claude, Card: "→ " + b.ref(p), Text: str(p, "note")})
	case "post_edit":
		card := "✎ " + b.rel(str(p, "file"))
		if _, has := p["added"]; has {
			card += fmt.Sprintf("  +%d −%d", num(p, "added"), num(p, "removed"))
		}
		return one(Entry{Speaker: Claude, Card: card})

	case "question":
		b.questionOpen, b.questionAnswd = true, false
		return one(Entry{Speaker: Claude, Label: "claude asks", Text: str(p, "text")})
	case "ask": // the closing record of a question; the answer may be shown already
		defer func() { b.questionOpen = false }()
		ans, _ := p["answer"].(map[string]any)
		var out []Entry
		if !b.questionOpen { // logs written before questions were announced
			out = append(out, Entry{Time: at, Speaker: Claude, Label: "claude asks", Text: str(p, "text")})
		}
		switch {
		case p["interrupted"] == true:
			out = append(out, Entry{Time: at, Dim: true, Text: "question interrupted"})
		case str(ans, "text") == "":
			out = append(out, Entry{Time: at, Dim: true, Text: "no answer: " + str(ans, "dropped")})
		case !b.questionAnswd:
			out = append(out, Entry{Time: at, Speaker: You, Label: "you answer", Text: str(ans, "text")})
		}
		return out, len(out) > 0

	case "notify":
		if str(p, "kind") == "idle_prompt" {
			return nil, false
		}
		return one(Entry{Speaker: Claude, Label: "claude needs you", Text: str(p, "message") + " (the Claude pane below; F7 for full screen)"})
	case "stop":
		return one(Entry{Dim: true, Card: "your turn"})
	}
	return nil, false
}

func (b *Builder) rel(file string) string {
	if file == "" {
		return ""
	}
	if b.Root != "" {
		if r, err := filepath.Rel(b.Root, file); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return file
}

func (b *Builder) ref(p map[string]any) string {
	file, s, e := b.rel(str(p, "file")), num(p, "start"), num(p, "end")
	switch {
	case s == 0:
		return file
	case e <= s:
		return fmt.Sprintf("%s:%d", file, s)
	default:
		return fmt.Sprintf("%s:%d-%d", file, s, e)
	}
}

// Printer writes entries for a given width, grouping consecutive entries
// of the same speaker under one header.
type Printer struct {
	W     io.Writer
	Width int
	Color bool

	lastHeader string
	lastAt     time.Time
	any        bool
}

const indent = "  "

// ANSI styles.
const (
	reset   = "\x1b[0m"
	dim     = "\x1b[2m"
	bold    = "\x1b[1m"
	green   = "\x1b[32m"
	cyan    = "\x1b[36m"
	magenta = "\x1b[35m"
	yellow  = "\x1b[33m"
	red     = "\x1b[31m"
)

// Reset forgets the grouping state, before a full re-render.
func (p *Printer) Reset() { p.lastHeader, p.any, p.lastAt = "", false, time.Time{} }

// Print writes one entry.
func (p *Printer) Print(e Entry) {
	width := p.Width
	if width < 20 {
		width = 20
	}
	if e.Dim { // a quiet, centered-looking note between turns
		line := e.Card
		if line == "" {
			line = e.Text
		}
		if p.any {
			fmt.Fprintln(p.W)
		}
		for _, l := range Wrap("· "+line, width-1) {
			fmt.Fprintln(p.W, p.style(dim, l))
		}
		p.lastHeader, p.any = "", true
		return
	}

	header := e.Speaker
	if e.Label != "" {
		header = e.Label
	}
	if header != p.lastHeader || e.Time.Sub(p.lastAt) > 90*time.Second {
		if p.any {
			fmt.Fprintln(p.W)
		}
		color := cyan
		switch {
		case e.Speaker == You:
			color = green
		case e.Label == "claude asks":
			color = magenta
		case e.Label == "claude needs you":
			color = red
		}
		stamp := ""
		if !e.Time.IsZero() {
			stamp = "  " + e.Time.Local().Format("15:04")
		}
		fmt.Fprintln(p.W, p.style(bold+color, header)+p.style(dim, stamp))
		p.lastHeader = header
	}
	p.lastAt, p.any = e.Time, true

	if e.Card != "" {
		fmt.Fprintln(p.W, indent+p.card(e.Card, width-len(indent)))
	}
	for _, l := range Wrap(e.Text, width-len(indent)-1) {
		fmt.Fprintln(p.W, indent+l)
	}
}

// card colors the counts of an edit card and shortens a path that does not
// fit.
func (p *Printer) card(s string, width int) string {
	if utf8.RuneCountInString(s) > width && width > 12 {
		r := []rune(s)
		s = string(r[:2]) + "…" + string(r[len(r)-(width-4):])
	}
	if !p.Color {
		return s
	}
	if i := strings.Index(s, "  +"); i > 0 {
		head, counts := s[:i], strings.Fields(s[i:])
		if len(counts) == 2 {
			return yellow + head + reset + "  " + green + counts[0] + reset + " " + red + counts[1] + reset
		}
	}
	return yellow + s + reset
}

func (p *Printer) style(code, s string) string {
	if !p.Color {
		return s
	}
	return code + s + reset
}

// Wrap breaks text into lines of at most width characters at word
// boundaries. Words longer than a line (paths, URLs) are split.
func Wrap(text string, width int) []string {
	if width < 8 {
		width = 8
	}
	var lines []string
	for _, para := range strings.Split(strings.TrimSpace(text), "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			continue
		}
		line := ""
		for _, w := range words {
			for utf8.RuneCountInString(w) > width {
				if line != "" {
					lines = append(lines, line)
					line = ""
				}
				r := []rune(w)
				lines = append(lines, string(r[:width]))
				w = string(r[width:])
			}
			switch {
			case line == "":
				line = w
			case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) <= width:
				line += " " + w
			default:
				lines = append(lines, line)
				line = w
			}
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	s, _ := m[k].(string)
	return s
}

func num(m map[string]any, k string) int {
	if m == nil {
		return 0
	}
	f, _ := m[k].(float64)
	return int(f)
}
