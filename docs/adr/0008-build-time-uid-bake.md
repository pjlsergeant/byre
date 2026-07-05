# Bake the host UID/GID at build time; no runtime chown, no root at runtime

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
  inside root build steps); nothing runs as root after PID 1.
- First-run hooks run as the user -- a future skill needing privileged
  setup would need an explicit, status-visible grant, not root hooks.
- No automatic volume migration was built for the upgrade (single-operator
  tool; the old fence had already chowned volumes to the host UID, so
  upgrade is a no-op; recovery is `byre reset` + re-login).
- Rootless Podman remaps user namespaces and breaks the bake. Decided,
  separately-sequenced follow-up: a generic-UID image on that path run
  with `--userns=keep-id:uid=<image-uid>,gid=<image-gid>`, falling back
  to detect-and-warn. Until built, rootless is detect-and-warn only.
- Out of scope, documented unsupported: `sudo byre`, CI-prebuild-then-run
  (build-as-one-user-run-as-another), identity-changing `run_args`
  (`--user`/`--userns` -- author-owned footgun per PRINCIPLES.md #1).
