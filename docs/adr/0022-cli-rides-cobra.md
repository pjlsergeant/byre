# The CLI rides cobra

Decided 2026-07-11 (tab completion and better generated help were wanted
in the near future). `cmd/byre` is a `spf13/cobra` command tree; the
hand-rolled per-command arg loops and manual dispatch are gone.

## The trigger

The hand-rolled dispatch was fine for what it did -- a command table
that generated the top-level usage, centralized help handling, and
dispatched through a test-pinnable `app` struct. What it could not do
without becoming a project is shell completion: doing that properly
means shipping per-shell scripts plus a hidden introspection subcommand
the scripts call back into, which is re-implementing cobra's
architecture badly. With completions and richer help as actual
requirements, the trade flipped.

cobra is also what byre's own ecosystem runs on: docker, podman, gh,
and kubectl are all cobra CLIs. Shelling out to two cobra binaries
while hand-rolling our own dispatch was consistency with nothing.

## What byre keeps (deliberately, cobra doesn't do these by default)

- **The exit-code contract**: usage errors = 2, byre failures = 1,
  agent/container exit codes pass through silently (`ExitError`).
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

## What changed for users

`byre completion bash|zsh|fish|powershell` exists (static completions
-- commands and flags; dynamic value completion, e.g. live box ids for
`--box`, was considered and REJECTED for now: completion callbacks
running engine discovery on every TAB is a latency/failure surface
byre doesn't need yet). Help and error wording are cobra-shaped
("unknown flag: --bogus", `Flags:` sections); `help` and `completion`
appear in the command list; `--flag=value` works uniformly (the old
loops accepted it on deliver but rejected it on develop).

A `completion <shell> --install` file-installer shipped in v0.1.5 and
was reversed the same day after field testing: the recommended setup is
the per-shell eval/source line in the user's own rc file
(`eval "$(byre completion bash)"` works with no bash-completion package
at all, costs ~3ms of shell startup, and covers powershell, which
`--install` had refused). Files written by v0.1.5's `--install` keep
working; users delete them at the printed path when they switch. Two
rulings survive the reversal: byre's completion command itself (its
help IS the per-shell instructions, and bare/unknown invocations stay
exit 2), and **byre never edits rc files** -- the user adds the eval
line themselves. Install remains out of release plumbing; the README
"How do I" entry owns it.

Consciously accepted: a value-taking
flag consumes a following `--help` -- `byre develop --template --help`
dispatches with the template `"--help"` instead of printing help. The
old parser's help-anywhere scan was implementation fallout, not a
promise (its purpose, `cmd --help` working, is cobra-native), and this
is exactly what docker, kubectl, and git do. Pinned by a dispatch test;
do not restore a pre-parse help scan.

## The dependency posture

The founding CLAUDE.md's "minimal deps" line was descriptive scaffolding
from day one (when the only dependency was the TOML parser), not a
ruling -- bubbletea and textdiff had already joined on merit. The
standing posture, now recorded: dependencies are added on demonstrated
merit, not collected. cobra brings pflag and (Windows-only) mousetrap,
nothing else.
