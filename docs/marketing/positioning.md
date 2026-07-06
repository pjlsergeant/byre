# byre -- positioning: evidence & authoring rules

> **Live copy is `README.md`** (repo root); when this file and the README
> disagree, the README wins. This file holds what steers *future* copy:
> the audience definition, voice rules, reusable one-liners, the
> competitive evidence base, and the site plan.
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
  googled. The farm in the H1 is cute, and that's where it stops; any cow
  is site-side visual flavor, never README prose. (Amended 2026-07-06,
  replacing the etymology-blockquote-as-payoff rule -- the blockquote is
  off the page.)
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

H1 evidence: one cold reader flagged the flag-based headline as marketing
copy trying too hard, and the H1 is a safety idiom, not a scope statement
-- both risks accepted; the plain what-it-is sentence under the H1 is the
mandatory mitigation, and `TODO.md`'s post-launch tripwire watches for
cold readers bouncing.

## Site plan

Roles: **site = landing page + real docs; README = landing page +
simplified docs; devlog = personal accountability record**, never the
front door. (Open work; tracked as a `TODO.md` pointer to this section.)

- **`index.md` becomes the landing page:** headline, the day-03-style
  screencast as hero (the media the README shouldn't carry), the comparison
  table, structurally-free paragraph, then a docs nav.
- **`/docs/…` becomes real documentation** (install, quickstart, config
  cascade, skills, volumes & state, the boxed/not-boxed contract, commands)
  — the README keeps only the simplified versions and links here.
- **Devlog moves to `/devlog/`** — linked simply as "see what's being
  built", never as the authority on what works (that's the README's job).
- One message everywhere: README converts a repo visitor in 30 seconds; the
  site is the shareable link that *shows* the drop-in moment and holds the
  depth.

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
