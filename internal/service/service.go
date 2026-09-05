// Package service installs and removes the tnl daemon as a user service on
// the current platform: a launchd LaunchAgent on macOS, a systemd user unit
// on Linux. On other platforms the Manager methods report an error.
package service

// Manager installs and removes the tnl daemon as a service that starts
// automatically.
type Manager interface {
	Install(binPath string) error
	Uninstall() error
}
