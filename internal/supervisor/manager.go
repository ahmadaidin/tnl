// Package supervisor spawns and supervises one system ssh process per port
// mapping, restarting dead mappings with exponential backoff and supporting
// per-tunnel lifecycle control.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/ahmadaidin/tnl/internal/config"
	"github.com/ahmadaidin/tnl/internal/status"
)

// killGrace is how long a child gets to exit after SIGINT before SIGKILL.
const killGrace = 2 * time.Second

// Options configures a Manager. Zero values select the documented defaults.
type Options struct {
	SSHBin        string        // ssh binary; default "ssh"
	BackoffBase   time.Duration // default 1s
	BackoffCap    time.Duration // default 60s
	BackoffJitter float64       // multiplicative ± jitter; default 0.2
	FailedAfter   int           // attempt count that flips the message to "failed - retrying with backoff"; default 5
	DialInterval  time.Duration // port probe polling interval; default 250ms
	DialTimeout   time.Duration // port probe timeout; default 300ms
	Log           *log.Logger   // nil = discard
	Selected      []string      // explicit tunnel names; override Enabled

	// PortProber reports whether a TCP connection to addr can be established.
	// A nil return means the local port is occupied. Defaults to a
	// net.Dialer with DialTimeout.
	PortProber PortProber

	// ReclaimPort terminates the process listening on a colliding local port
	// when the tunnel has reclaim: true. Defaults to an lsof-based
	// implementation that refuses to kill processes owned by other users.
	ReclaimPort func(local int) error
}

// PortProber reports whether a TCP connection to addr can be established.
// A nil return means the port is already in use.
type PortProber func(ctx context.Context, addr string) error

// Manager supervises the tunnels declared in a Config. A Manager is not safe
// for concurrent use until Run has been called; afterwards StartTunnel,
// StopTunnel, RestartTunnel, and Snapshot are safe from any goroutine.
type Manager struct {
	cfg   *config.Config
	opts  Options
	store *status.Store
	probe PortProber
	// reclaim terminates the occupant of a colliding local port when the
	// tunnel has reclaim: true. Injectable for tests.
	reclaim func(local int) error

	mu       sync.Mutex
	root     context.Context
	selected map[string]bool
	stopped  map[string]bool
	force    map[string]bool
	loops    map[string]*tunnelLoop
	attempts map[string]map[string]int
	states   map[string][]status.MappingStatus
}

// tunnelLoop is the per-tunnel supervision goroutine handle.
type tunnelLoop struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// NewManager returns a Manager supervising the tunnels in cfg.
func NewManager(cfg *config.Config, opts Options) *Manager {
	if cfg == nil {
		cfg = &config.Config{Tunnels: map[string]*config.Tunnel{}}
	}
	opts = resolveOptions(opts)
	m := &Manager{
		cfg:      cfg,
		opts:     opts,
		store:    status.NewStore(),
		selected: make(map[string]bool),
		stopped:  make(map[string]bool),
		force:    make(map[string]bool),
		loops:    make(map[string]*tunnelLoop),
		attempts: make(map[string]map[string]int),
		states:   make(map[string][]status.MappingStatus),
	}
	m.probe = opts.PortProber
	if m.probe == nil {
		m.probe = defaultProber(opts.DialTimeout)
	}
	m.reclaim = opts.ReclaimPort
	if m.reclaim == nil {
		m.reclaim = reclaimPortLsof
	}
	for _, n := range opts.Selected {
		m.selected[n] = true
	}
	for _, name := range cfg.SortedNames() {
		t := cfg.Tunnels[name]
		ms := make([]status.MappingStatus, len(t.Mappings))
		for i, mp := range t.Mappings {
			ms[i] = status.MappingStatus{
				Label:      mp.Label,
				Local:      mp.Local,
				RemoteHost: mp.RemoteHost,
				Remote:     mp.Remote,
				State:      status.StateStopped,
			}
		}
		m.states[name] = ms
		m.store.EnsureTunnel(name, t.Mappings)
	}
	return m
}

