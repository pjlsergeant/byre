# byre — positioning & messaging (v1)

> **Decided 2026-07-03** in a grilling session (audience → hook → category →
> honesty → competitive framing → free promise → hero → site/README roles →
> headline). Supersedes the June conversation in
> `positioning-discussion.md` (kept as the record of earlier reasoning).
> Competitive facts below were re-verified against live sources on
> **2026-07-03**; re-check before any launch push.
>
> **Amended 2026-07-04** in a second grilling session, against new evidence:
> the under-claimed personal-toolkit strength, two Slack feedback threads,
> and a claude-pod review. Amendments are marked inline; the evidence is
> recorded in "Reader-response evidence" below.

## Positioning statement

For **developers who already run coding agents daily and want full autonomy
without handing the agent their machine**, byre is **the local-first,
Docker-native project harness for autonomous coding agents**: one command puts
Claude Code, Codex, or Gemini in a throwaway, project-scoped container that
sees the repo and what you explicitly grant — nothing else. Unlike
account-backed sandbox products, agents' built-in host sandboxes, or
hand-authored devcontainers, byre needs **no account, no cloud, and no
authoring** — and everything it does is a generated file you can read.

## The decisions

1. **Audience: YOLO-mode agent users.** People already running Claude
   Code/Codex/Gemini daily, using or tempted by `--dangerously-skip-permissions`
   / full-auto modes. They already believe in agents; they need the enclosure.
   Not (yet): agent-skeptics, teams/eng-leads, Docker aesthetes — those are
   secondary reads, not the target.
2. **Hook: the YOLO enclosure, fused with effortlessness.** One promise, not
   two: *the safe way is also the easy way.* People YOLO on the host because
   the host is zero-effort; byre wins only if the box costs nothing to enter.
3. **Category language:** "sandbox" / "the box" colloquially (the audience's
   words); "local-first, Docker-native project harness for autonomous coding
   agents" as the formal one-liner. The explicit security contract does the
   honesty work so the colloquial word never overclaims.
4. **Honesty placement: hook first, honest status second.** The README opens
   with the pitch, then a candid status block — reworded from "do not use
   this" to "early and moving fast; here's exactly what works and what
   doesn't". The README itself is the authority on current status; the
   devlog is linked only as "see what's being built". Caveats are time-scoped
   ("for now") — they describe age, not design. Declared immaturity builds
   trust with this audience; discovered dishonesty destroys it. (Amended
   2026-07-04: the *detailed* status block moves down the page; what stays
   at the top — as roughly the third sentence — is one bold line: **THIS IS
   A VERY YOUNG AND FAST-CHANGING PROJECT**. Signal early, detail later: a
   reader who bails at 20% of the page still leaves warned. Prompted by a
   reader who read ~20% and called the page ~50% too long.)
5. **Competitive framing: full and honest, expressed as a "Why not X?"
   list** — one prose entry per alternative, each answering honestly
   (including "use it if…" where the alternative wins), rather than a
   feature grid that flattens microVM-vs-container into checkmarks. The
   fact table below stays as the internal evidence base. Tone rule, verbatim
   from the session: **"we're not trying to persuade, we're trying to
   illuminate."**
6. **Free promise: structurally free — later merged with reversibility.**
   MIT, free forever — as in beer and as in speech — and *structurally* so:
   no account to upsell, no control plane to meter, no telemetry to
   monetize. The architecture makes the promise credible; this doubles as
   the differentiator vs account-backed products. (Amended same session,
   after outside feedback: reversibility — eject via `byre dockerfile`,
   clean removal via `byre forget`, no relationship to unwind — is the same
   fact viewed from the other side, so the two share one pillar: "nothing to
   sign into, nothing to unwind.")
7. **Hero proof: a simplified launch transcript** ending in the agent's own
   UI. The experience is "you're just in Claude Code — you watched the walls
   go up on the way in", not a headless Docker dashboard. (Amended later the
   same session: the transcript shows the **first run** — `brew install`,
   `byre develop`, the two-question picker with Enter-able defaults, one
   walls-up line, then the agent banner — and sits **directly under the
   headline**, before any prose; the explanation follows the evidence, and
   the transcript gets no caption. The onboarding IS the demo: it answers
   "too busy to twizzle" by letting the reader count the steps themselves.)
   (Amended 2026-07-04: the mandatory plain what-it-is sentence now sits
   between the headline and the transcript — see decision 9.)
