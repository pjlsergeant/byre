# Skill apt hoists above the skill blocks

Decided 2026-07-20. Every skill's apt install emits in its own section
between the core block and the skill blocks -- one `RUN apt-get` per skill,
in the same provenance order as the blocks (ADR 0041). Before this, each
skill's apt ran inside its block, ahead of that skill's COPYs and raw lines.

## Why

Apt layers are the only skill layers with a network dependency on mutable
external state: every `apt-get update` re-run is a fresh exposure to slow
mirrors, transient failures, and moved package versions -- the layer cache
is the only stability they have. Yet inside a block they sat *behind* other
skills' payload COPYs and raw lines, so ordinary churn (a payload edit, an
installed-skill bump) re-ran every apt layer after it. ADR 0041's provenance
sort protected the expensive bundled installers from that churn; this
extends the same protection to apt.

The hoist is safe by the existing intra-block contract. Apt already ran
first within a block, before the skill's own COPYs and raw lines -- so no
declarative apt list can depend on any raw line, not even its own skill's.
Moving all apt ahead of all blocks preserves "apt before everything" for
every skill; nothing that worked before relied on apt running late.

One RUN per skill, not one merged install: per-skill RUNs keep cache
granularity (one skill's package-list change re-runs only from its own apt
layer), keep the `# skill:` attribution meaningful, and cost only duplicate
`apt-get update` lines on a cold build.

## What does NOT change

Enable order remains the agent-facing order everywhere; only image layers
move (same posture as ADR 0041). The hoisted section reuses the blocks'
provenance order -- gen emits both passes from the same sorted slice, so
determinism holds. `npm_global` stays in the block: unlike apt (always
present in the Debian-derived chassis), node/npm may be provided by an
earlier skill's raw lines, so hoisting it could break a real dependency.
The project block's apt is untouched -- project volatility belongs in the
project tail.

## The risk accepted

A skill whose raw lines add an apt *repo* (key + sources entry) can no
longer feed a LATER skill's declarative apt list from that repo: all
declarative apt now runs before all raw lines. No skill does this, the
failure is loud at build time (`Unable to locate package`), and the escape
hatch is natural -- a skill needing a custom repo installs the package in
its own raw lines, where the repo setup already lives.

## Rejected

- **One merged apt RUN**: any skill's package change would re-run every
  skill's packages, and attribution blurs into one anonymous layer.
- **Hoisting npm_global too**: breaks the earlier-skill-provides-node case
  (above); apt has no analogous provider.
- **Hoisting payload COPYs**: still deferred, as in ADR 0041 -- the pain
  observed (repeated `apt-get update` on skill churn, 2026-07-20) was
  entirely apt's.
