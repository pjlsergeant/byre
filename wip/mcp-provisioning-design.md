# MCP provisioning -- round 4: the complete picture

Status: post-review synthesis. Round 3 (namespace convergence, uniform) was
reviewed by codex (flawed), grok (good with reservations), and a
code-verifying subagent (good with reservations). All three agreed the
ownership architecture is right and the mechanism contract was underspecified;
their probe evidence forced one structural change (maintainer-approved):
**claude special-cases to injection**. This document is the whole design.

## The contract (uniform across agents)

The declared MCP set is available to the agent session byre launches;
removing a declaration converges (the server leaves byre's provisioned
surface); provenance is structural, never bookkept. HOW an adapter delivers
that is per-agent, chosen on CLI evidence -- the context_target precedent:
one declaration model, adapter-private mechanics.

## 1. Declaration

```toml
[[mcp]]
name = "github"                            # grammar: [a-z0-9][a-z0-9-]*;
command = ["github-mcp-server", "stdio"]   #   names starting "byre__" REJECTED
env = ["GITHUB_TOKEN"]                     # names only, never values

[[mcp]]
name = "linear"
url = "https://mcp.linear.app/mcp"
egress = ["auth.linear.app"]               # optional extra hosts
```

- Two homes: `byre.config` layers and skill.toml contributions.
- **Merge taxonomy (RULED):** within config layers, later layer replaces
  by name -- normal cascade. Skill contributions union AFTER the merge.
  `[[mcp]]` `!name` adopts the ADR 0030 closure semantic WHOLESALE: a
  closure is kept through the merge (never consumed) and subtracts after
  the skill union -- one rule, effective against config-declared and
  skill-declared servers alike. So "this skill, minus one of its
  servers" works from day one (maintainer: pure-MCP toolkit skills make
  per-name disable real immediately, unlike egress where the need was
  discovered post-hoc). Duplicate ACTIVE declarations across sources
  (config+skill, skill+skill): hard reject, unchanged.
- Self-discriminating local (command) vs remote (url); no transport field.
- Pure wiring: binaries via existing build machinery. Two stanzas for a
  config-only local server, accepted.
- Sugar, maybe-v1: `byre mcp add <url>` writes the block into config.

## 2. Ingredients (settled, unchanged from r2/r3)

- Tokens: env NAMES in declarations; values via `env_from_host`/`[env]`
  (attributed grants). **Fallback policy (RULED, three tiers):**
  (1) prefer by-name indirection where the CLI has it (`${VAR}`
  expansion, `--bearer-token-env-var`) -- functionally better, not just
  purer: by-name picks up a rotated token automatically, a literal copy
  goes stale; (2) where the CLI only takes literals, the registrar
  resolves the value and registers it, with one disclosure line -- the
  agent's volume-resident config already holds the agent's own OAuth
  tokens, so this is the same class of storage, agent-owned, cleared by
  `byre reset` (docker-host warranty shape: disclose, don't refuse;
  refusing a registration the user explicitly declared would be
  nannying); (3) hard lines, few and absolute: never into image layers,
  never into byre-owned files, never into the repo tree.
- Remote OAuth: agent-owned; per-project via volume scoping. **Pinned
  (carried from r2, dropped twice): shared-auth skills share inference
  identity ONLY; MCP tokens stay on the project volume, or status
  degrades "MCP auth scope unknown."**
