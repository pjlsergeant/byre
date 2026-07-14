# env_from_host grows env: and tz: sources; TERM and TZ join the core layer

ADR 0026 shipped `env_from_host` with `git:` as the only source scheme
and explicitly reserved `env:...` "until someone actually needs it — at
which point it arrives with its own adoption flagging, not as a quiet
extension". This ADR is that arrival, prompted by two needs: skills
that consume host-supplied API keys (`GEMINI_API_KEY`), and boxes that
should render and timestamp like the terminal byre was launched from.
Decided 2026-07-14 with the maintainer (grilled; each fork his ruling).

Mechanics:

- **Sources are a CLOSED scheme set**: `git:<config-key>` (unchanged),
  `env:<HOST_VAR>` (the named host env var, read at launch; an absent
  var sets nothing, same as an unset git key), `tz:` (no argument: the
  host timezone — the `TZ` env var if set, else the IANA name derived
  from the `/etc/localtime` symlink; underivable sets nothing), and
  `""` (disables the key). Anything else is a validation error naming
  the legal schemes.
- **No `raw:` / literal scheme** (proposed, rejected): a literal value
  already has a home — `[env]` — and per the glossary a config-literal
  env var is *config* while `env_from_host` entries are *grants*; a
  literal riding the grant key would be a non-grant wearing a grant
  costume on every GRANTS surface. The runtime-only-literal case is the
  already-parked "runtime-only env" item, not this key's job.
- **`env:` allows renaming** for free (`BOX_VAR = "env:HOST_VAR"`): the
  map key is the box-side name, the source names the host side.
- **TERM and TZ ship in `CoreEnvFromHost`** (`TERM = "env:TERM"`,
  `TZ = "tz:"`), riding the git-identity rails from ADR 0026: visible
  in status's Host env row and the config editor's env screen, counted
  in the exposure tally, overridable or disable-able per layer, and
  skipped by the adoption summary as shipped baseline. Not hardcoded
  injection — the `BYRE_*` protocol vars stay the only invisible env.
- **All adoption/legibility machinery is inherited unchanged**: an
  `env:` entry beyond the shipped defaults is flagged by the adoption
  summary exactly as ADR 0026 promised (`extraHostEnv` diffs against
  the core layer). Values are never echoed on any surface — status and
  the UI print sources, so a secret passed via `env:` stays out of the
  scrollback.

Deferred: host-CWD passthrough ("perhaps" in the originating TODO) —
no consumer today, and the scheme grammar makes adding one later cheap
and non-breaking.

Shipped alongside (same session, separate concern): `[runtime.env_docs]`
— a skill's declared consumed-env guidance map (`NAME = "one-line
guidance"`), pure documentation with no validation of the box; the
config UI env screen renders unprovided declared vars as dim suggestion
rows and enter prefills the add editor. See GLOSSARY "Env docs".
