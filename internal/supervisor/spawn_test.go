package supervisor

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ahmadaidin/tnl/internal/config"
)

func TestSpawnArgsDestHost(t *testing.T) {
	got := spawnArgs(
		&config.Tunnel{Host: "dev.civitas.id", User: "aidin"},
		config.Mapping{Local: 3329, RemoteHost: "db.suteki.tech", Remote: 3306},
	)
	want := strings.Fields("-N -L 3329:db.suteki.tech:3306 -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -o ExitOnForwardFailure=yes -l aidin dev.civitas.id")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("spawnArgs = %q, want %q", got, want)
	}
}

func TestSpawnArgsNoDestHost(t *testing.T) {
	got := spawnArgs(
		&config.Tunnel{Host: "example.com"},
		config.Mapping{Local: 3000, Remote: 3000},
	)
	want := strings.Fields("-N -L 3000:localhost:3000 -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -o ExitOnForwardFailure=yes example.com")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("spawnArgs = %q, want %q", got, want)
	}
}
