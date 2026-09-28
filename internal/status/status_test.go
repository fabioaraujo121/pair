package status

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

func TestTurnAndStateTimes(t *testing.T) {
	cfg := config.Config{StateDir: filepath.Join(t.TempDir(), ".pair")} // no session: tmux is not touched

	if st := Read(cfg.StateDir); st.State != tmux.StateIdle {
		t.Fatalf("nothing recorded must read as your turn: %+v", st)
	}

	StartTurn(cfg)
	first := Read(cfg.StateDir)
	if first.State != tmux.StateWorking || first.Turn.IsZero() {
		t.Fatalf("turn not started: %+v", first)
	}

	time.Sleep(20 * time.Millisecond)
	Set(cfg, tmux.StateWorking, "reading app/x.rb")
	again := Read(cfg.StateDir)
	if !again.Turn.Equal(first.Turn) || !again.Since.Equal(first.Since) || again.Detail != "reading app/x.rb" {
		t.Fatalf("same state must keep its times: %+v vs %+v", again, first)
	}

	Set(cfg, tmux.StateSpeaking, "")
	speaking := Read(cfg.StateDir)
	if !speaking.Turn.Equal(first.Turn) || !speaking.Since.After(first.Since) {
		t.Fatalf("a new state starts its own clock inside the same turn: %+v", speaking)
	}

	Set(cfg, tmux.StateIdle, "")
	if idle := Read(cfg.StateDir); !idle.Turn.IsZero() {
		t.Fatalf("the turn ends with your turn: %+v", idle)
	}

	time.Sleep(20 * time.Millisecond)
	StartTurn(cfg)
	if next := Read(cfg.StateDir); !next.Turn.After(first.Turn) {
		t.Fatalf("a new prompt starts a new turn: %+v", next)
	}
}

func TestAnOpenMicrophoneOutranksEverythingElse(t *testing.T) {
	cfg := config.Config{StateDir: filepath.Join(t.TempDir(), ".pair"), AskMax: 60}

	StartTurn(cfg)
	Set(cfg, tmux.StateListening, "")
	// Claude keeps working and reporting while the engineer talks.
	Set(cfg, tmux.StateWorking, "reading app/models/user.rb")
	Set(cfg, tmux.StateAttention, "Claude needs your permission to use Bash")
	st := Read(cfg.StateDir)
	if st.State != tmux.StateListening || !st.Mic {
		t.Fatalf("the display left listening while the microphone was open: %+v", st)
	}
	if st.Behind == nil || st.Behind.State != tmux.StateAttention {
		t.Fatalf("what was reported meanwhile must be kept: %+v", st.Behind)
	}

	// The microphone closes: the latest report is shown, not the fallback.
	ListeningDone(cfg, tmux.StateIdle, "")
	st = Read(cfg.StateDir)
	if st.State != tmux.StateAttention || st.Mic || st.Behind != nil || st.Detail == "" {
		t.Fatalf("after the microphone closed: %+v", st)
	}

	// Nothing was reported meanwhile: the fallback is shown.
	Set(cfg, tmux.StateListening, "")
	ListeningDone(cfg, tmux.StateIdle, "")
	if st = Read(cfg.StateDir); st.State != tmux.StateIdle || st.Mic {
		t.Fatalf("fallback after the microphone closed: %+v", st)
	}
}

func TestAMicrophoneLeftOpenDoesNotFreezeTheDisplay(t *testing.T) {
	cfg := config.Config{StateDir: filepath.Join(t.TempDir(), ".pair"), AskMax: 60}
	// A listener that died ten minutes ago without closing the microphone.
	save(cfg.StateDir, Status{State: tmux.StateListening, Mic: true, Since: time.Now().Add(-10 * time.Minute)})
	Set(cfg, tmux.StateWorking, "running the tests")
	if st := Read(cfg.StateDir); st.State != tmux.StateWorking || st.Mic {
		t.Fatalf("stale microphone froze the display: %+v", st)
	}
}
