// Package events appends the session's event log (.pair/events.jsonl, one
// JSON object per line, replayable) and the human-readable session log
// (.pair/session.log) that the bottom-right tmux pane tails.
package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Log writes to the files inside one state directory.
type Log struct {
	Dir string
}

// Open makes sure dir exists and returns a Log for it.
func Open(dir string) *Log {
	Private(dir)
	return &Log{Dir: dir}
}

// Private makes sure the state directory exists, is readable by its owner
// only, and is ignored by git whether or not `pair init` was run: it holds
// prompts, transcripts and the engineer's recorded voice.
func Private(dir string) {
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	if fi, err := os.Stat(dir); err == nil && fi.Mode().Perm() != 0o700 {
		_ = os.Chmod(dir, 0o700)
	}
	ignore := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(ignore); os.IsNotExist(err) {
		_ = os.WriteFile(ignore, []byte("# pair session state: never committed\n*\n"), 0o600)
	}
}

// Append writes one {ts, type, payload} line to events.jsonl.
func (l *Log) Append(typ string, payload any) error {
	rec := struct {
		TS      string `json:"ts"`
		Type    string `json:"type"`
		Payload any    `json:"payload,omitempty"`
	}{time.Now().Format(time.RFC3339Nano), typ, payload}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return appendBytes(filepath.Join(l.Dir, "events.jsonl"), append(b, '\n'))
}

// Session writes one timestamped line to session.log.
func (l *Log) Session(text string) error {
	line := time.Now().Format("15:04:05") + "  " + text + "\n"
	return appendBytes(filepath.Join(l.Dir, "session.log"), []byte(line))
}

// SessionRaw writes pre-formatted text (for example a rendered diff) to
// session.log.
func (l *Log) SessionRaw(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	if b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	return appendBytes(filepath.Join(l.Dir, "session.log"), b)
}

// SessionLogPath is the file the tmux log pane tails.
func (l *Log) SessionLogPath() string { return filepath.Join(l.Dir, "session.log") }

func appendBytes(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Mode().Perm() != 0o600 {
		_ = f.Chmod(0o600) // written by an earlier version
	}
	_, err = f.Write(b)
	return err
}
