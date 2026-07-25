package tomldoc

// The mutation surface. Every operation is one splice (tomldoc.go) computed
// from the current index; rendered content comes from the caller (render.go
// provides byre's house shapes). Table paths are nil for root.

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2/unstable"
)

// SetKey sets key (relative to table; nil table = root) to the rendered
// value. An existing key's value span is replaced in place -- layout, line
// position, and any trailing inline comment survive. A new key lands after
// the last key of the same table (or right after its header), a new ROOT key
// before the first table header (TOML's own requirement), and a key in an
// absent table appends a fresh [table] block at the end.
func (d *Doc) SetKey(table []string, key string, rendered string) error {
	keyPath := []string{key}
	if i := d.findKeyValue(table, keyPath); i >= 0 {
		return d.splice(d.exprs[i].valSpan, []byte(rendered))
	}
	line := fmt.Sprintf("%s = %s\n", encodeKey(key), rendered)
	if at, ok := d.insertPointInTable(table); ok {
		return d.splice(span{at, at}, []byte(line))
	}
	if table == nil {
		at := d.rootInsertPoint()
		return d.splice(span{at, at}, []byte(line))
	}
	// No [table] header, but the table may exist through DOTTED spellings
	// (`env.FOO = "x"` at root). Emitting a [table] header then would
	// redefine the implicit table -- invalid TOML -- so the new key joins
	// its kin in their spelling, after the last of them.
	if at, ok := d.dottedKinInsert(table); ok {
		dotted := fmt.Sprintf("%s = %s\n", encodeKeyPath(append(append([]string(nil), table...), key)), rendered)
		return d.splice(span{at, at}, []byte(dotted))
	}
	block := fmt.Sprintf("[%s]\n%s", encodeKeyPath(table), line)
	at := len(d.src)
	return d.splice(span{at, at}, []byte(d.separated(at)+block))
}

// dottedKinInsert finds where a new key of table lands when the table exists
// only via dotted key-values declared in a SHALLOWER context: after the last
// such kin. ok=false when no kin exist.
func (d *Doc) dottedKinInsert(table []string) (int, bool) {
	at, ok := -1, false
	for _, e := range d.exprs {
		if e.kind != unstable.KeyValue || eq(e.table, table) {
			continue
		}
		full := append(append([]string(nil), e.table...), e.key...)
		if len(full) <= len(table) {
			continue
		}
		if eq(full[:len(table)], table) {
			at, ok = d.lineSpan(e.span).end, true
		}
	}
	return at, ok
}

// RemoveKey removes key (relative to table) if present: its full line --
// trailing inline comment included -- plus any full-line comments glued
// immediately above. Absent keys are a no-op.
func (d *Doc) RemoveKey(table []string, key string) error {
	i := d.findKeyValue(table, []string{key})
	if i < 0 {
		return nil
	}
	rm := d.lineSpan(d.exprs[i].span)
	rm.start = d.gluedCommentStart(i, rm.start)
	return d.splice(rm, nil)
}

// AppendArrayTable appends one [[name]] block with the rendered body (the
// body is the key-value lines only, newline-terminated). It lands after the
// last existing [[name]] block, else at the end of the document.
func (d *Doc) AppendArrayTable(name string, body string) error {
	block := fmt.Sprintf("[[%s]]\n%s", encodeKeyPath([]string{name}), body)
	at := len(d.src)
	if last := d.lastArrayTable(name); last >= 0 {
		at = d.blockEnd(last)
	}
	return d.splice(span{at, at}, []byte(d.separated(at)+block))
}

// ReplaceArrayTable replaces the [[name]] block whose matchKey equals
// matchValue with a freshly rendered block (header + body): the edited entry
// comes out in house shape; its glued comments above are kept. False when no
// block matches.
func (d *Doc) ReplaceArrayTable(name, matchKey, matchValue, body string) (bool, error) {
	hdr := d.matchArrayTable(name, matchKey, matchValue)
	if hdr < 0 {
		return false, nil
	}
	block := fmt.Sprintf("[[%s]]\n%s", encodeKeyPath([]string{name}), body)
	s := span{d.exprs[hdr].span.start, d.blockEnd(hdr)}
	return true, d.splice(s, []byte(block))
}

