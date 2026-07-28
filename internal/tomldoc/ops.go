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
	if i, rel, ok := d.inlineTarget(fullPath(table, key)); ok {
		return d.rewriteInline(i, rel, rendered, false)
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
	// its kin in their spelling, after the last of them, spelled relative
	// to the kin's own context.
	if at, ctx, ok := d.dottedKinInsert(table); ok {
		rel := append(append([]string(nil), table[len(ctx):]...), key)
		dotted := fmt.Sprintf("%s = %s\n", encodeKeyPath(rel), rendered)
		return d.splice(span{at, at}, []byte(dotted))
	}
	block := fmt.Sprintf("[%s]\n%s", encodeKeyPath(table), line)
	at := len(d.src)
	return d.splice(span{at, at}, []byte(d.separated(at)+block))
}

// dottedKinInsert finds where a new key of table lands when the table exists
// only via dotted key-values declared in a SHALLOWER context: after the last
// such kin, whose table context is returned so the caller can spell the new
// key relative to it. Only a kin whose declaring context is a strict prefix
// of table qualifies -- a key-value living under a DEEPER header (a
// [sources."other"] subtable) is no anchor: a line inserted there would land
// inside that header's context and change meaning -- sibling subtables
// swallowed the insert before this existed. ok=false
// when no kin exist.
func (d *Doc) dottedKinInsert(table []string) (int, []string, bool) {
	at, ok := -1, false
	var ctx []string
	for _, e := range d.exprs {
		if e.kind != unstable.KeyValue {
			continue
		}
		if len(e.table) >= len(table) || !eq(e.table, table[:len(e.table)]) {
			continue
		}
		full := append(append([]string(nil), e.table...), e.key...)
		if len(full) <= len(table) || !eq(full[:len(table)], table) {
			continue
		}
		at, ctx, ok = d.lineSpan(e.span).end, e.table, true
	}
	return at, ctx, ok
}

// RemoveKey removes key (relative to table) if present: its full line --
// trailing inline comment included -- plus any full-line comments glued
// immediately above. A member of an inline table takes the construct with it
// (see rewriteInline). Absent keys are a no-op.
func (d *Doc) RemoveKey(table []string, key string) error {
	i := d.findKeyValue(table, []string{key})
	if i < 0 {
		if j, rel, ok := d.inlineTarget(fullPath(table, key)); ok {
			return d.rewriteInline(j, rel, "", true)
		}
		return nil
	}
	rm := d.lineSpan(d.exprs[i].span)
	rm.start = d.gluedCommentStart(i, rm.start)
	return d.splice(rm, nil)
}

// fullPath is a table context plus a key: the path an edit addresses.
func fullPath(table []string, key string) []string {
	return append(append([]string(nil), table...), key)
}

// inlineTarget finds the inline-table construct an edit addresses INSIDE:
// the indexed key-value whose value is an inline table and whose own path is
// a strict prefix of full. It returns that expression's index and the target
// path relative to the construct. The innermost such construct wins... which
// is the outermost inline table, since a nested one is not indexed as an
// expression of its own -- its leaves belong to the construct that encloses
// them, and that is the construct an edit rewrites.
func (d *Doc) inlineTarget(full []string) (int, []string, bool) {
	for i, e := range d.exprs {
		if e.kind != unstable.KeyValue || !e.inline {
			continue
		}
		own := append(append([]string(nil), e.table...), e.key...)
		if len(own) < len(full) && eq(own, full[:len(own)]) {
			return i, full[len(own):], true
		}
	}
	return -1, nil, false
}

