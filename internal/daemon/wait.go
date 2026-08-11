package daemon

import (
	"fmt"
	"os"
	"time"
)

// waitPollInterval is how often WaitForSocket checks for the socket file.
const waitPollInterval = 50 * time.Millisecond

// WaitForSocket blocks until the daemon socket exists, polling every 50ms.
// The check requires a real Unix socket (not a regular file) at
// paths.SocketFile. It returns an error once timeout elapses.
func WaitForSocket(paths Paths, timeout time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("wait for socket %s: invalid timeout %s", paths.SocketFile, timeout)
	}
	deadline := time.Now().Add(timeout)
	for {
		if fi, err := os.Stat(paths.SocketFile); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for socket %s", paths.SocketFile)
		}
		time.Sleep(waitPollInterval)
	}
}
