# byre -- positioning: evidence & authoring rules

> **Live conversion copy is `README.md`** (repo root); when this file and
> the README disagree on conversion copy, the README wins. For
> *operational* content the canonical home is the site's docs pages
> (`site/content/docs/`, in this repo) -- there, the site page wins
> (amended 2026-07-17; the split is P1/P2 of the Site plan below,
> Pete-ratified same day). This file holds what steers
> *future* copy: the audience definition, voice rules, reusable
> one-liners, the competitive evidence base, and the site plan.
>
> Decided 2026-07-03, amended 2026-07-04 (both grilling sessions);
> slimmed 2026-07-06 -- rationale for decisions the shipped README now
> embodies was deleted, git history is the archive. Competitive facts
> were verified against live sources on **2026-07-03**; re-check before
> any launch push.

## Positioning statement

For **developers who already run coding agents daily and want full autonomy
without handing the agent their machine**, byre is **the local-first,
Docker-native project harness for autonomous coding agents**: one command puts
Claude Code, Codex, or Gemini in a throwaway, project-scoped container that
sees the repo and what you explicitly grant — nothing else. Unlike
account-backed sandbox products, agents' built-in host sandboxes, or
hand-authored devcontainers, byre needs **no account, no cloud, and no
authoring** — and everything it does is a generated file you can read.

Not (yet) the audience: agent-skeptics, teams/eng-leads, Docker aesthetes --
secondary reads, not the target. Known-unreachable (reader evidence,
2026-07-04): less-technical YOLO users who don't care -- they'd never adopt
tooling like this; maturity-signal seekers won't adopt pre-1.0 regardless
(accepted loss); per-case-isolation users who enjoy hand-configuring each
environment are correctly out of scope.

## One-liners for different slots

- **Formal / repo description:** *Run an AI coding agent in a throwaway,
  project-scoped container — no account, no cloud, just your Docker.*
- **Elevator (~40 words):** *byre puts Claude Code, Codex, or Gemini in a
  throwaway container that sees one project and what you explicitly grant —
  not your home dir, keys, or the rest of your machine. One command in.
  Local Docker/Podman, no account, MIT, free forever.*
- **The framing fact (use everywhere it fits):** Anthropic's own sandboxing
  docs concede that sandboxed commands **inherit host environment variables —
  credentials included — by default**, and can read `~/.ssh` and
  `~/.aws/credentials` unless you hand-configure denials. That is precisely
  the gap a throwaway, project-scoped container closes *by construction*.
  (Source: code.claude.com/docs/en/sandboxing, verified 2026-07-03.)

## Voice rules

- **Illuminate, don't persuade.** No superlatives, no fear-selling, no
  contrast ads. Show the artifact (transcript, status output, Dockerfile,
  table) and let it argue.
