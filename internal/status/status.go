// Package status records what is happening in the session right now:
// Claude is working, speaking, asking, the microphone is listening, or it
// is the engineer's turn. The hooks and the MCP server set it; the status
// bar, the pane borders and the presence animation show it.
package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

// Status is the content of <repo>/.pair/status.json.
type Status struct {
	State  string    `json:"state"`
	Detail string    `json:"detail,omitempty"`
	Since  time.Time `json:"since"`          // when this state began
	Turn   time.Time `json:"turn,omitempty"` // when Claude's current turn began
}

const file = "status.json"

// Read returns the current status; "your turn" when nothing was recorded.
func Read(stateDir string) Status {
	st := Status{State: tmux.StateIdle}
	data, err := os.ReadFile(filepath.Join(stateDir, file))
	if err != nil {
		return st
	}
	var got Status
	if json.Unmarshal(data, &got) != nil || got.State == "" {
		return st
	}
	return got
}

// Set records the state and shows it in the status bar and on the pane
// borders of the running session.
func Set(cfg config.Config, state, detail string) {
	write(cfg, state, detail, false)
}

// StartTurn marks the beginning of a turn of Claude's: the elapsed time
// shown while it works counts from here.
func StartTurn(cfg config.Config) {
	write(cfg, tmux.StateWorking, "", true)
}

func write(cfg config.Config, state, detail string, newTurn bool) {
	now := time.Now()
	if cfg.StateDir != "" {
		prev := Read(cfg.StateDir)
		st := Status{State: state, Detail: detail, Since: now, Turn: prev.Turn}
		if prev.State == state && !prev.Since.IsZero() {
			st.Since = prev.Since
		}
		switch {
		case state == tmux.StateIdle:
			st.Turn = time.Time{}
		case newTurn || st.Turn.IsZero():
			st.Turn = now
		}
		if data, err := json.Marshal(st); err == nil {
			_ = os.MkdirAll(cfg.StateDir, 0o755)
			tmp := filepath.Join(cfg.StateDir, file+".tmp")
			if os.WriteFile(tmp, data, 0o644) == nil {
				_ = os.Rename(tmp, filepath.Join(cfg.StateDir, file))
			}
		}
	}
	tmux.SetState(cfg.Session, state, detail)
}

// Label is the plain sentence for a state.
func Label(state string) string {
	switch state {
	case tmux.StateIdle:
		return "your turn"
	case tmux.StateSpeaking:
		return "Claude is speaking"
	case tmux.StateAsking:
		return "Claude is asking you"
	case tmux.StateListening:
		return "listening"
	case tmux.StateAttention:
		return "Claude needs you"
	default:
		return "Claude is working"
	}
}
