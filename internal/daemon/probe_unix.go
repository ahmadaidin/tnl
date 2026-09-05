//go:build !windows

package daemon

import "syscall"

// probePID reports whether a process with the given pid exists and is
// signalable, using the zero-signal probe syscall.Kill(pid, 0), which sends
// no signal. nil means alive; ESRCH means gone; EPERM means alive but owned
// by another user. The probe is unix-specific: windows has no stdlib
// equivalent, so probe_windows.go supplies a stub.
func probePID(pid int) error { return syscall.Kill(pid, 0) }
