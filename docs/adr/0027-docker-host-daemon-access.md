# docker-host: host Docker daemon access, with a warranty disclaimer

The `docker-host` skill grants a box access to the **host's** Docker
daemon via `/var/run/docker.sock`. It is the ergonomic form of a grant a
user could already assemble by hand (socket mount + docker CLI + group
membership); the skill's value is bundling that with honest, loud
legibility. Decided 2026-07-13: designed with the maintainer via
/grilling (three rounds of codex + grok design review), host-verified on
Docker Desktop and native Linux, built, and build-reviewed. This ADR
absorbs the design working-doc (deliver precedent); git history keeps it.

Not docker-in-docker (no nested daemon), not a Podman host (a Podman host
would need a sibling skill with its own verified facts), and not the
service-sidecars case (which covers compose-deps *without* daemon
access).

## The warranty model — the load-bearing decision

Socket access is root-equivalent on the host: `docker run -v /:/host` is a
two-line takeover. So the grant breaches **all five** of byre's isolation
walls at once — filesystem, network, privilege, cross-project, host
control — and does so *invisibly*, through the daemon, not through any
surface byre renders (the exposure line still says "1 host mount", the
Network row still says deny-by-default, other boxes' credential volumes
become mountable without touching those boxes).

The honesty response is NOT to qualify each per-wall status row (arbitrary:
why the Network row and not the filesystem exposure line?). **byre warrants
its own construction, never the consequences of your hole.** Every existing
row keeps describing what byre built — and it genuinely built them; they
still hold *for the box*. A separate, loud `containment` line describes the
hole and voids the warranty for anything done through it, covering all five
walls in one sentence including attacks not yet enumerated (e.g. a sibling
container with `--network container:<box>` + `NET_ADMIN` flushing the box's
own firewall).

This deliberately does NOT degrade the Network row, even though byre
already degrades it for raw `run_args`. The two are different epistemics:
raw `run_args` degrade the claim because byre *cannot see* what they do (it
declines to vouch for its own construction under an unauditable layer);
docker-host is a *declared door beside a sound wall* — byre's construction
is intact and byre knows exactly what the skill does. Degrading a claim
because of a skill's content would make core introspect skills to re-score
unrelated claims — the "policing skills" role byre refuses (PRINCIPLES:
deny-by-default; footgun doctrine). The `🛑` containment line carries the
signal at self-edit's loudness (a standing, host-wide grant deserves at
least a one-session store mount's alarm).

## Mechanisms

- **CLI install**: `docker-ce-cli` + `docker-compose-plugin` +
  `docker-buildx-plugin` from Docker's official apt repo (client only — no
  engine, no daemon in the box). compose/buildx are same-socket plugins:
  the capability is identical with or without them, so bundling is
  ergonomics.
- **Socket group access** (`sock_groups` runtime key): the box runs
  unprivileged as `dev` with no runtime root (ADR 0008), so it cannot join
  the socket's owning group from inside. The runner instead injects numeric
  `--group-add <gid>` at `docker create` — where caps and mounts already
  land, root staying outside the box. The gid is discovered by an
  ENGINE-SIDE probe (a one-shot container `stat`-ing the bind), never a
  hardcoded value or host-OS branch: it is `0` on Docker Desktop (the VM
  serves the socket root-owned) and the `docker` group on native Linux —
  host-verified. `sock_groups` is itself an attributed grant (the group is
  wider than the named path); each entry must match an active bind target
  on the same skill.
- **Missing socket = attributed warning, not refusal**: byre uses
  `--mount type=bind`, which already errors on a missing source (the
  create-a-root-dir footgun is `-v`, which byre does not use), so the
  engine is the fail-closed authority. byre only adds attribution ("Docker
  not running? Podman-only host?"), and suppresses it under Docker Desktop
  where a host-side `stat` is not authoritative (the bind resolves in the
  VM; `/var/run/docker.sock` can be absent on the Mac host yet live in the
  box — host-verified).
- **Compose namespacing**: every box's cwd is `/workspace`, so compose's
  default project name would collide across worktrees. Core passes
  `BYRE_PROJECT` (shared) + `BYRE_WORKTREE` (per-worktree, ADR 0009) into
  every box as plumbing; the skill's env.d hook defaults
  `COMPOSE_PROJECT_NAME` to `byre-$BYRE_WORKTREE` (distinct per worktree,
  stable per checkout, user override respected).
- **Egress**: none. The CLI speaks a unix socket; daemon-side network
  activity is the host's, covered by the warranty disclaimer, not by an
  egress declaration.
- **Agent context** (`context.md`): guidance against the accident class —
  you drive the *host* daemon, clean up what you create, do not
  stop/remove/prune others' containers (one is YOU) or mount foreign
  volumes (that exfiltrates other projects' shared-auth tokens).

## Legibility

The `containment` declaration renders on four surfaces — `byre status`
(a `🛑` Containment row beside Network), the launch line, the adoption
warning summary (a new top-sorted class, above machine volumes: a standing
host-wide hole must not hide below them), and the config UI. Skill-owned
text, validated single-line / no-control-char / bounded so a declaration
cannot forge adjacent rows. Multi-declarer renders all, attributed (unlike
the single-declarer `network_posture`). `docs/DOCKER-HOST.md` carries the
full reasoning chain (root-equivalence, the Docker Desktop VM nuance,
shared-auth volume exfiltration, when to prefer service sidecars).

## Rejected / consciously not done

- Rendering the numeric probed gid on any user surface: status/adoption run
  before any probe (the number isn't known there), and `0` vs `989` changes
  no decision — the grant line's "wider than the named path" carries the
  meaning; the digits are debug detail on the probe's failure path.
- A launcher root phase to set group membership (the original design):
  impossible under ADR 0008 (no runtime root, no gosu drop) — the runner
  `--group-add` is the correct layer.
- Podman host support: a sibling `podman-host` skill with its own verified
  facts, out of scope here.
