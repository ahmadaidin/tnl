//go:build darwin

package service

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const (
	// label is the launchd service label; it also names the plist file.
	label = "com.ahmadaidin.tnl"
	// plistName is the LaunchAgent property list filename.
	plistName = "com.ahmadaidin.tnl.plist"
)

// runCmd builds the command that invokes launchctl. It is a variable so
// tests can substitute a fake and never execute the real launchctl.
var runCmd = exec.Command

// launchAgentsDir returns the per-user LaunchAgents directory. It is a
// variable so tests can redirect writes to a temporary directory.
var launchAgentsDir = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

type plistData struct {
	Label   string
	BinPath string
}

// plistTemplate renders the LaunchAgent plist: Label, ProgramArguments
// (daemon binary plus --internal-daemon), RunAtLoad true, KeepAlive false.
var plistTemplate = template.Must(template.New("plist").Parse(
	`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{ .Label }}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{ .BinPath }}</string>
		<string>--internal-daemon</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<false/>
</dict>
</plist>
`))

// plistPath returns the absolute path of the LaunchAgent plist file.
func plistPath() (string, error) {
	dir, err := launchAgentsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, plistName), nil
}

// plistContents renders the LaunchAgent plist for the daemon binary at
// binPath.
func plistContents(binPath string) ([]byte, error) {
	var buf bytes.Buffer
	if err := plistTemplate.Execute(&buf, plistData{Label: label, BinPath: binPath}); err != nil {
		return nil, fmt.Errorf("render launchd plist: %w", err)
	}
	return buf.Bytes(), nil
}

// launchdManager installs and removes the tnl LaunchAgent.
type launchdManager struct{}

// New returns a Manager backed by launchd, the macOS service manager.
func New() Manager { return launchdManager{} }

// Install writes the LaunchAgent plist for the tnl daemon and registers it
// with launchctl so the daemon starts at login.
func (launchdManager) Install(binPath string) error {
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
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	// Reinstalling must replace an already-loaded LaunchAgent. bootout is
	// intentionally best-effort because the first install has nothing to unload.
	_ = runCmd("launchctl", "bootout", domain+"/"+label).Run()
	cmd := runCmd("launchctl", "bootstrap", domain, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Uninstall removes the tnl LaunchAgent: it unloads the service with
// launchctl (ignoring failures, e.g. when it is not loaded) and deletes the
// plist file.
func (launchdManager) Uninstall() error {
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
