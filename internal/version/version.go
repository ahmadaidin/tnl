// Package version holds the tnl build version.
package version

// Version is the build version. It defaults to "dev" and can be overridden
// at link time with -ldflags "-X github.com/ahmadaidin/tnl/internal/version.Version=<ver>".
var Version = "dev"

// String returns the current version string.
func String() string {
	return Version
}
