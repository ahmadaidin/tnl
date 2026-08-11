package supervisor

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/ahmadaidin/tnl/internal/config"
)

const (
	keepaliveInterval = "30"
	keepaliveCountMax = "3"
)

// spawnArgs builds the ssh argv for a mapping: keepalive options, then the
// optional identity file (environment-expanded), optional user, then the host.
func spawnArgs(t *config.Tunnel, mp config.Mapping) []string {
	dest := mp.RemoteHost
	if dest == "" {
		dest = "localhost"
	}
	args := []string{
		"-N",
		"-L", fmt.Sprintf("%d:%s:%d", mp.Local, dest, mp.Remote),
		"-o", "ServerAliveInterval=" + keepaliveInterval,
		"-o", "ServerAliveCountMax=" + keepaliveCountMax,
		"-o", "ExitOnForwardFailure=yes",
	}
	if t.IdentityFile != "" {
		args = append(args, "-i", os.ExpandEnv(t.IdentityFile))
	}
	if t.User != "" {
		args = append(args, "-l", t.User)
	}
	args = append(args, t.Host)
	return args
}

// lastLines is a bounded writer that retains the most recent complete lines
// of process output; the last line is surfaced in status messages.
type lastLines struct {
	mu   sync.Mutex
	part []byte
	ring []string
	max  int
}

func newLastLines(max int) *lastLines {
	if max < 1 {
		max = 1
	}
	return &lastLines{max: max}
}

func (w *lastLines) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.part = append(w.part, p...)
			break
		}
		w.part = append(w.part, p[:i]...)
		line := strings.TrimRight(string(w.part), "\r")
		w.part = w.part[:0]
		if line != "" {
			w.ring = append(w.ring, line)
			if len(w.ring) > w.max {
				w.ring = w.ring[len(w.ring)-w.max:]
			}
		}
		p = p[i+1:]
	}
	return n, nil
}

// last returns the most recent complete line, or "" if none was written.
func (w *lastLines) last() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.ring) == 0 {
		return ""
	}
	return w.ring[len(w.ring)-1]
}
