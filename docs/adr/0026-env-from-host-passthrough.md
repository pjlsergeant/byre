# env_from_host: named host-value passthrough, shipped visible

> Amended by ADR 0031 (2026-07-14: more source schemes, TERM/TZ in the
> core layer) and ADR 0057 (2026-08-13: project credentials ride this
> table as `encrypted:` rows).

byre passes the host git identity (user.name/user.email as the four
`GIT_AUTHOR_*`/`GIT_COMMITTER_*` vars) into every box so commits are
attributed to the developer. That identity is a real grant, so it rides
a visible mechanism rather than hidden plumbing: `env_from_host` is a
config map `KEY = "source"`, and the git identity is byre's shipped
default layer of it. Decided 2026-07-12 (audit finding; design settled
in review with the maintainer, who chose rows over a boolean switch).

Mechanics:

- **Sources** are a closed scheme set, each member added by its own
  decision (the current set and its rules are ADR 0031's; encrypted rows
  are ADR 0057's). This ADR's member is `"git:<config-key>"` (read via
  `git config --get` on the host at launch; an empty host value sets
  nothing), plus `""`, which disables the key -- how a layer drops a
  lower layer's entry. A new scheme is a bigger grant than a new row
  (`env:` reaches any secret in the host environment), so it arrives
  with its own review flagging, never as a quiet extension.
- **The core layer**: `CoreEnvFromHost()` (the four git-identity keys,
  plus TERM and TZ from ADR 0031) merges UNDER `default.config` in the cascade -- a real config layer,
  not code. Any higher layer overrides or disables per key; an explicit
  `[env] KEY` in any layer beats the passthrough for that key (an
  explicit value outranks a shipped default).
- **Legibility**: counted in the launch exposure tally (the only
  uncounted plumbing is BYRE_UID/GID); one attributed
  `Host env:` row in `byre status`; read-only rows with source in the
  config editor's env screen (both editors -- the core layer is below
  the global file too); the preset-apply grant review flags any entry
  beyond the shipped defaults (a proposal asking for host values is
  exactly what that review exists to surface -- the core baseline
  doesn't cry wolf there).

Rejected: a `git_identity = true` boolean (the values are what the user
reasons about, and a switch can't represent per-key override or future
sources); sentinel values inside `env` (a string that secretly isn't
data -- collision-prone, non-declarative, and it would still need
special rendering).

ADR 0007 stays closed: the passthrough reads two public git config
values, not credentials, and nothing else crosses without its own
`env_from_host` entry -- which validation bounds to the closed scheme
set and the preset-apply review surfaces.
