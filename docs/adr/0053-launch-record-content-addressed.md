# The launch record: what byre told the engine, addressed by its own hash

Decided 2026-07-28. Every container byre creates gets a **launch record** -- a
TOML file in the project store holding the exposure facts byre handed the
engine, named by the sha256 of its own bytes, pointed at by a `byre.launch`
container label. `byre status` reads and VERIFIES it, and while a box is
running that box is the page's subject: the grant rows describe it, and a
`Next launch` section lists only what differs in the current config.

Principles: P4 (a claim byre cannot stand behind is qualified, never silently
asserted); P0 (the screen is the product -- this is a status bug, and a status
bug ranks with an engine bug); P1 (the footgun doctrine -- byre degrades, it
never blocks over its own bookkeeping). Related: ADR 0004 (the per-worktree
engine record, the nearest existing thing to this), ADR 0009 (worktrees share
the project store), ADR 0050 (the claim surface and the reserved BYRE_
namespace), ADR 0052 (the containment disclosure that patched this gap
honestly without closing it).

## The fact

byre modelled the *current configuration* very well and did not model *the
immutable resolved configuration that created a particular running box*.

`Status` resolved the config cascade, the skill set and the host env as they
are NOW and rendered those. Its only engine queries were liveness
(`RunningContainersByLabel`) and identity (`ContainerLabels`, for orphan
detection). It never inspected the running container's mounts, ports, env or
network mode, and nothing queryable survived a launch: the container labels
carried `byre.project` and `byre.workdir` -- identity, not configuration.

So: launch a box with `/home/pete/secrets` mounted, delete the mount from the
config, run `byre status`. The page showed `Container: running` directly
beneath a Host mounts block with no `/secrets` row in it, describing a world
that did not exist yet, next to a box that was still holding the secrets. The
develop banner IS an accurate launch-time record, and it is transient stderr.

An external reviewer's phrasing: *a desired-state renderer wearing the clothes
of an observed-state inspector.*

ADR 0052 is the nearest prior art and it is an honesty patch over this same
gap, not a closure: it discloses that byre cannot vouch for a covered path. The
2026-07-28 reserved-env work made the banner and status agree about the NEXT
launch. Neither spoke for the running box.

## The decision

### Content: what byre TOLD THE ENGINE, not the config that produced it

The record holds the exposure facts as they went out the door: bind mounts,
published ports, named volumes, env KEYS, the network posture and the resolved
egress string, the skill-set reserved `BYRE_` keys, the image tag AND digest,
the base image, `run_args` verbatim, and the skill set with its identities.
Every field is read off the assembled `runner.RunParams` and the resolution
that built them -- captured, not re-derived.

It is explicitly NOT the resolved `config.Config`. That would be a second
serialization of a moving schema, and worse, it would invite re-deriving
exposure from config at read time -- which is the gap. The record is one step
CLOSER to reality than config, never a copy of it.

**Env KEYS, values never.** The exit report's rule, and it holds on every
surface byre has. `run_args` is verbatim: status already prints it verbatim,
and the configuration reference already says that is not the place for a
secret. Hashing the values was considered and rejected -- everything in the
record is non-sensitive by construction, and a hashed bind row cannot tell you
`/secrets` is still mounted, which is the entire point.

**The image digest is the quiet win.** `byre rebuild` moves the tag; only the
digest pins what the running box was BUILT from. It is an engine inspect after
the build; a failure records an empty digest WITH the reason, because an honest
empty beats a plausible hash byre never obtained.

### Storage: a store file named by its own hash, pointed at by a label

`~/.byre/projects/<id>/launches/<sha256>.toml`, written under the setup lock
from the post-lock resolution (the re-resolve that landed the same day), with
the container created carrying `byre.launch=<sha256>`.

Content-addressing gives integrity for free, and the integrity is not
decorative. **Under `--self-edit` the store is mounted rw into the box**, so
that directory is the agent's to write. status therefore VERIFIES: it re-hashes
what it read and compares against the label the container carries, and a
mismatch is DISCLOSED, not trusted. The label value is checked against
`^[0-9a-f]{64}$` before it becomes a path component, because it comes back off
a container.

This sits inside the existing self-edit trust ruling rather than extending it,
but it has to be said out loud: **the record only ever INFORMS a human reading
status. No host action is driven by it.** A forged record can make a status page
lie about a box in a session that already authors its own next sandbox; it
cannot make byre mount, build or run anything.

Worktrees share one project store (ADR 0009). Each worktree box writes its own
record there and they are told apart by their addresses, not by their paths --
the label is the pointer, so nothing needs to be keyed by worktree.

### Lifecycle: written at create, read by status, reaped with the container

The reap is opportunistic and never load-bearing: under the same lock, after a
successful create, byre lists the project's containers, collects the
`byre.launch` labels they carry, and unlinks any record nothing points at. An
engine that will not answer causes the reap to do nothing at all -- an
unanswerable engine is not evidence that a record is stale. A record that
survives costs a few hundred bytes.

