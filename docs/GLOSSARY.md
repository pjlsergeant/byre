# byre glossary

byre runs an AI coding agent in a throwaway, project-scoped container -- a
local-first, inspectable, Docker-native harness. This file is canonical for
**vocabulary only**: TODO.md owns what to do, `ARCHITECTURE.md` owns how
things work, `PRINCIPLES.md` owns how we decide, `adr/` owns why decisions
went the way they did -- this file owns what things are called. When
another doc, the code, or a conversation disagrees with a definition here,
one of them is wrong and should be reconciled -- naming drift is a bug you
can point at.

## Language

### The box and its lifecycle

**Box**:
The sandboxed environment a project's agent runs in -- the container plus
everything granted to it. The user-facing word; "container" is reserved for
the engine-level artifact (the status row, port mappings, engine errors).
"Sandbox" stays legal as descriptive prose about what a box is, not as the
noun itself.

**Session**:
One foreground run of a project's box (one `byre develop`, one container).
Single-session per project directory; parallelism comes from worktrees.

**Agent**:
The AI coding CLI (Claude, Codex, Gemini) that runs inside the box. byre is
not an agent; it runs one.

**Engine**:
The container tool byre shells out to: Docker or Podman.
_Avoid_: runtime (collides with a skill's `[runtime]` contributions)

**Launcher**:
The constant, unprivileged entrypoint script that preps the box (git
identity, agent context, first-run hooks, launch gate) and execs the
selected agent's command.

**Launch gate**:
The launcher's wait, at its very top, for the firewall ready signal before
anything else runs. No signal within the timeout kills the box -- it fails
closed, never launches open.

### Config

**Cascade**:
The three-layer config resolution `default ⊕ template ⊕ project`. Scalars
override (last wins), lists union, `!name` removes.

**Layer**:
One file in the cascade: the global default, a template, or the project
config.

**Template**:
A named, reusable config layer under `~/.byre/templates/<name>` (e.g. go,
node). A config-cascade concept, not a Dockerfile template.

**Base**:
The `FROM` image the generated Dockerfile builds on. Debian-derived in v0.

**Host-side store**:
`~/.byre/projects/<id>/` -- where a project's live config actually lives,
deliberately outside the rw-mounted project tree so the boxed agent can't
rewrite its own sandbox.

**Proposal**:
A `byre.config` committed in the project tree. Inert until adopted; the
agent may edit it freely and nothing changes.

**Adoption**:
The explicit, host-side, human act of reviewing a proposal's grants and
copying it into the host-side store. Re-prompts when the proposal changes.
_Avoid_: import, sync

**Raw block**:
A verbatim passthrough byre never parses inside: `dockerfile_pre`,
`dockerfile_post` (build) and `run_args` (runtime). Shown verbatim and
flagged as not-introspected in status; presence degrades any posture claim.
_Avoid_: escape hatch as the noun (it describes what a raw block is *for*)

### Skills

**Skill**:
A portable bundle that contributes to any layer byre controls: build
(Dockerfile block + files), runtime (mounts/env/args), agent context, and
state (named volumes). All opinions live in skills; enabling a skill is
trusting it.

**Agent skill**:
A skill with an `[agent]` table: contributes the agent's CLI, its launch
command, and its state volume. The `agent` config scalar selects which one
the launcher execs, and implicitly enables it.

**Core**:
The byre binary minus all skill content: config resolution, generation,
the runner, identity, lifecycle commands, status -- including the skill
loader, though never skill content. Core ships no opinions and knows no
skill by name.

**Chassis**:
Core's constant provision to every box -- the core block at build time
plus the runtime constants (git identity passthrough, launcher behavior,
the launch gate). What a box has regardless of config; "core chassis"
when ownership needs saying. Cf. the microservice-chassis pattern.
_Avoid_: plumbing (the old spec's word), fittings, infra

**Core block**:
The build-time slice of the chassis -- core's constant contribution to
the generated Dockerfile: the `dev` user
baked at the host UID/GID, home/workspace ownership, the launcher install
(plus the `USER dev` + ENTRYPOINT tail, emitted last so earlier steps build
as root). The block family is named by contributor: template block, core
block, skill blocks, project block.
_Avoid_: infra layer (collides with cascade layers and Docker image
layers), users block; "plumbing" stays informal prose for core's job

**Materialize**:
Writing a built-in skill's files into `~/.byre/skills/<name>/` as editable
copies. Stale copies are refreshed by `byre skill update`, never silently
overwritten.

### Grants, mounts, volumes

**Grant**:
Anything that widens what the box can reach beyond a bare box: the project
mount (the implicit grant every box carries), host mounts, ports, skill
runtime holes, env *passed through from the host* (git identity is the one
today; a config-literal env var is config, not a grant), and egress
entries under a restrictive posture (an open network is the default world,
not a grant). byre makes grants legible; it never gates them.
_Avoid_: permission (implies a policy engine deciding; byre only reports)

**Host mount**:
A host path bound into the box via `mounts` (default read-only). The
project itself is the implicit one: mounted read-write at `/workspace`.

**Volume**:
A Docker named volume, per-project (`byre-<project_id>-<name>`), surviving
rebuilds. Usually contributed by a skill. There is no scope knob -- sharing
across unrelated projects was considered and dropped.
_Avoid_: shared volume, volume scope

**State (role)**:
A precious volume: agent auth, history, scratch. `byre reset` warns and
names what dies.

**Cache (role)**:
A disposable, regenerable volume: node_modules, build caches. Wiping is a
shrug.

**Seed**:
A one-time copy into a *fresh* state volume from a declared source. Not
credentials (today) -- agent logins are rotating tokens, so they are
performed in the box instead; see ADR 0007.

### Network

**Posture**:
The box's network stance: `open` (default) or `deny-by-default` (the
firewall skill). Declared by a skill via `network_posture`; printed by
status under the honesty rules.

**Egress**:
The derived allowlist: every enabled skill declares the `host[:port]`
endpoints it needs, byre unions them (plus the user's `FIREWALL_ALLOW`)
and enforces them as port-scoped per-IP rules. Empty is legal -- a
maximally-locked box.

**Netns helper**:
The run-to-completion container (root + NET_ADMIN, sharing only the box's
network namespace) that applies and verifies the firewall rules from
outside. The box itself gains no capabilities and no sudo.

**Degrade**:
What status does to a claim it can no longer fully stand behind (e.g. a
posture with raw blocks present): qualify it honestly instead of refusing
the configuration.

**Fail closed**:
The firewall's failure direction: any helper or gate failure kills the box
offline. It never launches silently open.

### Identity

**Project**:
The unit everything is scoped to -- image, volumes, config, labels.
Identity anchors at the main worktree's canonical path: linked worktrees
belong to the *same project*, inheriting its config, volumes, and image,
while each keeps its own container and workspace (the `byre.project` /
`byre.workdir` label pair).
_Avoid_: family, worktree group (diary-era words for the shared tier;
"repo" stays informal prose)

**Project id**:
The path-derived slug + short hash naming the image, container, volumes,
label, and host-side store. A naming device only; caching is Docker's job.

**Rehome**:
Re-pointing a moved or renamed directory's identity (image, volumes,
config) onto its new path-derived id.

### Doctrine

**Footgun doctrine**:
The named principle that byre's threat model is the *agent*, never the
user. Protections are tamper-proof against the box and one config edit
away from off for the user; byre never refuses a deliberate user choice.
We don't give you a gun that will shoot you in the foot if you were aiming
somewhere else, but we do give you a gun that lets you accurately target
your foot should you wish to.
Substance: PRINCIPLES.md #1.

**Legibility**:
byre's alternative to gating: `byre status` names every grant truthfully,
flags what it can't introspect, and degrades claims it can't stand behind.
Substance: PRINCIPLES.md #4.
