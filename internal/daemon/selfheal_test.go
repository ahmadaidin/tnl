package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmadaidin/tnl/internal/status"
)

// TestSelfHealPIDRestoresMissingFile: a daemon whose pid file was removed
// must rewrite it so CheckRunning still reports it running.
func TestSelfHealPIDRestoresMissingFile(t *testing.T) {
	paths := testPaths(t)
	pid := os.Getpid()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); selfHealPID(ctx, paths, pid, 20*time.Millisecond) }()

	waitForFile(t, paths.PIDFile, "pid file to be restored")
	if got, err := ReadPID(paths); err != nil || got != pid {
		t.Errorf("ReadPID = %d, %v; want %d", got, err, pid)
	}
	cancel()
	<-done
}

// TestSelfHealPIDOverwritesWrongPid: a pid file holding a stale pid must be
// corrected, preventing a duplicate daemon launch.
func TestSelfHealPIDOverwritesWrongPid(t *testing.T) {
	paths := testPaths(t)
	if err := WritePID(paths, 12345); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	pid := os.Getpid()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); selfHealPID(ctx, paths, pid, 20*time.Millisecond) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if got, err := ReadPID(paths); err == nil && got == pid {
			break // healed: pid file rewritten to the live pid
		}
		if time.Now().After(deadline) {
			got, _ := ReadPID(paths)
			t.Fatalf("pid file not healed: got %d, want %d", got, pid)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

// testPaths returns Paths anchored in a short temp dir (macOS rejects unix
// socket paths longer than 104 bytes).
func testPaths(t *testing.T) Paths {
	t.Helper()
	dir := t.TempDir()
	short := filepath.Join(os.TempDir(), "tnl-test-"+filepath.Base(dir))
	if err := os.MkdirAll(short, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	return Paths{
		RuntimeDir: short,
		PIDFile:    filepath.Join(short, "pid"),
		SocketFile: filepath.Join(short, "sock"),
		LogFile:    filepath.Join(short, "log"),
	}
}

// waitForFile polls for the existence of path.
func waitForFile(t *testing.T, path, desc string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

// stubController is a minimal Controller for server tests in this package.
type stubController struct{}

func (stubController) StartTunnel(string) error                { return nil }
func (stubController) StopTunnel(string) error                 { return nil }
func (stubController) RestartTunnel(string) error              { return nil }
func (stubController) Snapshot() []status.TunnelStatus         { return nil }

// TestServerSocketSelfHeal: removing the socket file from underneath a
// running server must not orphan it — the heal loop re-creates the socket
// and the server keeps answering.
func TestServerSocketSelfHeal(t *testing.T) {
	paths := testPaths(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := NewServer(paths, stubController{}, 12345, nil)
	srv.healInterval = 20 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Errorf("server did not shut down")
		}
	})
	if err := WaitForSocket(paths, 2*time.Second); err != nil {
		t.Fatalf("server did not start: %v", err)
	}

	if _, err := QueryStatus(ctx, paths); err != nil {
		t.Fatalf("status before heal: %v", err)
	}
	if err := os.Remove(paths.SocketFile); err != nil {
		t.Fatalf("remove socket: %v", err)
	}
	waitForFile(t, paths.SocketFile, "socket to be re-created")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := QueryStatus(context.Background(), paths); err == nil {
			break // healed: server answers again
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not answer after socket heal")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The socket must survive subsequent heal ticks: closing a replaced
	// listener must not unlink the replacement's socket file (Go's
	// UnixListener.Close would, absent SetUnlinkOnClose(false)).
	time.Sleep(5 * srv.healInterval)
	if _, err := os.Stat(paths.SocketFile); err != nil {
		t.Fatalf("socket file vanished after heal ticks: %v", err)
	}
	if _, err := QueryStatus(context.Background(), paths); err != nil {
		t.Fatalf("status after heal ticks: %v", err)
	}
}
