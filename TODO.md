# TODO

**This file is authoritative.** It is the single source of truth for what is
open, planned, deferred, and consciously dropped -- and it is *edited
directly* to set direction: whatever this file says the TODO is, that IS the
TODO. Agents: re-read it at the start of a session, take priorities from it
as marching orders, and keep statuses current here (mark items done, move
dropped items to Parked) -- but don't restructure or reprioritize it
unprompted. Design docs and the diary hold *rationale* and are linked as
background; when one of them disagrees with this file about status, scope,
or priority, this file wins.

## 1. Launch blockers

Two gates on going public. Until **both** ship: README-next.md must not
replace README.md, and the site must not go live. (Both claim these
features in copy -- shipping the copy first would make it a lie.)

- [ ] **Default-deny firewall skill.** BUILT 2026-07-05 (design:
  `docs/firewall-design.md`; unit-tested, committed). Remaining before
  this checks off:
  - [ ] **Host-side verification** (needs Docker; can't run from the dev
    box): enable `firewall` on a real project; confirm the box reaches an
    allowlisted host, can't reach others, `byre status` prints the
    posture, and sabotaging the helper makes the launch die closed. Then
    wire it as the gated `BYRE_DOCKER_TESTS=1` integration test (§5).
  - [ ] Host action: `byre skill update` + rebuild to materialize the new
    skill on existing installs (the usual caveat).
  - Done when: the README contract block's claim ("enable the
    default-deny firewall skill to close it") is true, and the hero
    transcript's `network:` line is live proof -- it prints `open` or
    `deny-by-default` per config. (README.md already mentions the skill;
    README-next's stronger claims stay gated on the host verification.)
  - v2 candidates (deliberately not v1): mid-session re-resolve of CDN
    IPs, allowlist derived from enabled skills, DNS filtering.
- [ ] **`brew install byre` works.** The hero and Install copy lead with
  it; the two-command story depends on it.
  - What: a tap is enough -- `brew install pjlsergeant/tap/byre` -- with
    the formula pulling a tagged release binary. Update README-next/site
    copy to whichever form actually ships.
  - Depends on: versioning + releases (§2).

## 2. Near-term roadmap

- [ ] **Versioning + distribution** (flagged 2026-07-01) -- so byre
  installs and runs on other boxes. Confirm scope with Pete before
  building.
  - Current state: greenfield. No `byre version` command, no version
    variable or `-ldflags` injection, no goreleaser/Makefile, no git tags.
  - Shape: `byre version` + version baked via `-ldflags -X`; tagged
    cross-compiled releases (goreleaser, or a small Makefile + GitHub
    Actions release workflow); an install path (curl|sh and/or
    `go install`).
  - Scope note: images build per-host and are never shipped (the
    build-time-UID decision), so distribution is just the single static
    binary -- skills and templates are embedded.

## 3. Config UI follow-ups

Lower priority, queued after the 2026-07-01 overhaul. (Background: diary
2026-07-01. `docs/byre-config-ui-spec.md` is outdated -- loose context
only, not gospel.)

- [ ] **env secret-masking:** env values render in plaintext in the form;
  mask values (show on reveal, or mask all but a prefix) so a shoulder-surf
  or screenshot doesn't leak tokens.
- [ ] **Grant-weight summary line:** a one-line summary of how much the
  config grants (mounts / ports / env / network) so the form communicates
  total exposure at a glance.
- [ ] **Host eyeballing pass:** the TUI can't be driven from inside the
  box; someone on the host needs to click through the rebuilt form
  (sections, pickers, list editors, volumes drill-in, ctrl+e raw edit).

## 4. Designed but not built

- [ ] **Rootless Podman keep-id path.** The baked-UID plumbing assumes a
  rootful daemon (in-container UID == on-disk UID); rootless Podman remaps
  user namespaces and breaks that.
  - Design (settled): ship a *generic*-UID image on the rootless path and
    run with `--userns=keep-id:uid=,gid=` so the container's `dev` maps to
    the host user. Mode-select on the existing `runner.IsRootlessPodman`
    detection, whose consumer changes from warn to mode-select.
  - Interim: today's detect-and-warn in develop/status stays until this
    lands. Background: `docs/milestone-build-time-uid.md`.
- [ ] **Skill packaging & distribution.** No `byre skill add` / fetch /
  install path exists -- v0 is built-in skills plus hand-dropped
  `~/.byre/skills/<name>/`. The full `skill.toml` semantics (ordering,
  dependencies, conflicts) are deferred to a skills milestone. Until this
  ships, any "publishable/shareable skills" pitch is aspirational (see the
  doc chore in §6).
- [ ] **Skill trust surface.** A skill can carry real grants (e.g. mount a
  host socket). Today that's legible via `byre status` and nothing else --
  no consent step, no permission framework. Decide how loudly grants are
  surfaced (at develop? at skill install?) and whether there's an approval
  gate.

## 5. Test debt (host-side; needs a Docker host)

End-to-end confirmations that can't run from inside the dev box. The unit
layer already pins build-time behavior (golden Dockerfile; chown-to-baked-
UID assertions in `gen_test.go`/`context_test.go`).

- [ ] Gated integration test (`BYRE_DOCKER_TESTS=1`): a fresh `develop`
  produces host-UID-owned files in `/home/dev`, a fresh cache volume, and
  `/workspace`, with no root phase / `chown` / `gosu` in the launch path.
- [ ] `internal/builtins`: assert a fresh volume comes up owned by the
  baked UID.
- [ ] Rootless-Podman keep-id integration coverage (when §4's path is
  built).
- [ ] Live-container worktree run: git commit inside the box + two
  concurrent sessions (main tree + worktree) at once. Recipe in
  `docs/agent-volume-sharing.md`.
- [x] Firewall end-to-end (`BYRE_DOCKER_TESTS=1`): allowlisted host
  reachable, others dropped, launch fails closed when the helper never
  signals. WRITTEN (`internal/commands/firewall_integration_test.go`,
  `-run IntegrationFirewall`) but NOT YET RUN host-side -- verify it
  actually passes on real Docker (fragile spots: `nc -l` syntax, getent
  resolution, the gate handshake timing). A `docker restart` fail-closed
  case could still be added.
- [ ] Pre-existing data race in `TestWithSetupLockNotesWhenWaiting`
  (`internal/commands/lock_test.go` ~38: a bool shared across goroutines
  without sync). Test-only, surfaced by `go test -race` during the
  firewall work; unrelated to it. Fix when touching the lock code.

## 6. Doc chores

- [ ] **README-next.md worktree copy is stale** (~line 226): it documents
  `byre worktree` as unconditionally "beside the repo". Shipped behavior is
  the three-state `worktree_base`: unset -> refuse (never guess),
  `"sibling"` -> beside the repo, path -> under it, with `--path` as a
  per-invocation override. Pete's draft -- he rewrites or delegates.
- [ ] **Walk back the "publishable/portable skills" framing** in the spec
  and README -- not a goal until §4's packaging work exists. Small doc
  edit, pending since 2026-06-23.

## 7. Nice-to-haves

- [ ] **Print the grant summary on launch:** a few terse `byre:` lines
  (project mount, host mounts, network, agent) before exec'ing the agent,
  so every real session opens by showing the walls going up. The hero
  transcript stays an accepted illustrative mock either way. Do it when
  convenient, not for launch.
- [ ] **Keep `byre status` output in lockstep with the marketing block:**
  the README/site show its output as proof; drift makes the proof a lie.
  Standing discipline -- re-verify at launch and after any status change.

## Post-launch tripwire

- The H1 (`--dangerously-skip-permissions, without risking the farm.`) is
  a safety idiom, not a scope statement, and one cold reader bounced off
  it. The plain what-it-is sentence directly under it is mandatory
  mitigation. If cold readers keep bouncing post-launch, revisit the H1.
  (Background: positioning.md "Reader-response evidence".)

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
- **Machine-wide `shared` volume scope** -- removed; no natural boundary
  across unrelated projects. Worktree identity-inheritance covers the real
  case.
- **Path nannying** (refusing to run on dangerous dirs) -- "a knife needs
  to be sharp"; Pete runs byre on `~/.byre` itself.
- **claude-pod feature steals** -- reviewed 2026-07-04, nothing adopted,
  no public mention.