8. **Site = landing page + real docs. README = landing page + simplified
   docs. Devlog = personal accountability record**, demoted from front door,
   kept honest.
9. **Headline: speak the flag — and risk the farm.** (Amended 2026-07-04;
   the reasoning and the accepted trade are under "Headline" below.)

## The marketing message

### Headline

> **`--dangerously-skip-permissions`, without risking the farm.**

(2026-07-04, replacing "…, minus your machine.") "Cute is the asset": the
idiom stands on its own even without the byre/farm resonance, and the
resonance -- byre is a cowshed; the farm is what it protects -- pays off on
the second read rather than being required on the first. Comprehension was
tested by showing three LLMs the headline alone and asking what the project
does: two of three reconstructed the product almost exactly ("YOLO mode,
but fenced in"; "blast radius confined to the sandbox"); the third drifted
to generic CI tooling -- the already-accepted doesn't-know-the-flag failure
mode. The flag carries the meaning; the farm carries the tone.

The accepted trade: the H1 is now a safety **idiom**, not a scope statement
-- "minus your machine" said what's out of reach; "the farm" doesn't. So
the **plain what-it-is sentence directly under the headline is now
mandatory** (previously a nice-to-have): one or two flat declarative
sentences carrying all the precision the old H1 carried, built from Pete's
own Slack phrasing -- the version a cold reader endorsed verbatim as "what
I would put at the top of the readme": *"if you want to regularly run
agents with `--dangerously-skip-permissions` in many different folders,
but don't trust the agent not to run `git push` as you, or go digging
around in other folders."* Order under the headline: plain sentence (with
the tri-agent mention), then the hero transcript, then the bold
young-project line (see decision 4).

Known risks, accepted: the flag is Claude's and could be renamed (sub-copy
still names all three agents; the H1 stays a five-minute edit). "Risking
the farm" is an Anglo idiom; non-native readers get the precision from the
plain sentence one line later. One cold reader (2026-07-03) flagged
flag-based headlines as "Claude trying hard to write marketing copy" --
judged an overreaction, but if cold readers repeat the complaint
post-launch, that's the tripwire to revisit.

### Message house

**Roof:** `--dangerously-skip-permissions`, without risking the farm.

**Pillars:**

1. **One command in.** `cd ~/project && byre develop` — template picked,
   image built, agent launched, repo mounted. The safe way costs nothing.
   And it scales the git way: `byre worktree <name>` spins up a parallel
   agent in one step — new linked worktree, own container, inheriting the
   repo's image, volumes, and login; hand-made `git worktree add` worktrees
   are detected and inherit identically (shipped 2026-07-03; see
   `docs/agent-volume-sharing.md`).
