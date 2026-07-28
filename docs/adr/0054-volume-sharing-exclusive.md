# A volume declares how many boxes may hold it: `sharing = "shared" | "exclusive"`

Decided 2026-07-29. `[[volumes]]` gains a `sharing` key. The default,
`shared`, is what every byre volume has always been. `exclusive` declares
single-writer data, and `byre develop` enforces it: it reads the launch
records (ADR 0053) of this project's live boxes and refuses -- exit 3, the
session-already-live family -- rather than mount a volume one of them is
holding, or proceed where it cannot establish that none is.

Principles: P1 (the footgun doctrine -- the threat model is the agent, and the
user may always overrule the declaration by editing one key); P4 (a claim byre
cannot stand behind is qualified, never silently asserted -- here byre either
establishes the contract or refuses, and never mounts while claiming a
guarantee it did not check); P0 (the new key ships with its editor row).
Related: ADR 0009 (worktrees share the project's volumes, and see the
amendment below), ADR 0053 (the launch record this reads), ADR 0004 (the
cross-engine refusal this is modelled on), ADR 0017 (machine scope).

## The fact

Volume names are project-scoped by construction (`byre-<id>-<name>`,
naming.go), and worktrees of one project are DESIGNED to run concurrently
(ADR 0009) -- so concurrent worktree boxes mount the identical volume set.
ARCHITECTURE presents that as the feature, and for the case it was designed
around it is.

ADR 0009 justified the safety of that arrangement in one sentence, rejecting a
creds/history split as "unnecessary: agents already handle concurrent access to
one state dir (same as two CLI processes sharing `~/.claude` on a host)". That
argument is sound, and it is about AGENT STATE DIRECTORIES. It was written when
volumes were, in practice, the agent's own.

Volumes are now generic grammar: skills contribute arbitrary `[[volumes]]`,
config declares them, and (since 2026-07-28) the config editor writes them. The
`Volume` struct carried Name / Role / Target / Seed / Scope, and `Scope` only
ever WIDENS sharing (project -> machine). Nothing let an author say "this one
must not have two writers". An external review named it: *"enabling a skill
means trusting it" does not imply its SQLite database tolerates two concurrent
writers without corrupting.*

## The decision

### The vocabulary is a word, not a bool

`sharing = "shared" | "exclusive"`, default `shared`. A word because the answers
are not a switch: a per-worktree copy is a third answer to the same question,
and `exclusive = true` would leave it nowhere to land. That third value is NOT
being built now.

`shared` as the default is not inertia -- it is what every existing skill and
config means today, and ADR 0009's reasoning does hold for the case it was
written about.

### `exclusive` is refused on a machine-scoped volume

byre honors the contract by scanning the live boxes of THIS project (the label
it owns). A machine-scoped volume is mounted identically by every project of
the user, so the boxes that could break the contract are exactly the ones the
scan cannot see. Rather than enforce halfway and sell a guarantee it cannot
keep, byre refuses the combination at validation, where the author can see it.

### Enforcement reads sibling LAUNCH RECORDS, not sibling config

The check asks each live box of the project what it actually mounted, which is
the launch record's whole purpose. Re-resolving today's config to guess at what
a box started an hour ago is exactly the error ADR 0053 exists to end -- and
here it would be worse than a wrong status row, because the answer decides
whether byre mounts.

