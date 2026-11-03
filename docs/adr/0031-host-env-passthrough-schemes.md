# env_from_host grows env: and tz: sources; TERM and TZ join the core layer

> Amended by ADR 0057 (2026-08-13): `encrypted:` and `encrypted-file:`
> join the closed scheme set.

ADR 0026 shipped `env_from_host` with `git:` as the only source scheme
and held that a new scheme arrives as its own decision with its own
review flagging, never as a quiet extension. This ADR is that arrival,
prompted by two needs: skills that consume host-supplied API keys
(`GEMINI_API_KEY`), and boxes that should render and timestamp like the
terminal byre was launched from. Decided 2026-07-14 with the maintainer;
`cwd:` added 2026-10-03 under the same rule.

Mechanics:

- **Sources are a CLOSED scheme set**:
  - `git:<config-key>` (ADR 0026).
  - `env:<HOST_VAR>`: the named host env var, read at launch; an absent
    var sets nothing, same as an unset git key.
  - `tz:` (no argument): the host timezone -- the `TZ` env var if set,
    else the IANA name derived from the `/etc/localtime` symlink;
    underivable sets nothing.
  - `cwd:` (no argument): the host directory the box's `/workspace`
    comes from -- the project dir, and under `byre worktree` the new
    worktree's own path, since that is the directory the box actually
    has. Opt-in per row (NOT in `CoreEnvFromHost`); an unresolvable one
    degrades like an absent host var: the row resolves empty, says so at
    develop, and the launch proceeds. `cwd:<anything>` is refused, and
    the editor's source picker has its own row for it.
  - `encrypted:` and `encrypted-file:`: a project credential's
    ciphertext, opened by the physical file's own `[credentials]`
    identity and delivered as an env var or a tmpfs file (ADR 0057).
  - `""`: disables the key.

  `encrypted:`, `encrypted-file:` and `""` are the only members whose
  value comes from the config rather than the host. Anything else is a
  validation error naming the legal schemes.
- **No `raw:` / literal scheme** (proposed, rejected): a literal value
  already has a home -- `[env]` -- and per the glossary a config-literal
  env var is *config* while an `env_from_host` entry naming a host source
  is a *grant* (a credential row is config the file carries, encrypted); a
  literal riding the grant key would be a non-grant wearing a grant
  costume on every GRANTS surface. The runtime-only-literal case is the
  already-parked "runtime-only env" item, not this key's job.
- **`env:` allows renaming** for free (`BOX_VAR = "env:HOST_VAR"`): the
  map key is the box-side name, the source names the host side.
- **TERM and TZ ship in `CoreEnvFromHost`** (`TERM = "env:TERM"`,
  `TZ = "tz:"`), riding the git-identity rails from ADR 0026: visible
  in status's Host env row and the config editor's env screen, counted
  in the exposure tally, overridable or disable-able per layer, and
  skipped by the preset-apply grant review as shipped baseline. Not hardcoded
  injection -- the `BYRE_*` protocol vars stay the only invisible env.
- **The legibility machinery is ADR 0026's**: an `env:` entry beyond
  the shipped defaults is flagged by the preset-apply grant review
  (`extraHostEnv` diffs against the core layer). Values are never echoed on any surface -- status and
  the UI print sources, so a secret passed via `env:` stays out of the
  scrollback.

A separate concern decided alongside: `[runtime.env_docs]`
-- a skill's declared consumed-env guidance map (`NAME = "one-line
guidance"`), pure documentation with no validation of the box; the
config UI env screen renders unprovided declared vars as dim suggestion
rows and enter prefills the add editor. See GLOSSARY "Env docs".
