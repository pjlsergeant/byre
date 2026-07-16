# TUI tests ride tmux

Decided 2026-07-16 (Pete dispatched the tiered release-testing plan; the
design ran two review rounds -- codex + grok -- before build; the working
doc `wip/tui-test-harness.md` is absorbed here and deleted). byre's
interactive surfaces get end-to-end tests: the SHIPPED binary drives a
real pty inside a **private tmux server per test**, and assertions read
captured pane text. The harness is `internal/tuitest`; conventions for
humans and agents are in BYRE-DEVELOPMENT.md.

## The gap it closes

Model-level tests (bubbletea update/view against synthetic messages)
cannot see the pty boundary: real key encoding through a terminal
(ctrl chords, bracketed paste), raw mode engaging and restoring, the
inline renderer against a real screen, cobra-to-TUI wiring in the built
binary. The field record put bugs exactly there (the 2026-07-10
drag-onto-the-window paste discovery was found by a human at a
terminal). Model tests stay; this tier covers what they can't.

## Why tmux

One substrate shared by three consumers: deterministic tests (this ADR),
humans replaying a repro (every test doubles as a keystroke script), and
the planned report-only agent QA pass (TODO: "Agent field-QA pass"),
which will drive the same verbs -- `send-keys`, `capture-pane`,
`paste-buffer`. That sharing is a **product preference, not an
inevitability** (review point): tmux is itself the VT emulator we're
electing to trust, and the harness API deliberately admits a hermetic
in-process pty backend later if one is ever needed. Consciously
rejected: teatest and kin (the model, not the wiring), expect (pty-
capable but no screen model, plus a language runtime), vhs (golden
artifacts -- goldens rot), golden screens in any form (fragment
assertions against exact product strings instead).

## The rules that survived review

- **Lifecycle first.** Panes run `remain-on-exit`; exit status comes
  from a shell wrapper recording `$?` to a file (NOT tmux's
  `pane_dead_status`, which proved version-sensitive — ubuntu's 3.4
  reported 0 where 3.5a reported the real status; CI caught it on the
  harness's first push); every wait races wanted-output against process
  death, so a crash fails with the final screen and status, never a
  blind timeout.
- **Transition epochs.** `Keys`/`Type`/`Paste` capture the pre-action
  screen; `WaitForAfter` fails immediately if the wanted string predates
  the action -- a persistent footer can't fake a result.
- **No settle-by-quiet.** Blink timers and the beat's 1.2s clipboard
  tick mean "two identical captures" never reliably settles; waits are
  semantic (a substring), and `CaptureNow` is a diagnostic dump, not a
  layout oracle.
- **Enforced headlessness.** Degraded-path tests unset
  `DISPLAY`/`WAYLAND_DISPLAY` and give the child a PATH resolving
  neither clipboard readers nor engines -- an inherited display plus an
  installed xclip must not silently flip the code path under test.
- **Store isolation is `BYRE_HOME`**, per test, never a `HOME` swap.
- **Gates**: `BYRE_TUI_TESTS=1` (skip unset; set-without-tmux FAILS --
  a silent skip would let an install regression delete the tier);
  engine tests add `BYRE_DOCKER_TESTS=1`; loopback-ssh tests add
  `BYRE_SSH_LOOP_TESTS=1` and ride the sacrificial VM only.
- **Serialization by placement**: every test sharing docker or the
  loopback-ssh provisioning lives in `internal/commands` -- one serial
  test binary. The day one wants to live elsewhere, `byre-inttest`
  grows `-p 1` in the same change.
- **Nondeterminism is not gated on.** The sending-meter test asserts
  the FINAL terminal state only (an unthrottled loopback transfer can
  outrun any capture); mid-transfer observation waits for a
  backpressure protocol nobody has needed yet, and byte-ordering claims
  stay with the buffer-level unit tests.
- **Flake discipline**: a test that flakes twice is rewritten or
  deleted, never retried until green.

## Accepted costs

- tmux is a dependency of test environments (CI installs it; the VM
  template carries it; never a dependency of byre).
- A pane capture is a post-render cell dump -- exact byte-stream
  assertions don't belong in this tier.
- All of it runs on Linux: macOS pasteboard integration keeps
  DELIVER.md's macOS-verified/Linux-reported posture. The live-beat
  clipboard path is testable later with fake `wl-paste`/`xclip` shims
  on a controlled PATH (test-environment faking only; the product
  binary stays untouched).

  **Amended 2026-07-17**: the engine-free tiers (unit suite + this tmux
  tier) now also run on macOS CI — the harness dropped its one GNU-ism
  (`Opts.Dir` rode `env -C`; now a `cd` inside the pane's shell wrapper)
  and the `wl-paste` shim tests skip on darwin, since
  `hostClipboardReader` rides osascript there and never consults the
  shim. So the pasteboard sentence above still stands: macOS clipboard
  integration keeps the macOS-verified posture (a fake-osascript
  sibling is the route if it ever needs CI teeth). The engine and
  loopback-ssh tiers stay Linux-hosted.

## Open question, resolved same day

The beat requires stdin to BE the terminal (beat.go); the question was
whether prompts should survive an occupied stdin via /dev/tty, the way
ssh's own prompts do. **Decided 2026-07-16 (Pete): adopt ssh's
contract.** Scope turned out to be the picker only -- the beat never
collides with a busy stdin (piped stdin means the pipe IS the payload
and the beat correctly doesn't run). hostPicker now tries, in order:
stdin-as-terminal, /dev/tty (rendering to the terminal device when
stderr is redirected too), a graphical dialog, then the
candidates-listing degradation. So `cmd | byre deliver` with several
boxes running picks interactively instead of erroring. Pinned by a
gated TUI test (a pipe on stdin, the picker steered over /dev/tty) --
the product change came first, the test second, as this section
originally required.
