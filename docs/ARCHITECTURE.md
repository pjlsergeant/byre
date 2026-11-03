# byre -- architecture

How byre works, as built. This file is the mechanics reference; it
describes current state only. Its sibling documents own the other lanes:
`GLOSSARY.md` (canonical vocabulary -- this file uses it), `PRINCIPLES.md`
(standing commitments), `adr/` (point-in-time decisions and their
rationale), `TODO.md` at the repo root (open work), `marketing/`
(positioning and launch copy).

byre is a small Go binary that runs an AI coding agent in a throwaway,
project-scoped container. `cd ~/project && byre develop` drops the agent
into a box that sees this project and what you explicitly grant -- not
your home dir, keys, or the rest of your machine.

## The shape

byre is a **transparent templating layer for running agents in
containers**. It is a convenience for the common case; it never stops you
writing Docker (PRINCIPLES.md #3). The split that defines the project
(PRINCIPLES.md #2):

- **Core** owns the chassis -- the agent-runtime scaffolding everyone
  reinvents (the dev identity baked into the image, git identity, the
  launcher, credential persistence). Core ships **no opinions**.
- **Skills** own the opinions. The workflow is a skill; the firewall is a
  skill; **the agent itself is a skill** (ADR 0005) -- byre ships agent
  skills for Claude, Codex, Gemini, Grok, and OpenCode, and `agent`
  selects which one launches. You compose your baseline from skills.

byre owns the frame around your Docker, not the Docker: Docker builds and
caches the generated image.

## Security contract

The boxed / not-boxed contract -- what byre isolates, what it leaves open
by design, and which grants widen it -- is stated once, on the site's
what's-boxed page (`site/content/docs/whats-boxed.md`); the threat model
and every disclosed residual are in the security model
(`site/content/docs/security-model.md`). The doctrine
behind both (the threat model is the agent, never the user; legibility
instead of gates; degrade claims, never refuse) is PRINCIPLES.md #1. The
mechanics below are what make that contract true: host state reaches the
box only through the grants this file names, each of which `byre status`
attributes.

### Network

Open to the world by default. No firewall, no `NET_ADMIN`/`NET_RAW` added
by core; because byre makes no network-containment claim by default, the
container's stock capability set is not a security surface byre reasons
about.

Network restriction is the built-in **firewall skill** (opt-in): it flips
a box's posture to deny-by-default egress with an allowlist. How it works:

1. `develop` starts the box normally; the launcher sees the gate file the
   skill baked into the image and waits at the **launch gate** -- at the
   very top, before first-run hooks -- for a ready signal (ADR 0011).
2. Concurrently, byre (host-side) runs the **netns helper**: a
   run-to-completion container sharing only the box's network namespace,
   root + `NET_ADMIN`, targeted by a per-invocation nonce label. It
   resolves the egress allowlist, installs port-scoped per-IP ACCEPT
   rules plus a default-DROP OUTPUT policy (v4 + v6), and self-verifies
   with a deny probe (ADR 0010). The probe covers v4 always and v6 where
   the netns has a global v6 address to probe from; without one, a probe
   that cannot leave would be indistinguishable from one that was
   blocked, so the helper reports the v6 side as applied-but-unverified
   rather than counting it.
3. The helper listens once on loopback; the launcher's poll-connect
   succeeds and the agent execs behind the wall. Any failure -- helper
   death, DNS failure, `docker restart` recreating the netns -- means no
   signal, timeout, and the box dies **closed** (ADR 0011).

The allowlist is **derived, and minimal by ruling** (ADR 0020). Every
enabled skill declares the `[runtime] egress = ["host[:port]"]` it NEEDS
to function (agents carry their API endpoints -- enabling the agent is
the intent). byre unions those with the user's `egress` config key (ADR
0012, key per ADR 0019 -- it cascades like every other list) and the
egress the declared MCP set carries, then
subtracts the config's **closures** -- `!host[:port]` entries, which
survive the cascade and subtract from the derived union LAST,
skill-declared entries included (ADR 0030). A portless closure closes
every port (addition is never greedy, subtraction may be), and a later
layer's plain entry re-opens what an earlier closure closed. Empty is
legal -- a maximally-locked box. An entry's host is a hostname, an IPv4
literal, or a bracketed IPv6 literal (`[2001:db8::1]:8443`, the RFC 3986
convention); bare IPv6 is ambiguous with `host:port` and is rejected
with a pointer at the brackets.

Nothing else opens. Convenience endpoints (git hosting, apt, language
registries) ship as `egress_offered` -- declared-but-closed doors the
config UI opens with one press, writing the entry into the user's own
config.

`byre status` prints the posture and shows the resolved allowlist as an
Egress section attributed per source (each skill, and `config` for the
key's entries), with closures as `Closed:` rows. Skill contributions are
trusted and attributed unless they DISPLACE byre's own machinery: a
reserved `BYRE_` knob degrades the claims it can skew, a mount over a
byre-managed path gets a containment line, and project-level raw blocks
degrade the claim (ADR 0010's annotation, ADR 0050, ADR 0052).

The **firewall-open skill** (ADR 0030) is the same mechanism with the
opposite default -- the `open-denylist` posture: the netns helper leaves
the OUTPUT policy at ACCEPT and drops only the closures' resolved IPs.
It is best-effort by design (an IP snapshot aimed at well-behaved
telemetry clients; the deny-by-default wall is the containment posture),
but it fails closed all the same: any helper failure -- including a
closure whose host doesn't resolve, which would otherwise stay silently
reachable -- kills the launch. The two enforcement siblings are mutually
exclusive (both declare a `network_posture`; resolution rejects two).

## Image generation

byre's job is to **generate a Dockerfile from config**; Docker's job is
to build and cache it (ADR 0001 -- byre owns no caching layer).

At every launch:

1. Resolve config -> generate Dockerfile *text* + build context.
2. Write it to `~/.byre/projects/<project_id>/context/Dockerfile.generated`.
3. `docker build --build-arg BYRE_UID=<uid> --build-arg BYRE_GID=<gid> -t byre-<project_id>-u<uid>-g<gid> <context>` (the UID/GID are baked in, so the tag carries them -- ADR 0008).
4. Under the setup lock, `docker create --rm -i[t] --label byre.project=<project_id> ... byre-<project_id>-u<uid>-g<gid>`; after the lock is released, `docker start --attach --interactive` in the foreground (see *Box lifecycle*).

On an unchanged config every instruction is a Docker cache hit. A change
rebuilds only the layers from the changed instruction onward. Freshness
is deliberate staleness: `byre rebuild` (`--no-cache --pull=true`) is
the valve for pulling a republished base and fresh upstream packages;
byre guarantees Dockerfile determinism, not upstream-artifact
reproducibility (ADR 0001).

**Instruction ordering is load-bearing** -- not for byre's own caching
(it has none) but because it determines how well *Docker's* layer cache
shares work across projects. The generator emits blocks in a stable
order, expensive-and-shared first, cheap-and-project-specific last:

```
FROM <base>                 # from template config
<template block>            # shared across all projects on this template ┐ Docker
<core block>                # constant: build-only gosu, baked dev user   │ layer-
<skill apt (hoisted)>       # every skill's apt RUN, ahead of all blocks  │ cached
<skill blocks>              # enabled skills: bundled, installed, local   │ across
<project block>             # this project only                           │ projects
<security guard>            # re-COPY chassis paths (launcher, gate, fw)  │
USER dev / ENTRYPOINT ...   # constant: drop to the baked user, then exec ┘
```

**Skill blocks** emit in provenance order -- bundled, then installed, then
local, stable within each class -- so editing a local or installed skill
does not re-run the bundled installers behind it (ADR 0041 has the
volatility argument). Enable order is unchanged everywhere the agent sees
it (context composition, status); only image layers move.

**Skill apt hoists above the skill blocks** (ADR 0042): every skill's apt
RUN emits, one per skill in the same provenance order, in its own section
between the core block and the skill blocks, so the only skill layers
that depend on mutable external state (`apt-get update`) sit where
payload and raw-line churn can't invalidate them.

The **security guard** re-COPYs byre's own copy of the security-critical
files *after* the project block:

- the launcher (the ENTRYPOINT's content);
- when a network-posture skill is enabled, its launch gate and netns
  enforcement script.

Those files are installed early (core and skill blocks), so without the
guard a project `files` entry targeting their paths would be a later
COPY and win in the built image: a one-line clobber could empty the
launch gate or stub the firewall while `byre status` still read
deny-by-default. It is the same posture as the
`USER`/`ENTRYPOINT`/`HEALTHCHECK NONE` tail: byre forces its
security-critical instructions last wherever it controls the order. A
`files` destination that collides with a guarded path draws a note at
develop and `byre dockerfile` (byre's copy takes precedence; the
override isn't silent).

The agent's own baked command (`agent-cmd`) is deliberately not
guarded: a `files` clobber there replaces the user's
agent with the user's own config.

The guard reaches only the image. A **runtime** mount or volume over a
byre-managed path -- the launcher, a netns hook, or anything under
`/etc/byre` (the launch gate and the baked delivery artifacts) -- is
applied by the engine over the finished image, where byre has nothing
left to re-assert with. So that case is disclosed instead of guarded: one
loud line at develop, in status's Containment register, and in a preset's
grant review, covering the project's own mounts and any a skill declares,
attributed (ADR 0052).

The **core block precedes skills**: it's constant, so placing it ahead of
the varying skill blocks keeps it cache-shared across all projects on a
base, and it means the `dev` user and `gosu` exist when skills build --
a skill can install as the dev user (e.g. `gosu dev` in a `RUN`) rather
than as root.

Ten projects on one `node` template share the early layers via Docker's
layer store; only the project tail diverges. byre never parses inside a
raw block, so it can't dedupe across them -- keep expensive shared
installs in the *template* block, not per-project blocks.

`byre dockerfile` prints the generated Dockerfile.

## Container engine

byre targets **Docker and Podman** through a thin runner abstraction over
the operations it needs -- build, run, volume/image/container ops --
shelling out to the engine CLI, never the SDK (ADR 0002). `engine =
"auto"` (default) picks `docker` if present, else `podman`.

Detection resolves the CLI to an ABSOLUTE path and every engine call
runs that path, pinned for the invocation. byre's other host-side spawns
(`git`, `ssh`, the shell behind `$EDITOR`, the clipboard helpers) are
pinned the same way, all through `internal/hostexec`.

A tool whose PATH resolution lands in a directory this project's box can
write -- the work tree, the main tree, the common git dir, byre's store
for the project -- is declined rather than run, naming the tool, the path
and the directory (ADR 0047). What a declined tool costs depends on the
command:

- `develop` refuses outright on a declined engine.
- The session-end probes degrade and disclose instead, since a session
  end must not be blockable by the thing it reports on.
- A declined engine is never read as an ABSENT one: `develop`'s
  single-session check treats it as uncheckable rather than as one engine
  fewer, and the commands that speak in totals (`forget`, `reset`,
  `rehome`, `backup`) refuse rather than claim "completely removed" over
  an engine byre never reached. Their `--ignore-docker` /
  `--ignore-podman` skip an engine that is installed but unreachable;
  they do not cover a declined one.

**Rootless Podman** is a first-class path with its own ownership math
(ADR 0032): the chassis bakes a GENERIC dev uid (1000) instead of the
host's, and the box -- plus every helper container that fills its volumes
-- runs under `--userns=keep-id:uid=1000,gid=1000`, mapping the invoking
user onto the baked id, so files land correctly owned just like the
rootful bake (ADR 0008). The netns-init helper joins the box's own userns
(`--userns=container:<box>`) instead, since NET_ADMIN over a netns exists
only inside the userns that owns it. byre refuses rootless Podman older
than 4.3 (no explicit keep-id mapping form);
`BYRE_ALLOW_ROOTLESS_PODMAN=1` overrides with the warning retained, the
same shape as the root-host refusal (`BYRE_ALLOW_ROOT=1`).

## Box lifecycle

The **container is throwaway; the volumes and image are not.** `byre
develop` creates a fresh container (`--rm`) under the setup lock, then
starts and attaches to it in the foreground; the engine removes it on
exit. What persists across sessions is the project-scoped image and the
named volumes (state + cache) -- agent auth, shell history, and caches
survive; no long-lived container accretes cruft.

A running session is identified by **labels** (`byre.project` +
`byre.workdir`), not an assumed name (ADR 0004); that's how `byre status`
finds "is a session running for this directory?".

**Every session container carries a launch record**
(`byre.launch=<sha256>`, ADR 0053). Under the setup lock, immediately
before `create`, byre writes what it is about to tell the engine --
binds, ports, volumes, env KEYS (never values), network posture and
resolved egress, the image tag AND digest and the effective base,
`run_args` verbatim, and the
skill identities -- to
`~/.byre/projects/<id>/launches/<sha256>.toml`, named by the sha256 of
its own bytes, and stamps that hash on the container. It records what
byre TOLD THE ENGINE, deliberately not the config that produced it: the
record is one step closer to reality than config, never a second copy of
it.

Readers VERIFY a record by re-hashing it: the store is box-writable under
`--self-edit`, so the address is checked, not trusted. Two things read
records. `byre status` makes a running box its subject: the grant rows
come from its record, the `Container` row says so, and a `Next launch`
section lists only what differs in the current config; a record byre
cannot read or verify degrades the page with one qualifier instead of a
guess. `develop`'s exclusive-volume check (ADR 0054, *Mounts & volumes*)
reads the records of this project's live boxes to decide whether an
exclusive volume is free, and refuses when it cannot establish that.
Records are reaped opportunistically at the next create, when nothing
points at them.

The other containers byre creates are one-shot helpers that run to
completion and exit: volume seeding, the worktree image step, and `byre
backup`'s capture and `byre restore`'s pour. None of them writes a
launch record, because none launches a session (ADR 0053 as amended by
ADR 0059), and none carries the project label. Their labels:

- the capture and the pour carry `byre.helper=<project id>` plus a
  per-invocation run id, which is how a cancelled or failed verb removes
  its OWN helpers and no others;
- the seed helper carries `byre.helper=<project id>` alone;
- rehome's migrate helper carries `byre.helper=<new id>` and
  `byre.helper.src=<old id>`, because it mounts both projects' volumes
  and a label key holds one value;
- the worktree helper carries neither label.

Every command that mounts or mutates a project volume -- backup,
restore, develop under its lock, `reset`, `forget`, `rehome`, and the
config editor's volume Clear -- queries `byre.helper=<project id>` and
`byre.helper.src=<project id>` first and refuses on a hit, naming the
container and its `rm -f` line, so a labelled helper left behind by a
killed byre or an engine outage can never run beside them unseen.

**`develop` is single-session per directory** (ADR 0004), and so is
`run`, against the same sessions: if a session is already running here,
it reports that session (and how to re-attach to its terminal, stop it,
or get a shell via `byre shell`) rather than spawning a parallel one --
two boxes on one directory would race the shared state volumes. For two
agents on one codebase, use worktrees: each worktree is its own workdir
with its own session, deliberately sharing the project's config,
volumes, and image (ADR 0009). A volume that cannot take two concurrent
holders says so with `sharing = "exclusive"`, and `develop` then refuses
the second box rather than corrupt it (ADR 0054).

**The configuration a session launches is read under the setup lock.**
The config editor's save takes that same lock, so a save landing while
`develop` waits for it is the one that launches. Everything the lock
guards -- generate, build, seed, create, and the exposure banner that
describes the result -- comes from the read taken under it; develop's
earlier read only decides what must happen *before* the lock (whether to
onboard, which engine to detect, which host tools to pin). `rebuild` and
the worktree create step read under the lock for the same reason.

The one thing the locked read cannot honor is a changed `engine`: the
runner, the identity mode (ADR 0032) and the image tag are already fixed
by the earlier detection, so all three refuse by name rather than build
or launch on the engine the config just stopped naming.

## Config

This section is the design view: why the cascade has the shape it has,
with the ADRs. Every key, the merge rules (scalars override, lists
union, identity-keyed entries replace, removal markers, closures) and the
preset and named-layer user guide are in the site's configuration
reference, `site/content/docs/configuration-reference.md` (hand-maintained;
the in-box `/etc/byre/config-reference.md` is derived from it by `go run
./cmd/byre config-reference-doc`, test-pinned); the editor walkthrough is
`configuration.md`.

Cascade, config-only (image steps are compiled output, never hand-written
twice):

```
~/.byre/default.config               your personal baseline
~/.byre/templates/<name>/            template.config (+ optional files)
~/.byre/layers/<name>/               layer.config (named layers, chained)
~/.byre/projects/<id>/byre.config    project config (the HOST-SIDE store)
```

Resolution: `default ⊕ template ⊕ chain(root … parent) ⊕ project`. The
merge rules are ADR 0018 (identity replace: its Cascade semantics);
`seed_prefs` is tri-state so a later layer's explicit `false` can turn
an inherited opt-in off (ADR 0045).

**Named layers** (ADR 0035) are user-authored shared baselines (an
employer config, a personal toolkit) slotted between the template and
the project. The project config -- or any layer -- names at most ONE
parent via `extends = "<name>"`; byre walks the pointers to the root and
merges root-first. Chains are strictly linear (no lists, so no diamonds
and no linearization rule to explain); cycles and dangling parents are
hard errors naming the loop / the exact path to create. Layers are plain
files, not packages (no version, no install verbs -- ADR 0029's
boundary), and resolve LIVE at every develop: the same trust position as
editing default.config. Attribution names the layer on every surface,
preset-review grant rows included; the `--self-edit` exclusion and the
management commands are in the configuration reference's Named layers
section.

**The project layer lives host-side, NOT in the project tree** (ADR
0003): a config inside the rw-mounted project would let the boxed agent
rewrite its own sandbox. A repo can ship a **preset** -- conventionally
`byre.preset`, a complete config proposal (ADR 0029; an in-repo
`byre.config` is refused with the rename remedy). Cloning gives you a
file, not a prompt: the proposal reaches the store only through an
explicit, host-side `byre preset apply` after its grant review, so the
boxed agent, which can rewrite the file, can never make it take effect.
The apply flow (installs, review, diff, the unattended `--yes` form of ADR 0063)
is the configuration reference's Presets section. Apply records an
`applied` marker; drift from the applied version is a passive
develop/status note, never a question.

`--self-edit` is the one announced exception to the host-side rule: the
session opens with a loud escalation warning and closes by reporting
what changed in the project store -- byre.config as a content diff (it
applies on the next develop), every other file listed as
added/changed/deleted.

Raw blocks are symmetric across both layers byre controls
(PRINCIPLES.md #3):

| layer   | nice primitives                             | raw block                           |
|---------|---------------------------------------------|-------------------------------------|
| build   | `base`, `apt`, `files`, `env` | `dockerfile_pre`, `dockerfile_post` |
| runtime | `mounts`, `volumes`, `env`                  | `run_args`                          |

byre never parses inside a raw block -- `byre status` counts the raw build
lines and flags them as not-introspected, and `byre status --full` shows
them verbatim.

**`run_args` is last-wins** (ADR 0006): byre's own flags first, `run_args`
appended last, so a raw flag can override byre's -- except the labels byre
puts on the container (identity, client pid, netns nonce, launch-record
address), all re-asserted after it.

There is no full-Dockerfile opt-out: byre either generates the build or
isn't involved (ADR 0014). A whole hand-written Dockerfile means raw
Docker, not byre.

## Skills

A skill is a portable bundle that can contribute to **any layer byre
controls**: build steps and files, runtime configuration (mounts, env,
egress declarations, network posture), agent context, and named volumes.
The manifest fields, the `[build].files` rules and every other part of
the author contract are in `docs/SKILLS.md`.

**Agents are skills** (ADR 0005). An agent skill contributes its CLI
(build), its launch command + autonomy flag, and its auth state volume
(`.claude` / `.codex` / `.gemini` / `.grok` / `.opencode`). The chassis
ENTRYPOINT is a constant launcher that execs the *selected* agent
skill's recorded command; the `agent` scalar picks which, and
**implicitly enables** that skill. More than one agent skill can be
enabled; `agent` decides the default command.

Skills are template-independent: they drop onto any *supported* base
identically ("supported" = the Debian-derived family of *The chassis*: a
skill may assume `apt`, a POSIX shell, and root at build time, nothing
more specific). Each skill gets its own insertion slot and its own Docker
layers.

Skills and templates are **packages** (ADR 0029) with three provenances
-- bundled, local, installed; `docs/SKILLS.md` covers provenance, fork,
adopt and the install lifecycle. The store carries its own guide for
host-side coding agents at `~/.byre/AGENTS.md` -- byre-owned, regenerated
whenever it differs from the running binary's copy: the map, per-entry
write rules (the consent-document rule for `projects/<id>/byre.config`
above all), and the version-control and sharing conventions.

Enabling a skill is trusting it (PRINCIPLES.md #2): skill content is
validated for legibility, not as a trust boundary. A skill's grants (a
mounted host socket, a network posture) are named by `byre status`, never
hidden -- and never blocked.

### MCP provisioning

`[[mcp]]` blocks (config layers and skill.toml alike) declare MCP servers
for the box -- **wiring, not grants** (ADR 0033, GLOSSARY): the
declarations list as configuration, while the egress a remote `url`
implies (plus declared extras) and the env NAMES a server consumes render
as attributed grants (`mcp:<name>`). The effective set is the config
cascade (later layer replaces by name) ∪ skill contributions, minus
`!name` closures (ADR 0030 semantics: a closure reaches a skill-declared
server). It bakes deterministically to **`/etc/byre/mcp.json` in every
image**, empty set included; the path and format are a stable contract
for anything that wants the set.

Delivery is injection, per agent, vouched by `[agent] mcp = "inject"`:

- claude's command carries `--mcp-config`;
- codex's is a skill-owned wrapper deriving per-invocation `-c`
  overrides from the same file;
- opencode's is the same wrapper shape building an
  `OPENCODE_CONFIG_CONTENT` env layer, which opencode deep-merges so
  byre's servers compose with user config (live-verified 2026-07-17).

byre never writes an agent's MCP state. An agent skill without an adapter
degrades honestly -- `byre status` shows declared-but-NOT-delivered plus
the baked path.

A declaration names the env vars a server consumes (`env =
["GITHUB_TOKEN"]`), never their values. The values reach the box the way
any env does -- an `env_from_host` passthrough, a credential row, or an
`[env]` literal (which bakes into the image) -- and a box's stdio servers
inherit the box env. Remote OAuth is agent-owned on the project state
volume (`claude mcp login --no-browser` works headless via URL
paste-back). `byre mcp add|remove|list` is the CLI sugar (remove is
closure-smart; `--global` targets default.config), and the config UI has
the full editor screen.

### Claude Skills delivery

`[[claude_skills]]` blocks declare **Claude Skills** (Anthropic's
agent-skill format: a directory whose root holds a `SKILL.md`) for the
box -- the same wiring-not-grants model and merge taxonomy as `[[mcp]]`
(ADR 0039): config layers replace by name, skill.toml contributions
union after, `!name` closures reach skill-declared entries, duplicates
hard-reject. Config declares a host `path` (`~/…` or absolute); a
skill.toml contributes a package-relative `from` (containment-checked
like `[build].files`).

Each declaration is validated as a Claude Skill at bake (root SKILL.md,
YAML frontmatter `name`/`description`, name match, 64-file/8 MiB bounds,
no symlinks), and the merged set bakes to
**`/etc/byre/claude-skills/.claude/skills/<name>/` in every image**,
empty set included -- claude's native discovery layout, so skills load
BARE (`/name`). Delivery is injection, vouched by `[agent] claude_skills
= "inject"`: the claude skill's command carries `--add-dir
/etc/byre/claude-skills`, and that flag is the whole adapter (no state
writes; a same-name skill the in-box agent authored into its own
`~/.claude/skills` shadows the delivered one -- box state wins).
Adapter-less agents degrade honestly in status. `byre claude-skill
add|remove|list` is the CLI sugar; the config UI has the editor screen.

### Standing instructions

`[[context]]` blocks declare **standing agent instructions** -- prose the
operator wants in front of the agent in every box the declaring layer
reaches (ADR 0043). The cascade is the scoping: `default.config` reaches
every box on the machine, a template or named layer its stack, the
project config one project. It is config-only vocabulary (no skill.toml
twin -- a skill's prose is its own `[context]` table): layers replace by
name, and `!name` removes an inherited snippet. Each declaration carries
inline `text` (the portable form for templates) or a host `file` (`~/…`
or absolute), read at bake under the skill-context size cap; a missing
file fails the develop, attributed to its declaration.

The merged prose joins the baked agent context AFTER the skill snippets,
in cascade order, and is INJECTED into the agent's session through that
agent's own vendor channel (ADR 0046), each vouched per skill with
`[agent] context = "inject"`:

| agent    | channel                              |
|----------|--------------------------------------|
| claude   | `--append-system-prompt-file`        |
| codex    | `developer_instructions`             |
| grok     | `--append-system-prompt`             |
| gemini   | include-dir memory                   |
| opencode | `instructions` config                |

No vouch means not delivered, and status says so. byre never writes an
agent-owned file to deliver prose.

Standing instructions are edited in the Instructions section of `byre
config` (prose via the `$EDITOR` handoff) or with `byre context
add|remove|list` (add with no flags opens `$EDITOR`, the git-commit
shape). They are one of three prose voices, each with its own channel:
the skill author's (`[context]` in skill.toml), the project's (in-tree
agent memory, committed and agent-writable), and the operator's
(`[[context]]`, host-side and out of the box's reach -- short of a
`--self-edit` session's explicit grant over the project layer).

## Mounts & volumes

Two mount species:

1. **host-bind** -- a host path into the box, declared in `mounts`
   (default `ro`). The project itself is the implicit one (`/workspace`,
   read-write by design). `:ro` protects another codebase from
   *modification*, not from being read. Plain Docker binds -- anything
   fancier drops to `run_args`. A mount can be **disabled**
   (`disabled = true`): it stays in the config and in `byre status`
   (marked), but produces no bind -- a switch for long-lived entries,
   distinct from `!target` removal; `mode` survives the off state.

   An ENABLED mount with an absent host path refuses the launch before
   the image is built, in one message naming every such mount; a
   disabled mount's host path may be absent. Two exceptions: under
   Docker Desktop, where a bind resolves inside the VM, an absent path
   warns instead; and a mount backing a skill's `sock_groups` socket is
   exempt on every platform -- a socket comes and goes with its daemon,
   so develop leaves the bind to the engine, warning at launch except
   under Docker Desktop.
2. **named volume** -- Docker-managed, project-scoped
   (`byre-<project_id>-<name>`), survives rebuilds. Usually contributed
   by a skill; a project can declare ad-hoc ones via `volumes`. Carries:
   - **role** -- `cache` (disposable, regenerable) or `state` (precious:
     agent auth + history). Drives lifecycle: `byre reset` warns and
     wipes; rebuild leaves volumes alone.
   - **scope** -- `project` (default) or `machine` (ADR 0017). A
     machine-scoped volume is one per user per machine
     (`byre-machine-u<uid>-<name>`) and mounts identically in every
     project's box -- the shared-auth companion skills' identity
     volumes are the canonical use. `byre status` lists them on their
     own "Shared vols" row; `reset`/`forget` never touch them and say
     so. Delete one deliberately via `byre config` -> Volume data ->
     clear, which refuses while ANY byre session runs. `seed` is
     invalid on a machine-scoped volume. (Worktree sharing is an
     identity question, not a volume one -- ADR 0009.)
   - **sharing** -- `shared` (default) or `exclusive` (ADR 0054). Scope
     says *which* boxes may see a volume; sharing says how many may hold
     it at once. `exclusive` declares single-writer data: `develop`
     reads the launch records of this project's live boxes and refuses
     (exit 3) rather than mount a volume one of them is holding, and
     refuses equally when it cannot establish that none is. Project
     scope only -- byre can only see this project's boxes.
   - **seed** (state only, optional) -- initialize a *fresh* volume from
     a host path or config literal (non-secrets only), once. A copy, not
     a shared mount; nothing flows back.

Credentials are **not seeded** (ADR 0007 -- a "not now", not doctrine:
copy-semantics breaks rotating OAuth tokens): agents log in once in the
box and the state volume persists the login per-project. `seed_prefs`
(ADR 0013) is the curated, non-secret exception for agent prefs.

The **shared-auth companion skills** (`claude-shared-auth`,
`codex-shared-auth`, `gemini-shared-auth`, `opencode-shared-auth`,
`grok-shared-auth`; ADR 0017) make one login serve every project WITHOUT
host copying: the credential lives in a machine-scoped identity volume
and byre reads nothing from the host. Codex, Gemini, and OpenCode log in
once in any box, and the credential lands in the shared volume through
symlinks. Claude uses a user-minted `claude setup-token` pasted at a
first-run prompt and exported to the agent process by a **launch env
hook** (`/etc/byre/env.d/*.sh`, sourced by the launcher after first-run
hooks, before the credential re-apply and exec, on the system PATH --
the chassis mechanism for skills that must put env into the agent
process). Grok's file sharing was retired in the field (ADR 0023); its
auth broker is ADR 0036. Each companion's readiness is a field gate
recorded in its own skill.toml, with the vendor evidence in
`AGENT-CREDENTIAL-MECHANICS.md`.

A companion whose gate has passed declares `shared_auth_for = "<agent>"`
in its skill.toml -- claude's, codex's and opencode's do (opencode's for
API-key logins; OAuth entries are unsupported and warned). The first-run
picker then asks, at every box's onboarding, whether to opt that box in;
yes puts the companion in the project's `byre.config` `skills`, the only
grant the answer makes. Save-as-default stores the answer as a favourite
-- the `[defaults] shared_auth` agent-to-companion map, which the picker
writes -- that prefills the next box's offer. The offer is skipped under
exactly two suppressions (ADR 0025): the companion already granted
machine-wide in `default.config` `skills` (a key the picker never writes
-- the answer could not matter), or `defaults.skip_questions`, the
standing instruction to configure new projects from the stored answers
unasked (which DOES apply the grant, and says so at the switch and at
develop time).

The gate-pending companions -- gemini (its API-key path is verified, its
two-box OAuth check pending) and grok (its broker rollover gate pending)
-- declare `companion_for` instead -- the pairing fact, which
nests a companion under its agent's row in the config UI without putting
anything in front of onboarding; readiness gates the offer, never the
display (ADR 0034).

## The chassis

Core's constant provision to every box. **Supported base:
Debian-derived images** -- the core block assumes `apt`, a POSIX shell,
glibc, and root at build time. Alpine/distroless/non-glibc bases are
unsupported; for those, use Docker directly (ADR 0014).

Build-time (the **core block**, constant for a given engine ownership
mode, always cached):

- Bake the dev identity into the image: the `dev` user is created at the
  host UID/GID on a rootful engine (ADR 0008) and at the generic 1000
  under rootless Podman's keep-id mode (ADR 0032), and `/home/dev` + the
  volume mount points are chowned to it, so a fresh volume inherits
  correct ownership. No runtime chown.
- Run unprivileged: `USER dev` after all root build steps; the agent
  never runs as root and there is no runtime `gosu` (it stays installed
  as a *build-only* helper for skill installs).
- Strip inherited `HEALTHCHECK`s (a base's probe could do network I/O
  before a firewall gate lands -- ADR 0011). Because the last
  `HEALTHCHECK` in a Dockerfile wins, `HEALTHCHECK NONE` is re-asserted
  at the tail alongside `USER`/`ENTRYPOINT`, so a raw skill or
  `dockerfile_post` line can't reintroduce one.
- Install the launcher as the constant ENTRYPOINT.
- Ship `byre-fetch-run <url> <interpreter>`, the build-only helper agent
  skills install their vendor CLIs with: fetch the installer to a file
  (bounded retries), refuse it unless it starts with `#!`, then run it.
  Never a `curl | sh` pipe -- a pipeline exits with the shell's status, so
  a failed fetch would run an empty script and Docker would cache a layer
  with no binary in it; the shebang check catches the download host
  answering 200 with an error page or an empty body.

Runtime constants:

- Pass named host values through via `env_from_host` -- **narrowly**,
  per key. The shipped core layer is git identity (`user.name` /
  `user.email` injected as `GIT_AUTHOR_*` / `GIT_COMMITTER_*` -- not
  your `.gitconfig`, not git credentials), `TERM` (the launching
  terminal's), and `TZ` (the host timezone: the TZ var if set, else
  the `/etc/localtime` symlink's IANA name). Sources are a closed
  scheme set: `git:<key>`, `env:<HOST_VAR>`, `tz:`, `cwd:` (the host
  directory the box's `/workspace` comes from: the project dir, and
  under `byre worktree` the worktree), and the two credential kinds
  `encrypted:` / `encrypted-file:`; an `env:` source whose host var is
  absent sets nothing. The `BYRE_` namespace is refused here as it is in
  `[env]` (ADR 0050 tier 1): the scheme set limits what a passthrough
  can SPELL, not what it can reach. Each host-sourced entry is a grant
  -- attributed in status, counted in exposure, disable-able per layer
  (`KEY = ""`). A credential row asks the host for nothing: it carries
  an age ciphertext this config file's own `[credentials]` identity
  opens, and it is delivered over the credential channel onto the
  session tmpfs rather than as `-e` (ADR 0057); it is classified
  rendered wherever it appears (ciphertext elides, values never render).
  Host env is otherwise isolated (ADR 0026).
- The launcher, in order:
  1. wait at the launch gate if a network-posture skill is enabled
     (ADR 0011);
  2. export the per-session context additions as `BYRE_SESSION_CONTEXT`
     -- under an allowlist posture, the session's enforced egress
     allowlist (the same `BYRE_EGRESS` string the netns helper applied,
     so announcement and enforcement share one source), plus the
     self-edit note when that grant is live;
  3. wait, bounded and fail-closed, for delivered credentials and export
     them (ADR 0057);
  4. run first-run hooks as the user (agent login flows live here; they
     inherit those exports, so a login hook stands down on a delivered
     key);
  5. source the launch env hooks;
  6. re-apply the credential exports so they win env collisions (ADR
     0028);
  7. exec the selected agent's command in autonomous mode.

  The launcher runs its own helpers on the system PATH
  (`/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`) from
  its first line, because the agent PATH puts the agent-writable
  `~/.local/bin` first, where repo code or the agent could plant a
  helper. It sources each launch env hook on that system PATH, re-pinned
  before each one, and gives the agent PATH back only to the first-run
  hooks and the final exec (ADR 0028). First-run hooks run under
  `/bin/bash` by absolute path, so a `bash` planted on the agent PATH is
  not the interpreter, and with fd 9 closed (descriptor hygiene: a
  descriptor the launcher inherited at that number does not pass into a
  hook it did not write).
- The agent command itself INJECTS the baked agent context (the chassis
  facts, the base image, a one-line inventory of config-provisioned
  `apt` packages when any exist -- the agent shouldn't discover tools by
  probing -- then skill snippets in enable order, then the operator's
  `[[context]]` declarations in cascade order) plus the session var,
  through its own vendor channel (ADR 0046); the launcher writes no
  agent file. The box is the safety boundary.

## Project identity

`project_id` is derived from the **canonicalized absolute path** of the
project dir: a readable slug of the last two path components plus a short
hash of the full path, e.g. `/Users/me/dev/byre -> byre-dev-0877d7`. It is
a *naming* device, not a caching one (ADR 0001): it names the image tag
(`byre-<project_id>-u<uid>-g<gid>` -- written `byre-<project_id>`
elsewhere in this doc for brevity), the container, the named volumes
(`byre-<project_id>-<name>`), the labels, and
`~/.byre/projects/<project_id>/`. A collision is not silent: byre records
each id's canonical path and fails loudly if a different path maps to an
existing id.

For a linked git worktree, identity anchors at the **main worktree's**
path (ADR 0009): config, volumes, image, and the setup lock come from the
project; the container name, `byre.workdir` label, and `/workspace` mount
stay per-worktree. `byre worktree` creation runs the repository's git
inside containers, never on the host -- registration in a short-lived,
network-less creation container, checkout at first launch (ADR 0009) --
so the hooks and filters a checkout runs stay contained.

Consequence: moving or renaming the folder yields a new id -> fresh image
and **fresh volumes**; the old volumes persist, orphaned, under the old
id. For `cache` that's a shrug; for state it strands the agent login.
`byre rehome` re-points identity after a move (refusing from a worktree,
which re-resolves automatically once git's own pointers are repaired).

## Commands

The command inventory, every flag and the exit codes are the generated
commands page, `site/content/docs/commands.md` (`byre --help` is the
same tree). The invariants every command keeps:

**No command mutates config behind your back.** Config *content* changes
only where you asked for the write: files you edit, the `byre config`
editor, the declaration verbs (`byre mcp add`/`remove`, `byre
claude-skill add`/`remove`, `byre context add`/`remove`), `byre
credentials set`/`unset` (the `[credentials]` rows of the project's
config, or with `--layer` a layer's), `byre layer new` (a stub layer
file), `byre preset apply` (after its review), `byre restore` (after its own, and only into a
project that has no config yet), and onboarding's initial write. `rehome`
and `forget` move or delete the store wholesale, never editing what's
inside; everything else -- `develop` and friends included -- only reads
it and acts. Lifecycle verbs that destroy state (`reset`, `forget`)
warn, name what dies, and refuse while a session is live; `forget` never
touches the project tree.

`byre dockerfile` and `byre dockerrun` print the generated build and an
ejection run command; they do not carry the firewall's outside-the-box
step (`byre ejectfirewall` prints it as a script) or credential delivery,
and both leave an ejected box gated to fail closed (`docs/EJECTING.md`).

### Status rendering

`byre status` is the legibility surface (PRINCIPLES.md #4): resolved
config, every grant and who granted it, network posture + egress,
volumes, raw blocks flagged as not introspected, session state. It has
three tiers. The DEFAULT page shows every row that exists -- a grant,
mount, volume, skill, port or reserved key is never elided -- and
truncates long values, marking each truncation with a count and a
`--full` pointer. It never folds a claim degradation or a containment
disclosure: those rows are the point, and they are short. `--full` is
the same page untruncated (raw build lines verbatim, passthrough
sources, package digests, the full delivery sentences). `--data` is the
same content as JSON, carrying a `version` field; it is versioned but
not frozen, and it is not a scripting interface until it says so.

Layout is byre's, not the terminal's: the row funnel wraps a long value
itself, hanging to the value column and breaking at the row grammar's
separators rather than through a path. Width is measured in terminal
CELLS, so a CJK path or byre's own two-cell 🛑 marker does not overrun
and get re-wrapped at column zero. On a terminal it lays out to the
terminal's width, clamped to [48, 160] (below the floor it lays out at
48 rather than pretending to 80); otherwise at a fixed 80, so redirected
output is the same wherever it is produced.

### Deliver and grab

`byre deliver` and its mirror `byre grab` are the **machine-scoped**
verbs: instead of deriving a project from cwd, they discover running
boxes across every installed engine by the `byre.project` label and pick
one by a fixed cascade, filtered to the caller's UID. Transport is an
`exec -i` stream into the running container (never a mount or a copy
through the engine's storage), every landing claims its final name
atomically and never overwrites, and remote delivery (`ssh://`) is one
tar stream over a single ssh exec. The user guide, the what-works-where
matrix and the transport mechanics are `docs/DELIVER.md`; decisions in ADR 0021, ADR 0037 (remote) and ADR 0040
(grab).

### Backup and restore

`byre backup` and `byre restore` move a project between machines. The
scope is deliberately narrow: the project's `byre.config` and its state
volumes, and nothing byre can rebuild (the image, which bakes this host's
UID/GID on a rootful engine or the generic 1000 under keep-id mode),
re-reference (layers, templates, skills, the agent -- these must
exist at the destination, and the file names every one it saw), or that
git already carries (the workspace). `backup` requires the project to be
completely still, previews what it will carry and what it leaves behind,
and asks; `restore` is terminal-only, refuses a project that already has
a config, and names what this machine must still install.

The file is one gzip tar: a strict TOML index first, then the config
byte-for-byte, then one plain tar per carried volume, each with its own
sha256. The digests are integrity, never authorship, and the restore
review says so.

Reading it is all refusals: a fixed member order, a hard budget on the
DECOMPRESSED stream (the one size gzip does not declare), one gzip member
with nothing after it, and a nested-tar contract every payload must meet
before any project state is written. The pour then replays a stream the
validator REBUILDS entry by entry, with ownership and the
setuid/setgid/sticky bits zeroed, so the restored tree is the source tree
under exactly one stated transformation. The contract's rules are
spelled in `internal/backup/tarcheck.go` and recorded in ADR 0059.

Both verbs read and write volume bytes through **helper containers** of
the seeding shape: `--rm`, entrypoint overridden, `-u 0:0` in the
identity's userns, no network, no host bind, no `run_args`, the volume
mounted with copy-up disabled at one per-run path. The tar environment
is pinned (`TAR_OPTIONS=` cleared, `-f -` on every tar command), because
an image's own `ENV` can otherwise change what tar does. The image is
proved before anything runs in it: GNU tar's own banner, then the verb's
COMPLETE command against a scratch directory.

Backup holds the project setup lock from the stillness sweep through the
last capture; restore holds it for the config write and every pour
together, with the review re-rendered under it and compared byte for
byte. Decisions and residuals: ADR 0059.

## Platform note

Baking the host UID/GID to yield correctly-owned files is a Linux-*host*
concern. On Docker Desktop (macOS/Windows) the file-sharing layer fakes
ownership, so the problem mostly doesn't arise and the baked UID is
harmless. Test the baked-UID ownership on a native Linux host.
