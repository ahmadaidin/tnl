package daemon

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestCheckRunningPermissionDeniedPreservesRuntimeFiles(t *testing.T) {
	paths := testPaths(t)
	pid := os.Getpid()
	if err := WritePID(paths, pid); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	if err := os.WriteFile(paths.SocketFile, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("create socket sentinel: %v", err)
	}

	gotPID, running, err := checkRunning(paths, func(int) error { return syscall.EPERM })
	if err != nil {
		t.Fatalf("checkRunning: %v", err)
	}
	if gotPID != pid || !running {
		t.Errorf("checkRunning = (%d, %v), want (%d, true)", gotPID, running, pid)
	}
	for _, path := range []string{paths.PIDFile, paths.SocketFile} {
		if _, statErr := os.Stat(path); statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("%s was removed after permission denied", path)
			} else {
				t.Errorf("stat %s: %v", path, statErr)
			}
		}
	}
}
