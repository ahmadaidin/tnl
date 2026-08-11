package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ahmadaidin/tnl/internal/config"
	"github.com/ahmadaidin/tnl/internal/status"
)

// shimPath is the fake ssh script, relative to this package's directory.
const shimPath = "../testutil/fakessh.sh"

// stubProber simulates the local-port probe. success=true means the port
// accepts connections (i.e. it is in use); success=false means it is free.
type stubProber struct {
	mu      sync.Mutex
	success bool
}

func (p *stubProber) probe(context.Context, string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.success {
		return nil
	}
	return errors.New("connection refused")
}

func (p *stubProber) set(v bool) {
	p.mu.Lock()
	p.success = v
	p.mu.Unlock()
}

func testConfig() *config.Config {
	return &config.Config{Tunnels: map[string]*config.Tunnel{
		"web": {
			Name: "web",
			Host: "example.com",
			Mappings: []config.Mapping{
				{Label: "primary", Local: 3000, Remote: 3000},
			},
		},
	}}
}

func boolPtr(v bool) *bool { return &v }

// fastOpts returns Options tuned for fast, deterministic tests.
func fastOpts(probe PortProber) Options {
	return Options{
		SSHBin:        shimPath,
		PortProber:    probe,
		BackoffBase:   10 * time.Millisecond,
		BackoffCap:    100 * time.Millisecond,
		BackoffJitter: 0.1,
		FailedAfter:   100,
		DialInterval:  5 * time.Millisecond,
		DialTimeout:   5 * time.Millisecond,
	}
}

// readLines reads a shim log file, one spawn argv per line.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read log: %v", err)
	}
	raw := strings.TrimRight(string(b), "\n")
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}

func spawnCount(t *testing.T, path string) int {
	t.Helper()
	return len(readLines(t, path))
}

// waitFor polls cond until it returns true or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

// startManager runs the manager and returns a stop function that cancels the
// context and waits for Run to fully stop.
func startManager(t *testing.T, m *Manager) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := m.Run(ctx); err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("manager did not stop within 5s of cancel")
		}
	}
}

func firstMapping(t *testing.T, m *Manager) status.MappingStatus {
	t.Helper()
	snap := m.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot has %d tunnels, want 1: %+v", len(snap), snap)
	}
	if len(snap[0].Mappings) != 1 {
		t.Fatalf("snapshot has %d mappings, want 1", len(snap[0].Mappings))
	}
	return snap[0].Mappings[0]
}

