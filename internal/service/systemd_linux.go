//go:build linux

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
	// unitName is the systemd user unit filename.
	unitName = "tnl.service"
	// serviceName is the unit name passed to systemctl (extension omitted).
	serviceName = "tnl"
)

// runCmd builds the command that invokes systemctl. It is a variable so
// tests can substitute a fake and never execute the real systemctl.
var runCmd = exec.Command

// systemdUnitDir returns the user systemd unit directory, honouring
// XDG_CONFIG_HOME. It is a variable so tests can redirect writes to a
// temporary directory.
var systemdUnitDir = func() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

type unitData struct {
	BinPath string
}

// unitTemplate renders the systemd user unit: ExecStart runs the daemon
// binary with --internal-daemon, and Restart is off so systemd never
// resurrects the daemon after `tnl stop` (the launchd KeepAlive false
// equivalent).
var unitTemplate = template.Must(template.New("unit").Parse(
	`[Unit]
Description=tnl SSH tunnel manager daemon

[Service]
Type=simple
ExecStart={{ .BinPath }} --internal-daemon
Restart=no

[Install]
WantedBy=default.target
`))

// unitPath returns the absolute path of the tnl systemd user unit file.
func unitPath() (string, error) {
	dir, err := systemdUnitDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, unitName), nil
}

// unitContents renders the systemd user unit for the daemon binary at
// binPath.
func unitContents(binPath string) ([]byte, error) {
	var buf bytes.Buffer
	if err := unitTemplate.Execute(&buf, unitData{BinPath: binPath}); err != nil {
		return nil, fmt.Errorf("render systemd unit: %w", err)
	}
	return buf.Bytes(), nil
}

// systemdManager installs and removes the tnl systemd user unit.
type systemdManager struct{}

// New returns a Manager backed by systemd, the Linux service manager.
func New() Manager { return systemdManager{} }

// Install writes the tnl systemd user unit, reloads systemd so the new unit
// is scanned, and enables it so the daemon starts automatically.
func (systemdManager) Install(binPath string) error {
	content, err := unitContents(binPath)
	if err != nil {
		return err
	}
	path, err := unitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create systemd user unit directory: %w", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}
	cmd := runCmd("systemctl", "--user", "daemon-reload")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	cmd = runCmd("systemctl", "--user", "enable", serviceName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Uninstall removes the tnl systemd user unit: it stops and disables the
// service (ignoring failures, e.g. when it was never installed), deletes the
// unit file, and reloads systemd to drop the unit from memory.
func (systemdManager) Uninstall() error {
	_ = runCmd("systemctl", "--user", "stop", serviceName).Run()
	_ = runCmd("systemctl", "--user", "disable", serviceName).Run()
	path, err := unitPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove systemd unit: %w", err)
	}
	cmd := runCmd("systemctl", "--user", "daemon-reload")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
