# Agent context is injected; byre never writes an agent-owned file

Decided 2026-07-26, maintainer-diagnosed ("we've made a terrible mistake
with the design of byre"). The agent context -- chassis facts, skill
snippets, `[[context]]` standing instructions -- now reaches every agent
by INJECTION through that agent's own vendor channel, exactly as MCP
servers (ADR 0033) and Claude Skills (ADR 0039) already did. The
`context_target` mechanism is retired: byre no longer writes into any
file an agent or its user owns, ever.

Principles: P4 (a delivery claim status can't stand behind is degraded,
never silently asserted); P7's custody line from ADR 0044, which this
decision finally applies to the file that needed it most.

## The mistake

`context_target` had each agent skill declare where its agent "reads
memory" -- `~/.claude/CLAUDE.md`, `$GROK_HOME/AGENTS.md`,
`~/.gemini/GEMINI.md`, `~/.config/opencode/AGENTS.md`,
`$CODEX_HOME/AGENTS.md` -- and the launcher delivered byre's prose by
`rm -f` + rewrite of that file, every launch, unconditionally (the
chassis paragraph makes the context non-empty on every box).

Follow the ownership: in every one of those agents' semantics, that
path is the USER'S file -- the personal, cross-project instruction slot
the human fills in. The repo's own instruction file (`/workspace/
CLAUDE.md`, a repo AGENTS.md) belongs to the REPO; byre rightly never
touched those. byre never had a file of its own in any agent's
hierarchy -- it expropriated the user's slot, documented it as "byre's
channel", and deleted whatever anyone else put there on every relaunch.
The same week ADR 0044 made "never clobber a shared-custody file" the
doctrine for config files, the flagship feature shipped on a delivery
pipe that clobbered the most shared-custody file in the system.
(Agents' own memory SYSTEMS -- e.g. Claude's per-project memory
directories -- were never in that file, which is why the damage stayed
invisible: the destroyed writes were the rare deliberate ones.)

It was also the one delivery left that violated ADR 0033's posture.
MCP: baked file + flag. Claude Skills: baked tree + flag. Context: a
state write. Three channels, two doctrines.

## The decision

One rule: **byre speaks through its own channel or not at all.** The
baked `/etc/byre/agent-context.md` stays the canonical artifact (bake
composition unchanged: chassis, base, provisioned inventory, skill
snippets, `[[context]]` in cascade order). Delivery is the agent
command's business, vouched per skill with `[agent] context = "inject"`
-- the mcp/claude_skills pattern, closed set, typos rejected. No vouch =
not delivered, and `byre status` / `byre context list` say so
(declared-but-NOT-delivered with the baked path), never a fallback
write.

Per-session dynamic content -- the enforced egress allowlist
announcement and the `--self-edit` note -- moves from the launcher's
file write to an exported `BYRE_SESSION_CONTEXT` env var (always set,
possibly empty, leading-separated so adapters can concatenate it
directly onto the baked text).

## The adapters (each source-verified against the vendor tree)

- **claude** -- the launch wrapper (`byre-claude-launch`) merges the baked
  `/etc/byre/agent-context.md` with `$BYRE_SESSION_CONTEXT` into one temp
  file passed as a single `--append-system-prompt-file`. Originally the
  two flags rode side by side (`--append-system-prompt-file` plus
  `--append-system-prompt "$BYRE_SESSION_CONTEXT"`, the pair verified
  against 2.1.220), but later CLIs reject the combination ("Cannot use
  both", field-hit 2026-07-29) -- and an EMPTY second flag slips that
  check, so the pair only died on boxes with session additions
  (`--self-edit`, a firewall) -- hence the merge. System-prompt append is
  arguably the RIGHT authority level for standing instructions anyway.
- **codex** -- the launch wrapper adds
  `-c developer_instructions="<baked>+<session>"`: a stable config key
  emitted as a separate developer-role message, APPENDED
  (`base_instructions` and AGENTS.md discovery untouched). The
  multi-line value deliberately fails `-c`'s TOML parse and lands as a
  literal string. One collision noted: a user's own config.toml
  `developer_instructions` would be overridden by the CLI layer.
- **grok** -- `--append-system-prompt` (alias of `--rules`): wrapped in
  `<human_rules>` and appended after the default prompt. Applied on
  fresh session spawns, not `--resume` of persisted sessions -- byre
  launches are fresh, so inert here.
- **gemini** -- no append flag exists and `context.fileName` is
  filename-only, so the channel is memory discovery:
  `--include-directories /etc/byre/context`, a dedicated dir whose
  `GEMINI.md` is a skill-baked SYMLINK to the baked context. Include
  dirs CONCAT with the user's own; folder trust is already forced by
  `GEMINI_CLI_TRUST_WORKSPACE` (the box is the trust boundary).
- **opencode** -- the launch wrapper's `OPENCODE_CONFIG_CONTENT` gains
  `instructions: ["/etc/byre/agent-context.md"]`; opencode appends
  instruction files to the system context and CONCATs the arrays across
  config layers, so user entries survive.

Recorded gaps, deliberate: the file-path channels (gemini, opencode)
carry the baked text only -- the per-session additions don't reach
them; claude/codex/grok get both. The announcements are informational,
and a file-write fallback would resurrect the buried mechanism.

## What died

The launcher's placement block (`rm -f` + rewrite), the
`agent-context-target` bake (pointer file, gen input, COPY), the
DevHome containment validation that existed only to bound the
placement, and every skill's `context_target` declaration. The RETIRED
key still parses so a pre-0046 installed agent skill loads -- it
confers nothing (no vouch, no validation, no writes); status reports
such an agent as not-delivered. The self-edit note now bakes whenever
any agent launches and rides the session var.

## Consequences

- Both instruction files in every agent's hierarchy revert to their
  owners: the repo's to the repo, the user's to the user. An in-box
  agent's deliberate writes to its own instruction files survive
  relaunches for the first time.
- Third-party agent skills upgrade by replacing `context_target` with
  `context = "inject"` plus a consuming command; until then they load
  fine and status tells the truth about delivery.
- The user-facing mechanism is documented on the site (how-do-i +
  configuration reference): instructions are injected into the agent's
  system context at launch, per-agent mechanics named.
