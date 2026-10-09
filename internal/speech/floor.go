package speech

import (
	"context"
	"path/filepath"
)

// The floor is who speaks. Narrations come from the MCP server's process
// and acknowledgements from the push-to-talk hook's, so a lock on disk,
// in the session's run directory, keeps one from talking over the other.

// AcquireFloor takes the session's speech lock. With wait it blocks until
// the lock is free or ctx is done. An empty dir means no session: the
// floor is free.
func AcquireFloor(ctx context.Context, dir string, wait bool) (*MicLock, error) {
	if dir == "" {
		return &MicLock{}, nil
	}
	return AcquireMic(ctx, filepath.Join(dir, "floor.lock"), wait)
}