// rewriteInline applies one member edit to an inline-table construct by
// rewriting THAT construct in house shape (ADR 0044): the inline line goes --
// with the comments glued above it, which describe this config and follow it
// -- and a [table] block carrying the members plus/minus the edit takes its
// place at the end of the document. The block cannot land where the inline
// line sat: a table header claims everything after it, so an in-place swap
// would swallow the following root keys into the new table.
//
// A removal that empties the construct takes the whole thing: an empty
// [table] block still asserts the table the caller asked to drop.
func (d *Doc) rewriteInline(i int, rel []string, rendered string, remove bool) error {
	e := d.exprs[i]
	type member struct {
		path []string
		text string
	}
	var kept []member
	at := -1
	for _, m := range e.members {
		// The edit displaces the member it names, anything nested under it
		// (setting d.a replaces whatever table d.a was), and anything it now
		// sits under (setting d.a.b where d.a was a scalar). Keeping either
		// side would spell a key twice.
		if prefixOf(rel, m.path) || prefixOf(m.path, rel) {
			if at < 0 {
				at = len(kept)
			}
			continue
		}
		kept = append(kept, member{path: m.path, text: string(d.src[m.val.start:m.val.end])})
	}
	if remove && at < 0 {
		return nil // absent member: nothing to rewrite
	}
	if !remove {
		add := member{path: rel, text: rendered}
		if at < 0 {
			at = len(kept)
		}
		kept = append(kept, member{})
		copy(kept[at+1:], kept[at:])
		kept[at] = add
	}

	rm := d.lineSpan(e.span)
	rm.start = d.gluedCommentStart(i, rm.start)
	lead := string(d.src[rm.start:d.lineSpan(e.span).start]) // the glued comment lines
	out := make([]byte, 0, len(d.src)+len(rendered)+16)
	out = append(out, d.src[:rm.start]...)
	out = append(out, d.src[rm.end:]...)
	if len(kept) > 0 {
		var b strings.Builder
		b.WriteString(separatorAt(out, len(out)))
		b.WriteString(lead)
		b.WriteString("[" + encodeKeyPath(append(append([]string(nil), e.table...), e.key...)) + "]\n")
		for _, m := range kept {
			b.WriteString(encodeKeyPath(m.path) + " = " + m.text + "\n")
		}
		out = append(out, b.String()...)
	}
	return d.adopt(out)
}

// prefixOf reports whether a is a prefix of b (equal paths included).
func prefixOf(a, b []string) bool {
	return len(a) <= len(b) && eq(a, b[:len(a)])
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
	return d.ReplaceArrayTableNth(name, matchKey, matchValue, 0, body)
}

// ReplaceArrayTableNth is ReplaceArrayTable on the skip-th matching block.
func (d *Doc) ReplaceArrayTableNth(name, matchKey, matchValue string, skip int, body string) (bool, error) {
	hdr := d.matchArrayTableNth(name, matchKey, matchValue, skip)
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
	return d.RemoveArrayTableNth(name, matchKey, matchValue, 0)
}

// RemoveArrayTableNth is RemoveArrayTable on the skip-th matching block.
func (d *Doc) RemoveArrayTableNth(name, matchKey, matchValue string, skip int) (bool, error) {
	hdr := d.matchArrayTableNth(name, matchKey, matchValue, skip)
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

// HasTable reports whether a [table] header with exactly this path exists.
func (d *Doc) HasTable(table []string) bool {
	for _, e := range d.exprs {
		if e.kind == unstable.Table && eq(e.table, table) {
			return true
		}
	}
	return false
}

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
	return d.matchArrayTableNth(name, matchKey, matchValue, 0)
}

// matchArrayTableNth is matchArrayTable with an OCCURRENCE index: skip is how
// many matching blocks to pass over first. Identity keys are unique in every
// vocabulary but one -- a layer may hold both a `remove = true` marker and a
// binding for the same `[[ports]]` container port -- and there both blocks
// answer to one selector. Position within the matching set tells them apart
// without a second selector key, so a caller can replace exactly the block it
// means and leave its neighbour's bytes and comments alone (ADR 0044).
func (d *Doc) matchArrayTableNth(name, matchKey, matchValue string, skip int) int {
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
				if skip == 0 {
					return hdr
				}
				skip--
				hdr = -1 // this block is spent; don't match it twice
			}
		}
	}
	return -1
}

// blockEnd is the offset just past a header expression's block: through its
// last key-value or DESCENDANT subtable header (a `[mcp.headers]` under a
// `[[mcp]]` is that block's content: stopping at every header left a replaced
// block's subtable behind, where it re-attached to the replacement or a
// peer), plus any comment run GLUED to
// that last expression. Interior comments and blank lines are block content
// wherever they sit; a trailing comment separated by a blank line is NOT the
// block's -- it belongs to whatever follows, and replacing or removing the
// block must not consume it.
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
func (d *Doc) separated(off int) string { return separatorAt(d.src, off) }

// separatorAt is separated against any bytes -- a rewrite computes its
// insertion against the document it is BUILDING, not the one on the Doc.
func separatorAt(src []byte, off int) string {
	if off == 0 {
		return ""
	}
	if off >= 2 && src[off-1] == '\n' && src[off-2] == '\n' {
		return ""
	}
	if src[off-1] == '\n' {
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
