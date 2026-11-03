# Firewall rules are applied from outside the box

The firewall skill's deny-by-default egress is enforced by iptables
rules **inside the box's network namespace** but applied **from outside
it**: a run-to-completion helper container (the box's own image, `-u 0`,
`--net=container:<box>`, `--cap-add NET_ADMIN`, sharing only the netns)
installs per-IP ACCEPT rules + a default-DROP OUTPUT policy (v4 and v6),
self-verifies with a deny probe, and exits. The box itself gains no sudo,
no capabilities, and no setuid binaries -- the agent has **no path to
CAP_NET_ADMIN**, so it cannot alter the rules, while the wall stays one
config edit away from off for the user
(PRINCIPLES.md #1).

Considered and rejected:

- **In-container script + pinned sudoers + NET_ADMIN on the box**
  (Anthropic's devcontainer reference): tamper story becomes "agent needs
  a sudo/root bug" and adds a setuid binary. Their placement was forced
  by having no host-side orchestrator; byre has one.
- **`--network none` at start + `docker network connect` after rules**:
  resolv.conf/embedded DNS is wired at container start and not rewritten
  on connect; Podman's CNI backend can't connect running containers.
- **Long-running proxy sidecar**: biggest core surface, breaks
  proxy-unaware clients (git+ssh, raw TCP). Kept as a v2 candidate for
  domain-level (CDN-proof) filtering.
- **setcap on iptables in the box**: the agent could use the capable
  binary to flush the rules.

Consequences / accepted holes (documented, not closed in v1):

- The helper is targeted by a per-invocation crypto-nonce label
  (`byre.run=<nonce>`) + resolved container ID -- names and path-derived
  labels are forgeable by a planted container, which could otherwise
  capture the root+NET_ADMIN helper.
- DNS goes via the engine's embedded resolver *outside* the netns, so
  data can be tunneled through DNS (same hole as Anthropic's reference;
  v2 candidate: filtering resolver).
- The allowlist is an IP snapshot at apply time; a CDN rotating IPs
  mid-session fails **closed**, not open. Re-applying is cheap.
- Rootless Podman is supported via ADR 0032 (the generic 1000:1000 image
  under `--userns=keep-id`).
- Status honesty rules: a skill contribution byre built as asked never
  degrades the posture claim (declared egress, a mount, an apt line:
  enabling a skill is trusting it, P2, and the grant is attributed on
  `byre status` rather than hedged into a claim), but project-level raw
  escape hatches do -- `run_args` or `dockerfile_*` present prints
  `deny-by-default (raw run_args present -- not guaranteed)`. Never an
  unqualified claim, never a refusal. Two cases sit outside that rule, and
  which config field a contribution rides decides neither:
  - **A contribution that DISPLACES byre's own machinery is disclosed.** A
    skill's `[runtime].env` setting a reserved `BYRE_` knob gets an
    attributed `Reserved env` row and degrades exactly the claims that
    knob names (ADR 0050); a skill's `[[mounts]]`/`[[volumes]]` covering a
    byre-managed path gets one blanket Containment line, because a path
    says nothing about which claims broke (ADR 0052). Neither reports
    distrust of the skill.
  - **What the box does THROUGH a granted channel is disclaimed once,
    never degraded per claim** -- ADR 0027's warranty model: byre warrants
    its own construction, never the hole's consequences. A residual of the
    channel itself (the DNS tunnelling above, an allowlisted CDN, a skill
    whose function is a tunnel) is true for every configuration that
    enables it, so it is published on the user-facing security-model page
    rather than rendered per box.
