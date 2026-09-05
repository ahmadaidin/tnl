//go:build !windows

package supervisor

import (
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

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
