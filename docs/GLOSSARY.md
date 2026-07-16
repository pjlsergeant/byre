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
The AI coding CLI (Claude, Codex, Gemini, Grok, OpenCode) that runs inside the
box. byre is not an agent; it runs one.

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
An `ssh://` target makes it a **remote delivery**: the same verb routed
through another machine's byre, same inbox, same path back. (ADR 0021;
remote: ADR 0035)
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

**Proposal / Adoption** (historical):
Pre-preset (ADR 0029) vocabulary: a `byre.config` committed in the
project tree was a "proposal", inert until "adopted" -- a develop-time
prompt that reviewed its grants and copied it into the host-side store,
with sticky per-version yes/no answers. The flow is deleted: presets
replace it (`byre preset apply`), and old adoption records migrate to
the Applied marker. Use the words only to describe what was removed.

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

**Package**:
The distribution unit: a skill or a template (one package = one kind;
both nouns share the verb set). Identified by a canonical id --
`owner/name`, or bare for local packages -- with three provenances:
bundled, local, installed. Enabling remains the only grant: a package's
existence changes what is *available*, never what runs. (ADR 0029.)

**Bundled package**:
A package inside the byre binary, loaded from `embed.FS` only --
structurally immutable. Ids are `byre/<name>`; `byre/*` is permanently
reserved, and a bundled package always owns its bare name as an alias.
The display mirror at `~/.byre/bundled/` is for humans (regenerated per
version, never loaded from).

**Local package**:
An editable directory under `~/.byre/skills|templates/` (bare or
`owner/name` nested); the id defaults to the store path. The directory
is the package: no manifest hashes, no install lifecycle.

**Installed package**:
A content-addressed, hash-verified snapshot under
`~/.byre/packages/<digest>/`, acquired with `byre skill|template
install <manifest-url>` and recorded in the store index. Installed ids
must be qualified (`owner/name`). Immutable; edit by forking.

**Fork**:
`byre skill|template fork <id> <new-id>`: copy an immutable (bundled or
installed) package into a local, editable one under a new id. The only
artifact-to-source transition; provenance in the fork is a documentary
comment, never read for resolution or trust.

**Manifest**:
A package's `[package]` block in its primary file (`skill.toml` /
`template.config`): id, version, kind, `package_api`, `requires_byre`;
for installed packages, also the exhaustive per-file payload list.
Parsing stages and integrity mechanics: ADR 0029.

**Package digest**:
The sha256 identity of *what was acquired* -- proof of acquisition,
never a runtime attestation. Keys the snapshot dir, the same-id no-op
rule, and `--digest` pins in printed install commands. Computation and
integrity model: ADR 0029.

**Catalog**:
The per-store index of every package byre can see -- bundled, installed,
local -- plus per-identity problem rows (INVALID / conflict / LEGACY)
shown disabled-with-reason. One resolution function serves every name
surface. Resolution mechanics: ADR 0029.

**Retired name**:
A bare name a past byre release bundled and a later release does not.
Permanently protected in an in-binary table with a tombstone remedy (the
exact pinned install command): the name never returns to the free pool.
First retirees: `codereview`, `devlog`.

**LEGACY row**:
The catalog's marker for a leftover materialized copy of a bundled or
retired name under `~/.byre/skills|templates/`: never loaded, listed
with its reason, archived aside by `byre skill archive-legacy`.

**`[sources]` hint**:
A config/preset table mapping package ids to `{ uri, digest }`
acquisition hints. Never auto-fetched: anywhere byre reports a missing
package it prints the exact, kind-correct install command from the map,
attributed to the layer that supplied it.

**Preset**:
A saved answer to onboarding's questions: a complete proposed config in
byre.config format, from anywhere -- conventionally `byre.preset` in a
repo. Not a package (no identity, no version, no install). Applied only
via `byre preset apply` (review + chauffeur + confirm + write);
`byre.config` is reserved for the box's live consent document.

**Chauffeur**:
The inside-apply walk-through of a preset's missing packages: each gets
its normal, kind-specific install flow with its own grant summary and
confirm -- N explicit consents the user solicited by invoking apply,
never the banned transitive install (silent fetching). The solicitation
rule's full statement: ADR 0029.

