# The accretion guardrails become standing commitments

Decided 2026-07-27. The 2026-07-18 configuration/feature-complexity
review ended with four working rules "until the consolidation lands",
with a stated exit: "if they survive contact with a few features,
promote the durable ones into `docs/PRINCIPLES.md` via an ADR." They
survived contact -- the [[context]] family, the exit report, and three
slop-review rounds all shipped under them -- so this ADR executes that
promotion. PRINCIPLES.md now carries them as the unnumbered
"Accretion guardrails" list under principle 7.

Deliberately unnumbered: the tree cites `PRINCIPLES.md §n` for the
seven numbered principles and `PLACEMENT.md Pn` for placement rules;
minting §8-§11 would re-create the two-namespace collision the
PLACEMENT.md extraction just disambiguated.

The guardrails, as promoted:

1. No new top-level config class until it shares the named-declaration
   rails (the MCP/Claude-Skills genus machinery).
2. No new removal/absence spelling; new absence semantics reuse an
   existing one.
3. No new compatibility path without a stated removal release
   (ADR 0049 holds the window policy and the live inventory).
4. A new typed skill field needs a stated justification: a second
   consumer, a security reason raw fields can't carry, or a legibility
   reason status must understand it.

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

The maintainer's framing stands: **we keep the features we have** --
what the review earned was consolidation of duplicated implementation,
retirement of pre-1.0 compat layers (ADR 0049), and these guardrails.
