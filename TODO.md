# TODO

**This file is authoritative.** It is the single source of truth for what is
open, planned, and consciously dropped -- and it is *edited directly* to set
direction: whatever this file says the TODO is, that IS the TODO. Agents:
re-read it at the start of a session, take priorities from it as marching
orders, and keep it live. Finished and dropped items are *removed* and take
their rationale with them -- git history is the archive. Don't restructure or
reprioritize it unprompted. Rationale lives in the ADRs and docs linked per
item; when one of them disagrees with this file about status, scope, or
priority, this file wins.

Sections are priority tiers -- Now, Next, Someday -- plus Standing
(disciplines, not tasks) and Parked (decided negatives).

## Now

- [ ] **Offer to fix a shadowing Claude credentials.json** (Pete,
  2026-07-08): the launcher already warns when a leftover per-project
  `/login` credential shadows the shared token (it 401s ~8h later); go one
  better and offer the fix (move `~/.claude/.credentials.json` aside)
  instead of just naming it.
- [ ] **README "How do I": enable the firewall** (Pete, 2026-07-08): doc
  add to the existing How-do-I section.
- [ ] **README "How do I": mount other folders** (Pete, 2026-07-08): doc
  add, same section.
- [ ] **Egress doors ship closed** (Pete, 2026-07-08; design settled in
  `docs/adr/0020-egress-doors-ship-closed.md`): new `egress_offered`
  string-list key (config + skill `[runtime]`), always inert; the Egress
  screen shows offered entries as closed switches whose toggle writes the
  entry into the open layer. The firewall's whole base list (git, apt,
  registries) moves to offered; templates carry their registries offered
  (verify storage.googleapis.com is still needed for go at all); a
  skill's `egress` stays open-with-enablement for its own functional
  endpoints only (agents). Firewall context.md redirects the agent;
  GLOSSARY gains the term.
- [ ] **AGENTS.md in `~/.byre`.** Minimal best-practices guide for agents in
  the store: version-controlling `~/.byre`, composing skills, layering over
  provided skills instead of editing them in place. Start minimal; grows
  alongside bundle sharing (e.g. "how do I eject this skill"). Delivery
  shape TBD -- likely how devloop's conventions ride in.
- [ ] **Skill & template bundle sharing + trust surface.** A bundle/install
  format for skills and templates -- today it's built-ins plus hand-dropped
  `~/.byre/skills/<name>/`, and templates have no sharing story at all. Its
  safety half ships with it: skills carry real grants, so decide how loudly
  grants are surfaced at install/develop and whether there's an approval
  gate. Full `skill.toml` semantics (ordering, dependencies, conflicts) stay
  deferred to a skills milestone.
- [ ] **Agent-runnable integration tests.** The gated `BYRE_DOCKER_TESTS=1`
  suite needs a Docker host the agent can reach. Design pass across: nested
  rootless podman in-box (pulls the keep-id work forward), a CI job (cheap,
  guards every push), a docker-capable host VM (e.g. a smolmachines
  instance). Not mutually exclusive. Unlocks most of the test debt below.

## Next

- [ ] **Skill env guidance strings** (Pete, 2026-07-08): let a skill
  declare env vars it CONSUMES, each with a one-line guidance string
  (shape sketch: `[[runtime.env_docs]]` with name + guidance); the config
  UI's env screen shows a dim suggestion row per declared var from
  enabled skills, enter prefills the add editor. Pure documentation, no
  validation. Example targets: `GEMINI_API_KEY`. (ADR 0019 removed the
  original motivating case, `FIREWALL_ALLOW`.)
- [ ] **Config UI: env secret-masking.** env values render in plaintext in
  the form; mask them (reveal on demand) so a shoulder-surf or screenshot
  doesn't leak tokens.
- [ ] **Grant summary, two surfaces.** A one-line total-exposure summary
  (mounts / ports / env / network) in the config UI, and a few terse
  `byre:` lines printed at launch before exec'ing the agent, so every real
  session opens by showing the walls going up.
