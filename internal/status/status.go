// Package status records what is happening in the session right now:
// Claude is working, speaking, asking, the microphone is listening, or it
// is the engineer's turn. The hooks and the MCP server set it; the status
// bar, the pane borders and the presence animation show it.
//
// An open microphone outranks everything else. While it is open the
// display stays on "listening", with the button that stops the recording,
// whatever Claude reports meanwhile; what was reported is shown when the
// microphone closes.
package status

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fabioaraujo121/pair/internal/agent"
	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

// Shown is a state and its detail.
type Shown struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// Status is the content of <repo>/.pair/status.json.
type Status struct {
	State  string    `json:"state"`
	Detail string    `json:"detail,omitempty"`
	Since  time.Time `json:"since"`          // when this state began
	Turn   time.Time `json:"turn,omitempty"` // when Claude's current turn began

	Mic    bool   `json:"mic,omitempty"`    // the microphone is open
	Behind *Shown `json:"behind,omitempty"` // reported while it was open, shown when it closes

	Agent   string `json:"agent,omitempty"`    // the agent's name in a sentence: "Claude", "Codex"
	TalkKey string `json:"talk_key,omitempty"` // the key that opens the microphone, for the labels
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

func save(stateDir string, st Status) {
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = os.MkdirAll(stateDir, 0o700)
	// A name of its own: several hooks write the status at the same moment.
	tmp := filepath.Join(stateDir, fmt.Sprintf("%s.%d.tmp", file, os.Getpid()))
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, filepath.Join(stateDir, file))
	}
}

// micOpen reports whether the microphone recorded in st is still to be
// believed: a listener that died without closing it must not freeze the
// display.
func micOpen(cfg config.Config, st Status, now time.Time) bool {
	longest := time.Duration(cfg.AskMax+45) * time.Second
	if cfg.AskMax <= 0 {
		longest = 105 * time.Second
	}
	return st.Mic && now.Sub(st.Since) < longest
}

// Set records the state and shows it in the status bar and on the pane
// borders of the running session. While the microphone is open, a state
// other than listening is kept for later.
func Set(cfg config.Config, state, detail string) {
	write(cfg, state, detail, false)
}

// StartTurn marks the beginning of a turn of Claude's: the elapsed time
// shown while it works counts from here.
func StartTurn(cfg config.Config) {
	write(cfg, tmux.StateWorking, "", true)
}

// ListeningDone closes the microphone and shows what happened meanwhile,
// or the given state when nothing was reported.
func ListeningDone(cfg config.Config, state, detail string) {
	next := Shown{State: state, Detail: detail}
	if cfg.StateDir != "" {
		prev := Read(cfg.StateDir)
		if prev.Behind != nil {
			next = *prev.Behind
		}
		prev.Mic, prev.Behind = false, nil
		save(cfg.StateDir, prev)
	}
	write(cfg, next.State, next.Detail, false)
}

// Who is the agent's name in a sentence, for the labels.
func Who(cfg config.Config) string {
	if a, err := agent.For(cfg.Agent); err == nil {
		return a.Short()
	}
	return "Claude"
}

func write(cfg config.Config, state, detail string, newTurn bool) {
	now := time.Now()
	who := Who(cfg)
	if cfg.StateDir != "" {
		prev := Read(cfg.StateDir)
		if state != tmux.StateListening && micOpen(cfg, prev, now) {
			prev.Behind = &Shown{State: state, Detail: detail}
			if newTurn {
				prev.Turn = now
			}
			save(cfg.StateDir, prev)
			return // the display stays on listening
		}
		st := Status{State: state, Detail: detail, Since: now, Turn: prev.Turn, Mic: state == tmux.StateListening, Agent: who, TalkKey: cfg.Keys.Talk}
		if prev.State == state && !prev.Since.IsZero() {
			st.Since = prev.Since
		}
		switch {
		case state == tmux.StateIdle:
			st.Turn = time.Time{}
		case state == tmux.StateListening:
			// the turn, if any, goes on
		case newTurn || st.Turn.IsZero():
			st.Turn = now
		}
		save(cfg.StateDir, st)
	}
	tmux.SetState(cfg.Session, state, detail, who, cfg.Keys.Talk)
}

// Label is the plain sentence for a state. who is the agent's name.
func Label(state, who string) string {
	if who == "" {
		who = "Claude"
	}
	switch state {
	case tmux.StateIdle:
		return "your turn"
	case tmux.StateSpeaking:
		return who + " is speaking"
	case tmux.StateAsking:
		return who + " is asking you"
	case tmux.StateListening:
		return "listening"
	case tmux.StateAttention:
		return who + " needs you"
	default:
		return who + " is working"
	}
}
