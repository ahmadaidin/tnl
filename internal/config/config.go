// Package config loads and validates the tnl tunnel configuration file.
//
// The configuration is a YAML document describing named tunnels, each with
// one or more port mappings. Loading is strict: unknown fields, out-of-range
// ports, missing hosts, and duplicate local ports across tunnels are all
// rejected at load time.
package config

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the parsed tunnel configuration. The key is the tunnel name as
// written in the YAML file.
type Config struct {
	Tunnels map[string]*Tunnel
}

// Tunnel is a named set of port mappings declared in the config; the unit of
// lifecycle control.
type Tunnel struct {
	// Name is populated from the YAML map key during decode.
	Name string

	// Host is the ssh destination host.
	Host string

	// User is the optional ssh login user (-l).
	User string

	// IdentityFile is the optional ssh identity file (-i); os.ExpandEnv is
	// applied to it during load.
	IdentityFile string

	// Enabled controls whether the tunnel is Wanted by default. nil means
	// enabled (the default).
	Enabled *bool

	// Reclaim, when true, makes the supervisor kill the process occupying a
	// colliding local port (same-user processes only) instead of reporting
	// "port in use" and waiting for it to free.
	Reclaim bool

	// Mappings are the local:remote port forwards supervised for this tunnel.
	Mappings []Mapping
}

// Mapping is a single port forward within a Tunnel.
type Mapping struct {
	// Label is an optional display name (the app listening on the port).
	// Informational only.
	Label string

	// Local is the local port that receives forwarded connections.
	Local int

	// RemoteHost is the destination host for the forward, resolved from the
	// ssh host's side. Empty means the ssh host itself (localhost).
	RemoteHost string

	// Remote is the destination port on RemoteHost.
	Remote int
}

// UnmarshalYAML decodes one ports entry, which is either a plain
// "local:remote" scalar (unlabeled) or a single-pair map
// "label: local:remote". Either spec may be "local:desthost:remote" to
// forward to a host reachable through the ssh host.
func (m *Mapping) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		return parsePortSpec(node.Value, &m.Local, &m.Remote, &m.RemoteHost)
	case yaml.MappingNode:
		if len(node.Content) != 2 {
			return fmt.Errorf("port mapping must be \"local:remote\" or \"label: local:remote\"")
		}
		key, value := node.Content[0], node.Content[1]
		if key.Kind != yaml.ScalarNode || value.Kind != yaml.ScalarNode {
			return fmt.Errorf("port mapping must be \"local:remote\" or \"label: local:remote\"")
		}
		if key.Value == "" {
			return fmt.Errorf("port mapping %q has an empty label", value.Value)
		}
		m.Label = key.Value
		return parsePortSpec(value.Value, &m.Local, &m.Remote, &m.RemoteHost)
	default:
		return fmt.Errorf("port mapping must be \"local:remote\" or \"label: local:remote\"")
	}
}

// parsePortSpec parses a port spec into two port numbers and an optional
// destination host. "local:remote" forwards to the ssh host itself;
// "local:desthost:remote" forwards through it. The middle parts are joined
// verbatim so bracketed IPv6 destinations survive.
func parsePortSpec(spec string, local, remote *int, destHost *string) error {
	parts := strings.Split(spec, ":")
	if len(parts) < 2 {
		return fmt.Errorf("invalid port mapping %q, want \"local:remote\"", spec)
	}
	l, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return fmt.Errorf("invalid port mapping %q: bad local port %q", spec, parts[0])
	}
	r, err := strconv.Atoi(strings.TrimSpace(parts[len(parts)-1]))
	if err != nil {
		return fmt.Errorf("invalid port mapping %q: bad remote port %q", spec, parts[len(parts)-1])
	}
	*local, *remote = l, r
	if len(parts) > 2 {
		host := strings.TrimSpace(strings.Join(parts[1:len(parts)-1], ":"))
		if host == "" {
			return fmt.Errorf("invalid port mapping %q: empty destination host", spec)
		}
		*destHost = host
	}
	return nil
}

// rawTunnel mirrors the YAML schema for a single tunnel. It exists so the
// decoder rejects unknown fields while decoding into the public types.
type rawTunnel struct {
	Host         string    `yaml:"host"`
	User         string    `yaml:"user"`
	IdentityFile string    `yaml:"identity_file"`
	Enabled      *bool     `yaml:"enabled"`
	Reclaim      bool      `yaml:"reclaim"`
	Mappings     []Mapping `yaml:"ports"`
}

type rawConfig struct {
	Tunnels map[string]rawTunnel `yaml:"tunnels"`
}

// Load reads, parses, and validates the configuration file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found: %s", path)
		}
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var raw rawConfig
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	if len(raw.Tunnels) == 0 {
		return nil, fmt.Errorf("no tunnels defined")
	}

	cfg := &Config{Tunnels: make(map[string]*Tunnel, len(raw.Tunnels))}

	// Track every claimed local port across all tunnels so duplicates are
	// caught regardless of tunnel ordering.
	claimed := make(map[int]string)

	names := make([]string, 0, len(raw.Tunnels))
	for name := range raw.Tunnels {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		rt := raw.Tunnels[name]
		t := &Tunnel{
			Name:         name,
			Host:         rt.Host,
			User:         rt.User,
			IdentityFile: os.ExpandEnv(rt.IdentityFile),
			Enabled:      rt.Enabled,
			Reclaim:      rt.Reclaim,
			Mappings:     rt.Mappings,
		}
		if t.Host == "" {
			return nil, fmt.Errorf("tunnel %q: missing host", name)
		}
		if len(rt.Mappings) == 0 {
			return nil, fmt.Errorf("tunnel %q: no ports defined", name)
		}
		for _, m := range rt.Mappings {
			if err := validatePort(m.Local); err != nil {
				return nil, fmt.Errorf("tunnel %q: local %w", name, err)
			}
			if err := validatePort(m.Remote); err != nil {
				return nil, fmt.Errorf("tunnel %q: remote %w", name, err)
			}
			if owner, ok := claimed[m.Local]; ok {
				return nil, fmt.Errorf("local port %d is claimed by both %q and %q", m.Local, owner, name)
			}
			claimed[m.Local] = name
		}
		cfg.Tunnels[name] = t
	}

	return cfg, nil
}

func validatePort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("port %d out of range (1-65535)", p)
	}
	return nil
}

// Enabled reports whether tunnel name is enabled. A tunnel with no explicit
// enabled setting is enabled by default.
func (c *Config) Enabled(name string) bool {
	t, ok := c.Tunnels[name]
	if !ok {
		return false
	}
	return t.Enabled == nil || *t.Enabled
}

// SortedNames returns the tunnel names in alphabetical order.
func (c *Config) SortedNames() []string {
	names := make([]string, 0, len(c.Tunnels))
	for name := range c.Tunnels {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
