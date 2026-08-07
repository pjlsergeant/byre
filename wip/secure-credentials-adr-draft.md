# ADR NNNN: Project credentials — confidentiality at rest, consent per launch

DRAFT (overnight build, 2026-08-07). Not filed into docs/adr/ — the feature
and its doctrine unit await Pete's ratification. Numbering, the README index
line, and the sibling amendments land together when filed.

## Status

Proposed.

## Context

Handing an agent an API key today means `env_from_host` (the host
environment must carry it) or a plaintext literal in config (baked into the
image; refused for good reason). Neither stores a secret at rest for the
project. wip/secure-credentials.md (v13, thirteen revisions and fifteen
independent review rounds) designed the vault; this ADR is its spine,
recorded so the threat model cannot silently regrow what was deliberately
cut.

## Decision

byre keeps named project credentials in an age-encrypted vault in the
host-side project store, unlocked by a passphrase per launch, delivered
into the box over an exec-stdin stream onto a per-session tmpfs.

The security content is exactly this:

- **Confidentiality at rest against off-box disk access** (stolen laptop,
  backup blob, synced dotfiles): values on disk are age ciphertexts, inert
  without the passphrase (scrypt-wrapped X25519 identity; work factor
  pinned, unwrap compute bounded as a liveness measure).
- **Explicit per-launch consent**: the [[credentials]] declarations are the
  standing, cascade-visible consent to the set; the passphrase entry is
  both authentication and the per-launch consent act. Declined, absent,
  non-TTY, or failed unlock launches WITHOUT credentials — a notice, never
  a block, at no waiting cost.
- **byre's own plaintext hygiene**: byre writes plaintext filesystem
  artifacts in exactly one place — the session tmpfs — which is excluded
  from image layers and the writable layer and empties when the box stops.
  No plaintext host file, image layer, engine-visible config value, or
  volume content. (Transient process/pipe memory is disclosed separately:
  swap/core/hibernation residency.)

Explicitly NOT defended, each already in-scope-accepted or out-of-model:

- **The agent reading or re-persisting its credentials** — it is handed
  them; that is the contract (Pete's razor: friction against a live
  privileged reader is theatre).
- **In-box code editing the vault** — the store is unreachable from a
  normal box; --self-edit mounts it rw under its own 🛑 disclosure, and
  credential tampering is strictly beneath that disclosed hole.
- **The user's own mounts or store edits** — footgun doctrine; the tmpfs is
  an ADR 0052 managed path whose shadow is disclosed, never gated. A
  durable shadow can re-surface and re-export delivered bytes on a bare
  restart without a fresh unlock; disclosed, not fought.
- **Store integrity** — a store writer can roll back, forge, or delete
  vault contents (the 2026-08-06 store-integrity ruling). The name and
  project-id stamped in each entry are ACCIDENT guards (cross-project copy,
  wrong-project restore → loud entry-mismatch), not integrity mechanisms.

Mechanics pinned by the brief and implemented: the scrypt unwrap runs
pre-lock (sibling worktrees never stall on a human); per-entry decrypts run
under the setup lock, each ciphertext read once; the export map rides the
delivery stream as a non-secret manifest; BYRE_CRED_EXPECT is a create-time
wait/export protocol flag with no verification meaning; the launcher's wait
is bounded FAIL-OPEN (the deliberate opposite of the fail-closed network
gate); a value never arrives as a command-line argument; vault creation
stages identity+index in one recoverable rename; every pre-launch read is
bounded; wrong passphrase re-prompts three attempts. Vault I/O rides
hostopen (standing rule). The launch record carries the unlock outcome and
per-name decrypt outcomes — a launch-time fact recorded with the launch it
belongs to; there is no live-state field anywhere (byre does not probe the
box).

## Consequences

Amendments that ship with this ADR when filed (drafted, pending):

- **ADR 0026**: a deliberate second host-value channel (the vault), beside
  env_from_host.
- **ADR 0050**: [[credentials]] claim classification (rendered: status rows
  + tally segment); BYRE_CRED_EXPECT/DIR/WAIT reserved chassis vocabulary,
  protocol-only.
- **ADR 0044**: declaration saves ride tomldoc; index.toml is
  machine-authored whole-file, outside scope.
- **ADR 0009/0032**: one vault per project, worktrees share, the setup lock
  serialises; tmpfs ownership uses the container identity.
- **ADR 0011**: the credentials wait is bounded fail-open beside the
  fail-closed network gate — opposite directions, both stated; the sentinel
  is restart-safe because the tmpfs empties (a restarted, un-re-unlocked
  box pays at most the bounded wait).
- **ADR 0028**: the post-env.d export step (credential exports win env
  collisions).
- **ADR 0052**: register /run/byre + the receiver as managed paths.
- **ADR 0007**: project credentials are user-declared values, not
  agent-login seeding — this does not reopen 0007.
- PRINCIPLES "not a secret manager" narrows to "not rotation/IAM";
  security-model page gains the at-rest claim + residual list; GLOSSARY
  disambiguates "project credentials" from the agent-login sense.
