# Philosophical problems -- the structural criticism from the first outside review

**Status: PARKED, for reflection.** Nothing here is a work order. This is the
doctrine-level criticism extracted from the first outside review byre has ever
had (2026-07-27, four independent reviewers). The specific bugs those reviews
found are in **`review-findings.md`** next to this file; this one keeps only the
criticism that is about *how the project reasons*, not about what any particular
line of code does. The brief the reviewers were given is in **`review-brief.md`**
-- re-runnable against a later tree.

Delete-on-absorb: if a theme here turns into a principle edit, an ADR, or a
mechanism, absorb it there and cut the section.

## Provenance

Four reviewers, same brief, no git history and no devlog, whole repo:

- **codex** (via `byre-codereview`) -- one finding, heavily verified, ran the suite.
- **grok** (via `byre-codereview`) -- seven findings, strongest on doc/ADR asymmetry.
- **Opus 5** (subagent) -- thirteen findings, strongest on breadth.
- **Fable 5** (subagent) -- seven findings plus a pattern analysis, several
  reproduced empirically with throwaway tests.

Every reviewer's overall verdict was positive and none thought the architecture,
the scope, or the reasoning-first method was wrong. Two said the reverse
explicitly: the method demonstrably works, and the risk on takeover is
*sustaining* the discipline rather than untangling a mess. The criticism below
is all of one kind, and that is why it is worth collecting in one place:

> **The approach's failure mode is that written reasoning starts to feel like
> shipped enforcement.**

Each theme is labelled with whether it was stated by a reviewer or is synthesis
across reviews.

---

## 1. Doctrine without an enforcement arm rots silently -- and reads as covered

*Stated independently by grok and Opus; Fable's absences (A1-A4) are the same
shape.*

The repo's habitual response to a risk is to write the reasoning down: a
principle, an ADR, a rule in `CLAUDE.md`. That is the method and it works. The
problem is what happens next: some of those doctrines get a mechanism that fails
loudly when violated, and some do not -- and from the outside the two are
indistinguishable, because both are equally well written.

Opus, most directly:

> "The mechanism built to prevent compat rot has no enforcement arm, in a repo
> that otherwise builds enforcement arms."

Its evidence: ADR 0049 defines retirement windows, and its compat inventory
already missed `context_target` (ADR 0046) within a day of that key shipping.
Grok found the same hole from the other side -- the windows have no clock: no
test, no dated constant, no `RELEASING.md` step that would ever fire. Opus made
the identical observation about the by-hand dependency policy in
`dependabot.yml`: "the policy is stated, the mechanism is absent," with five
pending bumps including two majors in the TUI as the evidence of drift.

**Why this is philosophical and not a to-do.** The repo *does* build enforcement
arms, and good ones: the `hostopen` conformance AST walk, the config-reference
reflection guard, `TestMergeCoversEveryField`, the how-do-I verbatim pin,
`TestRepoSkillsAreCommittedPacked`. Because those exist, a written rule in this
repo carries the authority of a tested one. A reader -- including a future
contributor, including you in eighteen months -- cannot tell by looking which
rules have teeth. That is a systematic hazard created by the method itself, and
it gets worse as the doctrine surface grows.

**The question it raises:** should every doctrine that constrains future code
carry, at authoring time, either a mechanism or an explicit "this one is
convention only" marker? The cost is real. The alternative is that the ratio of
enforced to unenforced doctrine drifts downward invisibly.

## 2. The claim renderer is load-bearing but has no completeness invariant

*Synthesis. All four reviewers found a different instance; none named the common
cause.*

P4 chose legibility over gating. That makes `byre status` not documentation but
the security mechanism -- the thing standing where a gate would otherwise be.
And `status` is a rendering layer with no structural guarantee that it renders
everything that bears on a claim. Nothing asserts "every input capable of
affecting the network claim degrades it."

The four independent instances:

