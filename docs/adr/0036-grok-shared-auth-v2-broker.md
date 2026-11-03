# grok-shared-auth v2: the auth broker

grok-shared-auth is **rebuilt** (2026-07-16) as an auth broker riding
`GROK_AUTH_PROVIDER_COMMAND` -- grok's own external-auth seam -- replacing
the retired v1 symlink design (ADR 0023). Every pre-build gate was answered
by source inspection of the published Grok CLI tree plus live probes
against the in-box 0.2.101 binary, without risking live credentials. The
gate record and vendor evidence live in
`docs/AGENT-CREDENTIAL-MECHANICS.md` (xAI Grok CLI section).

## The mechanism

A ~150-line bash broker (`/etc/byre/grok-auth-broker`), shipped by the
companion skill and named in every opted-in box's
`GROK_AUTH_PROVIDER_COMMAND`. No resident process: grok runs it when it
needs a credential and waits. Per invocation, under one `flock` in the
machine-scoped identity volume: read the shared OAuth pair; if the access
token is fresh, emit it; if stale, perform the standard OIDC
`refresh_token` grant (endpoint from issuer discovery, cached), persist via
temp+rename, then emit. Kernel flocks are atomic across containers on one
shared volume, so exactly one process machine-wide ever spends the
single-use refresh token -- the correlated cold start serializes like any
other contention.

The decisive source finding: with the provider command set, grok's
`build_refresher` **never constructs its own OIDC refresher** -- the
external command is the refresh authority for every token type, on expiry
and on 401 recovery alike. No box can rotate the chain behind the
broker's back, which is the failure that killed v1.

The broker always emits the JSON form with `expires_in`: grok then tracks
the server-authoritative expiry, where a bare token would fall back to a
30-day assumed lifetime. It mirrors grok's own error classification
(`invalid_grant` and `invalid_client` terminal, everything else retried).
A transient refresh failure degrades to emitting the cached access token
-- but only when grok did not flag its own copy dead (`GROK_AUTH_EXPIRED`
covers 401 rejections, where re-emitting the same token would loop) and
the cache clears grok's 300s early-invalidation buffer (a smaller token is
instantly re-expired and thrashes); otherwise it fails closed and grok's
own 300s failure TTL paces the retry while its in-memory token carries the
session.

**Seeding** goes through grok itself: the firstrun hook runs
`GROK_AUTH_PATH=<store> grok login --device-auth` (the env var relocates
grok's whole credential subsystem -- reads, writes, login persistence,
lock), so the shared store is a grok-native `auth.json` and no
copy/translate hygiene exists to get wrong. A real per-box login found at
launch (refresh token present -- broker-issued tokens never carry one) is
promoted to the machine store and dropped locally, announced. Without that
promotion a leftover per-box login would still be used as-is until it
expires, delaying broker adoption by at most one access-token lifetime
(~6h). A dead chain (`invalid_grant`) is moved aside by the broker with
re-seed instructions -- which also self-heals the v1 volume's orphaned
credential, since the rebuild reuses the same `grok-identity` volume and
store path.

## Constraints from the seam

Source facts the broker is built against that the mechanics record does
not carry: 401 recovery runs `RefreshReason::ServerRejected` ->
`refresh_chain` -> `ExternalBinaryRefresher`, and the command is also
re-run proactively when `expires_in` was provided. The refresh grant is a
form-encoded POST with `client_id` and optional
`principal_type`/`principal_id` -- no secret, no scope -- to the token
endpoint from `{issuer}/.well-known/openid-configuration`.

Refresh-path invocations are killed at **5 seconds** and their stderr is
swallowed. The broker budgets every wait to fit:

- The lock wait is 2.6s, longer than a winner's whole 2.2s POST budget,
  so a lock loser outwaits the winner instead of failing into grok's
  ~300s retry TTL.
- The refresh path does no discovery; the endpoint cache is pre-warmed at
  seeding. A kill can therefore never land mid-POST and strand a spent
  refresh token.
- Diagnosis goes to `broker.log` beside the store, because stderr is
  invisible on that path.

## Alternatives rejected

- **Design 2 (watcher + jitter)**: its accepted race has a confirmed
  maximal price (a reused refresh token triggers `invalid_grant` and
  token-family revocation), and its premise (grok lacks cross-process
  refresh coordination) is false -- grok already flocks and adopts
  sibling tokens; what it cannot do is cross-**container** coordination
  (next item).
- **"Design 0" (share a real credential file via `GROK_AUTH_PATH`, no
  broker)**: grok's lock is a real flock held across the refresh, but its
  staleness probe is `kill(pid, 0)`, so a holder in another PID namespace
  reads as dead, a contending box **unlinks a live lock near-instantly**
  and both spend the refresh token. grok's own locking can never
  serialize boxes; only a lock grok doesn't interpret (the broker's) can.
- **`XAI_API_KEY`**: ruled out on cost (ADR 0023; ~50x the subscription
  at coding-agent volumes).

## The remaining gate, and the vouch

Everything above is source plus in-box probes; the one thing that cannot
be verified that way is a **live broker refresh accepted end-to-end by
the backend** (a real seeded store rolling over a ~6h expiry in a real
box). Until that field gate runs, the skill ships with `companion_for =
"grok"` -- enabled by hand, paired in the picker -- and NOT
`shared_auth_for` (the ADR 0025 onboarding offer), because declaring that
key is vouching the mechanism. That is the v1 lesson applied: the vouch
follows the field gate, never precedes it. Flip the key when the gate
passes.

## Failure and recovery

On the mid-session refresh path grok swallows the provider's stderr, so
what the user sees there is grok's own auth error -- the broker's full
diagnosis (including re-seed instructions) lives in `broker.log` beside
the store. The worst case of any failure is one `grok login
--device-auth` re-seed, with per-box plain logins always available as the
fallback shape.
