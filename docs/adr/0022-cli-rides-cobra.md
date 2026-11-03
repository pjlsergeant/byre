# The CLI rides cobra

Decided 2026-07-11 (tab completion and better generated help were
requirements). `cmd/byre` is a `spf13/cobra` command tree, replacing
hand-rolled per-command arg loops and manual dispatch.

## The trigger

The hand-rolled dispatch was fine for what it did -- a command table
that generated the top-level usage, centralized help handling, and
dispatched through a test-pinnable `app` struct. What it could not do
without becoming a project is shell completion: supporting it across
shells means shipping per-shell scripts plus a hidden introspection subcommand
the scripts call back into, which is re-implementing cobra's
architecture badly. With completions and richer help as actual
requirements, the trade flipped.

cobra is also what byre's own ecosystem runs on: docker, podman, gh,
and kubectl are all cobra CLIs. Shelling out to two cobra binaries
while hand-rolling our own dispatch was consistency with nothing.

## What byre keeps (deliberately, cobra doesn't do these by default)

- **The exit-code contract**: usage errors = 2, byre failures = 1,
  a main-goroutine panic = 70 (Go exits a panic with 2, byre's usage
  code -- indistinguishable from a bad flag to anything reading the
  status). The claim runs one way only: byre's own recovered
  main-goroutine panic exits 70, but a panic on another goroutine still
  ends the process at 2, since a recover cannot reach it.
  Agent/container exit codes pass through silently (`ExitError`), so an
  agent's own 70 reaches the caller as 70 too (the commands page says
  how byre's output tells them apart).
  cobra's own error printing and usage-dumping are silenced; flag-parse
  failures are wrapped into `usageError` via `FlagErrorFunc`, and all
  positional-arg validators are byre's own (cobra's built-ins return
  untyped errors that would misreport as exit-1 byre failures).
  `main_test` pins "usage errors never dispatch".
- **The `app` struct seam**: `newRootCmd` builds the tree per
  invocation around an `app` value, so tests pin flag->function wiring
  with recorders instead of executing real commands.
- **Bare `byre`, bare `byre skill`, and unknown commands stay exit 2**
  -- root and `skill` carry a `RunE` returning `usageError` instead of
  cobra's show-help-and-exit-0 default for non-runnable parents.
- **`--version` stays an alias** rewritten to the `version` command, so
  both spellings share help, operand checking, and byte-identical
  output.
- **Command order**: registration order (develop first), not
  alphabetical -- the top of the help is the happy path. The root usage
  template also drops the phantom `byre [flags]` use-line (root's
  `RunE` exists only for the exit-2 path).

## Completion

Completions are static -- commands and flags. Dynamic value completion
(e.g. live box ids for `--box`) is rejected for now: completion callbacks
running engine discovery on every TAB is a latency/failure surface byre
doesn't need yet. The setup is the per-shell eval/source line in the
user's own rc file (`eval "$(byre completion bash)"` works with no
bash-completion package at all, costs ~3ms of shell startup, and covers
powershell); the completion command's help IS the per-shell
instructions, and bare/unknown invocations stay exit 2. **byre never
edits rc files** -- the user adds the line themselves, so there is no
file-installer. The README "How do I" entry owns setup.

Consciously accepted: a value-taking
flag consumes a following `--help` -- `byre develop --template --help`
dispatches with the template `"--help"` instead of printing help. The
old parser's help-anywhere scan was implementation fallout, not a
promise (its purpose, `cmd --help` working, is cobra-native), and this
is exactly what docker, kubectl, and git do. Pinned by a dispatch test;
do not restore a pre-parse help scan.

## The dependency posture

Dependencies are added on demonstrated merit, not collected (bubbletea
and textdiff had already joined on that basis). cobra brings pflag and
(Windows-only) mousetrap, nothing else.
