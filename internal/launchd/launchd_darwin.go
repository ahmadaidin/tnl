//go:build darwin

package launchd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Install writes the LaunchAgent plist for the tnl daemon and registers it
// with launchctl so the daemon starts at login.
func Install(binPath string) error {
	content, err := plistContents(binPath)
	if err != nil {
		return err
	}
	path, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents directory: %w", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write launchd plist: %w", err)
	}
	cmd := runCmd("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Uninstall removes the tnl LaunchAgent: it unloads the service with
// launchctl (ignoring failures, e.g. when it is not loaded) and deletes the
// plist file.
func Uninstall() error {
	_ = runCmd("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), label)).Run()
	path, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove launchd plist: %w", err)
	}
	return nil
}
