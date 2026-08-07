# secure-credentials -- design brief v13

Provenance: v1-v11 2026-08-06..07 built the feature up through eleven fresh
three-reviewer rounds, then ACCRETED a large defensive apparatus (engine-truth
mount/entrypoint/env verification, a "trusted executable path set" chased down
through bash -> its aliases -> usrmerge symlinks -> its dynamic loader and
libc, foreign-vault quarantine, recipient poisoning, snapshot-hash tamper
detection, an in-box status probe). v12 (2026-08-07) is a CLEAN SWEEP of that
apparatus. It is a deliberate deletion, not a regression: the apparatus was
defending byre's in-box plaintext against in-box code and against mounts --
i.e. against the agent and against the user's own store edits -- both of which
are OUT of byre's threat model. See "Threat model" below. Independent
corrected-frame reviews (2026-08-07) confirmed the sweep cut deep enough and
the remainder is load-bearing; the deleted machinery is not to be restored.
v13 (2026-08-07) folds those reviews' correctness/robustness/honesty pins --
lock scope, export-map protocol, recoverable vault creation, bounded reads,
wrong-passphrase policy, the no-CLI-argument contract line, disclosure
precision, restart-honest status -- and softens leftover adversarial
vocabulary to plain correctness terms. Status: NOT ratified; awaiting Pete
(ratification, then UI + forks 4/7/8, then the doctrine unit).

## Pete's rulings (binding)

- Value entry is EDITOR-INTEGRATED (north star): masked input, staged until ^s;
  quit discards. CLI verbs are shortcuts. UI detail deferred.
- Crypto is age-style (`filippo.io/age`); the passphrase layer is age's scrypt
  (P7); the recipient model is what makes passphrase-free cold staged writes
  possible.
- **Pete's razor:** friction against a live privileged reader is theatre; a copy
  that outlives the unlock window is a real hole. This razor DEMANDED the v12
  sweep -- the in-box agent is the live privileged reader, and the deleted
  apparatus was friction against it.
- **Store integrity (2026-08-06):** the vault adds confidentiality at rest, NOT
  integrity against its own store. Rollback/forgery/replacement by a store
  writer is a disclosed residual, never a defended surface.

## Threat model (what this feature does and does not defend)

byre credentials protect against **off-box access to the disk**: a stolen or
lost laptop, a backup blob, a synced dotfiles directory, a sold machine. The
value on disk is an age ciphertext, inert without the passphrase. That is the
whole of the security content, and it is real.

Explicitly NOT defended, because each is already in-scope-accepted or
out-of-model:

