# tnl — SSH Tunnel Manager

A daemon-based SSH tunnel manager. Declares tunnels in a YAML config, spawns the system `ssh` binary for each port mapping, and supervises them: restarting dead mappings with exponential backoff and exposing per-tunnel lifecycle control.

## Language

**Tunnel**:
A named set of port mappings declared in the config; the unit of lifecycle control (`tnl start|stop|restart <name>`).
_Avoid_: service, connection

**Mapping**:
A single `local:remote` port forward within a Tunnel, optionally through a destination host reachable from the tunnel's ssh host (`local:desthost:remote`); each Mapping runs its own `ssh -L` process and is the unit of supervision.
_Avoid_: port forward, rule

**Label**:
An optional display name attached to a Mapping, shown in status output and logs. Informational only — never used for lifecycle control.
_Avoid_: id, name (those belong to the Tunnel)

**Wanted**:
A Tunnel or Mapping that should be running: enabled in config and not manually stopped. Only Wanted units are supervised; an explicit stop makes a unit not-Wanted and exempt from restarts.
_Avoid_: active, enabled (overloaded)

**Connecting**:
Supervision state of a Mapping whose ssh process is running but whose local port does not yet accept TCP connections.

**Active**:
Supervision state of a Mapping whose local port accepts TCP connections.

**Backing off**:
Supervision state of a Wanted Mapping whose process exited; the supervisor waits an exponentially increasing interval (capped, jittered) before restarting, and tracks the attempt count for reporting.

**Provisioning**:
The one-time setup that gives a Tunnel's ssh host an Identity: generating a keypair, recording the IdentityFile in `~/.ssh/config`, adding the host to known_hosts, and installing the public key in the remote account's `authorized_keys`. Invoked as `tnl setup [name]`; it never starts a Tunnel.
_Avoid_: keygen, bootstrap (too broad)

**Identity**:
The private key an ssh connection authenticates with for a given host. A host **has an Identity** when an explicit IdentityFile is configured for it (tnl `identity_file` or an `~/.ssh/config` `IdentityFile`) and the file exists, or when any of ssh's default-named keys exists on disk.
_Avoid_: credential, key (overloaded)

**Provisioned host**:
A host that has an Identity AND a confirmed `authorized_keys` push. `tnl setup` skips it.
_Avoid_: configured, registered

## Relationships

- A **Tunnel** contains one or more **Mappings**; each **Mapping** runs exactly one `ssh -L` process.
- A **Mapping** that is **Wanted** and exits transitions to **Backing off** and is restarted; a Mapping that is not **Wanted** stays stopped.
- A **Mapping** whose local port is occupied by another process reports `error - port in use` and skips spawning, but stays **Wanted** and keeps retrying with backoff, recovering automatically when the port frees. A **Tunnel** with `reclaim: true` instead terminates the occupant (same-user processes only) and takes the port. Two **Tunnels** claiming the same local port is a configuration error rejected at load.
- A **Tunnel** is reported ready when all of its **Wanted Mappings** are **Active**.
- Connection-death detection is delegated to ssh keepalives (`ServerAliveInterval`/`ServerAliveCountMax`), which turn dead connections into process exits the supervisor observes.
- **Provisioning** targets a Tunnel's ssh host, not its Mappings: one **Identity** per (host, effective-user) pair, shared by every Mapping in the Tunnel (and by any other Tunnel naming the same host+user).
- A Tunnel's `user` override (which becomes `-l` in the spawned ssh) also selects the remote account **Provisioning** installs the public key into; otherwise ssh's own resolution of the alias applies.

## Example dialogue

> **Dev:** "If the `db` tunnel's 3306 mapping dies overnight, what does the user see in the morning?"
> **Domain expert:** "The Mapping was **Wanted**, so the supervisor put it in **Backing off** and kept restarting it. By morning it's **Active** again — status shows a high attempt count if it flapped. If someone had run `tnl stop db`, it would be stopped and stay stopped, because it's no longer **Wanted**."

## Flagged ambiguities

- "enabled" was used to mean both config intent and runtime state — resolved: **enabled** is config intent only; **Wanted** is the runtime truth (enabled AND not manually stopped).
- "active" was used to mean both the supervision state and "is it running" — resolved: **Active** is a supervision state of a Mapping, never a property of a Tunnel.
