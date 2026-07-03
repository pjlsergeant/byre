# byre — positioning & messaging (v1)

> **Decided 2026-07-03** in a grilling session (audience → hook → category →
> honesty → competitive framing → free promise → hero → site/README roles →
> headline). Supersedes the June conversation in
> `positioning-discussion.md` (kept as the record of earlier reasoning).
> Competitive facts below were re-verified against live sources on
> **2026-07-03**; re-check before any launch push.

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
   doesn't", devlog linked as receipts. Declared immaturity builds trust with
   this audience; discovered dishonesty destroys it.
5. **Competitive framing: a full, honest comparison table** — including the
   rows byre loses. Tone rule, verbatim from the session: **"we're not trying
   to persuade, we're trying to illuminate."**
6. **Free promise: structurally free.** MIT, free forever — as in beer and as
   in speech — and *structurally* so: no account to upsell, no control plane
   to meter, no telemetry to monetize. The architecture makes the promise
   credible; this doubles as the differentiator vs account-backed products.
7. **Hero proof: a simplified launch transcript** ending in the agent's own
   UI. The experience is "you're just in Claude Code — you watched the walls
   go up on the way in", not a headless Docker dashboard. (Product
   implication below.)
8. **Site = landing page + real docs. README = landing page + simplified
   docs. Devlog = personal accountability record**, demoted from front door,
   kept honest.
9. **Headline: speak the flag.**

## The marketing message

### Headline

> **`--dangerously-skip-permissions`, minus your machine.**

Immediately followed by (in this order): the two-line quickstart, the
tri-agent mention (so the flag reads as in-group language, not
Claude-exclusivity), and the honest scope sentence (so the headline
overclaims for exactly one line, then gets scoped).

Known risks, accepted: the flag is Claude's and could be renamed; mitigation
is that the sub-copy names Claude/Codex/Gemini and the formal one-liner is
vendor-neutral, so only the H1 would ever need a five-minute edit.

### Message house

**Roof:** `--dangerously-skip-permissions`, minus your machine.

**Pillars:**

1. **One command in.** `cd ~/project && byre develop` — template picked,
   image built, agent launched, repo mounted. The safe way costs nothing.
2. **The box is legible.** byre generates a Dockerfile you can read, prints
   what the agent can touch, and `byre status` answers "what can this thing
   reach?" at any moment. Raw Docker stays first-class; escape hatches in
   both directions.
3. **Nothing to sign into, nothing to meter.** No account, no cloud identity,
   no control plane, no telemetry. A binary on your machine talking to your
   Docker or Podman. MIT, free forever — structurally.

**Foundation (the honest contract, always within one screen of any claim):**
byre boxes your host filesystem, environment, and credentials. It does *not*
restrict the network (open by default), the project mount is read-write by
design, and a container is not a microVM. Early software; sharp edges;
don't point it at anything you can't afford to break.

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
- **Declared immaturity, never discovered.** Sharp edges are named before
  the reader finds them.
- **Never claim "secure".** byre is not a security product and never
  competes on isolation strength. The words are *boxed*, *scoped*,
  *legible* — not *secure*, *safe*, *hardened*.
- **The cowshed is flavor, not load-bearing.** Keep the etymology aside;
  don't make readers parse a metaphor to learn what the tool does.

## Competitive landscape (verified 2026-07-03)

Four columns for the public table; two footnotes. Full sources at the end.

