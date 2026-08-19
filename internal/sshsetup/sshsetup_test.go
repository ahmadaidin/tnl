package sshsetup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ahmadaidin/tnl/internal/config"
)

// fakeSSH is a scriptable Run: it records argv, writes dummy key material for
// ssh-keygen invocations, and returns an injectable error for ssh pushes.
type fakeSSH struct {
	calls   [][]string
	pushErr error
}

func (f *fakeSSH) run(ctx context.Context, argv ...string) error {
	f.calls = append(f.calls, append([]string(nil), argv...))
	if len(argv) == 0 {
		return nil
	}
	if filepath.Base(argv[0]) == "ssh-keygen" {
		path := ""
		for i, a := range argv {
			if a == "-f" && i+1 < len(argv) {
				path = argv[i+1]
			}
		}
		if path != "" {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte("PRIVATE KEY\n"), 0o600); err != nil {
				return err
			}
			if err := os.WriteFile(path+".pub", []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI fake@test\n"), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	return f.pushErr
}

// newProvisioner builds a Provisioner rooted at a temp home with a fake Run
// that fails pushes when pushErr is non-nil. Prompt panics on unexpected use.
func newProvisioner(t *testing.T, pushErr error) (*Provisioner, *fakeSSH) {
	t.Helper()
	home := t.TempDir()
	f := &fakeSSH{pushErr: pushErr}
	return &Provisioner{
		SSHBin:    "ssh",
		KeygenBin: "ssh-keygen",
		HomeDir:   home,
		EffectiveConfig: func(host string) ([]string, string, error) {
			return []string{filepath.Join(home, ".ssh", "id_ed25519")}, "aidin", nil
		},
		Run:    f.run,
		Prompt: func(string) (string, error) { t.Fatal("unexpected prompt"); return "", nil },
		Out:    io.Discard,
		Err:    io.Discard,
	}, f
}

func tunnel(host string) *config.Tunnel {
	return &config.Tunnel{Name: "t", Host: host}
}

func TestProvisionSkipsProvisionedHost(t *testing.T) {
	tests := []struct {
		name string
		set  func(home string)
	}{
		{
			name: "explicit identity file exists",
			set: func(home string) {
				if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), []byte("k"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "default named key exists",
			set: func(home string) {
				if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), []byte("k"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, f := newProvisioner(t, nil)
			tt.set(p.HomeDir)
			r := p.Provision(context.Background(), tunnel("myserver"), Options{Yes: true})
			if r.Err != nil {
				t.Fatalf("Provision error: %v", r.Err)
			}
			if !r.Skipped {
				t.Fatalf("Provision = %+v, want Skipped", r)
			}
			if len(f.calls) != 0 {
				t.Fatalf("Run called %d times for a provisioned host: %v", len(f.calls), f.calls)
			}
		})
	}
}

func TestProvisionHappyPath(t *testing.T) {
	p, f := newProvisioner(t, nil)
	r := p.Provision(context.Background(), tunnel("myserver"), Options{Yes: true})
	if r.Err != nil {
		t.Fatalf("Provision error: %v", r.Err)
	}
	if r.Skipped {
		t.Fatalf("Provision unexpectedly skipped")
	}
	wantKey := filepath.Join(p.HomeDir, ".ssh", "id_ed25519_myserver")
	if r.KeyPath != wantKey {
		t.Errorf("KeyPath = %q, want %q", r.KeyPath, wantKey)
	}
	if r.User != "aidin" {
		t.Errorf("User = %q, want aidin", r.User)
	}

	// argv[0] = keygen.
	if len(f.calls) < 2 {
		t.Fatalf("got %d calls, want keygen + push", len(f.calls))
	}
	wantKeygen := []string{"ssh-keygen", "-t", "ed25519", "-N", "", "-f", wantKey, "-C", ""}
	if !reflect.DeepEqual(f.calls[0], wantKeygen) {
		t.Errorf("keygen argv = %v, want %v", f.calls[0], wantKeygen)
	}

	// argv[1] = push. Check flags and that the remote command carries the key.
	push := f.calls[1]
	if push[0] != "ssh" || push[1] != "-o" || push[2] != "StrictHostKeyChecking=accept-new" {
		t.Errorf("push argv = %v, want accept-new first", push)
	}
	if push[3] != "-l" || push[4] != "aidin" {
		t.Errorf("push argv = %v, want -l aidin", push)
	}
	if push[5] != "myserver" {
		t.Errorf("push argv = %v, want host myserver", push)
	}
	remote := push[6]
	if !strings.Contains(remote, "grep -qFx") || !strings.Contains(remote, ">> ~/.ssh/authorized_keys") {
		t.Errorf("remote command %q lacks idempotent install", remote)
	}
	pub, _ := os.ReadFile(wantKey + ".pub")
	if !strings.Contains(remote, strings.TrimSpace(string(pub))) {
		t.Errorf("remote command %q lacks the public key", remote)
	}

	// Config file created with the block, 0600.
	cfgPath := filepath.Join(p.HomeDir, ".ssh", "config")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	wantCfg := "Host myserver\n    IdentityFile " + wantKey + "\n"
	if string(data) != wantCfg {
		t.Errorf("config = %q, want %q", data, wantCfg)
	}
	info, _ := os.Stat(cfgPath)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %o, want 600", info.Mode().Perm())
	}
	pubInfo, _ := os.Stat(wantKey + ".pub")
	if pubInfo.Mode().Perm() != 0o644 {
		t.Errorf("pubkey mode = %o, want 644", pubInfo.Mode().Perm())
	}
}

func TestProvisionKeygenBits(t *testing.T) {
	for _, tt := range []struct {
		alg  string
		want []string
	}{
		{"ecdsa", []string{"ssh-keygen", "-t", "ecdsa", "-b", "521", "-N", "", "-f"}},
		{"rsa", []string{"ssh-keygen", "-t", "rsa", "-b", "4096", "-N", "", "-f"}},
	} {
		t.Run(tt.alg, func(t *testing.T) {
			p, f := newProvisioner(t, nil)
			r := p.Provision(context.Background(), tunnel("myserver"), Options{Algorithm: tt.alg, Yes: true})
			if r.Err != nil {
				t.Fatalf("Provision error: %v", r.Err)
			}
			got := f.calls[0][:8]
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("keygen argv = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProvisionDanglingIdentityFile(t *testing.T) {
	p, f := newProvisioner(t, nil)
	tr := tunnel("myserver")
	tr.IdentityFile = "~/keys/legacy"
	r := p.Provision(context.Background(), tr, Options{Yes: true})
	if r.Err != nil {
		t.Fatalf("Provision error: %v", r.Err)
	}
	want := filepath.Join(p.HomeDir, "keys", "legacy")
	if r.KeyPath != want {
		t.Errorf("KeyPath = %q, want %q", r.KeyPath, want)
	}
	if len(f.calls) == 0 || !strings.Contains(strings.Join(f.calls[0], " "), "-f "+want) {
		t.Errorf("keygen argv = %v, want -f %s", f.calls[0], want)
	}
}

func TestProvisionFilenameAutoSuffix(t *testing.T) {
	p, f := newProvisioner(t, nil)
	rec := filepath.Join(p.HomeDir, ".ssh", "id_ed25519_myserver")
	if err := os.MkdirAll(filepath.Dir(rec), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rec, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rec+"-2", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := p.Provision(context.Background(), tunnel("myserver"), Options{Yes: true})
	if r.Err != nil {
		t.Fatalf("Provision error: %v", r.Err)
	}
	want := rec + "-3"
	if r.KeyPath != want {
		t.Errorf("KeyPath = %q, want %q", r.KeyPath, want)
	}
	if !strings.Contains(strings.Join(f.calls[0], " "), "-f "+want) {
		t.Errorf("keygen argv = %v, want -f %s", f.calls[0], want)
	}
}

func TestProvisionUserOverride(t *testing.T) {
	p, f := newProvisioner(t, nil)
	tr := tunnel("myserver")
	tr.User = "root"
	r := p.Provision(context.Background(), tr, Options{Yes: true})
	if r.Err != nil {
		t.Fatalf("Provision error: %v", r.Err)
	}
	if r.User != "root" {
		t.Errorf("User = %q, want root", r.User)
	}
	push := f.calls[1]
	if push[3] != "-l" || push[4] != "root" {
		t.Errorf("push argv = %v, want -l root", push)
	}
}

func TestProvisionPassphraseFile(t *testing.T) {
	p, f := newProvisioner(t, nil)
	var warn bytes.Buffer
	p.Err = &warn
	pf := filepath.Join(p.HomeDir, "secret.txt")
	if err := os.WriteFile(pf, []byte("hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := p.Provision(context.Background(), tunnel("myserver"), Options{PassphraseFile: pf, Yes: true})
	if r.Err != nil {
		t.Fatalf("Provision error: %v", r.Err)
	}
	keygen := strings.Join(f.calls[0], " ")
	if !strings.Contains(keygen, "-N hunter2") {
		t.Errorf("keygen argv = %v, want -N hunter2", f.calls[0])
	}
	if !strings.Contains(warn.String(), "ssh-agent") {
		t.Errorf("warning = %q, want ssh-agent mention", warn.String())
	}
}

func TestProvisionRollback(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, ".ssh", "config")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	prior := "Host other\n    HostName elsewhere\n"
	if err := os.WriteFile(cfgPath, []byte(prior), 0o600); err != nil {
		t.Fatal(err)
	}

	f := &fakeSSH{pushErr: errors.New("connection refused")}
	p := &Provisioner{
		SSHBin:    "ssh",
		KeygenBin: "ssh-keygen",
		HomeDir:   home,
		EffectiveConfig: func(host string) ([]string, string, error) {
			return []string{filepath.Join(home, ".ssh", "id_ed25519")}, "aidin", nil
		},
		Run:    f.run,
		Prompt: func(string) (string, error) { return "", nil },
		Out:    io.Discard,
		Err:    io.Discard,
	}
	r := p.Provision(context.Background(), tunnel("myserver"), Options{Yes: true})
	if r.Err == nil {
		t.Fatalf("Provision succeeded, want push failure")
	}
	key := filepath.Join(home, ".ssh", "id_ed25519_myserver")
	if _, err := os.Stat(key); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("private key still exists after rollback")
	}
	if _, err := os.Stat(key + ".pub"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("pubkey still exists after rollback")
	}
	after, _ := os.ReadFile(cfgPath)
	if string(after) != prior {
		t.Errorf("config = %q, want restored %q", after, prior)
	}

	t.Run("no prior config", func(t *testing.T) {
		f := &fakeSSH{pushErr: errors.New("boom")}
		p := &Provisioner{
			SSHBin:    "ssh",
			KeygenBin: "ssh-keygen",
			HomeDir:   t.TempDir(),
			EffectiveConfig: func(host string) ([]string, string, error) {
				return []string{filepath.Join(p.HomeDir, ".ssh", "id_ed25519")}, "", nil
			},
			Run:    f.run,
			Prompt: func(string) (string, error) { return "", nil },
			Out:    io.Discard,
			Err:    io.Discard,
		}
		r := p.Provision(context.Background(), tunnel("h"), Options{Yes: true})
		if r.Err == nil {
			t.Fatalf("Provision succeeded, want push failure")
		}
		if _, err := os.Stat(filepath.Join(p.HomeDir, ".ssh", "config")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("config file still exists after rollback")
		}
	})
}

func TestInsertIdentityFile(t *testing.T) {
	cfgPath := func(t *testing.T, home, content string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(home, ".ssh", "config")
		if content != "" {
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return path
	}

	tests := []struct {
		name    string
		content string
		host    string
		want    string
	}{
		{
			name:    "insert before Host *",
			content: "Host *\n    ServerAliveInterval 5\n",
			host:    "myserver",
			want:    "Host myserver\n    IdentityFile /keys/myserver\nHost *\n    ServerAliveInterval 5\n",
		},
		{
			name:    "insert before exact host case-insensitive",
			content: "Host MyServer\n    User aidin\n",
			host:    "myserver",
			want:    "Host myserver\n    IdentityFile /keys/myserver\nHost MyServer\n    User aidin\n",
		},
		{
			name:    "insert before multi-pattern",
			content: "Host a b,c*\n    User aidin\n",
			host:    "cbastion",
			want:    "Host cbastion\n    IdentityFile /keys/cbastion\nHost a b,c*\n    User aidin\n",
		},
		{
			name:    "insert before Match block",
			content: "Match host foo\n    User aidin\n",
			host:    "myserver",
			want:    "Host myserver\n    IdentityFile /keys/myserver\nMatch host foo\n    User aidin\n",
		},
		{
			name:    "append when nothing matches",
			content: "Host other\n    User aidin\n",
			host:    "myserver",
			want:    "Host other\n    User aidin\nHost myserver\n    IdentityFile /keys/myserver\n",
		},
		{
			name:    "append to file without trailing newline",
			content: "Host other\n    User aidin",
			host:    "myserver",
			want:    "Host other\n    User aidin\nHost myserver\n    IdentityFile /keys/myserver\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			path := cfgPath(t, home, tt.content)
			prior, err := insertIdentityFile(path, tt.host, "/keys/"+tt.host)
			if err != nil {
				t.Fatalf("insertIdentityFile error: %v", err)
			}
			if string(prior) != tt.content {
				t.Errorf("prior = %q, want %q", prior, tt.content)
			}
			data, _ := os.ReadFile(path)
			if string(data) != tt.want {
				t.Errorf("config = %q, want %q", data, tt.want)
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0o600 {
				t.Errorf("mode = %o, want 600", info.Mode().Perm())
			}
		})
	}

	t.Run("create when missing", func(t *testing.T) {
		home := t.TempDir()
		path := cfgPath(t, home, "")
		prior, err := insertIdentityFile(path, "myserver", "/keys/myserver")
		if err != nil {
			t.Fatalf("insertIdentityFile error: %v", err)
		}
		if prior != nil {
			t.Errorf("prior = %q, want nil (file did not exist)", prior)
		}
		data, _ := os.ReadFile(path)
		if string(data) != "Host myserver\n    IdentityFile /keys/myserver\n" {
			t.Errorf("config = %q", data)
		}
	})
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern string
		host    string
		want    bool
	}{
		{"*", "anything", true},
		{"myserver", "myserver", true},
		{"MyServer", "myserver", true}, // case-insensitive
		{"myserver", "other", false},
		{"web*", "web01", true},
		{"web*", "db01", false},
		{"web?", "web1", true},
		{"web?", "web10", false},
		{"[a-z]*", "mydb", true},
		{"[a-z]*", "9db", false},
		{"[!x]*", "abc", true},
		{"[!x]*", "xyz", false},
		{"host[", "host[", true}, // unterminated class: literal
		{"host[", "hostx", false},
		{"a*b*c", "aXbYc", true},
		{"", "", true},
	}
	for _, tt := range tests {
		if got := matchPattern(tt.pattern, tt.host); got != tt.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.pattern, tt.host, got, tt.want)
		}
	}
}

func TestParseGEffective(t *testing.T) {
	out := "host myserver\nidentityfile /home/u/.ssh/id_ed25519\nuser aidin\nidentityfile /home/u/.ssh/id_rsa\nignoreme junk\n"
	ids, user := parseGEffective(out)
	want := []string{"/home/u/.ssh/id_ed25519", "/home/u/.ssh/id_rsa"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("identities = %v, want %v", ids, want)
	}
	if user != "aidin" {
		t.Errorf("user = %q, want aidin", user)
	}
}

func TestProvisionUnknownAlgorithm(t *testing.T) {
	p, _ := newProvisioner(t, nil)
	r := p.Provision(context.Background(), tunnel("myserver"), Options{Algorithm: "dsa", Yes: true})
	if r.Err == nil || !strings.Contains(r.Err.Error(), "unknown algorithm") {
		t.Fatalf("Err = %v, want unknown algorithm error", r.Err)
	}
}

func TestProvisionInteractivePrompts(t *testing.T) {
	home := t.TempDir()
	f := &fakeSSH{}
	p := &Provisioner{
		SSHBin:    "ssh",
		KeygenBin: "ssh-keygen",
		HomeDir:   home,
		EffectiveConfig: func(host string) ([]string, string, error) {
			return []string{filepath.Join(home, ".ssh", "id_ed25519")}, "aidin", nil
		},
		Run: f.run,
		Prompt: func(prompt string) (string, error) {
			switch {
			case strings.HasPrefix(prompt, "Key algorithm"):
				return "ecdsa", nil
			case strings.HasPrefix(prompt, "Key file"):
				return "", nil // accept recommendation
			case strings.HasPrefix(prompt, "Key passphrase"):
				return "hunter2", nil
			}
			t.Fatalf("unexpected prompt %q", prompt)
			return "", nil
		},
		Out: io.Discard,
		Err: io.Discard,
	}
	r := p.Provision(context.Background(), tunnel("myserver"), Options{})
	if r.Err != nil {
		t.Fatalf("Provision error: %v", r.Err)
	}
	keygen := strings.Join(f.calls[0], " ")
	for _, want := range []string{"-t ecdsa", "-b 521", "-N hunter2"} {
		if !strings.Contains(keygen, want) {
			t.Errorf("keygen argv = %q, want %s", keygen, want)
		}
	}
}
