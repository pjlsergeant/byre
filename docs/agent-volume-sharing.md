# Design note: worktree volume inheritance

Status: **proposal** (not built). Captures the direction agreed on 2026-06-23.
Supersedes an earlier draft that proposed a per-repo volume *scope* and a
creds/history split — both dropped (see "What we ruled out").

## The problem

byre identity is **per canonical path**: a project at `/p` gets id
`<slug>-<hash(/p)>`, and everything keys off it — config lookup, volume names,
image tag, container name + single-session lock. A git **worktree** of a repo is
a *different path*, so it's a *different* byre project: different volumes, so you
**re-authenticate the agent (and lose caches) in every worktree.** We want a
worktree to inherit the parent repo's setup instead.

## The mechanism: resolve identity from the parent worktree

A linked worktree is unambiguously detectable, no config:
`git rev-parse --git-dir` (→ `<common>/worktrees/<name>`) differs from
`--git-common-dir` (→ the main `.git`). On detecting one, byre resolves identity
from the **main worktree's path** — *except* the things that must stay local.
The project id drives several roles; they split cleanly:

| keyed on id | for a worktree, resolve from… | why |
|---|---|---|
| config lookup | **parent** | inherit the repo's byre.config — no re-onboarding |
| volume names | **parent** | inherit `.claude`/`.codex`/caches — the whole point |
| image tag | **parent** | same config ⇒ same image |
| container name + single-session lock | **worktree** | else two worktrees can't run at once |
| `/workspace` mount | **worktree** | else you'd edit the *main* tree's files, not the worktree's |

So: **config + volumes + image from the parent; container + workspace from the
worktree.** With an opt-out (a flag / config key) to force a standalone identity.

## Why there's no concurrency problem (and so no creds/history split)

Two worktrees of one repo can run agents at the same time, both mounting the
shared state volume. That is **fine** — it's identical to running several
`claude`/`codex` processes on one host against one `~/.claude`, which already
works: the agents handle concurrent access to their own state dir (per-session
files, sqlite WAL, atomic writes, a single credential file).

This is the opposite of the codex breakage we hit earlier, which was caused by
**two separate COPIES** of a rotating token each independently rotating ("refresh
token already used"). *Copying* to two places breaks; *sharing one volume* does
not. So no creds-vs-history split is needed — the writable state can be shared
wholesale.

## The one real prerequisite: mount the common git dir

Independent of volumes: a linked worktree's `.git` is a *file* pointing at the
main repo's common git dir, where objects/refs live (outside the worktree). If
byre mounts only the worktree at `/workspace`, git is broken in the box (no
objects, can't commit). So worktree support must also mount the repo's common git
dir (read-write — commits write objects there).

## Scope of the work

1. **Identity resolution** — in project resolution, detect a linked worktree and
   resolve config/volumes/image id from the main worktree's path, with an
   opt-out. Keep container name + `/workspace` per-worktree.
2. **Common-git-dir mount** — add it when running in a worktree.
3. **Legibility** — `byre status` names it ("worktree of `<repo>`; inheriting its
   volumes").

## What we ruled out (so we don't relitigate)

- **Machine-wide `shared` volumes** — built in P5, then dropped as a user knob;
  the whole scope mechanism was later removed. Sharing across *unrelated*
  projects has no natural boundary.
- **A per-repo volume *scope* tier** — superseded by parent-path identity
  resolution, which gives volume inheritance without a new scope dimension.
- **The creds/history split** (nested mounts / dir-symlinks to share creds but
  isolate history) — unnecessary: the agents already handle concurrent shared
  state, so the whole state volume can be shared.

## Open questions

- **Opt-out shape** — config key vs `develop` flag for forcing standalone
  identity in a worktree.
- **Family id stability** — `git-common-dir` moves if the main worktree moves;
  how does that interact with `rehome`?
- **Lifecycle** — `byre reset`/`forget` run from a worktree act on the *parent's*
  volumes (shared). That's correct (they're one project's volumes) but should be
  loud about affecting all worktrees.

## Related but separate: config/prefs seeding — SHIPPED

Adjacent to (and independent of) this feature, now built: copying non-secret,
non-history host prefs (theme, keybindings) into a fresh project state volume —
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
