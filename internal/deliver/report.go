package deliver

import (
	"fmt"
	"io"

	"github.com/pjlsergeant/byre/internal/packages"
)

// Delivery reporting goes out one line at a time, and nearly every line quotes
// something byre did not author: a path the user named, an entry name from a
// walked directory or an ARCHIVE, an id or label read out of a box, an error
// from the engine. All of it prints as data -- terminal control sequences
// stripped, the framing newline added here so a value carrying its own cannot
// forge a second report line under byre's name. byre's own wording carries no
// control characters, so its messages pay nothing for this.
//
// Two exemptions, both deliberate and both visible in the code:
//   - the send meter (remote.go) OWNS its line: it writes CR-anchored progress
//     and an explicit erase, so it is the one place here that emits ANSI on
//     purpose. It writes to m.err directly and must never be funneled.
//   - stdout (cfg.Out) is the porcelain contract, not a report: it must carry
//     the bytes a script reads back, so it is escaped nowhere. What keeps it
//     line-framed is that its values are sanitized where they are CLAIMED
//     (sanitizeBase and its callers) rather than where they are printed. See
//     the porcelain contract in the package comment for who authors what.

// reportTo writes one report line to w. The writer form exists for the remote
// planner, which reports through a caller-supplied warn writer rather than the
// session config.
func reportTo(w io.Writer, format string, args ...any) {
	fmt.Fprintln(w, packages.EscapeTerminal(fmt.Sprintf(format, args...)))
}

// reportf writes one report line to the user's stderr.
func reportf(cfg Config, format string, args ...any) {
	reportTo(cfg.Err, format, args...)
}
