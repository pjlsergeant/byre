# The shared-auth offer is per box; the saved answer is a favourite

The onboarding shared-auth offer (ADR 0024) is rescoped twice over: the
question is "Opt this box into <agent> shared credentials?", it is asked
at **every box's onboarding**, and its yes writes exactly one thing —
the companion skill into **this project's `byre.config`** `skills`. The
saved preference is a **favourite, not a grant**: "Save these as your
default?" stores the answer in the picker-owned `shared_auth` list in
`default.config`, which only changes what the *next* offer prefills
(`[Y/n]` instead of `[y/N]`) — exactly how the template/agent
favourites work. The picker never writes `default.config`'s `skills`.
Decided 2026-07-12, superseding ADR 0024's recording mechanics; the
readiness gate (`shared_auth_for`, the author's vouch) and the offer's
placement directly after the agent question are unchanged.

ADR 0024 (and this ADR's own first draft) conflated two different
scopes. The *mechanism* is machine-scoped (one identity volume), so the
recording went machine-wide — but the *decision* at a first-run picker
belongs to the box being onboarded. Recording one box's answer as
machine state stretched a single "y" into every future box's silent
grant, and a single "n" into a machine-wide never-ask. The second draft
moved that write behind the save-default question and called it
consent — but one box's picker cannot consent for boxes that don't
exist yet; a question in front of a default grant is still a default
grant. The doctrine check that kills both drafts: **no box gains a
capability without its own question.** What CAN be saved is the
answer's *default* — a preference over future answers, costing each
future box exactly one Enter, granting nothing by itself.

The mechanics:

- **The offer's "yes" is the companion skill in the project's
  `byre.config`** — the same first-class representation a hand-enabled
  skill uses, written by the same `WriteProjectConfig` that writes
  template/agent, one atomic file creation. A "no" writes nothing,
  anywhere. EOF anywhere in the picker still aborts with nothing
  written.
- **`shared_auth` is the fourth favourite.** A picker-owned top-level
  list in `default.config` naming the agents whose saved answer is yes;
  save-yes adds the chosen agent, save-no removes it, and the
  save-default question fires under the one rule all axes share: only
  when the given answer differs from stored state. Like every
  picker-owned key, the resolver strips it from resolved configs — it
  is inert as configuration. The write is surgical (one line, re-parse
  verified, refused with a do-it-by-hand error on shapes it can't
  follow or a file it can't parse).
- **Prefill is not auto-grant.** A `[Y/n, i for info]` offer accepts on Enter or
  an explicit y; unrecognized input never lands on the granting side,
  whatever the default.
- **One suppression only**: the companion already in `default.config`'s
  `skills` — a hand-made (or `byre config --global`) machine-wide
  grant. Then the cascade covers every box regardless of any per-box
  answer, and asking would imply an "n" that does nothing. That grant
  path remains legitimate and remains the user's to make by hand; the
  picker never makes it.
- **`shared_auth_declined` is vestigial.** v0.1.7 wrote it; under the
  favourite model a "no" needs no record (the default is already No).
  The key stays decodable (strict parsing; v0.1.7 configs must still
  parse) and resolver-stripped; a v0.1.7 decliner is simply asked
  again, per box, default No. A v0.1.7 machine-wide "yes" (companion in
  `skills`) keeps working via the suppression above.

Consequences: every onboarding of an offer-eligible agent asks one more
one-keystroke question — that is the point, not a cost to optimize
away; the per-box grant is the consent. The fully-flagged zero-prompt
contract is unchanged (no prompts, no offer, no writes beyond
byre.config). The wording deliberately drops the companion skill's
name: which skill implements the mechanism is config plumbing, legible
in the written `byre.config`, not part of the decision. That detail
lives one keystroke away instead: answering `i` prints exactly what
each answer writes — y's file and scope, n's nothing, the save
question's prefill-only effect — naming the companion, then re-asks.
Legibility offered, never forced.
