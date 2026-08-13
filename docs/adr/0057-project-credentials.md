# Project credentials: encrypted values inline in the config files

Handing an agent an API key used to mean `env_from_host` (the host
environment must already carry it) or a plaintext `[env]` literal (baked into
the image; the reference explicitly warns against it). Neither stores a secret
for the project. This ADR lands the values **inline**: an `encrypted:` or
`encrypted-file:` row in `env_from_host` carries an age ciphertext, a
file-local `[credentials]` table holds the identity that opens the rows in
THAT file (scrypt-wrapped under a passphrase) beside the cleartext recipient
new values encrypt to, and a passphrase per contributing file unlocks the
launch. Decided 2026-08-13.

```toml
[env_from_host]
STRIPE_KEY = "encrypted:<age blob, base64>"        # arrives as an env var
TLS_CERT   = "encrypted-file:<age blob, base64>"   # arrives as a tmpfs file, path in an env var

[credentials]
identity  = "<scrypt-passphrase-wrapped age X25519 identity, base64>"
recipient = "age1..."   # the cleartext public half, so `set` never prompts
```

Principles: P1 (the footgun doctrine — the threat model is the agent, and the
config files are the user's own; byre discloses rather than gates), P2
(legibility: the answer to the one real residual here is a louder consent
gate, not a storage architecture), P4 (a claim byre cannot stand behind is
qualified), P0 (the editor authors these; "expert vocabulary, hand-edit it" is
not an answer byre gives). Related: ADR 0026 (env_from_host — the scheme set
these two members join), ADR 0031 (the scheme grammar), ADR 0035 (a layer's
values reach every project extending it, live), ADR 0011 (the fail-closed gate
whose shape the launcher wait now takes), ADR 0052 (the tmpfs is a managed
path), ADR 0053 (the record carries outcomes, never values).

## Provenance

This ADR originally ratified a vault-directory design: named values under
`~/.byre/projects/<id>/credentials/`, `[[credentials]]` declarations naming
what may be delivered and where, and a join between the two. The grilling of
2026-08-13 overturned it. Config can already hold an opaque 256-character
plaintext API key, so value legibility was never an invariant and "ciphertext
doesn't belong in config" defended nothing. An encrypted row is a plaintext
env row plus at-rest protection, minus nothing that matters: blobs change only
when a human re-sets the value (rekey does not rewrite them), so a plain byte
compare gives drift the same semantics as any other value. Every argument for
the separate directory reduced to cosmetics except one real asymmetry (the
preset bridge, below), which byre answers the way it answers everything —
legibility at the consent gate, not storage architecture. What the directory
cost was a join key, two divergence states, an involved-set computation,
credential-specific resolution with its own orphan-shadowing hazard
(structurally impossible here), an index, a lifecycle CLI, and an entire
layer-vault design docket. The vault mechanics are not restated below; git
history holds them.

## The threat model IS the decision

The feature's security content is exactly three things, and the list of what
it does NOT defend is as binding as the list of what it does:

- **Confidentiality at rest against off-box disk access** (stolen laptop,
  backup blob, synced dotfiles): each value is an age ciphertext, X25519 to
  the recipient in its own file's `[credentials]` block, whose identity is
  scrypt-wrapped under the passphrase (pinned work factor; `SetMaxWorkFactor`
  bounds the unwrap as a LIVENESS measure, not a defense). The passphrase is
  the protection — a weak one weakens the claim, and that is the user's
  residual. The recipient being CLEARTEXT is what makes passphrase-free writes
  possible: `set` encrypts to it without prompting, and rekey rewraps the
  identity alone, leaving every value blob byte-identical.
- **Explicit per-launch consent**: the row IS the standing, cascade-visible
  consent to the value (there is no second declaration to keep in step with
  it), and the passphrase entry is both authentication and the per-launch
  consent act. A value never arrives as a command-line argument (argv is shell
  history and the process list).
- **byre's own plaintext hygiene**: byre writes plaintext filesystem artifacts
  in exactly ONE place — the session tmpfs at `/run/byre` (`--tmpfs`: no image
  layer, no writable layer, empties when the box stops). No plaintext host
  file, image layer, engine-visible config value, or volume content. The rows
  ride env_from_host and NOT `[env]` for exactly this reason among others: an
  `[env]` row bakes into the image through the Dockerfile, which would put
  ciphertext in every layer and force a rebuild on every re-set. Encrypted
  rows are excluded from the ordinary env_from_host `-e` export and travel
  only on the delivery channel. Transient process and pipe memory is disclosed
  separately (swap/core/hibernation residency, security-model page).

Explicitly NOT defended:

- **The agent reading or re-persisting its credentials.** It is HANDED them;
  that is the contract. The maintainer's razor: friction against a live
  privileged reader is theatre; a copy that outlives the unlock window is a
  real hole — and the tmpfs dying with the box is what answers it. Verifying
  the in-box execution environment defends plaintext against the party it was
  given to, and is not built. Do not restore it.
- **The user's own mounts and store edits** (footgun doctrine, P1). The tmpfs
  is an ADR 0052 managed path: a mount over `/run/byre` relocates delivered
  plaintext onto durable storage — and a durable shadow can re-surface AND
  re-export the bytes on a bare `docker restart` without a fresh unlock.
  Disclosed by the shadow machinery, never gated.
- **Integrity of the config files themselves.** They are shared-custody: the
  user's editor, byre's own writers, and — for a repo-shipped preset — an
  agent-writable tree all reach them. The payload's key and kind stamps are
  ACCIDENT guards, never integrity mechanisms (below). Whoever can write a
  file can change what a row delivers; the preset bridge is where that
  actually bites, and it is answered at the consent gate.

## Payload binding is an accident guard, not integrity

The encrypted payload is stamped with a format version plus the config key and
kind it was set for. A mismatch after decryption fails loudly naming both
keys (`row-mismatch`). It catches a blob swapped between rows, a value
replayed from git history onto a renamed key, a transplant across files, and
honest copy-paste. It is NOT integrity: anyone holding the cleartext recipient
can mint a correctly-bound blob, and that is stated rather than fought — the
same stance the vault design took toward its own store, arrived at again.

## The launch flow

1. Resolve the cascade. Collect the winning `encrypted:`/`encrypted-file:`
   rows and group them by the physical file that contributed each (provenance
   the effective view already tracks). An `[env]` literal beating a credential
   row takes the key out of env_from_host entirely (ADR 0026), so no
   passphrase is spent on it — one rule, `DeliversCredential`, spelled once and
   asked by every surface that counts credentials.
2. Print the plan line before asking for anything —
   `unlocking credentials: default (2), layer acme (1), project (3)` —
   so a user knows how many passphrases they are about to be asked for. Then
   prompt per contributing file, root-most first. Each entered passphrase is
   first tried against every still-locked identity, because people reuse them;
   only the files still locked are prompted for.
3. **Credentials are BLOCKING.** A declared credential is delivered or the
   launch stops: wrong passphrase (three attempts), unparseable file, broken
   identity, undecryptable blob, a row that appeared while develop waited for
   the setup lock — each stops the launch naming the file, the row, and the
   remedy. `--credentials=ask|skip|stdin`: `ask` is the default; `skip` is the
   one deliberate way to launch without them; `stdin` reads passphrase lines,
   each tried against every still-locked identity in order and each counting
   as an attempt against the root-most file still locked, under that same
   three-attempt bound. What the unlock consented to bounds what the decrypt
   delivers: never a row beyond the set the plan line counted — a row removed
   under the lock simply drops out and less is delivered, since the consent
   is an upper bound, not a promise of presence.
4. Delivery: decrypt under the setup lock, stream framed base64 over container
   stdin to the baked receiver, land on the per-session tmpfs. Never `docker
   -e`, never an image layer, volume, or writable layer. The export map rides
   the stream as a non-secret MANIFEST written before the `.done` sentinel,
   which is written LAST, so the launcher never observes a half-written tree.
   The receiver is handed KEYS, which are already env-grammar — it restates no
   name grammar of its own.
5. **The launcher wait is bounded FAIL-CLOSED** — ADR 0011's shape, not its
   opposite. No sentinel, a manifest line the launcher cannot honor, or a bare
   restart with credentials scheduled, and the agent never runs. The host
   Stops the container on an inject failure and the launch fails. The export
   step follows the env.d loop (ADR 0028), so credential exports win env
   collisions; env-kind exports are byte-exact (`read -rd ''`, never command
   substitution).
6. **Honesty by measurement**: the inject's stderr outcome says plain
   "delivered" only when it provably landed inside the launcher's wait (the
   epoch is captured before the goroutine spawns, so the measurement
   overestimates); past that it hedges. The launch record carries the unlock
   outcome and per-key decrypt outcomes ("scheduled", deliberately not
   "delivered": the record is immutable and written pre-start). There is NO
   live-state surface anywhere: byre does not probe the box. Status shows the
   launch-time outcome and claims nothing live.

