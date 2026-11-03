# The shared-auth offer is per box; the saved answer is a favourite

The onboarding shared-auth offer (ADR 0024) is per box: the question is
"Opt this box into <agent> shared credentials?", it is asked at **every
box's onboarding**, and its yes writes exactly one thing -- the companion
skill into **this project's `byre.config`** `skills`. The saved
preference is a **favourite, not a grant**: "Save these as your
default?" stores the answer in the picker-owned `[defaults].shared_auth`
in `default.config`, which only changes what the *next* offer prefills
(`[Y/n]` instead of `[y/N]`) -- exactly how the template/agent
favourites work. The picker never writes `default.config`'s `skills`.
Decided 2026-07-12, superseding ADR 0024's recording mechanics; the
readiness gate (`shared_auth_for`, the author's vouch) and the offer's
placement directly after the agent question are ADR 0024's.

The mechanism is machine-scoped (one identity volume), but the
*decision* at a first-run picker belongs to the box being onboarded.
Recording one box's answer as machine state would stretch a single "y"
into every future box's silent grant, and a single "n" into a
machine-wide never-ask; putting that write behind a save-default
question would still be a default grant, because one box's picker cannot
consent for boxes that don't exist yet. The rule: **no box gains a
capability without its own question**, except under the two explicit
standing instructions below. What CAN be saved is the answer's
*default* -- a preference over future answers, costing each future box
exactly one Enter, granting nothing by itself.

The mechanics:

- **The offer's "yes" is the companion skill in the project's
  `byre.config`** -- the same first-class representation a hand-enabled
  skill uses, written by the same `WriteProjectConfig` that writes
  template/agent, one atomic file creation. A "no" writes nothing,
  anywhere. EOF anywhere in the picker still aborts with nothing
  written.
- **`[defaults].shared_auth` is the fourth favourite.** A picker-owned
  entry in `default.config` naming the agents whose saved answer is yes
  (and the companion picked for each); save-yes adds the chosen agent,
  save-no removes it, and the save-default question fires under the one
  rule all axes share: only when the given answer differs from stored
  state. The resolver strips `[defaults]` whole from resolved configs,
  so it is inert as configuration. The legacy top-level spelling is
  ADR 0049's.
- **Prefill is not auto-grant.** A `[Y/n, i for info]` offer accepts on Enter or
  an explicit y; unrecognized input never lands on the granting side,
  whatever the default.
- **Two suppressions, each an explicit standing instruction.**
  - The companion already in `default.config`'s `skills` -- a hand-made
    (or `byre config --global`) machine-wide grant. The cascade then
    covers every box regardless of any per-box answer, and asking would
    imply an "n" that does nothing. That grant path remains the user's to
    make; the picker never makes it.
  - `defaults.skip_questions` (2026-07-27), which configures a new
    project from the stored answers unasked -- the shared-auth pick
    included, and that pick GRANTS. Unlike the first, it suppresses a
    question whose answer matters, and assumes yes. It stands because an
    explicit standing instruction at machine scope is not what P5 forbids
    (a REMEMBERED answer quietly becoming a default), and it is never
    silent: develop states what it configured and which companion it
    enabled. The failure mode is a login shared one project earlier than
    expected, not a capability the user did not choose.

  **The mitigation is disclosure at the switch**, because both act on
  projects that do not exist yet and both are switches in `byre config
  --global`: the `skip_questions` checkbox names the credential answer
  among what it will stop asking (before it is ticked, not after), and
  enabling a vouched companion on the Skills screen says new projects get
  it and stop being asked. A third suppression, if one is ever added,
  owes the same disclosure in the same place.
- **`shared_auth_declined` is retired** (ADR 0049): under the favourite
  model a "no" needs no record (the default is already No), and a stale
  occurrence parses as a tolerated retired key. A v0.1.7 decliner is
  asked again, per box, default No; a v0.1.7 machine-wide "yes"
  (companion in `skills`) keeps working via the first suppression.

Consequences: every onboarding of an offer-eligible agent asks one more
one-keystroke question -- that is the point, not a cost to optimize
away; the per-box grant is the consent. The fully-flagged zero-prompt
contract is unchanged (no prompts, no offer, no writes beyond
byre.config). The wording deliberately drops the companion skill's
name: which skill implements the mechanism is config plumbing, legible
in the written `byre.config`, not part of the decision. That detail
lives one keystroke away instead: answering `i` prints exactly what
each answer writes -- y's file and scope, n's nothing, the save
question's prefill-only effect -- naming the companion, then re-asks.
Legibility offered, never forced.
