package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// clientDialTimeout bounds how long the CLI waits to connect to the daemon
// socket. A missing socket fails immediately; this caps slow or blocked
// listeners.
const clientDialTimeout = 2 * time.Second

// clientRoundTripTimeout bounds the whole request/response exchange once
// connected.
const clientRoundTripTimeout = 5 * time.Second

// QueryStatus asks the daemon for its current snapshot of tunnel statuses.
func QueryStatus(ctx context.Context, paths Paths) (*Response, error) {
	return roundTrip(ctx, paths, Request{Command: "status"})
}

// SendCommand sends a lifecycle command ("start", "stop", "restart",
// "shutdown") to the daemon. The dial timeout is 2s; a daemon that is not
// running fails fast.
func SendCommand(ctx context.Context, paths Paths, command, tunnel string) (*Response, error) {
	return roundTrip(ctx, paths, Request{Command: command, Tunnel: tunnel})
}

// roundTrip sends one request over the daemon socket and reads one response.
func roundTrip(ctx context.Context, paths Paths, req Request) (*Response, error) {
	dialer := net.Dialer{Timeout: clientDialTimeout}
	conn, err := dialer.DialContext(ctx, "unix", paths.SocketFile)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon socket %s: %w", paths.SocketFile, err)
	}
	defer func() { _ = conn.Close() }()

	deadline := time.Now().Add(clientRoundTripTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("set socket deadline: %w", err)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("send request to daemon: %w", err)
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read response from daemon: %w", err)
	}
	return &resp, nil
}
