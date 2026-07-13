# docker-host skill -- design of record (pre-build)

Status: design settled with Pete via /grilling 2026-07-13; awaiting
independent design review (codex + grok) before building. This document is
the review input: it states the decided shape, the alternatives considered,
and why each fork resolved the way it did. Reviewers: challenge the
decisions on their merits; nothing here is sacred except where a standing
principle or ADR is cited.

Working-doc lifecycle (deliver precedent): absorbed into an ADR +
docs/docker-host.md when built, then deleted; git history is the archive.

## What it is

`docker-host` is a builtin skill granting the box access to the **host's
Docker daemon** via its socket. It is the ergonomic form of a grant already
expressible by hand (mount + CLI + group); the skill's value is bundling it
with honest, loud legibility.

Explicitly NOT: docker-in-docker (no nested daemon), not a Podman variant
(a Podman host would want a sibling `podman-host` skill with its own
verified facts, out of scope), not the service-sidecars feature (which
covers the compose-deps case WITHOUT this grant; see TODO).

## Decisions

### D1. Name: `docker-host`

"Access to the host's Docker socket." Rejected: anything DinD-flavored
(misdescribes the mechanism exactly where precision matters most);
`docker-on-docker` (same). GLOSSARY gains the entry.

### D2. CLI install: docker-ce-cli + plugins, from Docker's apt repo

`[build]` installs `docker-ce-cli`, `docker-compose-plugin`,
`docker-buildx-plugin` from Docker's official apt repository (~4 Dockerfile
lines: keyring fetch, sources list with ID/VERSION_CODENAME read from
/etc/os-release, apt-get update + install). Works across Debian- and
Ubuntu-family bases (default base is debian:bookworm; templates ride
Debian-family images).

Rejected:
- `docker.io` (Debian archive): pulls the whole engine -- containerd, runc.
  Heavy, and plants a daemon in a box whose point is "no daemon in here".
- `docker-cli` (Debian split package): absent from bookworm.
- Static tgz from download.docker.com: fewer moving parts but hand-pinned
  version, manual arch mapping, no plugin story.

Compose + buildx are client-side plugins speaking to the same socket: the
capability is identical with or without them, so including them is
ergonomics, not scope creep. Build-time fetch from download.docker.com is
the same class of build egress as codex's vendor-installer curl.

### D3. Socket access: mount + a new generic `sock_groups` core mechanism

The mount: `/var/run/docker.sock -> /var/run/docker.sock`, rw (connecting
to a unix socket requires write).

The permission problem: the box runs as `dev`, never root (ADR 0008). On a
Linux host the socket bind-mounts as root:docker 0660 with a HOST-specific
gid; on Docker Desktop the container sees whatever the VM presents (host-
side stat of ~/.docker/run/docker.sock tells you nothing). No static
skill.toml value can name the right gid, and `--group-add docker` by name
resolves against the container's /etc/group.

