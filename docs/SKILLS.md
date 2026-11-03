# Skills and templates as packages

How to discover, install, author, and publish byre packages. Mechanics
reference: `ARCHITECTURE.md`; decision record:
`adr/0029-skills-are-packages.md`; vocabulary: `GLOSSARY.md` (Packages).

A **package** is a skill or a template. Where it came from is its
provenance, and `byre skill list` / `byre template list` always show it:

```text
claude (byre/claude)          bundled v0.2.0     The Claude Code agent...
pjlsergeant/codereview        installed 1.0.0 (sha256:36609376...)  byre-codereview...
pete/my-linter                local              my WIP linter skill
```

- **bundled** -- shipped inside your byre binary, immutable. Display
  copies live at `~/.byre/bundled/` for reading; edits there are ignored
  (the loader reads the binary). A bundled package owns its bare name:
  `claude` always means `byre/claude`.
- **local** -- plain directories under `~/.byre/skills/` and
  `~/.byre/templates/`, yours to edit. Nest them (`pete/my-linter`) or
  keep them bare; the id defaults to the store path, and a symlinked
  directory loads like a real one.
- **installed** -- immutable snapshots fetched from a URL, hash-verified
  file by file, stored under `~/.byre/packages/`.

Whatever the provenance, **nothing runs until a box's config enables
it**. Installing is acquisition, like cloning a repo; the grant is the
`skills = [...]` / `agent = ...` / `template = ...` line in a config,
per box, as ever.

## Installing

```sh
byre skill inspect https://raw.githubusercontent.com/owner/repo/v1.0.0/skills/thing/skill.toml
byre skill install https://raw.githubusercontent.com/owner/repo/v1.0.0/skills/thing/skill.toml --digest sha256:...
```

