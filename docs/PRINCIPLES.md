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
  `byre.project` identity label, re-asserted so lifecycle and status always
  work.
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

## What byre is not

Boundary statements, kept here so they don't get re-argued feature by
feature. byre is not: an agent (it runs one); a Docker replacement; a
devcontainer implementation; a policy engine; a secret manager (it seeds
non-credential data, never stores or rotates secrets); a cloud sandbox
service (no hosted runtime, no sign-in, no fleet, no telemetry); a
security product with a stronger-than-Docker isolation claim
(it competes on legibility and management, not on the boundary itself).
