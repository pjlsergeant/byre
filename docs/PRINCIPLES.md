# byre principles

Standing commitments -- the constitution decisions must answer to. A
point-in-time decision belongs in `docs/adr/` and should cite the principle
it follows from; if changing an idea would mean re-litigating the project
rather than superseding one decision, it belongs here. Vocabulary for these
concepts is pinned in `docs/GLOSSARY.md`.

## 0. The TUI is the gold

**Without the TUI, byre is a fusty and dull one-of-many sandbox.** Putting
an agent in a container is table stakes -- several tools do it, and the
Dockerfile byre generates is not what anyone stays for. What byre has that
they do not is the surface over it: a screen that shows effective state
across the cascade, names every grant, and lets you change any of it in
place. That is the differentiator, and it is therefore the product.

Numbered 0 because it is prior to the rest: it says what byre IS, and
several principles below are consequences of it.

Implications:

- **"Expert vocabulary -- hand-edit it" is never an available answer.**
  The rule has one shape: **a config key with no reachable row in the
  editor is a hole in the product.** What that row must be follows from
  who owns the key -- a key the EDITOR owns needs a widget that edits it;
  a key a FLOW owns needs a read-only row naming that owner. No row at
  all is not a third option: outside the one narrow exemption below -- a
  spelling byre no longer writes -- it is the place where byre stops
  being byre and hands you a text editor, exactly the fusty one-of-many
  it would otherwise be. P6 is this principle applied to config
  vocabulary; this is the reason behind it.
- **A flow-owned key shows read-only -- never hidden.** Two keys are in
  that class today: `[sources]`, which `byre preset apply` records as it
  chauffeurs an install, and `defaults.shared_auth`, which the first-run
  question answers with its machine-wide credential consequence stated
  (P5). A widget there would author the value away from the flow that
  gives it meaning, so the row shows the value and names the writer
  instead -- the read-only "Skill files" screen is the shape.
- **The only key that may have NO row is one no byre still writes.** The
  exemption class is exactly the retired and migration-only spellings a
  current byre reads but never authors -- today the pre-2026-07-28
  top-level `shared_auth`, which every save canonicalizes into
  `[defaults]` (ADR 0049's live inventory). Giving those a row would
  offer to edit a key byre is in the middle of removing. **A live key
  never qualifies**, and the exemption is not a place to put one: each
  entry is named in the arm's own map with the retirement it rides, so
  joining that map is a claim a reviewer can check and reject, not a way
  for a hidden key to become doctrinally fine.
- **Editor defects carry the weight of engine defects.** A screen that
  misreports effective state, destroys a key on save, or cannot express an
  off-switch is a product bug of the same class as a wrong Dockerfile --
  not a polish item to be scheduled after the "real" work.
- **Investment in the TUI is not gold-plating.** The TUI test tier (ADR
  0038) and the demo casts exist because this surface has to keep working
  and has to be SHOWN -- it is the thing that sells byre, so the demos are
  product, not decoration. The proportionality rule that governs
  corner-case machinery does not license skimping here.
- **The editor never becomes the only way, either.** Hand-editing stays a
  defended right (P1) and every file stays a plain, diffable TOML the
  user owns; this principle raises the editor to first class, it does not
  demote the file.

## 1. The footgun doctrine

**byre's threat model is the agent, never the user.** A footgun is harm a
user aims at their foot *accidentally* -- not the fact that a user can point
a gun at their foot on purpose. byre guards against the first kind with
legibility, and defends the second as a right: a user may weaken or remove
any protection -- raw `run_args`, raw Dockerfile blocks, disabling a
protective skill, baking sudo into their own image -- and byre runs it
without refusal.

Implications:

- Protections are built **tamper-proof against the boxed agent** and **one
  config edit away from off** for the user.
- When byre can no longer stand behind a claim, `byre status` **degrades
  the claim** -- it never blocks the configuration.
- A "safety" feature that would gate a deliberate user choice rather than
  prevent an accident does not belong in byre.

Precedents: no path nannying (byre runs on `~/.byre` itself); `run_args`
overrides byre's own flags by design; the firewall is disabled by
removing it from `skills`, not by a dedicated flag.

## 2. Core ships no opinions

**Core owns the plumbing everyone reinvents; skills own every opinion --
including the agent itself.** Core provides generic mechanism (the config
cascade, generation, the runner, identity, the chassis) and knows no skill
by name. Anything with a point of view -- which agent, which workflow,
which firewall policy, which endpoints -- lives in a skill you enable.

Implications:

- New capabilities should land as a skill plus, at most, a *generic*
  core mechanism the skill plugs into.
- A skill-specific key in core config is a smell; prefer typed generic
  fields in `skill.toml` or existing generic mechanisms (env, mounts).
- Enabling a skill is trusting it: skill contributions are validated for
  legibility, not as a trust boundary.

Precedents: the agent is a skill (`agent` selects which one launches); the
firewall skill carries all firewall policy while core carries only the
generic `network_posture`/`netns_init`/launch-gate mechanisms plus the
`egress` config key -- vocabulary, not policy: declaring an endpoint is
core's job the same way `ports` is, while deny-by-default stays the
skill's opinion. (ADR 0019 superseded the earlier precedent here, which
kept the allowlist in a generic env var; the env vehicle gave the
list override-instead-of-union semantics and hid a grant.)

