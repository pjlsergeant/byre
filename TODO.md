# TODO

**This file is authoritative.** It is the single source of truth for what is
open, planned, deferred, and consciously dropped -- and it is *edited
directly* to set direction: whatever this file says the TODO is, that IS the
TODO. Agents: re-read it at the start of a session, take priorities from it
as marching orders, and keep it live: finished items are *removed* (git
history and the diary are the archive), dropped items move to Parked. Don't
restructure or reprioritize it unprompted. Design docs and the diary hold
*rationale* and are linked as background; when one of them disagrees with
this file about status, scope, or priority, this file wins.

## 1. Near-term roadmap

- [ ] **Site** (landing page + real docs, devlog demoted to `/devlog/`):
  still on; the decided shape lives in `docs/marketing/positioning.md`
  "Site plan".
- [ ] **AGENTS.md in `~/.byre`: best-practices guide for agents** (Pete,
  2026-07-06; refined 2026-07-07): drop a minimal AGENTS.md into the store
  covering at least: how to version-control `~/.byre`, how to compose
  skills, and "don't over-write agent-provided ones (go over them
  instead)" -- i.e. layer on top of provided skills rather than editing
  them in place. Start minimal; may grow into the comprehensive guide
  originally floated. Delivery shape TBD -- likely the way devloop's
  conventions ride in (agent context/memory).
- [ ] **Shared agent credentials -- BUILT (steps 1-7 of 8), host
  verification pending** (built 2026-07-07 per
  `docs/shared-auth-design.md`; rationale
  `docs/adr/0017-shared-agent-identity.md`; evidence
  `docs/agent-credential-mechanics.md`). Shipped: skill `description`
  field; machine-scoped volumes (`scope = "machine"`,
  `byre-machine-u<uid>-<name>`); launch env hooks (`/etc/byre/env.d`);
  reset/forget spare-and-say-so + guarded UI clears; claude-shared-auth
  (setup-token paste -> env) and codex-shared-auth (symlink assert +
  codex-login identity guard). Remaining:
  - [ ] Host-side verification (needs Docker; recipes in the design
    doc steps 5-6): token prompt in project A -> project B launches
    logged in; codex login in A -> B authenticated; logout-fork heal;
    reset spares the identity volumes and says so. Then
    `byre skill update` + rebuild to materialize on real boxes.
  - [ ] **gemini-shared-auth (step 8)**: build LAST, ship gated on the
    empirical refresh-rotation test (two concurrent boxes sharing one
    credential file, force a refresh, neither session dies). If
    rotation is Claude-shaped: not released; record either way in ADR
    0017.
  - [ ] Design-doc lifecycle: absorb `docs/shared-auth-design.md` into
    ADR/ARCHITECTURE and delete once step 8 resolves
    (firewall-design.md precedent). Revisits two prior negatives, deliberately: Parked
  "machine-wide shared volume scope" (agent identity IS naturally
  machine-scoped) and the retired creds/history split; ADR-0007 stays
  closed (no host-credential copying). Env passthrough (§6) remains the
  separate CI/API-key story.
- [ ] **Brew tap** (optional, Pete-side): create the
  `pjlsergeant/homebrew-tap` repo + the `HOMEBREW_TAP_GITHUB_TOKEN`
  Actions secret (steps in `docs/RELEASING.md`); the next tagged release
  publishes the cask automatically. The README Install section already
  shows the brew line (added 2026-07-06, ahead of the tap -- it 404s
  until the tap repo exists). Everything else about versioning + distribution
  shipped 2026-07-06 as v0.1.1 (`docs/adr/0016`, `docs/RELEASING.md`,
  `CHANGES.md`).

## 2. Config UI follow-ups

Lower priority, queued after the 2026-07-01 overhaul. (Background: diary
2026-07-01.)

- [ ] **env secret-masking:** env values render in plaintext in the form;
  mask values (show on reveal, or mask all but a prefix) so a shoulder-surf
  or screenshot doesn't leak tokens.
- [ ] **Grant-weight summary line:** a one-line summary of how much the
  config grants (mounts / ports / env / network) so the form communicates
  total exposure at a glance.
- [ ] **Host eyeballing pass:** the TUI can't be driven from inside the
  box; someone on the host needs to click through the rebuilt form
  (sections, pickers, list editors, volumes drill-in, ctrl+e raw edit).

## 3. Designed but not built

- [ ] **Rootless Podman keep-id path.** The baked-UID plumbing assumes a
  rootful daemon (in-container UID == on-disk UID); rootless Podman remaps
  user namespaces and breaks that.
  - Design (settled): ship a *generic*-UID image on the rootless path and
    run with `--userns=keep-id:uid=,gid=` so the container's `dev` maps to
    the host user. Mode-select on the existing `runner.IsRootlessPodman`
    detection, whose consumer changes from warn to mode-select.
  - Interim: today's detect-and-warn in develop/status stays until this
    lands. Background: `docs/adr/0008-build-time-uid-bake.md`.