// resolveOptions fills in the documented defaults for zero-valued options.
func resolveOptions(opts Options) Options {
	if opts.SSHBin == "" {
		opts.SSHBin = "ssh"
	}
	if opts.BackoffBase <= 0 {
		opts.BackoffBase = time.Second
	}
	if opts.BackoffCap <= 0 {
		opts.BackoffCap = 60 * time.Second
	}
	if opts.BackoffJitter <= 0 {
		opts.BackoffJitter = 0.2
	}
	if opts.FailedAfter <= 0 {
		opts.FailedAfter = 5
	}
	if opts.DialInterval <= 0 {
		opts.DialInterval = 250 * time.Millisecond
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 300 * time.Millisecond
	}
	if opts.Log == nil {
		opts.Log = log.New(io.Discard, "", 0)
	}
	return opts
}

// defaultProber reports whether addr accepts TCP connections.
func defaultProber(timeout time.Duration) PortProber {
	return func(ctx context.Context, addr string) error {
		d := net.Dialer{Timeout: timeout}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		return conn.Close()
	}
}

// Run supervises all Wanted tunnels, blocking until ctx is cancelled, then
// gracefully stops every child and returns nil.
func (m *Manager) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.root != nil {
		m.mu.Unlock()
		return errors.New("manager already running")
	}
	m.root = ctx
	m.mu.Unlock()

	for _, name := range m.cfg.SortedNames() {
		if m.wanted(name) {
			_ = m.startLoop(name)
		}
	}
	<-ctx.Done()

	m.mu.Lock()
	loops := make([]*tunnelLoop, 0, len(m.loops))
	for _, l := range m.loops {
		loops = append(loops, l)
	}
	m.mu.Unlock()
	for _, l := range loops {
		l.cancel()
	}
	for _, l := range loops {
		<-l.done
	}
	return nil
}

// StartTunnel makes name Wanted and starts supervising it. It is a no-op if
// the tunnel is already running. Unknown names return an error.
func (m *Manager) StartTunnel(name string) error {
	if !m.known(name) {
		return fmt.Errorf("unknown tunnel %q", name)
	}
	return m.startLoop(name)
}

// StopTunnel makes name not-Wanted, stops its ssh children, and blocks until
// the tunnel's supervision loop has fully exited.
func (m *Manager) StopTunnel(name string) error {
	if !m.known(name) {
		return fmt.Errorf("unknown tunnel %q", name)
	}
	m.mu.Lock()
	m.stopped[name] = true
	l := m.loops[name]
	m.mu.Unlock()
	if l != nil {
		l.cancel()
		<-l.done
	}
	return nil
}

// RestartTunnel stops name, resets its restart attempts, and starts it again.
func (m *Manager) RestartTunnel(name string) error {
	if !m.known(name) {
		return fmt.Errorf("unknown tunnel %q", name)
	}
	m.mu.Lock()
	l := m.loops[name]
	m.mu.Unlock()
	if l != nil {
		l.cancel()
		<-l.done
	}
	m.mu.Lock()
	delete(m.attempts, name)
	m.mu.Unlock()
	return m.startLoop(name)
}

// Snapshot returns the current status of every tunnel.
func (m *Manager) Snapshot() []status.TunnelStatus {
	return m.store.Snapshot()
}

// known reports whether name is declared in the config.
func (m *Manager) known(name string) bool {
	return m.cfg.Tunnels[name] != nil
}

// wanted reports whether a tunnel should be supervised: enabled in config (or
// explicitly selected/started) and not manually stopped.
func (m *Manager) wanted(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped[name] {
		return false
	}
	if m.force[name] {
		return true
	}
	return m.cfg.Enabled(name) || m.selected[name]
}

