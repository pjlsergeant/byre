# Claude Skills are wiring: [[claude_skills]] declarations, an always-baked tree, --add-dir injection

Decided 2026-07-16 with the maintainer (a greenfield ideation round — two
independent reviewer consultations plus a docs pass — then a full /grilling
session settling five decision points, then an in-box spike; the working
design doc lived in `wip/` and is absorbed here). byre gains a
`[[claude_skills]]` vocabulary for shipping **Claude Skills** — Anthropic's
agent-skill format: a directory whose root holds a `SKILL.md` — into the
box. Declarations are **wiring, not grants**; the effective set bakes to
`/etc/byre/claude-skills` in **every** image; delivery is **injection-only**
— the claude skill's command carries `--add-dir /etc/byre/claude-skills` —
and byre never writes an agent's skill state.

Principles: opinion-free core (P2) keeps the per-CLI flag in the claude
skill; legibility (P4) drives the attribution rows and honest degradation;
box-scoped consent (P5) rides the existing cascade homes; the footgun
doctrine (P1) rules the validation posture (attributed refusal of a
malformed declaration at develop; no policing of skill content).

## The problem

Users and byre-skill authors want "this box's agent has these Claude
Skills" with byre's usual properties: declared in the config cascade,
attributed, removable (removal converges — the skill really disappears),
visible in `byre status`. The old sketch (a `claude-skills.d` convention
dir synced by a hook) died with the MCP registrar (ADR 0033); this design
starts from the injection architecture that replaced it.

## The model

**A Claude Skill is WIRING, not a grant** (the `[[mcp]]` genus):
instructions plus support files confer nothing bash doesn't already have
inside the box. Declarations list as configuration, attributed, and
contribute zero to the exposure line. Anything a skill's scripts need at
runtime (env, egress) is the contributing byre skill's ordinary attributed
business. **Skills-only**: full Claude plugins (hooks, commands, agents,
embedded `.mcp.json`) are NOT a byre unit — a plugin can smuggle
auto-executing wiring past the grant inventory; MCP stays on `[[mcp]]`.
No format axis either (`format = "anthropic-skill"` was considered):
zero second customers today, and a future format joins as an optional
key with a default.

```toml
# byre.config / any cascade layer — source is a HOST directory
[[claude_skills]]
name = "tdd-loop"
path = "~/claude-skills/tdd-loop"

# skill.toml — source is a PACKAGE-RELATIVE directory
[[claude_skills]]
name = "review-loop"
from = "claude-skills/review-loop"
```

