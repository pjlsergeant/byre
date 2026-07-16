# TUI test harness -- tier 2 of the release-testing plan

Proposed 2026-07-16 (box agent, dispatched by Pete: "propose Tier 2 in a
wip document, get feedback on it from our code-reviewers, and then let's
build it"). REVISED same day against two independent design reviews
(codex + grok, .byre-devlog/reviews.md 2026-07-16T18:04) -- every
finding below is incorporated or consciously rejected inline. Status:
review round 2 / build. Lifecycle: absorbed into an ADR +
BYRE-DEVELOPMENT.md when built; this file is deleted then.

Context: tier 1 (loopback-ssh gated test, the no-fakes remote-delivery
loop) shipped 2026-07-16. Tier 3 -- an agent-driven exploratory QA pass
before releases -- is decided-in-principle but NOT built now; it shapes
this design (see "Tier-3 constraints") and is otherwise out of scope.

## Problem

byre's interactive surfaces -- the config UI (Pete's daily-driver
surface), the paste beat, the pickers, the deliver sending meter -- are
tested at the bubbletea-model level only: update/view functions fed
synthetic messages. The *pty boundary* is unverified end to end:

- real key encoding arriving through a pty (ctrl chords, bracketed
  paste, ESC disambiguation), and raw mode actually engaging on a real
  terminal (the degraded beat hand-rolls MakeRaw and its restore);
- the inline bubbletea renderer against a real terminal (form.go
  documents one silent-breakage mode: a frame taller than the terminal);
- the shipped binary as a whole -- flag wiring through cobra into a
  live TUI, something no model test touches.

(Correction from review, both reviewers: the first draft claimed beat
and picker "read /dev/tty directly so prompts work while stdin is
busy". They don't -- both read `s.In`, and the beat *requires* stdin to
be the terminal (beat.go:269). The /dev/tty behavior in this codebase
is the ssh binary's own, quoted in sshexec.go. Whether byre *should*
fall back to /dev/tty under a redirected stdin is a product question
this design does not answer; flagged separately to Pete.)

The field record says bugs live at this boundary: the
drag-onto-the-window paste discovery (2026-07-10, now the
`importFromPaste` disambiguation) was found live at a terminal, not by
a test.

## Shape: tmux, chosen with eyes open

A Go helper package, `internal/tuitest`, that runs the SHIPPED byre
binary (or any argv) inside a **private tmux server** and asserts on
captured pane text.

The alternatives, with the objections the reviews sharpened:

- **In-process bubbletea drivers** (teatest and kin) exercise the
  model, not the pty/CLI boundary. We have model tests; the gap is
  what they can't see.
- **expect/pexpect**: capable of arbitrary pty byte streams (the first
  draft's "line-oriented" dismissal was too categorical -- review
  finding) -- but it has no screen model, so full-screen assertions
  must be hand-built, and it adds a language runtime to test
  environments.
- **In-process pty + VT emulation** (creack/pty + vt10x): the most
  hermetic option -- exact stream assertions, lifecycle control, exit
  codes for free, no external binary. Both reviewers noted the first
  draft dismissed it too fast, and one pointed out fairly that tmux
  *is* a VT emulator, so "no private substrate" was overstated: tmux
  doesn't avoid emulator-specific behavior, it *selects* tmux as the
  emulator. Accepted. The choice of tmux is a **product preference,
  not an inevitability**: one substrate shared with the tier-3 agent
  and with a human replaying a repro, at the cost of an external test
  dependency and a second terminal layer. If a hermetic backend is
  ever needed (CI without tmux, exact-byte assertions), the high-level
  API below admits a pty backend behind the same verbs -- consciously
  deferred, not rejected.
- **charmbracelet/vhs** (tape → GIF): golden-artifact testing of
  exactly the kind this design rejects (goldens rot); noted so nobody
  rediscovers it mid-build.

