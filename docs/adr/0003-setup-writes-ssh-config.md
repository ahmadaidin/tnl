# `tnl setup` records identities in ~/.ssh/config, not .tnlrc.yaml

tnl's config already has a per-tunnel `identity_file` field, yet `tnl setup` writes newly generated IdentityFiles into `~/.ssh/config` instead.

Why: the setup trigger spans both `.tnlrc.yaml` and `~/.ssh/config`, so the natural home for a generated key is ssh's own config, where it works for plain `ssh` and for any other tunnel naming the same host. Writing `.tnlrc.yaml` would round-trip a user-owned file and couple the key to one tunnel. `~/.ssh/config` is parsed by ssh natively, so the daemon's spawned `ssh` picks the key up without tnl needing to pass `-i`./

Consequences: tnl never rewrites `.tnlrc.yaml`. A tunnel with a set-but-missing `identity_file` has its key generated at that exact path (the config stays authoritative). ssh's first-match-wins semantics force the insertion to go before any earlier matching `Host`/`Match` block, which setup computes rather than blindly appending.

Status: accepted.
