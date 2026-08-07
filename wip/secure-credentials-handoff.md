# secure-credentials -- handoff / continuation notes

**Read this first after a compact.** It captures the current state and the
remaining work in plain correctness/robustness terms. The full design brief is
`wip/secure-credentials.md` (v13); this file is the orientation layer on top
of it.

## What the feature is

byre lets a user declare named project credentials. The values are stored
encrypted at rest in the host-side project store and only decrypted at launch,
after an explicit unlock. Inside the box the values are handed to the agent as
environment variables or files -- the agent using them is the whole point.

The one property the feature provides: **byre never writes a value in plaintext
anywhere durable.** On disk the values are age-encrypted; at launch they are
decrypted host-side and piped into a per-session in-memory filesystem (tmpfs)
that empties when the box stops. byre writes no plaintext to any host file,
image layer, engine-visible config value, or persistent volume. Everything else
is degrade-never-block behaviour and legibility (clear reporting of what
happened).

## The big recent change: the v12 sweep

Versions v1-v11 accreted a large apparatus of runtime checks -- inspecting the
created container's mounts and entrypoint, verifying the launcher's interpreter
and the libraries it loads, scanning run arguments, and more. **We deleted all
of it in v12.** The reasoning, and it was confirmed correct by three
independent reviews:

- Those checks tried to keep the in-box plaintext away from in-box code. But
  the agent is handed the credentials by design, so checking the in-box
  environment only guards the values from the party that is meant to have
  them -- wasted machinery.
- The encrypted store is reachable from inside a box only under `--self-edit`,
  which already bind-mounts the project store read-write and prints its own
  prominent warning. In-box modification of the store is therefore an
  already-disclosed, opt-in mode, not something the credential feature needs to
  re-cover.
- A user placing a mount over byre's paths, or editing their own store, is a
  configuration mistake -- covered by byre's existing managed-path disclosure
  (ADR 0052), not by a runtime gate. byre's standing doctrine is that the
  design accounts for the box's own agent, never the user.

Net effect: v12 is about half the size of v11. What remains is the vault
format, the unlock flow, the delivery mechanism, and honest disclosure of what
is and isn't covered.

## Three independent design reviews confirmed the sweep (2026-08-07)

Codex, grok, and a Fable subagent each reviewed v12 against the narrowed scope
and were asked specifically whether the deletion went far enough. All three:
yes, deep enough, do not restore the removed machinery. They converged on:

- Keep the one remaining accident guard -- a project id and the name stamped
  inside each encrypted entry -- so a whole-vault copy between projects, or a
  wrong-project restore, produces a loud mismatch instead of silently
  delivering the wrong value. This is an accident catch, not a defence, and is
  labelled as such.
- Soften leftover wording that reads as an active opponent (e.g. "corrupt
  header", not "hostile header"); the size/work-factor bound it describes is a
  liveness guard and stays.
- A short list of correctness / robustness / honesty pins for the next
  revision (below).

Review logs: `.byre-devlog/secure-credentials/sweep-review-{codex,grok}.log`
plus the Fable notes captured in the diary.

## v13 worklist (small, bounded -- no architecture, no re-accretion)

**STATUS: FOLDED into the brief as v13 (2026-08-07), plus the vocabulary
pass. The list below is kept as the record of what v13 pinned and the choices
made: (1) scrypt unwrap moved to the prompt, pre-lock; (2) export map =
non-secret manifest on the delivery stream, `BYRE_CRED_EXPECT` = create-time
wait/export flag set only when a delivery is in flight; (3) creation stages
identity+index in a temp dir, renamed into place as one step; (4) byte caps
on identity/index reads; (5) wrong passphrase re-prompts, three attempts,
Enter skips; (6-8) contract/disclosure/status wording as listed. The v13
review round (codex + grok + Fable, fresh, corrected frame, 2026-08-07) came
back: no re-accretion anywhere, all eight pins judged folded; grok fully
ratification-ready; codex + Fable wording findings folded (at-rest claim
reworded to one plaintext filesystem PLACE, lock naming unified on the
GLOSSARY "setup lock", BYRE_CRED_EXPECT lifecycle stated without
contradiction). Logs: scratchpad v13-review-{codex,grok}.log +
.byre-devlog/secure-credentials/v13-review-*.**

RESOLVED by Pete (2026-08-07, "we're ant-fucking the unlock status"): no
ceremony. The unlock outcome is a launch-time fact recorded with that box's
launch; status shows it and says NOTHING about live in-box state -- no
live-state field, so the unknown-vs-cleared question does not exist.

Implementation-time pins carried forward from the round (one-liners, not
brief material): env-kind values stored under the same
`/run/byre/credentials/<name>` convention; the manifest's on-disk name;
launcher wait bound >= host inject deadline + start skew; whether the tmpfs
mounts on non-credential launches; the `.done` check's position relative to
the env.d loop. Plus one optional legibility nicety (grok, twice now): a
launch notice tying an ARG_MAX exec failure to credential size. And the new
ADR should cite ADR 0007's boundary in a sentence (project credentials are
not agent-login seeding).

Correctness / liveness:

1. Move the scrypt identity-unwrap to the pre-lock prompt, not under the store
   lock. Holding the shared project lock across the expensive unwrap stalls
   sibling worktrees for an authentication cost; only the cheap per-entry
   decrypts belong under the lock. (Flagged independently by two reviewers.)
2. Pin the export-map channel. The launcher needs each value's kind and target
   variable; carry that as non-secret launch protocol (create-time chassis
   env, a declaration snapshot, or a manifest on the delivery stream -- pick
   one). Pin `BYRE_CRED_EXPECT` as a wait/export protocol flag only, with no
   verification meaning left over from v11.