- [ ] **Skill & template bundle sharing** (upgraded to a definite need,
  Pete 2026-07-07: "we definitely need a way to share skills and template
  bundles, even if that's just a bundle/install format"). No
  `byre skill add` / fetch / install path exists -- v0 is built-in skills
  plus hand-dropped `~/.byre/skills/<name>/` -- and templates have no
  sharing story at all. Minimum viable shape: a bundle/install format
  covering both skills and templates; the full `skill.toml` semantics
  (ordering, dependencies, conflicts) stay deferred to a skills
  milestone. Until this ships, any "publishable/shareable skills" pitch
  is aspirational (see the doc chore in §5).
- [ ] **Skill trust surface.** A skill can carry real grants (e.g. mount a
  host socket). Today that's legible via `byre status` and nothing else --
  no consent step, no permission framework. Decide how loudly grants are
  surfaced (at develop? at skill install?) and whether there's an approval
  gate.

## 4. Test debt (host-side; needs a Docker host)

End-to-end confirmations that can't run from inside the dev box. The unit
layer already pins build-time behavior (golden Dockerfile; chown-to-baked-
UID assertions in `gen_test.go`/`context_test.go`).

- [ ] Gated integration test (`BYRE_DOCKER_TESTS=1`): a fresh `develop`
  produces host-UID-owned files in `/home/dev`, a fresh cache volume, and
  `/workspace`, with no root phase / `chown` / `gosu` in the launch path.
- [ ] `internal/builtins`: assert a fresh volume comes up owned by the
  baked UID.
- [ ] Rootless-Podman keep-id integration coverage (when §3's path is
  built).
- [ ] Live-container worktree run: git commit inside the box + two
  concurrent sessions (main tree + worktree) at once. Recipe (in a
  byre'd repo):
  `git worktree add -b feat ../repo-feat && cd ../repo-feat`;
  `byre status` (expect "worktree of ...; inherited"); `byre develop`
  (inherits image+volumes -- agent already logged in); in the box
  `git commit --allow-empty -m x` (writes to the shared .git);
  meanwhile `byre develop` in the main tree runs CONCURRENTLY.
- [ ] Firewall `docker restart` fail-closed integration case -- the
  shipped `-run IntegrationFirewall` (passed 2026-07-05) covers
  allow/deny/fail-closed-at-launch but not a restarted box.
- [ ] Pre-existing data race in `TestWithSetupLockNotesWhenWaiting`
  (`internal/commands/lock_test.go` ~38: a bool shared across goroutines
  without sync). Test-only, surfaced by `go test -race` during the
  firewall work; unrelated to it. Fix when touching the lock code.

## 5. Doc chores

- [ ] **Walk back the "publishable/portable skills" framing** in the spec
  and README -- not a goal until §3's packaging work exists. Small doc
  edit, pending since 2026-06-23.

## 6. Nice-to-haves

- [ ] **Host-env passthrough** (Pete, 2026-07-05): a config key to pass
  named host env vars into the box (shape TBD, e.g.
  `env_passthrough = ["FOO"]`). Today `env` is literal-only and nothing
  crosses from the host except git identity (`GIT_AUTHOR_*` /
  `GIT_COMMITTER_*`). Per GLOSSARY.md, a passed-through var IS a grant
  (a literal isn't) -- so when built, it must be named in `byre status`
  and belongs in the config UI's GRANTS section.
- [ ] **Print the grant summary on launch:** a few terse `byre:` lines
  (project mount, host mounts, network, agent) before exec'ing the agent,
  so every real session opens by showing the walls going up. The hero
  transcript stays an accepted illustrative mock either way. Do it when
  convenient, not for launch.
- [ ] **Keep `byre status` output in lockstep with the marketing block:**
  the README/site show its output as proof; drift makes the proof a lie.
  Standing discipline -- re-verify at launch and after any status change.
  (README's Quickstart status block reconciled against status.go's actual
  rows -- Project id/Ports/Skills lines, volume names, container short id
  -- 2026-07-06, at the README-next swap.)

## Post-launch tripwire

- The H1 (`--dangerously-skip-permissions, without risking the farm.`) is
  a safety idiom, not a scope statement, and one cold reader bounced off
  it. The plain what-it-is sentence directly under it is mandatory
  mitigation. If cold readers keep bouncing post-launch, revisit the H1.
  Also: the flag is Claude's and could be renamed -- the H1 stays a
  five-minute edit. (Background: docs/marketing/positioning.md
  "Copy bank".)

## Parked / consciously not doing

Decided negatives, recorded so they don't get re-raised. Rationale lives in
the diary and the docs cited.

- **Secret-manager seed backend** (`pass` / resolved-reference seed kind)
  -- host-path + config-literal seeding covers the single-user case; no
  backend worth committing to yet. Design constraint if ever revived: the
  seed-source model reserves room for a resolved-reference kind, so don't
  hardcode new code paths to "path".
- **Automatic volume migration** for the baked-UID upgrade -- a no-op in
  practice; recovery is `byre reset` + re-login (documented, no code).
- **run_args `--user`/`--userns` detect-and-warn** -- author-only
  footguns; one-sentence spec caveat instead of code.
- ~~**Machine-wide `shared` volume scope**~~ -- REVERSED by ADR 0017
  (2026-07-07): agent identity turned out to be the natural boundary the
  original ruling said didn't exist. Machine-scoped volumes are now part
  of the shared-auth design (§1).
- **Hardening the project store against a --self-edit agent** (symlink
  checks on the build context, byre.config writes, path record, lock) --
  reverted 2026-07-06 (0f35743) after being built from a codereview
  finding. A --self-edit agent already authors the next develop's config
  (mounts/run_args) and build context through the front door, so
  store-symlink defenses protect a boundary that doesn't exist;
  `--self-edit` means trusting the agent with the host, full stop.
  Reviewers WILL re-find this class -- it's a conscious negative, don't
  re-fix.
- **Path nannying** (refusing to run on dangerous dirs) -- "a knife needs
  to be sharp"; Pete runs byre on `~/.byre` itself.
- **claude-pod feature steals** -- reviewed 2026-07-04, nothing adopted,
  no public mention.
