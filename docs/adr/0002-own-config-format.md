# Own config format, not tunn-compatible

tnl's config is its own design: tunnels keyed by name under `tunnels:`, each with `host`, optional `user`/`identity_file`/`enabled`/`reclaim`, and a `ports:` list of `local:remote` specs — or `local:desthost:remote` to forward through the ssh host — with an optional `label: <spec>` pair form for display names. It lives at `~/.tnlrc` (overridable with `-c`).

Why: tunn compatibility was initially assumed, then explicitly waived — there is no migration path to preserve. Terse `local:remote` specs read like ssh's own `-L` vocabulary (the three-part form is ssh's `-L local:desthost:remote` verbatim); labels are attached per mapping as a single-pair map when needed, so the common case stays one line per mapping. The shape maps 1:1 onto the domain model (Tunnel → Mappings with optional Labels). The `enabled` flag, Labels, and `reclaim` (kill a colliding port's same-user occupant) are delta features with no tunn equivalent.

Consequences: tunn configs do not parse as tnl configs, and vice versa. Field names echo tunn (`host`, `ports`, `user`, `identity_file`) deliberately — they are OpenSSH vocabulary, not a compatibility promise.

Status: accepted. Rewritten in place from the earlier tunn-compatible-schema draft, which the compatibility waiver invalidated before anything was built on it.
