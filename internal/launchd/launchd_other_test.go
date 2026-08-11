//go:build !darwin

package launchd

import "testing"

func TestUnsupportedPlatform(t *testing.T) {
	const want = "launchd integration is only supported on macOS"
	if err := Install("/usr/local/bin/tnl"); err == nil || err.Error() != want {
		t.Errorf("Install error = %v, want %q", err, want)
	}
	if err := Uninstall(); err == nil || err.Error() != want {
		t.Errorf("Uninstall error = %v, want %q", err, want)
	}
}
