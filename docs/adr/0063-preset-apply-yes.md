# `preset apply --yes`: unattended apply is consent to grants, never to a credential swap

Decided 2026-11-02. `byre preset apply --yes` applies a preset without a
terminal: the full review still prints to stderr, the confirm is skipped,
and the chauffeured installs run with `--yes`. It refuses, before anything
is fetched or written, in three cases: the preset is named by an https
URI, an https `[sources]` hint it would install from carries no digest, or
the preset or the stored config it would replace carries credential rows. Without `--yes`, a pipe still refuses, and
the refusal now names `--yes`.

Principles: P5 (consent to applied content is consent to its RENDERED
grants -- `--yes` keeps the rendering, into the CI log); P1 (every refusal
here names a route the user can take on purpose; none blocks a deliberate
choice); P6 (no new config key; `[sources]` stays the existing read-only
row). Related: ADR 0003 (the sandbox config is read from the host-side store
only), ADR 0029 (installs are digest-verified), ADR 0057 (project
credentials; the preset bridge is answered at the consent gate), ADR 0061
(headless runs, the CI route this completes).

## Why

A CI runner has no terminal, and `preset apply` refused without one ("the
review is the point"). It is the only reviewed bridge from a committed
`byre.preset` into the project's stored config, so a headless job had no
config step except `byre run --template <t> --agent <a>` on a
never-configured checkout -- enough for the minimal case, nowhere near a
preset's skills, egress and `env_from_host`.

## What `--yes` consents to

The consent act moves earlier: a human reviews and commits the preset, and
writes `--yes` into the job that applies it. The invocation then stands in
for the confirm -- for the grants the review renders, and only those. The
review is not skipped; it renders exactly as on a terminal, un-highlighted
off one (the `⚠` prefix carries the weight), so the CI log holds what was
applied. `--yes` on a terminal also skips the confirm: the flag says the
same thing wherever it is typed.

The chauffeured installs inherit the flag, and so inherit what `skill
install --yes` already means: a replacement or an activation proceeds
without a prompt.

## Digest-less https sources refuse

On a terminal, each chauffeured install shows its own grant summary and a
human confirms it. Under `--yes` nobody looks at the bytes an https URL
serves, so the digest the committed preset pins is what vouches for them
(ADR 0029's digest check then holds the fetch to it). An https hint without
one refuses, naming the row, before any fetch. A path source passes: it
names local bytes, in CI typically a file in the same checkout whose
committed preset is the consent. The route around the refusal is the
user's own: `byre skill install <url> --yes` before the apply, which is
the user choosing those bytes by name.

## An https preset URI refuses

The same rule covers the preset itself. `byre preset apply --yes
https://...` would apply whatever the URL serves at that moment, and no
committed digest pins it: the uninspected-bytes case the `[sources]` rule
refuses, one level up. So `--yes` refuses an https preset URI, naming it,
before any fetch. A path and the conventional `byre.preset` pass: they
name bytes the job controls. The remedy is to commit the preset to the
repo (or fetch it in a step you control) and apply the path.

## Credential rows refuse

ADR 0057 answers the preset bridge -- a repo writer can mint, swap,
transplant or replay a credential value through `preset apply` -- at the
consent gate. The review's credential lines name every changed,
appeared or vanished credential row and any change to the file-local
`[credentials]` block, because a swapped ciphertext is invisible in the
diff. The lines do not gate. The human's confirm against them does, and
`--yes` is not that human.

So when the preset OR the stored config it would replace carries an
`encrypted:` or `encrypted-file:` row -- or the credential review has any
line to show, such as an identity arriving, leaving or being replaced, or a
side it could not read -- `--yes` refuses, naming the rows. Either side:
replacing a config that holds rows with one that drops or changes them is
the same unvouched move as bringing new ones. Unchanged rows refuse too:
the rule is about who vouches for the value, and the review's silence over
an unchanged row is not a human having looked at it. It refuses on a
terminal as well; the ruling is about the confirm, not the terminal.

The remedy names both routes: apply on a terminal without `--yes`, or keep
credentials out of the preset and deliver them with `env_from_host` from
the runner's environment (`KEY = "env:KEY"`, fed by the CI secret store).
The second is the natural CI route anyway: the secret already lives in
the CI secret store, and `env_from_host` hands it to the box without a
passphrase to feed in (`--credentials stdin`) or a ciphertext to keep in
the repo.

This is the residual ADR 0057 leaves for the unattended path: a `--yes`
apply is consent to the rendered grants and never to a credential swap. It
is restated on the security-model page.

## Rejected

- **`byre develop --config <file>`**, applying a repo file for one run.
  It lets the project tree define its own sandbox, which the host-side
  store exists to prevent (ADR 0003): the store's config is the one the
  boxed agent cannot rewrite. A preset only reaches the store through the
  reviewed apply.
- **A switch that forces credential rows through `--yes`.** Not built:
  `env_from_host` covers the CI case, and applying on a terminal covers
  the rest. P1's rule that a refusal hands over a switch is about a
  refusal protecting one of byre's own claims; this one protects a consent
  act, and both routes past it are ordinary commands. If a real need
  appears, it is a future ADR that names what stands in for the human's
  confirm.

## Costs

- The CI log is where the review lands, and nobody reads it at apply time.
  What was consented to is the committed preset; a commit nobody reviewed
  applies unread. That is the same trust a CI job already places in the
  commit it builds.
- A chauffeured install that fails under `--yes` (a digest mismatch, an
  unreachable URL) still completes the apply, as on a terminal: the review
  marks the package not installed, and the box fails loudly at launch with
  the reinstall remedy. A CI job sees that failure one step later, at
  `byre run`.

## Tests

`TestPresetApplyYesRefusesCredentialRows` (either side, env and file rows,
on a terminal and off one, nothing written or installed);
`TestPresetApplyYesRefusesUndigestedHTTPSHint`;
`TestPresetApplyYesRefusesHTTPSPresetURI`;
`TestPresetApplyYesOffTTYAppliesAndChauffeurs`;
`TestPresetApplyYesOnTTYSkipsTheConfirm`;
`TestPresetApplyYesRefusesAnIdentityArriving`;
`TestPresetApplyRefusesNonTTY`.
