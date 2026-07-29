# Repositioning proposal v3 -- convenience leads, containment enables

**Status: FINAL 2026-07-29, applying.** v1 and v2 were each reviewed
independently by codex and grok (findings in `.byre-devlog/reviews.md`);
the maintainer ruled on every contested item. Touches `README.md`,
`site/content/_index.md`, a new `site/content/why-not.md`,
`docs/marketing/positioning.md`, `docs/PLACEMENT.md`, `TODO.md`. Delete
on absorb.

## Diagnosis

The conversion copy is comfort-first in assertion but containment-first
in proof. The H1, the opening sentence, and the first README section
already claim comfort; the *evidence* is all containment: the sole
epigram is a safety idiom, the only artifacts are the exposure banner
and the `byre status` grants ledger, the masthead's doc links end at the
security model, and the TUI -- which PRINCIPLES.md P0 calls the thing
that sells byre -- has no artifact on either convert surface and is
absent from the landing entirely. The fix is adding evidence for the
comfortable half, not replacing one slogan with another.

## Maintainer decisions (rounds 1 and 2)

1. The new voice rule is a hierarchy guideline, not an absolute ban.
   Round 2: its header names product evidence (toolkit, editor,
   persistent logins), not an exclusivity claim, avoiding conflict with
   "no contrast ads". Pairing autonomy claims with what-the-box-sees is
   page/section-level; the farm epigram is exempt as the accepted
   safety idiom.
2. No thesis line in the hero; the hero gains one flat comfort fact.
   The thesis lives in the why-not page's priming paragraph, plus (per
   the round-2 hedge) one line inside the shared pointer strip.
3. The wedge keeps its internal register; "the floor no competitor
   clears" cut as coined phrase-making. Wedge language never migrates
   verbatim into conversion copy.
4. The positioning statement drops the audience preamble and the
   Unlike clause, and minimizes dash asides.
5. Dash frequency is a house-style rule: prefer plain sentences; spend
   `--` only where an aside earns it.
6. The TUI section sells by scenario, not by naming editor headers.
7. "Your toolkit, every folder" merges into "Comfortable". The why-not
   entries move to one canonical site page; README and landing each
   carry the verbatim thesis-plus-pointer strip (round-2 hedge:
   competitive honesty keeps one on-page line).
8. On demo revival the `config-tui-walk` cast swaps in for the landing
   TUI section's text artifact in place. Never a second stacked embed.
9. Login copy states the true scope: the login persists per project;
   sharing across projects is opt-in.

## Changes to docs/marketing/positioning.md

### Positioning statement (replaces the current one)

> byre is the local-first, Docker-native harness that brings your
> environment to any folder: one command and your agent is running with
> full autonomy, your tools installed, your defaults applied, its login
> persisting, per project, across rebuilds, inside a throwaway,
> project-scoped container that sees the folder and what you explicitly
> grant. No account, no cloud, no authoring.

The audience paragraph below it is unchanged and now carries the
audience alone.

### One-liners

- Formal/repo description: *Run an AI coding agent in a throwaway,
  project-scoped container with your tools, logins, and defaults
  already inside. No account, no cloud, just your Docker.*
- Elevator: *byre puts Claude Code, Codex, Gemini, Grok, or OpenCode in
  a throwaway container around any folder, with your tools and skills
  inside and full autonomy on. Agent logins persist per project across
  rebuilds. The box sees the folder and what you grant, not your home
  dir or keys. One command in. Local Docker/Podman, no account, MIT,
  free forever.*

("free forever" stays: it is the live promise, kept deliberately.)

### Voice rules

New rule, placed next to "Never claim secure":

> - **Lead with the toolkit, the editor, and the logins that survive
>   rebuilds** -- the half no competitor ships. Containment is the
>   enabler of full autonomy: on any page that claims autonomy, pair
>   the claim with a concrete statement of what the box can see, shown
>   through legibility (`status`, the generated Dockerfile) -- at
>   page or section level, not necessarily the next sentence; the H1
>   and farm epigram stay exempt as the accepted safety idiom. Argue
>   comparative isolation strength only where the reader is already
>   evaluating containment (the security model, the why-not page).