| Reviewer | Instance |
|---|---|
| codex | `env_from_host` reports a grant delivered when the source resolved empty (all three schemes: `env:`, `git:`, `tz:`) |
| Opus, Fable | `[env]` renders no status row at all, while a `BYRE_*` key there can switch the launch gate off; `networkLine` never consults env |
| Fable | Rows closed by a *lower* layer keep `rowSkill` kind, so the egress summary and exposure tally count closed doors as effective |
| grok | The ADR 0009 concurrent-bind residual is in the ADR but absent from the user-facing security-model page |

Four reviewers, four holes, one missing invariant. Fable's framing of the `[env]`
case is the sharpest version of the general point: it built a table showing four
sibling vectors to the same outcome are each guarded *and* each degrade the
claim, and the fifth does neither -- and it noted the fix precedent already
exists one field over, in `runparams.go`, where `BYRE_EGRESS` is deliberately
re-asserted "so no `[env]` key can skew what the box is told byre enforces."

**Why this is philosophical.** If gates were the mechanism, an unrendered field
would be a cosmetic bug. Because legibility *is* the mechanism, every omission in
the claim surface is a security defect of the same class as a missing check
would be under a gating design. The design choice was made; the corresponding
rigor was applied to the enforcement paths (`hostopen`, the security guard, the
launch gate) but not to the reporting path that the design promoted to
equal standing.

**The question it raises:** does the status renderer deserve the categorical
treatment `hostopen` got -- one predicate, one enforcement test, a growth guard
that fails when a new config field can reach a claim without degrading it?

## 3. The developer docs are more honest than the user-facing docs

*Stated by grok, in its closing paragraph; Opus's release-chain finding is the
same shape.*

grok:

> "a few residuals where the developer docs are more honest than the
> user-facing sharp-facts list."

Its case: ADR 0009's accepted residual (bind sources are pathnames, not
inode-pinned handles, so a concurrent rw session can redirect a bind in the
detect-to-mount window) is fully explained in the ADR and in `project/worktree.go`
-- and absent from `site/content/docs/security-model.md`, where the sharp facts a
user will actually read are listed. Worktrees make concurrent sessions
first-class, so this is not a hypothetical configuration.

Opus's release-chain finding is structurally identical: `install.md` says
"checksum-verified" without qualification, when what is verified is transport
integrity, not authenticity -- `checksums.txt` comes from the same GitHub release
as the binary.

**Why this is philosophical.** The whole premise is that disclosure substitutes
for enforcement. Disclosure that lands in `docs/adr/` but not on the page the
user reads has not been made -- the promise is partly undischarged, and the
gap is invisible from inside the repo because *someone* wrote it down.

**The question it raises:** is there a rule that a residual accepted in an ADR
must appear in the user-facing security model before the ADR can be marked
accepted?

## 4. Case-by-case excellence, with the generalization step done in English

