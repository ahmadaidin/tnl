// Package daemon implements the tnl daemon: runtime path resolution,
// pid-file handling, the Unix-socket IPC server, and the client used by the
// tnl CLI to talk to a running daemon.
package daemon

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths holds the runtime files used by the daemon. Every path lives inside
// a single private runtime directory so the whole footprint can be cleaned
// up in one place.
type Paths struct {
	RuntimeDir string
	PIDFile    string
	SocketFile string
	LogFile    string
}

// ResolvePaths returns the daemon's runtime paths, creating the runtime
// directory if needed. The directory is $XDG_RUNTIME_DIR/tnl when
// XDG_RUNTIME_DIR is set, otherwise ~/.cache/tnl. It is created with 0700
// permissions and explicitly re-chmodded to 0700 so a permissive umask
// cannot widen it.
func ResolvePaths() (Paths, error) {
	var dir string
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		dir = filepath.Join(xdg, "tnl")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("resolve runtime dir: %w", err)
		}
		dir = filepath.Join(home, ".cache", "tnl")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Paths{}, fmt.Errorf("create runtime dir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return Paths{}, fmt.Errorf("set permissions on %s: %w", dir, err)
	}
	return Paths{
		RuntimeDir: dir,
		PIDFile:    filepath.Join(dir, "daemon.pid"),
		SocketFile: filepath.Join(dir, "daemon.sock"),
		LogFile:    filepath.Join(dir, "daemon.log"),
	}, nil
}
