package daemon

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// ReadPID reads the daemon pid from paths.PIDFile. A missing file returns an
// error wrapping os.ErrNotExist; unparseable content returns a descriptive
// error.
func ReadPID(paths Paths) (int, error) {
	data, err := os.ReadFile(paths.PIDFile)
	if err != nil {
		return 0, fmt.Errorf("read pid file %s: %w", paths.PIDFile, err)
	}
	s := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(s)
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid file %s: %q", paths.PIDFile, s)
	}
	return pid, nil
}

// WritePID writes pid to paths.PIDFile with 0600 permissions, replacing any
// previous content.
func WritePID(paths Paths, pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	if err := os.WriteFile(paths.PIDFile, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write pid file %s: %w", paths.PIDFile, err)
	}
	return nil
}

// RemovePID removes the pid file. A missing file is not an error.
func RemovePID(paths Paths) error {
	if err := os.Remove(paths.PIDFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove pid file %s: %w", paths.PIDFile, err)
	}
	return nil
}

// RemoveSocket removes the daemon socket. A missing file is not an error.
func RemoveSocket(paths Paths) error {
	if err := os.Remove(paths.SocketFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove socket %s: %w", paths.SocketFile, err)
	}
	return nil
}

// Cleanup removes the daemon's runtime files (pid and socket). It is
// best-effort: errors are ignored because cleanup runs on paths that may
// already be gone.
func Cleanup(paths Paths) {
	_ = RemovePID(paths)
	_ = RemoveSocket(paths)
}

// CheckRunning reports whether a daemon is alive, probing liveness with
// syscall.Kill(pid, 0), which sends no signal. A dead pid (ESRCH) means the
// pid file is stale: CheckRunning cleans up the pid and socket files and
// reports not running. A missing pid file likewise reports not running. A
// corrupt pid file is surfaced as an error rather than guessed at.
func CheckRunning(paths Paths) (pid int, running bool, err error) {
	return checkRunning(paths, func(pid int) error { return syscall.Kill(pid, 0) })
}

func checkRunning(paths Paths, probe func(int) error) (pid int, running bool, err error) {
	pid, err = ReadPID(paths)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, err
	}
	err = probe(pid)
	switch {
	case err == nil:
		return pid, true, nil
	case errors.Is(err, syscall.ESRCH):
		Cleanup(paths)
		return pid, false, nil
	default:
		// EPERM and similar: the process exists but is not ours to signal.
		// Treat it as running rather than risk a duplicate daemon.
		return pid, true, nil
	}
}
