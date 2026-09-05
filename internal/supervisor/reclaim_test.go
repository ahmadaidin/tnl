package supervisor

import (
	"os"
	"path/filepath"
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
