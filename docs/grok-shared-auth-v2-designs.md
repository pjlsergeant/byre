# grok-shared-auth v2: candidate designs (2026-07-11)

Status: **PARKED** (2026-07-12, ADR 0023) -- ideas for a future build, not
scheduled work. v1 was retired after its field failure; boxes use per-box
grok logins meanwhile. Whoever picks this up: run each design's gates
BEFORE building, and get second opinions first -- the document is written
neutrally for exactly that. Two candidate designs -- **Design 1: auth
broker** (`GROK_AUTH_PROVIDER_COMMAND`) and **Design 2: watcher + jitter**.
Bull and bear cases for each; deliberately no recommendation. Ruled-out and
shelved approaches are listed at the end so reviewers don't re-propose
them.

Shared caveat that applies to both designs equally: each is machinery whose
entire payoff is avoiding an occasional `grok login --device-auth`. Whether
either clears that proportionality bar is part of what reviewers should
judge.

## Background (what happened to v1)

byre runs each project's AI coding agent in a throwaway per-project
container (a "box"). grok-shared-auth v1 tried to share one Grok
subscription login across all boxes on a machine, copying the pattern that
works for Codex: each box's `$GROK_HOME/auth.json` is a symlink into a
machine-scoped identity volume, so all boxes share one credential file. It
failed in the field (2026-07-10, twice in one day):

- grok's credential is an OAuth pair: access token (~6h) + refresh token
  (single-use -- each refresh invalidates the old one).
- On refresh, grok (evidently) writes via temp+rename, which **replaces the
  symlink with a private local file**. The rotated pair lands in that box's
  private volume; the shared copy freezes (observed: shared file mtime stuck
  at login time while boxes kept running).
- The stale shared pair becomes permanently rejected ("ServerRejected",
  `refresh_chain short-circuit on permanent failure` in grok's event log,
  `unified.jsonl`).
- v1's launch hook re-asserts the symlink every launch (the
  "clobber-heal"), deleting the box's *working* forked credential and
  replacing it with the dead shared one.

The plain grok skill (per-box file logins, no sharing) works correctly. The
question is only how to share one subscription login across boxes.

## The core difficulty

grok acts first and is observed after: it decides to refresh, spends the
single-use refresh token, and writes the result, without asking anything
else for permission. Any mechanism that merely *observes* grok therefore
has a window in which a second box can spend the same refresh token. The
two designs differ precisely in their relationship to this fact: Design 1
interposes on the token request itself (grok asks and waits), Design 2
observes and accepts a residual window.

The dangerous scenario has a specific shape, the **correlated cold start**:
every box idle long enough for the shared token to go stale (e.g.
overnight), then several boxes' grok started within the same second -- for
instance a script fanning a task out across worktree boxes. All of them see
"expired, refresh now" simultaneously. Staggering refresh *schedules* does
not help here, because none of the refreshes is schedule-driven.

## Established facts

Verified in this box, or in the vendor docs shipped inside the CLI install
(0.2.93). Note the shipped user guide (`~/.grok/docs/user-guide/`) is the
*only* source for several load-bearing features -- it is vendor-authored
but unindexed anywhere public, and the public README is known to lag it.

- Boxes are throwaway containers (`docker run --rm`), fresh each session;
  volumes persist. All boxes live on ONE machine / one kernel; the shared
  identity volume is a bind-mounted named volume, so `flock` and inotify
  work across boxes on it.
- Worktree sessions deliberately share the project's volumes (byre ADR
  0009), so two boxes CAN mount the same grok state volume concurrently,
  and scripted parallel launches of worktree sessions -- the trigger for
  the correlated cold start above -- are a designed byre workflow.
