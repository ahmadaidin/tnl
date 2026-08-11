package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tnlrc")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

const fullConfig = `
tunnels:
  api:
    host: bastion.example.com
    user: deploy
    identity_file: ~/.ssh/id_ed25519
    ports:
      - primary: 3000:3000
      - 8080:80
  db:
    host: 10.0.0.5
    enabled: false
    ports:
      - 5432:5432
`

func TestLoadFullParse(t *testing.T) {
	cfg, err := Load(writeConfig(t, fullConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Tunnels) != 2 {
		t.Fatalf("got %d tunnels, want 2", len(cfg.Tunnels))
	}

	api := cfg.Tunnels["api"]
	if api == nil {
		t.Fatal("tunnel \"api\" missing")
	}
	if api.Name != "api" {
		t.Errorf("Name = %q, want %q", api.Name, "api")
	}
	if api.Host != "bastion.example.com" {
		t.Errorf("Host = %q", api.Host)
	}
	if api.User != "deploy" {
		t.Errorf("User = %q", api.User)
	}
	if api.IdentityFile != "~/.ssh/id_ed25519" {
		t.Errorf("IdentityFile = %q", api.IdentityFile)
	}
	if api.Enabled != nil {
		t.Errorf("Enabled = %v, want nil when key absent", *api.Enabled)
	}
	if len(api.Mappings) != 2 {
		t.Fatalf("got %d mappings, want 2", len(api.Mappings))
	}
	if api.Mappings[0].Label != "primary" {
		t.Errorf("Mappings[0].Label = %q, want %q", api.Mappings[0].Label, "primary")
	}
	if api.Mappings[0].Local != 3000 || api.Mappings[0].Remote != 3000 {
		t.Errorf("Mappings[0] = %d:%d, want 3000:3000", api.Mappings[0].Local, api.Mappings[0].Remote)
	}
	if api.Mappings[1].Local != 8080 || api.Mappings[1].Remote != 80 {
		t.Errorf("Mappings[1] = %d:%d, want 8080:80", api.Mappings[1].Local, api.Mappings[1].Remote)
	}
	if api.Mappings[1].Label != "" {
		t.Errorf("Mappings[1].Label = %q, want empty", api.Mappings[1].Label)
	}

	db := cfg.Tunnels["db"]
	if db == nil {
		t.Fatal("tunnel \"db\" missing")
	}
	if db.Name != "db" {
		t.Errorf("Name = %q, want %q", db.Name, "db")
	}
	if db.Enabled == nil || *db.Enabled {
		t.Errorf("db Enabled = %v, want explicit false", db.Enabled)
	}
}

func TestEnabledDefaultsTrue(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
tunnels:
  web:
    host: example.com
    ports:
      - 9000:9000
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Enabled("web") {
		t.Error("Enabled(web) = false, want true when enabled absent")
	}
	// Explicit false overrides the default.
	if cfg.Enabled("db") {
		t.Error("Enabled(db) = true, want false")
	}
	// Unknown names are not enabled.
	if cfg.Enabled("nope") {
		t.Error("Enabled(nope) = true, want false")
	}
}

func TestLoadDuplicateLocalPort(t *testing.T) {
	_, err := Load(writeConfig(t, `
tunnels:
  alpha:
    host: a.example.com
    ports:
      - 3000:3000
  beta:
    host: b.example.com
    ports:
      - 3000:4000
`))
	if err == nil {
		t.Fatal("expected duplicate port error, got nil")
	}
	want := `local port 3000 is claimed by both "alpha" and "beta"`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestLoadPortOutOfRange(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec string
		want string
	}{
		{"local zero", "0:3000", "local port 0 out of range"},
		{"local too high", "65536:3000", "local port 65536 out of range"},
		{"remote zero", "3000:0", "remote port 0 out of range"},
		{"remote negative", "3000:-1", "remote port -1 out of range"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, `
tunnels:
  t:
    host: example.com
    ports:
      - `+tc.spec+`
`))
			if err == nil {
				t.Fatal("expected out-of-range error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want containing %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLoadInvalidPortSpecs(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{"single part", "- 3000", `invalid port mapping "3000"`},
		{"empty dest host", "- 3000::5000", "empty destination host"},
		{"non-numeric local", "- abc:3000", `bad local port "abc"`},
		{"non-numeric remote", "- 3000:xyz", `bad remote port "xyz"`},
		{"old object form", "- local: 3000\n        remote: 3000", "port mapping must be"},
		{"multi-key mapping", "- primary: 3000:3000\n        extra: 4000:4001", "port mapping must be"},
		{"empty label", "- \"\": 3000:3000", "empty label"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, `
tunnels:
  t:
    host: example.com
    ports:
      `+tc.yaml+`
`))
			if err == nil {
				t.Fatal("expected port mapping error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want containing %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLoadReclaimFlag(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
tunnels:
  a:
    host: example.com
    reclaim: true
    ports:
      - 3000:3000
  b:
    host: example.com
    ports:
      - 3001:3001
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Tunnels["a"].Reclaim {
		t.Error("a.Reclaim = false, want true")
	}
	if cfg.Tunnels["b"].Reclaim {
		t.Error("b.Reclaim = true, want false (absent)")
	}
}

func TestLoadDestHost(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
tunnels:
  civitas:
    host: dev.civitas.id
    user: aidin
    ports:
      - 3329:db.suteki.tech:3306
      - db: 5432:db.suteki.tech:5432
      - 4000:4001
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ms := cfg.Tunnels["civitas"].Mappings
	if len(ms) != 3 {
		t.Fatalf("got %d mappings, want 3", len(ms))
	}
	if ms[0].Local != 3329 || ms[0].RemoteHost != "db.suteki.tech" || ms[0].Remote != 3306 {
		t.Errorf("Mappings[0] = %d:%s:%d, want 3329:db.suteki.tech:3306", ms[0].Local, ms[0].RemoteHost, ms[0].Remote)
	}
	if ms[1].Label != "db" || ms[1].Local != 5432 || ms[1].RemoteHost != "db.suteki.tech" || ms[1].Remote != 5432 {
		t.Errorf("Mappings[1] = %q %d:%s:%d, want labeled 5432:db.suteki.tech:5432", ms[1].Label, ms[1].Local, ms[1].RemoteHost, ms[1].Remote)
	}
	if ms[2].RemoteHost != "" {
		t.Errorf("Mappings[2].RemoteHost = %q, want empty (ssh host)", ms[2].RemoteHost)
	}
}

func TestLoadUnknownField(t *testing.T) {
	_, err := Load(writeConfig(t, `
tunnels:
  t:
    host: example.com
    proto: tcp
    ports:
      - 3000:3000
`))
	if err == nil {
		t.Fatal("expected strict field error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want yaml strict field rejection", err.Error())
	}
}

func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "config file not found: "+path {
		t.Errorf("error = %q, want %q", err.Error(), "config file not found: "+path)
	}
}

func TestLoadZeroTunnels(t *testing.T) {
	_, err := Load(writeConfig(t, "tunnels: {}\n"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "no tunnels defined" {
		t.Errorf("error = %q, want %q", err.Error(), "no tunnels defined")
	}
}

func TestLoadMissingHost(t *testing.T) {
	_, err := Load(writeConfig(t, `
tunnels:
  t:
    ports:
      - 3000:3000
`))
	if err == nil {
		t.Fatal("expected missing host error, got nil")
	}
	if !strings.Contains(err.Error(), `tunnel "t": missing host`) {
		t.Errorf("error = %q, want missing host", err.Error())
	}
}

func TestLoadZeroMappings(t *testing.T) {
	_, err := Load(writeConfig(t, `
tunnels:
  t:
    host: example.com
    ports: []
`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), `tunnel "t": no ports defined`) {
		t.Errorf("error = %q, want no ports defined", err.Error())
	}
}

func TestSortedNames(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
tunnels:
  zebra:
    host: z.example.com
    ports:
      - 3000:3000
  alpha:
    host: a.example.com
    ports:
      - 3001:3001
  mid:
    host: m.example.com
    ports:
      - 3002:3002
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.SortedNames()
	want := []string{"alpha", "mid", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("SortedNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SortedNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestIdentityFileEnvExpansion(t *testing.T) {
	t.Setenv("TNL_TEST_HOME", "/home/tester")
	cfg, err := Load(writeConfig(t, `
tunnels:
  t:
    host: example.com
    identity_file: $TNL_TEST_HOME/.ssh/id_ed25519
    ports:
      - 3000:3000
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := "/home/tester/.ssh/id_ed25519"
	if got := cfg.Tunnels["t"].IdentityFile; got != want {
		t.Errorf("IdentityFile = %q, want %q", got, want)
	}
}