- Egress: remote URL host = implied, attributed (`mcp:linear`), closable.
  OAuth side-hosts never derived -- declared or discovered via visible
  failure, documented as manual troubleshooting that may only show a
  timeout (firewall DROPs silently, ADR 0030). Local server with no
  declared egress under a firewall = first-class "unknown outbound"
  status row. **Status coupling (codex r3): a `!host` closing a required
  MCP endpoint renders ON the MCP row ("declared; endpoint closed; not
  operational"), not as a distant closure row.**
- claude account connectors: `ENABLE_CLAUDEAI_MCP_SERVERS = "false"` in
  the claude skill (verified to filter). **OPEN, two unresolved
  mechanics, options named:** (a) the re-enable path -- config `[env]`
  bakes as image ENV and loses to skill runtime `-e` (needs the parked
  runtime-env work or a skill-side knob); (b) the warning's owner --
  core can print at develop but must not know the var; the claude
  adapter knows the var but only has launch output. Candidate: a
  skill-declared "warn-if-env" generic in core vocabulary. Maintainer
  decision needed.

## 3. Adapters

### 3a. claude (v1): INJECTION -- no state writes at all

Core bakes the canonical declared set to `/etc/byre/mcp.json`
(deterministic, golden-tested). The claude skill's agent command carries
`--mcp-config /etc/byre/mcp.json`. That's the whole adapter.

What this dissolves (probe-backed reasons it was forced): claude has no
update verb (add-on-existing errors, so state-drift-fix would be
remove-then-add with a crash window), no JSON list, a `list` that
health-checks EVERY server including the user's own (foreign side
effects + latency every launch), a multi-scope model where unscoped
`remove` errors or lands in the wrong scope and `-s project` writes the
repo tree, and `mcp__<server>__<tool>` tool-ID encoding that makes
`byre__` prefixes ambiguous. Injection needs none of it: no list, no
writes, no scopes, no prefix, no lock, no receipt. Convergence is exact
per session by construction; a stale-image sibling box injects its own
old set into its own session only (no cross-box ping-pong). Disable a
skill -> rebuild -> the file no longer carries it.

Costs on the record: attach-shell `claude` sessions don't see byre's
servers (documented; the shell is not the agent session). Spikes:
same-name shadowing between injected config and the user's state-volume
entries; `${VAR}` expansion inside `--mcp-config` files; env inheritance
to spawned stdio servers.

### 3b. State-writing adapters (grok, gemini; codex if it doesn't inject):
the `byre__` namespace protocol

For CLIs without an injection seam. The maintainer's ownership ruling
stands: inside `byre__*`, config wins, like apt -- hand edits trampled,
removal path is config. Outside: byre issues no mutations, ever. The
protocol, now with the reviews' teeth:

- **Honest boundary sentence (all three reviewers):** "byre issues
  mutations only for `byre__*` names in the adapter's pinned
  volume-resident scope; the agent CLI owns the file it rewrites." The
  CLI's whole-file rewrite is the vehicle; byre can't warrant more.
- **Scope pinning is part of the ownership rule, per-CLI:**
  volume-resident scopes only (grok: `user`, never `project` -- writes
  the workspace; codex: config.toml under CODEX_HOME on the volume).
  Enumerate the pinned scope, not a merged view. A `byre__*` entry
  found in a forbidden scope is EXTERNAL: reported, never touched.
- **Reconcile order + evidence bar (codex):** authoritative read
  (JSON where available -- codex and grok have `--json`) -> plan, no
  writes -> adds -> updates (diff-before-mutate: no write when equal;
  codex re-add overwrites, grok add-or-updates -- no remove-then-add
  needed on either) -> stale removals LAST, and only if enumeration
  completed cleanly and every add/update succeeded -> post-read verify.
  A partial or failed read suppresses ALL deletion.
- **Concurrency (all three):** a volume-resident lock around
  list->mutate->verify, carrying a declared-set generation stamp so a
  stale-image sibling cannot revert a newer generation. Timeout and
  stale-lock behavior specified. Without the lock taken, the adapter
  does adds only (never deletes) and reports degraded.
- **Receipt:** the adapter writes `last_reconcile.json` (timestamp,
  generation, per-name result) to the volume; `byre status` renders it
  as HISTORICAL last-attempt data, never present-tense.

### 3c. Selection seam (subagent finding 2, verified; RULED)

