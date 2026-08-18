// Package sshsetup provisions ssh identities for tunnels.
//
// Provisioning gives a Tunnel's ssh host an Identity: it generates a keypair
// with the system ssh-keygen, records the IdentityFile in ~/.ssh/config, and
// installs the public key into the remote account's authorized_keys with the
// system ssh. All external commands, paths, and prompts are injectable so
// tests never need a real sshd or network.
package sshsetup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ahmadaidin/tnl/internal/config"
)

// Provisioner performs provisioning for tunnels. The zero value is usable:
// every field resolves to a real default (system binaries, the user's home
// directory, interactive stdio). Tests override the fields directly.
type Provisioner struct {
	// SSHBin is the ssh binary used for detection and the authorized_keys
	// push. Default "ssh".
	SSHBin string
	// KeygenBin is the ssh-keygen binary. Default "ssh-keygen".
	KeygenBin string
	// HomeDir is the user's home directory; ~/.ssh paths resolve against it.
	// Default os.UserHomeDir().
	HomeDir string
	// EffectiveConfig returns the identity files and user ssh would use for
	// host. The production implementation runs `ssh -G <host>`.
	EffectiveConfig func(host string) (identities []string, user string, err error)
	// Run executes an external command with stdin/stdout/stderr wired to the
	// real terminal (interactive password/passphrase prompts). The production
	// implementation uses os/exec; tests inject a fake.
	Run func(ctx context.Context, argv ...string) error
	// Prompt reads one line of interactive input. The production
	// implementation reads os.Stdin; tests inject a scripted reader.
	Prompt func(prompt string) (string, error)
	// Out is the destination for progress output. Default os.Stdout.
	Out io.Writer
	// Err is the destination for warnings. Default os.Stderr.
	Err io.Writer
}

// Options selects the non-interactive behavior of Provision. Empty fields fall
// back to interactive prompts (or defaults under Yes).
type Options struct {
	// Algorithm is the key algorithm: ed25519, ecdsa, or rsa. Empty prompts.
	Algorithm string
	// Filename is the private key path, overriding the recommendation.
	// Empty uses the recommendation (or the tunnel's identity_file).
	Filename string
	// PassphraseFile names a file whose contents become the key passphrase.
	// Empty means no passphrase (prompted unless Yes).
	PassphraseFile string
	// Yes accepts all defaults without prompting: ed25519, the recommended
	// filename, and an empty passphrase.
	Yes bool
}

// Result reports the outcome of provisioning one host.
type Result struct {
	// Host is the ssh host that was provisioned.
	Host string
	// User is the effective remote account the key installs into.
	User string
	// Skipped is true when the host already had an Identity.
	Skipped bool
	// KeyPath is the private key written, or "" when Skipped.
	KeyPath string
	// Err is non-nil when provisioning failed. Key and config changes are
	// rolled back before Err is set.
	Err error
}

