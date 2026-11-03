# Adding (or source-hardening) an agent CLI

A playbook: take an agent CLI whose source we have, answer the
credential-mechanics gates from that source, and build or repair its
skills on the evidence. This doc is METHOD; per-agent facts, worked
answers and gate status live in `docs/AGENT-CREDENTIAL-MECHANICS.md`
and `TODO.md` -- don't duplicate status here, it rots.

The one-sentence version: **read the source before building, distrust
every "evidently" in the existing record, and let the vendor's own
machinery do the work wherever it has a sanctioned seam.**

## Before you start

- **Pin the version story first.** The source tree's version literals
  may not match the shipped binary at all -- grok's crates said
  `0.1.220-alpha.4`/`0.2.0-dev` while the binary said `0.2.101`,
  because the real version is env-injected at build. The reliable
  alignment signal is **grepping the source for literal log strings
  observed in the field** (all three strings from grok's 0.2.93 field
  failure existed verbatim). Record the in-box binary version
  (`<agent> --version`) next to every claim; behavior is
  version-dependent (grok's headless hang was vendor-fixed between
  0.2.93 and 0.2.101, which dissolved a whole TODO residual).
- **Read the shipped docs inside the install**, not just the public
  README. Grok's `~/.grok/docs/user-guide/` documented the auth-provider
  seam a version before the public README knew it existed.
- **Re-read the agent's section of AGENT-CREDENTIAL-MECHANICS** and list
  every claim marked observed/evidently/unverified. Those are your gate
  questions. Keep the empirical history when you upgrade the doc -- mark
  claims SOURCE-CONFIRMED/-CORRECTED with a date rather than rewriting
  history; the field record is what the gates were run against.

## Running the source pass

Fan out parallel read-only research agents, one per question cluster,
and **demand file:line citations with verbatim excerpts** -- then
spot-verify the load-bearing lines yourself before building on them.
Clusters that worked for grok: (1) the external-auth/provider seam and
its exact contract, (2) credential file write/lock/reload mechanics,
(3) the refresh protocol and failure classification, (4) follow-ups the
first round surfaces (grok's `GROK_AUTH_PATH` was a one-line mention
that changed the seeding design).

The gate checklist -- what to answer for ANY agent CLI. The Grok section
of AGENT-CREDENTIAL-MECHANICS answers every item from source and is the
worked example:

- **Write pattern**: temp+rename or in-place? (Decides whether a
  symlink survives a credential write.) Check for divergent fallback
  paths -- one error path can write in place and follow symlinks while
  the normal path replaces them.
- **Rotation semantics**: token lifetimes; is the refresh token
  single-use; what does reuse cost (vendor comments may state it
  outright, turning an "experiment needed" into a lookup); which error
  codes are terminal vs transient, and whether a terminal verdict is
  sticky.
- **Locking**: real kernel flock or PID-file theater? Held across the
  IdP call or only the file write? And the trap that killed two designs:
  **any staleness/steal probe built on `kill(pid, 0)` is
  cross-container-broken** -- a live holder in another PID namespace
  reads as ESRCH and gets its lock stolen. A single kernel's flocks DO
  work across boxes on a shared volume; it's the vendor's own steal
  logic that breaks them. Only a lock the vendor binary doesn't
  interpret can serialize boxes.
- **Env overrides**: anything that relocates the credential file or
  delegates auth. For each one, trace EVERY consumer: an override the
  login flow honors and the file watcher ignores is fit for seeding and
  unfit as the sharing mechanism.
- **Headless failure shape**: hang vs fail-closed, per entrypoint --
  print mode, agent mode, `--no-browser` variants can differ within one
  binary. Cheap live probe: point the state-dir env at an empty temp
  dir and run the headless mode under `timeout` (never gamble the box's
  live credential).
- **Precedence**: stored login vs env API key vs external provider, at
  selection time AND at refresh time. An external provider can become
  the refresh authority for every token type, a stored OAuth login
  included.
- **Invocation contracts** for any seam you'll ride: timeouts per call
  path, stderr visibility, stdout parsing (bare token vs JSON), and the
  default lifetime when no expiry is provided -- always emit an explicit
  expiry.

## Design rules

- **Prefer the vendor's sanctioned seam to fighting its file handling.**
  Symlinks against a temp+rename writer lose (grok-shared-auth v1); a
  documented provider-command seam removes the race (v2). If the CLI has
  an "enterprise SSO" style delegation hook, that's probably your
  mechanism.