Decision: new skill.toml key, `[runtime] sock_groups = ["/var/run/docker.sock"]`.
For each listed path that exists, the launcher -- while still root at PID 1,
before dropping to dev -- reads the gid owning the path AS SEEN INSIDE THE
CONTAINER and grants dev supplementary membership in a group with that gid,
then drops as usual. Correct on Linux and Docker Desktop alike (the
in-container view is the only one that's always right). Generic mechanism:
any future socket-ish grant (podman-host, pcscd, ...) reuses it. Opinion-
free core: "make this path's group reachable" is mechanism, not policy.

Rejected:
- Static `run_args = ["--group-add", ...]`: no correct static value exists.
- byre stats the host path at launch and injects the gid: right on Linux,
  wrong on Docker Desktop (host gid != in-VM gid).
- In-box fix: structurally impossible (no sudo; firstrun hooks run as dev;
  netns_init joins only the network namespace).

Note: the TODO's "all expressible in skill.toml today" turned out false --
this is a small core change, accepted knowingly.

### D4. Missing socket = REFUSE the launch (generic, all mounts)

Docker's behavior for a bind whose host source is missing is to create it
as an empty root-owned DIRECTORY -- on the host. For this skill on a
socketless machine (Podman-only host, Docker Desktop not running) that
plants a root-owned dir at /var/run/docker.sock which then blocks dockerd/
Desktop from ever creating its socket there: host damage requiring root to
undo, from a mere config/machine mismatch, plus an in-box failure
("is a directory") pointing away from the cause.

Decision: launch-time check, generic across ALL binds (config- and skill-
contributed): any bind whose host source does not exist refuses the launch
with an attributed, actionable error naming the path, the contributing
skill, what Docker would have done, and the fix. No override flag and no
warn-and-proceed: proceeding cannot mean what the user meant (they chose
"mount the socket"; there is no socket), and if someone genuinely wants
Docker's create-the-host-dir behavior, pre-creating the dir on the host is
one command -- the escape hatch is the filesystem. Precedent class:
rootless-podman detect-and-refuse, non-TTY partial-flag onboarding refusal.

### D5. Warnings: a declarative `containment` key, skills-only

New `[runtime]` key: `containment = "<skill-owned one-liner>"`. Purely
declarative, mirroring `network_posture`: the skill declares what it does
to the box's containment story; byre prints it, attributed, and never
inspects or enforces. Skills-only for now (no project-config equivalent)
-- we trust skills to say what they break; proportionality says wait for a
second consumer before widening.

Rendering surfaces (all rendering, no gates anywhere -- footgun doctrine:
legibility, never a block; the adoption prompt is already the consent
gate):
- `byre status`: a Containment row beside the Network row.
- Launch: a `byre: containment: ...` line joining the exposure/network pair.
- Adoption warning summary: a NEW top-sorted warning class (above machine
  volumes), rendered when an incoming config enables a containment-
  declaring skill. The summary's job is meaning, not inventory: the mount
  line below it is the mechanism, this line is what you're consenting to.
- Config UI: same line in the skills/GRANTS view.

The network posture claim is UNTOUCHED. Standing doctrine (status.go
networkLine): skill contributions never degrade another claim -- enabling
a skill IS trusting it; its grants are attributed separately. Each status
row stays independently true and unqualified: "deny-by-default" remains an
honest statement about the box's netns; the Containment row states the
escape hatch once, loudly. Rejected: cross-degrading the network line
("bypassable via host daemon -- see Containment") -- two qualified lines
carrying one fact, more load on the grant statements, and a contradiction
the reader has to reconcile.

Wording (skill-owned strings; tunable at review):
- Adoption/config UI: "docker-host changes this box's security posture --
  it can drive the host's Docker daemon. At least skim docs/docker-host.md
  before enabling."
- Status: "changed -- this box can drive the host's Docker daemon
  (skill: docker-host -- at least skim docs/docker-host.md)"
- Launch: "byre: containment: changed by docker-host -- box can drive the
  host's Docker daemon. Worth a skim: docs/docker-host.md"

"Root-equivalent" deliberately does NOT appear in the one-liners: accurate
but uninformative without the reasoning chain, which is the doc's job. The
one-liner is a signpost with an imperative nudge to the doc.

### D6. The discussion doc: standalone docs/docker-host.md

User-facing, born with the feature (deliver.md precedent; future site
page). Linked from the skill.toml header, the containment strings, and a
short pointer section in SECURITY.md. Contents:
1. What the grant is, first and plainly: socket access is root-equivalent
   on the host (`docker run -v /:/host` is a two-line takeover) -- the
   grant working as designed, not a flaw.
2. What survives vs what's gone: accident-scale isolation intact (a
   confused agent still trashes only the box); containment against a
   deliberate or prompt-injected escape gone.
3. Firewall interaction: daemon-side actions (pulls, --net=host
   containers) ride the HOST's network; the box's egress rules govern only
   the box's own namespace.
4. Host state that outlives the box: images/containers/volumes the agent
   creates; byre's teardown knows nothing of them.
5. When to grant it vs the alternative: service sidecars (TODO, L) cover
   compose-deps without this grant; docker-host is for when the agent's
   job IS containers.

### D7. Agent-facing context.md

The skill ships a `[context]` snippet (firewall precedent) -- guidance
against the ACCIDENT class, which is what context is for:
- You are driving the HOST's Docker daemon, not a private one; what you
  create is host state that outlives this box; clean up what you create.
- Containers visible to you -- including byre boxes, one of which is YOU --
  belong to the user. Do not stop/remove/prune anything you didn't create;
  `docker system prune` is off-limits (it eats other projects' state).
- Label what you create so it's attributable and cleanable.
- The box's network rules do not apply to containers you launch; security
  discussion: docs/docker-host.md.

### D8. Egress: none

The docker CLI speaks to a unix socket; the skill declares `egress = []`
and opens zero network doors. (Daemon-side network activity is the host's,
covered by D5/D6 honesty, not by egress declarations.)

## Verification plan

- Design review (this doc) by codex + grok BEFORE building; big findings
  re-grilled with Pete.
- Host-verify (Pete): socket gid presentation on real Linux and Docker
  Desktop/macOS; sock_groups mechanism end-to-end; docker/compose/buildx
  function in-box.
- Unit: skill.toml parse (new keys), launcher sock_groups logic via
  injected seams, mount-source refusal, containment rendering on all four
  surfaces, golden Dockerfile output for the apt-repo lines.

## Related decisions banked during the same grilling (separate TODOs)

- `!host` egress closures (S): subtract from the derived allowlist under
  deny-by-default, INCLUDING skill-declared entries (today skill egress
  unions in after the cascade, out of `!name`'s reach).
- Open-denylist firewall mode (M): otherwise-open network with `!host`
  closures enforced; best-effort IP-snapshot blocking aimed at well-behaved
  clients (telemetry), worded so.
- Observed-open ("logging") firewall: raised and WITHDRAWN -- deny-by-
  default already fails loudly and legibly; observation is strictly weaker
  legibility than the closed posture.
