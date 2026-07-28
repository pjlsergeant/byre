package commands

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	xterm "github.com/charmbracelet/x/term"

	"github.com/pjlsergeant/byre/internal/packages"
)

// statusrender.go owns `byre status`'s LAYOUT: the tiers, the row funnel, and
// the wrapping. status.go decides what the page says; this file decides how
// much of it a tier prints and where the characters land.

// statusTier is how much of the status page a render prints.
//
// Both tiers render the same PAGE -- every row that exists appears in both.
// The default tier truncates values and folds mechanism notes; every
// truncation names itself (a count, an ellipsis, a `--full` pointer), so the
// short page is a summary of the long one and never a subset of it. Two
// things it never folds: a claim degradation and a containment disclosure.
// Those rows are what the page exists for, and they are short.
type statusTier int

const (
	tierDefault statusTier = iota
	tierFull
)

func (t statusTier) full() bool { return t == tierFull }

// FullHint is the pointer a truncated value carries, so a reader can always
// tell a summary from the whole of something. Named because status prints it
// on every truncated row and the docs quote it.
const FullHint = "--full to show"

// statusRow is one label/value pair before layout. An empty Label is a
// continuation of the row above it.
type statusRow struct {
	Label string
	Value string
}

// statusLabelMin is the label column's floor. It is the width the page has
// always used, so a page whose labels all fit keeps its established shape
// and only a genuinely longer label (`Claude Skills:`) moves the column.
const statusLabelMin = 13

// statusFallbackWidth is the column budget for output that is not going to a
// terminal -- a pipe, a file, a test buffer. Fixed on purpose: redirected
// status output is byte-identical wherever it is produced, which is what
// lets the README and the docs site pin a sample of it.
const statusFallbackWidth = 80

// statusWidth is the column budget for a render to w: the terminal's own
// width when byre is printing to one, the fixed fallback otherwise. Clamped
// at both ends -- a 20-column terminal cannot be laid out two-column, and a
// 300-column one produces rows no eye tracks back.
func statusWidth(w io.Writer) int {
	f, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return statusFallbackWidth
	}
	cols, _, err := xterm.GetSize(f.Fd())
	if err != nil || cols < 48 {
		return statusFallbackWidth
	}
	if cols > 160 {
		return 160
	}
	return cols
}

// writeStatusRows lays rows out on w.
//
// Every row is data -- config-authored paths, skill names, engine output --
// and status emits no ANSI of its own, so the terminal strip lands here
// rather than at each of the hundred-odd call sites: a value carrying CSI/OSC
// bytes would otherwise repaint or exfiltrate from the very screen reporting
// it, and one unescaped call site is all it takes. Control characters go too,
// so a value can never end a line: a value that could would forge a row, and
// the "Label: value" grammar is what makes the page scannable.
//
// A value too long for the budget WRAPS here, hanging to the value column.
// Letting the terminal do it instead put continuations at column zero, where
// they read as new rows and shred the two columns the page is made of.
func writeStatusRows(w io.Writer, rows []statusRow, width int) {
	labelW := statusLabelMin
	for _, r := range rows {
		if r.Label == "" {
			continue
		}
		if n := displayLen(packages.EscapeTerminal(r.Label)) + 1; n > labelW {
			labelW = n
		}
	}
	indent := strings.Repeat(" ", labelW+1)
	avail := width - labelW - 1
	if avail < 24 {
		avail = 24
	}
	for _, r := range rows {
		head := ""
		if r.Label != "" {
			head = packages.EscapeTerminal(r.Label) + ":"
		}
		lines := wrapValue(packages.EscapeTerminal(r.Value), avail)
		fmt.Fprintf(w, "%s%s %s\n", head, strings.Repeat(" ", labelW-displayLen(head)), lines[0])
		for _, l := range lines[1:] {
			fmt.Fprintf(w, "%s%s\n", indent, l)
		}
	}
}

// displayLen counts a rendered string's columns. Rune count, not byte count:
// status prints em dashes and warning signs of byre's own, and a byte count
// would wrap those rows early. It does not model double-width runes -- the
// only ones byre prints are the 🛑 containment markers, which cost one column
// on the rows that carry them.
func displayLen(s string) int { return utf8.RuneCountInString(s) }

// wrapValue breaks one row's value into lines of at most width columns.
//
// It breaks at the separators the row grammar already uses -- ", " and "; "
// between clauses -- so a wrapped row breaks between the things it lists.
// A clause longer than the budget falls back to breaking at spaces, and a
// TOKEN longer than the budget is left whole and allowed to overhang: an
// unbroken path is worth one long line, and a path cut in half is a lie
// about the path.
func wrapValue(val string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.TrimRight(string(cur), " "))
			cur = nil
		}
	}
	for _, seg := range clauses(val) {
		r := []rune(seg)
		if len(cur) > 0 && len(cur)+len(r) > width {
			flush()
			r = []rune(strings.TrimLeft(string(r), " "))
		}
		cur = append(cur, r...)
		for len(cur) > width {
			cut := lastSpaceWithin(cur, width)
			if cut == 0 {
				break // one token longer than the budget: leave it whole
			}
			out = append(out, strings.TrimRight(string(cur[:cut]), " "))
			cur = []rune(strings.TrimLeft(string(cur[cut:]), " "))
		}
	}
	flush()
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// clauses splits a value after each ", " / "; ", keeping the separator with
// the clause it closes so joining the pieces reproduces the value exactly.
func clauses(s string) []string {
	var out []string
	start := 0
	for i := 0; i+1 < len(s); i++ {
		if (s[i] == ',' || s[i] == ';') && s[i+1] == ' ' {
			out = append(out, s[start:i+2])
			start, i = i+2, i+1
		}
	}
	return append(out, s[start:])
}

// lastSpaceWithin returns the index just past the last space at or before
// width, or 0 when the first token alone overruns the budget.
func lastSpaceWithin(r []rune, width int) int {
	for i := width; i > 0; i-- {
		if r[i-1] == ' ' {
			return i
		}
	}
	return 0
}

// pkgColumn formats a package id and its provenance as two columns, with the
// id padded to fit the WIDEST id on the page. A fixed column width was the
// bug: an id longer than it (`pjlsergeant/claude-skills-pocock`) pushed its
// provenance out and mangled the alignment of every row around it.
func pkgColumn(id, provenance string, idWidth int) string {
	if provenance == "" {
		return id
	}
	pad := idWidth - displayLen(id)
	if pad < 1 {
		pad = 1
	}
	return id + strings.Repeat(" ", pad) + provenance
}
