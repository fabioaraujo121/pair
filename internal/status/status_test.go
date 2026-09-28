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
