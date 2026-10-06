// Package presence draws the animated indicator at the top of the activity
// column (`pair presence`). Each state has its own motion and colour, so a
// glance from across the room tells whether Claude is working, speaking,
// waiting for an answer, or done:
//
//	working    a wave travelling left to right, yellow, with the elapsed time
//	speaking   bars jumping like a voice, cyan
//	asking     all bars breathing together, magenta
//	listening  bars growing from the centre, red
//	attention  the whole row blinking, red
//	your turn  a calm line with one slow dot, green
package presence

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/fabioaraujo121/pair/internal/status"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

// FPS is the animation rate.
const FPS = 10

var blocks = []rune("▁▂▃▄▅▆▇█")

var spinner = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// colour returns the ANSI colour code of a state.
func colour(state string) string {
	switch state {
	case tmux.StateIdle:
		return "32" // green
	case tmux.StateSpeaking:
		return "36" // cyan
	case tmux.StateAsking:
		return "35" // magenta
	case tmux.StateListening, tmux.StateAttention:
		return "31" // red
	default:
		return "33" // yellow
	}
}

// level maps 0..1 to a block character.
func level(v float64) rune {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return blocks[int(math.Round(v*float64(len(blocks)-1)))]
}

// noise is a repeatable pseudo-random value in 0..1 for a bar at a step.
func noise(i, step int) float64 {
	x := uint32(i*7919 + step*104729 + 12345)
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	return float64(x%1000) / 999
}

// bars returns the animated row for a state at a frame.
func bars(state string, frame, n int) string {
	out := make([]rune, n)
	t := float64(frame)
	mid := float64(n-1) / 2
	for i := range out {
		x := float64(i)
		switch state {
		case tmux.StateSpeaking: // a voice: uneven bars that change fast
			envelope := 0.35 + 0.65*math.Abs(math.Sin(t*0.35+x*0.3))
			out[i] = level(noise(i, frame/2) * envelope)
		case tmux.StateAsking: // breathing together
			out[i] = level(0.15 + 0.7*(math.Sin(t*0.25)+1)/2)
		case tmux.StateListening: // growing from the centre
			d := math.Abs(x-mid) / (mid + 1)
			pulse := (math.Sin(t*0.5) + 1) / 2
			out[i] = level((1 - d) * (0.25 + 0.75*pulse))
		case tmux.StateAttention: // blinking
			if (frame/4)%2 == 0 {
				out[i] = blocks[len(blocks)-1]
			} else {
				out[i] = blocks[0]
			}
		case tmux.StateIdle: // calm: one dot gliding along a flat line
			out[i] = blocks[0]
			if n > 0 && i == (frame/3)%n {
				out[i] = blocks[2]
			}
		default: // working: a wave travelling to the right
			out[i] = level((math.Sin((x-t*0.7)*0.42) + 1) / 2)
		}
	}
	return string(out)
}

// clock formats a duration as m:ss.
func clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// Frame returns the lines to draw for a status at a frame. It is pure, so
// the animation can be tested.
func Frame(st status.Status, frame, width int, now time.Time, color bool) []string {
	if width < 12 {
		width = 12
	}
	n := width - 4
	if n > 48 {
		n = 48
	}
	paint := func(code, s string) string {
		if !color || s == "" {
			return s
		}
		return "\x1b[" + code + "m" + s + "\x1b[0m"
	}
	c := colour(st.State)

	glyph := string(spinner[frame%len(spinner)])
	switch st.State {
	case tmux.StateIdle:
		glyph = "○"
		if (frame/10)%2 == 0 {
			glyph = "●"
		}
	case tmux.StateSpeaking:
		glyph = "🗣"
	case tmux.StateAsking:
		glyph = "❓"
	case tmux.StateListening:
		glyph = "🎙"
	case tmux.StateAttention:
		glyph = "⚠"
	}

	label := status.Label(st.State, st.Agent)
	switch st.State {
	case tmux.StateIdle:
		label += " · press F5 or click talk"
	case tmux.StateAsking:
		label += " · press F5 to answer"
	case tmux.StateListening:
		label += " · pause or press F5 to send"
	case tmux.StateAttention:
		label += " · press F7"
	}
	elapsed := ""
	switch {
	case st.State == tmux.StateIdle:
	case st.State == tmux.StateAsking || st.State == tmux.StateListening || st.State == tmux.StateAttention:
		if !st.Since.IsZero() {
			elapsed = clock(now.Sub(st.Since))
		}
	case !st.Turn.IsZero():
		elapsed = clock(now.Sub(st.Turn))
	}
	head := glyph + " " + label
	if elapsed != "" {
		head += "  " + elapsed
	}

	// One line, whatever it holds: a command the agent runs can span several,
	// and the panel has room for four.
	detail := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			return -1
		}
		return r
	}, st.Detail)), " ")
	if st.State == tmux.StateAsking || st.State == tmux.StateListening {
		detail = "" // the label already says what to do
	}
	return []string{
		"",
		"  " + paint("1;"+c, fit(head, width-3)),
		"  " + paint(c, bars(st.State, frame, n)),
		"  " + paint("2", fit(detail, width-3)),
	}
}

// fit shortens s to at most n characters.
func fit(s string, n int) string {
	if n < 1 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// Run animates until ctx is done.
func Run(ctx context.Context, stateDir string, out *os.File) error {
	color := term.IsTerminal(int(out.Fd()))
	if color {
		fmt.Fprint(out, "\x1b[?25l\x1b[2J") // hide the cursor, clear
		defer fmt.Fprint(out, "\x1b[?25h")
	}
	resized := make(chan os.Signal, 1)
	signal.Notify(resized, syscall.SIGWINCH)
	defer signal.Stop(resized)

	tick := time.NewTicker(time.Second / FPS)
	defer tick.Stop()
	frame := 0
	st := status.Read(stateDir)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-resized:
			if color {
				fmt.Fprint(out, "\x1b[2J")
			}
		case <-tick.C:
		}
		if frame%2 == 0 { // the status file is read five times a second
			st = status.Read(stateDir)
		}
		width := 80
		if w, _, err := term.GetSize(int(out.Fd())); err == nil && w > 0 {
			width = w
		}
		var b strings.Builder
		if color {
			b.WriteString("\x1b[H")
		}
		for _, line := range Frame(st, frame, width, time.Now(), color) {
			b.WriteString(line)
			if color {
				b.WriteString("\x1b[K")
			}
			b.WriteString("\n")
		}
		fmt.Fprint(out, strings.TrimSuffix(b.String(), "\n"))
		frame++
	}
}