*Fable named the pattern ("the bound exists one field over", four instances);
Opus proposed the fix shape independently ("make it one exported predicate the
build path shares"). The root-cause reading is synthesis.*

The observed pattern: when a class of bug is found, the instance gets fixed
beautifully, and the *class* gets recorded in prose -- a principle, a rule in
`CLAUDE.md`, a comment. What it does not get is a shared predicate or a test that
fails on the next member of the class. So the sibling case is missed:

- `maxHooksEntries = 500` bounds the hooks walk; the `<gitdir>/worktrees/`
  enumeration and the `.env*` listing next to it are uncapped (Fable, measured:
  15s of wedged `develop` from three planted entries, linear in count, before
  the box starts).
- `exitSnapshot` carries five completeness fields against the invented-deletion
  bug class, with named tests; the self-edit half of the same watch set carries
  one, and reproduces the bug (Fable, reproduced).
- The Claude Skills 64-file / 8 MiB bound is enforced in the pre-copy walk, not
  in the staging copy.
- `BYRE_EGRESS` is re-asserted against `[env]` skew; `BYRE_LAUNCH_GATE_FILE` and
  `BYRE_ENVD_DIR` are not.
- `agent-writable` is defined in `CLAUDE.md` as "the project tree *and anything a
  box can shape*" and implemented in `build/context.go` as one directory, with no
  consultation of rw mounts or `CommonGitDir` (Opus).

**Why this is philosophical.** Each individual fix is better than most projects
manage. The missing move is the last one: *what class is this, and what in the
code -- not in the prose -- will catch the next member?* This is theme 1 seen
from the code side rather than the doctrine side, and it is the most mechanically
actionable of the five: the sweep is a grep, not a rewrite.

## 5. The doctrine is measurably good at deflecting outside criticism

*Observed in Opus's review; the hazard reading is synthesis.*

`CONTRIBUTING.md` carries a list of "settled positions a reviewer will be
tempted to flag." Opus called it "the single most reviewer-considerate thing in
the repo" -- and then said, in the same paragraph:

> "I have respected it: I am not raising the flat `commands` package, the
> doc-comment style, the duplicated test helpers, or the complexity-review
> scale-backs."

The device worked exactly as designed. Four objections were pre-empted and never
made. Whether all four *should* have been suppressed is not knowable from here --
that is precisely the problem. The list cannot distinguish "already litigated
correctly" from "litigated once, by one person, and never re-examined."

**Why this is philosophical.** In a single-maintainer project, outside critique
is the scarcest input available. A mechanism that reliably reduces it is
load-bearing in a direction nobody audits. The list is not wrong to exist -- it
saved reviewer attention for the findings that mattered, which is real value --
but it deserves periodic re-derivation rather than maintenance: are these still
settled, or merely old?

**The question it raises:** should entries on that list carry the reasoning (or
an ADR link) rather than the conclusion, so a reviewer can disagree with the
argument instead of being asked to accept the verdict?

## 6. P1's trust model is binary, and the world has a third class

*Synthesis; no reviewer stated this outright. Both Fable's question 4 and the
`[env]` finding live in this gap, which is what suggests it.*

P1 says the threat model is the agent, never the user. That partition is clean
and it drives a lot of good decisions. But config does not arrive from only two
places. It arrives:

- from the user, by hand or through `byre config` -- P1's "user" case;
- from the boxed agent -- P1's "agent" case, and structurally impossible today
  for the store config, which is the design working;
- **from a cloned repo's `byre.preset`, via `byre preset apply`** -- content the
  user *consented to* but did not author;
- **from a skill's `[runtime].env`** -- covered by "enabling a skill is trusting
  it," which is documented, but is a trust *extension* rather than authorship.

The third class is where two of the review's findings sit. The `[env]` gate
redirect is a footgun when you type it and something else when it arrives in a
preset from a repo you cloned -- it is rendered in the apply diff as raw text,
but it gets no `⚠` grant line, because `[env]` is classified "not a grant."
Fable's fourth question -- does invoking `byre preset apply` in a directory count
as "naming" that directory's `byre.preset"? -- is the same boundary seen from a
different angle, and it notes the two call sites currently answer differently,
with the passive probe documenting a distinction the solicited path ignores.

**Why this is philosophical.** The doctrine gives no vocabulary for
user-blessed-but-not-user-authored input, so each instance gets adjudicated ad
hoc, and the adjudications drift apart. A third trust class would be a principles
or glossary change, not a code change.

---

## What is explicitly not being criticized

Recorded because it constrains how to read the above, and because a list of
problems with no bounds is misleading:

- No reviewer thought the architecture was wrong.
- No reviewer thought the scope was overreaching.
- No reviewer thought the reasoning-first method was a mistake; codex and grok
  both said the opposite, and Fable credited the method with making its highest-
  consequence finding *findable* ("the gap was visible only against that
  pattern").
- The security engineering, the supply chain, `hostopen`, the firewall's
  fail-closed discipline, and the docs' own enforcement arms were praised by all
  four independently.

The criticism is narrow and structural: the method's characteristic failure is
that written reasoning accumulates the authority of tested reasoning without the
guarantees, and the places where that has already happened are findable.
