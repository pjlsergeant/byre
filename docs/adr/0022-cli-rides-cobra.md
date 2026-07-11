# The CLI rides cobra

Decided 2026-07-11 (Pete's dispatch: he wants tab completion and better
generated help "in the very near future"). `cmd/byre` is a `spf13/cobra`
command tree; the hand-rolled per-command arg loops and manual dispatch
are gone.

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

`byre completion <shell> --install` shipped in v0.1.5 and was
**REVERSED the same day** (Pete's call, after field-testing it): the
recommended setup is the per-shell eval/source line in the user's own
rc file, and `--install` is gone. What the field test established:
the static file bought little and cost real machinery. The scripts
are live shims (candidates come from the hidden `__complete` command
at TAB time), so staleness was never the issue -- but the static bash
file only loads through the bash-completion package's autoload dirs,
while `eval "$(byre completion bash)"` works with no package at all
(the script carries an `_init_completion` fallback), and regeneration
costs ~3ms of shell startup. The eval line also gave powershell a
story `--install` had refused, and deleted the ownership-marker/
site-functions-cascade machinery that had taken four review rounds to
harden. Files written by v0.1.5's `--install` keep working; users
delete them at the printed path when they switch. Two things
survive the reversal: byre's own completion command (its help now IS
the per-shell instructions, and bare/unknown invocations stay exit 2)
and the **never-edit-rc-files** ruling -- the user adds the eval line
themselves, exactly the shape of the fpath line the zsh installer
used to print. Install remains out of release plumbing; the README
"How do I" entry owns it.

Consciously accepted (review round 1, Pete-ratified): a value-taking
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
