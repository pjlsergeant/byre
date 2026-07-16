# Claude Skills delivery -- working design (grilled 2026-07-16)

Status: design CONFIRMED with Pete (grilling session 2026-07-16); spike
in progress. Absorb into an ADR + docs on ship, then delete this file
(wip lifecycle). Ideation history: two greenfield reviewer rounds (codex,
grok) + a docs pass, notes in .byre-devlog/claude-skills-ideation.md.

## The unit

A **Claude Skill**: a directory whose root holds a `SKILL.md` (Anthropic's
Agent Skills format: YAML frontmatter `name` + `description`, optional
support files/scripts). Skills-only -- full Claude plugins (hooks,
commands, agents, embedded MCP) are NOT a byre unit; MCP stays on
`[[mcp]]`. No format axis (YAGNI; a future format joins as an optional
key with a default).

Ruling: a Claude Skill is **wiring, not a grant** (the [[mcp]] genus) --
instructions confer nothing bash lacks in-box. Attributed configuration,
zero exposure contribution.

## Vocabulary

- Config table: `[[claude_skills]]`. GLOSSARY term: "Claude Skill", with
  the byre-skill disambiguation. The existing "agent skill" term (a byre
  skill with an [agent] table, ~49 uses) is untouched -- that collision
  is why `[[agent_skills]]` died.
- Config-side source: `path = "<host dir>"` (precedent: config `files`).
- Skill-side source: `from = "<package-relative dir>"`.
- Both feed the same bake.

```toml
# byre.config / any cascade layer
[[claude_skills]]
name = "tdd-loop"
path = "~/claude-skills/tdd-loop"

# skill.toml (e.g. pjlsergeant/codereview)
[[claude_skills]]
name = "review-loop"
from = "claude-skills/review-loop"
```

## Validation ("looks like a skill", resolve-time, attributed, loud)

1. Directory exists; root holds `SKILL.md`.
2. Parseable YAML frontmatter with non-empty `name` + `description`
   (small YAML dep approved).
3. Frontmatter `name` == declared `name` (one error, both spellings).
4. Bounds: <=64 files, single-digit-MB total (exact number at impl).

Nothing deeper -- frontmatter extras, body, scripts are claude's contract.
Legibility check, not nannying: a failing dir delivers nothing anyway.

## Merge ([[mcp]] taxonomy verbatim)

Config layers replace by name -> enabled byre skills' contributions union
after the merge -> `!name` closures subtract after the union -> duplicate
ACTIVE names hard-reject with both claimants named.

## Delivery: bake + --add-dir injection

- Core renders each box's own merged set to
  `/etc/byre/claude-skills/.claude/skills/<name>/...` -- UNCONDITIONALLY
  (empty set = empty skills/ dir) so the adapter flag is static and never
  dangling. Golden-tested. COPY layer after skills/agent layers.
- NO manifest JSON: the tree is the consumed format and is
  self-describing; attribution is computed from resolved config at status
  time. (Deliberate divergence from the mcp.json bake.)
- The claude byre skill's command carries `--add-dir
  /etc/byre/claude-skills`; vouch key `[agent] claude_skills = "inject"`
  (closed enum, one value; the vouch is THAT the agent consumes the
  contract, not how). Typo refuses at resolve.
- Skills present BARE (`/foo`) -- Pete's must-have; this is what killed
  `--plugin-dir` (plugin skills are structurally namespaced
  `plugin:skill`).
- byre never writes `~/.claude` or `/workspace`. Convergence is
  session-exact via rebuild.
- Adapter-less agents (codex, grok, ...) degrade honestly: "declared but
  NOT delivered -- baked at /etc/byre/claude-skills".
- Selection seam as MCP: adapter work keys off the SELECTED agent; the
  bake is unconditional regardless.

Documented boundaries (not fixed):
- Attach shells (`byre shell`) don't carry the flag; no delivered set.
- A same-name skill the in-box agent authored into `~/.claude/skills`
  (the state volume -- the ONLY way personal scope populates in a box;
  prefs seeding is curated and never carries skills) shadows the
  delivered one. Box-state-wins is doctrine-correct; one docs line.
- Stale-image sibling box injects its own old set into its own session.
- Descriptions of all delivered skills ride the agent's context each
  turn (claude lazy-loads bodies only) -- docs note the per-pack cost.

## Product surface (v1, all confirmed)

- `byre claude-skill add <path> [--global]` -- runs the validation, writes
  the stanza (project config; --global -> default.config).
- `byre claude-skill remove <name>` -- closure-smart (delete own stanza,
  else write `!name`).
- `byre claude-skill list` -- resolved set + attribution.
- Config UI: a `[[claude_skills]]` editor screen (the cockpit doesn't
  omit a config class its siblings have -- MCP ruling).
- Status: "Claude Skills" section -- per-pack source attribution,
  closures shown, delivery line or NOT-delivered degradation. Zero
  exposure contribution.
- Docs sweep in the same unit: GLOSSARY entry, SKILLS.md section,
  shadowing caveat, README touch if needed.

## Spike (gates ADR + build)

Throwaway/live box, pinned claude version, raw commands + outputs
recorded; findings absorbed into the ADR.

1. DECISIVE: `.claude/skills/<name>/SKILL.md` under an `--add-dir` root
   loads BARE as `/foo`; auto-triggers from description; works headless
   (`-p`); multiple skills + support files + exec-bit scripts + relative
   references resolve.
2. DECISIVE: read-only, root-owned baked dir (image content, agent runs
   as dev) -- loads fine, file-watcher tolerates it.
3. Shadowing direction vs a `~/.claude/skills` twin (confirm
   personal-beats-delivered for the docs line).
4. Empty `skills/` dir under `--add-dir`: silent, harmless.
5. Working-dir presentation: what the extra dir does to system
   prompt/context; agent doesn't start treating /etc/byre/claude-skills
   as workspace.
6. Malformed SKILL.md in the baked set: skipped vs session-fatal.
7. Opportunistic: a dedicated skills-dir flag/env (would beat --add-dir);
   CLAUDE_CONFIG_DIR moving personal scope (shadowing doc line only).

If 1 or 2 fails: back to the table -- the `--plugin-dir` fallback cannot
do bare names (Pete's must-have), so that's a re-decision, not a
silent fallback.

## Dead (do not re-propose)

Full plugins as a declarable unit; per-source plugin namespacing
(`/codereview:foo`); `--plugin-dir` as primary rail (namespacing kills
bare names); launch-copy into `~/.claude/skills/` (the MCP registrar
again); overlay/mount at user scope; `/workspace/.claude/skills`
materialization; convention-only delivery (no vocabulary -> no
attribution/cascade/!name); the manifest bake; a `format` axis;
`[[agent_skills]]` as a key (glossary collision).
