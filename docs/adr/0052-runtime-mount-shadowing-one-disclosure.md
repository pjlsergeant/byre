# A mount over a byre-managed path gets one blanket disclosure, skills included

> **Amended by ADR 0057**: its credential tmpfs `/run/byre` is a managed
> root (below).

Decided 2026-07-28. A `[[mounts]]` or `[[volumes]]` target covering a path
byre bakes replaces byre's file in the RUNNING box. byre reports this once,
in the containment register, and does not degrade the individual claims that
read from those paths. Skill-declared mounts and volumes are checked the same
way the project's own are, attributed to the declaring skill. `files`-based
shadowing keeps its granular, per-path reporting.

Principles: P1 (the footgun doctrine -- tell, never gate); P4 (a claim byre
cannot stand behind is qualified, never silently asserted). Related: ADR
0027 (the warranty model this register comes from), ADR 0011 (the launch
gate), ADR 0033, ADR 0039, ADR 0046 (the baked artifacts).

## The fact

byre bakes its own machinery into the image and then makes claims that read
from it: `/usr/local/bin/byre-launch` (the ENTRYPOINT),
`/usr/local/bin/byre-headless` (the headless-run chassis, ADR 0061),
`/etc/byre/launch-gate` (the fail-closed wait), the netns enforcement script a
network-posture skill declares, and the delivery artifacts under `/etc/byre`
-- `mcp.json`, the agent context, the Claude Skill tree, the credential
receiver. At run time it also owns `/run/byre`, the tmpfs credential delivery
lands on (ADR 0057).

At BUILD time a `files` entry landing on one of the security paths is
harmless: ADR 0011's build-tail re-assertion puts byre's copy back, and a note
says the entry did not take effect. There is no runtime equivalent. A bind or a named volume is applied by
the engine over the built image, and byre has nothing that runs after it.

The sharpest instance is `[[volumes]] target = "/etc/byre"`. What the box sees
there is the volume's content, and a volume is filled once: an empty new one
picks up the image directory when the engine first mounts it, a `seed`ed one
is populated by byre before the box mounts it at all (so the image's copy
never lands), and a bind mount of a host directory skips the question
entirely. After that the volume is the authority -- the box gets whatever it
holds, and a gate a later build bakes never arrives. The launcher's wait is
`[ -s "$GATE_FILE" ]`, so an absent or emptied gate is not a failure it can
see: it reads "no gate, nothing to wait for", and the next `docker restart`
recreates the netns with no firewall in it and launches anyway, on a
configuration whose Network row said deny-by-default.

The credential tmpfs is the other sharp case: a durable mount over `/run/byre`
relocates delivered plaintext onto durable storage, and can re-surface AND
re-export it on a bare restart without a fresh unlock.

## The decision

**One line per offending mount/volume target, in the containment register.**
The line says three things: byre cannot re-assert over a runtime mount; the
box therefore gets what is mounted rather than what byre baked; and whatever
byre claims from that path -- the firewall's launch gate, the MCP /
instructions / Claude Skills delivery -- describes byre's construction and not
this box.

It renders on three surfaces from one exported prose function, so they cannot
drift: `byre status` beside the skill-declared containment holes, a 🛑 warning
at develop, and a containment-weight line in the preset-apply grant review.

The review is included because consent is the point: a proposed
`[[volumes]]` on `/etc/byre` read as a storage row would be answered before
the disclosure ever appeared, and grant-shaped content renders with the same
weight whichever table carries it (ADR 0050).

The consequence is worded as the scope byre stops warranting rather than as a
list of things that broke, because the two differ: a bind on `mcp.json` alone
leaves the launch gate untouched, and byre cannot tell which case it is
looking at: runtime mounts are opaque, so byre knows a path is covered and
nothing about what covers it. Naming the claims that read from that path is the whole of what it can
honestly say, and it is said the same way whichever path was hit.

Detection is mount-centric: a target shadows if it overlaps a *managed root*
-- `/etc/byre` and `/run/byre` as wholes, the launcher, the headless chassis,
and each declared netns hook. A target on a root, or above it, buries it. For
the two directory roots, a target INSIDE counts too, because each entry there
is separately replaceable (a bind
straight onto the launch gate). Under a file root nothing counts: a target
below a regular file replaces nothing, there being no directory there to bind
into -- the engine errors rather than starting a box whose launcher is
shadowed. Individual artifacts are deliberately not enumerated, so an artifact
baked under `/etc/byre` later is covered without a second edit.

**Why not per-claim degradation.** Reporting a hit per guarded path, and
hedging the Network row with "a mount/volume over a security path present",
says the wrong thing twice. It implies the damage is enumerable, when the list
of things byre bakes under `/etc/byre` grows; and it hedges the one claim that
has a hedge already while the MCP, instructions and Claude Skills delivery
claims -- equally stale, reading from artifacts under the same directory --
carry none. This uses ADR 0027's warranty model instead: every row keeps
describing what byre built, and one loud line disclaims the hole, including
consequences nobody has enumerated yet.

**Skill mounts are in.** Treating them as "byre's trusted construction (as
with `files`)" would be a false analogy: what makes a skill's `files` entry
safe is not the skill's trustworthiness, it is the build-tail re-assertion,
which runs regardless of who wrote the entry -- and it has no runtime twin. Enabling a skill IS trusting it (P2), and this line does not
refuse or block one: it is a disclosure that names the skill, exactly as the
grant rows already name skill-declared holes. Attribution is per skill, so
`Resolved`'s per-skill data is what is iterated, not the concatenating
`Mounts()`/`Volumes()` helpers, which lose it.

**`files` keeps its granular reporting.** A `files` collision is byte-known at
build time: byre knows which path, which direction the collision resolves, and
whether byre's copy or yours ends up in the image; runtime mounts are opaque.
The fidelity difference in the two reports is that difference, not an inconsistency to be
tidied away.

## Accepted residuals

- **The disclosure is not a gate.** A user may mount over `/etc/byre` or
  `/run/byre` and develop proceeds. That is P1, and the firewall-open-on-restart
  and plaintext-on-durable-storage consequences are the user's to accept; the
  line's job is that they can know.
- **No runtime verification.** byre does not inspect the built or running box
  to confirm the gate is intact -- the disclosure fires on the CONFIGURATION,
  from the same resolved data status renders. A mount arriving via raw
  `run_args` is not seen at all; raw args already degrade the posture claim as
  unauditable text (P3), which is the correct treatment for them.
- **Nothing distinguishes a benign overlap.** A read-only bind of an identical
  launcher, or a volume over `/etc/byre/claude-skills` intended to supply the
  skills, discloses exactly like a hostile one. Judging intent would mean
  introspecting mount contents -- the policing role byre refuses.
- **The apply review's fallback paths see less.** When the cascade or the
  skills cannot be expanded, the review already says so and shows what it
  has; the shadow check there runs against the fixed roots only (no netns
  hook), since a hook resolution never reached names nothing. The full check
  runs on the ordinary path.
- **Skills that legitimately want to place something under `/etc/byre` have no
  quiet path.** None does (every builtin's targets are under
  `/home/dev` or the docker socket). If one arrives, the answer is a build
  contribution, not a runtime mount; a `containment`-style opt-out is not
  offered until something needs it.
