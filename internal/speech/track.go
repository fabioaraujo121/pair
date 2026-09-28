package speech

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A session must be able to silence its own voice and stop its own
// recording without touching those of another session on the same
// machine. So the processes that make or capture sound are tracked: their
// pid is written under <state dir>/run while they live, and an interrupt
// signals exactly those.

const (
	trackSpeech = "speech" // say, or the player of a neural voice
	trackRecord = "record" // rec
)

// runTracked runs cmd, recording its pid in dir under the given kind for
// as long as it runs. An empty dir means no tracking.
func runTracked(cmd *exec.Cmd, dir, kind string) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	if dir != "" {
		run := filepath.Join(dir, "run")
		if os.MkdirAll(run, 0o755) == nil {
			file := filepath.Join(run, kind+"-"+strconv.Itoa(cmd.Process.Pid)+".pid")
			if os.WriteFile(file, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644) == nil {
				defer os.Remove(file)
			}
		}
	}
	return cmd.Wait()
}

// signalTracked sends SIGTERM to the tracked processes of a kind.
func signalTracked(dir, kind string) {
	files, _ := filepath.Glob(filepath.Join(dir, "run", kind+"-*.pid"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 1 {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
		_ = os.Remove(f)
	}
}

// KillSpeech silences the session whose state directory is dir.
func KillSpeech(dir string) { signalTracked(dir, trackSpeech) }

// KillRecording stops the recording of the session whose state directory
// is dir.
func KillRecording(dir string) { signalTracked(dir, trackRecord) }

// RecordingSince reports when the session's recording in progress began.
func RecordingSince(dir string) (time.Time, bool) {
	files, _ := filepath.Glob(filepath.Join(dir, "run", trackRecord+"-*.pid"))
	var since time.Time
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil && (since.IsZero() || fi.ModTime().Before(since)) {
			since = fi.ModTime()
		}
	}
	return since, !since.IsZero()
}

// A key that is held down repeats itself many times a second. Those
// repeats must not count as "press again to stop", or holding the talk
// key would end the recording it started.
const (
	repeatGap   = 500 * time.Millisecond // presses closer than this are one key held down
	stopAfter   = 700 * time.Millisecond // a recording this young is not stopped
	pressMarker = "talk.press"
)

// PressWantsStop records a press of the talk key and reports whether it
// asks to end the recording in progress: a press of its own, not the echo
// of a key held down, on a recording that has had time to start.
func PressWantsStop(dir string, now time.Time) bool {
	marker := filepath.Join(dir, "run", pressMarker)
	var previous time.Time
	if fi, err := os.Stat(marker); err == nil {
		previous = fi.ModTime()
	}
	_ = os.MkdirAll(filepath.Dir(marker), 0o755)
	_ = os.WriteFile(marker, nil, 0o644)
	_ = os.Chtimes(marker, now, now)

	since, recording := RecordingSince(dir)
	if !recording || now.Sub(since) < stopAfter {
		return false
	}
	return previous.IsZero() || now.Sub(previous) > repeatGap
}
