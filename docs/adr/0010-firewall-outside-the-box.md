# Firewall rules are applied from outside the box

The firewall skill's deny-by-default egress is enforced by iptables
rules **inside the box's network namespace** but applied **from outside
it**: a run-to-completion helper container (the box's own image, `-u 0`,
`--net=container:<box>`, `--cap-add NET_ADMIN`, sharing only the netns)
installs per-IP ACCEPT rules + a default-DROP OUTPUT policy (v4 and v6),
self-verifies with a deny probe, and exits. The box itself gains no sudo,
no capabilities, and no setuid binaries -- the agent has **no path to
CAP_NET_ADMIN**, so the wall is structurally tamper-proof against it
while remaining one config edit away from off for the user
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
- Rootful engines only in v1 (same stance as ADR 0008).
- Status honesty rules: skill contributions never degrade the posture
  claim (enabling a skill is trusting it), but project-level raw escape
  hatches do -- `run_args` or `dockerfile_*` present prints
  `deny-by-default (raw run_args present -- not guaranteed)`, and the
  full-Dockerfile opt-out prints `declared; custom Dockerfile -- byre
  didn't build the wall`. Never an unqualified claim, never a refusal.
