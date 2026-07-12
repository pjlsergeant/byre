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
The AI coding CLI (Claude, Codex, Gemini, Grok) that runs inside the box. byre is
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

**Deliver**:
Getting a file from the host into a running box, human-initiated: `byre
deliver` streams path arguments, the host clipboard, or stdin into the
box's inbox and hands the in-box path back (stdout + host clipboard).
Machine-scoped -- the one verb that picks a box by discovery, not cwd.
(ADR 0021)
_Avoid_: drop, ingest, airlock (all lost the naming)

**Inbox**:
Where delivered files land in the box: `/inbox`, a dev-owned directory
baked into the image, dead with the container -- re-deliver rather than
expect it to survive. Always spelled as the absolute path in output and
docs. (ADR 0021)
_Avoid_: airlock (connotes a two-way chamber; this is one-way and
human-initiated)

**Deliver app**:
The generated host-side drag target `byre deliver --install-app` writes:
a readable macOS `.app` (display name "Byre Deliver") or Linux
`.desktop` entry whose only job is invoking `byre deliver` on what you
drop. The Finder Quick Action is "Deliver to Byre". (ADR 0021)
_Avoid_: droplet (DigitalOcean owns it), materialize (reserved for
built-in skill copies), shim

### Config

**Cascade**:
The three-layer config resolution `default ⊕ template ⊕ project`. Scalars
override (last wins), lists union, a removal marker removes.

**Removal marker**:
A later layer's off-switch for an inherited list entry. Two spellings by
identity type: `!name` where the entry's identity is a string (skills,
apt, volumes, mounts by target), `remove = true` where it's structured
(ports, keyed by container port alone). Applied after the same layer's
additions; the removed entry is gone from the resolved set -- contrast a
mount's `disabled`, which keeps the entry visible (ADR 0015). (ADR 0018)

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
copying it into the host-side store. Both answers stick: yes and no are
each remembered for that version of the proposal, and any change to it
re-prompts. Adopting replaces the store config wholesale (the prompt
shows the diff).
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

**Companion skill**:
A skill that augments the selected agent skill rather than being one --
enabled alongside it, carrying only the delta (a volume, a hook, some
wiring), leaving the agent skill untouched. The shared-auth trio
(`claude-shared-auth` etc., ADR 0017) are the canonical examples.

**Shared-auth offer**:
The first-run picker's per-box question -- "Opt this box into <agent>
shared credentials?" -- asked at every onboarding whose chosen agent
has a companion skill declaring `shared_auth_for` (the author's vouch
that the mechanism is ready to offer; a broken or gate-pending
companion omits it). Yes puts the companion in the project's
`byre.config` `skills` -- the only grant the answer ever makes; no
writes nothing. "Save these as your default?" saves the answer as a
favourite (the picker-owned, cascade-inert `shared_auth` list), which
only prefills the next box's offer ([Y/n, i for info] vs [y/N, i for info]). Answering `i`
prints exactly what each answer writes (naming the companion skill),
then re-asks. The one suppression: the companion already granted
machine-wide by hand in `default.config` `skills` -- the picker itself
never writes that key.
ADR 0025 (superseding ADR 0024's machine-wide recording).

**Launch env hooks**:
The chassis mechanism `/etc/byre/env.d/*.sh`: skill-contributed scripts
the launcher sources (sorted, best-effort, unprivileged) after firstrun
hooks and immediately before exec'ing the agent -- the only way a skill
can put env into the agent process at launch. Sibling of `firstrun.d`.

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

**Devlog dir**:
`.byre-devlog/` at the working-tree root -- the self-ignoring dir (its own
`.gitignore` is `*`) where box-side skills keep their working files: the
agent diary, the code-review log. A convention established by builtin
skills, not core behavior; each skill that uses it ensures it exists
(hardened, via the shared devlog lib), so no skill depends on another.
Born as `.devloop/`; the old name is dropped, not migrated -- an existing
old dir is left untouched, renamed by hand if its history matters.
_Avoid_: naming it after any one skill -- that coupling is why it was
renamed.

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
A mount can be **disabled**: kept in the config and shown in status,
but not bound. Disabling is a switch (the entry and its mode survive);
`!target` removal is how a later cascade layer deletes the entry.

**Volume**:
A Docker named volume, surviving rebuilds. Usually contributed by a
skill. Project-scoped by default (`byre-<project_id>-<name>`); see Volume
scope.

**Volume scope**:
Which boxes share a volume: `project` (the default -- one per project) or
`machine` (one per user per machine, `byre-machine-u<uid>-<name>`, mounted
identically by every project that declares it; ADR 0017). General
`[[volumes]]` grammar, config or skill. "Machine" deliberately means
per-USER-per-machine -- the uid qualifier keeps two users on a shared box
from silently sharing state.

**Identity volume**:
The informal name for a machine-scoped volume holding one agent's
credential and nothing else -- what the shared-auth companion skills
declare. Deliberately identity-only: everything cwd-keyed stays in the
per-project state volume.

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
endpoints it NEEDS to function, byre unions them (plus the user's
`egress` config key, ADR 0019) and enforces them as port-scoped per-IP
rules. Empty is legal -- a maximally-locked box. An egress entry under a
restrictive posture is a grant; without one it is declared and inert.

**Offered egress**:
A declared-but-CLOSED door (`egress_offered`, ADR 0020): same grammar,
never enforced. Templates and skills use it for convenience endpoints
(registries, git hosting); the config UI offers each as a switch whose
open writes the plain entry into the user's own `egress`. Deny-by-default
means nothing opens except explicit user intent or a skill's own
functional requirement.

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

**Exposure line**:
The terse one-line tally of what a box can reach — host mounts (disabled
split out), ports, env vars, and the network stance — spoken in one shared
voice on two surfaces: atop the config UI's form, and as `byre:` lines at
launch (where the implicit `/workspace` mount and, when active, the
`--self-edit` store mount are named too). Counts only; `byre status`
is the detailed, attributed view for everything except plain env
values: the host passthrough (`env_from_host`) is attributed there,
and the full variable list lives in the config editor's Env screen.
Called "exposure", not "grants": a config-literal env var is counted (it
reaches the box) but is not a Grant.
_Avoid_: grant summary (env vars in the tally aren't all grants)
