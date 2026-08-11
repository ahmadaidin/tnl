package daemon_test

import (
	"net"
	"testing"
	"time"

	"github.com/ahmadaidin/tnl/internal/daemon"
)

func TestWaitForSocketAlreadyPresent(t *testing.T) {
	paths := testPaths(t)
	ln, err := net.Listen("unix", paths.SocketFile)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	if err := daemon.WaitForSocket(paths, 2*time.Second); err != nil {
		t.Errorf("WaitForSocket: %v", err)
	}
}

func TestWaitForSocketAppears(t *testing.T) {
	paths := testPaths(t)
	stop := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		ln, err := net.Listen("unix", paths.SocketFile)
		if err != nil {
			return
		}
		defer ln.Close()
		<-stop
	}()

	if err := daemon.WaitForSocket(paths, 5*time.Second); err != nil {
		t.Errorf("WaitForSocket: %v", err)
	}
	close(stop)
}

func TestWaitForSocketTimeout(t *testing.T) {
	paths := testPaths(t)
	start := time.Now()
	if err := daemon.WaitForSocket(paths, 120*time.Millisecond); err == nil {
		t.Fatal("WaitForSocket succeeded, want timeout error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeout took %v, polling is too slow", elapsed)
	}
}

func TestWaitForSocketRejectsZeroTimeout(t *testing.T) {
	paths := testPaths(t)
	if err := daemon.WaitForSocket(paths, 0); err == nil {
		t.Error("WaitForSocket with zero timeout succeeded, want error")
	}
}