2. **The box is legible — and legibility is editable.** byre generates a
   Dockerfile you can read, and `byre status` answers "what can this thing
   reach?" at any moment. `byre config` is the same idea as an editor: an
   interactive terminal UI organized around grants ("what can this box
   reach?"), not TOML fields — adding a package or mounting a repo
   read-only takes seconds, in the same grant vocabulary status prints; and
   `--self-edit` lets the agent tune its own box as an explicit, announced
   grant. Raw Docker stays first-class; escape hatches in both directions.
3. **Nothing to sign into, nothing to unwind.** No account, no cloud
   identity, no control plane, no telemetry — MIT, free forever,
   structurally. And no exit cost: `byre dockerfile` prints a plain
   Dockerfile (your setup survives byre), `byre forget` deletes every trace
   of a project, and byre never writes into your project tree. Easy to
   adopt, just as easy to leave — there is no mechanism by which byre
   *could* hold you.
4. **The box is stocked -- with your stuff.** (Added 2026-07-04 -- the
   under-claimed strength.) `~/.byre` accumulates your building blocks:
   your baseline config, your templates, your skills. Every box you enter,
   in any directory, is already your environment -- curl is there, your
   Claude skills are there, your usual tools are there -- with no
   per-project setup. A bare host in a strange folder has none of that
   curation either: the box isn't just as easy as the host, it's more
   comfortable. This pillar is the real answer to "why not a VPS / raw
   Docker / devcontainers" (your environment doesn't follow you there).
   Evidence it was missing: Pete reaches for it spontaneously when
   pitching ("over time I want a variety of tools to live in these little
   containers... I may wanna stick pgclient in a byre container with node
   in it. I'm not sure the README makes that that clear.")

**Foundation (the honest contract, always within one screen of any claim):**
byre boxes your host filesystem, environment, and credentials. The network is
open by default — the default-deny firewall skill closes it when you enable
it — the project mount is read-write by design, and a container is not a
microVM. Early software; sharp edges; for now, don't point it at anything
you can't afford to break. (The status caveats are time-scoped on purpose —
"for now" — because they describe the project's age, not its design.)

### One-liners for different slots

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

### Voice rules

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
- **The cowshed is flavor, not load-bearing.** Keep the etymology aside;
  don't make readers parse a metaphor to learn what the tool does.
  (Amended 2026-07-04: the farm is now in the H1, so the metaphor is
  allowed to be load-bearing *there* -- and the etymology blockquote
  becomes the headline's payoff. Everywhere else the rule stands.)
- **Break the aphorism cadence.** (Rewrite-pass note, 2026-07-04 -- a craft
  instruction, not doctrine.) Reader feedback flagged three body sentences
  as AI-sounding; what they shared wasn't length but rhythm -- every
  sentence landing as an epigram. Most sentences should state their fact
  flatly; spend the epigram on the two or three places that earn one (the
  headline already is one).

## "Why not X?" — the public competitive copy

This is the artifact the README and site carry (the fact table further down
is the internal evidence base behind it):

```markdown
## Why not…?

byre is a thin layer over the Docker or Podman you already run. The
alternatives:

**…raw Docker?** Nothing — and byre never takes it away. You'd just be
hand-rolling what it generates: host-matched file ownership, per-project
agent login that survives rebuilds, templates, a clean reset. If you want to
stop using byre, `byre dockerfile` prints your exit.

**…Docker Sandboxes™?** Commercial product with a hosted control plane (you
sign in) and paid tiers. Not open source. *(But it gives you kernel-level
microVM isolation, and we don't.)*

**…your agent's built-in sandbox?** All-or-nothing file isolation, on your
real machine, wearing your identity — env vars and credentials come along by
default, so a stray `git push` goes out as *you*. byre's box holds nothing
you didn't put in it.

**…nothing — just keep YOLOing on the host?** The host is the incumbent:
zero setup, and nothing bad has happened yet. But the agent works as you,
in your real home dir — byre exists because Claude went editing a sibling
repository and did things with an ssh key it shouldn't have. The box costs
one command, so the host's convenience argument is gone. *(If you've never
had the scare, you may not feel the need — byre is for after your first
one.)*

**…devcontainers?** You hand-write the Dockerfile and JSON per project, and
wire up agent credentials yourself. byre generates the Docker from config —
`byre config` adds a package, mounts another repo read-only, or swaps agents
in seconds. *(But it's the mature industry spec, and we're young.)*

**…container-use?** Explicitly experimental, and MCP-shaped: your agent
manages a fleet of environments; you don't sit inside one. byre does
parallel the git way — one boxed session per worktree, sharing the repo's
image, volumes, and agent login.

**…a cloud sandbox (e2b, Daytona, your agent's web offering)?** Account,
usage billing, your code in their cloud — and they're repo-shaped, built
for shipping agent products or driving a GitHub repo. byre is for dropping
into whatever folder you're standing in.

**…a cheap VPS (a Hetzner box)?** A box per project doesn't scale across
many repos — and half of what you'd point an agent at isn't a repo, just a
folder. byre is a throwaway box per folder, on the machine you're already
sitting at, with your toolkit already inside. *(But a remote box is real
hardware isolation — if the agent must never share a kernel with your
machine, rent one.)*
```

Format rules for these entries: **answer the question literally** — "why
not X?" gets X's actual drawbacks, crisp and factual, as the lede; the
honest concession goes in a trailing italic parenthetical *("but it gives
you kernel-level isolation, and we don't")*, never as the opening — and only
when it's a concession this audience actually feels (microVM isolation yes;
"no install needed" no, they already run Docker). 2–3 sentences. Where the
drawback is "you do it by hand" (devcontainers, raw Docker), byre's
conveniences ARE the answer — that's where `byre config`, templates,
per-project login, and the eject path live. Order and the ™ are
load-bearing: the priming line plus "…raw Docker?" first teach the reader
that byre IS ordinary Docker underneath *before* they meet the Docker
Sandboxes™ name — which reads like a category but is a product; the ™ marks
it as one. (Real reader confusion, 2026-07-03: "did you build your own
container infra?" — and confirmed a second time 2026-07-04: "I'd be
explicit that you mean the enterprise product.")

Added 2026-07-04 from reader feedback (see "Reader-response evidence"):
the "…just keep YOLOing?" and "…a cheap VPS?" entries, and the cloud entry
extended to absorb agent-vendor web offerings. The list is now eight
entries — acceptable because the section is demoted below the fold
(amended decision 4), where it reads as reference, not pitch.

## Competitive fact base (internal — verified 2026-07-03)

Full sources at the end.

| | **byre** | **Docker Sandboxes** | **Agent built-in sandboxes**¹ | **Dev Containers**² |
|---|---|---|---|---|
| Isolation | container (shared kernel) | **microVM, own kernel — strongest** | OS-level (Seatbelt/Landlock); agent runs **on your host** | container |
| Fresh, throwaway environment | ✔ fresh container per session | ✔ | ✘ — your host, your `$HOME` | long-lived by convention |
| Host files & creds exposed | only what you mount | none | **whole-disk reads + env vars (incl. credentials) inherited by default** | only what you mount |
| Network control | ✔ default-deny egress skill\* (open by default without it) | ✔ policies | Codex: off by default; Claude: approval proxy | DIY (Anthropic's reference ships a deny-by-default firewall) |
| Account / sign-in | none | **Docker OAuth required** | vendor auth only (needed anyway) | none |
| Setup per project | one command; config generated | low (product flow) | zero | hand-author `devcontainer.json` + Dockerfile |
| Definition you can read | ✔ generated Dockerfile | partial (templates yes; runtime is product machinery) | n/a | ✔ — because you wrote it |
| Per-project agent auth & state | ✔ named volumes; `reset` / `rehome` | no per-project story | shared host state | DIY |
| Engines | Docker & Podman | own runtime | none needed | Docker |
| Maturity | **early, pre-1.0** | new (late 2025), Docker Inc. behind it | shipped inside the agents | industry spec, mature ecosystem |
| Price / license | MIT, free forever | CLI free; org governance needs Docker Business ($24/user/mo); proprietary | free with the agent | open spec, MIT CLI |

\* The default-deny firewall skill is committed for launch. The public copy
that claims it (the README contract block's "enable the default-deny
firewall skill to close it") does not go live before the skill ships; until
then the claim carries an asterisk here and appears nowhere else.
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

(Network egress control *was* a fourth loss; the default-deny firewall skill
is now a launch commitment, so public copy may claim it — asterisked here
until it ships.)

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
has the per-*person* layer at all (fifth element added 2026-07-04 with
pillar 4).

## Reader-response evidence (added 2026-07-04)

Two Slack feedback threads on the README draft, plus a competitor review.
What changed because of them is recorded in the amended decisions above;
this records the evidence itself.

**Thread 1 (README feedback):** one reader managed ~20% of the page and
called it ~50% too long; wanted Install/Quickstart surfaced and "Why
not…?" / status demoted; flagged three body sentences as AI-sounding
verbosity (their shared trait: aphorism cadence, not length). A second
reader couldn't tell what the product was from the top of the page and
asked for "one sentence at the top which just plainly says what it is" —
then endorsed, verbatim, Pete's off-the-cuff use-case sentence as the
thing to put there. The repeated pattern across both threads: Pete's
spontaneous phrasings ("it's instant per-folder sandboxes", the use-case
sentence, "stop you needing to twizzle", "I don't want a Hetzner box per
repo") outperformed the crafted copy. Raw material — mine these before
writing new lines.

**Thread 2 (gist feedback):** confirmed the Docker Sandboxes™ confusion a
second time. Produced the missing "Why not…?" objections — "why not a
cheap Hetzner box?" and, from a reader who doesn't feel the pain, why not
just not bother — whose in-thread answers became the new entries. Audience
refinements for the fact base: part of the YOLO population is unreachable
(less-technical users who don't care — they'd never adopt tooling like
this); maturity-signal seekers won't adopt pre-1.0 regardless (accepted
loss, already in the loses-honestly list); per-case-isolation users who
enjoy hand-configuring each environment are correctly out of scope. And
the toolkit strength surfaced spontaneously in Pete's own pitching (see
pillar 4).

**claude-pod (github.com/trekhleb/claude-pod), reviewed 2026-07-04:** the
nearest neighbor — a four-file, ~200-line, Claude-only Docker wrapper
(MIT, 21 stars) whose README leads with the same flag. Machine-wide auth,
`node:24-slim` base (any other stack means editing the Dockerfile
yourself), no templates, no multi-agent, no worktrees, no config model.
Decision: no public mention (not popular enough to name), and nothing
adopted from its README either — five candidate steals were reviewed and
rejected wholesale. It stands in the fact base as proof of the "I could
write this in 200 lines" objection, and of what 200 lines doesn't get you
(pillar 4).

**Conscious negatives from this round:** no path nannying — byre will
happily box `~` or `/`; Pete deliberately runs it on `~/.byre` itself ("a
knife needs to be sharp"). No approval-fatigue framing fact (a reader's
TouchID-Pavlov line was considered; Pete has a different project for
that). No new voice doctrine from the register complaints — one
rewrite-pass note only (see voice rules).

## README draft

> **Superseded by `README-next.md`** (the living draft, at repo root) —
> that file is where copy evolves; this section records the original shape
> and the notes that motivated it. When they disagree, README-next.md wins.

Replace the current hero + warning with the following shape (Install,
Commands, Configuration etc. survive below it, condensed; full docs move to
the site):

```markdown
# byre

**`--dangerously-skip-permissions`, minus your machine.**

    cd ~/project && byre develop

One command drops you into Claude Code, Codex, or Gemini — running
full-autonomy in a throwaway container that sees this project and what you
explicitly grant. Your home dir, keys, and the rest of your machine stay
outside the box.

    $ byre develop
    byre: image cached, launching
    byre: ~/project -> /workspace (rw)
    byre: host mounts: none · network: open
    byre: agent: claude — full autonomy inside the box

    ╭──────────────────────────────────╮
    │ ✻ Claude Code                    │
    │   /workspace                     │
    ╰──────────────────────────────────╯

No account. No cloud. No control plane. byre is a single MIT-licensed Go
binary that generates a Dockerfile you can read and hands it to your local
Docker or Podman. Free forever — as in beer and as in speech — and
structurally so: there's no account to upsell, no service to meter, no
telemetry to monetize. Leaving is as easy as trying it: `byre dockerfile`
prints a plain Dockerfile you keep, and `byre forget` removes every trace.

> *byre* (rhymes with *buyer*) is Scots/Northern-English for a cowshed — the
> enclosure you keep the thing in so it doesn't wander off.

## Status: early, moving fast

byre is young and I'm building it in the open. I use it for all my daily
development, but interfaces and config **will change without warning**, and
there are sharp edges around isolation and agent auth. Here's the honest
contract:

- **Boxed:** your host filesystem, environment, and credentials. The agent
  sees only what you mount or pass.
- **Not boxed, by design:** the network (open by default — enable the
  default-deny firewall skill to close it) and the project itself (mounted
  read-write — it's the agent's job to edit it). An agent with an open
  network can exfiltrate the project it's working on.
- **Not a security product:** a container is not a microVM. If you need the
  strongest isolation story, use one.

For now: don't point byre at anything you can't afford to break. The
[devlog](https://pjlsergeant.github.io/byre/devlog/) shows what's being
built.

## Why not…?

*Claims checked against live docs 2026-07-03; corrections welcome.*

[the "Why not…?" list above, verbatim]

## Install / Quickstart / Commands / Configuration

[condensed from current README; deep material links to the site]
```

Two notes on the hero transcript:

- It is a **simplified mock** of the launch, and that's fine — it's
  illustrative, shaped like the truth (nothing in it claims a grant byre
  doesn't enforce). Making `byre develop` actually print those lines is a
  nice-to-have, not a prerequisite.
- The `network: open` line stays in the hero on purpose — the honest scope
  claim inside the money shot is the whole voice in one line.

## Site plan

- **`index.md` becomes the landing page:** headline, the day-03-style
  screencast as hero (the media the README shouldn't carry), the comparison
  table, structurally-free paragraph, then a docs nav.
- **`/docs/…` becomes real documentation** (install, quickstart, config
  cascade, skills, volumes & state, security contract, commands) — the README
  keeps only the simplified versions and links here.
- **Devlog moves to `/devlog/`.** It's a personal accountability artifact;
  it stops being the front door and is linked simply as "see what's being
  built" — never as the authority on what works (that's the README's job).
- One message everywhere: README converts a repo visitor in 30 seconds; the
  site is the shareable link that *shows* the drop-in moment and holds the
  depth.

## Product implications (small, on-brand, argued for by this positioning)

> Tracked authoritatively in `TODO.md` (launch blockers + nice-to-haves);
> this section holds the positioning argument for *why*.

1. **(Nice-to-have) Print the grant summary on launch.** The hero transcript
   is an accepted illustrative mock — no product change required. Still, a
   few terse `byre:` lines (project mount, host mounts, network, agent)
   before exec'ing the agent would make every real session open by showing
   the walls going up. Do it when convenient, not for launch.
1b. **(Launch blocker) `brew install byre` must work.** The hero and Install
   section lead with it — the two-command story depends on it. A tap
   (`pjlsergeant/tap/byre`) is enough; update the copy to whichever form
   ships. (Alongside the firewall skill, this is the second gate on going
   live; both are listed in README-next.md's header comment.)
2. **The default-deny firewall skill is a launch blocker.** The public copy
   claims it (the README contract block: "enable the default-deny firewall
   skill to close it"), so it must exist before the README/site go live. A
   default-deny egress skill — even a blunt allowlist — keeps core
   opinion-free while closing the one gap where three competitors had a
   story and byre had none. Once it ships, the hero transcript's `network:`
   line becomes live proof (it prints `open` or `deny-by-default` per
   config).
3. **Keep `byre status` output in lockstep with the marketing block** — the
   README/site show its output as proof; drift makes the proof a lie.

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