// RemoveArrayTable removes the [[name]] block whose matchKey equals
// matchValue, glued comments above included. False when no block matches.
func (d *Doc) RemoveArrayTable(name, matchKey, matchValue string) (bool, error) {
	hdr := d.matchArrayTable(name, matchKey, matchValue)
	if hdr < 0 {
		return false, nil
	}
	s := span{d.gluedCommentStart(hdr, d.exprs[hdr].span.start), d.blockEnd(hdr)}
	return true, d.splice(s, nil)
}

// RemoveTable removes an entire [table] block (header, body, glued comments
// above). Absent tables are a no-op.
func (d *Doc) RemoveTable(table []string) error {
	for i, e := range d.exprs {
		if e.kind == unstable.Table && eq(e.table, table) {
			s := span{d.gluedCommentStart(i, e.span.start), d.blockEnd(i)}
			return d.splice(s, nil)
		}
	}
	return nil
}

// HasKey reports whether key exists (relative to table).
func (d *Doc) HasKey(table []string, key string) bool {
	return d.findKeyValue(table, []string{key}) >= 0
}

// ---- lookup helpers --------------------------------------------------------

// findKeyValue finds a KeyValue expression by table context and key path.
// Dotted spellings match their flattened path: `a.b = 1` at root is found by
// table=["a"], key=["b"]... which callers express as table ["a"], key "b" --
// the index stores the key path relative to the DECLARING table, so both the
// dotted-at-root and under-header spellings are checked.
func (d *Doc) findKeyValue(table []string, key []string) int {
	want := append(append([]string(nil), table...), key...)
	for i, e := range d.exprs {
		if e.kind != unstable.KeyValue {
			continue
		}
		full := append(append([]string(nil), e.table...), e.key...)
		if eq(full, want) {
			return i
		}
	}
	return -1
}

// insertPointInTable finds where a NEW key of table lands: after the last
// KeyValue in the table's block, or right after its header line. ok=false
// when the table (or root content) has no anchor yet. For root (nil), the
// anchor is the last root-level KeyValue.
func (d *Doc) insertPointInTable(table []string) (int, bool) {
	at, ok := -1, false
	for _, e := range d.exprs {
		switch e.kind {
		case unstable.Table, unstable.ArrayTable:
			if table != nil && eq(e.table, table) && e.kind == unstable.Table {
				at, ok = d.lineSpan(e.span).end, true
			}
		case unstable.KeyValue:
			if eq(e.table, table) {
				at, ok = d.lineSpan(e.span).end, true
			}
		}
	}
	return at, ok
}

// rootInsertPoint is where a new root key lands when no root keys exist yet:
// before the first table header (root keys must precede it) and its glued
// comments, else end of document.
func (d *Doc) rootInsertPoint() int {
	for i, e := range d.exprs {
		if e.kind == unstable.Table || e.kind == unstable.ArrayTable {
			return d.gluedCommentStart(i, d.lineStart(e.span.start))
		}
	}
	return len(d.src)
}

// lastArrayTable finds the index of the last [[name]] header expression.
func (d *Doc) lastArrayTable(name string) int {
	last := -1
	for i, e := range d.exprs {
		if e.kind == unstable.ArrayTable && eq(e.table, []string{name}) {
			last = i
		}
	}
	return last
}