Second addition:

> - **Prefer plain sentences over dash asides.** Spend `--` where an
>   aside earns it; a page whose every sentence pivots on a dash reads
>   as AI-cadence the same way stacked epigrams do.

### Framing fact demoted

The Anthropic env-inheritance fact moves from "use everywhere it fits"
to: use where host-sandbox behavior or exposure is under discussion
(security model, whats-boxed, the why-not page).

### The wedge

> **The wedge nobody else occupies:** *a personal toolkit that follows
> you into any folder + a screen that shows and edits the box's whole
> config (every live key editable or shown read-only with its owner
> named, P0) + per-project agent state + local + no-account +
> generated-readable.* Each competitor concedes at least one, and none
> has the per-person layer at all. The toolkit and the screen are the
> product (PRINCIPLES.md P0). Wedge phrasing is internal: it never
> migrates verbatim into conversion copy.

### Surfaces table and site plan

- "The surfaces and their readers" gains a fifth row:
  `site/content/why-not.md` | evaluator comparing alternatives |
  clicked through from either convert surface | convert.
- The "Why not X" entry-format section: the entries' canonical home is
  now `site/content/why-not.md`; README and landing carry the shared
  strip only.
- Landing row of the demo table: *hero clip (VM-recorded) + the
  `config-tui-walk` cast, which on revival REPLACES the landing TUI
  section's hand-curated text artifact in place. One moving embed per
  section, never stacked.* `/docs/configuration/` keeps the cast as its
  opening artifact (P11).
- Hero clip storyboard requirement (recorded in TODO.md's revival
  item): it must visibly show comfort -- familiar tools present, agent
  already authenticated -- not only develop → Claude.

## Changes to README.md

### Hero

Unchanged through the console block. Then:

> It's **`--dangerously-skip-permissions`, without risking the farm.**
>
> Every box opens familiar: your tools installed, your defaults
> applied, your agent's login persisting, per project, across rebuilds.

Farm line stays the sole epigram; the comfort line is a flat fact,
shared verbatim with the landing (joins the P5 stable list).

### Masthead doc links

quickstart · configuration · cookbook · security model

### Section order and content

1. Hero (as above; ask-your-agent prompt unchanged)
2. **Comfortable: bring your environment** -- current section absorbs
   "Your toolkit, every folder" whole (both paragraphs, verbatim
   moves): tools/skills/caches/packages and the five agents, then the
   build-your-own paragraph and the compounding postgres-client story.
   The old standalone toolkit section is deleted.
3. **Change the box in seconds** (new; replaces the old low-down
   "Configuration" section, whose cascade/hand-edit prose moves here):

   > `byre config` opens a keyboard-driven editor over the whole box
   > (keyboard-driven, works over SSH), in the same vocabulary
   > `byre status` prints.
   >
   > ```text
   > [HAND-CURATED console block of the real byre config screen.
   >  HARD SHIP-GATE: captured from the real editor; joins the P9
   >  inventory. Nothing invented.]
   > ```
   >
   > One client's projects need their own standing instructions? Keep
   > them in a shared layer and point each of that client's projects at
   > it. Want ripgrep in just this box? Add the package and rebuild.
   > The agent needs a sibling repo? Mount it read-only. Each is a
   > couple of seconds in `byre config`, then relaunch and `/resume`
   > where you left off.
   >
   > And if you want to live dangerously: `byre develop --self-edit`
   > hands the agent its own box config, and what it changed is shown
   > when you leave.
   >
   > (then the cascade paragraph: plain TOML, host-side store, the
   > editor is the interface, hand-edit as a right; links to the
   > configuration page and reference. No standalone Configuration
   > section remains anywhere below.)

   The package scenario uses ripgrep, not postgres, so it does not
   collide with the compounding postgres story now directly above it.
4. **Constrained: keep the host out of reach** -- current copy, with
   the `byre config` teaching replaced by a hand-off: grants (mounts,
   env, egress, ports) are rows in the editor above; `byre status`
   shows the resulting access; the Dockerfile is right there.
5. Install / Quickstart -- unchanged, status artifact and all. (The
   quickstart's "Log the agent in once; the login persists, per
   project, across rebuilds" stays; the hero fact deliberately does
   not duplicate it verbatim.)
