# Firewall skill: default-deny egress (design)

**Status: DRAFT -- under design review, no code yet.** Launch blocker (see
`TODO.md` §1). Decisions below were grilled with Pete 2026-07-05; open
questions are marked.

## Goal

A skill that flips a box's network posture from today's "open" to
deny-by-default egress with an allowlist, so the README contract block's
claim ("enable the default-deny firewall skill to close it") becomes true
and the hero transcript's `network:` line prints `open` or
`deny-by-default` per config, honestly.

Non-goals for v1: domain-level (CDN-proof) filtering, DNS filtering,
rootless engines, inbound rules (ports feature already covers inbound).

## Threat model: the agent, not the user

The wall must be agent-proof; it must never be user-proof. Per the
footgun doctrine (spec, Security contract): a footgun is *accidental*
harm; a user deliberately weakening or removing protections is exercising
a right, and byre's obligation is that `byre status` reports the truth --
never that byre refuses. Concretely:

- Disabling the firewall = removing it from `skills` in config. One edit,
  same as dropping any grant. No dedicated flag in v1.
- Nothing in this design refuses a user configuration. Every bypass a
  user configures is simply reflected in the status posture line.
- Enforcement below is exactly as strong as it sounds *against the
  agent*: it has no path to modifying the rules.

## Decision: host-applied netns rules (not in-container, not a proxy)

