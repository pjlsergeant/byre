# Shared auth: decided design + implementation map

> **Decided 2026-07-07** in a grilling session with Pete. This doc is the
> build plan: it carries the complete decided design, an ordered
> implementation map with code pointers, and the ruled-out list. Do NOT
> relitigate decisions here; the rationale of record is
> `docs/adr/0017-shared-agent-identity.md`. Evidence base:
> `docs/agent-credential-mechanics.md` (state-dir inventories, write
> patterns, rotation semantics -- empirical + source-verified).
>
> Lifecycle: like `firewall-design.md` before it, this doc is absorbed
> into the ADR/ARCHITECTURE and **deleted** once implementation ships.

## The problem

Every project logs its agent in separately (the `.claude`/state volume is
per-project by construction). For the drop-into-any-folder pitch that's a
real adoption cost: N folders x 3 agents = N x 3 login ceremonies. The goal:
**log in once per agent, per machine, ever** -- opt-in, legible, and without
byre ever touching host credentials (ADR 0007 stays closed: byre reads and
copies nothing from the host -- Codex/Gemini logins happen in a box;
Claude's shared token is user-minted anywhere and explicitly handed over
at a prompt).

## The decided design, in one paragraph

Three new **companion skills** -- `claude-shared-auth`, `codex-shared-auth`,
`gemini-shared-auth` -- each enabled *alongside* its agent skill (typically
in `~/.byre/default.config`; any project can remove it with
`!claude-shared-auth`). Each contributes a **machine-scoped volume** (new
core grammar: `scope = "machine"` on `[[volumes]]`, Docker name
`byre-machine-u<uid>-<name>`) holding only that agent's identity, plus a
firstrun hook and -- for Claude -- a **launch env hook** (new core chassis:
`/etc/byre/env.d/*.sh`, sourced by the launcher just before exec). The
agent skills stay untouched with ONE exception, found in review:
`codex-login.sh` gains an identity-aware guard (see codex-shared-auth
below). Per-project state (history, transcripts, trust) stays exactly
where it is today.

