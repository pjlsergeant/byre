# The accretion guardrails become standing commitments

Decided 2026-07-27. The four working rules the 2026-07-18
configuration/feature-complexity review set "until the consolidation
lands" are standing commitments: PRINCIPLES.md carries them as the
unnumbered "Accretion guardrails" section after principle 7, which is
their one home. They earned promotion by surviving contact -- the [[context]]
family, the exit report, and three slop-review rounds all shipped under
them.

Deliberately unnumbered: the tree cites `PRINCIPLES.md §n` for the
seven numbered principles and `PLACEMENT.md Pn` for placement rules;
minting §8-§11 would re-create the two-namespace collision PLACEMENT.md
exists to avoid.

## Context: what the same review REJECTED

The complexity review also proposed cuts that were adjudicated and
rejected -- recorded here because slop/complexity reviews reliably
re-derive them:

- **Package-manager scale-back**: rejected as a code action. The system
  stays; the accepted posture is passive -- don't expand distribution
  features without usage evidence, note interesting signals when they
  appear.
- **Removing one-consumer typed skill fields** (`containment`,
  `sock_groups`): rejected -- they pass the security/legibility test;
  guardrail 4 is the whole takeaway.
- **UI legibility-row cuts** (stale-marker rows, offered rows, legacy
  problem rows in their support window): rejected -- legibility
  features, not accretion.
- **A line-count target**: rejected as a goal. Reduction is a byproduct,
  never chased as a number.
