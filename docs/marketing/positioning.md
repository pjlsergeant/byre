# byre -- positioning: evidence & authoring rules

> **Live conversion copy is `README.md`** (repo root); when this file and
> the README disagree on conversion copy, the README wins. For
> *operational* content the canonical home is the site's docs pages
> (`site/content/docs/`, in this repo) -- there, the site page wins.
> This file holds what steers *future* copy: the audience definition,
> voice rules, the competitive evidence base, and the site plan.
>
> Competitive facts were verified against live sources on **2026-07-03**;
> re-check before any launch push.

## Positioning statement

byre is **the local-first, Docker-native harness that brings your
environment to any folder**: one command and your agent is running with
full autonomy, your tools installed, your defaults applied, its login
persisting, per project, across rebuilds, inside a throwaway,
project-scoped container that sees the folder and what you explicitly
grant. No account, no cloud, no authoring.

Not (yet) the audience: agent-skeptics, teams/eng-leads, Docker aesthetes --
secondary reads, not the target. Known-unreachable (reader evidence):
less-technical YOLO users who don't care -- they'd never adopt tooling like
this; maturity-signal seekers won't adopt a young project regardless
(accepted loss); per-case-isolation users who enjoy hand-configuring each
environment are correctly out of scope.

## One-liners for different slots

- **Formal / repo description:** *Run an AI coding agent in a throwaway,
  project-scoped container with your tools and defaults already inside,
  and agent logins that persist. No account, no cloud, just your
  Docker.*
- **Elevator (~40 words):** *byre puts Claude Code, Codex, Gemini, Grok,
  or OpenCode in a throwaway container around any folder, with your
  tools and skills inside and full autonomy on. Agent logins persist per
  project across rebuilds. The box sees the folder and what you grant,
  not your home dir or keys. One command in. Local Docker/Podman, no
  account, MIT, free forever.*
- **The framing fact (for host-sandbox and exposure contexts -- the
  security model, whats-boxed, the why-not page -- no longer the
  everywhere-fact):** Anthropic's own sandboxing
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
  artifact to show, specifics are the only honest substitute — adjectives
  are the tell that the register is drifting.
- **Declared immaturity, never discovered.** Sharp edges are named before
  the reader finds them.
- **Never claim "secure".** byre is not a security product and never
  competes on isolation strength. The words are *boxed*, *scoped*,
  *legible* — not *secure*, *safe*, *hardened*.
- **Lead with the toolkit, the editor, and the logins that survive
  rebuilds.** Containment is the enabler of full autonomy: on any page
  that claims autonomy, pair the claim with a concrete statement of
  what the box can see, shown through legibility (`status`, the
  generated Dockerfile) — at page or section level, not necessarily the
  next sentence; the H1 and farm epigram stay exempt as the accepted
  safety idiom. Argue comparative isolation strength only where the
  reader is already evaluating containment (the security model, the
  why-not page).
- **Prefer plain sentences over dash asides.** Spend the dash where an
  aside earns it; a page whose every sentence pivots on a dash reads as
  AI-cadence the same way stacked epigrams do.
- **The name is just a name.** No etymology on the page -- it's easily
  googled. The farm in the H1 is cute; an occasional light touch in body
  prose is fine ("new pastures"), but no cows and no etymology -- those
  stay site-side visual flavor.
- **Break the aphorism cadence.** Most sentences should state their fact
  flatly; spend the epigram on the two or three places that earn one (the
  headline already is one). Reader feedback flagged every-sentence-lands-
  as-an-epigram rhythm as AI-sounding.
- **The H1 is a safety idiom, not a scope statement** -- an accepted risk;
  the plain what-it-is sentence under it is the mandatory mitigation, and
  TODO.md's post-launch tripwire watches for cold readers bouncing.

## "Why not X?" entry format