- **Sell with falsifiable specifics, not adjectives.** "A couple of seconds"
  over "never a slog"; a guardrail list over a wink. When a section has no
  artifact to show (e.g. the config editor, whose mock we deliberately
  don't fake), specifics are the only honest substitute — adjectives are
  the tell that the register is drifting. (Added 2026-07-03 after outside
  feedback caught exactly this in the Configuration section.)
- **Declared immaturity, never discovered.** Sharp edges are named before
  the reader finds them.
- **Never claim "secure".** byre is not a security product and never
  competes on isolation strength. The words are *boxed*, *scoped*,
  *legible* — not *secure*, *safe*, *hardened*.
- **The name is just a name.** No etymology on the page -- it's easily
  googled. The farm in the H1 is cute; an occasional light touch in body
  prose is fine ("new pastures"), but no cows and no etymology -- those
  stay site-side visual flavor. (Amended 2026-07-15: the H1-only rule
  overstated the case. Previously amended 2026-07-06, replacing the
  etymology-blockquote-as-payoff rule -- the blockquote is off the page.)
- **Break the aphorism cadence.** (Rewrite-pass note, 2026-07-04 -- a craft
  instruction, not doctrine.) Reader feedback flagged three body sentences
  as AI-sounding; what they shared wasn't length but rhythm -- every
  sentence landing as an epigram. Most sentences should state their fact
  flatly; spend the epigram on the two or three places that earn one (the
  headline already is one).

## "Why not X?" entry format

The entries themselves live in `README.md`. When a new alternative needs
one: **answer the question literally** -- "why not X?" gets X's actual
drawbacks, crisp and factual, as the lede; the honest concession goes in a
trailing italic parenthetical *("but it gives you kernel-level isolation,
and we don't")*, never as the opening -- and only when it's a concession
this audience actually feels (microVM isolation yes; "no install needed"
no, they already run Docker). 2–3 sentences. Where the drawback is "you do
it by hand" (devcontainers, raw Docker), byre's conveniences ARE the
answer -- that's where `byre config`, templates, per-project login, and
the eject path live. Order and the ™ are load-bearing: the priming line
plus "…raw Docker?" first teach the reader that byre IS ordinary Docker
underneath *before* they meet the Docker Sandboxes™ name -- which reads
like a category but is a product; the ™ marks it as one. (Real reader
confusion, twice: "did you build your own container infra?" 2026-07-03;
"I'd be explicit that you mean the enterprise product" 2026-07-04.)

## Competitive fact base (internal — verified 2026-07-03)

Full sources at the end.

| | **byre** | **Docker Sandboxes** | **Agent built-in sandboxes**¹ | **Dev Containers**² |
|---|---|---|---|---|
| Isolation | container (shared kernel) | **microVM, own kernel — strongest** | OS-level (Seatbelt/Landlock); agent runs **on your host** | container |
| Fresh, throwaway environment | ✔ fresh container per session | ✔ | ✘ — your host, your `$HOME` | long-lived by convention |
| Host files & creds exposed | only what you mount | none | **whole-disk reads + env vars (incl. credentials) inherited by default** | only what you mount |
| Network control | ✔ default-deny egress skill (open by default without it; shipped 2026-07-05) | ✔ policies | Codex: off by default; Claude: approval proxy | DIY (Anthropic's reference ships a deny-by-default firewall) |
| Account / sign-in | none | **Docker OAuth required** | vendor auth only (needed anyway) | none |
| Setup per project | one command; config generated | low (product flow) | zero | hand-author `devcontainer.json` + Dockerfile |
| Definition you can read | ✔ generated Dockerfile | partial (templates yes; runtime is product machinery) | n/a | ✔ — because you wrote it |
| Per-project agent auth & state | ✔ named volumes; `reset` / `rehome` | no per-project story | shared host state | DIY |
| Engines | Docker & Podman | own runtime | none needed | Docker |
| Maturity | **early, pre-1.0** | new (late 2025), Docker Inc. behind it | shipped inside the agents | industry spec, mature ecosystem |
| Price / license | MIT, free forever | CLI free; org governance needs Docker Business ($24/user/mo); proprietary | free with the agent | open spec, MIT CLI |

¹ Claude Code `/sandbox` (Seatbelt / bubblewrap + sandbox-runtime) and Codex
CLI (Seatbelt / Landlock), collapsed: same architecture, same gaps — filesystem
*write* limits and network mediation, but the agent still runs in your real
environment. Claude Code's sandbox is also incompatible with running `docker`
inside it.
² Including Anthropic's reference devcontainer for Claude Code — which
Anthropic labels "a working example rather than a maintained base image."

**Where byre honestly loses (each carried into its "Why not…?" entry — the
alternative's win leads the paragraph):**

1. **Isolation strength** — a microVM with its own kernel beats a
   shared-kernel container. Don't hedge it.
2. **Maturity and backing** — Docker Inc., an industry spec, and 95k-star
   vendor CLIs vs. a pre-1.0 project.
3. **Zero-install** — native sandboxes need nothing (macOS) or two packages
   (Linux); byre needs a container engine running.

(Network egress control *was* a fourth loss; the default-deny firewall
skill shipped 2026-07-05, so public copy claims it outright.)

**Footnote-tier, not columns:** Dagger's *container-use* (experimental,
parallel-agents-per-git-branch — a different problem; releases stalled since
2025-08). Cloud sandboxes (e2b, Daytona, Modal — account + usage billing,
API-first, for building agent products, not local dev). Single-agent Docker
wrappers (largest, *claudebox* at ~1.1k stars, unmaintained ~10 months;
freshest, *claude-pod* — four files, ~200 lines, Claude-only, 21 stars,
high-visibility author — reviewed 2026-07-04, nothing adopted, no public
mention).

**The wedge nobody else occupies:** *local + no-account + generated-readable
+ per-project agent state + a personal toolkit that follows you into any
folder*, all five at once. Each competitor concedes at least one: Docker
Sandboxes (account, opacity), native sandboxes (host execution, shared
state), devcontainers (hand-authoring, no state story) — and none of them
has the per-*person* layer at all. That fifth element is the real answer
to "why not a VPS / raw Docker / devcontainers": your environment doesn't
follow you there.

## Copy bank (reader-tested, 2026-07-04)

Pete's spontaneous phrasings outperformed the crafted copy in both README
feedback threads -- mine these before writing new lines:

- "it's instant per-folder sandboxes"
- the use-case sentence, endorsed verbatim by a cold reader as "what I
  would put at the top of the readme": *"if you want to regularly run
  agents with `--dangerously-skip-permissions` in many different folders,
  but don't trust the agent not to run `git push` as you, or go digging
  around in other folders."*
- "stop you needing to twizzle"
- "I don't want a Hetzner box per repo"
- "Ask your agent if byre is right for you:" + the repo-review prompt
  ("...Is it a good project or just vibe-coded trash? Is it right for me?
  Would you be happy there?") -- Pete, 2026-07-15; not yet reader-tested,
  shipped to README + landing same day

H1 evidence: one cold reader flagged the flag-based headline as marketing
copy trying too hard, and the H1 is a safety idiom, not a scope statement
-- both risks accepted; the plain what-it-is sentence under the H1 is the
mandatory mitigation, and `TODO.md`'s post-launch tripwire watches for
cold readers bouncing.

## Site plan

Roles: **site = landing page + canonical operational docs; README =
landing page + conversion summaries**. The devlog stays a private
working record -- publishing it under `/devlog/` was dropped 2026-07-17.
(Remaining work tracked as a `TODO.md` pointer to this section. The
placement principles below were drafted in
`wip/site-structure-principles.md` and absorbed 2026-07-17 when the
README trim shipped; git history keeps the full draft, including the
executed disposition map.)

### The surfaces and their readers

Four surfaces, two jobs: a surface either **converts** (turns a visitor
into a try) or **serves** (answers a user who already said yes). A page
must know which job it's doing; a page doing both does neither.

| Surface | Reader | Moment | Job |
|---|---|---|---|
| `README.md` | GitHub visitor (human or their agent) | deciding whether to care; ~30 seconds | convert |
| Site landing (`/`) | someone sent a link, or searching | same decision, but off-GitHub -- can carry media | convert |
| Site `/docs/` | someone who said yes | installing, configuring, hitting a question | serve |
| Repo `docs/` | contributor, auditor, skill author | verifying or extending byre | serve (deep) |

### Placement principles

**P1. Conversion copy may be adapted; operational fact has one home.**
The two converting surfaces face different arrival moments, so each may
carry its own rendering of the pitch. Operational content -- how to
actually drive byre -- is written once, in its canonical home; every
other surface gets a summary plus a link, never a second copy.

**P2. Canonical homes, by content type.** Conversion copy: `README.md`,
steered by this file (the landing adapts from it). Operational docs:
`site/content/docs/` (this repo). Deep reference (architecture, skill
authoring, credential mechanics): repo `docs/` -- the site links to
these on GitHub, never mirrors them. Point-in-time rationale:
`docs/adr/`. (The header's canonicality rule encodes this split;
ratified 2026-07-17.)

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
when it changes rarely: the boxed/not-boxed contract, the H1 pitch, the
blessed install command, the develop one-liner, the How-do-I tldrs.
Volatile content lives only in its canonical home.

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
contributor to user (first flip, shipped 2026-07-17: the security model
to `/docs/security-model/`; repo SECURITY.md keeps the reporting
policy).

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

### Publish-time asciinema demos (prototyped 2026-07-17; harness not yet built)

The tuitest substrate records site demos: an asciinema spectator
(`asciinema rec --headless --window-size` pinned to the pane) attached
to the private tmux server while the scenario sends keys and WaitFors
-- one substrate, fourth consumer. Each demo is a gated test
(`BYRE_DEMO_REC=1`) that asserts its waits AND emits a `.cast` into
`site/static/`: a layout change fails the demo, which fails the publish
(this, not taste, is the case against vhs -- a `.tape` has no
assertions). Player: self-hosted asciinema-player + a Hugo shortcode;
no service, no uploads. In-box prototype proved the pipeline end to end
(static asciinema 3.2.1 + static tmux 3.6b release binaries, no root,
CI-viable; artifacts in the dev box's `~/scratch/demo-prototype/`);
one discipline it surfaced: the harness must trim the cast's tail
events so the poster frame is the final TUI screen, not tmux's
server-exited notice.

Limits: publish-time demos cover engine-free surfaces only (`byre
config`, the picker to the engine boundary, `status` against a seeded
`BYRE_HOME`, the deliver picker) -- CI never holds agent credentials,
so the develop-into-Claude hero clip stays a deliberately-recorded
artifact (made on the VM with the same verbs, committed as a `.cast`,
refreshed around releases). A flaky demo breaks publishes; the
flakes-twice rule applies. Placement (P11 applied):

| Where | Demo |
|---|---|
| Landing | the hero clip only (develop → Claude; VM-recorded) -- resist the arcade |
| `/docs/quickstart/` | first-run picker + `status` (generated) |
| `/docs/configuration/` | the `byre config` TUI walk -- the flagship generated demo |
| Cookbook | per-recipe where interactive: deliver paste flow, completion (generated); firewall / worktrees (engine-bound → VM-recorded, added when recorded) |
| `/docs/install/`, `/docs/commands/`, volumes, contract | none -- nothing interactive |

Engine-bound casts sit in pages identically to generated ones; which is
which lives in the scenario inventory (P9), not on the page.

Until a cast exists, its slot carries a visible placeholder in the page
(blockquote marker + `<!-- demo-placeholder: <slug> -->` comment), so
the layout is judged with the demo's space reserved and the placeholder
inventory is grep-able. Static screenshots use the same convention
(`<!-- image-placeholder: <slug> -->`).

### User documentation vs Reference (decided 2026-07-17)

Site `/docs/` splits into two nav groups. **User documentation** is the
approachable tier -- task-first pages a newcomer can read without fear
(the configuration page describes the editor, not the TOML contract).
**Reference** is the precise tier: the configuration reference (full
vocabulary + merge rules), How it works, the security model. The split
is a front-matter flag (`reference: true`), not a URL move -- pages keep
their addresses when they change tier. A user page may always link down
into its reference counterpart; reference pages carry the sharp
register.

### The cookbook (decided 2026-07-17)

The bar for a cookbook entry: a question a real user has, a one-or-two
line tldr, a shipped feature behind it. Entries group by the reader's
situation (configuring the box; daily workflow; skills & templates;
lifecycle & recovery). The README index carries the show-off subset
only (P6); the rest are cookbook-only, found by question when needed.
The closing entry is always last in both index and cookbook: **"…do
something not listed here?"** -- point your agent at the repo (P4 as a
user-facing feature; the long tail of the cookbook nobody has to
write).

Deliberately not written, so they don't get re-raised: "free disk
space" (story is per-project `forget`; advertises a gap); "see the
agent's network calls" (that's the future resolver-sidecar work); "box
a folder of many repos" (mechanics allow it, but the recipe would bless
an anti-pattern against the per-project story); one-off command in the
box / CI / subfolder mount exclusions / offline (no feature, wrong
shape, not shipped, moot).

### CONTRIBUTING.md (shipped 2026-07-17)

A repo `CONTRIBUTING.md`, canonical repo-side per P8 -- contributors
are the repo-docs reader, and GitHub surfaces the file natively on
issues and PRs. The site links it, never mirrors. Content: this repo's
unusual conventions (TODO.md authoritative, the `wip/` lifecycle, ADRs,
the binding glossary), the dev environment pointer, and
young-single-maintainer expectations; written knowing agents read it
too (P4).

## Sources (verified 2026-07-03)

- Docker Sandboxes: docs.docker.com/ai/sandboxes (get-started shows `sbx
  login` → Docker OAuth; microVM isolation; agents list; kits experimental);
  docker.com/products/docker-sandboxes (pricing/governance).
- Claude Code sandboxing: code.claude.com/docs/en/sandboxing (env inheritance
  incl. credentials by default; whole-disk reads by default; proxy TLS
  caveats; `dangerouslyDisableSandbox`; docker incompatibility);
  github.com/anthropic-experimental/sandbox-runtime.
- Codex CLI sandboxing: developers.openai.com/codex/concepts/sandboxing
  (Seatbelt/Landlock; network off by default); github.com/openai/codex.
- Dev Containers: containers.dev; github.com/devcontainers/cli;
  code.claude.com/docs/en/devcontainer + anthropics/claude-code
  `.devcontainer/` (init-firewall.sh; "working example" framing).
- container-use: github.com/dagger/container-use (Experimental badge; v0.4.2
  2025-08-19; Apache-2.0).
- e2b: e2b.dev/pricing (account required; Hobby free tier; Pro $150/mo +
  usage).
- claudebox: github.com/RchGrav/claudebox (~1.1k stars; last push 2025-08-31).
