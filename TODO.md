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

Two gates on going public. Until **both** ship: docs/marketing/README-next.md
must not replace README.md, and the site must not go live. (Both claim these
features in copy -- shipping the copy first would make it a lie.)

- [ ] **Default-deny firewall skill.** BUILT + core host-verified
  2026-07-05 (decisions: `docs/adr/0010`-`0012`; unit-tested, committed).
  Verified live on Docker Desktop via `byre develop`: box launches (gate
  opens), `curl api.anthropic.com` works, `curl example.com` times out,
  codex first-run auth reaches its allowlisted endpoint behind the wall.
  Remaining before this checks off:
  - [ ] Confirm `byre status` prints `Network: deny-by-default (skill:
    firewall)` + the Egress section for that project (10-second eyeball;
    not yet done), and rebuild the live project box once more to pick up
    the derived/port-scoped firewall.sh.
  - [x] Automated gated test (`-run IntegrationFirewall`): PASSED
    2026-07-05 on Docker Desktop (arm64) -- allow github.com, deny
    example.com, fail-closed with no helper. Bonus: Debian resolved
    iptables to the nft variant and the rules held, settling the
    nft-vs-legacy question empirically.
  - [x] Host action: `byre skill update` + rebuild -- done (the develop
    run above built the skill in).
  - Done when: the README contract block's claim ("enable the
    default-deny firewall skill to close it") is true, and the hero
    transcript's `network:` line is live proof -- it prints `open` or
    `deny-by-default` per config. (README.md already mentions the skill;
    README-next's stronger claims stay gated on the host verification.)
  - DONE since: allowlist DERIVED from enabled skills' `[runtime] egress`
    declarations + port-scoped rules (host[:port], default 443) -- Pete's
    2026-07-05 catches; needs a fresh host-side spot-check (rebuild, then
    confirm agent API + `curl example.com` behavior unchanged, and that
    `git clone https://...` works while ssh-to-github hangs unless
    `github.com:22` is added to FIREWALL_ALLOW).
  - v2 candidates (deliberately not v1): mid-session re-resolve of CDN
    IPs, DNS filtering, registry egress moved into language templates.
- [ ] **`brew install byre` works.** The hero and Install copy lead with
  it; the two-command story depends on it.
  - What: a tap is enough -- `brew install pjlsergeant/tap/byre` -- with
    the formula pulling a tagged release binary. Update README-next/site
    copy to whichever form actually ships.
  - Depends on: versioning + releases (§2).

## 2. Near-term roadmap

- [ ] **Firewall × custom-Dockerfile seam** (codereview finding, discussed
  with Pete 2026-07-06; fix properly, next up).
  - The confusion, resolved: `dockerfile =` turns off ALL of byre's
    build-time half (core block, launcher + launch gate, skills' build
    blocks, `base`/`apt`/`files`/`dockerfile_pre/post` go dead) and keeps
    ALL of the runtime half (mounts, volumes, env, ports, labels,
    lifecycle, netns hooks). "byre stops being the image author and stays
    the chauffeur."
  - The bug lives on that seam: the firewall skill's *runtime* netns hook
    still fires on opt-out projects, but the *build-time* pieces it needs
    (`/usr/local/bin/byre-firewall`, the launch gate) were never baked in.
    The box's own entrypoint starts immediately with open networking while
    the helper fails on the missing script -- the fail-closed ordering
    silently doesn't exist. `status` degrades the claim; develop says
    nothing up front.
  - Direction agreed: **verify the contract, then decide.** The opt-out
    contract already assigns the launcher ENTRYPOINT (and thus the gate)
    to the user, so an opt-out Dockerfile that implements the chassis
    contract is legitimate, not misconfigured. Before firing netns hooks
    on an opt-out project, probe the image for the gate + hook entrypoint:
    present -> run hooks, fail-closed holds; absent -> skip hooks and
    print the degraded claim loudly at develop time. Rejected: blanket-
    disable hooks on opt-out (breaks contract-implementing users; makes
    the firewall secretly generated-only -- a gate, doctrine-wise) and
    attempt-and-fail-loudly (open-network window before the helper dies).
  - Companion fixes from the same review (host-netns guard, worktree
    build-source notice, seed_prefs doc honesty) done 2026-07-06.
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

## 4. Designed but not built

- [ ] **Rootless Podman keep-id path.** The baked-UID plumbing assumes a
  rootful daemon (in-container UID == on-disk UID); rootless Podman remaps
  user namespaces and breaks that.
  - Design (settled): ship a *generic*-UID image on the rootless path and
    run with `--userns=keep-id:uid=,gid=` so the container's `dev` maps to
    the host user. Mode-select on the existing `runner.IsRootlessPodman`
    detection, whose consumer changes from warn to mode-select.
  - Interim: today's detect-and-warn in develop/status stays until this
    lands. Background: `docs/adr/0008-build-time-uid-bake.md`.
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
  concurrent sessions (main tree + worktree) at once. Recipe (in a
  byre'd repo):
  `git worktree add -b feat ../repo-feat && cd ../repo-feat`;
  `byre status` (expect "worktree of ...; inherited"); `byre develop`
  (inherits image+volumes -- agent already logged in); in the box
  `git commit --allow-empty -m x` (writes to the shared .git);
  meanwhile `byre develop` in the main tree runs CONCURRENTLY.
- [x] Firewall end-to-end (`BYRE_DOCKER_TESTS=1`): allowlisted host
  reachable, others dropped, launch fails closed when the helper never
  signals (`internal/commands/firewall_integration_test.go`,
  `-run IntegrationFirewall`). PASSED host-side 2026-07-05 (Docker
  Desktop, arm64; iptables-nft variant). A `docker restart` fail-closed
  case could still be added.
- [ ] Pre-existing data race in `TestWithSetupLockNotesWhenWaiting`
  (`internal/commands/lock_test.go` ~38: a bool shared across goroutines
  without sync). Test-only, surfaced by `go test -race` during the
  firewall work; unrelated to it. Fix when touching the lock code.

## 6. Doc chores

- [x] **Doc taxonomy migration** (2026-07-05, branch doc-taxonomy). One
  lane per kind of knowledge: `docs/GLOSSARY.md` (canonical vocabulary),
  `docs/PRINCIPLES.md` (standing commitments), `docs/adr/0001-0013`
  (decisions), `docs/ARCHITECTURE.md` (the spec, transformed to
  current-state mechanics), `docs/marketing/` (README-next +
  positioning). firewall-design.md packed into ADRs 0010-0012 and
  removed; historical markers on the milestone/design-note docs; code
  reconciled with the glossary (core block, "session is running",
  project-not-family).

- [ ] **docs/marketing/README-next.md worktree copy is stale** (~line 226):
  it documents
  `byre worktree` as unconditionally "beside the repo". Shipped behavior is
  the three-state `worktree_base`: unset -> refuse (never guess),
  `"sibling"` -> beside the repo, path -> under it, with `--path` as a
  per-invocation override. Pete's draft -- he rewrites or delegates.
- [ ] **Walk back the "publishable/portable skills" framing** in the spec
  and README -- not a goal until §4's packaging work exists. Small doc
  edit, pending since 2026-06-23.

## 7. Nice-to-haves

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

## Post-launch tripwire

- The H1 (`--dangerously-skip-permissions, without risking the farm.`) is
  a safety idiom, not a scope statement, and one cold reader bounced off
  it. The plain what-it-is sentence directly under it is mandatory
  mitigation. If cold readers keep bouncing post-launch, revisit the H1.
  (Background: docs/marketing/positioning.md "Reader-response evidence".)

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