// startLoop launches the supervision goroutine for name unless one is already
// running. A started tunnel is marked Wanted regardless of Enabled.
func (m *Manager) startLoop(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.loops[name]; ok {
		return nil // already running
	}
	root := m.root
	if root == nil {
		root = context.Background()
	}
	ctx, cancel := context.WithCancel(root)
	l := &tunnelLoop{cancel: cancel, done: make(chan struct{})}
	m.loops[name] = l
	m.stopped[name] = false
	m.force[name] = true
	go func() {
		m.runTunnel(ctx, name)
		m.mu.Lock()
		if m.loops[name] == l {
			delete(m.loops, name)
		}
		m.mu.Unlock()
		close(l.done)
	}()
	return nil
}

// runTunnel supervises every mapping of a tunnel concurrently.
func (m *Manager) runTunnel(ctx context.Context, name string) {
	t := m.cfg.Tunnels[name]
	var wg sync.WaitGroup
	for _, mp := range t.Mappings {
		wg.Add(1)
		go func(mp config.Mapping) {
			defer wg.Done()
			m.runMapping(ctx, name, mp)
		}(mp)
	}
	wg.Wait()
}

// runMapping is the per-mapping supervision loop: probe the local port, spawn
// ssh, poll for active, and on exit either stop or back off and respawn.
func (m *Manager) runMapping(ctx context.Context, name string, mp config.Mapping) {
	key := mappingKey(mp.Local, mp.Remote)
	addr := fmt.Sprintf("127.0.0.1:%d", mp.Local)
	// reclaimed tracks whether this spawn cycle already killed a port
	// occupant; at most one reclaim attempt per cycle, so a re-occupied port
	// falls through to the error + backoff path instead of killing again.
	reclaimed := false
	for {
		if ctx.Err() != nil || !m.wanted(name) {
			m.resetAttempt(name, key)
			m.setMapping(name, mp, status.StateStopped, 0, "")
			return
		}

		// Port probe before spawn: a successful probe means the local port is
		// occupied, so spawning would fail. Reclaim it once if configured,
		// otherwise stay Wanted and retry with backoff.
		pctx, cancel := context.WithTimeout(ctx, m.opts.DialTimeout)
		probeErr := m.probe(pctx, addr)
		cancel()
		if probeErr == nil {
			if !reclaimed && m.cfg.Tunnels[name].Reclaim {
				reclaimed = true
				if err := m.reclaim(mp.Local); err != nil {
					m.opts.Log.Printf("tunnel %s: reclaim of port %d failed: %v", name, mp.Local, err)
				} else {
					m.opts.Log.Printf("tunnel %s: reclaimed port %d", name, mp.Local)
					continue
				}
			}
			m.setMapping(name, mp, status.StateError, m.attempt(name, key),
				fmt.Sprintf("port %d in use", mp.Local))
			if !m.waitBackoff(ctx, name, key) {
				m.setMapping(name, mp, status.StateStopped, 0, "")
				return
			}
			continue
		}
		reclaimed = false

		cmd, err := m.startProcess(name, mp)
		if err != nil {
			m.setMapping(name, mp, status.StateError, m.attempt(name, key),
				fmt.Sprintf("failed to start ssh: %v", err))
			if !m.waitBackoff(ctx, name, key) {
				m.setMapping(name, mp, status.StateStopped, 0, "")
				return
			}
			continue
		}
		m.opts.Log.Printf("tunnel %s: mapping %d:%d spawned (pid %d)",
			name, mp.Local, mp.Remote, cmd.Process.Pid)

		procDone := make(chan error, 1)
		go func() { procDone <- cmd.Wait() }()
		m.setMapping(name, mp, status.StateConnecting, m.attempt(name, key), "")

		// Poll the local port until it accepts connections or the process exits.
		active := false
		ticker := time.NewTicker(m.opts.DialInterval)
	loop:
		for {
			select {
			case <-procDone:
				break loop
			case <-ctx.Done():
				_ = m.killAndWait(cmd, procDone)
				break loop
			case <-ticker.C:
				pctx, cancel := context.WithTimeout(ctx, m.opts.DialTimeout)
				probeErr := m.probe(pctx, addr)
				cancel()
				if probeErr == nil {
					active = true
					m.setMapping(name, mp, status.StateActive, m.attempt(name, key), "")
					break loop
				}
			}
		}
		ticker.Stop()
		if active {
			select {
			case <-procDone:
			case <-ctx.Done():
				_ = m.killAndWait(cmd, procDone)
			}
		}

		if ctx.Err() != nil || !m.wanted(name) {
			m.resetAttempt(name, key)
			m.setMapping(name, mp, status.StateStopped, 0, "")
			return
		}

		// The process exited while the mapping is still Wanted: back off and
		// respawn with an incremented attempt count.
		n := m.bumpAttempt(name, key)
		msg := fmt.Sprintf("restarting (attempt %d)", n)
		if n >= m.opts.FailedAfter {
			msg = "failed - retrying with backoff"
		}
		m.opts.Log.Printf("tunnel %s: mapping %d:%d exited; %s",
			name, mp.Local, mp.Remote, msg)
		m.setMapping(name, mp, status.StateBackoff, n, msg)
		if !m.waitBackoff(ctx, name, key) {
			m.setMapping(name, mp, status.StateStopped, 0, "")
			return
		}
	}
}

