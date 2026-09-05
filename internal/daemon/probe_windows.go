//go:build windows

package daemon

import "errors"

// probePID reports process liveness. The standard library offers no
// zero-signal probe on windows (os.FindProcess always succeeds and Signal is
// unimplemented), so every probe fails. checkRunning treats a non-ESRCH
// failure as "running" — the same conservative default as EPERM on unix —
// which prevents a duplicate daemon even when the pid file is stale.
func probePID(pid int) error {
	return errors.New("process liveness probing is not supported on windows")
}
