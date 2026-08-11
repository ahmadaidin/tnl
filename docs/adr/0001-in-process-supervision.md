# In-process supervision instead of autossh

tnl supervises SSH tunnels: it restarts dead port mappings with exponential backoff and exposes per-tunnel lifecycle control. The classic tool for tunnel supervision is autossh, and we considered wrapping it. We decided against it — the tnl daemon spawns the system `ssh` binary directly and owns the supervision state machine itself.

Why: lifecycle control (per-tunnel `start`/`stop`/`restart`) requires the daemon to own the state and commands, and autossh's supervision is embedded in its child process — not queryable, not controllable, no per-mapping status. The state machine and its reporting (`connecting`/`active`/`backing off (attempt N)`) are the product, not a side effect. autossh's one technical advantage — detecting dead-but-alive TCP connections — is covered by OpenSSH's own keepalives: spawning with `-o ServerAliveInterval=30 -o ServerAliveCountMax=3` makes ssh exit when the connection dies, converting connection death into a process exit the supervisor already handles.

Consequences: we reimplement battle-tested retry logic — mitigated by keeping it small and testing it against a fake `ssh` shim rather than real connections. We depend on OpenSSH keepalive options, which are standard on every supported platform.

Status: accepted.