`inspect` on a URL fetches and verifies the package and renders its full
trust surface -- contributions, grants, payload hashes, digest -- without
installing anything. `install` does the same and then snapshots it; the
review leads with the grant summary. `--digest` pins the whole package
to the bytes the person who handed you the command reviewed (a git tag
can be moved; the digest can't lie) -- published commands should carry
it, and installs record the digest either way, so a later change at the
same URL surfaces as a replacement review rather than sliding in.

Reinstalling the same id is a no-op on an identical digest, and a
**replacement review** otherwise: version and digest before/after,
payload adds/changes/removals, new or dropped grant declarations (raw
Dockerfile lines shown verbatim), and the boxes whose configs reference
the id -- replacement is machine-wide, and those boxes run the new code
at their next launch, so it always confirms (`--yes` in scripts).
Installing an id that configs already reference gets the same treatment:
that install is activation.

`byre skill uninstall <id>` lists the referencing boxes first, confirms,
and removes the snapshot; a box left referencing a missing package fails
loudly at develop with the exact reinstall command.

Sources are `https://` URLs or local paths (`file:` works too). Private
https (auth) is unsupported -- clone privately and install from the
path.

## Authoring and publishing

A local skill is a directory with a `skill.toml`; `byre skill init
<name>` scaffolds one, and `byre skill fork <id> <new-id>` copies any
immutable package into a local editable one under a new id (the fork's
provenance is a documentary comment, never read for resolution or
trust); to make a
checkout the live source for the SAME id, `byre skill adopt` it (see
Publishing below). `byre skill validate` runs the full strict
parse plus every value rule byre can judge from ONE skill.toml; broken
local packages also show as INVALID rows in `list` with the reason, and
install refuses a package that fails the same rules.

**`validate` is a partial promise, structurally.** It judges a skill
alone, and some of byre's rules are properties of a SET that only exists
once a config selects one: two skills declaring a `network_posture` or a
`netns_init`, two setting the same env key to different values, MCP or
Claude Skill names colliding across skills and config, the `agent`
scalar naming a skill that is not enabled, a `requires` entry naming one
that is not. Those are checked when the cascade resolves -- at develop
-- and no per-package command can check them earlier, because the other
packages are not in view. A green
`validate` says this skill.toml is sound, never that any particular box
will come up.

The fields a `skill.toml` can carry, in one agent skill (a non-agent
skill omits `[agent]`); the paragraphs below cover the ones with rules
worth knowing:

```toml
description = "One line shown wherever skills are listed side by side."
requires    = ["codex"]                 # skills that must also be enabled (ADR 0062)
# companion_for = "claude"              # pairing fact for the config UI (ADR 0034), or
# shared_auth_for = "claude"            # the shared-auth offer's vouch (ADR 0025); not both

[build]
apt        = ["curl", "jq"]             # hoisted above every skill block (ADR 0042)
files      = { "launch.sh" = "/usr/local/bin/my-launch" }   # skill-relative -> image path
dockerfile = ["RUN chmod +x /usr/local/bin/my-launch"]      # raw build lines, after files

[agent]                                 # marks this as an agent skill (ADR 0005)
command       = "my-agent --yolo"       # what the launcher execs
state         = ".my-agent"             # names its state volume; must be declared below
mcp           = "inject"                # vouch: the command consumes /etc/byre/mcp.json
claude_skills = "inject"                # vouch: ... /etc/byre/claude-skills
context       = "inject"                # vouch: ... the baked agent context (ADR 0046)
prefs = { from = "~/.my-agent", files = ["keybindings.json"] }  # seed_prefs candidates (ADR 0013)

[runtime]
env             = { MY_AGENT_HOME = "/home/dev/.my-agent" }   # baked literals
env_docs        = { MY_API_KEY = "where the value comes from" }
egress          = ["api.example.com"]   # what a firewall opens for it (ADR 0012/0020)
egress_offered  = ["registry.example.com"]  # closed doors the config UI offers
mounts          = []                    # host binds, as in config
run_args        = []                    # raw docker-run flags
caps            = []                    # --cap-add entries
# network_posture, netns_init: the firewall skills' enforcement hooks
# sock_groups, containment: a socket grant and its disclosed hole (docker-host)

[[volumes]]
name   = ".my-agent"
role   = "state"                        # or "cache"
target = "/home/dev/.my-agent"
# scope = "machine" / sharing = "exclusive": see below
# (credentials are never seeded: the agent logs in IN THE BOX -- ADR 0007)

# [[mcp]] and [[claude_skills]] blocks: MCP servers it wires (ADR 0033)
# and Claude Skills it ships (ADR 0039), both below

[context]
file = "agent-context.md"               # or text = "..."; appended to the agent context
```

Hooks are files, not fields: ship a script with `[build].files` to
`/etc/byre/firstrun.d/<name>` (run as the user before the agent, where
login flows live) or `/etc/byre/env.d/<name>.sh` (sourced by the
launcher to put env into the agent process; export-only, ADR 0028).

A skill ships payload files into the image with `[build].files` --
`"<source>" = "/absolute/image/path"`, e.g. `files = { "firewall.sh" =
"/usr/local/bin/byre-firewall" }`. **Sources resolve against the SKILL's
own directory**, not the project: `..` escapes and symlinks pointing out
of the skill dir are refused, and `"."` is legal and means the whole
skill directory (how a package that ships a tree stages it). They are
COPY'd before the skill's raw `[build].dockerfile` lines, so a `RUN`
there can read them. This is not the user's `[files]` config key, which
shares the name but resolves against the project and lands in the
project block, after `dockerfile_pre` has already been emitted -- the
COPY-before-raw property is yours, not theirs. A user cannot override
what your skill bakes in: the config editor shows your files on a
read-only **Skill files** screen, attributed to you, and the way to
change them is `byre skill fork`.

**An image destination takes one content** (ADR 0056). Within one
skill.toml, two entries installing to the same path are refused wherever
the skill is judged (validate, install, ingest, develop), because only
one of them can land there and which one would be map order. Across the
enabled skill SET, two *different* skills may claim the same destination
only with byte-identical content, exec bit included (byre normalizes the
rest of a file's mode at staging, git-style, so an umask difference
between two checkouts never counts as divergence); both COPYs are
emitted. Divergent content refuses the assemble at develop, naming both
skills, the destination, and what differed -- the alternative is
build-order last-writer-wins with nothing the user can read saying which
skill's file their box runs.

That carve-out is the dual-ship pattern: each skill carries the same lib
so either works alone. It is judged over the staged bytes, so keep
dual-shipped files exactly in sync -- a shared source of truth in your
repo, copied into each package at release. byre's own built-ins work that
way: five skills (`codex-shared-auth`, `gemini-shared-auth`,
`opencode-shared-auth` and the `codex` and `opencode` login skills) each
stage `byre-shared-auth-lib.sh` to `/usr/local/lib/`, which is what lets
a box enable any one of them alone, and a test pins the copies to one
canonical file in byre's tree.

A skill that reads env vars it doesn't set (an API key, a feature
toggle) can document them in `[runtime.env_docs]` -- `NAME = "one-line
guidance"` per var. Pure documentation: nothing validates or warns, but
the config editor's env screen shows each unprovided var as a dim
suggestion row attributed to your skill, and enter prefills the add
editor with the name.

