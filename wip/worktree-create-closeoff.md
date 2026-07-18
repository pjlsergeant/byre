# Worktree creation — remaining closeoff

**Status:** Feature built (commits 9b1d622, df08b96, c6504ea) — creation now
runs in the box via `runner.WorktreeAdd`. Three items remain before it's done.
Delete this file once they're absorbed.

## 1. Add the test for the property the whole change delivers

The central claim of this refactor is *"every git side effect during creation
happens inside the box, within its mounted paths"*. The gated integration test
(`TestIntegrationWorktreeCreateInBox`) exercises the happy path — registration,
marker, ownership — but nothing exercises that isolation property directly. Add
a test that does, because it's the one that actually pins down what we changed.

The property is observable: git occasionally has to follow an **indirection**
in one of its metadata paths (a symlink where it expects a file or directory).
Running creation in the box means git resolves that indirection in the
*container's* namespace, so anything it points at outside the three mounts
(main tree / common git dir / target) simply isn't reachable — the write lands
inside the box, not at the host location the link names.

Shape of the test (gated `BYRE_DOCKER_TESTS=1`, since it needs a real box):

- In a scratch repo, turn one of the metadata paths git writes during a
  `worktree add` into a symlink pointing **outside** the mounted set. Two worth
  covering:
  - `.git/logs/refs/heads/<branch>` — git writes the new branch's creation-log
    entry here on a `-b`.
  - `.git/worktrees/<leaf>/` — the per-worktree admin dir git creates on every
    add.
- Put a small sentinel file at the link's host-side destination with known
  contents.
- Run `byre worktree <branch>`.
- Assert:
  - the host-side sentinel is **unchanged** — git resolved the link inside the
    container, so nothing reached that host path outside the mounts; and
  - the worktree still **registers correctly** and the marker is present — the
    real metadata writes landed in the mounted common dir, as normal.

This is the direct, observable confirmation of the isolation the change is for;
the happy-path test can't show it because it never puts an indirection in the
way. Keep the language of the test plain — it's a property/isolation test of
the creation flow.

## 2. `ensureProjectImage` builds without onboarding — decide if that's fine

`ensureProjectImage` resolves config and builds under the setup lock but does
not run onboarding (by design — it skips develop's session concerns). So the
first-ever `byre worktree` in a repo that has never been `byre develop`ed builds
an image from the resolved-default config, and then the hand-off to `Develop`
runs onboarding and may rebuild. Not broken — the second build is cheap and the
worktree is already registered by then — but decide whether that's acceptable as
is, or whether creation should share develop's onboarding first so there's one
build and one config decision. Low priority.

## 3. Confirm the mount ordering on both engines

`worktreeAddArgs` binds the main tree **before** the common git dir on purpose:
for a normal repo the common dir is `<main>/.git`, nested inside the main-tree
mount, and the deeper mount has to layer over the shallower one on engines that
apply mounts in argv order. That's an engine-behavior assumption the unit tests
can't prove. Make sure the gated run confirms `byre worktree` end-to-end on
**both** Docker and rootless Podman before calling this done.
