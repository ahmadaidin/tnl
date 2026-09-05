//go:build !darwin && !linux

package service

import "errors"

// errUnsupported is returned by Install and Uninstall on platforms without a
// supported service manager.
var errUnsupported = errors.New("tnl install is only supported on macOS (launchd) and Linux (systemd)")

// unsupportedManager reports an error on platforms without launchd or systemd.
type unsupportedManager struct{}

// New returns a Manager that reports the unsupported-platform error.
func New() Manager { return unsupportedManager{} }

// Install reports that service integration is unavailable on this platform.
func (unsupportedManager) Install(binPath string) error {
	return errUnsupported
}

// Uninstall reports that service integration is unavailable on this platform.
func (unsupportedManager) Uninstall() error {
	return errUnsupported
}
