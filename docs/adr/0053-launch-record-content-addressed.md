# The launch record: what byre told the engine, addressed by its own hash

> **Amended by ADR 0057** (2026-08-07): the record gains credential_unlock
> and per-name decrypt outcomes ("scheduled", never "delivered" — the
> record is immutable and written pre-start). Names and outcomes only;
> values never, the standing keys-not-values rule.

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

**Values are recorded EFFECTIVE, not as the config spells them.** The base is
the instance: an empty `base` key means `gen.DefaultBase`, and a record holding
`""` would mean "whatever `DefaultBase` meant on the byre that wrote this" -- a
value only recoverable by asking a LATER byre what its default is now, which is
precisely the re-derivation this record exists to abolish. Change that default
in a release and every such record silently starts describing an image its box
never ran, with no delta to show for it: `""` on both sides compares equal
while `FROM` really did move. So `imageRecord` resolves it once, where the
record is assembled. The comparison normalizes too, but for a different
population -- that is what keeps a record written BEFORE this rule, holding a
bare `""`, comparable at all. For THOSE records the moved-default delta is
unrecoverable: their `""` reads as today's default on both sides, so the first
`DefaultBase` change after an upgrade shows no `~ Base` line for boxes
launched before the rule. Accepted -- the value was never written down, and
only records written from here on carry it. The `(default: ...)` spelling
survives as display only.

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
status. No host action is driven by it.** An edited record is refused rather
than believed, so the forgeable outcome is not a false page: it is a WITHDRAWN
one. A self-edit agent can delete its own record or make it unreadable, and
status then says which and falls back to describing the current config.

**Residual, disclosed** (`site/content/docs/security-model.md`): a `--self-edit`
box can therefore stop `byre status` describing it. The failure direction is a
claim withdrawn, never a false one, and it costs an agent nothing it did not
already have -- a box that authors its own next sandbox can already change
what the next status page describes by editing the config.

Worktrees share one project store (ADR 0009). Each worktree box writes its own
record there and they are told apart by their addresses, not by their paths --
the label is the pointer, so nothing needs to be keyed by worktree.

### Lifecycle: written at create, read by status, reaped with the container

The reap is opportunistic and never load-bearing: under the same lock, after a
successful create, byre lists the project's containers, collects the
`byre.launch` labels they carry, and unlinks any record nothing points at.

**The live set spans every engine byre can see, not the configured one.** ADR
0004 stops two boxes existing for one WORKTREE across engines; it says nothing
about SIBLINGS, and worktrees of a project share this store (ADR 0009) while
each may legitimately run on a different engine. Reaping from the configured
engine's view alone lets a docker launch in worktree A unlink the record of
worktree B's live podman box -- and B's status then reports a record "missing"
for a box that is running, which is byre lying about the one thing this file
exists to tell the truth about. develop already resolves that peer set for the
ADR 0004 check, so this costs no new host probing.

Every uncertainty ABANDONS the reap rather than narrowing the live set: an
engine that will not answer, and an engine byre found but DECLINES to run
(which may be holding a sibling right now, unqueryably). The two outcomes are
not symmetric -- a record kept too long is litter, a record deleted too early
is a live box byre can no longer describe.

**A missing record degrades, never guesses.** Six states, each with its own
sentence: no `byre.launch` label at all (a box from an older byre), a label
whose record is gone, bytes that do not hash to their address, a record byre
cannot read or parse, a schema from a newer byre, and verified. Only the last
makes the box the subject; every other one qualifies the rows and says they
describe the CURRENT CONFIG.

Only a PROVABLE absence counts as "gone". A non-regular file where the record
should be, an oversize one, an unreadable mode, an I/O error -- those are byre
unable to LOOK, and under `--self-edit` a box can arrange each of them
deliberately, so folding them into "no longer in the store" would hand an agent
a way to make status report a record deleted while it sits there. The record
carries `record = <n>`; decoding is lenient about unknown FIELDS (two byre
versions share one store) and explicit about an unknown SCHEMA (liveness only)
-- the packages index's stance, one step further on.

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

**A delta line must be a real difference.** The section's only value is that
every line in it is true, so a standing false row is worse than no section: it
trains the reader to skip the one place byre says what changed. Four rules fall
out, and each closed a defect a reviewer found:

- both sides run through the SAME normalization the engine got. Host paths are
  tilde-expanded and cleaned (`~/secrets` and `/home/pete/secrets` are one
  mount, not a `-`/`+` pair), and ports through `config.PortEffective`.
- both sides are the ENFORCED egress allowlist, not the declared union. Status
  keeps a closed entry visible on its Egress rows, marked closed-by, which is
  the point of `!host` reaching past the cascade -- diffing that against a
  record reports every closed-but-declared endpoint as arriving next launch,
  forever, on the ordinary claude-minus-statsig config. One function
  (`egressAfterClosures`) now performs the subtraction for both the resolution
  that feeds the record and the view that is compared to it.
- the base is compared by EFFECTIVE value, never by config spelling. `gen`
  substitutes `gen.DefaultBase` for an empty `base`, so writing the default out
  explicitly and leaving it unwritten produce the same `FROM` -- and a `~ Base`
  line across that edit is a standing false row. Empty still RENDERS as the
  default it stands for, so the line names what will actually be built.
- `run_args` compare as SLICES. Joining on a space is not injective, so a
  joined compare calls `{"--label", "x=a b"}` and `{"--label x=a", "b"}` equal
  -- reporting no change across an edit that changes what the engine is handed.
  Rendered shell-quoted, so the line is unambiguous too.

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