The conflict key is the ENGINE volume name, matched against the names this
session would mount. The sibling's own opinion about sharing is not consulted:
exclusivity is a property of the DATA, and a sibling launched under an older
config still has the file open. The record's `sharing` field therefore serves
the STATUS surface (a running box's volume rows and the Next-launch delta),
not the conflict test.

The reverse case -- a sibling declared exclusive, the current config does not --
is deliberately NOT guarded. Worktrees of a project resolve the same config, so
that case means the user removed the declaration between launches. Honoring a
declaration the user has deleted would be byre overruling the current config
with a stale one, which is the opposite of the footgun doctrine.

### It BLOCKS, and every uncertainty blocks with it

Byre's degrade-never-block rule governs byre's claims about ITSELF: a
bookkeeping failure must never cost a session. This is not that. It is byre
honoring a contract the volume's author declared, against data loss that no
later disclosure undoes -- the same family as the one-session-per-workdir
refusal and ADR 0004's cross-engine refusal, both of which already block.

The dispositions, all refusals, each naming which one fired:

| Situation | Disposition |
|---|---|
| A live sibling's record lists the volume | Refuse, naming the volume, the worktree, and `<engine> stop <id>` |
| A sibling has NO launch record (an older byre) | Refuse |
| Its record is missing, tampered, unreadable, or from a newer byre | Refuse |
| A sibling's labels cannot be read | Refuse |
| An engine cannot be listed (unreachable, error) | Refuse, naming the engine |
| An engine byre found and will NOT run (ADR 0047) | Refuse, naming the engine and why |
| A sibling's record lists other volumes only | Proceed |

The asymmetry is the argument: a wrong refusal costs one command (stop the
sibling, start the engine, or delete one config key), and a wrong launch costs
the volume's contents, irreversibly. "byre cannot prove the sibling is not
holding it" is not "the sibling is not holding it".

The no-record arm is not the weakest of these: an older-byre box leaves nothing
that says what it mounted, and volume names are DERIVED from the project rather
than chosen, so nothing about that box's own config would have kept it off this
volume. What byre cannot claim is that it DID mount it -- the exclusive
declaration may postdate that box's launch. Cannot-rule-out is the claim, and
it is the one the refusal text makes. The unreadable arm is also the
adversarial one -- under `--self-edit` the box owns the store, and if
"unreadable" resolved to "proceed", an agent could delete its own record to buy
a second writer for its neighbour.

The whole check is gated on this session declaring at least one exclusive
volume. Nothing bundled does, so the common launch pays no probe and gains no
new way to fail; a config that opts in is asking byre for the guarantee.

### Compatibility: the record's new field, and old byres

The record's `[[volumes]]` entries carry `sharing`, always written. A record
LACKING it was written by a byre without the vocabulary -- and reading that
back as `shared` is not a lenient guess. Config files and skill manifests both
decode STRICTLY (unknown keys are refused with "a typo, or a config written by
a newer byre"), so a byre that did not know the key refused outright any
declaration carrying it. No box of that vintage can have mounted a volume its
own config called exclusive.

That leaves the honest gap, and the enforcement covers it: a user may add
`sharing = "exclusive"` while an older-byre box is already running. IF that
box mounted this volume, it could only have done so under shared semantics --
and whether it did is knowable only from its record. The conflict test keys
on the mounted NAME, so a recorded mount is caught -- and where the box
predates records entirely, the no-record arm refuses on cannot-rule-out.

## Surfaces

- **Editor** (P0): the volumes item editor gains a **Sharing** picker
  (`shared` / `exclusive`), the last control, with a note that follows the
  selection and says what `exclusive` costs. It is the item editor's first
  SECOND picker -- an entry with two independent closed vocabularies (role,
  sharing) needs two controls, and folding them into one would offer
  combinations the grammar does not have. The volumes editor goes from three
  focusable controls to four; no other form changes. Only the non-default
  answer is written, so `sharing = "shared"` never appears in a file. The
  block renderer emits the key: a field the model carries and the renderer
  forgets is invisible everywhere except the file, which is where the contract
  lives -- a guard now holds every `[[block]]` vocabulary to that.
- **List rows**: `name -> target (role) [exclusive — one live box at a time]`,
  beside the existing machine-scope and seed flags.
- **Status**: the volume rows mark an exclusive volume by name -- from the
  RECORD when a box is running, since that box is the page's subject and this
  is what the record's `sharing` field is for. The Worktrees row stops saying
  siblings "share these volumes" unqualified, but its qualifier reads the
  CURRENT CONFIG even then: it is a claim about what the next develop will do,
  and the current config has the last word on enforcement. The two can
  disagree, and must -- a declaration deleted since the launch would otherwise
  have status promising a refusal that will not happen.
- **`--data`**: each volume carries `sharing`, always spelled out like
  `scope`, so the two tiers cannot describe different boxes.
- **Docs**: the configuration reference, ARCHITECTURE (Mounts & volumes, and
  the worktree paragraph), GLOSSARY (Volume sharing), SKILLS.md (authoring).

## Accepted residuals

- **A stopped box someone starts by hand during a launch.** The scan covers
  RUNNING boxes, because a stopped container holds nothing open. Starting one
  by hand between the check and the mount is outside byre's setup lock and
  outside what byre can serialize. Recorded, not chased.
- **Machine-scoped volumes get no single-writer vocabulary at all.** Refusing
  the combination is honest, but it means an author whose cross-project data
  needs one writer has no answer from byre. The scan that would back it spans
  every project's store and is not built.
- **The contract is byre's, not the kernel's.** `exclusive` stops byre from
  mounting a second time; it does not stop `docker run -v` from doing so, and
  it makes no claim about anything already inside a box.

## Amendment to ADR 0009 (2026-07-29)

ADR 0009's rejection of a creds/history split reads "unnecessary: agents
already handle concurrent access to one state dir". That sentence is scoped to
the AGENT STATE volume it was written about, and it still holds there. It was
never a warranty that every volume a project might one day declare tolerates
concurrent holders -- and by the time skills, config and the editor could all
declare arbitrary volumes, it was being read as one. The general question is
this ADR's; ADR 0009 carries the amendment note in place.
