package speech

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Handing a spoken answer to a question Claude asked.
//
// The microphone is closed by default: nothing records until the engineer
// opens it with a key or a click. So when Claude asks something, the MCP
// server does not record by itself. It marks the question as pending and
// waits; the engineer's push-to-talk, a different process, sees the pending
// question and delivers the transcript here instead of typing it into
// Claude Code's input.

const (
	pendingFile = "ask.pending"
	answerFile  = "ask.answer"
)

// PendingAsk is a question waiting for the engineer's answer.
type PendingAsk struct {
	Question string    `json:"question"`
	Since    time.Time `json:"since"`
}

// SetPendingAsk records that a question is waiting and drops any stale
// answer.
func SetPendingAsk(stateDir, question string) error {
	_ = os.MkdirAll(stateDir, 0o755)
	_ = os.Remove(filepath.Join(stateDir, answerFile))
	data, _ := json.Marshal(PendingAsk{Question: question, Since: time.Now()})
	return writeAtomic(filepath.Join(stateDir, pendingFile), data)
}

// ClearPendingAsk forgets the pending question and any undelivered answer.
func ClearPendingAsk(stateDir string) {
	_ = os.Remove(filepath.Join(stateDir, pendingFile))
	_ = os.Remove(filepath.Join(stateDir, answerFile))
}

// PendingAskFor returns the pending question when there is one younger
// than maxAge.
func PendingAskFor(stateDir string, maxAge time.Duration) (PendingAsk, bool) {
	var p PendingAsk
	data, err := os.ReadFile(filepath.Join(stateDir, pendingFile))
	if err != nil || json.Unmarshal(data, &p) != nil {
		return p, false
	}
	if maxAge > 0 && time.Since(p.Since) > maxAge {
		return p, false
	}
	return p, true
}

// DeliverAnswer hands what was heard to the waiting question.
func DeliverAnswer(stateDir string, h Heard) error {
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(stateDir, answerFile), data)
}

// WaitForAnswer blocks until an answer is delivered, the timeout passes
// (ok is false) or ctx is cancelled (ErrInterrupted).
func WaitForAnswer(ctx context.Context, stateDir string, timeout time.Duration) (h Heard, ok bool, err error) {
	path := filepath.Join(stateDir, answerFile)
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(120 * time.Millisecond)
	defer tick.Stop()
	for {
		if data, rerr := os.ReadFile(path); rerr == nil {
			_ = os.Remove(path)
			if json.Unmarshal(data, &h) == nil {
				return h, true, nil
			}
		}
		if time.Now().After(deadline) {
			return Heard{}, false, nil
		}
		select {
		case <-ctx.Done():
			return Heard{}, false, ErrInterrupted
		case <-tick.C:
		}
	}
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
