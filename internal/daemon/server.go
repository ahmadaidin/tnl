package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/ahmadaidin/tnl/internal/status"
)

// Request is one IPC command sent to the daemon over its Unix socket.
type Request struct {
	Command string `json:"command"`
	Tunnel  string `json:"tunnel,omitempty"`
}

// Response is the daemon's reply to a Request.
type Response struct {
	Running bool                  `json:"running"`
	Mode    string                `json:"mode"`
	PID     int                   `json:"pid"`
	Message string                `json:"message,omitempty"`
	Error   string                `json:"error,omitempty"`
	Tunnels []status.TunnelStatus `json:"tunnels,omitempty"`
}

// Controller is the subset of the supervisor's lifecycle API the daemon
// exposes over IPC.
type Controller interface {
	StartTunnel(name string) error
	StopTunnel(name string) error
	RestartTunnel(name string) error
	Snapshot() []status.TunnelStatus
}

// Server is the daemon's Unix-socket IPC server. It accepts exactly one JSON
// request per connection and answers with exactly one JSON response.
type Server struct {
	paths  Paths
	ctrl   Controller
	pid    int
	stopFn func()

	// healInterval is how often the server checks that its socket file still
	// exists. Shortened in tests.
	healInterval time.Duration

	// mu guards ln so the socket-heal loop can swap in a replacement
	// listener if the socket file is removed from underneath the daemon.
	mu sync.Mutex
	ln net.Listener
}

// NewServer returns a Server that dispatches IPC commands to ctrl, reports
// pid to clients, and calls stopFn after answering a "shutdown" request.
// stopFn may be nil; when set it must not block (the daemon main wires it to
// a context cancel).
func NewServer(paths Paths, ctrl Controller, pid int, stopFn func()) *Server {
	return &Server{paths: paths, ctrl: ctrl, pid: pid, stopFn: stopFn, healInterval: 5 * time.Second}
}

// connReadTimeout bounds how long the server waits for a client to send its
// request, so a half-open connection cannot pin a handler goroutine forever.
const connReadTimeout = 10 * time.Second

// listenUnix is net.Listen("unix", path) with Go's close-unlink behavior
// disabled: UnixListener.Close otherwise removes the socket path it was
// created with, so closing a listener replaced by the socket-heal loop would
// delete the replacement's socket file (same path). tnl manages the socket
// file lifecycle itself: Run removes a stale file before listening, and
// daemon.Cleanup removes it at exit.
func listenUnix(path string) (net.Listener, error) {
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if ul, ok := ln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	return ln, nil
}

// Run listens on paths.SocketFile and serves IPC requests until ctx is
// cancelled or the listener fails. A stale socket file left by a previous
// daemon is removed before listening, and the socket is created with 0600
// permissions. If the socket file is removed from underneath the daemon
// (e.g. by a stale-state cleanup from a concurrent client), the heal loop
// re-creates it so the daemon stays reachable and stoppable. Run returns nil
// on graceful cancellation.
func (s *Server) Run(ctx context.Context) error {
	if err := os.Remove(s.paths.SocketFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket %s: %w", s.paths.SocketFile, err)
	}
	ln, err := listenUnix(s.paths.SocketFile)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.paths.SocketFile, err)
	}
	if err := os.Chmod(s.paths.SocketFile, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("set socket permissions %s: %w", s.paths.SocketFile, err)
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	// The socket file is removed by daemon.Cleanup only after the supervisor
	// has killed every child, so client stop-polling sees the daemon as
	// running until it has fully exited.
	defer s.closeListener()

	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	go func() {
		for {
			cur := s.current()
			conn, err := cur.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					// Listener closed during shutdown; not an error.
					return
				default:
				}
				if errors.Is(err, net.ErrClosed) {
					if s.current() == cur {
						// Genuine close (shutdown), not a heal swap.
						return
					}
					continue // the heal loop swapped in a replacement listener
				}
				select {
				case errCh <- err:
				default:
				}
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.handle(conn)
			}()
		}
	}()

	go s.healLoop(ctx)

	select {
	case <-ctx.Done():
	case err := <-errCh:
		s.closeListener()
		wg.Wait()
		return err
	}
	s.closeListener()
	wg.Wait()
	return nil
}

func (s *Server) healLoop(ctx context.Context) {
	t := time.NewTicker(s.healInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := os.Stat(s.paths.SocketFile); !errors.Is(err, os.ErrNotExist) {
				continue
			}
			ln, err := listenUnix(s.paths.SocketFile)
			if err != nil {
				continue // another daemon may hold the path; retry next tick
			}
			if err := os.Chmod(s.paths.SocketFile, 0o600); err != nil {
				_ = ln.Close()
				_ = os.Remove(s.paths.SocketFile)
				continue
			}
			s.swap(ln)
		}
	}
}

// current returns the active listener. It is never nil once Run has started.
func (s *Server) current() net.Listener {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ln
}

// swap installs a replacement listener and closes the old one. In-flight
// Accepts on the old listener return ErrClosed, which the accept loop treats
// as a retry when the current listener differs.
func (s *Server) swap(ln net.Listener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.ln
	s.ln = ln
	if old != nil {
		_ = old.Close()
	}
}

// closeListener closes the active listener. The listener is intentionally
// not nilled so the accept loop can distinguish a shutdown close from a heal
// swap.
func (s *Server) closeListener() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		_ = s.ln.Close()
	}
}

// handle serves a single connection: one request in, one response out.
func (s *Server) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(connReadTimeout))

	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		// Unparseable request; still answer so the client sees a response.
		_ = json.NewEncoder(conn).Encode(Response{
			Running: true, Mode: "daemon", PID: s.pid,
			Error: "invalid request: " + err.Error(),
		})
		return
	}
	resp := s.dispatch(&req)
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		return
	}
	// "shutdown" is answered before the daemon tears down, so the client is
	// guaranteed to see the response even though stopFn cancels everything.
	if req.Command == "shutdown" && s.stopFn != nil {
		s.stopFn()
	}
}

// dispatch resolves one IPC command against the controller. Lifecycle
// failures (including unknown tunnel names) are reported in Response.Error.
func (s *Server) dispatch(req *Request) Response {
	base := Response{Running: true, Mode: "daemon", PID: s.pid}
	switch req.Command {
	case "status":
		base.Tunnels = s.ctrl.Snapshot()
		return base
	case "start":
		if err := s.ctrl.StartTunnel(req.Tunnel); err != nil {
			base.Error = err.Error()
			return base
		}
		base.Message = "started"
		return base
	case "stop":
		if err := s.ctrl.StopTunnel(req.Tunnel); err != nil {
			base.Error = err.Error()
			return base
		}
		base.Message = "stopped"
		return base
	case "restart":
		if err := s.ctrl.RestartTunnel(req.Tunnel); err != nil {
			base.Error = err.Error()
			return base
		}
		base.Message = "restarted"
		return base
	case "shutdown":
		base.Message = "shutting down"
		return base
	default:
		base.Error = "unknown command: " + req.Command
		return base
	}
}
