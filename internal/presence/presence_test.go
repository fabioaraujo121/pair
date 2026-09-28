package presence

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fabioaraujo121/pair/internal/status"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

func TestEveryStateMovesAndFits(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for _, state := range []string{tmux.StateWorking, tmux.StateSpeaking, tmux.StateAsking, tmux.StateListening, tmux.StateAttention, tmux.StateIdle} {
		st := status.Status{State: state, Detail: "reading app/models/user.rb", Since: now.Add(-5 * time.Second), Turn: now.Add(-42 * time.Second)}
		seen := map[string]bool{}
		for frame := 0; frame < 40; frame++ {
			lines := Frame(st, frame, 40, now, false)
			if len(lines) != 4 {
				t.Fatalf("%s: %d lines", state, len(lines))
			}
			for _, l := range lines {
				if utf8.RuneCountInString(l) > 40 {
					t.Fatalf("%s: line wider than the pane: %q", state, l)
				}
			}
			seen[lines[2]] = true
		}
		if len(seen) < 2 {
			t.Fatalf("%s does not move: the animation row never changes", state)
		}
	}
}

func TestStatesLookDifferent(t *testing.T) {
	now := time.Now()
	rows := map[string]string{}
	for _, state := range []string{tmux.StateWorking, tmux.StateSpeaking, tmux.StateAsking, tmux.StateListening, tmux.StateIdle} {
		rows[state] = Frame(status.Status{State: state}, 7, 60, now, false)[2]
	}
	seen := map[string]string{}
	for state, row := range rows {
		if other, dup := seen[row]; dup {
			t.Fatalf("%s and %s draw the same row", state, other)
		}
		seen[row] = state
	}
}

func TestWorkingShowsElapsedTimeAndDetail(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	st := status.Status{State: tmux.StateWorking, Detail: "running the test suite", Turn: now.Add(-(2*time.Minute + 5*time.Second))}
	lines := Frame(st, 3, 60, now, false)
	if !strings.Contains(lines[1], "Claude is working") || !strings.Contains(lines[1], "2:05") {
		t.Fatalf("head: %q", lines[1])
	}
	if !strings.Contains(lines[3], "running the test suite") {
		t.Fatalf("detail: %q", lines[3])
	}
	idle := Frame(status.Status{State: tmux.StateIdle}, 3, 60, now, false)
	if !strings.Contains(idle[1], "your turn") || strings.Contains(idle[1], "0:") {
		t.Fatalf("idle head: %q", idle[1])
	}
	// Colour is applied only when asked for.
	if strings.Contains(strings.Join(lines, ""), "\x1b[") {
		t.Fatal("escape codes without colour")
	}
	if !strings.Contains(strings.Join(Frame(st, 3, 60, now, true), ""), "\x1b[1;33m") {
		t.Fatal("working must be yellow")
	}
}
