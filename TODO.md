# TODO

The single home for open work. Items were swept here from the dev diary
(`.devloop/DIARY.md`), `docs/positioning.md`, the spec's Open questions
(`docs/byre-spec-v0.md`), `docs/milestone-build-time-uid.md`, and the
README-next header (2026-07-05). Each item keeps a pointer to where the
detail lives; update this file when an item ships or gets consciously
dropped.

## 1. Launch blockers

Gates on going public -- README-next.md must not replace README.md, and the
site must not go live, before these ship (see the README-next.md header
comment and docs/positioning.md "Product implications" 1b/2).

- [ ] **Default-deny firewall skill.** The public copy claims it (the README
  contract block: "enable the default-deny firewall skill to close it"), so
  it must exist first. Even a blunt egress allowlist is enough; keeps core
  opinion-free. Once shipped, the hero transcript's `network:` line becomes
  live proof. URGENT -- Pete flagged 2026-07-03 and plans to drive it
  himself. Start from the spec's network-controls note and
  `internal/runner/runargs.go` (a run-time concern, not a Dockerfile one).
- [ ] **`brew install byre` must work.** The hero and Install copy lead with
  it. A tap (`pjlsergeant/tap/byre`) is enough; update the copy to whichever
  form ships. Depends on versioning + distribution below.

## 2. Near-term roadmap

- [ ] **Versioning + distribution** (flagged 2026-07-01) -- so byre installs
  on other boxes. Greenfield: no `byre version`, no ldflags injection, no
  goreleaser/Makefile, no tags. Likely shape: `byre version` + `-ldflags -X`;
  tagged cross-compiled releases (goreleaser or Makefile + Actions); an
  install path (curl|sh and/or `go install`). Images build per-host, so it's
  just the binary. Confirm scope with Pete before building.

## 3. Config UI follow-ups

Lower priority, queued after the 2026-07-01 overhaul (diary entry has
context; `docs/byre-config-ui-spec.md` is outdated -- loose context only).

- [ ] **env secret-masking** in the config UI.
- [ ] **Grant-weight summary line** (brief exists -- see diary 2026-07-01).
- [ ] **Host eyeballing pass** -- the TUI can't be driven from inside the
  box; someone on the host needs to click through the rebuilt form.

## 4. Designed but not built

- [ ] **Rootless Podman keep-id path.** The baked-UID plumbing assumes a
  rootful daemon; rootless remaps user namespaces. Design settled (generic-
  UID image + `--userns=keep-id`) in `docs/milestone-build-time-uid.md`;
  today's detect-and-warn stays until it lands.
- [ ] **Skill packaging & distribution.** `skill.toml` shape (ordering,
  dependencies, conflicts) deferred to the skills milestone; no
  `byre skill add` yet, so the "publishable/shareable" pitch is aspirational
  until a fetch/install path exists. See spec Open questions.
- [ ] **Skill trust surface.** How loudly to surface a skill's runtime
  grants (e.g. mounting a host socket). v0: legible via `byre status`; no
  permission framework yet. See spec Open questions.
- [ ] **Seed source kinds.** Host-path and config-literal exist; a
  resolved-reference kind (secret manager / `pass`) is reserved room, not
  built. See spec Open questions.

## 5. Test debt (host-side; needs a Docker host)

Deferred end-to-end confirmations from the build-time-UID milestone
(`docs/milestone-build-time-uid.md` § test matrix). The unit layer already
pins the build-time behavior.

- [ ] Gated integration test (`BYRE_DOCKER_TESTS=1`): a fresh `develop`
  produces host-UID-owned files in `/home/dev`, a fresh cache volume, and
  `/workspace`, with no root phase / `chown` / `gosu` in the launch path.
- [ ] `internal/builtins`: assert a fresh volume comes up owned by the baked
  UID.
- [ ] Rootless-Podman keep-id integration coverage (when that path is built).
- [ ] Live-container worktree run (git commit inside the box, two concurrent
  sessions) -- recipe in `docs/agent-volume-sharing.md`.

## 6. Doc chores

- [ ] **README-next.md worktree copy is stale** (~line 226): documents
  `byre worktree` as unconditionally "beside the repo"; the shipped behavior
  is the three-state `worktree_base` (unset -> refuse). Pete's WIP draft --
  flagged, not edited.
- [ ] **Walk back the "publishable/portable skills" framing** in spec/README
  (parked P1 from 2026-06-23; small doc edit, still pending).

## 7. Nice-to-haves

- [ ] **Print the grant summary on launch** -- a few terse `byre:` lines
  (project mount, host mounts, network, agent) before exec'ing the agent.
  "Do it when convenient, not for launch" (positioning.md).
- [ ] **Keep `byre status` output in lockstep with the marketing block** --
  the README/site show its output as proof; drift makes the proof a lie.
  Standing discipline, re-check at launch.

## Post-launch tripwire

- The H1 (`--dangerously-skip-permissions, without risking the farm.`) is a
  safety idiom, not a scope statement; one cold reader bounced. If cold
  readers keep bouncing off it post-launch, revisit (positioning.md
  "Reader-response evidence").

## Parked / consciously not doing

Recorded so they don't get re-raised. Detail in the diary and the docs cited.

- **Secret-manager seed backend** -- host-path seeding covers the
  single-user case; no backend worth committing to yet.
- **Automatic volume migration** for the baked-UID upgrade -- a no-op in
  practice; recovery is `byre reset` + re-login (documented, no code).
- **run_args `--user`/`--userns` detect-and-warn** -- author-only footguns;
  one-sentence spec caveat instead.
- **Machine-wide `shared` volume scope** -- removed; no natural boundary
  across unrelated projects. Worktree inheritance covers the real case.
- **Path nannying** (refusing to run on dangerous dirs) -- "a knife needs to
  be sharp".
- **claude-pod feature steals** -- reviewed 2026-07-04, nothing adopted.