A skill declares the storage it needs with `[[volumes]]` -- `name`,
`role` (`cache` or `state`), `target`. Two properties are worth deciding
rather than defaulting through. `scope = "machine"` makes it one volume
per user per machine, mounted by every project that declares it (ADR
0017); the shared-auth companions' identity volumes are the case it
exists for. `sharing = "exclusive"` declares the volume single-writer
(ADR 0054): worktree boxes of one project run **concurrently** and mount
the identical volume set, so if two holders would corrupt your data --
an embedded database, an index with no locking -- say so and `byre
develop` refuses the second box instead. The default, `sharing =
"shared"`, is the right answer for caches and for agent state
directories (agents already tolerate a shared state dir; that is what
ADR 0009 settled). Exclusive
is project scope only: byre can see this project's live boxes and no
others.

A skill can wire MCP servers into the box with `[[mcp]]` blocks (ADR
0033) -- `name` plus a local `command` argv or a remote `url`; ship a
local server's binary through the normal `[build]` machinery. List the
env var NAMES the server consumes (`env = ["GITHUB_TOKEN"]`) -- never
values; the user supplies those via `env_from_host`/`[env]`, and status
marks each name provided or not. A remote server that wants static-token
auth takes `headers = { Authorization = "Bearer ${TOKEN}" }` -- the
`${NAME}` refs expand from the box env at launch (natively or via the
agent skill's wrapper -- expansion parity is part of vouching
`mcp = "inject"`), so the token itself never enters the declaration. The
whole url (userinfo and query string included), a local command's argv,
and any literal header fragment bake into the image like an `[env]`
literal -- byre never refuses what you put there, so keep secrets out
yourself (tokens ride env names / `${NAME}` refs). A remote url's host
becomes attributed egress (`mcp:<name>`) automatically; declare extra
hosts (an OAuth authorize endpoint) in the block's own `egress`. Users
can drop one of your servers without disabling the whole skill via
`!name` in their config's mcp list. The declared set bakes to `/etc/byre/mcp.json`;
delivery into the agent session is the agent skill's job (any agent
that vouches `mcp = "inject"` in its skill.toml), so a toolkit skill
declares servers and stays agent-agnostic.

A skill can also ship **Claude Skills** (Anthropic's agent-skill format:
a directory whose root holds a `SKILL.md`) with `[[claude_skills]]`
blocks (ADR 0039) -- `name` plus `from`, a directory relative to the
skill dir:

```toml
[[claude_skills]]
name = "review-loop"                # must equal the SKILL.md frontmatter name
from = "claude-skills/review-loop"
```

The dir is validated at bake (frontmatter `name`/`description`, 64-file /
8 MiB bounds, no symlinks) and delivered to the agent session bare (the
agent sees `/review-loop`). Users can drop one of your Claude Skills
without disabling the whole skill via `!name` in their config's
claude_skills list, and standalone skills need no package at all: `byre
claude-skill add <dir>` declares a host directory straight from config.
A skill that ships ONLY Claude Skills is a fine package -- enabling it
stays the grant, like any other.