// matchArrayTable finds the [[name]] header whose block contains a KeyValue
// matchKey with string value matchValue. Only a DIRECT child key-value
// matches -- a same-named key inside a descendant subtable ([mcp.headers])
// is that subtable's, not the block's identity -- and a descendant header
// doesn't end the candidate block (it's block content, see blockEnd).
func (d *Doc) matchArrayTable(name, matchKey, matchValue string) int {
	want := []string{name}
	hdr := -1
	for i, e := range d.exprs {
		switch e.kind {
		case unstable.Table:
			if !(len(e.table) > 1 && e.table[0] == name) {
				hdr = -1
			}
		case unstable.ArrayTable:
			hdr = -1
			if eq(e.table, want) {
				hdr = i
			}
		case unstable.KeyValue:
			if hdr >= 0 && eq(e.table, want) && len(e.key) == 1 && e.key[0] == matchKey && e.strValue == matchValue {
				return hdr
			}
		}
	}
	return -1
}

// blockEnd is the offset just past a header expression's block: through its
// last key-value or DESCENDANT subtable header (a `[mcp.headers]` under a
// `[[mcp]]` is that block's content -- review finding 2026-07-25, round 2:
// stopping at every header left a replaced block's subtable behind, where it
// re-attached to the replacement or a peer), plus any comment run GLUED to
// that last expression. Interior comments and blank lines are block content
// wherever they sit; a trailing comment separated by a blank line is NOT the
// block's -- it belongs to whatever follows, and replacing or removing the
// block must not consume it (round 1 finding).
func (d *Doc) blockEnd(hdr int) int {
	own := d.exprs[hdr].table
	last, stop := hdr, len(d.exprs)
	for i := hdr + 1; i < len(d.exprs); i++ {
		e := d.exprs[i]
		switch e.kind {
		case unstable.Table, unstable.ArrayTable:
			if len(e.table) > len(own) && eq(e.table[:len(own)], own) {
				last = i // descendant subtable header: block content
				continue
			}
			stop = i
		case unstable.KeyValue:
			last = i
			continue
		default:
			continue
		}
		break
	}
	end := d.lineSpan(d.exprs[last].span).end
	for j := last + 1; j < stop; j++ {
		e := d.exprs[j]
		if e.kind != unstable.Comment || d.blankLineBetween(d.exprs[j-1].span.end, e.span.start) {
			break
		}
		end = d.lineSpan(e.span).end
	}
	return end
}

// gluedCommentStart walks upward from expression i over full-line comment
// expressions that are GLUED to it (no blank line between), returning the
// earliest line start to include; fallback is the given default.
func (d *Doc) gluedCommentStart(i int, def int) int {
	start := def
	at := i
	for j := i - 1; j >= 0; j-- {
		e := d.exprs[j]
		if e.kind != unstable.Comment {
			break
		}
		// Full-line comment only (an inline comment belongs to its line).
		ls := d.lineStart(e.span.start)
		if !isCommentLine(d.src[ls:e.span.start]) {
			break
		}
		if d.blankLineBetween(e.span.end, d.exprs[at].span.start) {
			break
		}
		start = ls
		at = j
	}
	return start
}

// isCommentLine reports whether the bytes before a comment's '#' on its line
// are only whitespace.
func isCommentLine(prefix []byte) bool {
	for _, b := range prefix {
		if b != ' ' && b != '\t' {
			return false
		}
	}
	return true
}

// separated returns the separator needed before appending a block at off: a
// blank line when the document has content that doesn't already end with one.
func (d *Doc) separated(off int) string {
	if off == 0 {
		return ""
	}
	if off >= 2 && d.src[off-1] == '\n' && d.src[off-2] == '\n' {
		return ""
	}
	if d.src[off-1] == '\n' {
		return "\n"
	}
	return "\n\n"
}

// encodeKey renders one key segment, quoting when it isn't a bare TOML key.
func encodeKey(k string) string {
	if isBareKey(k) {
		return k
	}
	return fmt.Sprintf("%q", k)
}

// encodeKeyPath renders a dotted key path with per-segment quoting.
func encodeKeyPath(path []string) string {
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = encodeKey(p)
	}
	return strings.Join(parts, ".")
}

func isBareKey(k string) bool {
	if k == "" {
		return false
	}
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