firstrun.d hooks run for EVERY enabled skill -- and byre's own dogfood
box enables codex as a non-agent reviewer skill beside claude. So the
ACTOR keys off the selected agent: core bakes a selected-agent
registrar pointer (the `agent-cmd`/`context_target` pattern) and ONE
core-owned firstrun hook invokes it. Non-selected agent skills get no
registrations run FOR them -- but `/etc/byre/mcp.json` is baked
UNCONDITIONALLY whenever `[[mcp]]`s are declared, regardless of agent
or adapter (maintainer ruling: it's secret-free data; anyone who wants
the codex reviewer or a non-byre tool wired to it points it at the
file themselves). No opt-in knob exists or is needed -- there is no
gate, only an actor byre doesn't run for you. Consequence: the file's
path and format are a quasi-public contract -- pinned, golden-tested
like Dockerfile output, format changes are versioned decisions.
Registrar-less agent skills degrade: status shows
declared-but-not-registered plus exact manual commands (pinning scope
AND cwd, since claude keys local scope by project path).

## 4. Status -- MCPs are wiring, not grants (maintainer ruling)

**Vocabulary ruling (goes to GLOSSARY at ship):** an MCP declaration is
WIRING -- configuration, like a package -- not a grant. A stdio server is
a process (nothing bash lacks); a remote one reaches nothing the
firewall doesn't allow. What's real are the grants it CARRIES: its
egress, its token passthrough. Those render where grants always render,
attributed `mcp:<name>` -- that attribution is the entire coupling.

- Declared `[[mcp]]`s list as CONFIGURATION (near packages/volumes),
  never as grant rows; they contribute ZERO to the exposure line except
  via carried egress/env, already counted in place.
- claude/inject: static truth -- "the launched agent session receives:
  github, linear" (deterministic from image).
