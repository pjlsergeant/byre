# MCP servers are wiring: [[mcp]] declarations, an always-baked file, agents adapt per CLI

Decided 2026-07-14/15 (a four-round design marathon with Pete: three
anchored reviews plus a greenfield round, then six spikes — spike results in
`wip/` history, absorbed here). byre gains an `[[mcp]]` vocabulary for
wiring Model Context Protocol servers into a box. Declarations are
**configuration, not grants**; the canonical declared set bakes to
`/etc/byre/mcp.json` in **every** image; **claude injects** it with
`--mcp-config` (no state writes at all); state-writing CLIs get a designed
but not-yet-built registrar protocol under a reserved `byre__` namespace.

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
(spike-verified). Guarding the secret-free claim: a url carrying
credentials (`user@host`) is refused at validation — same stance as
`env_from_host` refusing literals — while a query string stays allowed
(legitimate endpoint shapes exist) and bakes into the image exactly like
an `[env]` literal; don't put secrets in either.

## Adapters: evidence-forced special-casing

Uniform mechanism died to probe evidence; the uniform thing is the
*contract* (declared set available; removal converges; provenance
structural), not the mechanism.

**claude (v1, shipped): injection.** The claude skill's agent command
carries `--mcp-config /etc/byre/mcp.json`, and `[agent] mcp = "inject"` is
the author's vouch that it does (closed set; typos refuse at resolve).
That's the whole adapter: no list, no writes, no scopes, no lock, no
receipt. Probe-backed reasons state-reconcile was hostile on claude: no
update verb, no JSON list, a health-checking `list`, a multi-scope model
where `-s project` writes the repo tree. Spike-verified semantics: an
injected server SHADOWS a same-name user-state twin (which never spawns);
other user-state servers union in; convergence is exact per session by
construction. Costs on the record: attach-shell `claude` sessions don't see
byre's servers (the shell is not the agent session); a stale-image sibling
box injects its own old set into its own session only.

**State-writing adapters (gemini; grok if ever revived): the `byre__`
namespace protocol — DESIGNED, NOT BUILT.** First implementation ships with
whichever state-writing agent arrives first (likely the OpenCode/gemini
pass). The protocol, pinned so it isn't re-litigated: inside `byre__*` in
the adapter's pinned volume-resident scope, config wins like apt (hand
edits trampled; removal path is config); outside it byre issues no
mutations, ever. Reconcile order: authoritative JSON read → plan → adds →
diff-before-mutate updates → stale removals LAST and only after a clean
enumeration; a partial read suppresses ALL deletion. A volume-resident lock
with a declared-set generation stamp stops a stale-image sibling reverting
a newer generation; without the lock, adds-only and report degraded. A
receipt (`last_reconcile.json`) renders in status as HISTORICAL data, never
present-tense. Spiked contract facts: codex has `mcp list --json`,
overwrite-in-place add, exit-0 missing remove, and per-invocation `-c`
injection that writes nothing — **if codex becomes a selectable agent, its
adapter is injection, not the registrar**; grok has `--json` with scope
attribution, add-or-update, exit-1 missing remove, `-s user` volume scope.

**Registrar-less agents degrade** (P1: report, never block): status shows
declared-but-NOT-delivered plus the baked path as the manual wiring point.
No fabricated per-CLI commands for unprobed CLIs.

**Selection seam:** delivery keys off the SELECTED agent (the dogfood box
enables codex as a non-agent reviewer skill; nothing may run registrations
for skills that aren't the actor). The bake is unconditional regardless.

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
  works in the box's interactive terminal. Unprobed hazard, documented:
  name-reuse credential re-inheritance (a new server behind an old name
  inheriting stale creds) — under injection byre never mutates the
  credential store, so the hazard is user-edit-driven; verify at first live
  remote use.
- **claude.ai account connectors are filtered**:
  `ENABLE_CLAUDEAI_MCP_SERVERS=false` in the claude skill (verified to
  filter). Connectors are ambient host-account authority, and deny-by-
  default means the box doesn't inherit them just because the login does
  (P5). No re-enable knob in v1 — it's a consent question for when a real
  user asks; the parked runtime-env work is the likely vehicle.

## v1 scope (builder's calls, Pete may override)

claude-inject only; gemini takes the degradation path until its agent-
mechanics pass; the namespace protocol stays design. No `byre mcp add`
sugar (the declaration is three TOML lines). No config-UI editor screen —
`[[mcp]]` round-trips VERBATIM through the UI (untouched-field
preservation), hand-edit rides the UI's $EDITOR round-trip, and status is
the detailed view.

## Dead (do not re-propose; reasons in the design history)

Legibility/"read before you run" as the pitch; a from-mcp converter;
firewall-as-egress-discovery (the firewall DROPs silently BY DESIGN);
`--strict-mcp-config`; an explicit seed flag; provenance ledgers and
hash-marker offer-once (the namespace/injection make memory jobless);
purging outside the namespace; uniform-mechanism-for-uniformity's-sake;
success-tense status captions for state-writing delivery; names-only drift
detection.

## Consequences

Two mechanism stories to maintain (evidence-forced, not preference).
`byre.config` grows a structured list whose entries carry attributed
grants; every image grows one COPY layer (placed after skills/agent so an
mcp change never busts their layers). The claude skill's command string now
encodes the adapter — a future claude CLI breaking `--mcp-config` semantics
is a skill fix, not a core one. Per-server disable of a skill's MCP is
`!name`; disabling the whole skill remains the blunt instrument.