**Applied marker**:
The per-project record `preset apply` writes (sha256 of the applied
preset bytes + its source). The three drift states derive from it: not
applied; applied-and-matching (silent); diverged ("the repo's preset
differs from the version you applied"). It proves preset-vs-applied
only -- live-config edits are yours, not drift.

**Agent skill**:
A skill with an `[agent]` table: contributes the agent's CLI, its launch
command, and its state volume. The `agent` config scalar selects which one
the launcher execs, and implicitly enables it.

**Companion skill**:
A skill that augments the selected agent skill rather than being one --
enabled alongside it, carrying only the delta (a volume, a hook, some
wiring), leaving the agent skill untouched. The shared-auth skills
(`claude-shared-auth` etc., ADR 0017) are the canonical examples. The
pairing is declared with `companion_for` in the skill.toml, or implied
by `shared_auth_for` (the shared-auth offer's vouch); it is a fact, not
a readiness claim, and is what nests the companion under its agent's
row in the config UI (ADR 0034).

**docker-host**:
The builtin skill that grants the box access to the **host's Docker
daemon** via its socket -- root-equivalent on the host, declared as a
`containment` hole so every grant surface disclaims the warranty for
anything done through the socket. Not docker-in-docker (no nested
daemon); not a Podman host skill. Mechanics: ADR 0027; user-facing
discussion: `docs/DOCKER-HOST.md`.

**Shared-auth offer**:
The first-run picker's per-box question -- "Opt this box into <agent>
shared credentials?" -- asked at every onboarding whose chosen agent
has a companion skill declaring `shared_auth_for`. Yes puts the
companion in the project's `byre.config` `skills` -- the only grant the
answer ever makes; the saved favourite (`shared_auth`, picker-owned,
cascade-inert) only prefills the next box's offer. Prompt wording,
suppression rule, and history: ADR 0025 (superseding ADR 0024's
machine-wide recording).

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
plus the runtime constants (the core `env_from_host` layer: git
identity, TERM, TZ; launcher behavior; the launch gate). What a box has regardless of config; "core chassis"
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

**Materialize** (historical):
Pre-package-model (ADR 0029) vocabulary: writing a built-in skill's files
into `~/.byre/skills/<name>/` as editable copies. The mechanism is deleted;
use the word only to describe what was removed. Leftover materialized dirs
are LEGACY rows, archived by `byre skill archive-legacy`.

**Devlog dir**:
`.byre-devlog/` at the working-tree root -- the self-ignoring dir (its own
`.gitignore` is `*`) where box-side skills keep their working files: the
agent diary, the code-review log. A convention that belongs to byre, not to
any one skill: the devlog skill curates it (bootstraps the dir, keeps the
diary), but each skill that writes there ensures it exists itself (hardened,
via the shared devlog lib), so codereview never needs devlog installed.
Born as `.devloop/`; the old name is dropped, not migrated -- an existing
old dir is left untouched, renamed by hand if its history matters. The
devloop *skill* was renamed to devlog in the same breath (a no-op stub
keeps old configs resolving): dir and skill had drifted two letters apart,
which confused more than it distinguished.

### Grants, mounts, volumes

**Grant**:
Anything that widens what the box can reach beyond a bare box: the project
mount (the implicit grant every box carries), host mounts, ports, skill
runtime holes, env *passed through from the host* (`env_from_host` --
byre ships git identity, TERM, and TZ; a config-literal env var is
config, not a grant), and egress entries under a restrictive posture (an
open network is the default world, not a grant). byre makes grants
legible; it never gates them.
_Avoid_: permission (implies a policy engine deciding; byre only reports)

**Host env passthrough (`env_from_host`)**:
The one deliberate host→box data channel: a config map `KEY = "source"`,
each live entry a grant. Sources are a closed scheme set --
`git:<config-key>`, `env:<HOST_VAR>` (absent host var sets nothing),
`tz:` (the host timezone: TZ var if set, else the `/etc/localtime`
symlink's IANA name), and `""` to disable a lower layer's key. byre's
core layer ships git identity, TERM, and TZ (ADR 0026/0031). A literal
value belongs in `[env]`; it is config, not a grant.

**Env docs (`env_docs`)**:
A skill's declared consumed-env guidance (`[runtime.env_docs]`,
`NAME = "one-line guidance"`): vars the skill READS but does not set.
Pure documentation -- no validation, no warning when unset; the config
UI env screen shows each unprovided var as a dim *suggestion row*
attributed to the skill, and enter prefills the add editor. Cf.
`runtime.env`, the vars a skill SETS.

**MCP declaration (`[[mcp]]`)**:
Wiring, not a grant (ADR 0033): a declared MCP server -- local
(`command`, an argv) or remote (`url`), self-discriminating. What's
real are the grants a declaration *carries* (the url's implied egress
plus declared extras, and the env NAMES it consumes), which render
where grants always render, attributed `mcp:<name>`. Merge and bake
mechanics: ADR 0033.
_Avoid_: calling an MCP a grant, or its status rows "grant honesty
machinery" -- they are config-application reporting.

**MCP adapter**:
How a selected agent's session receives the declared set — always by
INJECTION: byre never writes an agent's MCP state. `[agent]
mcp = "inject"` is the skill author's vouch that the agent command
consumes the baked file; an adapter-less agent degrades honestly
(declared-but-NOT-delivered). Per-agent mechanics and the walked-back
registrar design: ADR 0033.

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
The box's network stance: `open` (default), `deny-by-default` (the
firewall skill), or `open-denylist` (the firewall-open skill: open
network, the config's closures dropped). Declared by a skill via
`network_posture`; printed by status under the honesty rules.

**Egress**:
The derived allowlist: every enabled skill declares the `host[:port]`
endpoints it NEEDS to function, byre unions them (plus the user's
`egress` config key, ADR 0019, and the declared MCP set's carried
endpoints), subtracts the closures, and enforces the rest as
port-scoped per-IP rules. A host is a hostname, an IPv4 literal, or a
bracketed IPv6 literal (`[2001:db8::1]:8443` -- the RFC 3986
convention; bare IPv6 is ambiguous with host:port and rejected with a
pointer at the brackets). Empty is legal -- a maximally-locked box. An
egress entry under a restrictive posture is a grant; without one it is
declared and inert.

**Closure**:
A `!host[:port]` entry in the `egress` config key (ADR 0030). Unlike
the plain-list `!name` idiom it is not consumed by the merge: it
survives the cascade and subtracts its endpoint from the DERIVED
allowlist, skill-declared entries included ("claude minus statsig").
Portless closes EVERY port (addition is never greedy, subtraction may
be); a later layer's plain entry re-opens. Under open-denylist the
closures are themselves the enforced list. Never invisible: status
prints them as `Closed:` rows whatever the posture.

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

**Keep-id mode**:
The rootless-Podman path (ADR 0032): a generic-uid image (dev at 1000)
run under `--userns=keep-id`, mapping the invoking user onto the baked
id. Rootful engines instead bake the host uid directly (ADR 0008); both
end at the same place -- box-written files land owned by the invoker.
_Avoid_: "rootless mode" (rootless names the ENGINE's state; keep-id
names byre's path on it)

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
