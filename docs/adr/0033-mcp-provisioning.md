# MCP servers are wiring: [[mcp]] declarations, an always-baked file, injection-only adapters

Decided 2026-07-14/15 (a four-round design marathon with Pete: three
anchored reviews plus a greenfield round, then six spikes — spike results in
`wip/` history, absorbed here; amended same-day after a scope grilling, see
"The registrar that wasn't" below). byre gains an `[[mcp]]` vocabulary for
wiring Model Context Protocol servers into a box. Declarations are
**configuration, not grants**; the canonical declared set bakes to
`/etc/byre/mcp.json` in **every** image; adapters are **injection-only** —
claude via `--mcp-config`, codex via a skill-owned wrapper deriving `-c`
overrides — and byre never writes an agent's MCP state, ever.

Principles: legibility (P4) drives the attribution and status surfaces;
opinion-free core (P2) keeps per-CLI mechanics in agent skills; box-scoped
consent (P5) rules the connectors default and the token tiers; the footgun
doctrine (P1) rules the degradation shapes (report, never block).

## The problem

Agent CLIs each have private, stateful MCP registries (volume-resident
config, multiple scopes, health-checking list commands). Users want a box's
MCP servers declared in byre's config cascade like everything else: visible,
attributed, removable, and converging — removing a declaration must actually
remove the server from what byre provisions. And tokens must not migrate
into byre-owned files.

## The model

**An MCP declaration is WIRING, not a grant** (GLOSSARY). A stdio server is
a process — nothing bash lacks; a remote one reaches nothing the firewall
doesn't allow. What's real are the grants a declaration *carries*: implied
egress and consumed env. Those render where grants always render, attributed
`mcp:<name>`; the declarations themselves list as configuration and
contribute zero to the exposure line.

```toml
[[mcp]]
name = "github"                            # [a-z0-9][a-z0-9-]* — no underscore,
command = ["github-mcp-server", "stdio"]   #   so the byre__ namespace is unreachable
env = ["GITHUB_TOKEN"]                     # var NAMES only, never values

[[mcp]]
name = "linear"
url = "https://mcp.linear.app/mcp"         # host = implied, attributed, closable egress
egress = ["auth.linear.app"]               # extra hosts (e.g. OAuth side-host)
```

- **Two homes, one merge taxonomy.** Config layers replace by name (normal
  cascade); skill contributions union AFTER the merge. `!name` adopts the
  ADR 0030 closure semantics wholesale: kept through the merge
  (`MCPClosed`), subtracting after the skill union — "this skill, minus one
  of its servers" works day one. Duplicate ACTIVE names across sources hard-
  reject, both claimants named.
- **Self-discriminating.** `command` = local stdio, `url` = remote
  streamable HTTP; no transport field. Pure wiring: binaries arrive via the
  existing build machinery; byre installs nothing from an `[[mcp]]`.
- **Tokens are names.** `env` lists what the server consumes; values arrive
  via `env_from_host`/`[env]` (attributed grants). Spike-verified: claude's
  stdio servers inherit the full agent process env, so a provided name
  reaches the server with no further wiring, and `${VAR}` expands inside
  injected config for the day another consumer needs it. Fallback tiers for
  future state-writing adapters (ruled): (1) prefer by-name indirection
  (rotation-proof); (2) literals into VOLUME-RESIDENT agent config allowed
  with one disclosure line (the agent's config already holds its own OAuth
  tokens — refusing would be nannying); (3) hard lines: never into image
  layers, byre-owned files, or the repo tree.

## The bake: /etc/byre/mcp.json, always