- State-writing: config-application reporting, not grant honesty
  machinery -- desired-state caption ("attempts convergence at launch;
  success is not observable here") + the historical receipt. Same
  mechanics as before, correctly low stakes.
- Endpoint-closed coupling reads as "this wiring's carried egress is
  closed; not operational" -- a why-it-won't-work note, not a grant
  conflict.
- Never an inventory claim: connectors, plugins, repo `.mcp.json`,
  hand adds exist by design and byre does not see them.

## Accepted costs (consolidated)

Two mechanism stories (evidence-forced, not preference); attach-shell
gap on inject agents; per-CLI registrar contracts to maintain; OAuth
re-auth after remote declaration changes; injected/user same-name
semantics agent-decided (spiked, then documented); v1 has no per-server
override of a skill's MCP short of disabling the skill.

## Dead (unchanged from r3, plus)

Everything in round 3's dead list, PLUS: uniform-mechanism-for-
uniformity's-sake (killed by probe evidence); success-tense status
captions; names-only drift detection (structurally impossible);
hash-marker offer-once (namespace/inject made memory jobless -- though
the state-writing receipt IS a bounded memory with a concurrency job,
conceded to codex).

## Spikes before ADR (merged, deduped)

1. **claude inject:** `${VAR}` expansion in `--mcp-config` files; env
   inheritance to stdio servers; same-name shadow semantics vs state
   volume; `--mcp-config` + user state union behavior.
2. **Per-CLI registrar contract table** (state-writing adapters): pinned
   scope, update idiom, JSON list, remove disambiguation, out-of-scope
   `byre__*` handling, `byre__` name acceptance (probed yes on
   claude/codex/grok; gemini unprobed), tool-ID encoding.
3. **OAuth lifecycle per CLI:** binding survival across update;
   token orphaning on remove; **name-reuse re-inheritance** (new server
   behind an old name inheriting stale credentials -- the nasty
   direction).
4. **codex adapter choice:** `-c` injection (quoting hazards) vs
   namespace reconcile (friendly CLI: JSON list, idempotent remove,
   re-add overwrites). Pick on spike results.
5. **Headless remote OAuth in a box:** callback reachability
   (localhost listener inside the container vs host browser),
   side-host egress. Go/no-go for the remote story's UX claims.
6. **ENABLE_CLAUDEAI_MCP_SERVERS:** stability detection; the
   re-enable/warning mechanics (sec. 2, maintainer decision).

## Open maintainer decisions

- The connectors re-enable/warning mechanism (options in sec. 2).
- Whether `byre mcp add <url>` sugar makes v1.
- v1 adapter scope: claude-inject only, or claude + codex.

RESOLVED 2026-07-15: `!name` = ADR-0030 closures wholesale (sec. 1);
mcp.json always baked, no opt-in gate (sec. 3c); token literals into
volume-resident agent config allowed with disclosure (sec. 2).

## Spike results (2026-07-15, this box: claude 2.1.209, codex 0.144.3, grok 0.2.101)

1. **claude inject: GO, semantics ideal.** Headless `-p --mcp-config file`
   connects and serves tool calls. `${VAR}` expands inside the config file's
   `env` values, `${VAR:-default}` too -- tier-1 by-name indirection works
   for claude. Spawned stdio servers inherit the FULL agent process env
   (probe: ambient var not named in config present in server env), so
   env_from_host values reach servers with no stanza at all. Same-name
   shadowing: the injected entry WINS and the user-state twin never spawns
   (its env-dump file was not written); other user-state servers still union
   in. Exactly the apt-like semantics the design wanted, for free.
2. **Registrar contract table** (state-writing protocol, if/when needed):
   - codex: `mcp list --json` (authoritative, per-CODEX_HOME scope), add
     overwrites in place (no remove-then-add), remove of a missing name
     exits 0, `byre__` accepted. State = CODEX_HOME/config.toml. Also has
     `env_vars` (pass-by-NAME env list) -- tier-1 indirection.
   - grok: `mcp list --json` WITH scope attribution (`"scope": "user"`),
     add-or-update in place, remove of a missing name exits 1 (registrar
     must tolerate), `-s user` pins the volume-resident scope. `byre__`
     accepted.
   - gemini: NOT probed (CLI absent from this box); its adapter waits for
     the OpenCode/gemini agent-mechanics pass.
   - Residue note: the design-night probe's `byre__test` entry was still in
     this box's ~/.grok user config (/bin/false); removed 2026-07-15.
3. **OAuth lifecycle: partially open.** claude has per-name `mcp login` /
   `mcp logout` verbs. Name-reuse re-inheritance (new server behind an old
   name inheriting stale creds) is NOT probeable without a real OAuth
   account -- documented hazard, verify at first live remote use. Under
   claude-inject byre never mutates the credential store, so the hazard is
   user-edit-driven, not byre-driven.
4. **codex adapter choice: injection WINS.** `-c 'mcp_servers.<name>...'`
   overrides are honored per-invocation and write NOTHING to config.toml
   (verified: override visible in `mcp list --json`, state file untouched).
   If/when codex becomes a selectable agent, its adapter is static `-c`
   flags baked into the agent command -- no registrar, no namespace, no lock.
5. **Headless remote OAuth: GO.** `claude mcp login --no-browser <name>`
   prints the authorization URL and prompts for the PASTED redirect URL --
   the localhost:randport callback listener never needs host reachability.
   Requires an interactive terminal (the box session is one; headless -p is
   not). For linear the authorize endpoint is on the declared host itself;
   side-hosts remain declare-or-troubleshoot per the design.
6. **ENABLE_CLAUDEAI_MCP_SERVERS:** design-night verification stands (var
   filters account connectors). Today's scratch identity carries no
   connectors, so re-probe was vacuous; the var is harmless when nothing is
   filtered.

## v1 scope (builder's calls, recorded for Pete's override)

- **Adapters: claude-inject only.** grok-as-agent is retired (ADR 0023);
  gemini is unprobed and takes the registrar-less degradation path (status:
  declared-but-not-registered + exact manual commands); codex isn't a
  selectable agent today -- when it is, injection is the adapter (spike 4).
  The byre__ namespace protocol stays DESIGN (sec. 3b), first implemented
  with whichever state-writing adapter arrives first.
- **Connectors: filtered, no v1 re-enable knob.** ENABLE_CLAUDEAI_MCP_SERVERS
  =false in the claude skill; account connectors are ambient host-account
  authority, and deny-by-default means closed -- the knob is a consent
  question for when a user asks. Documented, not warned.
- **`byre mcp add` sugar: not in v1.** The declaration is three TOML lines.
