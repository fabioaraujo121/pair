package speech

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
