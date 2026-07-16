# Companion pairing is a fact; the vouch stays `shared_auth_for`

A companion skill's pairing to its agent -- "this skill augments that
agent skill" -- is declared as its own skill.toml key, `companion_for =
"<agent>"`, and the config UI's nesting (the indented child row under
the agent) rides that key. `shared_auth_for` keeps exactly its ADR
0024/0025 meaning -- the author's vouch that the shared-auth mechanism
is ready, the sole trigger for the onboarding offer -- and continues to
imply the pairing, so a vouched companion declares nothing extra.
Declaring both keys with different agents is a load error. Decided
2026-07-16.

Before this, one key carried two meanings. `shared_auth_for` was born
as the vouch (ADR 0024); when the config UI later needed to know which
agent a companion belongs to, it read the only machine-readable pairing
datum there was -- the vouch. The consequence was never chosen: when
gemini-shared-auth and opencode-shared-auth shipped gate-pending
(correctly withholding the vouch), they silently fell out of the
nesting and rendered as unrelated rows in the flat skills list. That
degraded legibility *as if it were* a gate and delivered neither: the
display stopped saying what the skill is, while the ungated mechanism
stayed one uninformative row away, hand-enableable as ever. Principle 4
(legibility is the product) says byre makes things visible instead of
gating them; hiding a true relationship because a warranty is pending
inverts that.

The split gives each meaning its own lifecycle. The pairing is true
from the day a companion exists -- gate-pending, vouched, or anything
between -- so `companion_for` is display-plumbing with no teeth: it
nests the row, shows in `skill show`, and triggers nothing. The vouch
is earned by gates (ADR 0017's verification record; grok failed them
and was retired, ADR 0023), so `shared_auth_for` alone feeds
`SharedAuthClaimants` and the per-box offer. When a pending gate
passes, the author swaps `companion_for` for `shared_auth_for` (or
adds the latter; the former becomes redundant) and the offer switches
on -- the display never changes, because the fact never did.

Rejected: redefining `shared_auth_for` as the pairing plus a separate
readiness flag. That silently changes a shipped key's teeth under
third-party skill authors -- a config that offered yesterday must not
stop offering because the schema moved under it. Adding a fact key
beside the vouch key leaves every existing skill.toml meaning what it
meant.

Retired stubs (grok-shared-auth) declare neither key: a stub makes no
claims, including companionship; it renders only when a config already
references it.
