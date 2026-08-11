package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// reclaimPortLsof terminates the process listening on the local port so a
// mapping can take it over. The occupant gets SIGINT, killGrace to exit, then
// SIGKILL — the same kill contract as supervised children. Processes owned by
// another user are refused: signal 0 returns EPERM in that case.
func reclaimPortLsof(local int) error {
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(local), "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return fmt.Errorf("lsof on port %d: %w", local, err)
	}
	pids := parsePIDs(string(out))
	if len(pids) == 0 {
		return fmt.Errorf("no listener found on port %d", local)
	}
	var firstErr error
	for _, pid := range pids {
		if err := terminateListener(pid); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// parsePIDs extracts numeric process IDs from lsof -t output.
func parsePIDs(out string) []int {
	var pids []int
	for _, line := range strings.Split(out, "\n") {
		if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

// terminateListener sends SIGINT to pid, waits killGrace for it to exit, then
// SIGKILLs it. It refuses to signal processes owned by another user.
func terminateListener(pid int) error {
	// Signal 0 probes existence and permission: EPERM means another user owns it.
	switch err := syscall.Kill(pid, 0); err {
	case nil:
	case syscall.EPERM:
		return fmt.Errorf("pid %d is not owned by this user", pid)
	default:
		return fmt.Errorf("pid %d already gone", pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	_ = proc.Signal(os.Interrupt)
	deadline := time.Now().Add(killGrace)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return nil // exited during the grace period
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = proc.Kill()
	return nil
}
