package daemon_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmadaidin/tnl/internal/daemon"
)

// testPaths builds a Paths value rooted at a short temp dir. macOS rejects
// unix socket paths longer than 104 bytes (sun_path), and t.TempDir() names
// grow with the test name, so the dir and file names here are kept short.
// Tests that need the real runtime layout use ResolvePaths instead.
func testPaths(t *testing.T) daemon.Paths {
	t.Helper()
	dir, err := os.MkdirTemp("", "tnl")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return daemon.Paths{
		RuntimeDir: dir,
		PIDFile:    filepath.Join(dir, "pid"),
		SocketFile: filepath.Join(dir, "sock"),
		LogFile:    filepath.Join(dir, "log"),
	}
}

func TestPIDRoundTrip(t *testing.T) {
	paths := testPaths(t)

	if err := daemon.WritePID(paths, 4242); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	got, err := daemon.ReadPID(paths)
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if got != 4242 {
		t.Errorf("ReadPID = %d, want 4242", got)
	}

	if err := daemon.WritePID(paths, 1337); err != nil {
		t.Fatalf("WritePID overwrite: %v", err)
	}
	got, err = daemon.ReadPID(paths)
	if err != nil {
		t.Fatalf("ReadPID after overwrite: %v", err)
	}
	if got != 1337 {
		t.Errorf("ReadPID = %d, want 1337", got)
	}

	if err := daemon.RemovePID(paths); err != nil {
		t.Fatalf("RemovePID: %v", err)
	}
	if _, err := daemon.ReadPID(paths); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadPID after remove: err = %v, want ErrNotExist", err)
	}
}

func TestRemovePIDIdempotent(t *testing.T) {
	paths := testPaths(t)
	if err := daemon.RemovePID(paths); err != nil {
		t.Errorf("RemovePID on missing file: %v", err)
	}
	if err := daemon.RemoveSocket(paths); err != nil {
		t.Errorf("RemoveSocket on missing file: %v", err)
	}
}

func TestWritePIDRejectsInvalid(t *testing.T) {
	paths := testPaths(t)
	for _, pid := range []int{0, -5} {
		if err := daemon.WritePID(paths, pid); err == nil {
			t.Errorf("WritePID(%d) succeeded, want error", pid)
		}
	}
}

func TestCheckRunningStalePIDCleanup(t *testing.T) {
	paths := testPaths(t)
	// 999999 is far above any real pid; syscall.Kill(999999, 0) → ESRCH.
	const deadPID = 999999
	if err := daemon.WritePID(paths, deadPID); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	if err := os.WriteFile(paths.SocketFile, []byte("stale"), 0o600); err != nil {
		t.Fatalf("create stale socket: %v", err)
	}

	pid, running, err := daemon.CheckRunning(paths)
	if err != nil {
		t.Fatalf("CheckRunning: %v", err)
	}
	if pid != deadPID {
		t.Errorf("pid = %d, want %d", pid, deadPID)
	}
	if running {
		t.Error("running = true, want false for dead pid")
	}
	for _, f := range []string{paths.PIDFile, paths.SocketFile} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s still exists after stale cleanup (err=%v)", f, err)
		}
	}
}

func TestCheckRunningLivePID(t *testing.T) {
	paths := testPaths(t)
	if err := daemon.WritePID(paths, os.Getpid()); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	pid, running, err := daemon.CheckRunning(paths)
	if err != nil {
		t.Fatalf("CheckRunning: %v", err)
	}
	if !running {
		t.Error("running = false, want true for live pid")
	}
	if pid != os.Getpid() {
		t.Errorf("pid = %d, want %d", pid, os.Getpid())
	}
}

func TestCheckRunningNoPIDFile(t *testing.T) {
	paths := testPaths(t)
	pid, running, err := daemon.CheckRunning(paths)
	if err != nil {
		t.Fatalf("CheckRunning: %v", err)
	}
	if pid != 0 || running {
		t.Errorf("pid = %d, running = %v, want 0/false", pid, running)
	}
}

func TestCheckRunningCorruptPIDFile(t *testing.T) {
	paths := testPaths(t)
	if err := os.WriteFile(paths.PIDFile, []byte("not-a-pid"), 0o600); err != nil {
		t.Fatalf("write corrupt pid file: %v", err)
	}
	if _, _, err := daemon.CheckRunning(paths); err == nil {
		t.Error("CheckRunning succeeded, want error for corrupt pid file")
	}
}

func TestCleanupRemovesFiles(t *testing.T) {
	paths := testPaths(t)
	if err := daemon.WritePID(paths, 42); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	if err := os.WriteFile(paths.SocketFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("create socket file: %v", err)
	}
	daemon.Cleanup(paths)
	for _, f := range []string{paths.PIDFile, paths.SocketFile} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s still exists after Cleanup (err=%v)", f, err)
		}
	}
}