// Provision provisions the ssh host of tunnel t. It never starts the tunnel.
func (p *Provisioner) Provision(ctx context.Context, t *config.Tunnel, o Options) Result {
	sshBin := p.SSHBin
	if sshBin == "" {
		sshBin = "ssh"
	}
	keygenBin := p.KeygenBin
	if keygenBin == "" {
		keygenBin = "ssh-keygen"
	}
	home := p.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	effCfg := p.EffectiveConfig
	if effCfg == nil {
		effCfg = defaultEffectiveConfig(sshBin)
	}
	run := p.Run
	if run == nil {
		run = defaultRun
	}
	prompt := p.Prompt
	if prompt == nil {
		prompt = defaultPrompt
	}
	out := p.Out
	if out == nil {
		out = os.Stdout
	}
	errw := p.Err
	if errw == nil {
		errw = os.Stderr
	}

	// Detect: a host with an existing Identity is already Provisioned.
	identities, effUser, err := effCfg(t.Host)
	if err != nil {
		return Result{Host: t.Host, Err: err}
	}
	user := t.User
	if user == "" {
		user = effUser
	}
	for _, id := range identities {
		if fileExists(expandPath(id, home)) {
			return Result{Host: t.Host, User: user, Skipped: true}
		}
	}

	// Choose the algorithm.
	alg := o.Algorithm
	if alg == "" {
		if o.Yes {
			alg = "ed25519"
		} else {
			ans, err := prompt("Key algorithm [ed25519]: ")
			if err != nil {
				return Result{Host: t.Host, User: user, Err: err}
			}
			alg = strings.TrimSpace(ans)
			if alg == "" {
				alg = "ed25519"
			}
		}
	}
	alg = strings.ToLower(alg)
	switch alg {
	case "ed25519", "ecdsa", "rsa":
	default:
		return Result{Host: t.Host, User: user,
			Err: fmt.Errorf("unknown algorithm %q (want ed25519, ecdsa, or rsa)", o.Algorithm)}
	}

	// Choose the key path. A configured identity_file stays authoritative;
	// otherwise the recommendation is ~/.ssh/id_<alg>_<host>.
	var keyPath string
	switch {
	case t.IdentityFile != "":
		keyPath = expandPath(t.IdentityFile, home)
	case o.Filename != "":
		keyPath = expandPath(o.Filename, home)
	default:
		rec := filepath.Join(home, ".ssh", fmt.Sprintf("id_%s_%s", alg, sanitizeHost(t.Host)))
		if o.Yes {
			keyPath = rec
		} else {
			ans, err := prompt(fmt.Sprintf("Key file [%s]: ", rec))
			if err != nil {
				return Result{Host: t.Host, User: user, Err: err}
			}
			ans = strings.TrimSpace(ans)
			if ans == "" {
				ans = rec
			}
			keyPath = expandPath(ans, home)
		}
	}
	keyPath = freePath(keyPath)

	// Choose the passphrase.
	var passphrase string
	switch {
	case o.PassphraseFile != "":
		data, err := os.ReadFile(expandPath(o.PassphraseFile, home))
		if err != nil {
			return Result{Host: t.Host, User: user, Err: fmt.Errorf("read passphrase file: %w", err)}
		}
		passphrase = strings.TrimRight(string(data), "\r\n")
	case o.Yes:
		passphrase = ""
	default:
		ans, err := prompt("Key passphrase (empty for none): ")
		if err != nil {
			return Result{Host: t.Host, User: user, Err: err}
		}
		passphrase = ans
	}
	if passphrase != "" {
		fmt.Fprintf(errw, "warning: passphrase-protected key; the daemon needs ssh-agent to use it\n")
	}

	// Generate the keypair.
	dir := filepath.Dir(keyPath)
	if err := ensureDir(dir); err != nil {
		return Result{Host: t.Host, User: user, Err: err}
	}
	keygenArgs := []string{"-t", alg}
	switch alg {
	case "ecdsa":
		keygenArgs = append(keygenArgs, "-b", "521")
	case "rsa":
		keygenArgs = append(keygenArgs, "-b", "4096")
	}
	keygenArgs = append(keygenArgs, "-N", passphrase, "-f", keyPath, "-C", "")
	if err := run(ctx, append([]string{keygenBin}, keygenArgs...)...); err != nil {
		return Result{Host: t.Host, User: user, Err: fmt.Errorf("generate key: %w", err)}
	}
	// ssh-keygen writes the private key 0600; pin the pubkey to 0644.
	if err := os.Chmod(keyPath+".pub", 0o644); err != nil {
		_ = os.Remove(keyPath)
		_ = os.Remove(keyPath + ".pub")
		return Result{Host: t.Host, User: user, Err: fmt.Errorf("set pubkey permissions: %w", err)}
	}
	fmt.Fprintf(out, "generated key %s\n", keyPath)

	// Record the IdentityFile in ~/.ssh/config.
	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		_ = os.Remove(keyPath)
		_ = os.Remove(keyPath + ".pub")
		return Result{Host: t.Host, User: user, Err: fmt.Errorf("read public key: %w", err)}
	}
	key := strings.TrimSpace(string(pub))
	configPath := filepath.Join(home, ".ssh", "config")
	prior, err := insertIdentityFile(configPath, t.Host, keyPath)
	if err != nil {
		_ = os.Remove(keyPath)
		_ = os.Remove(keyPath + ".pub")
		return Result{Host: t.Host, User: user, Err: fmt.Errorf("update ssh config: %w", err)}
	}
	fmt.Fprintf(out, "recorded IdentityFile for %s in %s\n", t.Host, configPath)

	// Install the public key into the remote account's authorized_keys.
	// accept-new records the host fingerprint (never StrictHostKeyChecking=no);
	// no BatchMode, so password auth stays interactive. The remote command is
	// idempotent: whole-line grep before append.
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	remoteCmd := fmt.Sprintf(
		"mkdir -p ~/.ssh && chmod 700 ~/.ssh && grep -qFx %s ~/.ssh/authorized_keys 2>/dev/null || echo %s >> ~/.ssh/authorized_keys; chmod 600 ~/.ssh/authorized_keys",
		quote(key), quote(key))
	pushArgs := []string{"-o", "StrictHostKeyChecking=accept-new"}
	if user != "" {
		pushArgs = append(pushArgs, "-l", user)
	}
	pushArgs = append(pushArgs, t.Host, remoteCmd)
	if err := run(ctx, append([]string{sshBin}, pushArgs...)...); err != nil {
		// All-or-nothing: a host is Provisioned only when identity AND push
		// both succeed, so roll back the key and the config insertion.
		_ = os.Remove(keyPath)
		_ = os.Remove(keyPath + ".pub")
		if prior == nil {
			_ = os.Remove(configPath)
		} else {
			_ = os.WriteFile(configPath, prior, 0o600)
		}
		return Result{Host: t.Host, User: user, Err: fmt.Errorf("install authorized key on %s: %w", t.Host, err)}
	}

	return Result{Host: t.Host, User: user, KeyPath: keyPath}
}