| | **byre** | **Docker Sandboxes** | **Agent built-in sandboxes**¹ | **Dev Containers**² |
|---|---|---|---|---|
| Isolation | container (shared kernel) | **microVM, own kernel — strongest** | OS-level (Seatbelt/Landlock); agent runs **on your host** | container |
| Fresh, throwaway environment | ✔ fresh container per session | ✔ | ✘ — your host, your `$HOME` | long-lived by convention |
| Host files & creds exposed | only what you mount | none | **whole-disk reads + env vars (incl. credentials) inherited by default** | only what you mount |
| Network control | ✘ open by default | ✔ policies | Codex: off by default; Claude: approval proxy | DIY (Anthropic's reference ships a deny-by-default firewall) |
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

**Rows byre loses, stated plainly (keep them in the public table):**

1. **Isolation strength** — a microVM with its own kernel beats a
   shared-kernel container. Don't hedge it.
2. **Network egress control** — three of four columns have a story; byre is
   open-by-default with a firewall skill as future work.
3. **Maturity and backing** — Docker Inc., an industry spec, and 95k-star
   vendor CLIs vs. a pre-1.0 project.
4. **Zero-install** — native sandboxes need nothing (macOS) or two packages
   (Linux); byre needs a container engine running.

**Footnote-tier, not columns:** Dagger's *container-use* (experimental,
parallel-agents-per-git-branch — a different problem; releases stalled since
2025-08). Cloud sandboxes (e2b, Daytona, Modal — account + usage billing,
API-first, for building agent products, not local dev). Single-agent Docker
wrappers (largest, *claudebox* at ~1.1k stars, unmaintained ~10 months).

**The wedge nobody else occupies:** *local + no-account + generated-readable
+ per-project agent state*, all four at once. Each competitor concedes at
least one: Docker Sandboxes (account, opacity), native sandboxes (host
execution, shared state), devcontainers (hand-authoring, no state story).

## README draft

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
telemetry to monetize.

> *byre* (rhymes with *buyer*) is Scots/Northern-English for a cowshed — the
> enclosure you keep the thing in so it doesn't wander off.

## Status: early, moving fast

byre is young and I'm building it in the open. I use it for all my daily
development, but interfaces and config **will change without warning**, and
there are sharp edges around isolation and agent auth. Here's the honest
contract:

- **Boxed:** your host filesystem, environment, and credentials. The agent
  sees only what you mount or pass.
- **Not boxed, by design:** the network (open by default) and the project
  itself (mounted read-write — it's the agent's job to edit it). An agent
  with both can exfiltrate the project it's working on.
- **Not a security product:** a container is not a microVM. If you need the
  strongest isolation story, use one.

Don't point byre at anything you can't afford to break. The
[devlog](https://pjlsergeant.github.io/byre/devlog/) is the running record
of what works and what doesn't.

## How it compares

*Checked against live docs 2026-07-03; corrections welcome.*

[the table above]

## Install / Quickstart / Commands / Configuration

[condensed from current README; deep material links to the site]
```

Two notes on the hero transcript:

- It is a **simplified mock** of the launch. Make it true before or as it
  ships: see product implication below.
- The `network: open` line stays in the hero on purpose — the honest scope
  claim inside the money shot is the whole voice in one line.

## Site plan

- **`index.md` becomes the landing page:** headline, the day-03-style
  screencast as hero (the media the README shouldn't carry), the comparison
  table, structurally-free paragraph, then a docs nav.
- **`/docs/…` becomes real documentation** (install, quickstart, config
  cascade, skills, volumes & state, security contract, commands) — the README
  keeps only the simplified versions and links here.
- **Devlog moves to `/devlog/`**, framed as "built in the open — the honest
  record." It's a personal accountability artifact; it stops being the front
  door but stays linked from the status block as receipts.
- One message everywhere: README converts a repo visitor in 30 seconds; the
  site is the shareable link that *shows* the drop-in moment and holds the
  depth.

## Product implications (small, on-brand, argued for by this positioning)

1. **Print the grant summary on launch.** `byre develop` should emit the
   3–4 terse `byre:` lines from the hero (project mount, host mounts,
   network, agent) before exec'ing the agent — so the README hero is a real
   transcript, and every session opens by showing the walls going up.
2. **A firewall skill raises the floor on the weakest table row.** Network
   control is byre's clearest honest loss (Codex defaults network-off;
   Anthropic's devcontainer ships deny-by-default). A default-deny egress
   skill — even a blunt allowlist — flips that row from ✘ to opt-in ✔ without
   making core opinionated. Worth sequencing.
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