## Resolution and scope

Resolution is the ORDINARY cascade merge — the winning row IS the value, and
`KEY = ""` in a nearer file is the idiomatic disable. There is no
credential-specific resolution, no nearest-vault-wins, and no orphan-shadowing
state to reason about.

The `[credentials]` block is **file-local**: it belongs to the physical file
and never cascade-merges, so a project block can never decrypt a layer's rows.
That is why `Parse` deliberately drops it (a block that reached the merged
`Config` could be merged) and why reading one takes `ParseCredentialsBlock`
over a single file's raw bytes.

## Write discipline

- `set`, `unset`, rekey and identity creation are a **compare-and-swap on the
  complete physical file** under the lock that guards THAT file — the project's
  setup lock for a project config, the layer's own lock for a layer (which
  `byre layer new` and the `--layer` editor take too). The FILE owns the lock;
  every cooperating writer takes it. The race this closes: `set` reads
  recipient R, a concurrent identity replacement lands R2, and the R-encrypted
  blob beside R2 is permanently undecryptable though both writes "succeeded".
  The identity race is refused in BOTH directions and named as what happened.
- The expensive work happens BEFORE the lock is taken: the scrypt unwrap runs
  at the prompt, because holding the setup lock for an authentication cost
  would stall sibling worktrees. Only the cheap per-row decrypts run under it.
