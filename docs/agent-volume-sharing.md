# Design note: worktree volume inheritance

Status: **decided 2026-07-03** (grilling session; decisions below) -- not yet
built. Supersedes an earlier draft that proposed a per-repo volume *scope* and
a creds/history split -- both dropped (see "What we ruled out").

## The problem

byre identity is **per canonical path**: a project at `/p` gets id
`<slug>-<hash(/p)>`, and everything keys off it -- config lookup, volume names,
image tag, container name + single-session lock. A git **worktree** of a repo is
a *different path*, so it's a *different* byre project: different volumes, so you
**re-authenticate the agent (and lose caches) in every worktree.** We want a
worktree to inherit the parent repo's setup instead -- and to be able to run a
byre session in the main worktree and a linked worktree **at the same time**
(that simultaneity is the headline user desire).

## The mechanism: resolve identity from the parent worktree

A linked worktree is unambiguously detectable, no config:
`git rev-parse --git-dir` (→ `<common>/worktrees/<name>`) differs from
`--git-common-dir` (→ the main `.git`). On detecting one, byre resolves identity
from the **main worktree's path** -- *except* the things that must stay local.
The project id drives several roles; they split cleanly:

| keyed on id | for a worktree, resolve from… | why |
|---|---|---|
| config lookup | **family** | inherit the repo's byre.config -- no re-onboarding |
| volume names | **family** | inherit `.claude`/`.codex`/caches -- the whole point |
| image tag | **family** | same config ⇒ same image |
| setup lock (`LockFile`) | **family** | serialize generate+build+seed across worktrees |
| container name | **worktree** | else two worktrees can't run at once |
| `/workspace` mount | **worktree** | else you'd edit the *main* tree's files |

So: **config + volumes + image + setup lock from the family; container +
workspace from the worktree.** No opt-out (see decisions).

## Why there's no concurrency problem (and so no creds/history split)

Two worktrees of one repo can run agents at the same time, both mounting the
shared state volume. That is **fine** -- it's identical to running several
`claude`/`codex` processes on one host against one `~/.claude`, which already
works: the agents handle concurrent access to their own state dir (per-session
files, sqlite WAL, atomic writes, a single credential file).