- **Two homes, one merge taxonomy** (ADR 0033 verbatim): config layers
  replace by name; skill contributions union AFTER the merge; `!name`
  closures survive the merge and subtract after the union ("this skill,
  minus one of its Claude Skills" works day one); duplicate ACTIVE names
  hard-reject, both claimants named.
- **The homes spell their source differently by design.** Config `path` is
  a host dir, `~/…` or absolute — deliberately wider than the
  project-relative `files` key, because a default.config declaration must
  reach the same dir from every project; safe because the payload is
  validated, bounded, and symlink-rejected. Skill `from` is
  containment-checked at resolve exactly like `[build].files`.
- **Name grammar** = the MCP grammar (`[a-z0-9][a-z0-9-]{0,63}`): the name
  becomes a directory in the baked tree, the agent's `/name` invocation,
  and an attribution label.
- **"Looks like a skill" validation** (resolve/bake-time, attributed,
  loud): dir exists; root `SKILL.md`; parseable YAML frontmatter with
  non-empty `name` + `description`; frontmatter name == declared name
  (claude derives identity from the directory, which byre names after the
  declaration); ≤64 files / ≤8 MiB; symlinks refused. Nothing deeper —
  claude's fuller contract is claude's and evolves; a malformed skill is
  non-fatal to a claude session (spike-verified), so this is hygiene with
  good attribution, not session survival. First consumer of the yaml.v3
  dependency (approved on demonstrated merit).

## The bake: /etc/byre/claude-skills, always

Each box bakes ITS OWN merged set (one owner: `skills.ClaudeSkillSet`) to
`/etc/byre/claude-skills/.claude/skills/<name>/…` — **claude's native
discovery layout**, so delivered skills load BARE (`/foo`), not
plugin-namespaced. The render is unconditional (empty set = empty dir), so
the adapter flag can be static in every box; the path is a quasi-public
contract like mcp.json, pinned by gen's golden test. The COPY lands after
the skills/agent layers so a skill-pack edit never busts them. No manifest
JSON (deliberate divergence from the mcp.json bake): the tree IS the
consumed format and is self-describing; attribution is computed from
resolved config at status time.

## Adapter: --add-dir injection, vouched

`[agent] claude_skills = "inject"` is the skill author's vouch that the
agent command consumes the baked tree — the vouch is THAT the agent
consumes the contract, not how; the mechanism lives in the command string.
The claude skill's command carries `--add-dir /etc/byre/claude-skills`,
and that flag is the entire adapter: no state writes, no launch-time
assembly, exact per-session convergence by construction. Adapter-less
agents degrade honestly (declared-but-NOT-delivered plus the baked path).
Delivery keys off the SELECTED agent; the bake is unconditional.

**Spike facts (claude 2.1.211, in-box, 2026-07-16; raw transcripts in the
design session):** `.claude/skills/<name>/SKILL.md` under an `--add-dir`
root loads bare as `/name`, auto-triggers from its description, works
headless (`-p`), reads support files, and runs exec-bit scripts; a
write-bit-stripped tree loads fine; an empty `skills/` dir is silent; a
malformed SKILL.md beside good ones is NON-FATAL (siblings load); a
same-name twin under the agent's own config-dir `skills/` SHADOWS the
delivered skill; `CLAUDE_CONFIG_DIR` does relocate personal skill scope.
No dedicated skills-dir flag/env exists at 2.1.211 beyond
`--add-dir`/`--plugin-dir`.

**Costs on the record:** attach shells (`byre shell` → claude) don't carry
the flag; a stale-image sibling box injects its own old set only; every
delivered skill's description rides the agent's context each turn (bodies
lazy-load); the box's own state volume can shadow byre's delivery
(box-state-wins is doctrine-correct — byre never clobbers agent-authored
skills, and prefs seeding never carries skills, so personal scope
populates only from in-box authorship).

## v1 surface

`byre claude-skill add <dir>` (validates first; name derived from the
frontmatter unless `--name`; `--global` targets default.config) /
`remove` (closure-smart, the `byre mcp remove` contract verbatim) /
`list` (rendered by status's own line functions). Status gets a Claude
Skills section: per-pack attribution, closures never invisible, one
delivery verdict keyed off the vouch. The config UI gets the full
`[[claude_skills]]` editor screen (the MCP-era ruling: the cockpit
doesn't omit a config class its siblings have).

## Dead (do not re-propose; reasons in the design history)

Full Claude plugins as a declarable unit (smuggling vs the grant
inventory); `--plugin-dir` as the primary rail (plugin skills are
structurally namespaced `plugin:name` — bare `/name` was ruled a
must-have; it remains the fallback if `--add-dir` ever regresses, at the
cost of re-opening that ruling); per-source plugin namespacing;
launch-copy into `~/.claude/skills/` (the ADR 0033 registrar again —
explicitly banned for this feature); overlay/mounting user scope (hides
agent-authored skills); materializing into `/workspace/.claude/skills/`
(the user's tree); convention-dir delivery with no vocabulary (fails
attribution/cascade/`!name`); a manifest bake; a `format` axis;
`[[agent_skills]]` as the key (collides with the standing "agent skill"
glossary term).

## Consequences

One new config class riding every existing rail (cascade, closures,
status, editor, CLI sugar); every image grows one COPY layer. The claude
skill's command now encodes two adapters (`--mcp-config`, `--add-dir`) —
a future claude breaking either is a skill fix, not a core one. The baked
tree's layout is a one-consumer contract today; a second CLI adopting the
Agent Skills format vouches with the same key and reads the same tree.
Carrier packages (a byre skill that ships only Claude Skills) need no new
machinery — ADR 0029 already gives them identity, digests, and install
consent, and enabling stays the grant.

User guide: `docs/SKILLS.md`. Vocabulary: `GLOSSARY.md` (Claude Skill,
Claude Skills adapter).
