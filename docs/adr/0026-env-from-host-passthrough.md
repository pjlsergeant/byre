# env_from_host: named host-value passthrough, shipped visible

byre passes the host git identity (user.name/user.email as the four
`GIT_AUTHOR_*`/`GIT_COMMITTER_*` vars) into every box so commits are
attributed to the developer. That used to be hardcoded plumbing —
invisible on every legibility surface, consciously excluded from the
exposure tally with a comment promising it would join "when named
host-env passthrough lands, being a real grant". This ADR lands it:
`env_from_host` is a config map `KEY = "source"`, and the git identity
becomes byre's shipped default layer of it. Decided 2026-07-12 (audit
finding; design settled in review with the maintainer, who chose rows
over a boolean switch).

Mechanics:

- **Sources**: `"git:<config-key>"` (read via `git config --get` on the
  host at launch; an empty host value sets nothing) or `""` (disables
  the key — how a layer drops a lower layer's entry). **`env:...` is
  reserved and rejected**: arbitrary host-env passthrough is a far
  bigger grant (any secret in the host environment) and stays undesigned
  until someone actually needs it — at which point it arrives with its
  own adoption flagging, not as a quiet extension.
- **The core layer**: `CoreEnvFromHost()` (the four git-identity keys)
  merges UNDER `default.config` in the cascade — a real config layer,
  not code. Any higher layer overrides or disables per key; an explicit
  `[env] KEY` in any layer beats the passthrough for that key (an
  explicit value outranks a shipped default).
- **Legibility**: counted in the launch exposure tally (the old
  "plumbing" exemption now covers only BYRE_UID/GID); one attributed
  `Host env:` row in `byre status`; read-only rows with source in the
  config editor's env screen (both editors — the core layer is below
  the global file too); the adoption ⚠ summary flags any entry beyond
  the shipped defaults (a proposal asking for host values is exactly
  what that summary exists to surface — the core baseline doesn't cry
  wolf there).

Rejected: a `git_identity = true` boolean (the values are what the user
reasons about, and a switch can't represent per-key override or future
sources); sentinel values inside `env` (a string that secretly isn't
data — collision-prone, non-declarative, and it would still need
special rendering).

ADR 0007 stays closed: the passthrough reads two public git config
values, not credentials, and nothing else crosses without its own
`env_from_host` entry — which validation bounds to `git:` and adoption
surfaces.