This is the opposite of the codex breakage we hit earlier, which was caused by
**two separate COPIES** of a rotating token each independently rotating ("refresh
token already used"). *Copying* to two places breaks; *sharing one volume* does
not. So no creds-vs-history split is needed -- the writable state can be shared
wholesale. (This also retires the "per-agent dir-vs-file history layout" spike
an earlier draft called for -- that spike only mattered for the split.)

## Decisions (2026-07-03)

1. **Detection: parse git's files directly** -- no host `git` binary
   dependency (byre currently shells out only to docker/podman; keep it that
   way). A linked worktree's `.git` is a one-line pointer file
   `gitdir: <path>/worktrees/<name>`; that dir contains a `commondir` file
   pointing back at the main `.git`. Require **both** the
   `…/worktrees/<name>` path shape **and** a readable `commondir` file --
   this naturally excludes **submodules**, whose `.git` pointer files target
   `…/.git/modules/<name>` and have no `commondir`. Submodules stay
   standalone projects, as today.
   - **Bare-repo families** (`repo.git` + worktrees, no main working tree):
     the family anchor is the common git dir's *parent* when its basename is
     `.git`, else (bare) the common dir itself.
   - **Dangling metadata** (pointer parses but the common dir is missing,
     e.g. the main repo moved and `git worktree repair` hasn't run): **hard
     error**, never a silent fall-back to standalone identity -- silent
     fallback would quietly mint a second set of volumes, which is exactly
     the confusion this feature exists to kill.

2. **Identity model: one `Paths`, inheritance is the default.** In a
   worktree, `project.Resolve` returns `Paths` whose `ID` / `Canonical` /
   `Dir` / `ContextDir` / config-store path / `LockFile` all derive from the
   **family** (main worktree) canonical path. New fields carry the strictly
   local side: the worktree's own canonical path (feeds the `/workspace`
   bind, the container name and the per-worktree label) and a
   `WorktreeOf`-style marker for legibility. Rationale: the doc's table says
   most roles inherit; making inheritance the default means all ~12 existing
   `project.Resolve` call sites get config/volumes/image inheritance for
   free, and only the run/session paths touch the new fields. (The
   alternative -- resolving two `Paths` and picking fields per call site --
   makes every site a chance to key the wrong thing off the wrong id.)

3. **Host state: everything stays under the family dir**
   (`~/.byre/projects/<family-id>/`). The path record keeps recording the
   *main* path only (worktrees resolve to the family id, so `Bootstrap`'s
   collision check works unchanged). **No per-worktree host state is
   needed** -- see next item.

4. **Session model: names + labels, not host locks.** Discovery from the
   code: live sessions are tracked by Docker **label**
   (`RunningContainersByLabel`) and single-session atomicity is the
   container **name** (commands.go: "the container name makes single-session
   atomic"); the host `LockFile` only serializes setup (generate + build +
   seed) and is released before the session runs. Therefore:
   - **Container name** gains a worktree suffix (e.g.
     `byre-<family-id>` for the main tree, `byre-<family-id>-wt-<wt-slug-hash>`
     for a worktree). Name uniqueness = per-worktree single-session.
   - **Labels**: keep `byre.project=<family-id>` on *all* family containers
     (family-wide queries: reset/forget/status blast radius), and add a
     second per-worktree label (e.g. `byre.workdir=<wt-id>`).
   - ⚠️ **The trap that breaks the feature if missed**: `Develop`'s
     "already running" fast path and `Shell`'s session lookup query by the
     project label. With a shared family label, worktree B's develop would
     see worktree A's session and refuse to start. Both must filter on the
     **per-worktree** label (or name); `reset`/`forget` guards keep querying
     the **family** label (that widening is desired -- never wipe volumes
     under any worktree's live session, and name the offending worktree via
     its label).
   - **Build race** (two simultaneous `develop`s generating the same
     Dockerfile + building the same tag): already solved -- the setup lock
     inherits to family level with zero new code.

5. **No opt-out.** Worktree ⇒ inherit, always. YAGNI: byre identity is
   already per-repo-not-per-branch, so worktrees sharing config is
   consistent; anyone wanting isolation has a zero-code escape hatch --
   `git clone` locally instead of `git worktree add` (a full clone is not a
   linked worktree). Two-way door: an `inherit`/`standalone` key can be
   added later, backward-compatibly.

6. **`rehome`: refuse-with-pointer from a worktree** ("this is a worktree of
   `<main>`; run rehome there if the main repo moved"). Main-worktree
   `rehome` works as today; worktrees re-resolve the family path from git
   metadata each run, so they land on the rehomed id automatically once
   git's own pointers are repaired (`git worktree repair` is the user's
   job). A *moved worktree* needs nothing -- its path only feeds the
   container name/label, derived fresh each run.

7. **`reset`/`forget` from a worktree: allowed, loud.** Print the blast
   radius first ("this project is a worktree of `<main>`; volumes are
   shared -- this affects ALL worktrees of this repo") before the existing
   confirmation. The live-session guard sweeps the family label (see 4) and
   names the offending worktree.

8. **`status` legibility, both directions.** In a worktree: "worktree of
   `<main-path>` -- config, volumes, image inherited". In the main tree:
   list known live worktree sessions (falls out of the family-label query +
   per-container worktree label).

9. **Onboarding / `byre config` / adoption from a worktree: allowed**, writes
   to the family store, prefixed with the same family banner. Adoption reads
   the committed `byre.config` proposal from the *worktree's* checkout
   (possibly a different branch than main's) -- fine: config is family-wide
   by design and the human sees exactly what they adopt.

## The git-dir mount: same-path mounting (decided)

Independent of volumes: a linked worktree's `.git` is a *file* pointing at the
main repo's common git dir, where objects/refs live (outside the worktree). If
byre mounts only the worktree at `/workspace`, git is broken in the box (no
objects, can't commit).

**The trap**: git's worktree metadata is full of **absolute host paths** in
*both* directions -- the worktree's `.git` file points at
`<main>/.git/worktrees/<name>`, and the `worktrees/<name>/gitdir` back-pointer
holds the absolute host path of the worktree. Mounting the common dir at a
byre-chosen path (`/byre-git`) leaves pointers dangling, and "fixing" them is
radioactive: that metadata is shared rw with the host, so `git worktree repair`
in the box would write *container* paths into it and break git on the *host*.
A dangling back-pointer is also what makes `git worktree prune` in the box
silently delete the worktree's registration from shared metadata.

**Decision: same-path mounting.** Mount the main repo's `.git` at its
**host-absolute path** inside the box (rw -- commits write objects there), and
*also* bind the worktree at its host-absolute path (same source as the
`/workspace` bind). Every absolute pointer then resolves in both directions;
nothing is rewritten; the box physically cannot write a container path into
shared metadata. `/workspace` stays the primary workspace exactly as today.
Accepted consequence: the box sees (and can modify) the whole family's git
metadata -- refs, branches, stashes -- which is inherent to "commits from a
worktree write to the shared object store" and consistent with byre's trust
model (the agent already has rw on your code).

## Implementation map (code pointers, ordered)

1. **`internal/project`** -- detection + identity. `Canonicalize` both paths
   (family path must go through the same symlink-resolving canonicalization
   before hashing). New `Paths` fields per decision 2; `Resolve` does the
   `.git`-file walk. Unit tests with fake directory trees (worktree,
   submodule, bare, dangling, plain repo, non-repo).
2. **`internal/commands` naming/labels** -- container name + second label;
   fix `Develop`'s fast path and `Shell`'s lookup to filter per-worktree
   (decision 4's trap). `commands.go:117`, `commands.go:211`,
   `commands.go:483` area.
3. **Run mounts** -- in `runParams`/`runner.RunArgs`: the `/workspace` bind
   source becomes the *worktree* path; add the two same-path git mounts
   (common git dir + worktree at host paths, rw). Extend the existing
   comma-in-path `--mount` guard to the new paths. Run-time only -- no
   `internal/gen` change, so the Dockerfile golden test is untouched.
4. **Lifecycle + legibility** -- family banners in `reset`/`forget`
   (`reset.go`), guard messages naming the worktree, `rehome`
   refuse-with-pointer (`rehome.go`), `status` lines both directions
   (`status.go:109` area), onboarding/adoption banner (`commands.go`
   `adoptIfProposed`/`onboardIfNeeded` callers).
5. **Integration test** (gated, `BYRE_DOCKER_TESTS=1`, host-side): create a
   real repo + `git worktree add`, run develop in both, assert shared
   volumes/image, distinct containers, git works in the box (log/commit in
   the worktree).

Misc notes for the implementer:
- `resolveProjectFile(paths.Canonical, cfg.Dockerfile)` (hand-written
  Dockerfile opt-out) reads from the **family** tree once `Canonical` is the
  family path -- that's consistent with "config from family"; leave it.
- `forget.go:61` derives image/container names from `paths.ID` -- inherits
  family-wide behavior automatically; just add the banner + widen the
  container cleanup to worktree-suffixed names (or clean up by family label).
- `seedVolumes`/`seedPrefs` run under the (now family-level) setup lock and
  are gated on *fresh* volumes -- correct as-is for worktrees.
- Comma guard: `develop` validates `paths.Canonical`; validate the worktree
  path and git-dir paths too (all become `--mount` values).

## What we ruled out (so we don't relitigate)

- **Machine-wide `shared` volumes** -- built in P5, then dropped as a user knob;
  the whole scope mechanism was later removed. Sharing across *unrelated*
  projects has no natural boundary.
- **A per-repo volume *scope* tier** -- superseded by parent-path identity
  resolution, which gives volume inheritance without a new scope dimension.
- **The creds/history split** (nested mounts / dir-symlinks to share creds but
  isolate history) -- unnecessary: the agents already handle concurrent shared
  state, so the whole state volume can be shared. (And with it, the
  dir-vs-file history-layout spike.)
- **An inherit/standalone opt-out** -- YAGNI (decision 5); `git clone` is the
  escape hatch.
- **Per-worktree host lock files** -- sessions are tracked by container
  name/label, not host locks (decision 4); the only host lock is the family
  setup lock, which exists today.
- **Pointer surgery / `GIT_DIR` env for the git mount** -- see the mount
  section; same-path mounting is the only approach where git's invariants all
  hold and the box can't corrupt host metadata.

## Related but separate: config/prefs seeding -- SHIPPED

Adjacent to (and independent of) this feature, now built: copying non-secret,
non-history host prefs (theme, keybindings) into a fresh project state volume --
a one-time, opt-in, per-project **seed** (not sharing).

- **Opt-in:** `seed_prefs = true` in `byre.config` (`config.Config.SeedPrefs`).
- **Curated by the skill:** an `[agent.prefs]` block (`skills.PrefsSpec`) names a
  host `from` dir and the `files` (relative paths, files or dirs) to copy. The
  skill author VOUCHES each path is secret-free; the loader validates only the
  shape (relative, non-escaping, requires a state volume to land in).
- **Curation (the hard part):** only files structurally incapable of holding a
  secret are listed. For Claude that's `keybindings.json` + `themes/`;
  `settings.json` is excluded (it mixes theme with `env`/`apiKeyHelper`/`hooks`/
  `permissions`/MCP) and `~/.claude.json` is excluded (OAuth + MCP tokens + trust).
- **Mechanism:** host-side at `develop`, gated on a FRESH state volume (same path
  as `[volumes.seed]`), via `runner.SeedFiles` (bind-mount `from` read-only, copy
  only the curated paths as argv, chown). Missing host files are skipped; a failed
  seed rolls the volume back. See `commands.seedPrefs`.