func TestSpawnArgvExact(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_SSH_LOG", logPath)
	cfg := &config.Config{Tunnels: map[string]*config.Tunnel{
		"web": {
			Name:         "web",
			Host:         "example.com",
			User:         "alice",
			IdentityFile: "$HOME/.ssh/id_ed25519",
			Mappings:     []config.Mapping{{Label: "primary", Local: 3000, Remote: 3000}},
		},
	}}
	m := NewManager(cfg, fastOpts((&stubProber{}).probe))
	stop := startManager(t, m)
	defer stop()

	waitFor(t, 5*time.Second, "first spawn", func() bool {
		return spawnCount(t, logPath) >= 1
	})
	want := fmt.Sprintf(
		"-N -L 3000:localhost:3000 -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -o ExitOnForwardFailure=yes -i %s -l alice example.com",
		os.ExpandEnv("$HOME/.ssh/id_ed25519"),
	)
	got := readLines(t, logPath)[0]
	if got != want {
		t.Fatalf("argv mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestImmediateExitBackoffRespawns(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_SSH_LOG", logPath)
	t.Setenv("FAKE_SSH_EXIT_IMMEDIATE", "1")
	m := NewManager(testConfig(), fastOpts((&stubProber{}).probe))
	stop := startManager(t, m)
	defer stop()

	waitFor(t, 5*time.Second, ">= 2 respawns with attempt >= 2", func() bool {
		ms := firstMapping(t, m)
		return ms.Attempt >= 2
	})
	if got := spawnCount(t, logPath); got < 2 {
		t.Fatalf("spawns = %d, want >= 2", got)
	}
	ms := firstMapping(t, m)
	if ms.State != status.StateBackoff {
		t.Fatalf("state = %q, want backing off", ms.State)
	}
	if !strings.Contains(ms.Message, "restarting (attempt") {
		t.Fatalf("message = %q, want restarting (attempt N)", ms.Message)
	}
}

func TestFailedAfterMessage(t *testing.T) {
	t.Setenv("FAKE_SSH_EXIT_IMMEDIATE", "1")
	opts := fastOpts((&stubProber{}).probe)
	opts.FailedAfter = 3
	m := NewManager(testConfig(), opts)
	stop := startManager(t, m)
	defer stop()

	waitFor(t, 5*time.Second, "attempt >= 3", func() bool {
		return firstMapping(t, m).Attempt >= 3
	})
	ms := firstMapping(t, m)
	if ms.Message != "failed - retrying with backoff" {
		t.Fatalf("message = %q, want failed - retrying with backoff", ms.Message)
	}
}

func TestStopTunnelNoRespawn(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_SSH_LOG", logPath)
	m := NewManager(testConfig(), fastOpts((&stubProber{}).probe))
	stop := startManager(t, m)
	defer stop()

	waitFor(t, 5*time.Second, "first spawn", func() bool {
		return spawnCount(t, logPath) >= 1
	})
	if err := m.StopTunnel("web"); err != nil {
		t.Fatalf("StopTunnel: %v", err)
	}
	// StopTunnel blocks until the loop has exited; the mapping must be stopped.
	ms := firstMapping(t, m)
	if ms.State != status.StateStopped {
		t.Fatalf("state = %q, want stopped", ms.State)
	}
	// Give a wrongly-respawning supervisor a window, then count spawns.
	time.Sleep(60 * time.Millisecond)
	if got := spawnCount(t, logPath); got != 1 {
		t.Fatalf("spawns after stop = %d, want 1 (no respawn)", got)
	}
}

func TestStartTunnelAfterStop(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_SSH_LOG", logPath)
	m := NewManager(testConfig(), fastOpts((&stubProber{}).probe))
	stop := startManager(t, m)
	defer stop()

	waitFor(t, 5*time.Second, "first spawn", func() bool {
		return spawnCount(t, logPath) >= 1
	})
	if err := m.StopTunnel("web"); err != nil {
		t.Fatalf("StopTunnel: %v", err)
	}
	if got := spawnCount(t, logPath); got != 1 {
		t.Fatalf("spawns while stopped = %d, want 1", got)
	}
	if err := m.StartTunnel("web"); err != nil {
		t.Fatalf("StartTunnel: %v", err)
	}
	waitFor(t, 5*time.Second, "respawn after start", func() bool {
		return spawnCount(t, logPath) >= 2
	})
}

func TestStartTunnelAlreadyRunningNoOp(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_SSH_LOG", logPath)
	m := NewManager(testConfig(), fastOpts((&stubProber{}).probe))
	stop := startManager(t, m)
	defer stop()

	waitFor(t, 5*time.Second, "first spawn", func() bool {
		return spawnCount(t, logPath) >= 1
	})
	if err := m.StartTunnel("web"); err != nil {
		t.Fatalf("StartTunnel on running tunnel: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if got := spawnCount(t, logPath); got != 1 {
		t.Fatalf("spawns = %d, want 1 (start on running tunnel must be a no-op)", got)
	}
}

func TestPortInUseThenRecovery(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_SSH_LOG", logPath)
	p := &stubProber{success: true} // local port occupied
	m := NewManager(testConfig(), fastOpts(p.probe))
	stop := startManager(t, m)
	defer stop()

	waitFor(t, 5*time.Second, "state error with port-in-use message", func() bool {
		ms := firstMapping(t, m)
		return ms.State == status.StateError && ms.Message == "port 3000 in use"
	})
	if got := spawnCount(t, logPath); got != 0 {
		t.Fatalf("spawns while port in use = %d, want 0", got)
	}

	p.set(false) // port freed: the supervisor must recover and spawn
	waitFor(t, 5*time.Second, "spawn after port frees", func() bool {
		return spawnCount(t, logPath) >= 1
	})
	waitFor(t, 5*time.Second, "state leaves error", func() bool {
		return firstMapping(t, m).State != status.StateError
	})
}

func TestConnectingToActive(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_SSH_LOG", logPath)
	p := &stubProber{} // port free: spawn, then connecting
	m := NewManager(testConfig(), fastOpts(p.probe))
	stop := startManager(t, m)
	defer stop()

	waitFor(t, 5*time.Second, "state connecting", func() bool {
		return firstMapping(t, m).State == status.StateConnecting
	})
	p.set(true) // local port starts accepting connections
	waitFor(t, 5*time.Second, "state active", func() bool {
		ms := firstMapping(t, m)
		return ms.State == status.StateActive && ms.Message == ""
	})
	if ms := firstMapping(t, m); ms.Attempt != 0 {
		t.Fatalf("attempt = %d, want 0 while active", ms.Attempt)
	}
}

func TestRestartTunnelResetsAttempts(t *testing.T) {
	t.Setenv("FAKE_SSH_EXIT_IMMEDIATE", "1")
	opts := fastOpts((&stubProber{}).probe)
	opts.BackoffBase = 50 * time.Millisecond // long enough to observe attempt 1
	m := NewManager(testConfig(), opts)
	stop := startManager(t, m)
	defer stop()

	waitFor(t, 5*time.Second, "attempt >= 2 before restart", func() bool {
		return firstMapping(t, m).Attempt >= 2
	})
	if err := m.RestartTunnel("web"); err != nil {
		t.Fatalf("RestartTunnel: %v", err)
	}
	// RestartTunnel resets attempts: a fresh sequence must start at 1.
	waitFor(t, 5*time.Second, "fresh attempt 1 after restart", func() bool {
		return firstMapping(t, m).Attempt == 1
	})
}

func TestUnknownTunnel(t *testing.T) {
	m := NewManager(testConfig(), fastOpts((&stubProber{}).probe))
	for _, op := range []struct {
		name string
		fn   func() error
	}{
		{"StartTunnel", func() error { return m.StartTunnel("nope") }},
		{"StopTunnel", func() error { return m.StopTunnel("nope") }},
		{"RestartTunnel", func() error { return m.RestartTunnel("nope") }},
	} {
		err := op.fn()
		if err == nil {
			t.Fatalf("%s(nope) = nil, want error", op.name)
		}
		if !strings.Contains(err.Error(), "nope") {
			t.Fatalf("%s error = %q, want mention of the name", op.name, err)
		}
	}
}

func TestEnabledFalseNotAutoStartedButStartable(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_SSH_LOG", logPath)
	cfg := &config.Config{Tunnels: map[string]*config.Tunnel{
		"on":  {Name: "on", Host: "h", Mappings: []config.Mapping{{Local: 3000, Remote: 3000}}},
		"off": {Name: "off", Host: "h", Enabled: boolPtr(false), Mappings: []config.Mapping{{Local: 3001, Remote: 3001}}},
	}}
	m := NewManager(cfg, fastOpts((&stubProber{}).probe))
	stop := startManager(t, m)
	defer stop()

	// Only the enabled tunnel is supervised by Run.
	waitFor(t, 5*time.Second, "enabled tunnel spawns", func() bool {
		return spawnCount(t, logPath) >= 1
	})
	snap := m.Snapshot()
	for _, ts := range snap {
		if ts.Name == "off" {
			for _, ms := range ts.Mappings {
				if ms.State != status.StateStopped {
					t.Fatalf("off mapping state = %q, want stopped (not auto-started)", ms.State)
				}
			}
		}
	}
	// Explicit start wins over enabled: false.
	if err := m.StartTunnel("off"); err != nil {
		t.Fatalf("StartTunnel(off): %v", err)
	}
	waitFor(t, 5*time.Second, "off spawns after explicit start", func() bool {
		return spawnCount(t, logPath) >= 2
	})
}

func TestSelectedOverridesEnabledFalse(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_SSH_LOG", logPath)
	cfg := &config.Config{Tunnels: map[string]*config.Tunnel{
		"off": {Name: "off", Host: "h", Enabled: boolPtr(false), Mappings: []config.Mapping{{Local: 3001, Remote: 3001}}},
	}}
	opts := fastOpts((&stubProber{}).probe)
	opts.Selected = []string{"off"}
	m := NewManager(cfg, opts)
	stop := startManager(t, m)
	defer stop()

	waitFor(t, 5*time.Second, "selected tunnel spawns despite enabled: false", func() bool {
		return spawnCount(t, logPath) >= 1
	})
}

func TestSnapshotReflectsStates(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_SSH_LOG", logPath)
	cfg := &config.Config{Tunnels: map[string]*config.Tunnel{
		"beta":  {Name: "beta", Host: "h", Mappings: []config.Mapping{{Local: 3001, Remote: 3001}}},
		"alpha": {Name: "alpha", Host: "h", Mappings: []config.Mapping{{Local: 3000, Remote: 3000}}},
	}}
	m := NewManager(cfg, fastOpts((&stubProber{}).probe))

	snap := m.Snapshot()
	if len(snap) != 2 || snap[0].Name != "alpha" || snap[1].Name != "beta" {
		t.Fatalf("initial snapshot = %+v, want [alpha beta] sorted", snap)
	}
	for _, ts := range snap {
		if ts.Mappings[0].State != status.StateStopped {
			t.Fatalf("initial state of %s = %q, want stopped", ts.Name, ts.Mappings[0].State)
		}
	}

	stop := startManager(t, m)
	waitFor(t, 5*time.Second, "alpha connecting", func() bool {
		snap := m.Snapshot()
		return len(snap) == 2 && snap[0].Name == "alpha" && snap[0].Mappings[0].State == status.StateConnecting
	})
	stop() // graceful shutdown must bring every mapping back to stopped

	snap = m.Snapshot()
	for _, ts := range snap {
		if ts.Mappings[0].State != status.StateStopped {
			t.Fatalf("after shutdown, %s state = %q, want stopped", ts.Name, ts.Mappings[0].State)
		}
	}
}
