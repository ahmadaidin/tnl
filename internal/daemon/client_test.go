package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ahmadaidin/tnl/internal/daemon"
)

func TestClientErrorsFastOnMissingSocket(t *testing.T) {
	paths := testPaths(t) // no server listening

	start := time.Now()
	_, err := daemon.QueryStatus(context.Background(), paths)
	if err == nil {
		t.Fatal("QueryStatus succeeded, want error for missing socket")
	}
	if !strings.Contains(err.Error(), paths.SocketFile) {
		t.Errorf("error %q does not mention socket %q", err, paths.SocketFile)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("missing socket should fail fast, took %v", elapsed)
	}

	start = time.Now()
	_, err = daemon.SendCommand(context.Background(), paths, "status", "")
	if err == nil {
		t.Fatal("SendCommand succeeded, want error for missing socket")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("missing socket should fail fast, took %v", elapsed)
	}
}

func TestClientRespectsContextCancellation(t *testing.T) {
	// A socket path that cannot be dialed because the context dies first.
	paths := testPaths(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if _, err := daemon.SendCommand(ctx, paths, "status", ""); err == nil {
		t.Fatal("SendCommand succeeded, want context error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("cancelled context should fail fast, took %v", elapsed)
	}
}

func TestClientHonorsContextDeadlineAfterConnect(t *testing.T) {
	paths := testPaths(t)
	_ = os.Remove(paths.SocketFile)
	listener, err := net.Listen("unix", paths.SocketFile)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	handlerDone := make(chan struct{})
	releaseHandler := make(chan struct{})
	go func() {
		defer close(handlerDone)
		defer listener.Close()
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		var req daemon.Request
		_ = json.NewDecoder(conn).Decode(&req)
		// Deliberately leave the response unread by the client.
		<-releaseHandler
	}()
	defer func() {
		close(releaseHandler)
		<-handlerDone
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = daemon.QueryStatus(ctx, paths)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("QueryStatus succeeded, want response-read timeout")
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("QueryStatus error = %v, want timeout net.Error", err)
	}
	if elapsed >= time.Second {
		t.Fatalf("context deadline was not honored after connect: %v", elapsed)
	}
}
