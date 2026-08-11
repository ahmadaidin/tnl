package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmadaidin/tnl/internal/daemon"
)

func TestResolvePathsUsesXDGRuntimeDir(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)
	t.Setenv("HOME", filepath.Join(xdg, "fake-home"))

	p, err := daemon.ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	wantDir := filepath.Join(xdg, "tnl")
	if p.RuntimeDir != wantDir {
		t.Errorf("RuntimeDir = %q, want %q", p.RuntimeDir, wantDir)
	}
	if p.PIDFile != filepath.Join(wantDir, "daemon.pid") {
		t.Errorf("PIDFile = %q, want %q", p.PIDFile, filepath.Join(wantDir, "daemon.pid"))
	}
	if p.SocketFile != filepath.Join(wantDir, "daemon.sock") {
		t.Errorf("SocketFile = %q, want %q", p.SocketFile, filepath.Join(wantDir, "daemon.sock"))
	}
	if p.LogFile != filepath.Join(wantDir, "daemon.log") {
		t.Errorf("LogFile = %q, want %q", p.LogFile, filepath.Join(wantDir, "daemon.log"))
	}
	fi, err := os.Stat(wantDir)
	if err != nil {
		t.Fatalf("runtime dir not created: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Errorf("runtime dir mode = %o, want 700", perm)
	}
}

func TestResolvePathsFallsBackToHomeCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("HOME", home)

	p, err := daemon.ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	wantDir := filepath.Join(home, ".cache", "tnl")
	if p.RuntimeDir != wantDir {
		t.Errorf("RuntimeDir = %q, want %q", p.RuntimeDir, wantDir)
	}
	fi, err := os.Stat(wantDir)
	if err != nil {
		t.Fatalf("runtime dir not created: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Errorf("runtime dir mode = %o, want 700", perm)
	}
}

func TestResolvePathsIdempotent(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)

	p1, err := daemon.ResolvePaths()
	if err != nil {
		t.Fatalf("first ResolvePaths: %v", err)
	}
	p2, err := daemon.ResolvePaths()
	if err != nil {
		t.Fatalf("second ResolvePaths: %v", err)
	}
	if p1 != p2 {
		t.Errorf("paths differ across calls: %+v vs %+v", p1, p2)
	}
}
