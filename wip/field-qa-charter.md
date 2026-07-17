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

# REPORT — pass #2 ran 2026-07-17 (VM, byre-qa @ c88d121 merged main incl. worktree-metadata)

All queued journeys driven; nothing skipped. No live keys used — every leg
ran on dummy/no credentials (the plumbing proves out without them). Two
sessions (pass paused and resumed via wip/qa-pass2-resume.md, now deleted).

## What just works (worth saying out loud)

- **Seeded gemini (the 2026-07-16 field-failure fix, live):** hand-enabled
  gemini-shared-auth (companion_for, so not offered) via the store
  byre.config. Box up: all four identity files are symlinks into the
  mounted machine volume (byre-machine-u501-gemini-identity),
  settings.json seeded {"security":{"auth":{"selectedType":"oauth-personal"}}},
  and gemini went STRAIGHT to the oauth-personal login — no auth-method
  chooser anywhere in scrollback. Contrast: the plain-gemini box (driven
  pre-pause) shows the chooser. The seed does its one job.
- **claude first-run:** sharing question renders bare (vouched
  shared_auth_for); `i` prints the full info text (what y writes — one
  skills line, where to undo — and that saving a default never opts a box
  in); `y` writes exactly `skills = ["claude-shared-auth"]`. In-box
  firstrun prompts for a setup-token paste with a clean Enter-skip:
  "byre: skipped — using this project's own login", then claude's own
  onboarding. Same shape as codex: a skip gets you a box, not a ready agent.
- **grok first-run:** NO sharing question — correct, grok-shared-auth is
  still companion_for (field gate pending). Device-auth firstrun hook is
  Ctrl-C-skippable (trap verified in source; alt-screen repaints over the
  message); after the skip grok shows its own sign-in, ctrl+q exits, box
  winds down clean.
- **Interrupts leave nothing behind.** Ctrl-C at the wizard: process dies
  on SIGINT, no config written (verified twice). Ctrl-C mid-image-build:
  buildx cancels ("context canceled"), develop exits 130, no stray
  containers; the next develop skips onboarding (config already written)
  and just rebuilds to a working box.
- **reset/forget are exemplary.** reset enumerates exactly what dies with
  the engine suffix ([docker]), warns about re-auth, defaults to No,
  names the machine-wide volumes it deliberately will NOT touch and the
  deliberate-delete path (config → Volumes → clear). Post-wipe: per-project
  volume gone, machine identities intact, rc=0. forget removes image +
  store dir (config, marker, context), rc=0. Declining reset because a
  session is running: message + rc=1 (measured unpiped this time).
- **Worktrees (the freshly merged metadata work, live):** `byre worktree
  wt1 --path ../got-wt1` created branch+worktree and a session in it; the
  box runs the MAIN project's image (shared, no rebuild); in-box
  /workspace is the worktree on branch wt1 and git works (the rebind path
  holding up). Concurrent main-tree session fine; status shows the
  sibling ("Worktrees: 1 other session(s) live"); deliver resolves to the
  cwd's own box (no picker when unambiguous), --box <id> lands in the
  worktree box (payload verified in both inboxes).
