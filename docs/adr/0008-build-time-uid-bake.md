# Bake the host UID/GID at build time; no runtime chown, no root at runtime in a session

> **Amended by ADR 0059** (2026-10-02): "in a session" is the scope; the
> one-shot helper containers run as root and are ADR 0059's decision.

The generated image creates the `dev` user at the invoking host user's
UID/GID (`--build-arg BYRE_UID/BYRE_GID`), chowns `/home/dev` and the
volume mount points to it at build, and sets `USER dev` -- so the box runs
unprivileged as the host user from PID 1 and every file it writes is
correctly owned. This replaced (root-and-branch) a runtime mechanism:
a privileged recursive chown "fence" in the launcher that repaired the
build-UID(1000)-vs-run-UID mismatch, with mount-pruning machinery to keep
the root chown off host binds, plus a runtime `gosu` drop.

Safe because `byre develop` builds and runs in the same invocation as the
same user on the same host -- the run UID is known at build time. The
"generic, portable image" the runtime fence defended was a property byre
never needed: images build per host and are never shipped (volumes were
always host-specific anyway).

Consequences:

- The image tag is UID-qualified (`byre-<id>-u<uid>-g<gid>`), derived in
  one central place, so users on a shared daemon can't reuse each other's
  wrong-UID image. Volume names are deliberately NOT UID-qualified
  (renaming them would silently orphan agent auth state).
- `gosu` survives as a *build-only* helper (skills install CLIs as `dev`
  inside root build steps); nothing runs as root after PID 1 in a session.
  The one exception is byre's one-shot helper containers (volume seeding,
  backup's capture, restore's pour), which run as root in the box's userns
  and exit before any session starts (ADR 0059).
- First-run hooks run as the user -- a future skill needing privileged
  setup would need an explicit, status-visible grant, not root hooks.
- No automatic volume migration was built for the upgrade (single-operator
  tool; the old fence had already chowned volumes to the host UID, so
  upgrade is a no-op; recovery is `byre reset` + re-login).
- Rootless Podman remaps user namespaces and breaks the bake; that path
  builds a generic-UID image run with an explicit keep-id mapping
  (ADR 0032).
- Out of scope, documented unsupported: `sudo byre`, CI-prebuild-then-run
  (build-as-one-user-run-as-another), identity-changing `run_args`
  (`--user`/`--userns` -- author-owned footgun per PRINCIPLES.md #1).
