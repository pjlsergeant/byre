package tomldoc

// Whole-TREE removal, plus the narrow read-only queries byre's package layer
// needs over the same parse. Both exist for one caller shape: internal/packages
// judges a manifest's [package] tree structurally -- is it there, where does
// the body begin, is this offset data -- and used to do it with hand-rolled
// line scanners that misjudged quoted headers, trailing comments and multiline
// arrays. The judgments belong to the parser, so they live here.
//
// The queries are deliberately purpose-shaped rather than a general AST
// export: each answers one question, over the bytes handed to Load, with a
// result the caller owns. The unstable AST does not leave this package -- an
// exported expression index would make every future caller's offset arithmetic
// tomldoc's problem, which is exactly the failure this replaces.

import (
	"fmt"
	"sort"

	"github.com/pelletier/go-toml/v2/unstable"
)

// RemoveTableTree removes every expression that defines part of the key tree
// rooted at name, wherever it sits in the document, and is the ONE owner of
// that operation (RemoveTable removes one named construct and stays the config
// editor's; this is the package layer's whole-subtree strip).
//
// An expression is targeted when the ROOT component of the path it defines
// equals name. That covers [package], [package.x] and [[package.files]] --
// including blocks that resume AFTER an intervening foreign table -- every
// quoted spelling of those headers (["package"], [['package'.files]]), a
// top-level dotted `package.id = "x"`, and the inline `package = { ... }`
// form. It does NOT cover a nested member: in `meta = { package = {...} }` the
// path defined is meta.package, whose root is meta, and the construct belongs
// to meta.
//
// A targeted header takes its whole block -- header line, body, descendant
// subtables, and full-line comments glued immediately above it -- the same
// removal unit RemoveTable and RemoveArrayTable use. A targeted key-value not
// already inside such a block takes its own line(s) plus its glued comments.
// Comments separated by a blank line from the removed content are NOT
// consumed: they describe whatever follows them.
//
// The spans are computed from ONE analysis pass, coalesced (a parent block
// contains its descendants' spans, and glued-comment spans meet -- overlap is
// expected, not a bug), and the surviving bytes are concatenated in order, so
// every retained byte is byte-identical to its input.
//
// Strictness is repair-friendly, matching every other mutation here: a
// document that was already semantically invalid on the way in comes back
// unvalidated-but-no-worse, and a clean document is checked -- syntactically
// by the index rebuild and semantically by the strict decoder -- before the
// result is adopted.
func (d *Doc) RemoveTableTree(name string) error {
	var spans, blocks []span
	for i, e := range d.exprs {
		switch e.kind {
		case unstable.Table, unstable.ArrayTable:
			if len(e.table) == 0 || e.table[0] != name {
				continue
			}
			s := span{d.gluedCommentStart(i, e.span.start), d.blockEnd(i)}
			spans = append(spans, s)
			blocks = append(blocks, s)
		}
	}
	for i, e := range d.exprs {
		if e.kind != unstable.KeyValue || rootComponent(e) != name {
			continue
		}
		// A key-value inside a targeted block is already going; taking its
		// span again would only add an overlap for the coalescer to undo.
		if withinAny(blocks, e.span.start) {
			continue
		}
		rm := d.lineSpan(e.span)
		rm.start = d.gluedCommentStart(i, rm.start)
		spans = append(spans, rm)
	}
	if len(spans) == 0 {
		return nil
	}
	merged, err := coalesceSpans(spans)
	if err != nil {
		return err
	}
	out := make([]byte, 0, len(d.src))
	prev := 0
	for _, s := range merged {
		out = append(out, d.src[prev:s.start]...)
		prev = s.end
	}
	out = append(out, d.src[prev:]...)
	return d.adopt(out)
}

// rootComponent is the first component of the path a key-value defines: its
// table context's root where it lives under a header, else its own key's root
// for a root-level (possibly dotted) spelling.
func rootComponent(e expr) string {
	if len(e.table) > 0 {
		return e.table[0]
	}
	if len(e.key) > 0 {
		return e.key[0]
	}
	return ""
}

// withinAny reports whether off falls inside one of the spans.
func withinAny(spans []span, off int) bool {
	for _, s := range spans {
		if off >= s.start && off < s.end {
			return true
		}
	}
	return false
}

// coalesceSpans merges overlapping and adjacent spans into an ordered,
// disjoint cover. Overlap among the INPUTS is expected -- a block span
// contains its descendants' -- so the non-overlap invariant is asserted on the
// RESULT, where a violation would mean the merge itself is wrong and the
// splice about to run would delete bytes twice.
func coalesceSpans(in []span) ([]span, error) {
	s := append([]span(nil), in...)
	sort.Slice(s, func(i, j int) bool {
		if s[i].start != s[j].start {
			return s[i].start < s[j].start
		}
		return s[i].end < s[j].end
	})
	out := []span{s[0]}
	for _, n := range s[1:] {
		last := &out[len(out)-1]
		if n.start <= last.end {
			if n.end > last.end {
				last.end = n.end
			}
			continue
		}
		out = append(out, n)
	}
	for i := 1; i < len(out); i++ {
		if out[i].start <= out[i-1].end {
			return nil, fmt.Errorf("tomldoc: removal spans overlap after coalescing (internal bug): %v then %v", out[i-1], out[i])
		}
	}
	return out, nil
}

// ---- focused queries -------------------------------------------------------
//
// Each reads the document Load parsed and returns a value the caller owns.
// Offsets address the bytes passed to Load; a later mutation reindexes the
// document, so a caller that edits must re-Load rather than carry offsets
// across the change.

// HasTableTree reports whether ANY expression defines part of the key tree
// rooted at name -- the presence question RemoveTableTree answers with
// bytes. An EMPTY table counts: `["package"]` with no keys under it declares
// the tree, and a text search for the one unquoted spelling did not see it.
func (d *Doc) HasTableTree(name string) bool {
	for _, e := range d.exprs {
		switch e.kind {
		case unstable.Table, unstable.ArrayTable:
			if len(e.table) > 0 && e.table[0] == name {
				return true
			}
		case unstable.KeyValue:
			if rootComponent(e) == name {
				return true
			}
		}
	}
	return false
}

// FirstTableHeaderOffset returns the byte offset at which the first real
// [table] or [[table]] header LINE begins, and whether the document has one.
// Real is the load-bearing word: header-shaped text inside a multiline string
// or a multiline array is data and is not a header.
func (d *Doc) FirstTableHeaderOffset() (int, bool) {
	for _, e := range d.exprs {
		if e.kind == unstable.Table || e.kind == unstable.ArrayTable {
			return d.lineStart(e.span.start), true
		}
	}
	return 0, false
}

// InValueSpan reports whether a byte offset falls inside some key-value's
// VALUE bytes -- the question "is this text data?" A comment line inside a
// multiline string, or inside a multiline array, is data; the same text on a
// line of its own is not.
func (d *Doc) InValueSpan(off int) bool {
	for _, e := range d.exprs {
		if e.kind == unstable.KeyValue && off >= e.valSpan.start && off < e.valSpan.end {
			return true
		}
	}
	return false
}

// ArrayTableHeaderOffsets returns, in document order, the byte offset of each
// [[path]] header line whose DECODED path equals path -- so every spelling of
// it ([[package.files]], [["package".files]], [['package'.files]]) answers to
// one query. The slice is freshly allocated.
func (d *Doc) ArrayTableHeaderOffsets(path []string) []int {
	var out []int
	for _, e := range d.exprs {
		if e.kind == unstable.ArrayTable && eq(e.table, path) {
			out = append(out, d.lineStart(e.span.start))
		}
	}
	return out
}
