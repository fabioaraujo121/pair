// Package replay plays back a session from .pair/events.jsonl: narrations
// are spoken again, jumps are replayed in Neovim when one is listening, and
// everything is printed as a transcript. Gaps are scaled by --speed and
// capped so a session that waited minutes on the engineer replays in
// seconds.
package replay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fabioaraujo121/pair/internal/nvim"
	"github.com/fabioaraujo121/pair/internal/plain"
	"github.com/fabioaraujo121/pair/internal/speech"
)

// Event is one line of events.jsonl.
type Event struct {
	TS      time.Time      `json:"ts"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

// Load reads every event of a JSONL file, skipping malformed lines.
func Load(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var events []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		events = append(events, e)
	}
	return events, sc.Err()
}

// Pointer replays jumps; nil means print only.
type Pointer interface {
	Show(ctx context.Context, file string, start, end int) (nvim.Shown, error)
}

// Options control a replay.
type Options struct {
	Speed   float64        // 1 = real time, 2 = twice as fast, 0 = no waiting
	MaxGap  time.Duration  // longest pause replayed between events (after scaling)
	Speaker speech.Speaker // nil = do not speak
	Nvim    Pointer        // nil = do not jump
	Out     io.Writer
}

// Run plays the events in order.
func Run(ctx context.Context, events []Event, opts Options) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.MaxGap == 0 {
		opts.MaxGap = 3 * time.Second
	}
	var prev time.Time
	for i, e := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i > 0 && opts.Speed > 0 && !e.TS.IsZero() && !prev.IsZero() {
			gap := time.Duration(float64(e.TS.Sub(prev)) / opts.Speed)
			if gap > opts.MaxGap {
				gap = opts.MaxGap
			}
			if gap > 0 {
				select {
				case <-time.After(gap):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		prev = e.TS
		if err := play(ctx, e, opts); err != nil {
			return err
		}
	}
	return nil
}

func play(ctx context.Context, e Event, opts Options) error {
	p := e.Payload
	stamp := e.TS.Local().Format("15:04:05")
	say := func(text string) error {
		if opts.Speaker == nil || strings.TrimSpace(text) == "" {
			return nil
		}
		return opts.Speaker.Speak(ctx, text)
	}
	show := func(file string, start, end int) {
		if opts.Nvim == nil || file == "" {
			return
		}
		_, _ = opts.Nvim.Show(ctx, file, start, end)
	}
	line := func(format string, a ...any) {
		// What is replayed was said or written by someone: it is printed as text.
		fmt.Fprintln(opts.Out, stamp+"  "+plain.Text(fmt.Sprintf(format, a...)))
	}

	switch e.Type {
	case "session_start":
		line("● session in %s", str(p, "root"))
	case "narrate":
		line("🗣 %s", str(p, "text"))
		spoken := str(p, "spoken")
		if spoken == "" {
			spoken = str(p, "text")
		}
		return say(spoken)
	case "show":
		line("→ %s:%d-%d %s", str(p, "file"), num(p, "start"), num(p, "end"), str(p, "note"))
		show(str(p, "file"), num(p, "start"), num(p, "end"))
		return say(str(p, "note"))
	case "pre_edit":
		if p["new"] == true {
			line("+ %s (new file)", str(p, "file"))
			return nil
		}
		line("→ %s:%d-%d (about to edit)", str(p, "file"), num(p, "start"), num(p, "end"))
		show(str(p, "file"), num(p, "start"), num(p, "end"))
	case "post_edit":
		line("✎ edited %s", str(p, "file"))
	case "ask":
		line("? %s", str(p, "text"))
		if err := say(firstNonEmpty(str(p, "spoken"), str(p, "text"))); err != nil {
			return err
		}
		if ans, ok := p["answer"].(map[string]any); ok {
			if t := str(ans, "text"); t != "" {
				line("👤 %s", t)
			} else {
				line("👤 (no answer: %s)", str(ans, "dropped"))
			}
		} else {
			line("👤 (no answer recorded)")
		}
	case "listen":
		if h, ok := p["heard"].(map[string]any); ok {
			if t := str(h, "text"); t != "" {
				line("🎙 %s", t)
			} else {
				line("🎙 (nothing heard: %s)", str(h, "dropped"))
			}
		}
	case "point":
		line("👉 %s:%d-%d %s", str(p, "file"), num(p, "start"), num(p, "end"), str(p, "note"))
	case "interrupt":
		line("✋ interrupted")
	case "prompt":
		if ch, ok := p["changes"].([]any); ok && len(ch) > 0 {
			names := make([]string, len(ch))
			for i, c := range ch {
				names[i] = fmt.Sprint(c)
			}
			line("✎ engineer changed: %s", strings.Join(names, ", "))
		} else {
			line("› prompt")
		}
	case "stop":
		line("■ turn ended")
	default:
		line("· %s", e.Type)
	}
	return nil
}

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[k].(string); ok {
		return s
	}
	return ""
}

func num(m map[string]any, k string) int {
	if m == nil {
		return 0
	}
	if f, ok := m[k].(float64); ok {
		return int(f)
	}
	return 0
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