// startProcess spawns the ssh process for a mapping, capturing its output in
// a small ring buffer.
func (m *Manager) startProcess(name string, mp config.Mapping) (*exec.Cmd, error) {
	t := m.cfg.Tunnels[name]
	ring := newLastLines(4)
	cmd := exec.Command(m.opts.SSHBin, spawnArgs(t, mp)...)
	cmd.Stdout = ring
	cmd.Stderr = ring
	// If the process detaches a child that keeps the output pipes open,
	// Wait would block forever; bound it like the kill grace.
	cmd.WaitDelay = killGrace
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// killAndWait sends SIGINT, gives the process killGrace to exit, then SIGKILL.
func (m *Manager) killAndWait(cmd *exec.Cmd, done <-chan error) error {
	_ = cmd.Process.Signal(os.Interrupt)
	select {
	case err := <-done:
		return err
	case <-time.After(killGrace):
		_ = cmd.Process.Kill()
		return <-done
	}
}

// waitBackoff sleeps for the backoff delay of the current attempt, returning
// false if ctx was cancelled first.
func (m *Manager) waitBackoff(ctx context.Context, name, key string) bool {
	attempt := m.attempt(name, key)
	d := backoffDelay(m.opts.BackoffBase, m.opts.BackoffCap, m.opts.BackoffJitter, attempt)
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// mappingKey uniquely identifies a mapping within a tunnel.
func mappingKey(local, remote int) string {
	return fmt.Sprintf("%d:%d", local, remote)
}

// attempt returns the current restart attempt count for a mapping.
func (m *Manager) attempt(name, key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.attempts[name][key]
}

// bumpAttempt increments and returns the restart attempt count for a mapping.
func (m *Manager) bumpAttempt(name, key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.attempts[name] == nil {
		m.attempts[name] = make(map[string]int)
	}
	m.attempts[name][key]++
	return m.attempts[name][key]
}

// resetAttempt clears the restart attempt count for a mapping.
func (m *Manager) resetAttempt(name, key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.attempts[name] != nil {
		delete(m.attempts[name], key)
	}
}

// setMapping records a mapping's status both in the manager's authoritative
// slice and in the status.Store.
func (m *Manager) setMapping(name string, mp config.Mapping, state status.MappingState, attempt int, msg string) {
	ms := status.MappingStatus{
		Label:      mp.Label,
		Local:      mp.Local,
		RemoteHost: mp.RemoteHost,
		Remote:     mp.Remote,
		State:      state,
		Attempt:    attempt,
		Message:    msg,
	}
	key := mappingKey(mp.Local, mp.Remote)
	m.mu.Lock()
	defer m.mu.Unlock()
	sl := m.states[name]
	for i := range sl {
		if mappingKey(sl[i].Local, sl[i].Remote) == key {
			sl[i] = ms
			break
		}
	}
	cp := make([]status.MappingStatus, len(sl))
	copy(cp, sl)
	m.store.UpdateTunnel(name, cp)
}
