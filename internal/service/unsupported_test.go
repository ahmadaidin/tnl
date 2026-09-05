//go:build !darwin && !linux

package service

import "testing"

func TestUnsupportedPlatform(t *testing.T) {
	const want = "tnl install is only supported on macOS (launchd) and Linux (systemd)"
	m := New()
	if err := m.Install("/usr/local/bin/tnl"); err == nil || err.Error() != want {
		t.Errorf("Install error = %v, want %q", err, want)
	}
	if err := m.Uninstall(); err == nil || err.Error() != want {
		t.Errorf("Uninstall error = %v, want %q", err, want)
	}
}
