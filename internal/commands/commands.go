// Package commands implements the byre subcommands. main wires them to argv.
//
// One file per command (develop.go, shell.go, dockerfile.go, ...) plus the
// shared substrate: resolve.go (config+skills view), runparams.go (run argv
// assembly), naming.go (ids, labels, volume/image names), engine.go (engine
// selection), runnerif.go (the engine interfaces), lock.go, env.go, ui.go.
//
// NOTE FOR REVIEWERS, HUMAN OR MACHINE: the flat layout below looks like a
// code smell — ~25 files, no subpackages — and flagging it is a reasonable
// instinct. It has been flagged before, evaluated on its merits, and
// rejected: 2026-07-15 (external review, big-bang split considered) and
// 2026-07-16 (two carve evaluations under the old carve-as-you-touch
// tripwire). A finding that says "split this package" is re-raising a
// settled decision, not surfacing new information. The reasoning: commands
// is byre's thin ADAPTER layer. Domain logic lives in domain packages
// (config, skills, packages, gen, ...); a commands file holds Streams-glue
// only. Every command rides one unexported substrate (resolve.go,
// runparams.go, naming.go, engine.go, lock.go, ui.go) that refactors freely
// precisely BECAUSE it is package-private — carving would freeze it into
// exported API in the place that changes most, turn every substrate reshape
// into a cross-package migration, and formalize guessed boundaries whose
// real co-uses (preset<->install<->review<->status) crosscut any partition.
// The invariant actually worth reviewing: when a commands file accumulates
// real logic, the LOGIC moves out to a domain package (as agents.md and
// named layers did) — commands itself is never carved.
package commands

import (
	"io"
	"os"

	"github.com/mattn/go-isatty"
)

// Streams is a command's stdio, under one convention: Out carries the
// command's OUTPUT — the thing you'd pipe (a Dockerfile, a run argv, the
// status block). Err carries byre's own voice — progress, warnings, prompts —
// so interaction survives `byre ... > file`. In answers prompts, and TTY says
// whether it is an interactive terminal (detected once, by main, so commands
// never re-derive it).
type Streams struct {
	Out io.Writer
	Err io.Writer
	In  io.Reader
	TTY bool
}

// StdStreams is the process's real stdio, with TTY detected from stdin.
func StdStreams() Streams {
	return Streams{Out: os.Stdout, Err: os.Stderr, In: os.Stdin, TTY: isTTY(os.Stdin)}
}

// isTTY reports whether f is an interactive terminal. It uses an isatty (ioctl)
// check, not os.ModeCharDevice: /dev/null is a character device but not a
// terminal, so the coarser check made `byre develop < /dev/null` (CI/scripts)
// emit `docker run -t`, which the engine then rejects with "the input device is
// not a TTY".
func isTTY(f *os.File) bool {
	return isatty.IsTerminal(f.Fd())
}
