package supervisor

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ahmadaidin/tnl/internal/status"
)

// TestReclaimOnCollision: a colliding port with reclaim: true must trigger the
// reclaimer, and once the port frees the mapping must spawn normally.
func TestReclaimOnCollision(t *testing.T) {
	probe := &stubProber{success: true}
	var reclaims atomic.Int32
	cfg := testConfig()
	cfg.Tunnels["web"].Reclaim = true

	opts := fastOpts(probe.probe)
	opts.ReclaimPort = func(local int) error {
		reclaims.Add(1)
		probe.set(false) // the occupant is gone: port now free
		return nil
	}
	logPath := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_SSH_LOG", logPath)

	m := NewManager(cfg, opts)
	stop := startManager(t, m)
	defer stop()

	waitFor(t, 2*time.Second, "reclaim to run", func() bool { return reclaims.Load() == 1 })
	waitFor(t, 2*time.Second, "mapping to spawn", func() bool { return spawnCount(t, logPath) >= 1 })
	waitFor(t, 2*time.Second, "mapping to leave error state", func() bool {
		return firstMapping(t, m).State != status.StateError
	})
	if ms := firstMapping(t, m); ms.State != status.StateConnecting {
		t.Errorf("state = %s, want connecting", ms.State)
	}
}

// TestReclaimFailureFallsBackToError: when the reclaimer fails, the mapping
// reports "port in use" and stays in backoff, reclaiming only once per cycle.
func TestReclaimFailureFallsBackToError(t *testing.T) {
	probe := &stubProber{success: true}
	var reclaims atomic.Int32
	cfg := testConfig()
	cfg.Tunnels["web"].Reclaim = true

	opts := fastOpts(probe.probe)
	opts.ReclaimPort = func(local int) error {
		reclaims.Add(1)
		return os.ErrPermission // occupant not killable (e.g. other user)
	}

	m := NewManager(cfg, opts)
	stop := startManager(t, m)
	defer stop()

	waitFor(t, 2*time.Second, "mapping to report port in use", func() bool {
		ms := firstMapping(t, m)
		return ms.State == status.StateError && ms.Message == "port 3000 in use"
	})
	time.Sleep(50 * time.Millisecond)
	if n := reclaims.Load(); n != 1 {
		t.Errorf("reclaim called %d times, want exactly 1", n)
	}
}

// TestNoReclaimWithoutFlag: reclaim: false (the default) never calls the
// reclaimer, even though the port is colliding.
func TestNoReclaimWithoutFlag(t *testing.T) {
	probe := &stubProber{success: true}
	var reclaims atomic.Int32
	cfg := testConfig() // Reclaim defaults to false

	opts := fastOpts(probe.probe)
	opts.ReclaimPort = func(local int) error {
		reclaims.Add(1)
		return nil
	}

	m := NewManager(cfg, opts)
	stop := startManager(t, m)
	defer stop()

	waitFor(t, 2*time.Second, "mapping to report port in use", func() bool {
		ms := firstMapping(t, m)
		return ms.State == status.StateError && ms.Message == "port 3000 in use"
	})
	if n := reclaims.Load(); n != 0 {
		t.Errorf("reclaim called %d times, want 0", n)
	}
}

// TestReclaimPortLsofKillsListener exercises the real lsof-based reclaimer
// against a helper process that actually binds the port.
func TestReclaimPortLsofKillsListener(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof not installed")
	}
	port := freePort(t)
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperListener")
	cmd.Env = append(os.Environ(), "TNL_HELPER_LISTEN=1", "TNL_HELPER_PORT="+strconv.Itoa(port))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	probe := defaultProber(100 * time.Millisecond)
	addr := "127.0.0.1:" + strconv.Itoa(port)
	waitFor(t, 5*time.Second, "helper to listen", func() bool {
		return probe(t.Context(), addr) == nil
	})

	if err := reclaimPortLsof(port); err != nil {
		t.Fatalf("reclaimPortLsof: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not exit after reclaim")
	}
	if probe(t.Context(), addr) == nil {
		t.Errorf("port %d still accepting connections after reclaim", port)
	}
}

// TestHelperListener is the child process for TestReclaimPortLsofKillsListener:
// it binds TNL_HELPER_PORT and blocks until killed.
func TestHelperListener(t *testing.T) {
	if os.Getenv("TNL_HELPER_LISTEN") == "" {
		t.Skip("helper process; not a real test")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("TNL_HELPER_PORT"))
	if err != nil {
		os.Exit(2)
	}
	defer func() { _ = ln.Close() }()
	// Block until terminated. A bare select{} would trip the runtime's
	// deadlock detector and kill the process right after binding.
	for {
		time.Sleep(time.Hour)
	}
}

// freePort returns a currently unused TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}