- Setting an inherited credential **names the physical write target before the
  value is accepted** — "writes to layer acme (…), used by 3 projects — this
  changes the value for every project extending it". Layer changes propagate
  live (ADR 0035), so the cross-project effect must be unmistakable. Editor
  and CLI both, from one seam.
- Removing a row removes the ciphertext. There is no undeclare-keeps-the-value
  state, deliberately. Removing the *last* row leaves the file's
  `[credentials]` block in place (ruled 2026-08-14): the identity is the
  file's, not any row's, and the next `set` reuses it under the passphrase the
  user already knows instead of minting a new one.
- `byre credentials set|unset|rekey|list`. There is no `init`: the first `set`
  mints the identity, prompting for a new passphrase. `list` reads config rows
  — key, kind, source file (a row IS the value, so there is no set/unset
  state). `set --layer <name>` targets a layer
  file explicitly; the default target is the project config.

## Size

The per-value plaintext cap is 256 KiB — no real credential is big, and
base64+age overhead keeps several such values comfortably inside the existing
1 MiB config read bound, so no config-infrastructure bound moves. Kind-specific
rules ride on top: an env-kind value must be NUL-free and is capped at the
existing `MaxEnvValue` (64 KiB), because an environment variable cannot carry
a NUL through the launcher export; 256 KiB is the file-kind ceiling. Enforced
at `set`, with the payload's own cap backstopping decrypt.

