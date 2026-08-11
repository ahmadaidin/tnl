package daemon

import (
	"context"
	"time"
)

// SelfHealPID keeps the daemon's pid file accurate, rewriting it whenever it
// is missing or holds a different pid. A daemon whose runtime files were
// removed must not become invisible to its own CLI: with the pid file
// restored, CheckRunning reports it running and clients refuse to launch a
// duplicate daemon.
func SelfHealPID(ctx context.Context, paths Paths, pid int) {
	selfHealPID(ctx, paths, pid, 5*time.Second)
}

// selfHealPID is SelfHealPID with an injectable interval for tests.
func selfHealPID(ctx context.Context, paths Paths, pid int, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cur, err := ReadPID(paths)
			if err != nil || cur != pid {
				_ = WritePID(paths, pid)
			}
		}
	}
}
