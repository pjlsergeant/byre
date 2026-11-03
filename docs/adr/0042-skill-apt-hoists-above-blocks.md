# Skill apt hoists above the skill blocks

Decided 2026-07-20. Every skill's apt install emits in its own section
between the core block and the skill blocks -- one `RUN apt-get` per skill,
in the same provenance order as the blocks (ADR 0041).

## Why

Apt layers contact mutable package indexes on every re-run: each `apt-get
update` is a fresh exposure to slow mirrors, transient failures, and moved
package versions -- the layer cache is the only stability they have. Inside
a block they would sit *behind* other skills' payload COPYs and raw lines,
so ordinary churn (a payload edit, an installed-skill bump) would re-run
every apt layer after it. ADR 0041's provenance
sort protected the expensive bundled installers from that churn; this
extends the same protection to apt.

The hoist is safe by the intra-block contract it preserves: a skill's apt
runs before its own COPYs and raw lines, so no declarative apt list can
depend on any raw line, not even its own skill's. Moving all apt ahead of
all blocks keeps "apt before everything" for every skill.

One RUN per skill, not one merged install: per-skill RUNs keep cache
granularity (one skill's package-list change re-runs only from its own apt
layer), keep the `# skill:` attribution meaningful, and cost only duplicate
`apt-get update` lines on a cold build.

## What does NOT change

Enable order remains the agent-facing order everywhere; only image layers
move (same posture as ADR 0041). The hoisted section reuses the blocks'
provenance order -- gen emits both passes from the same sorted slice, so
determinism holds. Installs of other ecosystems (npm and the like) are raw
`dockerfile` lines and stay in the block: their toolchain may be provided by
an earlier skill's raw lines, and apt has no analogous provider. The project
block's apt is untouched -- project volatility belongs in the
project tail.

## The risk accepted

A skill whose raw lines add an apt *repo* (key + sources entry) cannot
feed a LATER skill's declarative apt list from that repo: all declarative
apt runs before all raw lines. No skill does this, the
failure is loud at build time (`Unable to locate package`), and the escape
hatch is natural -- a skill needing a custom repo installs the package in
its own raw lines, where the repo setup already lives.

## Rejected

- **One merged apt RUN**: any skill's package change would re-run every
  skill's packages, and attribution blurs into one anonymous layer.
- **Hoisting payload COPYs**: deferred, as in ADR 0041 -- the pain
  observed (repeated `apt-get update` on skill churn, 2026-07-20) was
  entirely apt's.
