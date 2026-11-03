# Claude Skills are wiring: [[claude_skills]] declarations, an always-baked tree, --add-dir injection

Decided 2026-07-16, design settled against an in-box spike. byre gains a
`[[claude_skills]]` vocabulary for shipping **Claude Skills** -- Anthropic's
agent-skill format: a directory whose root holds a `SKILL.md` -- into the
box. Declarations are **wiring, not grants**; the effective set bakes to
`/etc/byre/claude-skills` in **every** image; delivery is **injection-only**
-- the claude skill's command carries `--add-dir /etc/byre/claude-skills` --
and byre never writes an agent's skill state.

Principles: opinion-free core (P2) keeps the per-CLI flag in the claude
skill; legibility (P4) drives the attribution rows and honest degradation;
box-scoped consent (P5) rides the existing cascade homes; the footgun
doctrine (P1) rules the validation posture (attributed refusal of a
malformed declaration at develop; no policing of skill content).

## The problem

Users and byre-skill authors want "this box's agent has these Claude
Skills" with byre's usual properties: declared in the config cascade,
attributed, removable (removal converges -- the skill really disappears),
visible in `byre status`. This design starts from ADR 0033's injection
architecture.

## The model

**A Claude Skill is WIRING, not a grant** (the `[[mcp]]` genus):
instructions plus support files confer nothing bash doesn't already have
inside the box. Declarations list as configuration, attributed, and
contribute zero to the exposure line. Anything a skill's scripts need at
runtime (env, egress) is the contributing byre skill's ordinary attributed
business. **Skills-only**: full Claude plugins (hooks, commands, agents,
embedded `.mcp.json`) are NOT a byre unit -- a plugin can smuggle
auto-executing wiring past the grant inventory; MCP stays on `[[mcp]]`.
No format axis either (`format = "anthropic-skill"` was considered):
no second customer exists, and a future format joins as an optional
key with a default.

```toml
# byre.config / any cascade layer -- source is a HOST directory
[[claude_skills]]
name = "tdd-loop"
path = "~/claude-skills/tdd-loop"

# skill.toml -- source is a PACKAGE-RELATIVE directory
[[claude_skills]]
name = "review-loop"
from = "claude-skills/review-loop"
```

- **Two homes, one merge taxonomy**: ADR 0033's named-declaration merge
  unchanged, so "this skill, minus one of its Claude Skills" is a `!name`
  closure.
- **The homes spell their source differently by design.** Config `path` is
  a host dir, `~/…` or absolute -- deliberately wider than the
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
  declaration); ≤64 files / ≤8 MiB; symlinks refused. Nothing deeper --
  claude's fuller contract is claude's and evolves; a malformed skill is
  non-fatal to a claude session (spike-verified), so this is hygiene with
  good attribution, not session survival. First consumer of the yaml.v3
  dependency (approved on demonstrated merit) -- see the dependency note
  below.

## The bake and the adapter

The mechanics -- the always-rendered `/etc/byre/claude-skills` tree in
claude's native `.claude/skills/<name>/` layout, the `[agent]
claude_skills = "inject"` vouch, the `--add-dir` flag -- are described in
`docs/ARCHITECTURE.md` (Claude Skills delivery). Why they take that shape:

- **Native layout** so delivered skills load BARE (`/foo`), not
  plugin-namespaced.
- **Unconditional render** (empty set = empty dir) so the adapter flag
  can be static in every box; the path is a quasi-public contract like
  mcp.json, pinned by gen's golden test. The COPY lands after the
  skills/agent layers so a skill-pack edit never busts them.
- **No manifest JSON** (deliberate divergence from the mcp.json bake):
  the tree IS the consumed format and is self-describing; attribution is
  computed from resolved config at status time. One owner computes each
  box's merged set: `skills.ClaudeSkillSet`.
- **The vouch is THAT the agent consumes the contract, not how**; the
  mechanism lives in the command string. That one flag is the entire
  adapter: no state writes, no launch-time assembly, exact per-session
  convergence by construction. Delivery keys off the SELECTED agent; the
  bake is unconditional. Adapter-less agents degrade honestly
  (declared-but-NOT-delivered plus the baked path).

**Spike facts (claude 2.1.211, in-box, 2026-07-16):**
`.claude/skills/<name>/SKILL.md` under an `--add-dir`
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
(box-state-wins is doctrine-correct -- byre never clobbers agent-authored
skills, and prefs seeding never carries skills, so personal scope
populates only from in-box authorship).

## Surfaces

The declaration rides the existing named-declaration surfaces: `byre
claude-skill add|remove|list` (remove is closure-smart, the `byre mcp
remove` contract), a Claude Skills section in status with one delivery
verdict keyed off the vouch, and the full `[[claude_skills]]` editor
screen in the config UI (the cockpit doesn't omit a config class its
siblings have). Syntax: the commands page.

## Rejected (do not re-propose without new evidence)

Full Claude plugins as a declarable unit (smuggling vs the grant
inventory); `--plugin-dir` as the primary rail (plugin skills are
structurally namespaced `plugin:name` -- bare `/name` was ruled a
must-have; it remains the fallback if `--add-dir` ever regresses);
launch-copy into `~/.claude/skills/` (the ADR 0033 registrar again);
overlay/mounting user scope (hides agent-authored skills);
materializing into `/workspace/.claude/skills/` (the user's tree);
convention-dir delivery with no vocabulary (fails
attribution/cascade/`!name`); a manifest bake; a `format` axis;
`[[agent_skills]]` as the key (collides with the standing "agent skill"
glossary term).

## Consequences

One new config class riding every existing rail (cascade, closures,
status, editor, CLI sugar); every image grows one COPY layer. The claude
skill's command encodes two adapters (`--mcp-config`, `--add-dir`) --
a future claude breaking either is a skill fix, not a core one. The baked
tree's layout is a one-consumer contract; a second CLI adopting the
Agent Skills format vouches with the same key and reads the same tree.
Carrier packages (a byre skill that ships only Claude Skills) need no new
machinery -- ADR 0029 already gives them identity, digests, and install
consent, and enabling stays the grant.

### The yaml.v3 dependency, on the record

`gopkg.in/yaml.v3` is byre's one YAML parser and it stays, on the same
terms `textdiff` is kept on (`internal/commands/diff.go`) -- the register
for a dependency byre accepts with its eyes open:

- **Upstream is archived** (2025-04-01, terminal release, explicitly
  unmaintained). Nothing will be fixed there, and Dependabot has nothing
  to signal: a silent dependency is not a maintained one.
- **Blast radius is one call.** It parses the YAML frontmatter of a
  `SKILL.md` -- a bounded block byre has already split off at the `---`
  fences, from a payload the install consent covered -- into two string
  fields. No network, no filesystem, no process. A parse that goes wrong
  yields a wrong name or description on a skill byre then refuses or
  attributes wrongly, not a compromise.
- **Replacement, if it comes to that:** the frontmatter byre reads is a
  flat map of scalars; hand-parsing that block is a day's work, and the
  validation around it (fences, closure, non-empty name) is byre's
  already.

User guide: `docs/SKILLS.md`. Vocabulary: `GLOSSARY.md` (Claude Skill,
Claude Skills adapter).