## The preset bridge residual (disclosed, accepted, annotated)

The repo is agent-writable and `preset apply` is the reviewed bridge into the
trusted store. If a user ships credentials through a preset (which ships the
recipient too), a hostile writer of that repo can **MINT** a chosen value;
even without the recipient it can **SWAP**, **TRANSPLANT**, or **REPLAY**
existing blobs. The gain is durable poisoning of future sessions — a swapped
Stripe key that outlives the box — not plaintext, since the box receives
delivered plaintext anyway.

Accepted because: never-shared credentials have no exposure at all; a foreign
identity block fails loudly at unlock; and strictly more direct legible
attacks (`run_args`, egress) already ride the same channel behind the same
gate.

**The mitigation is the consent gate, not storage.** The preset review
annotates, at ⚠ weight:

- any changed row where EITHER side is a credential scheme — encrypted to
  encrypted (rotation or swap), encrypted to a plaintext scheme or an `[env]`
  literal, and plaintext to encrypted — with *"credential value changed … if
  you didn't rotate this credential, reject"*. Either-side is the point:
  replacing ciphertext with plaintext must not dodge the classifier.
- a credential row that appeared or vanished, more quietly: those are
  declarations, and the grant summary already lists them.
- any change to the file's `[credentials]` block — identity or recipient
  appearing, changing, or vanishing — which is otherwise invisible, since
  `Parse` drops the block by design. *"this preset replaces the file's
  credentials identity — its rows open under ITS passphrase, and values you
  set afterward would encrypt to ITS recipient; if you didn't do this,
  reject."*

Judged over both files' RAW bytes, ciphertext elided (`RenderSource`), values
never rendered. It does not gate.

Residuals the annotation does NOT close, named rather than fought: a change
absorbed BEFORE the user reaches the gate (an agent that edits the repo preset
and the applied marker together shows a clean drift state, exactly as it can
for `run_args` today), and a value changed in one window and reviewed in
another by a user who no longer remembers whether they rotated it. Both are
the standing limit of a review gate, not of credentials.

## Consequences

- `filippo.io/age` joins the dependency set (P7: the crypto is owned, not
  reimplemented).
- PRINCIPLES' "not a secret manager" narrows to the rotation/IAM sense.
- The security-model page carries the at-rest claim and the residual list,
  including mint/swap/transplant/replay in those words.
- The editor is where these are authored (P0): credential rows show on the Env
  screen, the Source picker
  carries both kinds, the Value input is masked and never echoed, and saving a
  value through the form IS the `set` path — same CAS, same file lock, same
  write-target disclosure, reached through a seam (`configui.CredentialAdmin`)
  rather than a second spelling. The first credential in a file opens the
  per-file passphrase modal. Boundaries taken deliberately: the credential
  kinds appear only where a credentials verb can target the file (project
  config, layer — not `default.config`); a damaged or reserved-key row refuses
  and names the CLI; the value lands on ACCEPT rather than at `^s`, and the
  form says so before a value is typed; a credential row's key cannot be
  renamed in place, since the payload is stamped with the key; and a file whose
  identity is gone gets told the truth about its orphaned rows rather than a
  modal claiming it holds no credentials.
- Sibling amendments: 0007 (project credentials are user-declared values, not
  agent-login seeding), 0009/0032 (credential state is the config cascade's,
  not the store's — worktrees share the project config and the setup lock;
  tmpfs owned by the container identity), 0011 (the wait takes this gate's
  fail-closed shape), 0026/0031 (the scheme set gains two members and
  env_from_host is again the one deliberate host→box channel), 0028, 0044
  (every credential write rides tomldoc, under compare-and-swap), 0050, 0052
  (register entry), 0053 (the record carries outcomes and keys, never values).
