# Placement principles

Where content lives and why: the placement rules for the README, the
site, and the repo docs. P1-P11 are citable as `PLACEMENT.md Pn`; three
are test-enforced (P6's tldr pin, P9's inventory in TODO.md, P10's
commands-page pin). Extracted from `docs/marketing/positioning.md`,
which keeps the audience, voice, and evidence base that these rules
serve.

**P1. Conversion copy may be adapted; operational fact has one home.**
The two converting surfaces face different arrival moments, so each may
carry its own rendering of the pitch. Operational content -- how to
actually drive byre -- is written once, in its canonical home; every
other surface gets a summary plus a link, never a second copy.

**P2. Canonical homes, by content type.** Conversion copy: `README.md`,
steered by the positioning doc (`docs/marketing/positioning.md`; the
landing adapts from it). Operational docs: `site/content/docs/` (this
repo). Deep reference (architecture, skill authoring, credential
mechanics): repo `docs/` -- the site links to these on GitHub, never
mirrors them. Point-in-time rationale: `docs/adr/`. (The positioning
doc's header carries the same canonicality rule.)

**P3. The README keeps the whole trial path.** A GitHub visitor must be
able to evaluate, install, and reach a first `byre develop` without
leaving the repo: pitch, contract, one blessed install command (brew),
the quickstart one-liner, the status artifact. Trim depth and breadth,
never the trial path.

**P4. The repo is read by agents as a first-class audience.** The
ask-your-agent prompt points at the repo, and `site/content/` lives in
it -- the agent reads the site sources and `docs/` anyway. This is what
makes P3's aggressive trim safe: the README is sized for the human skim.

**P5. Duplicate only the stable.** A fact appears on two surfaces only
when it changes rarely: the H1 pitch, the blessed install command, the
develop one-liner, the How-do-I tldrs. Volatile content lives only in
its canonical home. (The boxed/not-boxed contract left this list when
the README's copy became a summary-plus-link; whats-boxed.md is its one
home.)

**P6. A README summary is a different genre, not an excerpt -- and
shared text is verbatim or absent.** README summaries are written for
the evaluator (*that* the capability exists, *why* it matters). Where a
line belongs on both surfaces it is duplicated character for character
-- the paraphrase is the rot vector. Enforcement:
`TestHowDoITldrsMatchSite` pins the How-do-I index's (question, tldr)
pairs verbatim against the cookbook.

**P7. Media lives site-side.** The README carries the logo and text
artifacts only; screencasts and visual flavor are the site's. The
README's console blocks stay *hand-curated* -- they are P6 summaries of
output, not transcripts; deriving them from captures would put them in
the wrong genre.

**P8. Depth links down, never sideways.** README → site `/docs/` → repo
`docs/`. A repo doc earns a site page only when its audience flips from
contributor to user (first flip: the security model to
`/docs/security-model/`; repo SECURITY.md keeps the reporting policy).

**P9. Pinned artifacts are inventoried.** Every surface showing real
byre output is a lockstep liability; the status/marketing tripwire in
`TODO.md` enumerates them so a sweep checks a list, not memory.

**P10. The build generates from the binary, never from `docs/`.** No
bulk `docs/` → site pipeline: repo docs are the wrong genre, and P8's
audience flip is a per-doc editorial call. Generation from code does
pay: `/docs/commands/` renders from the cobra tree (hidden
`byre commands-page`; `TestCommandsPagePinsSiteFile` fails when stale),
so a new command cannot ship without its line.

**P11. Show the surface, don't describe it.** Where a site page teaches
an interactive surface, a demo cast is the page's opening artifact --
one demo per page/section, doing that page's job; every embed's poster
frame is the final screen; where the cast shows the interaction, the
prose states outcomes instead of narrating keystrokes.
