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

**P10. The build generates from the binary, never from `docs/`.** No bulk
`docs/` → `site/` pipeline: repo docs are the wrong genre for web pages
(reference structure, repo-relative links), and P8's audience flip is a
deliberate per-doc call, not a bulk default -- the user-facing candidates
(DELIVER, EJECTING, DOCKER-HOST) flip *editorially*, absorbed into
cookbook recipes. The generation that does pay is from the cobra command
tree: the site's commands page is byre's most volatile enumeration and
its true source is code -- every command's one-liner lives in its cobra
Short string, and a generator (spf13/cobra/doc, or a hidden command)
emits the page, so a new command cannot ship without its line and the
table cannot rot. Same move as shell completions: derive from the binary,
never hand-sync.

**P11. Show the surface, don't describe it.** Wherever a site page
teaches an interactive surface, a demo cast is the page's opening
artifact -- the default, not the exception. Disciplines: one demo per
page/section, doing that page's job (a cast that isn't demonstrating the
thing taught is decoration -- cut it); every embed's poster frame is the
final screen, so a non-playing reader still gets the static artifact;
and where the cast shows the interaction, the prose stops narrating
keystrokes and states outcomes (P6's genre split -- the demo owns "how
it feels", prose owns "what it means"). The README stays text (P7), and
its console blocks stay *hand-curated* (decided 2026-07-17): they are P6
summaries of output -- deliberately simplified for the evaluator's skim,
not transcripts -- so deriving them from captures would put them in the
wrong genre. Derived final-screen captures are a site-side option only
(poster frames, docs-page text blocks); for the README's curated blocks
the lockstep tripwire stays a swept checklist (P9).

### Publish-time asciinema demos (assumed feasible 2026-07-17; prototype still owed)

The tuitest substrate (ADR 0038) can record site demos: an `asciinema rec
-c "tmux -L <socket> attach"` spectator alongside the existing driver
captures the real escape-sequence stream while the scenario sends keys
and `WaitFor`s as tests do today -- one substrate, fourth consumer. Each
demo is a *gated test* (`BYRE_DEMO_REC=1`) that asserts its waits AND
emits a `.cast` into `site/static/`: a layout change fails the demo,
which fails the publish -- P9's tripwire mechanized, P10 extended to
moving pictures. (This, not taste, is the case against vhs: a `.tape`
has no assertions, so it can silently record broken output.) Player is
self-hosted asciinema-player + a Hugo shortcode; no service, no uploads.

Limits: publish-time demos cover engine-free surfaces only (`byre
config`, the picker to the engine boundary, `status` against a seeded
`BYRE_HOME`, the deliver picker) -- CI will never hold agent
credentials, so the develop-into-Claude hero clip stays a
deliberately-recorded artifact (made on the VM with the same verbs,
committed as a `.cast`, refreshed around releases; RELEASING.md's sweep
is the backstop). Flake discipline gains teeth: a flaky demo breaks
publishes, and the flakes-twice rule carries over. To prototype first:
asciinema under headless CI with geometry pinned to the pane, and a
seeded store whose `status` output presents well. Rough size: (M).

Placement (P11 applied; decided 2026-07-17):

| Where | Demo |
|---|---|
| Landing | the hero clip only (develop → Claude; VM-recorded) -- one cast, then the table and the pitch; resist the arcade |
| `/docs/quickstart/` | first-run picker + `status` (generated) |
| `/docs/configuration/` | the `byre config` TUI walk -- the flagship generated demo |
| Cookbook | per-recipe where interactive: deliver paste flow (generated), completion (generated, short); firewall / worktrees (engine-bound → VM-recorded, added when recorded) |
| `/docs/install/`, `/docs/commands/`, volumes, contract | none -- nothing interactive; text artifacts suffice |

Engine-bound casts sit in pages identically to generated ones; which is
which lives in the scenario inventory (P9), not on the page -- the
reader doesn't care, the sweep does.

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
| Commands table | **Move** (decided 2026-07-17) -- volatile, grows with every release; README links `/docs/commands/`, which becomes generated from the cobra tree. The handful of verbs already shown in prose suffice | P5, P10 |
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
2. **SECURITY.md** (P8): leave as a GitHub link for now, or is it close
   enough to user-facing to plan a site page?