Per-agent transport (dictated by the research doc's findings):

| Agent | Mechanism | Why |
|---|---|---|
| Claude | static token file -> env (`CLAUDE_CODE_OAUTH_TOKEN`) | refresh tokens are SINGLE-USE (concurrent refresh cascades logout) and Claude replaces symlinked files via temp+rename -- file sharing is unsafe twice over |
| Codex | symlink `$CODEX_HOME/auth.json` -> identity volume | in-place writes (source-verified); vendor-endorsed sharing; infrequent refresh |
| Gemini | symlinks for `oauth_creds.json`, `google_accounts.json`, `installation_id` | in-place writes (source-verified); BUT rotation semantics unverified -- build LAST, ship gated on an empirical test |

## Core changes (opinion-free; the skills carry the opinions)

### 1. Skill `description` field

One-line `description = "..."` at the top level of `skill.toml`. Parsed in
`internal/skills/skills.go` (the typed-field struct near the `[build]` /
`[runtime]` tables, ~skills.go:94), surfaced in the config UI's skill
picker and anywhere skills are enumerated side by side; `byre status`'s
Skills row stays names-only. Validation: warn-not-fail when absent
(hand-dropped skills stay legal); all builtins gain one. This is why:

```
claude              The Claude Code agent; login persists per project.
claude-shared-auth  Share one Claude login across all your projects.
```

Independent of everything below -- build and ship it first.

### 2. Volume scope

`Volume` (internal/config/config.go:102) gains `Scope string`
(`toml:"scope"`), values `project` (default, empty = project) | `machine`.
General config grammar -- valid in any `[[volumes]]` entry, config or
skill; validation rejects anything else (`config.Validate`, with the
existing volume-name-charset test as the pattern).

Naming (internal/commands/naming.go:55 `volumeName`): project scope stays
`byre-<id>-<name>`; machine scope is **`byre-machine-u<uid>-<name>`**.
The uid qualifier matches the `ImageTag` precedent (ADR 0008): on a
shared dev box, two users must NOT silently share one identity volume.
"machine" scope therefore means per-user-per-machine. Belt-and-braces:
validation refuses a project id literally equal to `machine`.

Mount wiring: `runparams.go` mounts machine-scoped volumes identically to
project ones (same `-v name:target`), only the name derivation differs.
Worktrees need no special handling -- the volume resolves to the same name
from every path by construction.

`seed` is FORBIDDEN on machine-scoped volumes (validation + test, review
finding): `seed.go:21` names seed targets project-scoped and stays that
way; a machine-scoped seed would seed one Docker volume and mount
another. The combination is also meaningless -- seeding is host->volume,
identity volumes are box-born by design.

### 3. Launch env hooks (`/etc/byre/env.d/`)

Chassis addition, sibling of `firstrun.d`: the launcher
(internal/gen/launcher.sh) sources every `/etc/byre/env.d/*.sh` (sorted,
best-effort, as the unprivileged dev user) **after** firstrun hooks and
immediately **before** exec'ing the agent, so a hook can export env into
the agent process. Skills ship env.d files the same way they ship firstrun
hooks (via `[build].files` into `/etc/byre/env.d/`). Generic mechanism --
core neither knows nor cares that the first user exports a token. Golden
Dockerfile test updates in `internal/gen` (byte-stable output rule).

### 4. Lifecycle + legibility

- **status** (internal/commands/status.go): machine-scoped volumes get
  their own row -- `Shared vols: claude-identity (machine-wide, all your
  projects)` -- after State/Cache vols. Not a grant (no host reach is
  widened; per-project credentials were already account-capable), but
  cross-project sharing must never be invisible.
- **reset** (internal/commands/reset.go): skips machine-scoped volumes and
  SAYS SO: names what it did not delete and prints how to delete it
  deliberately (`byre config` -> Volumes -> clear). Same for **forget**
  (forget.go). Blast-radius honesty, worktree-banner precedent.
- **VolumeAdmin / config UI Volumes drill-in** (internal/configui): lists
  machine-scoped volumes marked shared; per-volume clear works but the
  live-session guard widens from "this project has a session" to "ANY
  byre session is running" (label query without the project filter --
  see the develop fast-path label queries for the pattern). Clearing the
  identity volume IS the logout story: every project falls back to
  per-project login.

## The three skills

### claude-shared-auth (build first of the three)

- `[[volumes]] name = "claude-identity", role = "state",
  scope = "machine", target = "/home/dev/.byre-identity/claude"`.
- **Firstrun hook**: if `<identity>/token` is missing AND stdin is a TTY:
  print how to mint a token -- run `claude setup-token` on the host or in
  a `byre shell` pane, wherever a browser is handy -- then prompt for a
  paste. `read -s` (a year-long credential must not sit in scrollback),
  trim whitespace, warn-don't-block if it doesn't look like a token
  (formats aren't ours to enforce), write mode 600. Empty input or no
  TTY: print one line and continue -- degrade to per-project login,
  never block the launch. Deliberately NOT running setup-token inside
  the hook: that would parse another tool's interactive output (nobody's
  stable API) and require a browser story in the box.
- **Onboarding seed (host-verified fix, 2026-07-07)**: the env token
  authenticates inference but interactive Claude's first-run WIZARD gates
  on `.claude.json` existing, not on auth -- so when the shared token is
  present and the per-project config dir has no `.claude.json`, the
  firstrun hook seeds `{"hasCompletedOnboarding": true}` (fresh volumes
  only; never rewrite a file Claude owns). Trade: no first-run theme
  picker; `/config` re-opens it in-session.
- **env.d hook**: `export CLAUDE_CODE_OAUTH_TOKEN="$(cat <identity>/token
  2>/dev/null || true)"` -- exports only when the file exists and is
  non-empty; otherwise leaves env unset so Claude falls back to the
  per-project login flow untouched.
- **`[context]`**: briefs the in-box agent: auth comes from a shared
  token minted by `claude setup-token`, it expires after ~a year, and an
  auth failure means "tell the user to re-mint and re-paste" (delete
  `<identity>/token`, relaunch, follow the prompt). No expiry timers or
  warnings in byre -- the failure self-announces and the resident expert
  is pre-briefed (proportionality).