// defaultEffectiveConfig resolves the effective identity files and user for
// host via `ssh -G <host>`.
func defaultEffectiveConfig(sshBin string) func(host string) ([]string, string, error) {
	return func(host string) ([]string, string, error) {
		out, err := exec.Command(sshBin, "-G", host).Output()
		if err != nil {
			return nil, "", fmt.Errorf("resolve ssh config for %s: %w", host, err)
		}
		identities, user := parseGEffective(string(out))
		return identities, user, nil
	}
}

// defaultRun executes a command with the process's stdio wired through, so
// interactive password/passphrase prompts reach the terminal.
func defaultRun(ctx context.Context, argv ...string) error {
	if len(argv) == 0 {
		return nil
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// defaultPrompt prints prompt to stderr and reads one line from stdin.
func defaultPrompt(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// parseGEffective extracts identity files and the user from `ssh -G` output.
// ssh -G always lists the default key names even when missing, so the caller
// checks existence on disk.
func parseGEffective(output string) (identities []string, user string) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "identityfile":
			identities = append(identities, fields[1])
		case "user":
			user = fields[1]
		}
	}
	return identities, user
}

// insertIdentityFile inserts a Host/IdentityFile block for host into the ssh
// config at configPath, before the first existing block whose host patterns
// match host (first-match-wins), or appends at the end if none matches. It
// returns the file's prior content, or nil if the file did not exist (a
// rollback must then remove the file). All pre-existing bytes are preserved.
func insertIdentityFile(configPath, host, keyPath string) (prior []byte, err error) {
	block := fmt.Sprintf("Host %s\n    IdentityFile %s\n", host, keyPath)
	prior, err = os.ReadFile(configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read ssh config: %w", err)
		}
		if err := ensureDir(filepath.Dir(configPath)); err != nil {
			return nil, fmt.Errorf("create ssh config dir: %w", err)
		}
		if err := os.WriteFile(configPath, []byte(block), 0o600); err != nil {
			return nil, fmt.Errorf("create ssh config: %w", err)
		}
		return nil, nil
	}
	content := string(prior)
	offset := -1
	for _, b := range splitBlocks(content) {
		if b.matches(host) {
			offset = b.startByte
			break
		}
	}
	var out string
	if offset < 0 {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		out = content + block
	} else {
		out = content[:offset] + block + content[offset:]
	}
	if err := os.WriteFile(configPath, []byte(out), 0o600); err != nil {
		_ = os.WriteFile(configPath, prior, 0o600) // best-effort restore
		return nil, fmt.Errorf("write ssh config: %w", err)
	}
	if err := os.Chmod(configPath, 0o600); err != nil {
		return nil, fmt.Errorf("set ssh config permissions: %w", err)
	}
	return prior, nil
}

// sshConfigBlock is one top-level block of an ssh config: either a Host or
// Match block, or the leading "global" block before the first Host/Match
// (keyword == "").
type sshConfigBlock struct {
	startByte int
	keyword   string // "host", "match", or ""
	patterns  []string
}

// matches reports whether the block's patterns match host. Match blocks are
// treated as matching conservatively so the insertion lands before them
// (first-match-wins safety).
func (b sshConfigBlock) matches(host string) bool {
	if b.keyword == "match" {
		return true
	}
	for _, pat := range b.patterns {
		if matchPattern(pat, host) {
			return true
		}
	}
	return false
}