The scope is the configured engine, which is sound because develop refuses
outright while a box for this worktree lives on another installed engine (ADR
0004). The residual is an engine that check SKIPPED as unreachable: a box there
can lose its record, and status then degrades honestly for it -- which is the
outcome the whole design is built to make safe.

**A missing record degrades, never guesses.** Six states, each with its own
sentence: no `byre.launch` label at all (a box from an older byre), a label
whose record is gone, bytes that do not hash to their address, a record byre
cannot parse, a schema from a newer byre, and verified. Only the last makes the
box the subject; every other one qualifies the rows and says they describe the
CURRENT CONFIG. The record carries `record = <n>`; decoding is lenient about
unknown FIELDS (two byre versions share one store) and explicit about an
unknown SCHEMA (liveness only) -- the packages index's stance, one step further
on.

### No generation counter

The content hash IS the identity. The race a counter would have served --
develop launching a config the editor changed while develop waited for the lock
-- was closed by re-resolving under the lock. A counter would be a second
identity to keep honest.

## The status split

**The running box is ALWAYS the subject when one exists.** `statusInfo` is
resolved from the current config exactly as before; a verified record then
REPLACES the exposure fields, and the config-derived values are kept for the
diff. Every row rides the funnel it already rode -- no second renderer to
drift. The `Container` row states the subject (*the grant rows above describe
THIS box (launch record <addr>); other rows describe the current config*), and
a `Next launch` section lists the deltas in the row vocabulary of the blocks
they refer to (`- Bind /home/pete/secrets -> /secrets  (ro)`). No delta, no
section: an "unchanged" line is a row nobody needs.

The claim-degradation inputs come off the record too -- the recorded box's own
raw `run_args`, its raw build lines, its reserved `BYRE_` keys -- because a
hedge computed from today's config would describe the wrong box, which is the
failure being fixed. A skill set that has since stopped resolving does not
blank the running box's posture claim; it appears as a next-launch change.

**With no box, nothing is relabelled.** The rows ARE the next launch and
today's semantics stand.

**What the record does not replace**, and why the Container row says so: the
wiring rows (MCP, Claude Skills, standing instructions), the build rows
(template, raw build lines, config `[env]`), the host-env passthrough, and the
skill-declared containment holes. The record is exposure-level by design; those
rows describe configuration and construction rather than what the engine was
told.

`--data` follows the same rule `--full` does: whatever the page's subject is,
the JSON says the same and carries the same qualifiers. It gains `subject`
(`running_box` / `next_launch`), a `launch` object (the record, or the state
and note explaining why there isn't one), and `changes_on_next_launch`.

## The config editor

The exposure headline keeps NEXT-LAUNCH semantics throughout -- it describes
the config being edited, which is the only thing the editor can change. With a
box already running the headline is QUALIFIED (`· box running -- changes apply
at next launch`), which labels those semantics rather than re-scoping them. The
save report gains the clause when true. Both are unsolicited liveness probes
held to status's own discipline: an unreachable engine degrades to the plain
message, silently. The save clause is re-probed rather than reused, because it
is fresh at the moment it is actionable.

Staleness is harmless in the direction that matters: if the box exits
mid-session, "changes apply at next launch" stays TRUE and only the "box
running" half goes stale.

**The develop banner is unchanged.** It already describes the launching box
accurately -- it is the record's SOURCE, not a consumer.

## Consequences

- Every `byre develop` writes one small file and one extra engine inspect.
- Boxes already running when byre is upgraded carry no label, so status
  qualifies their rows until they are restarted. That is the honest reading.
- The record's serialization is a CONTRACT in the strongest sense available:
  the bytes ARE the address, so reordering a field changes every address. It
  is pinned byte-exact.
- `status --data` moves to version 2.
- Env keys are recorded and rendered (`Box env`) but deliberately do NOT
  participate in the delta: the config side has no runtime env key set without
  re-implementing `runParams`' env union, which is the re-derivation this
  record abolishes. The `Env` and `Host env` rows continue to describe
  configuration -- config `[env]` in particular is baked into the image by the
  Dockerfile and never rides `-e`, so it is a build fact the record has no
  business holding.
- `byre dockerrun` prints the run command WITHOUT the `byre.launch` label, as
  it already omits the per-invocation nonce and prints its own client pid. It
  is an ejection aid, not a byte-exact replay; a box started by hand simply
  has no record, and status degrades honestly for it.
- `byre rehome` migrates the config and the applied marker, not the records.
  It refuses while a session runs, so no live box's record is lost -- what
  goes is bookkeeping for containers that are already gone.
- A future consumer arrives with ADR-worthy weight of its own: volume
  `sharing = "exclusive"` enforcement reads sibling boxes' records to learn
  which volumes they actually mounted, rather than guessing from their current
  config.
