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
- Rootful engines only in v1 (same stance as ADR 0008). **Superseded by
  ADR 0032**: rootless Podman is first-class, running the generic 1000:1000
  image under `--userns=keep-id`.
- Status honesty rules: skill contributions never degrade the posture
  claim (enabling a skill is trusting it), but project-level raw escape
  hatches do -- `run_args` or `dockerfile_*` present prints
  `deny-by-default (raw run_args present -- not guaranteed)`, and the
  full-Dockerfile opt-out printed `declared; custom Dockerfile -- byre
  didn't build the wall` (the opt-out was since removed -- ADR 0014).
  Never an unqualified claim, never a refusal. **Annotated below**: the
  sentence about skill contributions is narrower than it reads.

## Annotation, 2026-07-28: what "skill contributions never degrade" covers

The bullet above is the sentence a later reader would cite to remove a
degradation as a defect, and read alone it says more than it means. Two
decisions since (ADR 0050, ADR 0052) have settled the cases it does not
cover. The boundary is a TEST, not a field kind -- which config field a
contribution rides decides nothing:

- **A contribution byre built as asked never degrades the claim.**
  Declared egress, a declared mount, an apt line: byre generated exactly
  what the skill asked for, so the resulting box IS byre's construction
  working. Enabling a skill is trusting it (P2), and the grant is
  attributed on `byre status` rather than hedged into a claim.
- **A contribution that DISPLACES byre's own machinery is disclosed,
  whatever field it rides.** A skill's `[runtime].env` setting a reserved
  `BYRE_` knob is one instance (ADR 0050 tier 2: accepted, attributed in
  a `Reserved env` row, and every claim it can skew stops asserting); a
  skill's `[[mounts]]`/`[[volumes]]` covering a byre-managed path is the
  other, decided in ADR 0052 rather than re-derived here (one blanket
  containment line, skills included and attributed). Neither reports
  distrust of the skill. Both report that the construction byre's claim
  describes is no longer the thing in force.
- **What the box does THROUGH a granted channel is disclaimed once,
  loudly, never degraded per claim.** A tunnel over an allowlisted 443,
  data pushed down the DNS channel this ADR accepts above, anything done
  through the docker socket: consequences of a grant, not defects in the
  construction. `docs/DOCKER-HOST.md`'s warranty model is the rule --
  byre warrants its own construction, so each status row keeps describing
  what byre built and holds for the box. Which surface carries the
  disclaimer depends on what the grant is: a hole THIS configuration
  actually has -- a skill's declared containment, the docker socket among
  them (ADR 0027), and a mount or volume over a byre-managed path (ADR
  0052) -- gets a per-configuration Containment line on `byre status`,
  develop and the apply review, disclaiming in one place including
  consequences nobody has enumerated. (The reserved-env displacement in
  the bullet above does NOT ride this line: it renders as its own
  attributed `Reserved env` row and degrades the claims it names, per ADR
  0050. Two displacement mechanisms, two vehicles.) A
  residual of the CHANNEL itself -- DNS tunnelling, an allowlisted CDN
  fronting many services, a skill whose function is a tunnel -- is true of
  the mechanism for every configuration that enables it, so there is no
  per-box fact for status to render: it is published on the user-facing
  security-model page, where the doctrine index's residual rule puts it.

**Why a `BYRE_` key degrades a NAMED claim while a mount gets a blanket
line.** The two look like one event and are reported differently on
purpose. A reserved env key NAMES the knob, so byre knows which claims it
can skew and degrades exactly those (`BYRE_LAUNCH_GATE_FILE` -> the
network claim, `BYRE_MCP_CONFIG` -> MCP delivery, an unrecognised knob
conservatively). A mount covers a PATH, and byre knows a path is covered
and nothing about what covers it -- naming the claims that broke would
imply an enumeration byre does not have, so what it says is the scope it
stops warranting, said the same way whichever path was hit (ADR 0052).

**The DNS residual is published.** The tunnelling hole accepted above
reaches users on the security-model page as "an allowlisted host is a
channel, not a permission"
(`site/content/docs/security-model.md`), which carries the CDN and
tunnel-shaped-skill cases in the same disclaimer. The doctrine index's
rule is why: a disclosure only contributors can find has not been made.