Enforcement is iptables/ipset default-DROP inside the **box's network
namespace**, but the rules are applied **from outside the box** by a
run-to-completion helper container. The skill ships **no sudo and grants
the box nothing that holds or can gain CAP_NET_ADMIN** -- the agent runs
as a non-root user with an empty effective capability set, so it is
subject to the rules and cannot modify them. (That's a description of
what this skill needs -- none of it is prohibited. A user who installs
sudo or setuid binaries via their own config has made the tamper
guarantee conditional on their own additions; their call, visible in
their own config. The claim byre stands behind is "no path to NET_ADMIN
*as shipped*", not "no capabilities exist".)

Why this shape:

- **Structural tamper-proofing.** Modifying netns rules needs
  CAP_NET_ADMIN, and no process in the box has a path to it. The
  alternative (Anthropic's
  devcontainer reference: in-container script + pinned sudoers +
  NET_ADMIN on the box) makes the tamper story "agent needs a sudo/root
  bug" and adds a setuid binary to the box. Their placement was forced by
  the devcontainer spec (no host-side orchestrator -- only in-container
  hooks); byre has a host-side binary, so enforcement can stay outside the
  cell. Anthropic's own newer sandbox runtime likewise filters network
  outside the sandbox boundary.
- **Preserves the unprivileged launch path** (the build-time-UID win): no
  root phase returns, no gosu, launcher stays unprivileged.
- **Protocol-transparent.** Unlike a filtering proxy, any client (git+ssh,
  raw TCP, proxy-unaware tools) works against allowlisted IPs.
- Rejected: `--network none` at start + `docker network connect` after
  rules (fragile: resolv.conf/embedded-DNS is wired at container start and
  not rewritten on connect; Podman's CNI backend can't connect running
  containers). Rejected: long-running proxy sidecar (biggest core surface;
  proxy-unaware clients break; keep as a possible v2 for domain-level
  filtering). Rejected: setcap on iptables (agent could use the capable
  binary to flush the rules).

## Flow (marker-gate ordering)

1. `byre develop` starts the box normally (usual network). The launcher
   sees a **gate file** the skill baked into the image and waits for a
   ready signal **at the top -- before context placement and before
   first-run hooks**, not merely before agent exec: hooks are skill code
   that does network I/O (codex device-auth login, literally), so they
   must also run behind the wall (review finding). Timeout (~30s) -> exit
   non-zero -> box dies **offline**, never launches open.
2. byre (host side, concurrently with the attached `docker run`): polls
   until the container is running, then runs the helper:
   `docker run --rm -u 0 --net=container:<box> --cap-add NET_ADMIN
   --entrypoint /usr/local/bin/byre-firewall <the box's own image>`.
   The helper shares ONLY the netns (not fs, not pid). It resolves the
   allowlist domains into an ipset, installs default-DROP OUTPUT rules
   (allow loopback, established/related, DNS to the embedded resolver, the
   ipset), self-verifies (a curl to a non-allowlisted host must FAIL, an
   allowlisted one must succeed -- stolen from Anthropic's script), and
   exits. Lifetime well under a second; nothing keeps running.
3. Ready signal delivered; launcher execs the agent behind the wall.
   **Decided: a loopback socket handshake** -- the launcher listens on
   `127.0.0.1:<port>` and the helper, after rules verify, connects and
   sends the go signal (they share `lo`; the netns IS the channel). No
   filesystem state exists, so nothing can go stale: on any container
   restart the fresh launcher listens, nobody connects, timeout, die
   closed. (A marker file was rejected by review: `/run` is NOT tmpfs in
   Docker by default, so a marker would survive `docker restart` into a
   rule-less netns and silently fail OPEN.)

No second image: the skill bakes iptables/ipset + the script into the box
image via existing `[build]` fields; both are inert to the capless agent.

## Skill/core split

Core stays opinion-free: every core piece is generic mechanism, the skill
supplies all firewall policy.

Skill ships (all via existing skill.toml fields):
- `[build]` apt: iptables, ipset; files: the `byre-firewall` script, the
  launch-gate file.
- The default allowlist and the script logic.

Core grows (generic, not firewall-specific):
- **Launch gate in the launcher:** if a gate file exists in the image,
  wait for the ready signal before exec'ing the agent; timeout -> die.
- **Post-start helper hook:** a skill-declarable "run this entrypoint in
  the box's netns as root after start" step, executed by `develop`
  concurrently with the attached run (goroutine: poll running -> run
  helper -> signal). This is the honest new-orchestration cost.
- **Posture declaration:** a typed skill.toml field (e.g. `[runtime]
  network_posture = "deny-by-default"`) so `byre status` and the launch
  line print the posture without core knowing any skill by name.
  `status.go` currently hardcodes `row("Network", "open")`.
  **Honesty rules** (from review, recalibrated to the footgun doctrine):
  status only claims what byre set up itself; it never refuses anything.
  The trust boundary follows the spec's existing stance -- enabling a
  skill IS trusting it (`skills.go`: skill build content is validated for
  legibility, "not as a trust boundary") -- so *skill* contributions never
  degrade the posture claim (they're attributed in status grants anyway).
  *Project-level raw escape hatches* do, because byre can't audit
  arbitrary argv/Dockerfile text:
  - project `run_args` present: print
    `deny-by-default (raw run_args present -- not guaranteed)`. run_args
    are appended last-wins after byre's flags (`runargs.go`), so
    `--privileged`, `--cap-add NET_ADMIN`, `--user 0`, `--entrypoint`,
    `--dns`, `--network host` can each undermine the gate or the rules.
  - project `dockerfile_pre`/`dockerfile_post` present: same degrade --
    raw build lines run after skill contributions and could remove the
    gate file or alter the script.
  - full-Dockerfile opt-out (`dockerfile = ...`): run it (the user owns
    that infra -- same precedent as the UID bake, which is also skipped
    on this path), but the skill's build bits never land, so print
    `deny-by-default (declared; custom Dockerfile -- byre didn't build
    the wall)`. Never an unqualified claim, never a refusal.

## Allowlist

- Skill ships a conservative default: the enabled agents' API endpoints,
  github.com, and the package registries implied by the box's template.
  OPEN QUESTION: fully static list (like Anthropic's) vs derived from
  enabled skills/template (agent skills declare their own endpoints).
- Per-project additions: `firewall_allow = [...]` in `byre.config` -- it's
  a grant, so it lives in the config cascade next to mounts/ports and
  shows in `byre status` and the config UI.

## Failure modes (analyzed)

- Helper fails / allowlist DNS fails: no signal -> launcher timeout ->
  box dies closed, visible error. Never silently open.
- Out-of-band `docker restart`: recreates the netns, rules vanish -- the
  classic silent-fail-open trap. Covered by the socket handshake having
  **no persistent state**: the restarted launcher listens afresh, nobody
  connects, timeout, box dies closed.
- CDN IP rotation mid-session: allowlist is an IP snapshot at apply time;
  a moved domain starts failing (closed, not open). v1 accepts it;
  re-applying (re-running the helper) is cheap if it bites.
- DNS exfil: the embedded resolver (127.0.0.11) forwards via the daemon
  OUTSIDE the netns, so DNS bypasses egress rules; data can be tunneled
  through DNS. Same hole as Anthropic's reference. Documented, not closed
  in v1 (v2: filtering resolver).
- IPv6: ip6tables default-drop from day one, or a v6-enabled host is a
  full bypass.
- Startup window: between container start and rules landing the network
  is open, but the gate sits at the top of the launcher, so no skill code
  (first-run hooks) and no agent has run yet -- only byre's own launcher,
  idling at the gate. Benign.
- `run_args` escape hatch: raw run_args can bypass everything
  (`--network host`, `--privileged`, `--cap-add`, `--user 0`,
  `--entrypoint`, `--dns`, ...), by design -- but status degrades the
  posture claim whenever they're present (see honesty rules above), and
  they're visible in status grants.
- Rootless engines: v1 is rootful-only (same stance as the baked-UID
  work); helper mechanics under rootless Podman untested.

## Open questions

1. Default allowlist: static vs derived from enabled skills/template.
2. Where the develop-side helper concurrency lives and its failure UX.
3. Re-resolve story for long sessions (accept v1 bluntness? a `byre`
   subcommand to re-run the helper?).

(Resolved by review: ready-signal mechanism = loopback socket handshake;
a filesystem marker fails open across `docker restart` because `/run`
isn't tmpfs in Docker containers by default.)

## Verification

- Helper self-verifies on every apply (deny + allow probes; failure ->
  non-zero -> launcher never unblocks).
- Gated host-side integration test (`BYRE_DOCKER_TESTS=1`): box with the
  skill can reach an allowlisted host, cannot reach others, launcher dies
  closed when the helper is sabotaged. Slots into TODO.md §5.