- Running grok sessions do NOT watch `auth.json`; they re-read it lazily
  when their in-memory access token looks expired (observed: "auth
  recovery: disk token expired" in our logs). Rotation kills the old
  *refresh* token, not in-flight access tokens -- which is why two
  terminals on one machine coexist.
- When grok cannot authenticate in a headless run (`grok -p`), it falls
  through to the interactive login flow and **hangs on a device prompt**
  (field-observed 2026-07-10; the device code landed in a debug file
  nobody was watching). Headless auth failure is a hang, not an error.
- `GROK_AUTH_EARLY_INVALIDATION_SECS` (default 300) is a documented per-box
  env knob: treat the token as expiring N seconds early.
- `auth.json.lock` observed in this box: persists after grok exits, content
  is `PID:timestamp` (`23185:1783753745`) -- the classic create-exclusively
  / steal-if-stale lockfile pattern, NOT an OS-level lock on a persistent
  file. PID-based staleness is meaningless across containers (separate PID
  namespaces), so **grok's own lock cannot provide cross-box mutual
  exclusion**, no matter how it's shared.
- `GROK_AUTH_PROVIDER_COMMAND` (shipped user guide, 02-authentication.md +
  05-configuration.md): delegate auth to an external command. grok runs it
  via `sh -c` when it needs a credential; stderr is surfaced to the user
  (login URLs etc.); **stdout is captured and stored as the access token**;
  exit 0 = success, non-zero = grok falls back to interactive login.
  Pitched at enterprise SSO, but nothing in the contract is SSO-specific.
- Credentials without a server-provided expiry fall back to a 30-day
  lifetime (shipped user guide).
- The OIDC client id is visible in `auth.json`'s scope key
  (`https://auth.x.ai::<client-id>`), and the refresh is a standard OIDC
  `refresh_token` grant -- the exact endpoint/params are unpublished but
  observable (strace/proxy) from a real refresh.

## Shared assumption (Pete's directive)

Refresh-token **reuse revokes the whole chain** (standard OAuth breach
detection; consistent with, but not isolated by, the field evidence). Both
designs are evaluated under this assumption -- it sets the cost of Design
2's accepted race and motivates Design 1's seeding hygiene.

It is cheaply falsifiable: on a scratch chain, refresh once, then replay
the spent refresh token and observe whether the chain is revoked or only
that attempt fails. Both designs' gate lists include this experiment by
reference; if the assumption fails, Design 2's worst case softens
considerably.

---

# Design 1: auth broker (`GROK_AUTH_PROVIDER_COMMAND`)

## Mechanism

A small static Go binary ("the broker"), shipped by the skill, set as every
box's `GROK_AUTH_PROVIDER_COMMAND`. **No resident process** -- it runs only
when grok invokes it, and grok waits for it. Per invocation:

1. Take `flock` on a lock file in the shared identity volume. This is a
   real kernel lock -- atomic across containers on the same volume,
   auto-released on process death, no PID interpretation anywhere.
2. Under the lock, read the shared credential pair from the identity
   volume (a plain file the broker owns; no symlinks into `$GROK_HOME`).
3. If the access token is fresh (with margin): print it to stdout, exit 0.
4. If stale: perform the OIDC refresh-token grant ourselves (one HTTP
   POST), write the new pair to the shared file via atomic temp+rename
   (the broker is the only writer), print the new access token, exit 0.

Lock losers block for the few hundred ms a refresh takes, wake, find a
fresh pair, and never refresh. Exactly one process ever spends a refresh
token, machine-wide, under a real mutex.

**Seeding:** one `grok login --device-auth` in any box, then the skill's
firstrun hook moves the resulting pair into the broker's shared file and
removes the box-local `auth.json` (a box-local chain left rotating would
invalidate the broker's pair, since rotation is single-use).

## What this design closes, and what it opens

By interposing on the token request (see The core difficulty), the
simultaneous-refresh race is closed rather than made rare: refresh is
serialized by a kernel lock because every consumer is forced through the
broker, and a correlated cold start serializes on the flock like any other
contention. There is no symlink for grok's temp+rename to destroy (the
broker owns all writes to the shared file; grok's own `auth.json` holds at
most a disposable cached token), and no resident process to keep alive.

What it opens in exchange: byre takes over the client side of grok's OAuth
refresh -- protocol, endpoint, and failure handling become our code against
an unpublished interface (see gates 1, 3, 4 and the bear case).

## Gates (run before building)

1. **Re-invocation semantics -- the design-killer if wrong.** When the
   broker-issued access token dies (~6h), does grok re-run the provider
   command? Or does it consider the stored token good for the 30-day
   default lifetime and only face reality on a 401 -- and does that
   auth-recovery path re-invoke the provider, or punt to interactive
   login? ("auth recovery" strings in our logs prove a recovery path
   exists; whether it reaches the provider command is the experiment.)
2. **Precedence vs a stored session login.** If a leftover per-box
   `auth.json` outranks the provider command, the skill needs move-aside
   hygiene for stale local logins (as our Claude token-sharing skill,
   claude-shared-auth, already does for the analogous case).
3. **The refresh grant itself.** Confirm endpoint/params by observing a
   real refresh, and that a broker-refreshed access token is accepted by
   the backend identically to a grok-refreshed one.
4. **Failure-path behavior headless.** Provider exits non-zero => grok
   falls back to interactive login, which headless is the hang documented
   in Established facts. Verify what a *transient* broker failure
   (identity volume unavailable, refresh endpoint down) looks like in
   `grok -p`, and whether the broker can degrade better (e.g. emit the
   stale-but-maybe-alive cached token rather than failing).
5. **Reuse consequence** -- the shared-assumption replay experiment (see
   Shared assumption); for this design it calibrates how urgently seeding
   hygiene (gate 2) must be enforced.

## Failure modes

| Failure | Consequence | Recovery |
|---|---|---|
| Refresh chain dies server-side (revocation, password change) | Broker can't refresh; all boxes' grok fails together | One device-auth re-seed |
| Broker bug / endpoint change | ALL boxes' grok breaks at once (single point of failure) | Fix broker; interim: per-box plain logins still work |
| Broker exits non-zero in a headless run | grok falls back to interactive login = headless hang (gate 4 probes mitigations) | Kill + re-seed; mitigation TBD at gate 4 |
| Stray box-local chain survives seeding | Its rotation invalidates the shared pair | Firstrun hygiene (gate 2); re-seed |
| Identity volume missing/unmounted | Broker fails; headless, same as the row above (hang, gate 4) | Restore volume or re-seed |

## Bull case

- The only candidate that **eliminates** the refresh race rather than
  shrinking it, and the only one with no resident process. Smallest
  moving-part count of anything on the table.
- Uses grok's vendor-sanctioned seam for exactly this job ("delegate auth
  to an external command"), rather than fighting the CLI's file handling
  from outside.
- Failure modes are loud and immediate (grok visibly can't get a token) --
  no silent staleness accumulating in the background.
- The binary is small and almost entirely testable without grok: flock,
  file I/O, one HTTP POST against a mockable endpoint.
- Keeps subscription billing -- the point of the whole exercise.

## Bear case

- Stands on **undocumented re-invocation semantics** (gate 1). If grok
  treats a provider token as good for 30 days and its 401-recovery doesn't
  re-invoke the provider, the design dies at the gate.
- **We take ownership of the OAuth refresh protocol**: an unpublished
  endpoint, discovered by observation, changeable in any release, and
  arguably not ours to speak to (ToS-gray -- though it is the user's own
  credential and the provider seam explicitly invites custom auth).
- Concentrates risk: one broker bug or one vendor-side change bricks grok
  in **every** box simultaneously -- v1 at least failed box-by-box. (The
  blast radius is bounded: plain per-box logins remain available.)
- The feature itself is gray documentation -- shipped-user-guide only, no
  public footprint, maturity and support status unknown. It could be
  quietly reworked or dropped.
- The failure path in headless runs is the documented hang unless gate 4
  finds a mitigation.
- Seeding requires discipline the skill must enforce, not assume: any
  surviving box-local chain poisons the shared pair.
- It is a whole subsystem (OAuth client, locking, seeding hygiene) built
  to avoid an occasional re-login; the shared proportionality caveat at
  the top applies in full.

---

# Design 2: watcher + jitter

## Mechanism

Keep v1's symlink, add two things: a per-box watcher process that ships
refresh results back to the shared volume, and per-box staggering of
refresh timing. Four parts:

1. **Symlink** (as v1): `$GROK_HOME/auth.json` -> shared identity volume.
   First login in any box writes through the dangling link; the credential
   lands machine-wide.

2. **Watcher: swipe + relink.** A small static Go binary per box, watching
   `$GROK_HOME` via inotify. When `auth.json` becomes a regular file (grok
   refreshed and its temp+rename replaced the symlink), atomically ship
   the fresh pair into the shared volume (temp+rename, newest-wins by
   content comparison) and re-assert the symlink. All operations
   idempotent, so N watchers (the worktree case -- two boxes on one grok
   volume) doing the same work concurrently are harmless. Idempotency is
   the concurrency strategy by necessity, not just taste: a pidfile/pgrep
   dedup guard cannot work across containers, for the same PID-namespace
   reason grok's own lock can't (see Established facts).

3. **Startup reconcile.** Before watching: if `auth.json` is a regular
   file (a fork left while the watcher was dead or the box was off), ship
   it / adopt the shared pair, newest wins, then relink. This REPLACES
   v1's clobber-heal: a returning box donates its fork instead of losing
   it.

4. **Jitter.** Each box gets a distinct `GROK_AUTH_EARLY_INVALIDATION_SECS`
   slot (slot assignment by box name, enforcing minimum spacing -- a raw
   hash spread would not guarantee separation), spread across ~300-3600s.
   Token lifetime is ~21,600s, so even the widest setting only costs a
   refresh every ~5h instead of ~6h. This staggers clock-driven refreshes
   so concurrently-running boxes rarely decide to refresh in the same
   window; the widest-window active box becomes the de facto designated
   refresher.

Launch hook -- containers are fresh per session, so there is nothing to
dedup against and the hook is one line:

    setsid grok-auth-watcher >>"$GROK_HOME/watcher.log" 2>&1 &

The log doubles as the liveness / what-shipped audit trail.

## What this design avoids, and what it accepts

By never touching grok's OAuth flow (see The core difficulty), everything
protocol-shaped stays grok's problem: byre never speaks to xAI's endpoints,
never parses or mints tokens, and only moves a file the CLI already wrote.
The interface to the closed binary is as small as it can be while still
sharing the credential.

What it accepts in exchange: grok still acts first, so two boxes can spend
the same single-use refresh token when both decide to refresh within the
same window (~one refresh round-trip, 0.2-2s). Jitter covers the
clock-driven case -- staggered thresholds mean two long-running boxes
essentially never trip together, and hand-started sessions land
seconds-to-minutes apart, outside the window. The **correlated cold start
(see The core difficulty) is NOT covered, by choice**: this design accepts
that exposure on the bet that scripted simultaneous grok cold-starts
against a stale token are rare in practice.

Under the shared assumption (reuse = revocation), losing the race means a
machine-wide logout. Recovery: one `grok login --device-auth` anywhere
re-seeds the machine (writes through the shared link). Self-announcing;
nothing silently corrupted.

## Gates (run before building)

1. **Write pattern.** Confirm temp+rename directly via strace rather than
   inferring it from field evidence. If grok actually writes in place,
   parts 2-3 shrink to a startup-reconcile script and this design gets
   substantially simpler.
2. **Reuse consequence** -- the shared-assumption replay experiment (see
   Shared assumption); for this design it prices the accepted race: if
   reuse merely fails one attempt, the race becomes nearly free.

## Failure modes

| Failure | Consequence | Recovery |
|---|---|---|
| Watcher dies mid-session | Box forks privately; shared copy goes stale; other boxes eventually hit "please log in" | Next launch's startup-reconcile ships the fork; or one device-auth re-login |
| Refresh race lost (cold-start fan-out) | Chain revoked; machine-wide logout | One device-auth re-login |
| grok changes its write pattern | Swipe stops firing; degrades toward v1 behavior; detection is passive (watcher log goes quiet) | Re-gate and adapt the watcher |
| Shipped-older-over-newer / swipe races | Prevented by content comparison + atomic rename (local-filesystem races, closeable in implementation) | n/a |

## Bull case

- Grok keeps full ownership of its own OAuth flow; byre's interface to the
  closed binary is minimal (see "What this design avoids").
- Depends on no gray-documentation features: inotify, rename, and one
  documented env knob.
- Degrades box-by-box, not machine-wide: a watcher failure hurts one box's
  sharing, and startup-reconcile self-heals most of it at next launch.
- With one exception (a vendor write-pattern change, whose detection is
  passive -- see the failure table), failures are loud and recover with
  one command; no silent corruption anywhere in the table.
- Startup-reconcile is strictly better than v1's clobber-heal even judged
  alone.
- Keeps subscription billing -- the point of the whole exercise.

## Bear case

- The refresh race is **reduced, never closed** -- the design's ceiling is
  "rare," and under the shared assumption every loss is a machine-wide
  logout.
- The uncovered case (correlated cold-start fan-out) is not exotic: byre's
  worktree workflow is *designed* for scripted parallel sessions, and
  "idle overnight, fan out in the morning" is a natural usage shape. The
  bet that it's rare is exactly that -- a bet, paid for in occasional
  re-logins if wrong.
- byre grows its first resident in-box process: a new category of moving
  part (liveness, logging, an orphaned-fork window while dead) in a system
  whose architecture is otherwise "hooks run and exit."
- Rests on an unverified write-pattern assumption (gate 1) about a closed
  binary that can change it in any release -- detectable, but by field
  degradation.
- It is a resident process plus shipping machinery built to avoid an
  occasional re-login; the shared proportionality caveat at the top
  applies in full.

---

# Ruled out / shelved (so reviewers don't re-propose them)

- **Env API key** (`XAI_API_KEY`, console.x.ai): **RULED OUT on cost
  (Pete, 2026-07-11).** Technically clean -- no daemon, no races, no
  rotation -- but it moves inference off the $30/month subscription onto
  xAI's separate pay-per-token API billing, and at coding-agent volumes
  that is a ~$1,500/month bill for the same usage, a ~50x cost increase.
  The economics disqualify it regardless of engineering merit. (It also
  isn't drop-in: vendor docs say a stored session login takes PRECEDENCE
  over the env key, so it would need the same stale-local-login hygiene
  noted at Design 1 gate 2.)
- **Cross-box lock relay** (watchers mirror grok's local lock into a
  shared flock and plant local locks in other boxes): shelved. It shrinks
  Design 2's race window from ~0.2-2s to ~5ms and is the known bolt-on if
  field data shows cold-start collisions are common -- but it cannot reach
  zero (grok never waits for permission), rests on two further unverified
  behaviors (whether grok's lock brackets the network refresh or only the
  file write; wait-vs-steal etiquette on a held lock), and adds a bespoke
  distributed protocol to Design 2's parts list.
- **Share the whole `GROK_HOME` volume machine-wide**: gives all boxes one
  real credential file with no watcher -- but cross-box refresh
  serialization would depend on grok's own PID-based lock, which is
  cross-namespace-broken (see Established facts), so the race remains; and
  it couples every project through grok's sessions/config/logs/
  worktrees.db.
- **Per-box logins** (status quo, plain grok skill): zero machinery, works
  today; the cost it fails to remove is one device-auth per new box or
  rebuild, which is the problem this whole document exists to solve.
