# grok-shared-auth v2: the auth broker

grok-shared-auth is **rebuilt** (2026-07-16) as an auth broker riding
`GROK_AUTH_PROVIDER_COMMAND` — grok's own external-auth seam — replacing
the retired v1 symlink design (ADR 0023). The two parked candidate designs
(`wip/grok-shared-auth-v2-designs.md`, now absorbed here and deleted)
required their gates to run BEFORE any build; xAI published the Grok CLI
source ("Grok Build") in the interim, so every gate was answered by
source inspection of the vendor tree plus live probes against the in-box
0.2.101 binary, rather than by risking live credentials. The gate record
below is the evidence; mechanics live in
`docs/AGENT-CREDENTIAL-MECHANICS.md` §6.

## The mechanism

A ~150-line bash broker (`/etc/byre/grok-auth-broker`), shipped by the
companion skill and named in every opted-in box's
`GROK_AUTH_PROVIDER_COMMAND`. No resident process: grok runs it when it
needs a credential and waits. Per invocation, under one `flock` in the
machine-scoped identity volume: read the shared OAuth pair; if the access
token is fresh, emit it (always the JSON form, with `expires_in`, so grok
tracks real expiry); if stale, perform the standard OIDC `refresh_token`
grant (endpoint from issuer discovery, cached), persist via temp+rename,
then emit. Kernel flocks are atomic across containers on one shared
volume, so exactly one process machine-wide ever spends the single-use
refresh token — the correlated cold start serializes like any other
contention.

The decisive source finding: with the provider command set, grok's
`build_refresher` **never constructs its own OIDC refresher** — the
external command is the refresh authority for every token type, on expiry
and on 401 recovery alike. No box can rotate the chain behind the
broker's back, which is the failure that killed v1.

**Seeding** goes through grok itself: the firstrun hook runs
`GROK_AUTH_PATH=<store> grok login --device-auth` (the env var relocates
grok's whole credential subsystem — reads, writes, login persistence,
lock — source-verified), so the shared store is a grok-native `auth.json`
and no copy/translate hygiene exists to get wrong. A real per-box login
found at launch (refresh token present — broker-issued tokens never carry
one) is promoted to the machine store and dropped locally, announced. A
dead chain (`invalid_grant`) is moved aside by the broker with re-seed
instructions — which also self-heals the v1 volume's orphaned credential,
since the rebuild reuses the same `grok-identity` volume and store path.

## The gate record (2026-07-16, all pre-build)

Source citations are to the published grok-build tree; the three log
strings observed in the 0.2.93 field failure all exist verbatim in it,
and the in-box binary is 0.2.101.

1. **Re-invocation (the design-killer) — PASS.** The provider command is
   re-run at token expiry, at 401 (`RefreshReason::ServerRejected` →
   `refresh_chain` → `ExternalBinaryRefresher`), and proactively when
   `expires_in` was provided; 401 recovery never falls back to
   interactive login. JSON output carries a server-authoritative expiry;
   a bare-token output would fall back to a 30-day assumed lifetime —
   the broker therefore always emits JSON.
2. **Precedence vs a stored login — understood, benign.** A valid stored
   credential is used as-is; once it expires, the refresh authority is
   the provider command regardless of the stored token's type. A
   leftover per-box login therefore delays broker adoption by at most
   one access-token lifetime (~6h) and is never spent again; the launch
   hook's promotion mops it up sooner.
3. **The refresh grant — verified from source.** Standard OIDC
   `refresh_token` POST (form-encoded; `client_id`, optional
   `principal_type`/`principal_id`; no secret, no scope), token endpoint
   from `{issuer}/.well-known/openid-configuration`. `invalid_grant` and
   `invalid_client` are the only terminal errors; everything else is
   retried. The broker mirrors exactly this classification.
4. **Headless failure — vendor-fixed.** The 0.2.93 "headless auth
   failure hangs on a device prompt" shape is gone: `grok -p`'s auth
   path bails with re-auth instructions (source), verified live against
   0.2.101 with an empty `GROK_HOME` (exit 1, no hang). The broker
   additionally degrades a transient refresh failure to emitting the
   cached access token while it plausibly lives.
5. **Reuse consequence — CONFIRMED, no experiment needed.** Vendor
   source states it outright: a reused refresh token triggers
   `invalid_grant` **and token-family revocation**, and grok's own lock
   is held across the IdP call precisely to prevent it. This priced
   Design 2 out (see below).

Constraint discovered at the seam: refresh-path invocations are killed at
**5 seconds** and their stderr is swallowed. The broker budgets every
wait to that (lock wait 1.5s, POST 2.5s, no discovery on that path — the
endpoint cache is pre-warmed at seeding) so a kill can never land mid-POST
and strand a spent refresh token; it also logs to `broker.log` beside the
store because stderr is invisible there.

## Alternatives closed by the same source pass

- **Design 2 (watcher + jitter)**: obsolete. Its accepted race now has a
  confirmed maximal price (family revocation), and its premise (grok
  lacks cross-process refresh coordination) turned out false — grok
  already flocks and adopts sibling tokens; what it cannot do is cross
  **container** coordination (next item).
- **"Design 0" (share a real credential file via `GROK_AUTH_PATH`, no
  broker)**: dead, and the reason sharpens ADR 0023. grok's lock is a
  real flock held across the refresh — but its staleness probe is
  `kill(pid, 0)`, and a holder in another PID namespace reads as ESRCH
  (dead), so a contending box **unlinks a live lock near-instantly** and
  both spend the refresh token. grok's own locking can never serialize
  boxes; only a lock grok doesn't interpret (the broker's) can.
- **`XAI_API_KEY`**: still ruled out on cost (ADR 0023; ~50x the
  subscription at coding-agent volumes).

## The remaining gate, and the vouch

Everything above is source plus in-box probes; the one thing that cannot
be verified that way is a **live broker refresh accepted end-to-end by
the backend** (a real seeded store rolling over a ~6h expiry in a real
box). Until that field gate runs, the skill ships with `companion_for =
"grok"` — enabled by hand, paired in the picker — and NOT
`shared_auth_for` (the ADR 0025 onboarding offer), because declaring that
key is vouching the mechanism. That is the v1 lesson applied: the vouch
follows the field gate, never precedes it. Flip the key when the gate
passes.

Failure modes and recovery are unchanged from the design doc's broker
table: everything is loud, and the worst case of any failure is one
`grok login --device-auth` re-seed, with per-box plain logins always
available as the fallback shape.