- Bonus recorded here: this also fixes the LATENT worktree hazard --
  concurrent worktree sessions sharing one `.claude` volume can already
  cascade-logout each other via single-use refresh tokens; with the
  static token, refreshes stop happening.

### codex-shared-auth

- `[[volumes]] name = "codex-identity", role = "state",
  scope = "machine", target = "/home/dev/.byre-identity/codex"`.
- **Firstrun hook**, idempotent, EVERY launch: ensure
  `$CODEX_HOME/auth.json` is a symlink to `<identity>/auth.json` (the
  identity volume's target IS the codex dir -- no extra nesting; create
  parent dirs; replace a plain file only if the identity copy is absent
  -- then MOVE the file in, adopting an existing login rather than
  clobbering it). Re-asserting every launch heals the logout-fork:
  `codex logout` unlinks the symlink, and a later `codex login` would
  otherwise write a local file, silently forking the credential.
- **Hook ordering + the base-skill exception (review findings)**:
  firstrun hooks run in glob order, so companion hooks install as
  `/etc/byre/firstrun.d/00-<name>` to sort before the agent skills'
  hooks. AND `codex-login.sh:25` currently deletes ANY symlinked
  `auth.json` before checking auth ("a symlinked credential must never
  count" -- an anti-planting defense from the initial import), which
  would rip out the companion's symlink every launch. It gains an
  identity-aware guard: a symlink whose target path lies inside
  `/home/dev/.byre-identity/` is legitimate and kept -- canonicalize the
  target's PARENT dir, and the final `auth.json` component MAY be absent:
  a DANGLING identity symlink is the expected first-login state (login
  writes through it into the shared volume) and must NOT be removed.
  Anything else is removed as today. Its "stored per-project" login message branches to
  "stored machine-wide (shared-auth)" when the symlink is in place.
  This narrows the planting defense to "any symlink except ours" --
  accepted: the agent can already read the credential the link would
  redirect.
- No env hook, no paste prompt: Codex writes in place (source-verified,
  storage.rs), so the FIRST `codex login` in any box writes through the
  dangling symlink and lands the credential in the shared volume.
  Vendor docs explicitly endorse moving `auth.json` between machines.
- Accepted residual (recorded, do not re-raise): the unlocked
  cross-process refresh race -- reload-before-write, no lock, refresh
  windows are 5-min-to-expiry / 8-day; worst case is last-writer-wins on
  a still-valid token.

### gemini-shared-auth (build LAST; ship gated)

- Same symlink pattern for `oauth_creds.json`, `google_accounts.json`,
  `installation_id` into `~/.gemini` (in-place writes, source-verified,
  oauth2.ts). `trustedFolders.json` is NOT touched -- per-project trust
  already lives in the per-project state volume, which is correct.
- **Ship gate**: Google refresh-rotation semantics are the one
  UNVERIFIED claim in the evidence doc (model knowledge says Google does
  not rotate refresh tokens on use, which would make this Codex-shaped
  and safe -- but nobody has verified it). Before this skill ships: two
  concurrent boxes sharing one credential file, force a token refresh,
  confirm neither session dies. If rotation turns out Claude-shaped:
  gemini-shared-auth is NOT released, Gemini stays per-project, and the
  gate result is recorded in the ADR (Google has no env-token
  equivalent to fall back to).

## SECURITY.md (new, repo root)

Created in step 7 as the home for security-model facts that are true,
important, and wrong for the README's register. Contents: the threat
model (the agent, never the user -- footgun doctrine), the boxed /
not-boxed contract in full, container-vs-microVM, **Docker daemon access
is root-equivalent** (any daemon user can mount any named volume,
including identity volumes -- byre cannot prevent this and does not
claim to; uid-qualified names prevent the *accidental* collision only),
`--self-edit` transitive trust, egress/exfiltration under an open
network, and machine-scoped volume visibility. GitHub surfaces
SECURITY.md natively.

## README / docs ripple (step 7)

- "byre never copies host credentials; agents log in once, inside the
  box" (README Volumes & state) gains the shared-auth reality: byre
  still reads and copies nothing from the host, but Claude's shared
  token is user-minted (possibly on the host) and pasted at a prompt --
  so reword to "nothing crosses unless you enable it; what you enable,
  status shows", NOT a login-location claim.
- ARCHITECTURE.md: volume scope, env.d, the companion-skill pattern.
- GLOSSARY: rewrite the **Volume** entry (it currently says "no scope
  knob... _Avoid_: volume scope" -- reversed by ADR 0017) + new entries:
  Volume scope, Identity volume, Companion skill, Launch env hooks.
- TODO Parked: delete the "machine-wide `shared` volume scope" negative
  (superseded by ADR 0017).

## Implementation map (ordered; one coherent commit each)

Workflow per CLAUDE.md: gofmt + go vet + go test green before each
commit; byre-codereview after each feature-sized step; unit tests via the
injected fake runner, host-side integration gated `BYRE_DOCKER_TESTS=1`.

1. **Skill `description`** -- skills.go parse + unknown-key test update;
   builtins gain descriptions; config-UI picker shows them. Ships alone.
2. **Volume scope (core)** -- config.go Volume.Scope + Validate
   (including seed-forbidden-on-machine-scope); naming.go machine-name
   derivation (+ TestVolumeName cases + the id != "machine" guard);
   runparams mount wiring; status Shared-vols row. Unit: fake-runner
   assertions that a machine-scoped volume mounts with the
   uid-qualified name from two different fake projects.
3. **Launch env hooks** -- launcher.sh sources /etc/byre/env.d/*.sh
   before exec; gen golden test update; launcher_test coverage.
4. **Lifecycle guards** -- reset/forget skip + explain (unit-test the
   wording: names the volume, names the deliberate-delete route);
   VolumeAdmin scope marking + any-session clear guard.
5. **claude-shared-auth** -- skill.toml + firstrun paste hook + env.d
   export + context brief; TestSelfHostCompositionResolves-style pin.
   Host verify: fresh project A prompts + accepts token; fresh project B
   launches logged-in with NO prompt; `byre reset` in A leaves auth
   intact and says so.
6. **codex-shared-auth** -- skill.toml + `00-`-prefixed symlink-assert
   hook (incl. the adopt-existing-login move) + the codex-login.sh
   identity-aware guard and message branch (the one agent-skill edit in
   this design). Host verify: login in A, B is authenticated;
   `codex logout` in A then relaunch A -- symlink healed, still one
   shared credential.
7. **SECURITY.md + doc sweep** -- as above; README claim reword lands
   here, keeping copy honest BEFORE anyone reads the new skills'
   descriptions and asks.
8. **gemini-shared-auth** -- skill + the empirical rotation gate; ships
   only if the gate passes. Record the result either way in ADR 0017.

## Ruled out (recorded so they stay ruled out)

- **Variant agent skills** (`agent = "claude-shared-auth"`): duplicates
  the whole agent skill per variant (installer, deps, egress) -- 3
  agents x 2 variants drifting apart. Companions carry only the delta.
- **Credential-file seeding from the host** (reopening ADR 0007): copies
  invite refresh collisions (Claude: cascading logout; all: stale
  forks), and byre would be in the host-credential business. byre never
  READING a credential -- everything arrives by in-box login or explicit
  user hand-over -- is load-bearing for the trust story.
- **Host env passthrough as the shared-auth story**: TODO 6's
  `env_passthrough` stays a separate, generic CI/API-key feature; making
  it the auth path puts a year-long token in host config/env and byre's
  hands. Shared-auth keeps the credential inside box-world.
- **A host-side token broker/daemon**: real revocation theater -- the
  agent holds the token in memory the moment it's delivered; byre is
  structurally daemon-less; disproportionate machinery.
- **Sharing the whole agent state dir** (one volume for everything):
  cross-project history/transcript bleed (all projects are /workspace --
  cwd-keyed state collides), and for Claude the root-level mixed-scope
  files make a clean mount split impossible anyway. Identity-only
  sharing sidesteps both.
- **Per-skill config keys** (`shared_auth = true` on the claude skill):
  no per-skill config grammar exists; enabling a companion skill IS the
  opt-in, in the vocabulary byre already has.
