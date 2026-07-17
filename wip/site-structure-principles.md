# Site & README structure -- placement principles

**Status: draft for discussion (2026-07-17).** The rules that decide what
lives on the site, what stays in the README, and what merely links. No copy
is written here -- this file exists so the TODO's "trim the README" item is
executed against principles instead of vibes. Absorb target: replaces the
"Site plan" section of `docs/marketing/positioning.md` (and updates its
canonicality header -- see P2) when the trim ships; then delete.

Voice is out of scope: positioning.md's voice rules already govern every
surface. This file is structural only -- which surface says it, not how.

## The surfaces and their readers

Four surfaces, two jobs. A surface either **converts** (turns a visitor
into a try) or **serves** (answers a user who already said yes). (The old
site plan had a fifth -- `/devlog/` as published evidence; dropped
2026-07-17. The devlog stays a private working record, not a surface.)

| Surface | Reader | Moment | Job |
|---|---|---|---|
| `README.md` | GitHub visitor (human or their agent) | deciding whether to care; ~30 seconds | convert |
| Site landing (`/`) | someone sent a link, or searching | same decision, but off-GitHub -- can carry media | convert |
| Site `/docs/` | someone who said yes | installing, configuring, hitting a question | serve |
| Repo `docs/` | contributor, auditor, skill author | verifying or extending byre | serve (deep) |

A page must know which job it's doing. A page doing both does neither.

## Principles

**P1. Conversion copy may be adapted; operational fact has one home.**
The two converting surfaces face different arrival moments (GitHub chrome
vs. a clean page), so each may carry its own rendering of the pitch -- the
H1, the console block, the "Why not…?" material, the ask-your-agent
conceit. Operational content -- how to actually drive byre -- is written
once, in its canonical home, and every other surface gets a summary plus a
link, never a second copy.

**P2. Canonical homes, by content type.**
- Conversion copy: `README.md`, steered by positioning.md. (Landing adapts
  from it.)
- Operational docs: `site/content/docs/` -- these files, in this repo,
  become the live copy once the trim ships. positioning.md's header
  ("when this file and the README disagree, the README wins") stays true
  for *conversion* copy but must be amended: for operational content the
  site page wins.
- Deep reference (architecture, security model, skill/template authoring,
  credential mechanics): repo `docs/`. The site links to these on GitHub;
  it never mirrors them.
- Point-in-time rationale: `docs/adr/`.

**P3. The README keeps the whole trial path.** A GitHub visitor must be
able to evaluate, install, and reach a first `byre develop` without
leaving the repo: pitch, contract, one blessed install command, the
quickstart one-liner, the status artifact. Trim depth and breadth, never
the trial path. Everything past first-run success may be a link.

**P4. The repo is read by agents as a first-class audience.** The
ask-your-agent prompt points at the *repo*, and `site/content/` lives in
it -- so trimming the README loses nothing to that audience; the agent
reads the site sources and `docs/` anyway. This is what makes P3's
aggressive trim safe: the README is sized for the human skim, not for
completeness.

**P5. Duplicate only the stable.** A fact may appear on two surfaces only
when it changes rarely: the boxed/not-boxed contract, the H1 pitch, one
install command, the develop one-liner. Volatile content -- the commands
table, the config vocabulary, the "How do I…?" recipes, skill lists --
lives only in its canonical home. This is the rot-control rule: every
duplicated sentence is a future docs-sweep miss.

**P6. A README summary is a different genre, not an excerpt -- and shared
text is verbatim or absent.** The "simplified versions" the README keeps
are written for the evaluator -- *that* the capability exists and *why*
it matters -- not shortened copies of the site page. Where a line
genuinely belongs on both surfaces (a tldr, an install command), it is
duplicated *character for character*, so a sweep can grep for drift. The
rot vector is the middle ground: the paraphrase -- same content, slightly
different words -- that drifts silently and turns every behavior change
into a three-file edit.

