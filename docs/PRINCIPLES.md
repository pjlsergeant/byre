# byre principles

Standing commitments -- the constitution decisions must answer to. A
point-in-time decision belongs in `docs/adr/` and should cite the principle
it follows from; if changing an idea would mean re-litigating the project
rather than superseding one decision, it belongs here. Vocabulary for these
concepts is pinned in `docs/GLOSSARY.md`.

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

## 6. The editor is the interface

**`byre config` is how configuration is edited -- for every config
feature, always. The TOML files are byre's storage format, not its user
interface.** No recipe, prompt, error remedy, or doc may require or
expect a user to open a config file in a text editor; a config
vocabulary that can only be reached by hand-editing is not done, the
same way a grant `byre status` can't name is not done (P4). Hand-editing
remains a defended *right* (P1: plain files, no lock-in, the editor and
`vim` write the same file, held to the same validation) -- a right is
not an interface.

Implications:

- A new config key ships with its editor story, in the same unit of
  work: a structured section or screen (plus CLI verbs where scripting
  matters), not "edit the file".
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

## What byre is not

Boundary statements, kept here so they don't get re-argued feature by
feature. byre is not: an agent (it runs one); a Docker replacement; a
devcontainer implementation; a policy engine; a secret manager (it seeds
non-credential data, never stores or rotates secrets); a cloud sandbox
service (no hosted runtime, no sign-in, no fleet, no telemetry); a
security product with a stronger-than-Docker isolation claim
(it competes on legibility and management, not on the boundary itself).