// splitBlocks parses content into top-level blocks delimited by Host/Match
// lines. Every other line (including directives inside a block) belongs to
// the current block.
func splitBlocks(content string) []sshConfigBlock {
	var blocks []sshConfigBlock
	offset := 0
	for _, line := range strings.SplitAfter(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			kw := strings.ToLower(fields[0])
			if kw == "host" || kw == "match" {
				blocks = append(blocks, sshConfigBlock{
					startByte: offset,
					keyword:   kw,
					patterns:  splitPatterns(strings.Join(fields[1:], " ")),
				})
			}
		}
		offset += len(line)
	}
	return blocks
}

// splitPatterns splits a Host pattern list on whitespace and commas.
func splitPatterns(rest string) []string {
	var pats []string
	for _, f := range strings.Fields(rest) {
		for _, p := range strings.Split(f, ",") {
			if p != "" {
				pats = append(pats, p)
			}
		}
	}
	return pats
}

// matchPattern reports whether an ssh Host pattern matches host, case-
// insensitively. Patterns support * and ? wildcards and [...] character
// classes ([!...] / [^...] negated, a-z ranges); an unterminated [ is
// treated literally, mirroring ssh's own leniency.
func matchPattern(pattern, host string) bool {
	return matchGlob(strings.ToLower(pattern), strings.ToLower(host))
}

func matchGlob(p, s string) bool {
	for len(p) > 0 {
		switch p[0] {
		case '*':
			for len(p) > 0 && p[0] == '*' {
				p = p[1:]
			}
			if len(p) == 0 {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if matchGlob(p, s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			p, s = p[1:], s[1:]
		case '[':
			// A ']' immediately after '[' (or after the negation marker) is a
			// literal member, so the first closing bracket is searched past it.
			start := 1
			if len(p) > 1 && (p[1] == '!' || p[1] == '^') {
				start = 2
			}
			j := -1
			for k := start; k < len(p); k++ {
				if p[k] == ']' && k != 1 {
					j = k
					break
				}
			}
			if j < 0 {
				// Unterminated class: literal '['.
				if len(s) == 0 || s[0] != '[' {
					return false
				}
				p, s = p[1:], s[1:]
				continue
			}
			class := p[1:j]
			p = p[j+1:]
			if len(s) == 0 || !classMatch(class, s[0]) {
				return false
			}
			s = s[1:]
		default:
			if len(s) == 0 || s[0] != p[0] {
				return false
			}
			p, s = p[1:], s[1:]
		}
	}
	return len(s) == 0
}

// classMatch reports whether byte c is in an ssh character class. A leading
// ! or ^ negates the class.
func classMatch(class string, c byte) bool {
	negate := false
	if strings.HasPrefix(class, "!") || strings.HasPrefix(class, "^") {
		negate = true
		class = class[1:]
	}
	matched := false
	for i := 0; i < len(class); i++ {
		if i+2 < len(class) && class[i+1] == '-' {
			lo, hi := class[i], class[i+2]
			if lo > hi {
				lo, hi = hi, lo
			}
			if lo <= c && c <= hi {
				matched = true
			}
			i += 2
		} else if class[i] == c {
			matched = true
		}
	}
	return matched != negate
}

// expandPath expands environment variables, a leading "~", and ssh's %d home
// token in p against home.
func expandPath(p, home string) string {
	p = os.ExpandEnv(p)
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	if strings.HasPrefix(p, "%d/") || p == "%d" {
		return filepath.Join(home, strings.TrimPrefix(p, "%d"))
	}
	return p
}

// sanitizeHost renders host as a filename component, replacing anything
// outside [A-Za-z0-9._-] with '_'.
func sanitizeHost(host string) string {
	var b strings.Builder
	for _, r := range host {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// freePath returns path, or path-2/-3/... if path exists, so a generated key
// never clobbers an existing file.
func freePath(path string) string {
	if !fileExists(path) {
		return path
	}
	dir, base := filepath.Dir(path), filepath.Base(path)
	for i := 2; ; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s-%d", base, i))
		if !fileExists(cand) {
			return cand
		}
	}
}

// ensureDir creates dir with 0700 permissions only when it does not exist;
// an existing directory's permissions are never touched.
func ensureDir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return os.Chmod(dir, 0o700)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