An agent skill's login hook (a first-run hook that offers an interactive
login) must not prompt when stdin is not a terminal (`[ -t 0 ]` fails):
a box launched from a script has nobody to answer. Exit 0 and print the
remedy on stderr instead -- the env key to set, or `byre shell` to log in.

A skill that does not work without another -- a wrapper that drives
codex's binary, say -- names it in a top-level `requires` list:

```toml
requires = ["codex"]
```

Each entry must already be enabled (in `skills` or as the agent); byre
never enables it for you, and an unmet requirement refuses at develop and
run with the `skills = [...]` remedy. Details and compatibility:
[ADR 0062](adr/0062-skill-requires.md).

To publish, declare identity in `[package]` -- a qualified id
(`owner/name`), a `version`, and a `requires_byre` constraint -- then:

```sh
byre skill pack owner/name -o skill.toml   # writes the payload list into the manifest
```

`pack` enumerates every file in the package directory (the primary file
itself excepted -- it cannot contain its own hash), computes sha256s and
exec bits, writes them into the `[package]` manifest of the primary
file, and prints the package digest plus a ready `install --digest`
command. Host the packed `skill.toml` with its payload files beside it
(payload paths resolve relative to it, same origin only) -- a raw GitHub
URL at a tag works as-is. Re-publishing is
edit, bump `version`, re-pack, re-tag: installers see a replacement
review of exactly what changed. Prefer `-o` over a shell redirect: `-o`
writes only after every read, so the primary file may be its own output
target; `pack id > skill.toml` truncates the target before byre runs.

Mind TOML's scoping around that header: a bare key belongs to the most
recent table header, so `files = {...}` written below `[package]` is
`package.files`, not `[build]`'s map. byre refuses a local package whose
`[package]` carries a key the table does not define, naming the key and
the move (above the header, or under its own table); `pack`, `fork` and
the `init` scaffolds place the header below your leading bare keys for
the same reason.

**Publishing from another machine (the round trip).** The catalog on a
fresh machine knows your published package only as *installed* --
immutable, not packable. `byre skill adopt <dir>` re-establishes the
source: it reads the id your repo checkout's `skill.toml` declares and
symlinks the directory into the store at that id's path, validated by a
full catalog reload (a directory that would land INVALID rolls the link
back out). Where the id is also installed -- your own preset installed
it -- the adopted copy *shadows* the snapshot: `list` shows `local
(shadows installed <version>)`, boxes build from your working copy, and
removing the link falls back to the snapshot. The release loop is then:
edit in the repo, bump `version`, `pack <id> -o <repo>/skill.toml`,
commit and tag. (ADR 0055.)

Templates get the identical verb set (`byre template ...`). Templates
are *shape* (base image, packages, egress offers, optional files copied
read-only into the image); they never reference other packages -- a
template config refuses `skills`, `agent` and `[sources]`, because
composition belongs in a preset. Named layers
(`~/.byre/layers/`, ADR 0035) are the other thing entirely: user-authored
LOCAL files, never distributed packages, and they may compose freely
(skills, agent, `[sources]`, everything but `template`).

## Presets

A repo that wants a particular box composition ships a **preset**
(conventionally `byre.preset`): a complete proposed config that the user
reviews and applies with `byre preset apply`. As a package author, what
you supply is the preset's `[sources]` table -- one `{ uri, digest }`
hint per package id, the same pinned URL and digest `pack` printed:

```toml
skills = ["owner/linter"]

[sources]
"owner/linter" = { uri = "https://raw.githubusercontent.com/owner/linter/v1.0.0/skill.toml", digest = "sha256:..." }
```

byre never fetches from a hint on its own: `preset apply` walks the user
through each missing install with its own review, and anywhere byre
reports a missing package it prints the exact install command from the
hint. A preset meant for unattended `byre preset apply --yes` must pin
every https source with a digest. The apply flow and its review are in the
configuration reference (`site/content/docs/configuration-reference.md`,
Presets).
