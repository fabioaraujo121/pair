package speech

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// sleeper starts a process that does nothing for a while.
func sleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

func exited(cmd *exec.Cmd, within time.Duration) bool {
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(within):
		return false
	}
}

// A pid file names a number, and numbers are reused. A file that a crash
// left behind, or that someone wrote, must never stop a process that is
// not the one that was tracked.
func TestAPidFileCannotReachAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	bystander := sleeper(t)
	pid := strconv.Itoa(bystander.Process.Pid)

	for name, content := range map[string]string{
		"speech-1.pid": pid,                                  // the old format: a pid alone
		"speech-2.pid": pid + "\n",                           // no start time
		"speech-3.pid": pid + "\nMon Jan  1 00:00:00 2001\n", // another process had this pid
		"speech-4.pid": "1\n" + processBegan(1) + "\n",       // never init
		"speech-5.pid": "-1\n",                               // never "every process"
		"speech-6.pid": "not a number",
		"record-7.pid": pid + "\nMon Jan  1 00:00:00 2001\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	KillSpeech(dir)
	KillRecording(dir)
	if exited(bystander, 500*time.Millisecond) {
		t.Fatal("a process that was never tracked was stopped")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.pid")); len(left) != 0 {
		t.Fatalf("stale files must be removed: %v", left)
	}
}

func TestTheTrackedProcessIsStopped(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sleep", "30")
	done := make(chan error, 1)
	go func() { done <- runTracked(cmd, dir, trackSpeech) }()

	var files []string
	deadline := time.Now().Add(5 * time.Second)
	for len(files) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the process was not tracked")
		}
		time.Sleep(20 * time.Millisecond)
		files, _ = filepath.Glob(filepath.Join(dir, "speech-*.pid"))
	}
	if fi, err := os.Stat(files[0]); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("pid file mode: %v %v", fi.Mode(), err)
	}
	if pid, began, ok := readTracked(files[0]); !ok || began == "" || processBegan(pid) != began {
		t.Fatalf("the file must say when the process began: %d %q %v", pid, began, ok)
	}
	KillSpeech(dir)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the tracked process was not stopped")
	}
}