6. What's boxed / Commands / How do I / Platform -- as today.

### Why not…? section

The entries move out whole. The heading stays (external anchors point
at `#why-not`) and carries only the shared strip:

> ## Why not…?
>
> Isolation is table stakes; the comfortable half is what nothing else
> has. The honest comparisons -- raw Docker, Docker Sandboxes™,
> devcontainers, your agent's built-in sandbox, a VPS, or staying on
> the host -- concessions included:
> [getbyre.com/why-not](https://getbyre.com/why-not/).

## New: site/content/why-not.md

The README's current why-not entries move here whole: the priming
paragraph (the thesis's long-form home), the entry order (raw Docker
first, before the ™), the concession parentheticals. Conversion tier,
top level, not under `/docs/`. The landing's compressed table is
deleted rather than moved (paraphrase, P6's rot vector).

## Changes to site/content/_index.md

1. Hero unchanged through the farm line; the flat comfort fact follows
   (verbatim the README's).
2. **Change the box in seconds** section: same intro sentence, the
   curated console block, the landing subset of scenario lines -- the
   ripgrep package line, the shared "Each is a couple of seconds"
   sentence, and the `--self-edit` closer, each verbatim per-line from
   the README -- plus the demo placeholder
   (`<!-- demo-placeholder: config-tui-walk -->` with the visible
   blockquote marker), swapped for the cast on revival.
3. The free-software sentence moves below that section.
4. The why-not table is deleted; the shared strip replaces it.

## Changes to docs/PLACEMENT.md

- **P2** gains: the why-not comparisons' canonical home is
  `site/content/why-not.md`; README and landing carry the shared strip
  only.
- **P5** stable-duplication list gains: the hero comfort fact, the
  why-not strip, and the landing's scenario-line subset (verbatim
  per-line with the README).

## Changes to TODO.md

- P9 status/marketing inventory gains the README/landing config console
  block (source: the real `byre config` screen; refresh trigger: any
  TUI change to the sections it shows).
- The demo-revival item gains the hero storyboard requirement (comfort
  visible) and the swap-in rule for `config-tui-walk`.
- New post-launch tripwire beside the H1 one: if "how is this different
  from X" becomes the recurring cold-reader question, the strip was
  not enough and a summary table returns to the README.

## Consciously overruled reviewer findings

- `/resume` stays in the scenario line (codex, twice: not universal
  across agents -- Grok's CLI spells it `--resume`). Colour; the
  audience reads it correctly. Maintainer ruling.
- "free forever" stays in the elevator line (codex). The live,
  intended promise.
- The wedge keeps "nobody else occupies" internally (codex). The
  evidence table supports the specific claim; the register is confined
  to internal steering text.
- "And if you want to live dangerously" stays (grok: wink register,
  reuses "dangerously"). The page's one deliberate joke; the
  disclosure fact rides in the same sentence. Maintainer ruling.
- Why-not competitive content on the convert surfaces is the one-line
  thesis inside the strip, not a summary table (grok wanted more).
  Maintainer ruling; the TODO.md tripwire is the backstop.

## Round 3 (applied-diff review) fixes

Both reviewers re-checked the applied tree. Folded in: the console
block recaptured against a `client-api-pjl-3bbe8c` seed with the
firewall skill enabled (codex: the trimmed egress row had hidden the
"unenforced" warning; grok: the block shared the status fixture's
project id while disagreeing with its grants); "per project" added to
the Comfortable login clause; the formal one-liner's "logins already
inside" became "agent logins that persist"; the strip made
byte-identical on both surfaces (same label, same breaks, same
absolute target) and P5's "modulo link target" carve-out removed; P8
scoped to serve/depth links; "Four surfaces" corrected to five; the
toolkit noun list restored; the demo-placeholder house rule amended to
match shipped comment-only reality.

## Not test-pinned, deliberately

The new shared lines (comfort fact, strip, landing scenario subset)
are convention-only, tracked by the P5 list, not by a new pin test.
`TestHowDoITldrsMatchSite` is not extended; it enforces a different
content class. Revisit only if drift actually bites.