Core renders the effective set (config ∪ skills − closures, one owner:
`skills.MCPSet`) to `/etc/byre/mcp.json` in **every** image — empty set
included (`{"mcpServers": {}}`), no opt-in gate (ruled: it's secret-free
data; there is no gate, only an actor byre doesn't run for you). The path
and format are a quasi-public contract: golden-tested like the Dockerfile,
format changes are versioned decisions. Env stanzas are deliberately absent
from the render — inheritance delivers values, and a rendered `${VAR}` for
an UNSET var passes the literal through as a garbage credential
(spike-verified). The secret-free claim means byre puts no secrets of its
own making in the file; what the USER writes into a url (userinfo, query
string) or a command's argv is theirs and bakes into the image exactly
like an `[env]` literal — `docker history` shows it, `byre mcp add` says
so, and nothing refuses it (footgun doctrine: the threat model is the
agent, never the user; a basic-auth url — a self-hosted MCP behind a
reverse proxy — is a real shape with no alternative spelling. A userinfo
refusal shipped briefly out of a review round and was walked back by
maintainer ruling). Tokens are still better ridden as env names.

## Adapters: injection-only

Uniform mechanism died to probe evidence; the uniform thing is the
*contract* (the baked file; declared set available; removal converges;
provenance structural), not the mechanism. `[agent] mcp = "inject"` is a
skill author's vouch that the agent command consumes the baked file
(closed set; typos refuse at resolve).

**claude (shipped): file injection.** The claude skill's agent command
carries `--mcp-config /etc/byre/mcp.json`. That's the whole adapter: no
list, no writes, no scopes, no lock, no receipt. Probe-backed reasons
state-reconcile was hostile on claude: no update verb, no JSON list, a
health-checking `list`, a multi-scope model where `-s project` writes the
repo tree. Spike-verified semantics: an injected server SHADOWS a
same-name user-state twin (which never spawns); other user-state servers
union in; convergence is exact per session by construction.

**codex (shipped): flag injection via a skill-owned wrapper.** Codex has
no config-file flag but honors per-invocation `-c mcp_servers.*` overrides
— live-verified state-free on 0.144.3, full tool-call round trip. The
flags vary with the declared set, so a static skill.toml command can't
carry them; instead the codex skill ships `byre-codex-mcp-launch`, which
reads the baked file at launch, derives the `-c` overrides, and execs
codex. Core stays opinion-free (P2): the CLI-specific syntax and quoting
live in the CLI's own skill, and the baked file is the adapter contract.
One probe-forced wrinkle: codex passes MCP servers a SCRUBBED env (unlike
claude's full inheritance), so declared env NAMES ride the file's
`x_byre_env` extension key (claude ignores it — probe-verified) and the
wrapper forwards them via codex's `env_vars` by-name passthrough.

**Adapter-less agents degrade** (P1: report, never block): status shows
declared-but-NOT-delivered plus the baked path as the manual wiring point.
No fabricated per-CLI commands for unprobed CLIs. grok has no injection
seam on its current launch surface (probed 0.2.101; `grok setup` and
config layering unprobed); gemini's CLI is unprobed entirely and may
inject for free — both degrade until evidence arrives, likely with the
OpenCode/gemini agent-mechanics pass.

**Costs on the record for both adapters:** attach-shell agent sessions
don't see byre's servers (the shell is not the agent session); a
stale-image sibling box injects its own old set into its own session only.

## The registrar that wasn't (byre__ namespacing, walked back)

The design rounds produced a full state-writing reconcile protocol under a
reserved `byre__` namespace: pinned volume-resident scopes, authoritative
JSON reads, diff-before-mutate, stale deletions last and only after clean
enumeration, a volume lock carrying a declared-set generation stamp, a
historical receipt rendered by status. It was review-hardened by three
independent reviewers and pinned "so it isn't re-litigated."

**Why we had it:** the design night's probes made claude look like the
injection *exception* — the one CLI whose state verbs were too hostile to
reconcile — so state-writing looked like the GENERAL case, and safe
convergence in a file the user also hand-edits needs structural provenance:
the namespace was load-bearing (the alternatives — a provenance ledger,
never-deleting, owning the whole table — were each killed on their own
demerits).

**Why we could discard it (ruled 2026-07-15):** same-day spikes flipped
the picture. Codex — the protocol's friendliest customer — turned out to
inject state-free via `-c`, proven with a live tool call. Claude alone was
weak coverage; claude + codex is dominant, and gemini may inject for free
once probed. That left the protocol one confirmed customer (grok) and a
known-unsound piece (the generation stamp has no monotonic source). A
review-hardened protocol with at most one consumer and a hole is exactly
the machinery proportionality says not to pin. So: **byre's MCP
architecture is injection-only.** Outside its own baked file, byre never
mutates agent MCP state. If a demanded agent someday truly cannot inject,
the state-writing design gets re-derived from that CLI's live facts — this
section is the map of what was learned, not a spec to build. The `[[mcp]]`
name grammar (no underscores) keeps `byre__` structurally unreachable, so
the namespace stays reserved for free.

**Selection seam (unchanged):** delivery keys off the SELECTED agent (the
dogfood box enables codex as a non-agent reviewer skill beside claude —
and both claude and codex are selectable agents; nothing runs adapter
work for skills that aren't the actor). The bake is unconditional
regardless.

## Egress, OAuth, connectors

- **Remote URL host = implied egress**, attributed `mcp:<name>`, closable
  like any entry; a `!host` closing a required endpoint renders ON the MCP
  row ("endpoint closed — not operational"), claimed only where closures are
  enforced. Declared `egress` extras ride the same attribution. A local
  server with no declared egress under an allowlist posture gets a
  first-class "outbound unknown" note. OAuth side-hosts are never derived:
  declared or discovered via visible failure (which may be only a timeout —
  the firewall DROPs silently by design, ADR 0030).
- **Remote OAuth is agent-owned, per-project** via volume scoping. Pinned:
  shared-auth skills share inference identity ONLY; MCP tokens stay on the
  project volume. Headless OAuth is GO (spike): `claude mcp login
  --no-browser` prints the authorization URL and accepts the pasted
  redirect URL — the localhost callback never needs host reachability;
  works in the box's interactive terminal. LIVE-VERIFIED against a real
  OAuth MCP (api.agentblocks.ai, 2026-07-15): claude stores MCP tokens in
  `.credentials.json` under a top-level `mcpOAuth` key — SEPARATE from
  the `claudeAiOauth` inference login, and in a shared-token box the file
  is born mcpOAuth-only; tokens carry a refreshToken and persist on the
  `.claude` volume; an INJECTED declaration picks them up by server URL
  in a fresh config dir (auth-once-per-project holds for byre's
  delivery); entries are keyed `name|<hash>` with the serverUrl stored,
  which closes the name-reuse re-inheritance hazard by construction.
  Interplay fix the verification forced: the claude-shared-auth firstrun
  hook's stale-login remediation now detects the actual hijacker
  (`claudeAiOauth`) instead of file presence — an mcpOAuth-only file is
  healthy MCP state the offer must never move aside.
- **claude.ai account connectors are filtered**:
  `ENABLE_CLAUDEAI_MCP_SERVERS=false` in the claude skill (verified to
  filter). Connectors are ambient host-account authority, and deny-by-
  default means the box doesn't inherit them just because the login does
  (P5). No re-enable knob in v1 — it's a consent question for when a real
  user asks; the parked runtime-env work is the likely vehicle.

## v1 scope (grilled and ruled, 2026-07-15)

Adapters: claude + codex (injection); grok and gemini degrade honestly
until injection evidence arrives. Account connectors: filtered, silently,
no re-enable knob (a consent question for when a real user asks).
`byre mcp add/remove/list` ships (`--global` targets default.config;
`remove` is closure-smart). The config UI gets a full `[[mcp]]` editor
screen — the earlier no-screen call was schedule dressed as scope, and the
cockpit doesn't get to omit a config class every sibling class has.

## Dead (do not re-propose; reasons in the design history)

Legibility/"read before you run" as the pitch; a from-mcp converter;
firewall-as-egress-discovery (the firewall DROPs silently BY DESIGN);
`--strict-mcp-config`; an explicit seed flag; provenance ledgers and
hash-marker offer-once (injection makes memory jobless); purging outside a
namespace; uniform-mechanism-for-uniformity's-sake; success-tense status
captions; names-only drift detection; and the `byre__` state-writing
registrar itself (walked back above — re-derive from live CLI facts if a
demanded agent ever truly cannot inject; do not build the recorded sketch).

## Consequences

One mechanism story (injection), two deliveries (a flag, a wrapper).
`byre.config` grows a structured list whose entries carry attributed
grants; every image grows one COPY layer (placed after skills/agent so an
mcp change never busts their layers). The agent skills' command strings now
encode their adapters — a future CLI breaking `--mcp-config` or `-c`
semantics is a skill fix, not a core one. The baked file's format is a
two-consumer contract (claude parses it, the codex wrapper derives from
it), pinned by tests on both sides. Per-server disable of a skill's MCP is
`!name`; disabling the whole skill remains the blunt instrument.