3. Make vault creation recoverable if interrupted. The identity file is written
   first with O_EXCL, then the index; a stop in between currently wedges. Write
   both as one recoverable step, or recognize the identity-only state and
   finish it at the next unlock.
4. Add modest size caps on the identity and index reads (entries already have
   one) so a corrupt or oversized file degrades to a clean notice instead of
   consuming unbounded memory before launch.
5. Pin the wrong-passphrase policy: re-prompt (with a bound) or one-shot skip.
   Currently unstated.

At-rest surface / honesty of the disclosures:

6. Make "a value never arrives as a command-line argument" a contract line, not
   a deferred-UI detail. A positional CLI form would leave plaintext in shell
   history and the process list; the editor's masked entry is the intended
   path.
7. Disclosure upgrades (all wording, no new machinery):
   - the index file's recipient and the credential names/kinds are not
     themselves encrypted -- off-box disk access can see that credentials exist
     and their public recipient;
   - a durable mount shadowing the session tmpfs could re-surface delivered
     bytes on a bare `docker restart` without a fresh unlock;
   - the precise claim is "one plaintext filesystem artifact" -- transient
     process/pipe memory is separate and already noted under swap/core
     residency;
   - the passphrase entry is itself the per-launch consent act, not merely
     authentication;
   - one sentence stating unlock covers the named set and the delivered bytes
     are those present at decrypt time (no frozen pre-prompt snapshot, and
     deliberately no return of the removed per-entry hashing).
8. Status honesty after restart: don't report live delivery state the design no
   longer probes. Report the last host-side unlock and mark live state
   unknown/cleared.

Vocabulary pass: soften the remaining words in the brief that presume an active
opponent to their correctness equivalents ("corrupt"/"unexpected"/"another
writer"), so the brief reads as the correctness feature it is.

## Pending Pete (after v13)

- Ratify v12 + v13.
- UI design session -- the editor Credentials screen (masked staged entry,
  per-name value-state cell) -- plus the three open forks: prompt habituation
  (fork 4), naming (fork 7), missing-value semantics (fork 8).
- The doctrine unit that ships with the feature: a new ADR plus amendments to
  the existing index (listed in the brief's "Doctrine unit" section).
- Implementation, four tree seams: `Runner.Create` returns the container id; a
  `RunParams` tmpfs field; a bounded exec-stdin delivery seam; a small baked
  in-box receiver that reads the delivery stream and writes the tmpfs.

## Files

- `wip/secure-credentials.md` -- the design brief, currently v13 (pins +
  vocabulary pass folded).
- `wip/secure-credentials-handoff.md` -- this file.
- `.byre-devlog/secure-credentials/` -- all review logs (the eleven build-up
  rounds plus the sweep review) and per-round notes.
- `.byre-devlog/DIARY.md` -- session-by-session narrative; the two most recent
  entries cover the sweep and the confirming reviews.

## OVERNIGHT BUILD (2026-08-07, Pete's dispatch: "build as much of it as you
## can overnight")

The core feature is IMPLEMENTED on branch secure-credentials (11 commits,
458c0ea4..424f4af0), reviewed to clean by codex + grok (independent, then
alternating re-check rounds; logs in .byre-devlog/secure-credentials/impl-*),
unit suite green, engine-side byre-inttest green against the final commit
(four runs total; the first caught a tuitest Down-count desync, fixed).

Shipped: internal/credentials (vault: age at rest, recoverable creation,
rekey with the replaced-vault guard, bounded reads); [[credentials]]
declarations on the named-decl rails (all growth guards); runner seams
(Create returns ID, TmpfsMount, ExecInputBounded); the baked receiver +
launcher fail-open export step; the develop launch flow (pre-lock masked
prompt, under-lock read-once decrypt, concurrent inject with
honesty-by-measurement delivery reporting); the `byre credentials` noun
(init/declare/undeclare/set/unset/rekey/list; values never via argv);
legibility (status rows + --data, exposure tally segment, preset review
line, /run/byre in the 0052 managed-path register, read-only editor row).

DEFERRED, awaiting Pete (unchanged gates):
- The editor Credentials screen (masked staged entry) -- the UI design
  session; the read-only GRANTS row keeps the key reachable meanwhile.
- The doctrine unit -- wip/secure-credentials-adr-draft.md holds the new
  ADR draft; NOTE: docs/adr/0026 is factually stale the moment this merges
  ("host values cross only via env_from_host") -- the amendment is drafted
  in the ADR draft's consequences list.
- Forks 4/7/8, ratification of v12+v13 and of the overnight implementation.

Implementation decisions taken overnight (flag any to revisit):
- Export manifest rides the delivery stream (base64-framed lines);
  BYRE_CRED_EXPECT/DIR/WAIT registered as launch-claim chassis knobs.
- The launch record carries credential_unlock + per-name decrypt outcomes
  with "scheduled" for successes (never "delivered": the record is
  immutable and pre-start; the inject's delivered/not-delivered is stderr
  reporting, with a delivered-late hedge when the inject provably missed
  the launcher's window -- honesty by measurement).
- Undeclared-name `set` keeps the trailing-newline courtesy; declared
  file-kind values are byte-exact.
- CLI `declare/undeclare` verbs exist so the feature is usable end-to-end
  while the editor row is read-only.

## One-line status

DESIGN: v13 settled and review-verified. CODE: built overnight and
review-clean; awaiting Pete's ratification, the UI session, and the
doctrine-unit filing.