The entries themselves live in `site/content/why-not.md` (their
canonical home since 2026-07-29; README and landing carry the shared
thesis-plus-pointer strip only — PLACEMENT.md P2/P5). When a new
alternative needs one: **answer the question literally** -- "why not X?" gets X's actual
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
like a category but is a product; the ™ marks it as one (real readers
have twice mistaken which is meant).

## Competitive fact base (internal — verified 2026-07-03)

Full sources at the end.

| | **byre** | **Docker Sandboxes** | **Agent built-in sandboxes**¹ | **Dev Containers**² |
|---|---|---|---|---|
| Isolation | container (shared kernel) | **microVM, own kernel — strongest** | OS-level (Seatbelt/Landlock); agent runs **on your host** | container |
| Fresh, throwaway environment | ✔ fresh container per session | ✔ | ✘ — your host, your `$HOME` | long-lived by convention |
| Host files & creds exposed | only what you mount | none | **whole-disk reads + env vars (incl. credentials) inherited by default** | only what you mount |
| Network control | ✔ default-deny egress skill (open by default without it) | ✔ policies | Codex: off by default; Claude: approval proxy | DIY (Anthropic's reference ships a deny-by-default firewall) |
| Account / sign-in | none | **Docker OAuth required** | vendor auth only (needed anyway) | none |
| Setup per project | one command; config generated | low (product flow) | zero | hand-author `devcontainer.json` + Dockerfile |
| Definition you can read | ✔ generated Dockerfile | partial (templates yes; runtime is product machinery) | n/a | ✔ — because you wrote it |
| Per-project agent auth & state | ✔ named volumes; `reset` / `rehome` | no per-project story | shared host state | DIY |
| Engines | Docker & Podman | own runtime | none needed | Docker |
| Maturity | **young (first stable tag 2026-07)** | new (late 2025), Docker Inc. behind it | shipped inside the agents | industry spec, mature ecosystem |
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
   vendor CLIs vs. a young v1.x project.
3. **Zero-install** — native sandboxes need nothing (macOS) or two packages
   (Linux); byre needs a container engine running.

**Footnote-tier, not columns:** Dagger's *container-use* (experimental,
parallel-agents-per-git-branch — a different problem; releases stalled since
2025-08). Cloud sandboxes (e2b, Daytona, Modal — account + usage billing,
API-first, for building agent products, not local dev). Single-agent Docker
wrappers (largest, *claudebox* at ~1.1k stars, unmaintained ~10 months).

**The wedge nobody else occupies:** *a personal toolkit that follows
you into any folder + a screen that shows and edits the box's whole
config (every live key editable or shown read-only with its owner
named, PRINCIPLES.md P0) + per-project agent state + local + no-account
+ generated-readable*, all six at once. The toolkit and the screen are
the product (P0). Each competitor concedes at least one: Docker
Sandboxes (account, opacity), native sandboxes (host execution, shared
state), devcontainers (hand-authoring, no state story) — and none of
them has the per-*person* layer at all. That layer is the real answer
to "why not a VPS / raw Docker / devcontainers": your environment
doesn't follow you there. Wedge phrasing is internal: it never migrates
verbatim into conversion copy.

## Site plan

Roles: **site = landing page + canonical operational docs; README =
landing page + conversion summaries**. The devlog stays a private
working record.

### The surfaces and their readers

Five surfaces, two jobs: a surface either **converts** (turns a visitor
into a try) or **serves** (answers a user who already said yes). A page
must know which job it's doing; a page doing both does neither.

| Surface | Reader | Moment | Job |
|---|---|---|---|
| `README.md` | GitHub visitor (human or their agent) | deciding whether to care; ~30 seconds | convert |
| Site landing (`/`) | someone sent a link, or searching | same decision, but off-GitHub -- can carry media | convert |
| Site `/why-not/` | evaluator comparing alternatives | clicked through from either convert surface | convert |
| Site `/docs/` | someone who said yes | installing, configuring, hitting a question | serve |
| Repo `docs/` | contributor, auditor, skill author | verifying or extending byre | serve (deep) |

