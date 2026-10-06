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
// recording without touching anything else on the machine. So the
// processes that make or capture sound are tracked while they live, in
// the session's run directory, which is under ~/.pair and not in the
// repository: a repository cannot plant a file there.
//
// A pid alone names whatever process holds it now. The file also records
// when the process began, and a signal is sent only to a process that
// began at that moment: a file left behind by a crash can never reach a
// process that came to reuse the pid.

const (
	trackSpeech = "speech" // say, or the player of a neural voice
	trackRecord = "record" // rec
)

// runTracked runs cmd, tracking it in dir under the given kind for as long
// as it runs. An empty dir means no tracking.
func runTracked(cmd *exec.Cmd, dir, kind string) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	if dir != "" && os.MkdirAll(dir, 0o700) == nil {
		pid := cmd.Process.Pid
		file := filepath.Join(dir, kind+"-"+strconv.Itoa(pid)+".pid")
		if os.WriteFile(file, []byte(strconv.Itoa(pid)+"\n"+processBegan(pid)+"\n"), 0o600) == nil {
			defer os.Remove(file)
		}
	}
	return cmd.Wait()
}

// processBegan returns when a process began, as the system prints it, or
// "" when there is no such process.
func processBegan(pid int) string {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// signalTracked sends SIGTERM to the tracked processes of a kind that are
// still the ones that were tracked.
func signalTracked(dir, kind string) {
	files, _ := filepath.Glob(filepath.Join(dir, kind+"-*.pid"))
	for _, f := range files {
		if pid, began, ok := readTracked(f); ok && began != "" && processBegan(pid) == began {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
		_ = os.Remove(f)
	}
}

func readTracked(file string) (pid int, began string, ok bool) {
	data, err := os.ReadFile(file)
	if err != nil {
		return 0, "", false
	}
	first, rest, _ := strings.Cut(string(data), "\n")
	pid, err = strconv.Atoi(strings.TrimSpace(first))
	if err != nil || pid <= 1 {
		return 0, "", false
	}
	return pid, strings.TrimSpace(rest), true
}

// KillSpeech silences the session whose run directory is dir.
func KillSpeech(dir string) { signalTracked(dir, trackSpeech) }

// KillRecording stops the recording of the session whose run directory is
// dir.
func KillRecording(dir string) { signalTracked(dir, trackRecord) }

// RecordingSince reports when the session's recording in progress began.
func RecordingSince(dir string) (time.Time, bool) {
	files, _ := filepath.Glob(filepath.Join(dir, trackRecord+"-*.pid"))
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
	marker := filepath.Join(dir, pressMarker)
	var previous time.Time
	if fi, err := os.Stat(marker); err == nil {
		previous = fi.ModTime()
	}
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(marker, nil, 0o600)
	_ = os.Chtimes(marker, now, now)

	since, recording := RecordingSince(dir)
	if !recording || now.Sub(since) < stopAfter {
		return false
	}
	return previous.IsZero() || now.Sub(previous) > repeatGap
}
