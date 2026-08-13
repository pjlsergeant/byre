# Inline credentials (design B) — ruled 2026-08-13

Status: RATIFIED design, replaces the vault-directory design on this branch.
Provenance: the layer-vault grilling (2026-08-12/13). Pete dismantled the
vault's arguments one by one; codex consulted twice — first fresh consult
picked the vault, second fresh consult, given the full argument chain,
returned "CONCUR: B is sound to build" (scratchpad logs:
inline-vs-vault-codex.log, b-signoff-codex.log). ADR 0057 is rewritten when
this ships; this file is deleted on absorb (wip/README.md).

## The ruling

Credential values live INLINE in config files. No vault directory, no entry
names, no join key, no involved-set computation, no orphan states, no
layer-vault scope machinery.

```toml
[env_from_host]
STRIPE_KEY = "encrypted:<age blob, base64>"        # delivered as env var
TLS_CERT   = "encrypted-file:<age blob, base64>"   # delivered as tmpfs file, path in env var

[credentials]
identity  = "<scrypt-passphrase-wrapped age X25519 identity, base64>"
recipient = "age1..."   # cleartext public half; lets set encrypt without the passphrase
```

The rows live in `env_from_host`, NOT `[env]` — that is where the grilling
ratified the scheme model, and the mechanics agree: env_from_host is a
closed scheme set (`git:`/`env:`/`tz:`) gaining two members, its `""`
disable is already the ratified per-project override idiom, its values are
runtime-resolved and never baked into the image (an `[env]` row rides the
Dockerfile ENV bake — ciphertext in the image and a rebuild on every
re-set), and `[env]` literals stay unrestricted (a literal beginning
"encrypted:" stays representable). Routing: encrypted rows are excluded
from the ordinary env_from_host `-e` export and delivered ONLY via the
tmpfs/stdin channel.

- The `[credentials]` block is FILE-LOCAL: it belongs to the physical file
  and never cascade-merges. A project block never decrypts a layer's rows.
- Values are encrypted to the recipient; the passphrase wraps only the
  identity. So `set` never prompts, and rekey rewraps one field — value
  blobs are byte-stable across passphrase changes.
- Resolution is the ordinary cascade merge. The winning row IS the value.
  `KEY = ""` in a nearer file is the idiomatic disable (ruled earlier).
- Kind rides the scheme (`encrypted:` env / `encrypted-file:` file), same
  shape as the ratified vault:/vault-file: scheme model it replaces.

## Why (one paragraph, for the ADR)