### Placement principles

Moved to `docs/PLACEMENT.md` (settled reference; the numbering is
unchanged, so `PLACEMENT.md Pn` cites resolve). Two departures were
recorded at extraction: P5's stable-duplication list dropped the
boxed/not-boxed contract, and P2's self-references now name this file.

### Publish-time asciinema demos (PARKED)

**Parked pre-release:** the recording pipeline is built and in-tree
(gated tests that drive real flows, assert every screen, and emit
`.cast` files -- an assertion failure fails the publish), but no casts
are recorded or embedded: the recorded demos weren't yet good enough to
represent the product (pacing, framing). Every slot renders invisibly.
The revival checklist lives in TODO.md's Site item; recording mechanics
and house rules live in `docs/BYRE-DEVELOPMENT.md` ("The demo-recording
tier").

Publish-time demos cover engine-free surfaces only (`byre config`, the
picker to the engine boundary, `status` against a seeded `BYRE_HOME`,
the deliver picker) -- CI never holds agent credentials, so the
develop-into-Claude hero clip stays a deliberately-recorded artifact
(made on the VM with the same verbs, committed as a `.cast`, refreshed
around releases). A flaky demo breaks publishes; the flakes-twice rule
applies. Placement (PLACEMENT.md P11 applied):

| Where | Demo |
|---|---|
| Landing | the hero clip (develop → Claude; VM-recorded, storyboard must show comfort: familiar tools present, agent already authenticated) + `config-tui-walk`, which on revival REPLACES the TUI section's hand-curated text artifact in place. One moving embed per section, never stacked |
| `/docs/quickstart/` | first-run picker + `status` (generated) |
| `/docs/configuration/` | the `byre config` TUI walk -- the flagship generated demo |
| Cookbook | per-recipe where interactive: deliver paste flow, completion (generated); firewall / worktrees (engine-bound → VM-recorded, added when recorded) |
| `/docs/install/`, `/docs/commands/`, volumes, contract | none -- nothing interactive |

A generated demo's slot is its `{{</* demo cast="<slug>" */>}}` shortcode
(grep `{{</* demo` for the inventory); the shortcode renders nothing
visible when the cast is absent and hard-fails under
`HUGO_REQUIRE_CASTS=1` (the deploy workflow). A slot whose cast does not
exist at all yet is marked with an
`<!-- demo-placeholder: <slug> -->` comment -- invisible in the page
(that is the shipped reality on every parked slot; the earlier
blockquote-marker idea never shipped), but the placeholder inventory
stays grep-able. Static screenshots use the same convention
(`<!-- image-placeholder: <slug> -->`).

### User documentation vs Reference

Site `/docs/` splits into two nav groups. **User documentation** is the
approachable tier -- task-first pages a newcomer can read without fear
(the configuration page describes the editor, not the TOML contract).
**Reference** is the precise tier: the configuration reference (full
vocabulary + merge rules), How it works, the security model. The split
is a front-matter flag (`reference: true`), not a URL move -- pages keep
their addresses when they change tier. A user page may always link down
into its reference counterpart; reference pages carry the sharp
register.

### The cookbook

The bar for a cookbook entry: a question a real user has, a one-or-two
line tldr, a shipped feature behind it. Entries group by the reader's
situation (configuring the box; daily workflow; skills & templates;
lifecycle & recovery). The README index carries the show-off subset
only (PLACEMENT.md P6); the rest are cookbook-only, found by question when needed.
The closing entry is always last in both index and cookbook: **"…do
something not listed here?"** -- point your agent at the repo (PLACEMENT.md P4 as a
user-facing feature; the long tail of the cookbook nobody has to
write). Entries that fail the bar stay unwritten -- e.g. recipes that
would advertise a gap ("free disk space"), bless an anti-pattern
against the per-project story ("box a folder of many repos"), or
describe an unshipped feature.

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