- **Seed through the vendor binary itself** when an override lets you
  (grok: `GROK_AUTH_PATH=<store> <agent> login`). The store stays in
  native format and there is no copy/translate hygiene to get wrong.
- **A refresh-is-needed flag covers 401s, not just expiry.** Grok sets
  `GROK_AUTH_EXPIRED=1` for both; short-circuiting it on wall-clock
  freshness re-emits a server-rejected token and 401-loops. The safe
  exception is a pair rotated seconds ago (almost always a sibling's new
  token, and the residual self-expires with the window).
- **Never emit a token under the CLI's early-invalidation buffer**
  (grok: 300s + 60s jitter). The CLI counts it as already expired and
  thrashes your seam in a refresh loop.
- **Fail closed beats degrading** more often than it feels like it
  should: the CLI's in-memory token carries live sessions to real
  expiry, and its failure TTL (grok: 300s, non-sticky) paces retries
  for free.
- **Budget every wait to the least patient caller**, and make a lock
  loser outwait the winner's whole hold (lock wait > refresh POST
  budget, sum under the kill timeout) -- otherwise a healthy store still
  costs the loser a full backoff cycle. And never let the caller's kill
  land mid-refresh-POST: a spent refresh token whose response died with
  the process is a lost chain. Persist before emitting.
- **Move a dead store aside, loudly, and let the seeding hook re-seed.**
  This doubles as migration/self-heal for whatever corpse a previous
  design left in the same volume. Branch on the `mv` -- a failed
  move-aside that still claims success sends the user the wrong way.
- **Serialize every store mutation under the same one lock** the broker
  (or equivalent) uses, publish via temp+rename, and re-check state
  after acquiring -- including in the firstrun hook. The one deliberate
  exception: never hold that lock across an interactive login; document
  the accepted residual instead.

## byre-side mechanics

- **Skill shape** for a shared-auth companion: `companion_for` pairing,
  machine-scoped identity volume, `[runtime] env` carrying the seam,
  its own `egress` for the endpoints IT talks to (even if the agent
  skill already opens them), `files` shipping firstrun hooks + support
  scripts. Hooks run via `bash "$hook"` and embedded files lose exec
  bits, so invoke support scripts as `bash /etc/byre/<name>` and never
  depend on the x bit. Ordering is glob order: `00-` prefixed hooks run
  before the agent skill's login hook.
- **Stand the agent skill's login hook down** when the companion is
  active -- an env check on the seam variable is enough, and the
  companion owns all login UX. Keep the anti-planting symlink heal
  running first regardless.
- **Vouch discipline**: ship with `companion_for`; flip to
  `shared_auth_for` (the onboarding offer, ADR 0025) only after the
  LIVE field gate -- for credential sharing that means a real box
  observed through a full token rollover. Source-verification is
  necessary, not sufficient; the vouch follows the field gate (the v1
  lesson, and both keys at once is a parse error, ADR 0034).
- **Tests to update**: the per-skill shape test,
  `TestBuiltinCompanionDeclarations` (companion map), the stub
  classification list, and self-host composition pins. Script behavior
  is testable from Go: run the hook/broker via `bash` with the
  `BYRE_IDENTITY_BASE` seam and a **fake curl on a prepended PATH**
  (drive success/4xx/timeouts by swapping its body); a fake `<agent>`
  binary that touches a marker file proves a hook did NOT invoke it.
  Skip cleanly when jq/flock are absent. Launcher tests isolate the
  host box's hooks via `BYRE_FIRSTRUN_DIR`/`BYRE_ENVD_DIR` -- if a
  launcher-driving test ever hangs ~600s, something is executing the
  real `/etc/byre` again.
- **Docs sweep is part of the unit**: upgrade the agent's
  AGENT-CREDENTIAL-MECHANICS section (dated SOURCE-CONFIRMED markers,
  history preserved), write the ADR with the full gate record, delete
  the absorbed `wip/` doc, sweep README + site + TODO + CHANGES, and
  give any superseded ADR a superseded-in-part header.
- **Review loop**: after codex is clean, get a second opinion from a
  DIFFERENT reviewer -- and if the change concerns an agent CLI we ship,
  use that CLI as the reviewer: it can live-probe the integration with
  fake stores. Expect and welcome reviewer probes; keep green YOUR job.
- **Ops**: the gated engine-side suite must actually run before
  handoff (`byre-inttest`); verify the tests RAN rather than
  skipped (count RUN/PASS lines, don't trust a fast `ok`).