Config can already hold an opaque 256-char plaintext API key; value
legibility was never an invariant, so "ciphertext doesn't belong in config"
defended nothing. An encrypted row is a plaintext env row plus at-rest
protection, minus nothing that matters: blobs only change when a human
re-sets the value (rekey doesn't rewrite them), so plain byte-compare gives
drift the same semantics as any other value. Every argument for the separate
directory reduced to cosmetics — except one real asymmetry (the preset
bridge, below), which byre answers the way it answers everything: legibility
at the consent gate, not storage architecture. What the directory cost was a
join key, two divergence states, an involved-set computation, credential-
specific resolution (with its own orphan-shadowing hazard, structurally
impossible under B), an index, a lifecycle CLI, and an entire layer-vault
design docket.

## Launch flow

1. Resolve the cascade. Collect winning `encrypted:`/`encrypted-file:` rows;
   group by the physical file that contributed each (provenance the
   effective view already tracks).
2. Prompt per contributing file, root-most first. Each entered passphrase is
   first tried on every still-locked identity (people reuse passphrases);
   prompt only for those still locked. Print a plan line first:
   `Unlocking credentials: default.config (2), layer acme (1), project (3)`.
3. BLOCKING (ruled earlier): declared credentials are delivered or the
   launch stops. Wrong passphrase (3 attempts), unparseable file, broken
   identity, undecryptable blob — all stop the launch naming file, row, and
   remedy. `--credentials=ask|skip|stdin`: ask is the default; skip launches
   deliberately without them; stdin reads passphrase lines, each tried
   against all still-locked identities in order.
4. Delivery half UNCHANGED: decrypt under the setup lock, stream framed
   base64 over container stdin to the baked receiver, land on the per-
   session tmpfs (engine-shaped rendering stays). Never `docker -e`, never
   image/volume/writable layer. Launcher flips fail-closed (ruled earlier):
   no delivery, no agent, same as the network gate; host Stops the container
   on inject failure; restart refuses.
5. Launch record carries outcomes, never values (unchanged).

## Payload binding (ratified)

The encrypted payload is stamped with a format version + the config key +
kind it was set for. Mismatch after decryption fails loudly, naming both
keys. This is an ACCIDENT GUARD, not integrity — anyone holding the public
recipient can mint a correctly-bound blob. What it catches: blob swap
between rows, replay from git history landing on a renamed key, transplant
across files, and honest copy-paste mistakes. (Codex: without it, age blobs
are not bound to their row, so swap/replay/transplant need no recipient.)

## Write discipline (ratified)

- `set`/`unset`/rekey/identity-creation use compare-and-swap on the complete
  physical file under the store lock. The race it closes: `set` reads
  recipient R; a concurrent identity replacement lands R2; the R-encrypted
  blob beside R2 is permanently undecryptable though both writes "succeeded."
- Setting an inherited credential names the physical write target before
  accepting the value: "this writes to layer acme, used by N projects" —
  layer changes propagate live (ADR 0035), so the cross-project effect must
  be unmistakable. Editor and CLI both.

## Size (ruled: no real credential is big)

Per-value plaintext cap 256 KiB. base64+age overhead keeps several such
values comfortably inside the existing 1 MiB config read bound, so no
config-infrastructure bound changes. The branch's 4 MiB file-credential cap
was never released; it just shrinks. Kind-specific rules survive from the
vault design (supervisor ruling, phase A follow-up): env-kind values stay
NUL-free and capped at the existing MaxEnvValue 64 KiB (an env var cannot
carry NUL through the launcher export); the 256 KiB cap is the file-kind
ceiling. Enforced at set; the payload's own cap backstops decrypt.

## Preset bridge residual (disclosed, accepted)

The repo is agent-writable and `preset apply` is the reviewed bridge into
the trusted store. If a user ships credentials through the preset (which
ships the recipient too), a hostile agent can MINT a chosen value; even
without the recipient it can SWAP, TRANSPLANT, or REPLAY existing blobs.
The gain is durable poisoning of future sessions (e.g. a swapped Stripe
key), not plaintext — the box receives delivered plaintext anyway.

Accepted because: never-shared credentials have no exposure; a foreign
identity block fails loudly at unlock; the review gate still shows THAT the
row changed; and strictly more direct legible attacks (run_args,
egress) already live in the same channel behind the same gate. Mitigations
TO SHIP (step 6, not yet built): preset review annotates any changed row
where EITHER side is encrypted ("credential value changed: if you didn't
rotate this, reject") — either-side so replacing ciphertext with plaintext
can't dodge the classifier — AND any change to a file's [credentials]
block itself (identity/recipient appearing, changing, or vanishing), which
is otherwise invisible because Parse deliberately drops the file-local
table; and the security-model page discloses mint/swap/transplant/replay
in those words.

## Display

Long values elide everywhere config is rendered (`encrypted:[…]`), wanted
for plaintext API keys anyway. TO SHIP (step 5, phase C — not yet built; at
HEAD the Env screen renders credential rows elided/read-only and refuses
the picker, naming the CLI): the editor shows credential rows on the Env
screen: Source picker gains the encrypted kinds, Value input is masked,
saving a value through the form is the `set` path (CAS + write-target rules
apply). First credential in a file prompts to create that file's passphrase
(a per-file passphrase-creation modal). Status shows
launch-time unlock/delivery outcome only — no live-state field (ruled
earlier, unchanged).

## CLI

`byre credentials set|unset|rekey|list`. `init` dies (first `set` creates
the identity, prompting for a new passphrase). `list` reads config rows —
key, kind, source file, set/unset. `set --layer <name>` targets a layer
file explicitly; default target is the project config. "Upsert" as a
concept dies: set just writes the row, there is nothing else to keep in
step.

## What dies (and what survives) on the branch

Dies: the vault directory + index.toml + entries/, NameGrammar + the
receiver's bash restatement of it (receiver now gets keys, already
env-grammar), ValidateName, EntryNames/dir listings, involved-set
intersection, nearest-vault-wins, row/entry divergence states, stored-not-
mapped, undeclare-keeps-orphan semantics (removing the row removes the
ciphertext — deliberate simplification, noted in docs), [[credentials]]
CredentialDecl genus (already ruled dead), the separate Credentials editor
screen, the layer-vault docket (scope ids, multi-vault locks, lifecycle).

Survives, reworked: scrypt identity wrap/unwrap + rekey + 3-attempt prompt
loop + EmptyPassphraseWorthless (internal/credentials, storage moves from
dir to config blobs); Outcome vocabulary; the whole delivery chassis
(framing, receiver, tmpfs rendering incl. podman --mount form, epoch
honesty measurement); blocking + --credentials flag work; fail-closed flip
(was already ruled, still to implement).

## Implementation order (when dispatched)

1. internal/credentials: re-plumb identity/blob crypto onto config-file
   storage; payload binding stamp; kill dir machinery + its tests' subjects.
2. internal/config: encrypted:/encrypted-file: schemes on env rows;
   file-local [credentials] decode; delete credentialdecl.go.
3. Write path: set/unset/rekey via tomldoc under CAS + store lock;
   write-target disclosure.
4. Launch: provenance grouping, root-most-first unlock, reuse trying, plan
   line, blocking + flag, fail-closed launcher flip + host Stop + restart
   refusal.
5. TUI: Env screen integration; per-file passphrase modal; elision.
6. Preset review annotation (either-side-encrypted).
7. Docs: rewrite ADR 0057 (+ re-sweep the ten amendment banners), GLOSSARY,
   security-model residuals (mint/swap/transplant/replay), configuration
   reference, PRINCIPLES "not a secret manager" stands.
