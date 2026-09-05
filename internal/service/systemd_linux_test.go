//go:build linux

package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner returns a runCmd replacement that records every invocation and
// returns a command that exits successfully (or with failure when fail is
// true) without ever touching the real systemctl.
func fakeRunner(calls *[][]string, fail bool) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		*calls = append(*calls, append([]string{name}, args...))
		if fail {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
}

// swapUnitDir redirects systemdUnitDir to dir for the duration of the test.
func swapUnitDir(t *testing.T, dir string) {
	t.Helper()
	old := systemdUnitDir
	systemdUnitDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { systemdUnitDir = old })
}

// swapRunner substitutes the recorded fake runner for the duration of the test.
func swapRunner(t *testing.T, calls *[][]string, fail bool) {
	t.Helper()
	old := runCmd
	runCmd = fakeRunner(calls, fail)
	t.Cleanup(func() { runCmd = old })
}

func equalArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// assertUnitContent verifies the required unit sections and directives:
// Description, Type=simple, ExecStart containing binPath then
// --internal-daemon, Restart=no, and WantedBy=default.target.
func assertUnitContent(t *testing.T, content, binPath string) {
	t.Helper()
	for _, want := range []string{
		"[Unit]",
		"Description=tnl SSH tunnel manager daemon",
		"[Service]",
		"Type=simple",
		"Restart=no",
		"[Install]",
		"WantedBy=default.target",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("unit missing %q", want)
		}
	}
	execIdx := strings.Index(content, "ExecStart=")
	binIdx := strings.Index(content, "ExecStart="+binPath)
	daemonIdx := strings.Index(content, "--internal-daemon")
	if execIdx < 0 {
		t.Errorf("unit missing ExecStart")
	}
	if binIdx < 0 {
		t.Errorf("unit missing ExecStart entry for %q", binPath)
	}
	if daemonIdx < 0 {
		t.Errorf("unit missing --internal-daemon argument")
	}
	if binIdx >= 0 && daemonIdx >= 0 && binIdx > daemonIdx {
		t.Errorf("unit ExecStart order: binary must precede --internal-daemon")
	}
}

func TestInstallWritesUnitReloadsEnablesAndStarts(t *testing.T) {
	dir := t.TempDir()
	swapUnitDir(t, dir)
	var calls [][]string
	swapRunner(t, &calls, false)

	const binPath = "/usr/local/bin/tnl"
	if err := New().Install(binPath); err != nil {
		t.Fatalf("Install returned error: %v", err)
	}

	path := filepath.Join(dir, unitName)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	assertUnitContent(t, string(content), binPath)

	wantReload := []string{"systemctl", "--user", "daemon-reload"}
	wantEnable := []string{"systemctl", "--user", "enable", serviceName}
	wantStart := []string{"systemctl", "--user", "start", serviceName}
	if len(calls) != 3 {
		t.Fatalf("systemctl invoked %d times, want 3: %v", len(calls), calls)
	}
	if !equalArgs(calls[0], wantReload) {
		t.Errorf("daemon-reload invocation = %q, want %q", calls[0], wantReload)
	}
	if !equalArgs(calls[1], wantEnable) {
		t.Errorf("enable invocation = %q, want %q", calls[1], wantEnable)
	}
	if !equalArgs(calls[2], wantStart) {
		t.Errorf("start invocation = %q, want %q", calls[2], wantStart)
	}
}

func TestInstallPropagatesSystemctlFailure(t *testing.T) {
	dir := t.TempDir()
	swapUnitDir(t, dir)
	var calls [][]string
	swapRunner(t, &calls, true)

	err := New().Install("/usr/local/bin/tnl")
	if err == nil {
		t.Fatal("Install succeeded despite failed systemctl, want error")
	}
	if !strings.Contains(err.Error(), "systemctl daemon-reload") {
		t.Errorf("Install error = %q, want it to mention systemctl daemon-reload", err)
	}
}

func TestUninstallStopsDisablesAndRemovesUnit(t *testing.T) {
	dir := t.TempDir()
	swapUnitDir(t, dir)
	path := filepath.Join(dir, unitName)
	if err := os.WriteFile(path, []byte("stub"), 0o644); err != nil {
		t.Fatalf("seed unit: %v", err)
	}
	var calls [][]string
	swapRunner(t, &calls, false)

	if err := New().Uninstall(); err != nil {
		t.Fatalf("Uninstall returned error: %v", err)
	}

	wantStop := []string{"systemctl", "--user", "stop", serviceName}
	wantDisable := []string{"systemctl", "--user", "disable", serviceName}
	wantReload := []string{"systemctl", "--user", "daemon-reload"}
	if len(calls) != 3 {
		t.Fatalf("systemctl invoked %d times, want 3: %v", len(calls), calls)
	}
	if !equalArgs(calls[0], wantStop) {
		t.Errorf("stop invocation = %q, want %q", calls[0], wantStop)
	}
	if !equalArgs(calls[1], wantDisable) {
		t.Errorf("disable invocation = %q, want %q", calls[1], wantDisable)
	}
	if !equalArgs(calls[2], wantReload) {
		t.Errorf("daemon-reload invocation = %q, want %q", calls[2], wantReload)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("unit still present after Uninstall (stat err = %v)", err)
	}
}

func TestUninstallIgnoresStopDisableFailure(t *testing.T) {
	dir := t.TempDir()
	swapUnitDir(t, dir)
	path := filepath.Join(dir, unitName)
	if err := os.WriteFile(path, []byte("stub"), 0o644); err != nil {
		t.Fatalf("seed unit: %v", err)
	}
	var calls [][]string
	swapRunner(t, &calls, true)

	if err := New().Uninstall(); err != nil {
		t.Fatalf("Uninstall returned error despite failed stop/disable: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("unit still present after Uninstall (stat err = %v)", err)
	}
}

func TestUninstallWithoutInstallIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	swapUnitDir(t, dir)
	var calls [][]string
	swapRunner(t, &calls, true)

	if err := New().Uninstall(); err != nil {
		t.Fatalf("Uninstall returned error on never-installed unit: %v", err)
	}
}
