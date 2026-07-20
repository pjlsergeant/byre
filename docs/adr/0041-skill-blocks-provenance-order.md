# Skill blocks emit in provenance order

> Extended by ADR 0042 (2026-07-20): skill apt installs now hoist out of the
> blocks into their own section ahead of them (the "hoisting" deferral below
> was for payload COPYs, which stay deferred).

Decided 2026-07-20. The generated Dockerfile's skill blocks emit ordered by
catalog provenance -- bundled, then installed, then local -- stable within
each class, with enable order breaking ties. Before this they emitted in
enable order alone.

## Why

Docker invalidates every layer after the first changed one. A skill block was
then `apt -> COPY files -> raw lines` (apt has since hoisted out; ADR 0042),
so one skill's payload edit re-runs every
later skill's raw lines -- and the expensive raw lines are the agent and
reviewer installers, which all live in bundled skills. Bumping an installed
package (the codereview 1.0.1 release, 2026-07-20) re-ran every installer
behind it in enable order.

Provenance is a volatility proxy that the catalog already knows: bundled
content changes only with the byre binary, installed packages only on install
events, local packages on any working-tree edit. Sorting stable-before-
volatile puts the expensive layers where edits can't reach them. No new
config, no skill-API change, no per-skill flags -- rejected alternatives
below.

## What does NOT change

Enable order remains the agent-facing order everywhere: context composition,
status attribution, grant review. The sort happens at the skills->gen seam
(`build.planSkillBlocks`), copies its input, and touches only image layer
order. Determinism holds: the order is a pure function of the resolved set.

## The risk accepted

A bundled skill's raw line that depended on an *installed* skill's layers
(its apt packages, its files) would now run before them. That dependency
direction is backwards -- bundled skills ship with byre and cannot know what
users install -- and no bundled skill does it. Within a class, relative order
is unchanged, so installed-to-installed dependencies keep working.

## Rejected

- **Global phase-split** (all raw lines, then all COPYs): breaks the
  files-before-raw-lines contract -- codex's block chmods a file its own COPY
  just placed.
- **Volatility flags in skill.toml**: API growth to declare what provenance
  already encodes.
- **Hoisting unreferenced payload COPYs to a tail section**: sound (move a
  COPY only when its dest appears in no raw line), and would catch churn
  *within* a class -- deferred until a real within-class pain arrives;
  today's pain was entirely cross-class.
