# Project credentials: confidentiality at rest, consent per launch

Handing an agent an API key used to mean `env_from_host` (the host
environment must already carry it) or a plaintext `[env]` literal (baked
into the image; the reference explicitly warns against it). Neither stores
a secret for the project. This ADR lands the vault:
`~/.byre/projects/<id>/credentials/` holds named values age-encrypted at
rest, `[[credentials]]` declarations name what may be delivered and where,
an explicit passphrase unlocks per launch, and the values arrive in the box
over an exec-stdin stream onto a per-session tmpfs. Decided 2026-08-07
(design brief: thirteen revisions, fifteen independent review rounds; the
v12 sweep below is the load-bearing decision).

## The threat model IS the decision

The feature's security content is exactly three things, and the list of
what it does NOT defend is as binding as the list of what it does:

- **Confidentiality at rest against off-box disk access** (stolen laptop,
  backup blob, synced dotfiles): values on disk are age ciphertexts —
  per-entry X25519 to the vault recipient, the identity scrypt-wrapped
  under the passphrase (pinned work factor; `SetMaxWorkFactor` bounds the
  unwrap as a LIVENESS measure, not a defense). The recipient model is what
  makes passphrase-free cold staged writes possible (the editor's ^s, `byre
  credentials set`).
- **Explicit per-launch consent**: the declarations are the standing,
  cascade-visible consent to the SET; the passphrase entry is both
  authentication and the per-launch consent act. Declined, absent, non-TTY,
  or failed unlock launches WITHOUT credentials — a notice, never a block,
  at no waiting cost. A value never arrives as a command-line argument
  (argv is shell history and the process list).
- **byre's own plaintext hygiene**: byre writes plaintext filesystem
  artifacts in exactly ONE place — the session tmpfs at `/run/byre`
  (`--tmpfs`: no image layer, no writable layer, empties when the box
  stops). No plaintext host file, image layer, engine-visible config value,
  or volume content. Transient process/pipe memory is disclosed separately
  (swap/core/hibernation residency, security-model page).

Explicitly NOT defended — each in-scope-accepted or out-of-model, and the
v12 sweep DELETED a large apparatus (engine-truth mount/entrypoint/env
verification, trusted-executable chains, foreign-vault quarantine,
snapshot-hash tamper detection, an in-box status probe) that existed only
to fight these:

- **The agent reading or re-persisting its credentials.** It is HANDED
  them; that is the contract. The maintainer's razor: friction against a
  live privileged reader is theatre; a copy that outlives the unlock window
  is a real hole — and the tmpfs dying with the box is what answers it.
  Verifying the in-box execution environment defends plaintext against the
  party it was given to, and is not built. Do not restore it.
- **In-box code editing the vault.** The store is unreachable from a normal
  box; `--self-edit` mounts it rw under its own 🛑 warning, and credential
  tampering is strictly beneath that disclosed hole.
- **The user's own mounts and store edits** (footgun doctrine, P1). The
  tmpfs is an ADR 0052 managed path: a mount over `/run/byre` relocates
  delivered plaintext onto durable storage — and a durable shadow can
  re-surface AND re-export the bytes on a bare `docker restart` without a
  fresh unlock. Disclosed by the shadow machinery, never gated.
- **Store integrity.** The vault adds confidentiality at rest, NOT
  integrity against its own store: a store writer can roll back, forge, or
  delete vault contents (ruling, 2026-08-06). The `project-id` and `name`
  stamped inside each entry are ACCIDENT guards — a cross-project copy or
  wrong-project restore decrypts to a mismatched stamp and is skipped
  loudly (`entry-mismatch`) — never integrity mechanisms: a store writer
  can mint a correctly-stamped entry, and that residual is disclosed, not
  fought.

## Mechanics (each one a pinned decision)

- **Lock scope**: the expensive scrypt unwrap runs at the prompt, BEFORE
  the setup lock (holding it there would stall sibling worktrees for an
  authentication cost); only the cheap per-entry decrypts hold the lock,
  each ciphertext read ONCE — the delivered bytes are those present at
  decrypt time (no frozen snapshot, no hashes; plain correctness against a
  cooperating worktree).
- **Creation is one recoverable step**: identity + index staged in a temp
  dir and renamed into place under the lock, refused if a vault exists — an
  interruption leaves sweepable debris, never an identity-without-index
  wedge. Rekey rotates the PASSPHRASE, not the identity (single-file
  replace, refused with `ErrVaultChanged` if the vault was replaced after
  the unlock); after a suspected identity leak the remedy is
  `init --replace`.
- **Every pre-launch read is bounded** (identity, index, entries): a
  corrupt or enormous file degrades to a clean outcome, never unbounded
  memory before a launch. Wrong passphrase re-prompts, three attempts,
  Enter skips at any point.
- **Delivery protocol**: the export map rides the delivery stream as a
  non-secret MANIFEST (per-name kind + target) written to the tmpfs before
  the `.done` sentinel, which is written LAST — the launcher never observes
  a half-written tree, and an emptied tmpfs never leaves a stale map.
  `BYRE_CRED_EXPECT` is a create-time chassis flag set only when a delivery
  is in flight, with wait/export protocol meaning ONLY (the v11
  verification semantics are deleted; ADR 0050 reserves the `BYRE_CRED_*`
  vocabulary as launch-claim knobs).
- **The wait is bounded FAIL-OPEN** — the deliberate opposite of ADR 0011's
  fail-closed network gate, stated side by side: no credentials is safe, no
  wall is not. The launcher's export step follows the env.d loop (ADR
  0028), so credential exports win env collisions; env-kind exports are
  byte-exact (`read -rd ''`, never command substitution). A restarted,
  un-re-unlocked box pays at most the bounded wait and runs without.
- **Honesty by measurement**: the inject's stderr outcome says plain
  "delivered" only when it provably landed inside the launcher's wait
  (the epoch is captured before the goroutine spawns, so the measurement
  overestimates); past that it hedges. The launch record carries the
  unlock outcome and per-name decrypt outcomes ("scheduled", deliberately
  not "delivered": the record is immutable and written pre-start) — a
  launch-time fact recorded with the launch it belongs to. There is NO
  live-state surface anywhere: byre does not probe the box (ruling,
  2026-08-07 — the unlock outcome is a launch fact; status shows it and
  claims nothing live).
- **Editor-integrated entry is the north star** (P0): masked staged input,
  values render nowhere, staged until ^s, quit discards; a vault-less
  project's first ^s creates the vault inline. CLI verbs are shortcuts.
  Vault I/O rides hostopen (standing rule — the store is agent-authorable
  under `--self-edit`; robustness, not a credential-specific defense).

## Consequences

`filippo.io/age` joins the dependency set (P7: the crypto is owned, not
reimplemented; a weak passphrase weakening at-rest confidentiality is the
user's residual). PRINCIPLES' "not a secret manager" narrows to the
rotation/IAM sense. The security-model page carries the at-rest claim and
the residual list. Sibling amendments: 0007 (project credentials are
user-declared values, not agent-login seeding), 0009/0032 (one vault per
project, worktrees share, setup lock serialises; tmpfs owned by the
container identity), 0011, 0026 (the second deliberate host-value channel),
0028, 0044 (declaration saves ride tomldoc; `index.toml` is
machine-authored whole-file, outside its scope), 0050, 0052 (register
entry), 0053 (the record carries outcomes and keys, never values).
