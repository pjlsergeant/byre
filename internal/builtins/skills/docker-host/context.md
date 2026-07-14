# Host Docker daemon (byre docker-host skill)

You drive the **host's** Docker daemon via `/var/run/docker.sock`, not a
private one. What you create (images, containers, volumes, networks) is
**host state that outlives this box** -- clean up what you create.

## Do not touch what you did not create

Containers AND volumes visible to you -- including other byre boxes (one is
YOU) and other projects' state/identity volumes -- belong to the user.

- Do not stop, remove, or prune anything you did not create.
- Do not mount foreign volumes. Mounting `byre-machine-*` or another
  project's state volume exfiltrates credentials -- same weight as prune.
- `docker system prune` is off-limits.

## Compose

Your compose project name is preset via `COMPOSE_PROJECT_NAME`
(`byre-$BYRE_WORKTREE`). Do not override it: unrelated byre boxes share the
daemon and a bare name collides. Label what you create so it is attributable
and cleanable.

## Network

The box's network rules (firewall, if any) do **not** apply to containers
you launch. Daemon-side pulls and `--network host` ride the host's network.
Security discussion: `docs/DOCKER-HOST.md`.

If you need to run containers on a *different* host, point your own docker
context / `DOCKER_HOST` at it (subject to opening egress to that host under a
firewall) -- byre mounts the local socket, but nothing stops you driving a
remote daemon yourself.