Byte-ordering claims (e.g. the meter guard's clear-line discipline)
stay with the existing buffer-level unit tests, which both reviews
correctly called the *stronger* oracle for that property -- a captured
pane is a post-render cell dump, not a transcript. tmux tests cover
the terminal boundary where a terminal is actually the thing observed.

## API sketch (revised: lifecycle-first)

```go
bin := tuitest.Binary(t)          // builds ./cmd/byre once per test binary
s := tuitest.Start(t, tuitest.Opts{
        Cols: 100, Rows: 30,
        Env: map[string]string{"BYRE_HOME": tmpStore},
}, bin, "config", "--global")
s.WaitFor("GRANTS")               // substring appears (or process died: fail with screen+status)
s.Keys("Down", "Down", "Enter")   // tmux send-keys tokens, verbatim
s.Type("text")                    // literal text (send-keys -l)
s.Keys("C-s")
s.WaitFor("Saved ✓")
s.Keys("q")                       // or the surface's quit chord
st := s.WaitForExit()             // exit status, from the dead pane
```

Mechanics, revised where the reviews hit:

- **Private server per test**: `tmux -L <socket>`, socket name derived
  from a hash of `t.Name()` (subtests carry `/`; hashing sidesteps
  sanitization and socket-path length limits -- review nit). Server
  killed on cleanup. Never the developer's own tmux.
- **Lifecycle is part of the API** (codex's biggest structural point):
  panes run with `remain-on-exit on`, so the process exiting never
  destroys the evidence; `WaitForExit` reads `pane_dead_status` for
  the real exit code. Every `WaitFor` races the wanted output against
  process death -- a dead process fails the wait immediately with the
  final screen and status, instead of timing out blind.
- **WaitFor is the primitive, not settle.** The first draft's
  "settle = two identical captures 50ms apart" is structurally broken
  on this codebase (both reviewers): focused textinputs blink
  (form.go:407 returns textinput.Blink) and the live beat re-samples
  the clipboard every 1.2s (beat.go:211) -- screens with timers either
  never settle or settle deceptively between animations. Replacements:
  - `WaitFor(substr)` / `WaitUntil(pred)`, one overall deadline;
  - `WaitForAfter(epoch, substr)`: `Keys` returns a capture epoch, and
    matches that predate the action are rejected -- the stale-success
    hazard codex named (the string was already on screen before the
    keystroke);
  - `CaptureNow()` for diagnostics -- explicitly a debug dump, never a
    layout oracle;
  - `WaitStable(window)` ONLY where a layout assertion truly needs it,
    normalizing cursor-only differences, with a stability *window*
    (unchanged for N ms), not a matching pair.
- **Failure output**: any failed wait prints the final screen and (if
  dead) the exit status. The debugging artifact is the screen.
- **Fixed geometry** per test (default 100x30), `TERM=xterm-256color`,
  status bar off. First frames can precede the WindowSizeMsg (the
  renderer's unknown-height fallback -- review nit), so tests wait for
  content, never assert the very first paint.
- **No golden screens.** Fragment assertions against *exact product
  strings* -- `Saved ✓`, `byre: wrote`, `byre: cancelled — nothing
  delivered` (em dash: the first draft asserted an ASCII `--` that
  isn't in the code; review catch) -- not broad fragments like
  "saved", which appear in footers and prompts.
- **Isolation = `BYRE_HOME`, not `HOME`** (both reviewers; the store
  resolves through project.Home(), BYRE_HOME first --
  project.go:107-113, and existing tests already use it). The first
  draft's "HOME=tmpdir, no global state" was both wrong (missed
  BYRE_HOME) and over-broad (HOME swaps git/ssh/shell behavior along
  for the ride, and clipboard/DISPLAY/PATH stay inherited regardless).
  The harness sets an explicit environment: `BYRE_HOME` to a temp
  store, `TERM`, and a documented pass-through set; anything the test
  means to control (PATH for clipboard shims, ssh config for VM cases)
  is set deliberately per test, not absorbed into one HOME swap.
- **stdout/stderr**: under a pane both land on the same pty, which is
  fine for observation -- noted (review) so a future
  split-stderr-for-logging change knows it breaks this.

## What runs where

Three gates, matching what each test actually needs -- the first draft
collapsed the third into the second (review catch):

| tier | gate | where |
|------|------|-------|
| pty-only (config UI, beat cancel, degraded paste) | `BYRE_TUI_TESTS=1`, tmux present | every push: CI installs tmux |
| + engine (picker over live boxes) | + `BYRE_DOCKER_TESTS=1` | byre-inttest → the VM |
| + loopback ssh (sending meter) | + `BYRE_SSH_LOOP_TESTS=1` | byre-inttest → the VM |

- **CI concretely**: the existing test job gains `apt-get install -y
  tmux` and `BYRE_TUI_TESTS=1`. In CI the gate means *must run*: gate
  set + tmux missing = **fail loudly** (a configuration error), never
  skip -- otherwise an install regression silently deletes the tier
  (review). Locally, gate unset = skip; gate set + no tmux = fail with
  the install hint.
- **Race mode**: the race detector instruments the *test* binary; the
  byre child is built separately (plain `go build`, once per test
  binary via a shared helper, the pattern the loopback test already
  uses). No interaction.
- **VM-tier serialization** (review): gated tests within a package
  already run serially under `go test`; the picker test creates and
  removes its own two boxes (the tier-1 test's create/cleanup
  boilerplate, twice); nothing VM-tier runs in parallel with anything
  that shares docker or the loopback ssh provisioning.
- **Platform honesty** (review): all of this runs on Linux. It covers
  the degraded beat and (with shims, below) the live-beat code path --
  it does NOT validate macOS pasteboard integration (osascript et al),
  which stays in DELIVER.md's existing macOS-verified/Linux-reported
  posture. Onboarding, which the first draft mislabeled a bubbletea
  surface, is line-oriented bufio prompts (onboard.go) -- already
  well unit-tested; a tmux smoke for it is possible but fixes no
  wiring gap, so it is not in the first wave.

## First tests (revised against the reviews; also the build order)

1. **Config UI save, then quit**: `byre config --global` (engine
   discovery untouched -- the project editor constructs a volume admin
   when engines resolve, so `--global` is the deterministic engine-free
   surface; review). Navigate to a real text field with the form's
   actual focus order (focus starts on the first GRANTS field, not
   where the first draft's sketch pretended), edit, `C-s`, wait for
   `Saved ✓`, **then quit and WaitForExit** (ctrl-s saves in place and
   keeps the UI open -- review), assert the file under
   `$BYRE_HOME` changed, exit 0, and `byre: wrote` printed on the way
   out.
2. **Config UI cancel**: edit, then ESC **twice** -- the first arms
   the dirty-form confirmation (form.go:452), asserted by its message;
   the second quits. Assert the file did NOT change and the exit is
   clean. (The first draft's single-cancel sketch would hang --
   review.)
3. **Paste-beat cancel** (degraded path -- headless CI has no
   clipboard backend, so this is the path that actually runs there;
   the draft didn't say which -- review): `byre deliver` on the pane
   tty, assert the degraded prompt (`byre: no clipboard access
   here…`), `C-c`, assert exactly one `byre: cancelled — nothing
   delivered` and exit 0, before any engine/discovery output.
4. **Degraded paste delivers text**: tmux `set-buffer` +
   `paste-buffer -p` (a real bracketed paste through tmux's own paste
   machinery, which is the negotiation a terminal actually performs --
   review; raw `ESC[200~` injection is kept in the harness for parser
   edge cases but isn't the default), `C-d`, and -- with no engine on
   PATH -- assert the loud `no container engine` error *after* the
   paste was accepted. Proves the pty leg of the beat end to end.
5. **VM tier -- picker**: two live boxes (tier-1 boilerplate twice),
   `byre deliver <file>` on a pane tty, assert both rows render, pick
   the second, assert the landed path.
6. **VM tier -- sending meter, weak-form** (both reviewers killed the
   strong form): >256 KiB payload (the meter is silent below
   meterStep) over loopback ssh with progress enabled. Asserts: a
   `sending` line appeared; the final pane shows `sent` and any remote
   notes on their own lines. Explicitly NOT claimed: the guard's
   mid-transfer byte ordering -- a final pane capture can't see it and
   tmux's emulator would normalize it anyway; that property stays
   pinned by the buffer-level unit tests
   (TestSendMeterHonestUnderInterruption). A deterministic
   mid-transfer assertion needs a backpressure protocol (remote stalls
   on a signal after the first meter step) -- consciously out of v1.

**Demoted from the first draft** (its test #3): the bracketed-paste
*disambiguation* (drag vs clipboard-mirror vs prose). Both reviewers
independently showed it cannot run as described: `importFromPaste` is
reached only from the live beat, the live beat exists only when a
clipboard backend resolves, and headless Linux has none -- the
degraded path absorbs the paste as plain text and the prose branch
prints no distinguishing message at all. The disambiguation logic
stays unit-tested (deliversources_test.go already covers the
branches). The live-beat path CAN be TUI-tested with **deterministic
clipboard shims** -- fake `wl-paste`/`xclip` executables on a private
PATH plus the env vars the backend probe wants -- which is
test-environment faking (the product binary is untouched), flagged as
the one place this design fakes a host capability. Second wave, not
first.

## Tier-3 constraints honored (shaping, not building)

- The harness does nothing an agent can't do with plain tmux -- same
  verbs (`send-keys`, `capture-pane`, `paste-buffer`), no in-process
  hooks. One honest correction from review: `WaitFor`/`WaitForExit`
  are Go conveniences *on top of* those verbs, so the
  BYRE-DEVELOPMENT.md conventions section documents their shell
  equivalents (a `capture-pane` poll loop; `pane_dead_status`) --
  that page, not the Go API, is what the QA agent gets pointed at.
- A finding reported as keystrokes translates mechanically into a
  tuitest regression test, and every test doubles as a repro script.
- The future agent pass stays report-only, never a gate; its safety
  posture is the inttest grant pattern reused verbatim (a byre box,
  egress closed except the sacrificial VM's ssh endpoint, one scoped
  key).

## Costs and risks

- **Flake surface**: blink and tick timers are why settle-equality
  died in review; WaitFor-first with epoch matching is the mitigation,
  plus the standing discipline: a test that flakes twice gets
  rewritten or deleted, never retried-until-green.
- **tmux versioning**: `send-keys`/`capture-pane`/`paste-buffer` are
  long-stable, but key-name and paste behaviors have shifted across
  major versions historically (review) -- CI installs from the distro
  and the harness asserts `tmux -V` >= a floor chosen at build time.
  Cheap insurance, not a pin.
- **CI time**: per-test private servers parallelize wall clock; the
  binary builds once per test binary. Measured before promised --
  the "sub-second" claim from the first draft is withdrawn until
  numbers exist (review).

## Not doing

- No pixel/screenshot testing, no VT-emulation dependency (deferred,
  not rejected -- the API admits a pty backend later), no golden
  screens, no agent in CI, no gating on anything nondeterministic, no
  macOS claims from a Linux harness.

## Open product question for Pete (out of scope here)

The beat *requires* stdin to be the terminal (beat.go:269: "the paste
beat needs a terminal on stdin"). Should interactive prompts survive a
redirected stdin by falling back to /dev/tty (the way ssh itself
does)? Today the answer is no by construction; the first draft
wrongly assumed yes. If that contract should change, it's a product
change first, a test second.
