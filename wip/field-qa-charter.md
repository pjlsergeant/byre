# Field-QA pass — charter (2026-07-17, draft for Pete)

**What this is:** the plan for the first run of the "Agent field-QA pass"
(TODO, Maybe someday): an agent drives REAL byre over tmux on the
sacrificial inttest VM and reports findings with repro keystrokes.
**Report-only** — no fixes, no gate, nothing blocks on it. Findings that
matter harden into deterministic tuitest cases afterwards, each on its own
dispatch. Delete this file when the pass has run and its findings are
reported (the report itself can live here until then).

## Ground rules

- Real binary, real engine, real boxes — built from current main on the VM.
- Exploratory, not scripted: the deterministic suite already pins the happy
  paths (tuitest walks every config sub-screen; integration tests pin
  launch/volumes/deliver). This pass hunts what fixed-keystroke tests
  can't: flows in SEQUENCE, interrupted flows, resized panes, wrong input
  at prompts, leftover state between runs.
- Credentials: dummy keys for mechanics; ONE capped live Anthropic key
  (Pete, 2026-07-17, ~$5 workspace cap, revoked after the pass) for
  agent-LIVENESS checks only — box comes up into a working agent, an MCP
  tool actually calls. Runtime-only: never in config [env] (bakes into
  image layers), never committed, VM-only. No other real logins —
  gemini/grok field gates stay parked, this pass is NOT them.
- A finding = observed behavior + why it's wrong (or just suspicious) +
  exact repro keystrokes from a fresh box. Cosmetic counts if a cold user
  would stumble.

## Targets, risk-ranked (risk = recently shipped × never field-driven)

1. **Onboarding offer for opencode shared-auth — NEVER SEEN LIVE.** The
   `shared_auth_for` flip (a6340b7, yesterday's gates) put the ADR 0025
   first-run picker in front of every opencode box. Drive: fresh project,
   `agent = "opencode"`, first `byre develop` → the offer appears; try
   yes / no / save-as-default; confirm yes lands the companion in
   `byre.config` skills and NOTHING else; re-develop → no re-ask; second
   fresh project → prefilled favourite. Also: does the offer's wording
   make sense cold?
2. **Opencode MCP delivery in a live session.** `mcp = "inject"` is new.
   Declare an MCP server, launch the box, confirm the wrapper's env
   reaches the real agent process (opencode `mcp list` in the box shell)
   and `byre status` now shows DELIVERED (it showed
   declared-but-not-delivered until yesterday). Try the empty-set box too
   (no MCP declared → env absent, status quiet).
3. **Claude Skills config-UI editor (ADR 0039, merged 2 days ago).** The
   dirty-signature bug (da1168d) was caught by review, which suggests the
   area is under-driven. Add/edit/close a Claude Skill entry, quit with
   and without saving, feed it junk paths, resize mid-edit.
4. **Deliver flows.** The busy-stdin /dev/tty picker is recent. Drive
   `cmd | byre deliver` with several boxes up, picker cancel, `--box`
   override, re-deliver same filename (the -2 claim), deliver into a
   worktree box.
5. **Onboarding generally, in sequence.** Trust prompt → theme-less first
   run → firstrun hooks (codex login SKIPPED cleanly via Ctrl-C — the
   trap path) → agent launches. Then the same box again (idempotence),
   then `byre reset` and again.
6. **Worktree concurrency.** Two sessions, main + worktree, same project:
   picker/status/deliver behavior with both up; volumes shared vs
   per-box as documented.

## Explicitly out of scope

- Real-credential flows (gemini OAuth, grok rollover, codex/claude real
  logins) — parked field gates, not this pass.
- Performance/latency judgments — the VM is not representative.
- Anything requiring egress beyond the VM's own network (firewall
  deep-testing has its own gated tier).
- Fixing anything found — report only.

## Mechanics

Byre built on the VM from synced main; flows driven with the
BYRE-DEVELOPMENT.md tmux vocabulary (private socket, 100x30, respawn-pane,
send-keys/capture-pane), transcripts kept per finding so repro lines are
copy-paste. Boxes and volumes created under test projects only; identity
volumes get the LiveGate treatment (never touch a pre-existing one — on
the sacrificial VM there are none).

## Report format (goes in this file when run)