## 3. Raw Docker is first-class

**byre is a transparent templating layer over Docker, not a replacement for
it.** It generates a Dockerfile you can read, and writing raw Docker is an
expected path, not an escape from the system. Nice primitives cover the
convenient 90%; symmetric raw blocks (`dockerfile_pre`/`dockerfile_post` at
build, `run_args` at runtime) cover the rest. Beyond the raw blocks,
ejection is raw Docker itself -- byre either generates the build or isn't
involved (ADR 0014).

Implications:

- byre never parses inside a raw block -- it shows raw blocks verbatim,
  flagged as not-introspected, and degrades any posture claim they could
  undermine (per the footgun doctrine).
- `run_args` is last-wins over byre's own flags; the sole exception is the
  `byre.project`/`byre.workdir` identity label pair, re-asserted so
  lifecycle and status always work.
- byre stays small *because* the raw tier exists; a primitive has to earn
  its place by covering a common case well.

## 4. Legibility is the product

**byre makes grants legible; it does not gate them.** The whole pitch is
answering "what can this thing actually touch?" truthfully -- so honesty
rules outrank features. `byre status` names every grant (including
skill-granted holes), shows raw blocks verbatim, and qualifies any claim it
can't fully stand behind.

Implications:

- A claim byre cannot verify is degraded, never silently asserted -- and
  never enforced by refusal (footgun doctrine).
- New grant surfaces ship with their status/legibility story, not before:
  if `byre status` can't name it, it isn't done.
- **Reported strings are data, never terminal control.** A value byre did
  not author -- a config target, a skill manifest field, a filename an
  agent left in the tree -- reaches the terminal escaped, or a report can
  be made to render as something other than what byre wrote, including
  overwriting lines already printed. This binds the surfaces whose job is
  reporting: `byre status` and its develop-time warnings, the exit
  report, the `--self-edit` review diff, `byre skill inspect`, deliver's
  transport notes. The editor's input widgets are deliberately outside
  it -- a prefilled textinput or textarea shows the stored bytes raw,
  which is what an editing surface is for.
- byre is not a policy engine; "grant", not "permission" (see the
  glossary).

## 5. Consent is scoped to the box

