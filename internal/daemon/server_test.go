package daemon_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ahmadaidin/tnl/internal/daemon"
	"github.com/ahmadaidin/tnl/internal/status"
)

// fakeController records lifecycle calls and serves a fixed snapshot,
// failing tunnels listed in fail.
type fakeController struct {
	mu        sync.Mutex
	started   []string
	stopped   []string
	restarted []string
	snap      []status.TunnelStatus
	fail      map[string]error
}

func (f *fakeController) StartTunnel(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[name]; err != nil {
		return err
	}
	f.started = append(f.started, name)
	return nil
}

func (f *fakeController) StopTunnel(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[name]; err != nil {
		return err
	}
	f.stopped = append(f.stopped, name)
	return nil
}

func (f *fakeController) RestartTunnel(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[name]; err != nil {
		return err
	}
	f.restarted = append(f.restarted, name)
	return nil
}

func (f *fakeController) Snapshot() []status.TunnelStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

// startServer runs a Server on a fresh temp-dir socket and returns once the
// socket is listening. The server is stopped and verified on test cleanup.
func startServer(t *testing.T, ctrl daemon.Controller, stopFn func()) (daemon.Paths, context.CancelFunc) {
	t.Helper()
	paths := testPaths(t)
	ctx, cancel := context.WithCancel(context.Background())
	srv := daemon.NewServer(paths, ctrl, 12345, stopFn)
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	if err := daemon.WaitForSocket(paths, 5*time.Second); err != nil {
		cancel()
		t.Fatalf("server did not start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server Run returned error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("server did not shut down")
		}
		_ = os.Remove(paths.SocketFile)
	})
	return paths, cancel
}

func TestServerStatusRoundTrip(t *testing.T) {
	fake := &fakeController{snap: []status.TunnelStatus{
		{Name: "web", Mappings: []status.MappingStatus{
			{Label: "primary", Local: 3000, Remote: 3000, State: status.StateActive},
		}},
		{Name: "db", Mappings: []status.MappingStatus{
			{Local: 5432, Remote: 5432, State: status.StateBackoff, Attempt: 3, Message: "restarting (attempt 3)"},
		}},
	}}
	paths, _ := startServer(t, fake, nil)

	resp, err := daemon.QueryStatus(context.Background(), paths)
	if err != nil {
		t.Fatalf("QueryStatus: %v", err)
	}
	if !resp.Running {
		t.Error("Running = false, want true")
	}
	if resp.Mode != "daemon" {
		t.Errorf("Mode = %q, want daemon", resp.Mode)
	}
	if resp.PID != 12345 {
		t.Errorf("PID = %d, want 12345", resp.PID)
	}
	if !reflect.DeepEqual(resp.Tunnels, fake.snap) {
		t.Errorf("Tunnels mismatch:\n got %#v\nwant %#v", resp.Tunnels, fake.snap)
	}
}

func TestServerDispatchLifecycleCommands(t *testing.T) {
	fake := &fakeController{}
	paths, _ := startServer(t, fake, nil)
	ctx := context.Background()

	for _, tc := range []struct {
		command string
		tunnel  string
		wantMsg string
	}{
		{"start", "web", "started"},
		{"stop", "db", "stopped"},
		{"restart", "db", "restarted"},
	} {
		resp, err := daemon.SendCommand(ctx, paths, tc.command, tc.tunnel)
		if err != nil {
			t.Fatalf("SendCommand(%s %s): %v", tc.command, tc.tunnel, err)
		}
		if resp.Error != "" {
			t.Errorf("SendCommand(%s %s) Error = %q", tc.command, tc.tunnel, resp.Error)
		}
		if resp.Message != tc.wantMsg {
			t.Errorf("SendCommand(%s %s) Message = %q, want %q", tc.command, tc.tunnel, resp.Message, tc.wantMsg)
		}
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !reflect.DeepEqual(fake.started, []string{"web"}) {
		t.Errorf("started = %v, want [web]", fake.started)
	}
	if !reflect.DeepEqual(fake.stopped, []string{"db"}) {
		t.Errorf("stopped = %v, want [db]", fake.stopped)
	}
	if !reflect.DeepEqual(fake.restarted, []string{"db"}) {
		t.Errorf("restarted = %v, want [db]", fake.restarted)
	}
}

func TestServerUnknownTunnelSetsError(t *testing.T) {
	fake := &fakeController{fail: map[string]error{"nope": errors.New("unknown tunnel: nope")}}
	paths, _ := startServer(t, fake, nil)

	resp, err := daemon.SendCommand(context.Background(), paths, "start", "nope")
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if resp.Error != "unknown tunnel: nope" {
		t.Errorf("Error = %q, want %q", resp.Error, "unknown tunnel: nope")
	}
	if resp.Message != "" {
		t.Errorf("Message = %q, want empty on failure", resp.Message)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.started) != 0 {
		t.Errorf("started = %v, want empty", fake.started)
	}
}

func TestServerUnknownCommand(t *testing.T) {
	paths, _ := startServer(t, &fakeController{}, nil)

	resp, err := daemon.SendCommand(context.Background(), paths, "frobnicate", "")
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if resp.Error != "unknown command: frobnicate" {
		t.Errorf("Error = %q, want %q", resp.Error, "unknown command: frobnicate")
	}
}

func TestServerShutdownInvokesStopFn(t *testing.T) {
	var stopped atomic.Bool
	paths, _ := startServer(t, &fakeController{}, func() { stopped.Store(true) })

	resp, err := daemon.SendCommand(context.Background(), paths, "shutdown", "")
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if resp.Error != "" {
		t.Errorf("Error = %q", resp.Error)
	}
	if resp.Message != "shutting down" {
		t.Errorf("Message = %q, want %q", resp.Message, "shutting down")
	}

	deadline := time.Now().Add(2 * time.Second)
	for !stopped.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !stopped.Load() {
		t.Error("stopFn was not invoked after shutdown response")
	}
}

func TestServerMultipleClientsConcurrently(t *testing.T) {
	fake := &fakeController{}
	paths, _ := startServer(t, fake, nil)
	ctx := context.Background()

	const n = 8
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			resp, err := daemon.SendCommand(ctx, paths, "start", "web")
			if err != nil {
				errs <- err
				return
			}
			if resp.Error != "" || resp.Message != "started" {
				errs <- errors.New("unexpected response")
				return
			}
			errs <- nil
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent client %d: %v", i, err)
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.started) != n {
		t.Errorf("started = %d calls, want %d", len(fake.started), n)
	}
}

func TestServerSocketPermissions(t *testing.T) {
	paths, _ := startServer(t, &fakeController{}, nil)
	fi, err := os.Stat(paths.SocketFile)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket mode = %o, want 600", perm)
	}
}
