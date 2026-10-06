package speech

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrMicBusy is returned when another pair process is already recording.
var ErrMicBusy = errors.New("the microphone is already in use by another recording")

// MicLock serializes microphone use across processes: the MCP server's
// `ask` and the push-to-talk hook are different processes, and a key that
// auto-repeats starts the hook many times over. Without it, several
// recorders write the same file and one utterance is transcribed and sent
// several times.
type MicLock struct {
	f *os.File
}

// AcquireMic takes the lock at path. With wait it blocks until the lock is
// free or ctx is done; without it, it returns ErrMicBusy immediately.
func AcquireMic(ctx context.Context, path string, wait bool) (*MicLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &MicLock{f: f}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, err
		}
		if !wait {
			_ = f.Close()
			return nil, ErrMicBusy
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ErrInterrupted
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Release frees the lock. The kernel also frees it when the process dies.
func (l *MicLock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}