The worked example is "How do I…?": the *question list plus tldrs* is
conversion content -- the evaluator scans it and learns byre has answers
for exactly the things they'd worry about, in falsifiable specifics --
so the README keeps the index (question + verbatim tldr + link per
entry). The explanatory recipes underneath are operational and volatile;
they move to the site's cookbook page. Anything that can't tldr in a
line or two isn't README material.

**P7. Media lives site-side.** The README carries the logo and text
artifacts (console blocks, `byre status` output) only. Screencasts, the
hero clip, and visual flavor (the cows) are the site's -- the media the
README shouldn't carry, per the site plan.

**P8. Depth links down, never sideways.** README → site `/docs/` → repo
`docs/`. A repo doc earns a site page only when its audience flips from
contributor/auditor to user -- until then, link to GitHub. (Watch item:
SECURITY.md is already cited from user-facing recipes; it may flip first.)

**P9. Pinned artifacts are inventoried.** Every surface that shows real
byre output (the status block, the develop banner) is a lockstep liability.
The status/marketing tripwire in TODO.md should enumerate the surfaces
carrying each artifact, so a sweep checks a list, not memory.

## Disposition map (the principles, applied)

What each current README section becomes. "Summary + link" means a P6
rewrite, not a truncation.

| README section today | Disposition | Rule |
|---|---|---|
| H1, what-it-is, badges, console block | **Keep** (landing adapts its own) | P1 |
| `--dangerously-skip-permissions` line, ask-your-agent | **Keep**; already on both | P1, P5 |
| Comfortable / Constrained | **Keep** -- it's pitch, not docs | P1 |
| Install | **Keep one blessed command** (brew or curl) + link to `/docs/install/` for the rest | P3, P5 |
| Quickstart | **Keep** the one-liner + status artifact; picker detail → `/docs/quickstart/` | P3, P9 |
| Your toolkit, every folder | **Keep** -- pitch | P1 |
| What's boxed, what isn't | **Keep in full** -- the contract is short, stable, and load-bearing for trust; also on site | P5 |
| Configuration | **Summary + link** -- cascade-exists + TUI-exists + files-are-yours; vocabulary, `!name` semantics, env sharp edge → `/docs/configuration/` | P5, P6 |
| Commands table | **Move** -- volatile, grows with every release; README links `/docs/commands/`. The handful of verbs already shown in prose suffice | P5 |
| Worktrees | **Summary + link** -- 2-3 sentences of pitch (it's a differentiator), mechanics → `/docs/worktrees/` | P6 |
| Volumes & state | **Move** -- the one-liner in "Comfortable" already covers the pitch; link | P5 |
| Why not…? | **Keep** (conversion, README-canonical); landing gets the comparison-table rendering per the site plan | P1 |
| How do I…? | **Keep the index, move the recipes** -- questions + verbatim tldrs stay (the question list is a capability showcase; each new recipe earns a three-line conversion slot); explanatory paragraphs → the site's cookbook page, one link per entry | P1, P6 |
| Platform | **Keep** -- short, stable, evaluators need it; also belongs on `/docs/install/` | P3, P5 |

And the site's side of the ledger (all already TODO'd, listed for
completeness): comparison table onto the landing, screencast hero, and --
new from this pass -- the `/docs/` pages stop being
README mirrors and become the canonical, fuller text (P2), each absorbing
the detail the README sheds.

## Open questions (Pete)

1. **Canonicality flip** (P2): happy to amend positioning.md's "README
   wins" header to "README wins for conversion copy; site page wins for
   operational docs"?
2. **Commands in the README**: drop the table entirely (proposed), or keep
   a 5-row "core verbs" mini-table? The mini-table is friendlier but is
   exactly the volatile-duplicate P5 warns about.
3. **SECURITY.md** (P8): leave as a GitHub link for now, or is it close
   enough to user-facing to plan a site page?
