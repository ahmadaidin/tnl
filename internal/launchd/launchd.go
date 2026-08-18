//go:build darwin

// Package launchd integrates tnl with macOS launchd by installing and
// removing a per-user LaunchAgent that starts the daemon at login.
package launchd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
