# docker-host: host Docker daemon access

The `docker-host` skill grants the box access to the **host's Docker
daemon** via `/var/run/docker.sock`. It is the ergonomic form of a grant
already expressible by hand (mount + CLI + group); the skill bundles that
with honest, loud legibility in `byre status`, the launch line, preset
apply's grant review, and the config UI.

This is **not** docker-in-docker (no nested daemon), and not a Podman host
skill (a Podman host would need a sibling skill with its own verified
facts).

## What the grant is

Socket access is **root-equivalent on the host** on native Linux:
`docker run -v /:/host` is a two-line takeover. That is the grant working
as designed, not a flaw.

**Platform nuance -- Docker Desktop** (macOS, Windows, and Docker Desktop
for Linux): the daemon and container root live in a **VM**. `-v /:/host`
exposes the VM's `/`, not the native host `/`. Native host files are
reachable only through Desktop's configured file sharing and keep native
permissions. Still a full compromise of Docker state, every byre volume,
and shared native paths -- but it is "VM root + Docker assets + shared
paths", not "your whole Mac."

## What survives vs what is gone

- **Accident-scale isolation of the box's own filesystem** remains: the
  agent still cannot see host paths you did not mount into *this* box.
- **Containment of host state and other boxes is gone.** Ordinary Docker
  mistakes (`docker compose down -v`, addressing a container by a
  plausible name, an unintended bind, removing a volume mid-cleanup) hit
  host state and other boxes with no adversary. Do not frame this as
  "accidents stay in the box" -- only the box's own filesystem does.

## Firewall interaction

Daemon-side actions (image pulls, `--network host`, sibling containers)
ride the **host's** network. The box's egress rules govern only the box's
netns. A sibling container started with `--network container:<box>` plus
`NET_ADMIN` can even flush the box's own firewall rules from inside its
netns. The Network status row still describes what byre built for the box
(and that remains true for the box); the Containment row disclaims the
hole.

## Cross-box / shared-auth volumes

`docker volume ls` plus mounting `byre-machine-u<uid>-*` reads other
projects' state and every shared-auth token on the machine **without
touching those boxes**. That weakens ADR 0025's per-box scoping
machine-wide. The agent context for this skill forbids mounting foreign
volumes; the skill does not enforce that -- it is guidance against the
accident class.

## Host state that outlives the box

Images, containers, volumes, and networks the agent creates on the host
daemon outlive the box. byre's teardown (`reset`, `forget`) knows nothing
of them. Clean up what you create; label it so it is attributable.

## When to grant it

- **Yes:** the agent's job *is* containers (building images, driving
  compose stacks, debugging against a real daemon).
- **Prefer not, when compose-deps alone are enough:** service sidecars
  (planned; see the project TODO) cover compose-deps **without** this
  grant. Prefer that path when the agent only needs dependencies, not
  daemon control.

## Compose project names

Every byre box has cwd `/workspace`, so compose's default project name
would collide across worktrees and projects. With `docker-host` enabled,
`COMPOSE_PROJECT_NAME` defaults to `byre-$BYRE_WORKTREE` (the per-worktree
id; equals the project id for a plain checkout). Do not override it
without reason.

## Warranty model

byre warrants its own **construction**, never the consequences of your
hole. The per-wall status rows (filesystem, network, privilege, ...) still
describe what byre built and hold **for the box**. The Containment line
disclaims everything done through the socket in one place -- including
attacks not yet enumerated.

See also: `docs/SECURITY.md` (daemon access is root-equivalent),
`docs/adr/0027-docker-host-daemon-access.md` (the design record), the
skill's agent context (`byre skill inspect docker-host`, or the display
mirror at `~/.byre/bundled/skills/docker-host/context.md`).
