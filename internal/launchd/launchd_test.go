//go:build darwin

package launchd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner returns a runCmd replacement that records every invocation and
// returns a command that exits successfully (or with failure when fail is
// true) without ever touching the real launchctl.
func fakeRunner(calls *[][]string, fail bool) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		*calls = append(*calls, append([]string{name}, args...))
		if fail {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
}

// swapDir redirects launchAgentsDir to dir for the duration of the test.
func swapDir(t *testing.T, dir string) {
	t.Helper()
	old := launchAgentsDir
	launchAgentsDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { launchAgentsDir = old })
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

// assertPlistContent verifies the required plist keys and values: Label,
// ProgramArguments containing binPath then --detach, RunAtLoad true, and
// KeepAlive false.
func assertPlistContent(t *testing.T, content, binPath string) {
	t.Helper()
	for _, want := range []string{
		"<key>Label</key>",
		"<string>" + label + "</string>",
		"<key>ProgramArguments</key>",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<key>KeepAlive</key>",
		"<false/>",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("plist missing %q", want)
		}
	}
	binIdx := strings.Index(content, "<string>"+binPath+"</string>")
	detachIdx := strings.Index(content, "<string>--detach</string>")
	if binIdx < 0 {
		t.Errorf("plist missing ProgramArguments entry for %q", binPath)
	}
	if detachIdx < 0 {
		t.Errorf("plist missing ProgramArguments entry for --detach")
	}
	if binIdx >= 0 && detachIdx >= 0 && binIdx > detachIdx {
		t.Errorf("plist ProgramArguments order: binary must precede --detach")
	}
}

func TestInstallWritesPlistAndBootstraps(t *testing.T) {
	dir := t.TempDir()
	swapDir(t, dir)
	var calls [][]string
	swapRunner(t, &calls, false)

	const binPath = "/usr/local/bin/tnl"
	if err := Install(binPath); err != nil {
		t.Fatalf("Install returned error: %v", err)
	}

	path := filepath.Join(dir, plistName)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read plist: %v", err)
	}
	assertPlistContent(t, string(content), binPath)

	want := []string{"launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), path}
	if len(calls) != 1 {
		t.Fatalf("launchctl invoked %d times, want 1: %v", len(calls), calls)
	}
	if !equalArgs(calls[0], want) {
		t.Errorf("launchctl invocation = %q, want %q", calls[0], want)
	}
}

func TestInstallPropagatesBootstrapFailure(t *testing.T) {
	dir := t.TempDir()
	swapDir(t, dir)
	var calls [][]string
	swapRunner(t, &calls, true)

	err := Install("/usr/local/bin/tnl")
	if err == nil {
		t.Fatal("Install succeeded despite failed launchctl bootstrap, want error")
	}
	if !strings.Contains(err.Error(), "launchctl bootstrap") {
		t.Errorf("Install error = %q, want it to mention launchctl bootstrap", err)
	}
}

func TestUninstallBootsOutAndRemovesPlist(t *testing.T) {
	dir := t.TempDir()
	swapDir(t, dir)
	path := filepath.Join(dir, plistName)
	if err := os.WriteFile(path, []byte("stub"), 0o644); err != nil {
		t.Fatalf("seed plist: %v", err)
	}
	var calls [][]string
	swapRunner(t, &calls, false)

	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall returned error: %v", err)
	}

	want := []string{"launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), label)}
	if len(calls) != 1 {
		t.Fatalf("launchctl invoked %d times, want 1: %v", len(calls), calls)
	}
	if !equalArgs(calls[0], want) {
		t.Errorf("launchctl invocation = %q, want %q", calls[0], want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("plist still present after Uninstall (stat err = %v)", err)
	}
}

func TestUninstallIgnoresBootoutFailure(t *testing.T) {
	dir := t.TempDir()
	swapDir(t, dir)
	path := filepath.Join(dir, plistName)
	if err := os.WriteFile(path, []byte("stub"), 0o644); err != nil {
		t.Fatalf("seed plist: %v", err)
	}
	var calls [][]string
	swapRunner(t, &calls, true)

	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall returned error despite failed bootout: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("plist still present after Uninstall (stat err = %v)", err)
	}
}
