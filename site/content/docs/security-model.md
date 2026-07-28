---
title: Security model
weight: 85
description: the threat model, the contract, and the sharp facts
params:
  reference: true
---

This is the straight version of byre's security story -- the facts that
are true and important but too sharp for a pitch.
[What's boxed, what isn't](/docs/whats-boxed/) is the summary; this is
the detail. byre is young; read the warning on the
[install page](/docs/install/) first.

## Threat model

**The threat is the agent, never the user.** byre exists so an
autonomous coding agent can run at full permissions without wearing your
identity or reaching your machine. It is NOT designed to protect you
from yourself: every protection is one config edit from off, and that is
deliberate ("not your nanny" -- the box is locked against the agent, not
against you). byre's standing promise is legibility, not gates:
`byre status` always tells you what the box can reach.

byre is built for over-eager, reckless, or misbehaving agents -- the
ordinary case, where the agent isn't trying to do anything to you.
Against an actively malicious agent, including one acting on hostile
instructions it read somewhere, byre takes some simple precautions; that
is all it offers. This isn't a security product. It is not built to
resist an agent deliberately working to get out, or a dedicated attacker
with kernel exploits.

## The contract

- **Boxed** ([what's boxed](/docs/whats-boxed/) is the one-breath
  version): the agent sees the project folder, plus exactly what you
  mount, pass, or enable -- nothing else. byre reads no host credentials
  and copies none. Agent logins happen inside the box; the one exception is the
  optional shared-auth token for Claude
  ([ADR 0017](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0017-shared-agent-identity.md)),
  which you mint yourself with `claude setup-token` -- wherever suits
  you -- and hand over at an explicit prompt. Explicit hand-over, never
  ambient inheritance.
- **Not boxed, by design:** the network (open by default; the
  default-deny firewall skill closes it to a derived allowlist) and the
  project tree, which stays read-write because editing it is the job.
- **A container is not a microVM.** The box shares your kernel. If your
  requirement is "the agent must never share a kernel with my machine",
  use a microVM product or a separate physical box.

## Specific facts worth knowing

**Docker daemon access is root-equivalent.** Anyone who can talk to the
Docker daemon (the `docker` group) can mount any named volume -- byre's
included, identity volumes included -- or the host filesystem itself.
This is Docker's design, not a byre gap, and byre cannot change it. On
shared machines, treat daemon access as root. byre's uid-qualified
naming (`byre-<id>-u<uid>-...` images, `byre-machine-u<uid>-...`
volumes) prevents users *accidentally* sharing state; it cannot stop a
daemon user doing it deliberately. **Project state volumes
(`byre-<id>-...`) are not uid-qualified**, so two Unix users sharing one
checkout on the same daemon share -- and one's `reset`/`forget` can
clobber -- that agent state; byre is a single-operator tool and does not
re-key them. `byre shell` does hide a box started by another Unix user
(`--skip-uid-check` enters it anyway, as that box's dev identity). The
optional `docker-host` skill is exactly this grant, made legible: see
[docs/DOCKER-HOST.md](https://github.com/pjlsergeant/byre/blob/main/docs/DOCKER-HOST.md)
before enabling it.

**A skill is trusted code -- enabling one hands it the box.** Skills
ship raw Dockerfile lines, shell hooks sourced at launch, and (for a
network-posture skill) a root helper in the box's network namespace.
The allowlists on a skill's typed fields (`apt`, env keys, `egress`)
exist for legibility -- so a typed field always reads as data -- not as
containment; there is no sandbox between an enabled skill and the box
it builds. The agent `command` is deliberately a shell fragment for the
same reason (flags ride in it; quoting it would contain nothing).
Treat enabling a skill like installing an editor extension: a trust
decision about its author, made once, with the consequences legible in
`byre status`.

**Config `env` values are baked into the image.** They are emitted as
Dockerfile `ENV` layers, so `docker history` shows them to anyone with
daemon access, and they live in the image -- surviving `byre reset`,
which clears volumes, not images -- until it is rebuilt or deleted.
To be clear about what this does NOT change: byre images never leave
your machine (there is no push path), and daemon access is
root-equivalent anyway -- nobody can read the layer who couldn't
already read the running container's env, the volumes, and the host.
So this is a hygiene fact, not a new exposure. The reasons to know it:
a value persists in image layers after you delete it from config, and
changing one re-runs the project block's installs. Real credentials
belong to the agents' own login flows or the shared-auth skills, which
keep them in volumes.

**An open network is an exfiltration channel.** With the default open
posture, an agent can send anywhere -- including the project it is
working on, or any credential you passed in. The firewall skill
(deny-by-default, per-skill derived allowlist, applied from outside the
box, fail-closed launch gate) is the mitigation; enable it if this is in
your threat model. The firewall-open skill is NOT: its open-denylist
posture blocks a snapshot of named hosts' IPs -- hygiene against
well-behaved telemetry clients, trivially routed around by an agent that
wants to. The network under it is still open, and this document treats
it as such.

**A mount over byre's own paths wins at runtime, and byre only tells
you.** byre bakes its machinery into the image -- the launcher, the
launch gate the firewall's fail-closed wait reads, and the delivery
artifacts under `/etc/byre` -- and re-asserts the security-critical ones
at the end of the build, so a `files` entry cannot quietly replace them.
A bind mount or a named volume is different: the engine applies it over
the finished image, and byre has nothing that runs afterwards. A volume
at `/etc/byre` is filled once -- from the image when the engine first
mounts an empty one, or from your seed if it has one -- and is the
authority from then on, so a gate a later build bakes never reaches the
box, and anything that empties the file it holds is permanent. The launcher treats no gate as nothing to wait for, so the
next `docker restart` brings the box up with its netns unfirewalled. `byre
status`, `byre develop`, and the grant review a preset asks you to
approve all disclose any mount or volume covering those paths -- the
project's own and any a skill declares, named -- in the Containment
register, and then run it: this is your configuration to make ([ADR 0052](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0052-runtime-mount-shadowing-one-disclosure.md)).

**Concurrent sessions share a pathname race.** Every byre bind mount is
handed to the engine as a pathname, not an inode-pinned handle -- the
docker/podman CLI-to-daemon contract is a pathname, resolved in the
daemon's own namespace (a VM under Docker Desktop), so byre cannot pin
it from the host side. The consequence: an agent in a concurrent rw
session that can rename an ancestor of a bind source during *another*
launch's short detect-to-mount window could redirect that bind.
Worktrees make concurrent sessions an ordinary workflow, which is why
this is worth knowing; the window belongs to a launch in progress -- a
session already running is not affected. The full analysis is in
[ADR 0009](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0009-worktrees-inherit-project-identity.md).

**The firewall's allowlist is an IP snapshot.** A hostname grant is
resolved once, at session launch, and the rules pin those IPs; the name
is never re-resolved while the box runs. If the host's DNS answer moves
after that -- CDNs rotating a pool, and some resolvers rotating the
answer on *every query* (Azure's forwarding DNS does) -- connections to
that host start failing even though it is granted. On a per-query
resolver this can bite seconds after launch, not just mid-session. The
failure direction is always closed: rotation can cost you reachability,
never containment. A session restart re-resolves; for an endpoint with
a stable address, granting the IP directly sidesteps the race.

**What comes out of the box may be executable.** The project is not just
data; it is a directory your host tools run code from. An agent with the
project mount can leave behind a git hook, a git config that names a
program (`core.hooksPath`, `credential.helper`, `core.sshCommand`,
`core.fsmonitor`, a `filter.*` or `diff.*` helper), an `.envrc` that
direnv evaluates when you `cd` into the project, an `.env` whose values
steer a later process, an editor task, or a build script -- and your
later host command that reaches it runs it, as you -- which command
depends on what was planted, but ordinary use of the project is the
trigger and nothing else has to go wrong. The firewall skill
does not help here: this rides the filesystem, not the wire. Neither
does reading the diff, for some of it -- the git admin directory sits
outside the working tree and never shows in `git status` or `git diff`,
and ignored files are normally omitted as well, while editor tasks and
build scripts usually do show. Git's configuration is also more than one
file: `include.path` and `includeIf` can pull in others. The practical
rule is that a tree an agent has worked in is its output rather than
your code, and running anything out of it -- `git` included -- runs what
it wrote. When a session ends byre mentions changes it noticed in a few
of those places, in the register of "we thought you should know"; it
checks a handful of spots, not everything, and a quiet exit is not a
clean bill of health.

**`--self-edit` is transitive trust of the agent with your host.** A
self-edit agent authors the next develop's config -- mounts, run args,
build context -- through the front door. There is no meaningful
containment beyond that point, and byre does not pretend otherwise: the
launch banner says exactly this, and the session ends with a diff of
what changed in the store.

**Shared (machine-scoped) volumes cross project boundaries.** The
shared-auth skills
([ADR 0017](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0017-shared-agent-identity.md))
put one agent credential where every one of your projects' boxes can use
it -- that is their purpose. A misbehaving agent in ANY box can read
(and so exfiltrate) that credential, exactly as it can its own
per-project login today; the tokens involved are inference-scoped where
the vendor allows it (Claude's setup-token). `byre status` names shared
volumes; `reset` / `forget` never delete them silently.

**Agents hold usable credentials by construction.** Whatever auth story
you choose, the agent can read its own credential -- it needs it to
work. byre's job is that the credential's scope is what you chose, its
location is legible, and nothing you did not choose rides along.

## Reporting

Report security issues via GitHub security advisories on
[pjlsergeant/byre](https://github.com/pjlsergeant/byre/security) -- the
policy lives in
[docs/SECURITY.md](https://github.com/pjlsergeant/byre/blob/main/docs/SECURITY.md).
