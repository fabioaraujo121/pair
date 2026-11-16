// Package captions exports the spoken words of a session as subtitles,
// built from events.jsonl: every sentence the agent said and every
// transcript of the engineer, timed from the events' own stamps. A clip
// recorded from the session can then carry exact captions for both voices
// (`pair captions`).
package captions

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/fabioaraujo121/pair/internal/agent"
	"github.com/fabioaraujo121/pair/internal/plain"
	"github.com/fabioaraujo121/pair/internal/replay"
	"github.com/fabioaraujo121/pair/internal/session"
)

// Cue is one subtitle: who said what, and when, counted from the first
// event of the session.
type Cue struct {
	Start, End time.Duration
	Speaker    string
	Text       string
}

// You is the engineer's name in the captions.
const You = "you"

// wordsPerSecond is the pace assumed for a sentence whose duration was not
// kept: about 2.5 words a second is ordinary speech.
const wordsPerSecond = 2.5

// Build turns the events into cues, in the order they were spoken. speaker
// names the agent in its cues.
func Build(events []replay.Event, speaker string) []Cue {
	var origin time.Time
	for _, e := range events {
		if !e.TS.IsZero() {
			origin = e.TS
			break
		}
	}
	var cues []Cue
	add := func(who, text string, start, end time.Time) {
		text = plain.Line(strings.TrimSpace(text))
		if text == "" || end.IsZero() {
			return
		}
		if !start.Before(end) { // no duration kept: assume an ordinary pace
			start = end.Add(-estimate(text))
		}
		if start.Before(origin) {
			start = origin
		}
		cues = append(cues, Cue{Start: start.Sub(origin), End: end.Sub(origin), Speaker: who, Text: text})
	}
	answered := false
	for _, e := range events {
		p := e.Payload
		switch e.Type {
		case "narrate":
			// Appended when the speech ends; ms is how long it took.
			add(speaker, str(p, "text"), e.TS.Add(-millis(p, "ms")), e.TS)
		case "show":
			// Appended before the note is spoken, and the speech is not
			// timed: the cue starts at the stamp and lasts an ordinary pace.
			note := str(p, "note")
			add(speaker, note, e.TS, e.TS.Add(estimate(note)))
		case "question":
			answered = false
		case "ask":
			// Appended after the answer was waited for: the speech ended
			// listen_ms before the stamp, and had run for ms before that.
			end := e.TS.Add(-millis(p, "listen_ms"))
			add(speaker, str(p, "text"), end.Add(-millis(p, "ms")), end)
			// With PAIR_ASK_MIC=auto the server records the answer itself
			// and no listen event is written: the answer is only here.
			if ans, _ := p["answer"].(map[string]any); !answered {
				add(You, str(ans, "text"), e.TS.Add(-seconds(ans)), e.TS)
			}
		case "listen":
			h, _ := p["heard"].(map[string]any)
			if p["answers"] == true {
				answered = true
			}
			// Appended once the transcript is ready; seconds is the length
			// of the recording, so the cue starts that long before the
			// stamp. The time whisper took is not kept and shifts it a
			// little.
			add(You, str(h, "text"), e.TS.Add(-seconds(h)), e.TS)
		}
	}
	// Speech never overlaps, but an answer is logged before the question
	// that carried it: the cues are ordered by when they were spoken.
	sort.SliceStable(cues, func(i, j int) bool { return cues[i].Start < cues[j].Start })
	return cues
}

// AgentIn is the title of the agent of the session kept in dir ("Claude
// Code"), or "agent" when the session does not say which one it was.
func AgentIn(dir string) string {
	m, ok := session.Current(dir)
	if !ok || m.Agent == "" {
		return "agent"
	}
	a, err := agent.For(m.Agent)
	if err != nil {
		return "agent"
	}
	return a.Title()
}

// WriteSRT writes the cues as SubRip text, the format tools burn in.
func WriteSRT(w io.Writer, cues []Cue) error {
	for i, c := range cues {
		if _, err := fmt.Fprintf(w, "%d\n%s --> %s\n%s: %s\n\n", i+1, stamp(c.Start, ','), stamp(c.End, ','), c.Speaker, c.Text); err != nil {
			return err
		}
	}
	return nil
}

// WriteVTT writes the cues as WebVTT, the speaker as a voice tag, for a
// player on a web page.
func WriteVTT(w io.Writer, cues []Cue) error {
	if _, err := io.WriteString(w, "WEBVTT\n\n"); err != nil {
		return err
	}
	for _, c := range cues {
		if _, err := fmt.Fprintf(w, "%s --> %s\n<v %s>%s</v>\n\n", stamp(c.Start, '.'), stamp(c.End, '.'), c.Speaker, c.Text); err != nil {
			return err
		}
	}
	return nil
}

// stamp formats a time as HH:MM:SS, then sep and the milliseconds: a comma
// in SRT, a point in VTT.
func stamp(d time.Duration, sep byte) string {
	if d < 0 {
		d = 0
	}
	h := int(d / time.Hour)
	m := int(d % time.Hour / time.Minute)
	s := int(d % time.Minute / time.Second)
	ms := int(d % time.Second / time.Millisecond)
	return fmt.Sprintf("%02d:%02d:%02d%c%03d", h, m, s, sep, ms)
}

// estimate is how long a text takes to say at an ordinary pace.
func estimate(text string) time.Duration {
	words := len(strings.Fields(text))
	if words == 0 {
		words = 1
	}
	return time.Duration(float64(words) / wordsPerSecond * float64(time.Second))
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// millis reads a duration logged in milliseconds, rounded to the millisecond.
func millis(m map[string]any, k string) time.Duration {
	f, _ := m[k].(float64)
	return time.Duration(math.Round(f)) * time.Millisecond
}

// seconds reads the length of a recording, rounded to the millisecond.
func seconds(m map[string]any) time.Duration {
	f, _ := m["seconds"].(float64)
	return time.Duration(math.Round(f*1000)) * time.Millisecond
}