- [ ] **Host-side test session.** The end-to-end cases that stay manual
  until agent-runnable tests exist; shrinks to whatever that item doesn't
  automate. The unit layer already pins build-time behavior.
  - fresh `develop`: host-UID-owned files in `/home/dev` and `/workspace`,
    fresh cache volume, no root phase / chown / gosu in the launch path
  - `internal/builtins`: a fresh volume comes up owned by the baked UID
  - live worktree run: git commit in-box + two concurrent sessions (main
    tree + worktree)
  - shared-auth machinery: automated coverage for the hand-verified
    behavior (`docs/adr/0017-shared-agent-identity.md`)
  - firewall fail-closed after `docker restart` (launch-path cases already
    pass)
- [ ] **Site.** Landing page + real docs, devlog demoted to `/devlog/`; the
  decided shape lives in `docs/marketing/positioning.md` "Site plan".
- [ ] **TERM + timezone + host-env passthrough.** Pass host TERM and TZ into
  every box via the chassis (the box currently guesses both), plus a config
  key for named host env vars (shape TBD, e.g. `env_passthrough = ["FOO"]`).
  Per `docs/GLOSSARY.md` a passed-through var IS a grant: name it in
  `byre status` and the config UI's GRANTS section.

## Someday

- [ ] **Rootless Podman keep-id path.** Design settled: generic-UID image on
  the rootless path, run with `--userns=keep-id`, mode-select on the
  existing `runner.IsRootlessPodman` detection. Today's detect-and-warn
  stays until this lands; add integration coverage when it does. Background:
  `docs/adr/0008-build-time-uid-bake.md`. Promotes if the agent-runnable-
  tests design picks nested podman.
- [ ] **Drag-and-drop into the boxed terminal.** Dropping a file pastes its
  host path, meaningless in-box. Needs a design pass: translate paths under
  the project dir to `/workspace`, treat outside paths as a grant question,
  survey per-terminal drop behavior.
- [ ] **gemini OAuth gate.** Two concurrent gemini boxes sharing one OAuth
  credential, run past the ~1h token expiry; neither dying = OAuth sharing
  is safe. The API-key path is already verified
  (`docs/adr/0017-shared-agent-identity.md` verification record).
- [ ] **Lock-test data race.** `TestWithSetupLockNotesWhenWaiting`
  (`internal/commands/lock_test.go`): a bool shared across goroutines
  without sync, test-only, surfaced by `go test -race`. Fix when touching
  the lock code.

## Standing

Disciplines and tripwires, not tasks.

- **Status/marketing lockstep:** the README/site show `byre status` output
  as proof; re-verify it against status.go after any status change so the
  proof doesn't become a lie.
- **Post-launch H1 tripwire:** the H1 is a safety idiom, not a scope
  statement; the plain what-it-is sentence under it is mandatory
  mitigation. If cold readers keep bouncing post-launch, revisit it.
  (Background: `docs/marketing/positioning.md` "Copy bank".)

## Parked / consciously not doing

Decided negatives, recorded so they don't get re-raised. Rationale lives in
the docs cited and in git history.

- **Secret-manager seed backend** (`pass` / resolved-reference seed kind) --
  host-path + config-literal seeding covers the single-user case. Design
  constraint if ever revived: the seed-source model reserves room for a
  resolved-reference kind, so don't hardcode new code paths to "path".
- **Automatic volume migration** for the baked-UID upgrade -- a no-op in
  practice; recovery is `byre reset` + re-login (documented, no code).
- **run_args `--user`/`--userns` detect-and-warn** -- author-only footguns;
  one-sentence spec caveat instead of code.
- ~~**Machine-wide `shared` volume scope**~~ -- REVERSED by ADR 0017: agent
  identity turned out to be the natural boundary the original ruling said
  didn't exist. Machine-scoped volumes shipped with shared auth.
- **Hardening the project store against a --self-edit agent** (symlink
  checks, byre.config writes, path record, lock) -- reverted (0f35743). A
  --self-edit agent already authors the next develop's config and build
  context through the front door, so store-symlink defenses protect a
  boundary that doesn't exist; `--self-edit` means trusting the agent with
  the host, full stop. Reviewers WILL re-find this class -- conscious
  negative, don't re-fix.
- **Path nannying** (refusing to run on dangerous dirs) -- "a knife needs
  to be sharp"; Pete runs byre on `~/.byre` itself.
- **claude-pod feature steals** -- reviewed, nothing adopted, no public
  mention.