- **Config UI ^e round-trip:** vi opens the real file; a bad key
  (`packages` — the row's key is `apt`) is caught on write-back with a
  recovery banner ("fix it and ctrl+e again: … unknown key(s)") while the
  UI keeps last-good values; fixing clears it; ^q prints "config
  unchanged." Resize mid-UI re-clips with the more-below indicator and
  footer intact; resize mid-wizard just rewraps (line-oriented prompts).
- **Templates:** go, node, python all build and launch. node v22.23.1,
  python 3.12.13 on PATH in the box shell. (go: see finding 1.)
- **Firewall:** banner flips open → "network deny-by-default · egress
  none"; enforcement real (npmjs times out, 000); grant one host →
  "egress 1 host", example.com=200, npmjs still closed. Deny-by-default
  means closed, observed.
- **Named layers:** `layer new` scaffolds a self-documenting file under
  $BYRE_HOME/layers; `layer validate` ok; `extends = "qa2base"` in the
  project cascade applied the layer's apt (rg baked into the image) and
  egress (banner + probe) on the next develop. Live resolution as
  documented.

## Findings (report-only; none fixed)

1. **Bug, the real catch of the pass — image ENV PATH is lost in the
   box's login shells.**
   Repro: template=go project, agent=none → in the foreground box shell
   `go version` → "command not found". Same via `byre shell` into any go
   box. PATH there is Debian's stock /etc/profile value
   (/usr/local/bin:/usr/bin:/bin:/usr/local/games:/usr/games); the image
   ENV (/usr/local/go/bin, /go/bin, ~/.local/bin) is gone — /etc/profile
   unconditionally overwrites PATH in login shells and
   /etc/profile.d/byre-env.sh doesn't restore it. The AGENT is unaffected
   (byre-launch execs with the container env — which is why the
   self-hosted dev box never noticed: claude runs go fine, humans weren't
   shelling in). Blast radius: any base image whose toolchain rides ENV
   PATH — the go template's golang image is the shipped victim;
   node/python live in /usr/local/bin and dodge it. So the go template's
   "run tests, poke around" story via byre shell is broken today.
   Fix direction is Pete's call (restore PATH in byre-env.sh from a
   baked-at-generate value? have byre-launch write the runtime env
   somewhere profile.d can source? drop the login-shell -l?); hardening:
   an in-box test asserting `command -v go` through the login-shell path.
2. **Bug, cosmetic — config UI renders `[none]` twice in the Pri. Agent
   picker** when the config says agent = "none" (any wizard-onboarded
   agentless project). pickerOpts (internal/configui/skills.go:343)
   appends the configured-but-not-discovered current value without
   guarding current == noneOption, then appends the none sentinel.
   Template row has the same latent case (template = "none").
   Fix is one condition + a unit test.
3. **Low — nonsense at the sharing question silently declines.** "banana"
   at `[y/N, i for info]` → taken as No, wizard moves on. Standard y/N
   convention, but this prompt has a third key: an `i` typo (or any
   stray input) silently declines an offer the user may have wanted to
   read first. A reprompt-on-unrecognized would cost one line.
4. **Low — a box killed under a live session reports `byre: exit status
   137` and exits 1.** Deliberate per the develop.go comment (≥125 is
   docker's engine range, stays a byre error, and the code_below-125
   pass-through is correct), but the message gives no hint the container
   was killed/removed externally — "byre: exit status 137" reads like a
   byre bug to the person whose box just vanished. Legibility, not
   behavior.
5. **Low — deliver into a worktree box is labeled with the main
   project's slug.** `deliver --box 90bef5d3ecdd` (the wt1 box) prints
   "delivering to got-qa2-3c1130 (docker, 90bef5d3ecdd)" — same text as a
   main-box delivery except the id. The worktree box has its own slug
   (got-wt1-qa2-80c17d); naming it would make the two deliveries
   distinguishable at a glance.
6. **Low — status names sibling worktree sessions by container id only**
   ("Worktrees: 1 other session(s) live: 90bef5d3ecdd"). Which worktree
   (path/branch) that is isn't recoverable from the output.

## Closed threads (no finding)

- **reset/forget decline exit code:** the pass-1 "rc=0" was a pipe
  artifact (measured `tail`'s status). Code path: the decline is a plain
  error → main.fatal prints the banner and exits 1. Verified both by
  reading reset.go/main.go and by an unpiped VM measurement (rc=1). The
  3-vs-1 asymmetry with develop's decline is deliberate: ExitRefused=3
  exists only because develop's exit code otherwise carries the agent's
  own status — an ambiguity reset/forget don't have.
- **Pre-pause journeys** (codex first-run + Ctrl-C skip, BYRE_HOME
  startup-notice fix live, develop-while-running rc=3, byre shell
  round-trip, plain-gemini chooser) — recorded in the pass-2 resume doc,
  all clean, absorbed here.

## Suggested next steps (each its own dispatch)

- Finding 1 is the one that bites users today (go template + byre
  shell). Fix + in-box regression test.
- Finding 2 is a one-line fix + unit test on pickerOpts.
- Findings 3–6 are wording/legibility rulings — Pete's call which are
  worth lines of code.
- Gemini shared-auth field gate: the seed plumbing is now field-proven
  without credentials; the remaining gate is the LIVE two-box shared
  login (needs a real Google login once, then two boxes).