**No box gains a capability without its own question.** A grant's consent
lives at the scope of its effect: a per-project capability is answered per
project (a config entry in that project's byre.config, a question at that
box's onboarding or preset apply); machine-wide grants are hand-made only
(default.config, `byre config --global`) and never manufactured from one
project's answer. The 2026-07-12 shared-auth episode (ADR 0024 -> 0025)
is the type specimen: one box's "y" became every future box's silent
default -- twice, the second time behind an extra question, because a
question in front of a default grant is still a default grant.

Implications:

- Preferences and grants are different kinds: picker-owned preference
  keys (favourites, `shared_auth`) are cascade-inert and only change what
  the next question pre-selects; keys with teeth (`skills`, `egress`,
  `mounts`) are never written by a picker on a scope the user didn't
  answer for.
- Wording equals write: every consent prompt states the actual scope and
  effect of the write its answer triggers, and Enter-through a default
  never grants beyond the current box.
- The mechanism's scope is not the decision's scope: a machine-scoped
  volume does not make opting in machine-scoped.
- Consent to applied content is consent to its RENDERED grants -- which
  holds only while the review's rendering is complete: anything
  grant-shaped renders with the same weight whichever table carries it
  (ADR 0050). A capability the apply review can't name isn't consented,
  the same way a grant status can't name isn't done (P4).

## 6. The editor is the interface

**`byre config` is how configuration is edited -- for every config
feature, always: every LIVE key is either editable there or shown
read-only with its owning flow named (P0). The TOML files are byre's storage
format, not its user interface.** (P0 is why: the TUI is what byre has
that a plain sandbox does not.) No recipe, prompt, error remedy, or doc
may require or expect a user to open a config file in a text editor; a
config vocabulary that can only be reached by hand-editing is not done,
the same way a grant `byre status` can't name is not done (P4).
Hand-editing remains a defended *right* (P1: plain files, no lock-in,
the editor and `vim` write the same file, held to the same validation)
-- a right is not an interface.

**Scope: P6 governs parseable config.** A file hand-edited into a state
byre's parser refuses is outside it: `byre config` names the file, plus
the line, column and key wherever the decoder can identify them, then
stops, rather than reconciling against a document it cannot read -- a
save would have to guess what to preserve, and the guess would land on
the rest of the user's file. Wherever it cannot -- a value decoded from
a rewritten fragment has no coordinates in the user's document -- it
names what it can and claims no position, because a wrong location is
worse than none. There is no editor to be inside at that point: the refusal
happens before the screen opens, so the refusal itself is the only
surface left to carry the remedy, and what P6 asks of it is that it be
precise enough to fix the file by hand. That is a standard the message
has to MEET, not a licence to stop at "this file is broken": a byre
error may not name a byre command as the remedy here, because every one
of them parses the file before it opens.

That boundary is honest only because byre cannot MOVE a file there:
every structured save of a stored config file rides the one document
engine, which re-decodes its own output and restores the previous bytes
rather than turn a loadable file into one a later load would refuse
(ADR 0044). byre's whole-file writes -- a reviewed preset applied, a new
layer stub, a store copied to a new id -- write content byre already
holds, never a splice into a file it has not read. `ctrl+e` is byre's
own door out to `$EDITOR` mid-session, and that case IS inside the
editor: the form is still open, so the reload reports the parse failure
and points back at `ctrl+e` without discarding what is on screen.

Implications:

- A new config key ships with its editor story, in the same unit of
  work: a structured section or screen (plus CLI verbs where scripting
  matters), not "edit the file" -- and where the writer is a flow
  rather than the editor, that story is a read-only row naming the
  owner (P0), not an absence.
- User docs speak editor-first. Raw TOML appears in the configuration
  *reference* (the format spec, needed for presets, layer sharing, and
  review) -- never as the instruction in a how-do-i recipe.
- Every surface that edits a stored config file rides byre's one
  style-preserving document engine and the same validation (ADR 0044);
  the editor round-trips untouched any key it does not yet structure,
  so partial coverage never destroys hand-written or flow-written
  config.
- Where prose or free text is the value, the editor hands off to
  `$EDITOR` (suspend, edit, reload) rather than growing a worse text
  editor inside the form.

Precedents: `[[mcp]]` ships the full editor screen plus `byre mcp
add|remove|list`; the `[[context]]` episode (2026-07-25) is the type
specimen -- the key shipped with "hand-editing is the interface" as its
documented v1 surface, the author's ruling reversed it within a day, and
this principle exists so the next key never repeats it.

## 7. Dependencies don't make design decisions

**byre's contracts are designed; a dependency is a component behind a
contract byre owns.** The file a user diffs, the exit code a script
checks, the Dockerfile a human reads, the merge semantics a cascade
promises -- product surfaces, all of them. What a library happens to
implement is never, by itself, a reason for a product surface to be
worse. When a dependency can't meet a contract byre wants, there are
exactly three honest moves: **own the seam** (build the missing piece
around it), **replace the dependency**, or **accept the limitation on
the record** -- an ADR that names the cost, which may equally conclude
byre didn't need the contract it first wanted. Never available: letting
the limitation masquerade as a design preference.

Implications:

- Distinguish the format from the binding. Choosing TOML, Docker, or
  the terminal is a product decision whose constraints are legitimate
  design inputs; what one library's encoder happens to emit is an
  accident. "The library can't" is an unfinished argument -- it ends
  at owned-around, replaced, or accepted-and-recorded, or it isn't
  over.
- Artifacts byre emits for humans are designed surfaces whose
  *contract* -- semantics and readability -- byre pins; byte-stability
  is promised only where it is deliberately part of that contract
  (gen's golden-pinned Dockerfile is the model). An emitted file whose
  shape is whatever the serializer produced is an unowned surface.
- Compensating apparatus is the tell: a warning, a sentinel, or a
  docs caveat built to apologize for a dependency's gap marks a
  decision that was never actually made. Each one either graduates to
  an accepted-on-the-record limitation or becomes work.
- Rewrites are not an argument. byre is coded by agents: a rewrite
  that would justify expediency elsewhere is a reviewable unit here,
  provided a human signs off and the tests hold the contract. byre
  strives for excellence, not expediency; the *size* of a rewrite is
  a scheduling fact, not a design input -- its migration risk and
  ongoing maintenance burden remain real design inputs, argued on
  their own terms.

Precedents: the exit-code contract (usage errors = 2) is byre's
promise, preserved deliberately around cobra; gen owns the Dockerfile's
shape down to the byte. Type specimen of the failure mode: the
2026-07-25 sidecar near-miss -- a storage architecture for `[[context]]`
prose was nearly chosen because the TOML binding's encoder lacks
multiline string emission, the binding's accident almost dictating
byre's file layout before design review caught it. The standing
instances found by the same audit (comment destruction behind a warning,
a `seed_prefs` bool that couldn't be un-set) were dispositioned
same-day rather than grandfathered -- ADR 0044 and ADR 0045.

## Accretion guardrails

Standing rules against configuration-surface sprawl, promoted from the
2026-07-18 complexity review after surviving contact (ADR 0048 -- kept
unnumbered so `PRINCIPLES.md §n` cites stay unambiguous):

- No new top-level config class until it shares the named-declaration
  rails.
- No new removal/absence spelling; new absence semantics reuse an
  existing one.
- No new compatibility path without a stated removal release (windows
  and inventory: ADR 0049).
- A new typed skill field needs a stated justification: a second
  consumer, a security reason raw fields can't carry, or a legibility
  reason status must understand it.

## What byre is not

Boundary statements, kept here so they don't get re-argued feature by
feature. byre is not: an agent (it runs one); a Docker replacement; a
devcontainer implementation; a policy engine; a secret manager (it seeds
non-credential data, never stores or rotates secrets); a cloud sandbox
service (no hosted runtime, no sign-in, no fleet, no telemetry); a
security product with a stronger-than-Docker isolation claim
(it competes on legibility and management, not on the boundary itself).