- **The agent reading its credentials.** It is handed them; that is the
  contract. In-box plaintext (tmpfs files, the launcher's environment) is
  readable by the dev-uid agent by design. The agent can also copy a delivered
  value into `/workspace` (a host bind) -- the accepted agent tier. Any
  machinery that "verifies" the in-box execution environment is defending
  plaintext against the party we already gave it to: theatre.
- **The box editing the vault.** The host store (`~/.byre/projects/<id>/`) is
  NOT mounted into a normal box; the vault is unreachable from inside. The ONLY
  way in-box code touches the store is `--self-edit`, which bind-mounts it rw
  and already prints "🛑 self-edit is on. A malicious or incompetent agent can
  change the configuration to grant itself full access to your host on the next
  run." Credential tampering is strictly beneath that disclosed hole. No
  credential-specific defense is added for it.
- **The user editing `~/.byre/` or writing a mount over byre's paths.** The
  user is not byre's adversary (footgun doctrine). A user who mounts a host dir
  over the credential tmpfs, or corrupts their own vault, gets legible
  disclosure (below), not a gate.

## Contract

User declares named project credentials; values live only in an age-encrypted
vault in the host-side project store. Explicit unlock at launch; declined,
absent, or non-TTY unlock degrades to a launch without them, with a notice,
never a block, at no waiting cost. In-box, credentials are plainly usable.

A value never arrives as a command-line argument -- a positional CLI form
would leave plaintext in shell history and the process list. The editor's
masked staged entry is the intended path; any CLI shortcut reads the value
from a prompt or stdin, never argv. This is a contract line, not a deferred
UI detail.

At-rest claim (narrowed to what byre ITSELF does): **byre writes plaintext
filesystem artifacts in exactly one place -- the session tmpfs at
`/run/byre`, which is excluded from image layers and the writable layer and
empties when the box stops. Outside it, byre creates no plaintext host file,
image layer, engine-visible config value, or volume content.** Plaintext also transits
process, pipe, and crypto-buffer memory on the way there; that is not a
filesystem artifact, and its residency risk (swap, core dumps, hibernation)
is the disclosed residual below. The tmpfs is a managed path (ADR 0052); a
user mount landing a host dir or persistent volume over `/run/byre` would
relocate that plaintext onto durable storage -- and because such a shadow
survives the box stopping, a bare `docker restart` could re-surface (and
re-export) previously delivered bytes without a fresh unlock. Both are the
existing 0052 managed-path shadow, DISCLOSED by the existing shadow
machinery, not a defended gate (the user is not the adversary).

Disclosed residuals (security-model page): the vault's metadata is not itself
encrypted -- `index.toml` carries the recipient and the credential
names/kinds in the clear, so off-box disk access learns that project
credentials exist, what they are called, and the public recipient (the values
stay ciphertext); host swap/core-dump/hibernation residency; remote engines
(`DOCKER_HOST=ssh://`) relocate every runtime surface; the agent may read and
re-persist delivered values (accepted agent tier); an open network
exfiltrates unlocked credentials (firewall interplay); `--self-edit` hands
the agent authorship of the vault (already disclosed with its 🛑); a store
writer can roll back, forge, or delete vault contents (store-integrity
ruling). Values are encrypted with a scrypt-derived key; a weak passphrase
weakens at-rest confidentiality (P7).

## Vault

Layout under `~/.byre/projects/<id>/credentials/`:

- `identity.age` -- scrypt-wrapped X25519 identity, the only object the
  passphrase unlocks. Created with a pinned work factor; `SetMaxWorkFactor`
  bounds unwrap compute so a corrupt or absurdly-parameterised header cannot
  stall the launch (a liveness bound).
- `entries/<name>.age` -- one ciphertext per credential, encrypted to the
  recipient; plaintext payload `{format-version, project-id, name, value}`.
  Per-entry files make passphrase-free cold staged writes coherent (add and
  replace are single-file writes). The name and project-id inside the payload
  are ACCIDENT guards, not integrity mechanisms: a re-labelled or wrong-project
  file decrypts to a mismatched name/project and is skipped loudly
  (`entry-mismatch`) -- this catches a cross-project copy or a wrong-project
  backup restore delivering the wrong value silently, which is a plausible
  accident. (It provides no integrity against a store writer, who can mint a
  correctly-stamped entry; that is the disclosed residual, not fought.)
- `index.toml` -- `{recipient, project-id, display cache (names/kinds)}`. The
  recipient is the encryption target for cold staged writes; the cache is
  display-only, repaired from decrypt results at unlock. Machine-authored
  whole-file (temp+rename), outside ADR 0044's user-config tomldoc scope.

Every pre-launch read is bounded: the identity and index reads carry modest
byte caps (entries already have one), so a corrupt or accidentally enormous
file degrades to a clean `unlock-failed` or notice instead of consuming
unbounded memory before launch. Caps are generous multiples of the largest
legitimate file, pinned at implementation.

One vault per project; worktrees share it; the single existing per-project
setup lock serialises writes (the GLOSSARY term; not the packages store
lock). Vault reads and writes ride hostopen (the
standing repo rule for any path the agent could author under --self-edit;
robustness, so a FIFO or symlink cannot hang or escape byre -- not a
credential-specific measure).

Creation (`byre credentials init`, or inline on the first editor ^s in a
vault-less project): TTY-only masked new-passphrase prompt with confirm,
pinned work factor. The identity and index are staged together in a temp
directory under the store and renamed into place as ONE step under the lock,
refused if a vault directory already exists -- so creation never silently
overwrites an existing vault, and an interruption leaves only a sweepable
temp directory, never a wedged identity-without-index state. `--replace` is
the explicit discard-and-recreate. Rekey re-wraps the identity under a new passphrase
(single-file atomic replace); it rotates the passphrase, not the identity --
after a suspected identity leak the remedy is a new vault, stated.

## Declarations

```toml
[[credentials]]
name   = "stripe"           # ^[a-z][a-z0-9-]{0,62}$
kind   = "env"              # env | file
target = "STRIPE_API_KEY"   # env: the variable; file: the variable holding
                            # the byre-owned tmpfs path
```

File-kind lands only at `/run/byre/credentials/<name>`; there are no free
filesystem targets. Validation refuses duplicate names, duplicate targets,
`BYRE_`-namespace targets, and targets that are not shell identifiers. Env-kind
values are NUL-free, trailing newline stripped, and capped at 64 KiB (headroom
under `MAX_ARG_STRLEN`; a user who declares enough large env values to blow
`ARG_MAX` gets a self-announcing exec failure -- a footgun, not a defended
surface). File-kind values are arbitrary bytes, capped generously. Declarations
ride the named-declaration rails (replace-by-name, `!name`); saves ride tomldoc
(ADR 0044). `vault:` is not an env_from_host scheme (0031 untouched). The editor
env surfaces and the exposure tally present the credential channel alongside
`env_from_host` as one "what env does the agent see" answer.

## Launch and delivery

1. **Unlock prompt** -- TTY only, before the setup lock (a prompt under it
   would stall siblings). Enumerates the declared set and per-name value-state.
   Passphrase, or Enter to skip. Non-TTY skips with a machine-readable notice.
   Declined/absent/non-TTY/empty-vault all launch without credentials, no wait.
   The declarations are the standing, cascade-visible consent to the set; the
   passphrase entry is both authentication and the per-launch consent act.
   The expensive scrypt identity-unwrap happens HERE, at the prompt, before
   any lock is taken -- holding the shared setup lock across it would stall
   sibling worktrees for an authentication cost. A wrong passphrase (unwrap
   failure) re-prompts, three attempts total, Enter skipping at any point;
   exhausted attempts launch without credentials (`unlock-failed`).
2. **Decrypt** -- with the identity already unwrapped, byre takes the setup
   lock only for the cheap per-entry decrypts, reading each ciphertext ONCE
   and delivering exactly those bytes (plain correctness against a concurrent
   cooperating worktree; no hashes). Unlock covers the named declared set,
   and the delivered bytes are those present in the store at decrypt time --
   there is no frozen pre-prompt snapshot, and deliberately no per-entry
   hashing. Per-name outcomes (`missing-value`, `entry-undecryptable`,
   `entry-mismatch`, `unsupported-format`) are reported here, at the prompt,
   where re-entry is actionable. If nothing is deliverable, launch proceeds
   without credentials.
3. **Deliver** -- byre creates the container with a `--tmpfs /run/byre`
   (`rw,noexec,nosuid,nodev,mode=0700,uid=<container-identity>,size=<cap>`;
   podman adds `notmpcopyup`; ADR 0032 ownership), starts it, and pipes the
   deliverable set as a framed stream over `exec -i` stdin (dev identity,
   bounded, wall-clock deadline) to a small baked in-box receiver. The stream
   opens with a non-secret MANIFEST -- per-name kind and target, the export
   map -- which the receiver writes under `/run/byre` alongside each value,
   then a `.done` sentinel LAST. The map travels with the values it
   describes, so an emptied tmpfs never leaves a stale map behind. The
   chassis env var `BYRE_CRED_EXPECT` is set at create time ONLY when this
   launch decrypted a deliverable set and scheduled an injection; once set
   it persists with the container (which is why a later bare restart pays
   the bounded wait). It is purely a wait/export protocol flag ("wait
   bounded for `.done`, then export from the manifest") -- it carries no
   verification meaning (that was v11 machinery, deleted). Delivery failure
   never blocks the box: it launches without credentials, notice recorded.
4. **Export** -- the launcher (when `BYRE_CRED_EXPECT` is set) waits bounded
   (fail-open) for `.done`; if it is present when the wait ends, the launcher
   exports env-kind values from the manifest (`TARGET=value`, byte-exact, no
   shell re-evaluation) and points file-kind targets at their tmpfs paths, in
   a step after the env.d loop so credential exports win env collisions.
   Timeout -> proceed without credentials ("without credentials" means byre
   performs no credential export; an unrelated channel defining the same
   target keeps its value). A restart empties the tmpfs, so a restarted box
   re-delivers only if re-unlocked; otherwise it pays at most the bounded
   wait (the create-time flag persists) and runs without.

Late or partial delivery is harmless and needs no arbitration: byre exports
only when `.done` is present (so a half-written tree is never exported), and a
value that lands after the launcher gave up simply sits on the tmpfs the agent
already has rights to. The elaborate publish-arbitration / outcome-slot
machinery of earlier drafts existed to police what the in-box agent could
observe, which is not defended.

## Legibility

- **Status / editor:** `byre status` lists declared credentials with kind,
  target, and vault value-state (set / unset), plus this box's launch-time
  unlock outcome (a launch fact, recorded with the launch; outcomes only,
  never values). The design no longer probes in-box state, so status says
  nothing about live delivery -- no live-state field at all. Live per-value
  in-box delivery state is a deferred legibility nicety, not core (it
  previously drove a heavy in-box probe; cut). The editor
  Credentials screen is declaration widgets + staged masked entry + a per-name
  value-state cell; values render nowhere. Ships with the grammar (P0/P6).
- **Exposure tally / banner:** gain a credentials segment; adoption/preset
  review flags new names and kind/target changes.
- All declaration- and vault-derived strings ride the existing P4 escape arms.

## Outcome vocabulary (small, honest)

Host-side, reported at the prompt and recorded for status:
`skipped-declined`, `skipped-nontty`, `unlock-failed` (passphrase attempts
exhausted, or a corrupt, oversize, or absent identity), `missing-value`
(declared, no entry), `entry-undecryptable` (corrupt or oversize ciphertext,
or one encrypted to a different recipient),
`entry-mismatch` (payload name or project-id disagrees -- the accident guard),
`unsupported-format`, `delivered`, `not-delivered` (delivery timed out or the
inject failed). No quarantine, foreign-vault, recipient-mismatch,
snapshot-mismatch, capture-failed, or restart-discriminator states -- those
named adversary conditions that are out of model.

## Tree seams (new work)

1. `Runner.Create` returns the container ID (today discarded), so byre can
   inject by ID.
2. A `RunParams` tmpfs field, per-engine flag assembly.
3. A bounded exec-stdin seam (framed stdin, wall-clock deadline, capped output)
   for the inject.
4. A small baked in-box receiver that reads the framed stream and writes the
   tmpfs (an ADR 0052 managed path).

(Gone from earlier drafts: engine-truth delivery-environment verification, the
trusted-path/interpreter checks, the run_args scan, the in-box status probe,
the bounded start-time inspect. None are needed once in-box code is not an
adversary.)

## Doctrine unit (ships with the feature)

- New ADR (project-credentials vault): the threat model above is the spine --
  confidentiality at rest against off-box access; the agent is handed the
  values; in-box execution and store edits are not defended; Pete's razor and
  the store-integrity ruling as the claim rules. hostopen for vault I/O cited
  as the standing rule. README index line in the same commit.
- **ADR 0026 amendment:** a deliberate second host-value channel (the vault),
  distinct from `env_from_host`.
- **ADR 0050:** `[[credentials]]` claim classification; `BYRE_CRED_EXPECT`
  reserved chassis vocabulary, pinned as a wait/export protocol flag only
  (no verification meaning survives from v11); target namespace rejection.
- **ADR 0044:** declaration saves ride tomldoc; `index.toml` machine-authored,
  outside scope.
- **ADR 0009 / 0032:** one vault per project, worktrees share, the single
  setup lock serialises; tmpfs ownership uses the container Identity UID/GID.
- **ADR 0011 amendment:** the credentials wait is a bounded FAIL-OPEN wait
  beside the fail-closed network gate (opposite directions, stated why); it
  sits at the launcher top and the sentinel is restart-safe because the tmpfs
  empties (a restarted, un-re-unlocked box pays at most the bounded wait,
  since the create-time flag persists).
- **ADR 0028 purity arm** for the post-env.d export step. **ADR 0052 register**
  entry for the tmpfs + receiver (the credential tmpfs shadow is the existing
  managed-path disclosure). PRINCIPLES "not a secret manager" narrows to "not
  rotation/IAM". security-model: the narrowed at-rest claim + the residual list.
  GLOSSARY entry ("project credentials" vs the agent-login sense).

## Open forks (unchanged, post-sweep)

4. Prompt habituation: full enumeration each launch, skip costs zero; revisit
   with field evidence.
7. Naming: docs "project credentials"; CLI noun `credentials`; GLOSSARY
   disambiguation line.
8. Missing-value semantics: UNSET row + one launch line; never a prompt trigger.