Per finding: SEVERITY (broken / confusing / cosmetic) — one-line summary —
repro keystrokes — observed vs expected — suggested hardening (tuitest
case sketch), no fix applied.

---

# REPORT — pass ran 2026-07-17 (VM, byre @ 3099ad1, opencode 1.18.3)

Targets 1–4 driven end to end; 5 partially (cold onboarding + completed
login; the Ctrl-C-skip trap path NOT driven); 6 (worktrees) not reached —
deliberate, that code changed today in a concurrent session and got its
own review + gated run. Real-key liveness spend: ~$0.06 of the $5 cap.
Key artifacts removed from the VM (qa-key file, identity + state volumes);
REVOKE THE KEY regardless.

## What just works (worth saying out loud)

The entire cold-user journey, first keystroke to working agent:
wizard → opencode pick → shared-auth offer (i/y/save-default) → exactly
one line written to byre.config + picker-owned favourites in
default.config → image build → firstrun login (real Anthropic key through
the provider picker) → live agent answering ($0.03 round trip). Then the
whole point of shared auth, live: a SECOND fresh project prefilled the
offer ([Y/n]), built, and came up logged-in with NO prompt — the
credential rode the symlink (verified link + type:"api" in the shared
inode from inside the box). MCP inject likewise: declared server baked,
wrapper env present on the agent's PID 1, `byre status` shows
"the agent session receives: qa-probe". Deliver: cwd-targeted, -2
no-clobber, no-tty degradation (rc=1 + candidates), tty picker + cancel,
OSC 52 clipboard message. Config UI: Claude Skill add flips the dirty
flag, ^q double-confirm, discard leaves the file byte-intact (da1168d
fix holds in the field). Offer info text ("i") is excellent — says
exactly what each answer writes.

## Findings (report-only; none fixed)

1. **Cosmetic — startup notices hardcode `~/.byre` when BYRE_HOME points
   elsewhere.**
   Repro: `BYRE_HOME=~/qa/home byre develop` in a fresh project.
   Observed: "byre: wrote ~/.byre/AGENTS.md" + "refreshed ~/.byre/bundled"
   — but the files land (correctly) in ~/qa/home (verified by mtime; the
   literal ~/.byre was untouched). Expected: print the real path.
   Hardening: unit test pinning the notice against a BYRE_HOME override.

2. **Confusing — the offer's mount note reads as already-decided.**
   Repro: wizard → Agent: opencode. Observed, BEFORE the y/N question:
   "Note: mounts machine-scoped volume "opencode-identity" (shared
   credentials)." A cold reader can't tell whether that note describes
   the agent choice (already made) or the offer (pending) — i.e. whether
   declining still mounts the volume. Expected: the note reads
   conditionally ("saying y mounts…") or moves into the i-text.
   Hardening: onboard unit test on the prompt copy, if reworded.

3. **Debatable — deliver picker CANCEL exits 0.**
   Repro: two boxes up, `echo x | byre deliver` from a non-project dir
   in a tty → q. Observed: "cancelled — nothing delivered", rc=0; the
   no-tty ambiguous path exits 1. A script can't distinguish cancelled
   from delivered. May be deliberate (user-chosen cancel = not an
   error); if so, worth a comment where the code decides it.

4. **Low — Claude Skill editor accepts a nonexistent directory
   silently.**
   Repro: config UI → Claude Skills → a → name qa-skill, dir
   /definitely/not/a/dir → accept. Observed: entry listed, no signal;
   the typo would surface only at the next develop. Name-shape
   validation, by contrast, is immediate and clear. Doctrine-consistent
   fix is legibility, not a gate: annotate the row "(path does not
   exist — build will fail)". Hardening: configui unit test on the
   annotation.

5. **Cosmetic — validation error truncates at the pane edge.**
   Repro: finding 4's flow but enter the junk PATH as the NAME.
   Observed: "...starting with a letter or digit (m" cut at 110 cols —
   the message doesn't wrap. Fine at wider terms.

## Suggested next steps (each its own dispatch)

- Harden findings 1 + 4 into unit tests alongside wording/annotation
  fixes; finding 2 is a copy decision (Pete); finding 3 is a ruling.
- The un-driven residue: Ctrl-C login-skip trap path, worktree flows,
  `byre reset` + re-develop idempotence — candidates for pass #2.
