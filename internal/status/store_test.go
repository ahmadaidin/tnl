package status

import (
	"testing"

	"github.com/ahmadaidin/tnl/internal/config"
)

func TestStoreEnsureTunnelInitializesStopped(t *testing.T) {
	s := NewStore()
	s.EnsureTunnel("web", []config.Mapping{
		{Label: "primary", Local: 3000, Remote: 3000},
		{Local: 3001, Remote: 3001},
	})
	snap := s.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot has %d tunnels, want 1", len(snap))
	}
	ts := snap[0]
	if ts.Name != "web" {
		t.Fatalf("tunnel name = %q, want web", ts.Name)
	}
	if len(ts.Mappings) != 2 {
		t.Fatalf("tunnel has %d mappings, want 2", len(ts.Mappings))
	}
	m0 := ts.Mappings[0]
	if m0.Label != "primary" || m0.Local != 3000 || m0.Remote != 3000 {
		t.Fatalf("mapping 0 = %+v, want label=primary local=3000 remote=3000", m0)
	}
	if m0.State != StateStopped || m0.Attempt != 0 || m0.Message != "" {
		t.Fatalf("mapping 0 default state = %+v, want stopped/0/empty", m0)
	}
}

func TestStoreEnsureTunnelIdempotent(t *testing.T) {
	s := NewStore()
	maps := []config.Mapping{{Local: 3000, Remote: 3000}}
	s.EnsureTunnel("web", maps)
	s.UpdateTunnel("web", []MappingStatus{{Local: 3000, Remote: 3000, State: StateActive}})
	// EnsureTunnel on an existing tunnel must not clobber its state.
	s.EnsureTunnel("web", maps)
	got := s.Snapshot()[0].Mappings[0]
	if got.State != StateActive {
		t.Fatalf("state = %q, want active (EnsureTunnel must not reset)", got.State)
	}
}

func TestStoreUpdateTunnelReplaces(t *testing.T) {
	s := NewStore()
	s.EnsureTunnel("web", []config.Mapping{{Local: 3000, Remote: 3000}, {Local: 3001, Remote: 3001}})
	s.UpdateTunnel("web", []MappingStatus{
		{Local: 3000, Remote: 3000, State: StateActive, Attempt: 2, Message: "x"},
	})
	got := s.Snapshot()[0].Mappings
	if len(got) != 1 {
		t.Fatalf("mappings = %d, want 1 (UpdateTunnel replaces the slice)", len(got))
	}
	if got[0].State != StateActive || got[0].Attempt != 2 || got[0].Message != "x" {
		t.Fatalf("mapping = %+v, want active/2/x", got[0])
	}
}

func TestStoreSnapshotSortedAndIsCopy(t *testing.T) {
	s := NewStore()
	s.EnsureTunnel("z", []config.Mapping{{Local: 3000, Remote: 3000}})
	s.EnsureTunnel("a", []config.Mapping{{Local: 3001, Remote: 3001}})
	snap := s.Snapshot()
	if snap[0].Name != "a" || snap[1].Name != "z" {
		t.Fatalf("snapshot order = [%s %s], want [a z]", snap[0].Name, snap[1].Name)
	}
	// Mutating the returned snapshot must not affect the store.
	snap[0].Mappings[0].State = StateActive
	snap[0].Mappings[0].Label = "mutated"
	again := s.Snapshot()
	if again[0].Mappings[0].State != StateStopped || again[0].Mappings[0].Label != "" {
		t.Fatalf("store was mutated through snapshot: %+v", again[0].Mappings[0])
	}
}
