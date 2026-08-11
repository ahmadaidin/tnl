//go:build !darwin

package launchd

import "errors"

// errUnsupported is returned by Install and Uninstall on platforms without
// launchd.
var errUnsupported = errors.New("launchd integration is only supported on macOS")

// Install reports that launchd integration is unavailable off macOS.
func Install(binPath string) error {
	return errUnsupported
}

// Uninstall reports that launchd integration is unavailable off macOS.
func Uninstall() error {
	return errUnsupported
}
