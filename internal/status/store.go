// Package status defines the supervision state model reported by tnl.
package status

import (
	"sort"
	"sync"

	"github.com/ahmadaidin/tnl/internal/config"
)

// MappingState is the supervision state of a single port mapping.
type MappingState string

const (
	// StateStopped means the mapping is not running and will not be restarted.
	StateStopped MappingState = "stopped"
	// StateConnecting means the ssh process is running but the local port is
	// not yet accepting TCP connections.
	StateConnecting MappingState = "connecting"
	// StateActive means the local port accepts TCP connections.
	StateActive MappingState = "active"
	// StateBackoff means a Wanted mapping's process exited and a restart is
	// scheduled with exponential backoff.
	StateBackoff MappingState = "backing off"
	// StateError means the mapping could not be started, e.g. because the
	// local port is occupied by another process.
	StateError MappingState = "error"
)

// MappingStatus is the runtime status of a single port mapping.
type MappingStatus struct {
	Label      string
	Local      int
	RemoteHost string // destination host; empty = the ssh host itself
	Remote     int
	State      MappingState
	Attempt    int    // > 0 while backing off
	Message    string // e.g. "port 3000 in use", "exit status 255"
}

// TunnelStatus is the runtime status of a tunnel and all of its mappings.
type TunnelStatus struct {
	Name     string
	Mappings []MappingStatus
}

// Store is a thread-safe registry of per-tunnel mapping statuses.
type Store struct {
	mu      sync.Mutex
	tunnels map[string][]MappingStatus
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{tunnels: make(map[string][]MappingStatus)}
}

// EnsureTunnel records name if it is absent, initializing every mapping to
// StateStopped. Existing tunnels are left untouched.
func (s *Store) EnsureTunnel(name string, mappings []config.Mapping) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tunnels[name]; ok {
		return
	}
	ms := make([]MappingStatus, len(mappings))
	for i, mp := range mappings {
		ms[i] = MappingStatus{
			Label:      mp.Label,
			Local:      mp.Local,
			RemoteHost: mp.RemoteHost,
			Remote:     mp.Remote,
			State:      StateStopped,
		}
	}
	s.tunnels[name] = ms
}

// UpdateTunnel replaces the status of every mapping of name.
func (s *Store) UpdateTunnel(name string, ms []MappingStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]MappingStatus, len(ms))
	copy(cp, ms)
	s.tunnels[name] = cp
}

// Snapshot returns a deep copy of all tunnel statuses, ordered by tunnel name.
func (s *Store) Snapshot() []TunnelStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.tunnels))
	for n := range s.tunnels {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]TunnelStatus, 0, len(names))
	for _, n := range names {
		ms := make([]MappingStatus, len(s.tunnels[n]))
		copy(ms, s.tunnels[n])
		out = append(out, TunnelStatus{Name: n, Mappings: ms})
	}
	return out
}
