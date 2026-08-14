# AGENTS.md — working in this repo

tnl is a daemon-based SSH tunnel manager: YAML config → one system `ssh` process per port mapping, supervised by a daemon with Unix-socket IPC. It is a from-scratch reimplementation of tunn's architecture with two deltas: supervision (backoff restarts, collision detection, crash reporting) and lifecycle control (per-tunnel start/stop/restart, `enabled: false`, macOS login integration).

## Read before changing behavior

- `CONTEXT.md` — domain language. Terms are load-bearing: **Tunnel**, **Mapping**, **Wanted**, **Connecting**, **Active**, **Backing off**, **Label**. Use them exactly; a "tunnel" is never a "connection", a "mapping" is never a "port forward".
- `docs/adr/0001-in-process-supervision.md` — the daemon supervises ssh itself; autossh is a deliberate non-goal.
- `docs/adr/0002-own-config-format.md` — the config format is own and deliberately NOT tunn-compatible. Do not "helpfully" re-add tunn compat.

## Non-negotiables

- **No new external dependencies.** The only module dependency is `gopkg.in/yaml.v3` (used by `internal/config`). Everything else is stdlib. Think hard before touching go.mod.
- **Spawn the system `ssh`, never a Go SSH library.** Keepalive flags on the spawn argv (`-o ServerAliveInterval=5 -o ServerAliveCountMax=2 -o ExitOnForwardFailure=yes`) are the connection-death detection; do not remove them.
- **Only Wanted mappings restart.** Wanted = enabled (or explicitly named) and not manually stopped. A stopped tunnel stays stopped; never auto-restart it.
- **Explicit names override `enabled: false`** — `tnl start pg_dev` and `tnl -d pg_dev` start a disabled tunnel; the flag only filters bare "all" operations.
- **Graceful kill contract**: children get SIGINT, 2s grace, then SIGKILL. `Manager.Run` must not return until every child is dead, and `runInternalDaemon` in `cmd/tnl/main.go` must wait for BOTH the manager and the IPC server before exiting — exiting early orphans ssh processes (regression-tested by smoke, not unit tests).
- **File hygiene**: runtime dir `0700`; pid/socket/log files `0600`. `daemon.Cleanup` removes pid + socket; socket removal happens there (after the supervisor has killed every child), not in the IPC server.
- **Platform**: unix-only (macOS/Linux). LaunchAgent code lives behind `//go:build darwin` in `internal/launchd`; non-darwin returns an error, never a silent no-op.

## Load-bearing interfaces (source of truth is the code, not this file)

- `internal/config` — `Config{Tunnels map[string]*Tunnel}`, `Tunnel{Name, Host, User, IdentityFile, Enabled *bool, Reclaim bool, Mappings []Mapping}`, `Mapping{Label, Local, RemoteHost, Remote}`. Strict YAML (`KnownFields(true)`); duplicate local ports across tunnels is a load error; `identity_file` gets env expansion. `ports` entries decode through `Mapping.UnmarshalYAML`: a plain `local:remote` scalar (unlabeled), a `local:desthost:remote` scalar (forward through the ssh host; `RemoteHost` empty = the ssh host itself), or a single-pair `label: <spec>` map — keep all three forms working. `RemoteHost` must flow into `status.MappingStatus` (three copy sites: `status.Store.EnsureTunnel`, supervisor initial states, supervisor `setMapping`) and into the `-L` argv (`spawnArgs`) and `output.labelOrSpec`.
- `internal/supervisor` — `Options{SSHBin, BackoffBase, BackoffCap, BackoffJitter, FailedAfter, DialInterval, DialTimeout, Log, Selected, PortProber, ReclaimPort}`; `Manager` with `Run(ctx)`, `StartTunnel`, `StopTunnel`, `RestartTunnel`, `Snapshot`; exported `backoffDelay(base, cap, jitter, attempt)`. PortProber is injectable — tests stub it; never hard-code real dials in tests. `ReclaimPort` defaults to `reclaimPortLsof` (lsof + same-uid guard, SIGINT→killGrace→SIGKILL) and is called at most once per spawn cycle from `runMapping`'s collision branch when the tunnel has `Reclaim: true`.
- `internal/status` — `MappingState`: `stopped | connecting | active | backing off | error`; `MappingStatus{Label, Local, Remote, State, Attempt, Message}`; `TunnelStatus{Name, Mappings}`; thread-safe `Store`.
- `internal/daemon` — IPC is JSON over a unix socket, one request/response per connection. `Request{Command, Tunnel}`, `Response{Running, Mode, PID, Message, Error, Tunnels}`. Commands: `status`, `start`, `stop`, `restart`, `shutdown`. The `Controller` interface decouples the server from the supervisor. Unknown tunnel → `Response.Error`, never a bare log line. The daemon is self-sufficient against lost runtime files: `SelfHealPID` rewrites a missing/wrong pid file (prevents duplicate-daemon launches) and the server's heal loop re-creates the socket (listeners are created via `listenUnix` with `SetUnlinkOnClose(false)` — Go's default close-unlink would delete a replacement socket during a heal swap).
- `internal/cli` — tunn-style parser: first positional verb, flags anywhere; conflict errors (verb + `--detach`, `stop` with 2+ names, etc.) are rejected in `Parse`, not in `main`.

## Testing

- Never require a real sshd or network. The fake ssh shim `internal/testutil/fakessh.sh` (executable) logs argv to `FAKE_SSH_LOG` and exits immediately when `FAKE_SSH_EXIT_IMMEDIATE=1`; point `Options.SSHBin` at it. Port probes are stub functions.
- Run `rtk go test ./...` after any change; use `-race` for `internal/supervisor` and `internal/daemon` (there were real teardown races; keep the shim's background output redirected so `cmd.Wait` doesn't hang).
- Spawn-argv tests assert the exact flag set (keepalives, `-L <local>:localhost:<remote>`, identity/user placement).
- Before modifying an exported symbol, run `lsp references` — the daemon and CLI cross-package contracts are used in several places.

## CLI surface

Keep the verbs stable: `tnl [names]` (foreground), `-d/--detach`, `status`, `start [names]`, `stop` (daemon) vs `stop <name>`, `restart <name>`, `install`, `uninstall`, `version`, `-c/--config`, `-h/--help`. `tnl start <name>` auto-daemonizes when no daemon is running. A bare `tnl` while the daemon runs is an error.

## Shutdown flow (the subtle part)

`tnl stop` → IPC `shutdown` → daemon cancels ctx → `Manager.Run` cancels every mapping loop and blocks on each loop's `done` (kill grace bounds this) → `runInternalDaemon`'s WaitGroup ensures both manager and server finished → `defer daemon.Cleanup`. The client polls for socket removal up to 5s. Dial errors that wrap `os.ErrNotExist` are mapped to "tnl daemon is not running" in `cmd/tnl/main.go`'s `sendCommand` — keep that mapping when touching command handlers.

## Git commits

- Use [Conventional Commits](https://www.conventionalcommits.org/) for every commit, with a type and concise imperative subject (for example, `fix(supervisor): prevent unwanted tunnel restarts`).
- Include a descriptive message body after a blank line. Explain the motivation, important implementation details, and user-visible or operational impact; do not leave the body empty for non-trivial changes.
