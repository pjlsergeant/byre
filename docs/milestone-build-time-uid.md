# Milestone — build-time UID ownership (delete the runtime chown fence)

**Status:** implemented (rootful path); rootless Podman keep-id path deferred to a
sequenced follow-up.
**Supersedes:** the runtime UID-reconciliation mechanism in `internal/gen/launcher.sh`
(`reown_storage` / `chown_tree` / `needs_chown`) and its supporting plumbing.

---

## TL;DR

_(Implemented. The present-tense framing below describes the **old** model this
milestone removed; see the Status line and "Decisions (resolved)" for what
shipped.)_

byre used to build its image **UID-agnostic** (the `dev` user defaulted to 1000)
and then, at every container launch, run a privileged recursive `chown` to
reconcile `/home/dev` and the named volumes to the host UID — behind a fragile
"fence" that pruned nested mounts (via `/proc/mounts` + `find -xdev` + `chown -h`)
so the root chown never escaped onto host files.

That fence was where almost all the historical back-and-forth went. **It existed
only because the image was built without the UID it would run as — even though
`byre develop` already knows that UID at build time** (it builds and runs as the
same user, in the same invocation, on the same host).

This milestone removed the runtime reconciliation entirely by **baking the host
UID/GID into the image at build time**. `/home/dev` is now born owned by the
runtime user, and lazily-created volumes inherit that ownership. No runtime
chown, no fence, and **no root or `gosu` at runtime** — the container runs as the
baked user from PID 1. (`gosu` stays installed as a build-only helper; see "What
gets deleted".)

Scope: this is the **rootful Docker / rootful Podman** fix. **Rootless Podman** is
handled by a decided, separately-sequenced `keep-id` path (see "Rootless Podman").

---

## Background — why this was hard, and the actual root cause

A bind mount shares raw UID *numbers* between host and container; whoever writes
a file stamps it with the writing process's UID. So "make the agent's files look
right on the host" = "make the agent write as the host UID." Three sub-surfaces:

- `/workspace` — the project bind mount. These are the **host's** files, already
  owned by the host UID, and the agent runs as the host UID. **Never an issue;
  no chown.**
- `/home/dev` — the agent's home. **Image-layer content**, born owned by the
  build-time UID (1000), reset to that state in every fresh container.
- **Named volumes** — cache (`node_modules`) and unseeded state volumes are
  lazily created by the engine on first run and **inherit the ownership of the
  image directory** at their mount point (the build UID). Seeded state volumes
  are already created the clean way (see below).

The runtime chown exists purely to repair the **build-UID (1000) vs runtime-UID
(e.g. 501)** mismatch on the latter two. That mismatch is self-inflicted: byre
chose to keep the image generic. The constraint that justified "generic image"
— portability/distribution of the image across UIDs — was never examined against
the fact that we know the UID at build, **and** is a property the volumes never
had anyway (a volume's contents are owned by whoever seeded/created it, so volumes
are already host-specific; `rehome`/`MigrateVolume` exists precisely to re-chown
them when moved). So the image was kept portable to defend a property the system
doesn't have end-to-end.

The escape hatch we investigated — **idmapped mounts** (translate UID at mount
time, no chown) — turns out not to apply: **Docker has no idmapped-mount feature
at any version** (it's Podman-only, and rootful-only). So for byre's primary
engine the chown is the only mechanism — which means it's worth deleting the
*need* for it, not hardening it.

---

## The decision

**Build the image owned by the UID/GID byre will run as.** Concretely:

1. byre already computes `os.Getuid()` / `os.Getgid()` at `develop` time
   (`internal/commands/commands.go`). Pass them into the build as `--build-arg
   BYRE_UID=… BYRE_GID=…`.
2. In the generated Dockerfile (`internal/gen/gen.go`, infra layer): create/adjust
   the `dev` user to that UID/GID, and `chown` `/home/dev` **and** every named-volume
   mount-point dir to it (today it chowns to `dev:dev` = build default).
3. The image tag includes the UID/GID (see "Image identity" below), so a
   UID-specific image is never reused for a different user on a shared daemon.
4. At runtime the container starts **as the baked user** (`USER`), already owning
   its home; fresh volumes inherit the baked ownership from the image dirs. The
   launcher no longer chowns anything.

This trades one property — "one image is valid for any UID" — for deleting the
entire runtime-chown subsystem. We give up portability the volumes never had, on
an artifact (`/home/dev`) that is rebuilt per host anyway.

### Why build-time UID is safe here

`byre develop` **builds and runs in the same invocation, as the same user, on the
same host**, so build-UID == run-UID by construction. This holds even under
"varied hosts I don't control," because each host builds its *own* image as its
*own* invoking user. The only divergence cases are:

- **`sudo byre` / build-as-one-user-run-as-another wrappers** — out of scope; document as unsupported.
- **`rehome` to a host with a different UID** — already handled by
  `MigrateVolume`'s `-u 0:0` `chown -R` to the new UID; unaffected by this change.
- **Raw `run_args` overriding identity — documented, no code.** `run_args` is
  appended last-wins (`internal/runner/runargs.go:38,71`), so a config/skill can
  set `--user`/`--userns` and break baked ownership with no runtime repair. This is
  a footgun only the config/skill author can pull (the same person who owns the
  raw escape hatch), so byre does **not** detect or warn — it's a one-sentence
  caveat in the spec under the existing "raw `run_args` = you own the infra layer"
  contract. Same for a `run_args`-added `--mount type=volume`: opaque to byre, not
  build-time-chowned, the author owns its ownership.

> **Verification (not an open question).** Within byre's own flow `develop` builds
> and runs in one invocation as `os.Getuid()`, so build-UID == run-UID. A
> sequencing step confirms there is no internal build-as-one-user / run-as-another
> path. The only divergences are the `run_args` case above (warned) and
> `sudo`/CI-prebuild (out of scope; documented as unsupported).

---

## What gets deleted

From `internal/gen/launcher.sh`:

- `reown_storage`, `chown_tree`, `needs_chown` — the entire recursive-chown +
  mount-pruning fence.
- The `/proc/mounts` parsing, `find -xdev` walk, `chown -h`, and the
  `BYRE_VOLUME_DIRS` / `volume-dirs` machinery feeding it.
- The runtime passwd/group append (the user now exists in the image).
- The **runtime root phase + `gosu` drop**: the image `USER`s the baked user and
  the launcher runs as that user start to finish — **no root, no `gosu` at
  runtime**. This is the core win.

  > **Decided: `gosu` is removed from the runtime path but stays installed as a
  > build-only helper.** Skill *build* steps install agent CLIs as the dev user via
  > `gosu dev` (`internal/builtins/skills/claude/skill.toml:23`,
  > `internal/builtins/skills/codex/skill.toml:49`), and a build legitimately mixes
  > root steps (`apt`, `ln` into `/usr/local/bin`) with dev steps — `gosu dev` in a
  > `RUN` is the clean idiom for that. So the infra layer keeps installing `gosu`
  > for build; only the launcher's runtime drop is deleted. Fully excising `gosu`
  > would mean reworking the skill build-step contract (`USER` toggling) for **zero
  > runtime benefit** — explicitly not doing that.

From `internal/build/context.go` / `internal/gen/gen.go`:

- The `volume-dirs` context file and the build-time list plumbing **iff** it's
  only used to feed the runtime reown. (It also drives build-time `mkdir`/`chown`
  of mount points — that part **stays**, now chowning to the baked UID.)

Supporting test fixtures that pin the deleted behavior (launcher reown tests,
golden Dockerfile bytes) — see "Tests."

> **Resolved — the entrypoint runs fully unprivileged; `gosu` is gone from the
> runtime path (retained build-only, above).**
> Audited everything the launcher does as root beyond the chown:
> - `git config --global safe.directory` and agent-context/memory placement —
>   already done as the user.
> - First-run hooks (`/etc/byre/firstrun.d/*`) — both built-ins
>   (`codex/codex-login.sh`, `devloop/devloop-firstrun.sh`) run as root **only to
>   immediately `gosu` back down to the user** for all real work (codex login
>   writes the user-owned `.codex` volume; devloop writes `/workspace`). Neither
>   needs root. With an unprivileged entrypoint they drop the `gosu` wrapper and
>   run directly as the user.
>
> So nothing in the entrypoint needs root once baking removes the chown. **Two
> consequences to record, not blockers:**
> 1. **Firstrun-hook contract change.** "Hooks run as root with `BYRE_USER` set"
>    becomes "hooks run as the user." Built-ins are fine; this removes the
>    *ability* for a future/third-party skill hook to do privileged setup
>    (e.g. align to a runtime-mounted `docker.sock` GID, set `iptables` egress
>    rules, install a corporate CA). If that's ever needed, add it back as an
>    **explicit, status-visible per-skill grant** (a declared root one-shot, like
>    the seed path) — not a blanket "all hooks are root."
> 2. **No runtime repair.** Removing the root phase removes the safety net that
>    could chown a stray root-owned mount at launch. Build-time ownership becomes
>    load-bearing: **every** named-volume mount-point must be build-time chowned to
>    the baked UID, and a volume mounted where no baked image dir exists would come
>    up root-owned and be permanently unwritable. Verify the volume-dir enumeration
>    stays exhaustive (it already feeds the build-time `mkdir`/`chown`).

---

## What changes (and what stays)

- **`gen.go` infra layer:** `useradd`/`usermod` to the baked UID/GID; `chown
  $BYRE_UID:$BYRE_GID` for `/home/dev` and the volume mount-point dirs.
  (`/workspace` is just a `mkdir`'d mountpoint — the host bind masks the image
  placeholder at runtime, so its image-time ownership is irrelevant; **don't chown
  it**.) Handle low UIDs (e.g. 501) without `useradd`'s `UID_MIN` warning — reuse
  the direct passwd/group append technique the launcher uses today, or pass the
  appropriate `useradd` flags. Keep Dockerfile **byte-stability**: inject the UID
  via `ARG` so the template text is constant and only the build-arg value varies
  (the golden test asserts on template text, not the resolved arg).
- **Build plumbing (reviewer):** `Runner.Build` (`internal/runner/runner.go:99`)
  has no build-arg parameter today. Extend it to pass `--build-arg
  BYRE_UID=… BYRE_GID=…`, and route it through **both** build paths — `develop`
  and `rebuild` both go via `buildImage` — so every build path passes the same
  UID/GID. Add command/runner tests asserting the args are passed on each path.
- **Seeded state volumes:** unchanged — already created the right way
  (`VolumeCreate` + `-u 0:0` one-shot `cp` + `chown -R` to host UID in
  `runner.SeedVolume`/`SeedLiteral`). This milestone makes the *rest* of the
  volumes match that model implicitly (via inherited build-time ownership) rather
  than via a launcher walk.
- **`rehome` / `MigrateVolume`:** unchanged — still the mechanism for moving
  existing volumes onto a host with a different UID.
- **`/workspace`:** just a mountpoint — the host bind masks the image's placeholder
  at runtime, so byre never chowns it (build-time *or* runtime); the host files are
  already owned by the host UID the agent runs as.

---

## Image identity (cache safety)

Baking the UID makes the image a function of `(base + skills + config + UID/GID)`,
so the **image tag must include the UID/GID** — otherwise two users at the same
project path on one daemon could reuse each other's wrong-UID image from the
shared build cache.

**Tag only — do NOT UID-qualify volume names (reviewer).** Volumes are named
`byre-<id>-<name>` with `<id>` derived from the project path
(`internal/commands/commands.go:381`). UID-qualifying them would make normal
develop/seed/run look for a *different* name and silently create fresh state
volumes — catastrophic for agent auth state — unless paired with an explicit
old→new rename migration. The shared-daemon-same-path collision that uid-qualified
volumes would solve is an extreme pre-existing edge, not introduced here. So leave
volume/container names alone; qualify only the image tag.

**The tag shape is derived in several places — all must change together
(reviewer):** `develop` (`commands.go`), `rebuild` (`rebuild.go:32`), `forget`
(`forget.go:58`), and `rehome`'s copy-image lookup (`rehome.go:126`) each hard-code
`byre-<id>`. Miss one and: `rebuild` targets the wrong image, `forget` leaks the
UID-qualified image, `rehome` can't find its copy image. Centralize the tag
derivation so there's one source of truth.

---

## Migration / upgrade path

**Decided: no automatic migration code.** byre is a single-operator pre-release
tool; an automatic reconcile (a `ChownVolume` one-shot + a per-project marker +
reconcile-on-develop + tests) is machinery for a fleet we don't have.

In practice the upgrade is a **no-op for existing installs**: the old launcher
already chowned volumes to the *host* UID at runtime, so existing volumes are
already owned by the UID you run as — which is exactly the UID now baked into the
image. The only volumes that could come up wrong-owned are ones that never
launched, or a box you now run as a *different* user than before.

- **`byre skill update` is REQUIRED on upgrade (release note).** The firstrun
  hooks changed — they no longer wrap work in `gosu` (the entrypoint is now
  unprivileged). But `MaterializeSkills` never overwrites an already-materialized
  skill, and it's the *host* copy under `~/.byre/skills/` that gets COPY'd into
  the image — so a new binary alone ships the STALE hooks. A stale
  `codex-login.sh`/`devloop-firstrun.sh` still calls `gosu`, which now fails
  (`failed switching to "1000:1000": operation not permitted`) because a non-root
  process can't switch users. The box still launches (hooks are best-effort) but
  codex auth won't fire. Fix: `byre skill update` on the host, then `byre
  develop`. (Same materialize caveat that bit the `[agent.creds]` removal.)
- **Recovery (release note, not code):** if an upgraded box won't start because a
  pre-existing state volume is wrong-owned, `byre reset` it and re-log-in. The
  only state at risk is agent auth (`.claude`/`.codex` — a 30-second device-auth /
  login) and `node_modules` (rebuilds). With the unprivileged entrypoint the
  user-mode firstrun hooks can't repair a wrong-UID volume themselves, so `reset`
  is the supported path.
- **Existing images:** rebuilt under the UID-qualified tag, so old images are
  orphaned, not reused. Note in release notes.

---

## Rootless Podman (decided design, separately sequenced)

Rootless Podman runs the engine as an unprivileged host user and remaps UIDs
through a user namespace: "the UID inside" != "the UID on disk." Baking does **not**
transfer cleanly, because the build itself runs in the userns. The proposed fix is
the standard rootless pattern:

- Keep a **generic** image (fixed `USER`, e.g. UID 1000) for the rootless path —
  *not* the baked-UID image.
- Run with **`--userns=keep-id:uid=<image-uid>,gid=<image-gid>`** so the host user
  maps to the image's user inside the namespace; files then land on disk owned by
  the host UID. Plain `keep-id` only lines up when the host UID already equals the
  image UID — the explicit `:uid=,:gid=` form forces alignment, which we need.
- No runtime chown and no in-container root required (consistent with the rootful
  direction): be born the right UID and own what you create.

**Decisions:**

1. **Two build modes, selected by engine detection — not one image.** The
   baked-UID image and the keep-id model need different build-time ownership, so
   rootful bakes the host UID while rootless builds the **generic-UID (1000)**
   image. Mode is chosen at `develop` from `runner.IsRootlessPodman()` (today it
   only warns — it becomes the mode selector).
2. **keep-id alignment is explicit:** run with `--userns=keep-id:uid=1000,gid=1000`
   to force the host user onto the generic image's UID.
3. **keep-id unavailable → fall back to detect-and-warn.** On Podman versions
   without working `keep-id`, byre keeps today's "rootless unsupported" warning
   rather than producing wrong ownership. That's the floor.

Sequenced as a **later phase** (after the rootful path lands); the design above is
settled, not open. Until then the existing detect-and-warn stays, its text updated
to point at this plan.

---

## Docs & comments to fix

Exhaustive checklist from a full-repo sweep. All describe the soon-to-be-removed
runtime-chown / UID-agnostic-image / `gosu` behavior unless noted.

### Prose docs
- [x] **`docs/byre-spec-v0.md`** (heaviest concentration):
  - L39 — changelog "rootless Podman vs the UID/GID plumbing … open question."
  - L56-59 — Thesis: "UID/GID passthrough, drop to non-root …" in the core-owns-plumbing list.
  - L168-172 — layer diagram: `<byre infra layer> # constant: gosu, non-root dev user, launcher`.
  - L174-188 — infra-before-skills rationale ("the `dev` user and `gosu` exist … `gosu dev` … `/home/dev/.local/bin`").
  - L216-221 — Container engine rootless caveat ("maps the host UID/GID and drops to non-root via gosu, assumes a rootful daemon").
  - L323-329 — full-Dockerfile opt-out ("you own … host UID/GID mapping, gosu drop-to-non-root, the launcher ENTRYPOINT").
  - L444-467 — **"## Plumbing"** canonical description ("Map host UID/GID …; fix ownership on volumes + home. Drop … via `gosu`; agent never runs as root"). Rewrite to build-time-UID + unprivileged entrypoint.
  - L546-552 — **"## Platform note"** ("UID/GID passthrough … test the `id -u`/`gosu` mapping").
  - L575-579 — Open questions → **Rootless Podman**: replace "don't market rootless until designed" with the keep-id plan (this doc).
  - Open questions → **Image distribution**: reframe — the "generic image" constraint it implied is resolved (we bake UID), not aspirational.
- [x] **`README.md`**: L152-156 "## Platform" (UID/GID passthrough yields correctly-owned files); L138-149 "Volumes & state" (dev-owned / seeding); L129-134 "Security contract" (rests on correctly-owned claim).
- [x] **`CLAUDE.md`**: L59-61 — golden-Dockerfile determinism note (test moves, not removed; ARG keeps it byte-stable).
- [x] **`docs/self-host.md`**: L23 ("non-root `dev` user mapped to your host UID/GID"); L53-57 (Claude CLI installed "as the `dev` user via `gosu dev`" — build-time gosu usage).
- [x] **`docs/agent-volume-sharing.md`**: L104-105 (seed-time `chown` / `SeedFiles` rollback) — still accurate (seed path stays), just confirm wording.
- [x] **`docs/positioning-discussion.md`**: L6, L113 — **markets "rootless"** ("no-account, no-daemon, rootless local agent harness"). Reconcile with the keep-id plan; don't over-claim until built.
- **`site/` devlog / blog — OUT OF SCOPE. Do not touch.** `site/day-02.md` and the
  other devlog entries are point-in-time narrative (they already discuss both the
  old chown and the new build-time conclusion). They are deliberately left as
  written and must **not** be edited as part of this milestone.

### Code comments
- [x] **`internal/gen/launcher.sh`** — entire reown/fence comment apparatus goes with the code (header L1-6; `needs_chown` L20-25; `chown_tree` L27-56 incl. KNOWN LIMITATIONS; `reown_storage` L58-84; passwd/group append L91-100; `reown_storage` call + `git config` via gosu L102-106; firstrun-as-root L150-154; `exec gosu` L167).
- [x] **`internal/gen/gen.go`** — `infraLayer` L73-87 (the `useradd … || true`, "no fixed `--uid` … launcher remaps at runtime" comment L74-80 is now **false**); `Input.VolumeDirs` doc L42-44 ("pre-create dev-owned … launcher re-owns"); `VolumeDirsName` L62-65; volume-mount-point block L142-160 ("bake the list so the launcher can re-own"); `SortedUnique` doc L210-213.
- [x] **`internal/build/context.go`** — L87-94, L115-116 ("so the launcher can re-own them … fresh volume inherits the image dir's ownership"). The mechanism stays; reword from "launcher re-owns" to "born owned via build-time chown."
- [x] **`internal/commands/commands.go`** — `runParams` BYRE_UID/GID injection L309-316 ("the launcher consumes them"); `rootlessPodmanWarning` L488-490; `warnRootlessPodman` L492-503; `byre shell` uid resolution L222, L270-276 (reads container BYRE_UID/GID — still valid, reword).
- [x] **`internal/runner/runner.go`** — `IsRootlessPodman` L77-95 (long "correctly-owned-files trick … assumes ROOTFUL" comment); `SeedVolume`/`SeedLiteral`/`SeedFiles`/`MigrateVolume` L213-307 ("fresh volume is root-owned … runs as root" — these stay as develop-time one-shots; reword to clarify they are NOT the entrypoint).
- [x] **`internal/config/config.go`** — L238-240 full-Dockerfile opt-out comment ("user owns infra layer; byre owns runtime").
- [x] **`internal/builtins/templates/node/template.config`** — L8 "starts empty + dev-owned" → baked-UID-owned.
- [x] **Firstrun hooks** — `devloop/devloop-firstrun.sh` (L2 "as root", L6-7, L10 gosu) and `codex/codex-login.sh` (L2, L19, L27, L44 gosu): drop the `gosu` wrappers; update headers to "runs as the user."

### Help text / output strings
The user-facing strings still say rootless ownership is **unsupported, use
rootful** — which is the honest, actionable message *until* the keep-id path is
actually built (pointing users at an unbuilt plan would be worse UX). So these
are intentionally left as-is and tied to the rootless follow-up; only the
internal rationale comment was updated to bake-at-build.
- [ ] `commands.go` — `rootlessPodmanWarning` printed text: rewrite to point at the keep-id plan **when that path ships** (the const's rationale already references it; the user-facing text still says "unsupported").
- [ ] `status.go` — `Rootless` field + Engine row `"(rootless — file ownership UNSUPPORTED in v0; use rootful)"`: update when keep-id lands.

---

## Tests to update

**Delete** (test only the removed reown fence):
- [x] `internal/gen/launcher_reown_test.go` — entire file. `TestLauncherReownStorage`
  (sources the launcher via `BYRE_LAUNCH_TEST=1`, stubs chown/stat, fakes
  `/proc/mounts`; asserts own-storage chowned, host binds never chowned, `-h` used)
  and `TestLauncherReownStorageIdempotentExitZero`. The functions are gone.

**Rewrite** (assert the new build-time behavior):
- [x] `internal/gen/gen_test.go` — `TestDockerfileGolden` (regen; infra layer still
  installs `gosu` for build, now also bakes UID via constant `ARG` +
  `chown $BYRE_UID:$BYRE_GID` and sets `USER` to the baked user; the launcher loses
  its root/`gosu` phase); `TestDockerfileCanonicalOrder` (infra still precedes
  skills so `gosu`/the dev user exist for skill builds — premise stays);
  `TestDockerfileVolumeDirsDevOwned` → assert chown to the baked UID;
  `TestDockerfileNoVolumeDirsSection`, `TestDockerfileVolumeDirInjectionQuoted`.
- [x] `internal/build/context_test.go` — `TestAssembleWritesDockerfileAndLauncher`
  (launcher shrinks/loses root phase); `TestAssembleVolumeDirsDevOwned` (chown
  target changes); the volume-dirs COPY/content assertions L255-292.
- [x] `internal/commands/status_test.go` — `TestRenderStatusRootlessPodman` (string
  changes once keep-id lands).
- [x] `internal/commands/commands_test.go` — `TestWarnRootlessPodman` / `fakeRootless`
  (warning text changes to point at the keep-id plan).

**Keep** (seed/rehome paths are unchanged by this milestone):
- [x] `internal/commands/seed_test.go` (`fakeSeeder`, `TestSeedVolumes*`) and
  `internal/commands/rehome_test.go` (`fakeRehome.MigrateVolume`) — still valid;
  these are the develop-time `-u 0:0` one-shots that stay.
- [x] `internal/runner/runargs_test.go` (`BYRE_UID`/`GID` env, `ContainerEnv`) —
  the env still flows (now consumed at build + by `byre shell`), so keep but
  re-verify intent.
- [x] `internal/runner/runner_test.go` `TestIsRootlessPodman` — keep (detection
  stays; its consumer changes from warn → mode-select).

**Add** (deferred — need a Docker host, so they're written/run host-side, not from the dev box):
- [ ] Gated integration test (`BYRE_DOCKER_TESTS=1`): a fresh `develop` produces
  host-UID-owned files in `/home/dev`, a fresh cache volume, and `/workspace`,
  with **no** root phase / `chown` / `gosu` in the launch path.
- [ ] `internal/builtins` — assert a fresh volume comes up owned by the baked UID.
- [ ] Rootless-Podman keep-id integration coverage (when that path is built).

The unit layer already pins the build-time behavior (golden Dockerfile + the
volume-dir chown-to-baked-UID assertions in `gen_test.go`/`context_test.go`); the
above are end-to-end confirmations that belong to a host-side run.

---

## Sequencing

1. Confirm in code that build-UID == run-UID across byre's `develop` path (the
   entrypoint-unprivileged and firstrun-hook questions are already resolved above).
2. `gen.go`: bake UID/GID (build-arg + chown image dirs + user creation; keep the
   build-only `gosu`).
3. Image identity: UID-qualify the image tag via one centralized derivation
   (`develop`/`rebuild`/`forget`/`rehome`). Volume names unchanged.
4. Strip the launcher: remove `reown_storage`/`chown_tree`/the fence, the
   passwd/group append, and the root phase. The entrypoint runs as the baked user
   (`gosu` retained build-only).
5. Docs + comments + help-text sweep (checklist below); release note: `byre reset`
   + re-login if an upgraded box won't start.
6. Tests: delete reown tests, regen golden, add integration coverage.
7. `byre-codereview` pass; iterate to clean.
8. Rootless Podman: separate follow-up implementing the keep-id path.

---

## Decisions (resolved)

Every prior open item was resolved and the rootful path is implemented; the only
remaining work is the sequenced rootless Podman phase.

- **Build-time `gosu`** — **kept as a build-only helper, deleted from the runtime
  path.** Not excising it from the build (no runtime benefit; would churn the skill
  build-step contract).
- **Identity-changing `run_args` (`--user`/`--userns`)** — **documented caveat, no
  code** ("you own the infra layer"). Author-only footgun.
- **`run_args`-added volumes (`--mount type=volume`/`-v`)** — **not byre-managed**:
  no build-time chown, not in `reset`/`rehome`; documented as the author's
  responsibility. Structured config/skill volumes stay fully managed.
- **Pre-existing-volume migration** — **none**. Single-operator tool; the upgrade
  is a no-op in practice (old launcher already chowned to the host UID). Recovery
  is `byre reset` + re-login, documented in release notes.
- **Image tag** — UID-qualified, **derivation centralized** across
  `develop`/`rebuild`/`forget`/`rehome`. Volume names unchanged.
- **Rootless Podman** — **two-mode build + `keep-id:uid=,gid=`**, fall back to
  detect-and-warn where keep-id is unavailable. Sequenced as a later phase.
- **Out-of-band build/run UID divergence** (`sudo`, CI prebuild) — **out of scope**,
  documented as unsupported.

---

## External review

Reviewed by the Codex-backed `byre-codereview` (design-review mode, blog/`site/`
explicitly out of scope). Six findings, all incorporated above:

1. `gosu` is used at **build time** by skill installs — can't leave the image
   wholesale → **decided: kept build-only, deleted from runtime** ("What gets deleted").
2. `run_args --user`/`--userns` can override identity → **decided: documented
   caveat, no code** (author-only footgun).
3. `run_args --mount type=volume` is opaque to byre → **decided: not byre-managed,
   documented**; structured volumes stay managed.
4. UID-qualifying volume names risks silently orphaning state volumes → **don't**;
   qualify the image tag only.
5. Tag shape is derived in `develop`/`rebuild`/`forget`/`rehome` → centralize.
6. `Runner.Build` has no build-arg param → extend it; route through both build
   paths with tests.

Confirmed by the reviewer: the firstrun hooks don't need root once ownership is
correct, and the empty-volume inheritance argument holds **for structured byre
volumes** — both contingent on the build-time-ownership and run-args caveats above.
