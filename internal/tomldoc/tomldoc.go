// Package tomldoc is byre's style-preserving TOML document editor -- the one
// place byre WRITES config files (ADR 0044). It exists because config files
// are shared-custody documents: byre writes them and users may hand-edit
// them, so a save must not clobber what it didn't touch. The guarantee is
// structural, not best-effort: every mutation is a byte-range splice against
// the original input, so bytes outside the edited span -- comments,
// formatting, ordering, oddities -- are preserved IDENTICALLY by
// construction.
//
// Parsing rides pelletier/go-toml's unstable parser (expression-level AST
// with byte ranges over the input); this package owns everything above it --
// the expression index, the splice engine, and byre's house rendering for
// NEW content (render.go). The contract with callers:
//
//   - An edit rewrites the smallest span that expresses it (a value, a line,
//     a block). An edited entry comes out in byre's house shape; an exotic
//     construct is normalized only when the edit itself targets it.
//   - New entries land after the last entry of the same kind when one
//     exists, else in the position TOML requires (root keys before the first
//     table header) or at the end.
//   - A removal takes the entry's line(s), its trailing inline comment, and
//     any full-line comments glued immediately above it (no blank line
//     between) -- a comment attached to config describes that config.
//
// The index is rebuilt after every splice: documents are small, and
// re-parsing beats offset bookkeeping at every callsite where it could
// silently drift.
package tomldoc

import (
	"fmt"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// span is a half-open byte range [start, end) into Doc.src.
type span struct{ start, end int }

func (s span) len() int { return s.end - s.start }

// expr is one indexed top-level expression.
type expr struct {
	kind unstable.Kind // Comment, Table, ArrayTable, or KeyValue
	// table is the enclosing table context: nil at root, the header path
	// after a [table] / [[table]] expression. For Table/ArrayTable exprs it
	// is the header's OWN path.
	table []string
	// key is a KeyValue's full key path within its table (dotted keys
	// flattened).
	key []string
	// span covers the expression's own bytes (header line for tables,
	// key-through-value for key-values, the comment text for comments).
	span span
	// valSpan covers a KeyValue's value bytes (computed even where the
	// parser reports no range for container values).
	valSpan span
	// strValue is the decoded value for scalar values ("" otherwise): the
	// string content for String kinds, raw text for Bool, and CANONICAL
	// DECIMAL for Integer (the raw token may be spelled 3_000 or 0xBB8) --
	// enough for identity matching (blocks match by a name key or a port
	// number).
	strValue string
	// inline marks a value that is an inline table, and members indexes what
	// lives inside it: an edit addressing one of those members targets the
	// construct, and the construct is what gets rewritten.
	inline  bool
	members []inlineMember
}

// inlineMember is one leaf key-value inside an inline table. path is the key
// path relative to the construct's own key, FLATTENED: a nested inline table
// that holds members contributes its leaves (`{ a = { b = 1 } }` yields
// a.b), because the house shape spells nesting with dotted keys under one
// header. A member with no leaves of its own -- `{}` -- stays a leaf so that
// the table it declares survives the rewrite.
type inlineMember struct {
	path []string
	val  span
}

// Doc is a parsed TOML document accepting style-preserving edits.
type Doc struct {
	src   []byte
	exprs []expr
}

// Load parses src into an editable document. The input is copied; the caller
// keeps ownership of its slice.
func Load(src []byte) (*Doc, error) {
	d := &Doc{src: append([]byte(nil), src...)}
	if err := d.reindex(); err != nil {
		return nil, err
	}
	return d, nil
}

// Bytes returns the document's current content.
func (d *Doc) Bytes() []byte {
	return append([]byte(nil), d.src...)
}

// reindex rebuilds the expression index from d.src.
func (d *Doc) reindex() error {
	p := &unstable.Parser{KeepComments: true}
	p.Reset(d.src)
	var exprs []expr
	var table []string
	for p.NextExpression() {
		e := p.Expression()
		switch e.Kind {
		case unstable.Comment:
			exprs = append(exprs, expr{kind: e.Kind, table: table, span: d.rawSpan(e.Raw)})
		case unstable.Table, unstable.ArrayTable:
			path, hdr := d.headerPathAndSpan(e)
			table = path
			exprs = append(exprs, expr{kind: e.Kind, table: path, span: hdr})
		case unstable.KeyValue:
			ex, err := d.keyValueExpr(e, table)
			if err != nil {
				return err
			}
			exprs = append(exprs, ex)
		}
	}
	if err := p.Error(); err != nil {
		return err
	}
	d.exprs = exprs
	return nil
}

func (d *Doc) rawSpan(r unstable.Range) span {
	return span{int(r.Offset), int(r.Offset) + int(r.Length)}
}

// headerPathAndSpan derives a [table] / [[table]] expression's key path and
// its header-line span. The parser reports no Raw range for header
// expressions, so the span is the full line containing the header keys.
func (d *Doc) headerPathAndSpan(e *unstable.Node) ([]string, span) {
	var path []string
	lo, hi := -1, -1
	for it := e.Key(); it.Next(); {
		k := it.Node()
		path = append(path, string(k.Data))
		s := d.rawSpan(k.Raw)
		if lo < 0 || s.start < lo {
			lo = s.start
		}
		if s.end > hi {
			hi = s.end
		}
	}
	return path, d.lineSpan(span{lo, hi})
}

// keyValueExpr indexes one key-value: full key path (dotted keys flattened
// under the current table) and the value span. The parser reports no Raw for
// container values (arrays, inline tables), so the value span is anchored
// from after the '=' to the expression's end.
func (d *Doc) keyValueExpr(e *unstable.Node, table []string) (expr, error) {
	whole := d.rawSpan(e.Raw)
	var key []string
	keyEnd := -1
	for it := e.Key(); it.Next(); {
		k := it.Node()
		key = append(key, string(k.Data))
		if s := d.rawSpan(k.Raw); s.end > keyEnd {
			keyEnd = s.end
		}
	}
	v := e.Value()
	val, err := d.valueSpan(v, whole, keyEnd, key)
	if err != nil {
		return expr{}, err
	}
	ex := expr{
		kind:    unstable.KeyValue,
		table:   table,
		key:     key,
		span:    whole,
		valSpan: val,
	}
	if v.Kind == unstable.InlineTable {
		ex.inline = true
		ms, err := d.inlineMembers(v, nil)
		if err != nil {
			return expr{}, err
		}
		ex.members = ms
	}
	switch v.Kind {
	case unstable.String, unstable.Bool:
		ex.strValue = string(v.Data)
	case unstable.Integer:
		// The parser keeps the raw token (`3_000`, `0xBB8`); identity
		// matching compares canonical decimal, so normalize numerically --
		// callers pass fmt.Sprintf("%d", ...) identities.
		if n, err := strconv.ParseInt(strings.ReplaceAll(string(v.Data), "_", ""), 0, 64); err == nil {
			ex.strValue = strconv.FormatInt(n, 10)
		} else {
			ex.strValue = string(v.Data)
		}
	}
	return ex, nil
}

// valueSpan locates the value bytes of one key-value, given the expression's
// whole span and the end of its key. Container values get their span from '='
// to the expression's end, BY KIND: the parser reports no range for an Array
// but a one-byte range (the opening brace) for an InlineTable -- trusting the
// length made the second edit of any inline-table value splice a single byte
// into invalid TOML (probed).
func (d *Doc) valueSpan(v *unstable.Node, whole span, keyEnd int, key []string) (span, error) {
	val := d.rawSpan(v.Raw)
	if v.Kind != unstable.Array && v.Kind != unstable.InlineTable && val.len() != 0 {
		return val, nil
	}
	start := keyEnd
	for start < whole.end && d.src[start] != '=' {
		start++
	}
	start++ // past '='
	for start < whole.end && (d.src[start] == ' ' || d.src[start] == '\t') {
		start++
	}
	if start >= whole.end {
		return span{}, fmt.Errorf("tomldoc: cannot locate value of %v", key)
	}
	return span{start, whole.end}, nil
}

// inlineMembers indexes the leaf key-values inside an inline table value,
// pathed relative to the construct (see inlineMember). Unlike a top-level
// key-value, a member's own expression node carries a Raw range covering key
// through value, so each member's bytes are addressable.
func (d *Doc) inlineMembers(v *unstable.Node, prefix []string) ([]inlineMember, error) {
	var out []inlineMember
	for it := v.Children(); it.Next(); {
		c := it.Node()
		if c.Kind != unstable.KeyValue {
			continue
		}
		whole := d.rawSpan(c.Raw)
		var key []string
		keyEnd := -1
		for kit := c.Key(); kit.Next(); {
			k := kit.Node()
			key = append(key, string(k.Data))
			if s := d.rawSpan(k.Raw); s.end > keyEnd {
				keyEnd = s.end
			}
		}
		path := append(append([]string(nil), prefix...), key...)
		cv := c.Value()
		vs, err := d.valueSpan(cv, whole, keyEnd, path)
		if err != nil {
			return nil, err
		}
		if cv.Kind == unstable.InlineTable {
			sub, err := d.inlineMembers(cv, path)
			if err != nil {
				return nil, err
			}
			if len(sub) > 0 {
				out = append(out, sub...)
				continue
			}
		}
		out = append(out, inlineMember{path: path, val: vs})
	}
	return out, nil
}

// splice replaces [s.start, s.end) with repl and reindexes.
func (d *Doc) splice(s span, repl []byte) error {
	out := make([]byte, 0, len(d.src)-s.len()+len(repl))
	out = append(out, d.src[:s.start]...)
	out = append(out, repl...)
	out = append(out, d.src[s.end:]...)
	return d.adopt(out)
}

// adopt makes out the document's content once it survives both parsers, and
// restores the previous bytes when it doesn't. The syntactic check is the
// index rebuild; the semantic one is the strict decoder, which is the parser
// the product's own loader uses. They disagree: the expression parser accepts
// a key defined twice (a second [defaults] block beside an inline
// `defaults = {...}`), so syntax alone would let byre write a file no later
// command can load. The decode is schema-agnostic -- a document byre doesn't
// understand stays editable.
func (d *Doc) adopt(out []byte) error {
	old := d.src
	d.src = out
	if err := d.reindex(); err != nil {
		d.src = old
		_ = d.reindex()
		return fmt.Errorf("tomldoc: edit produced invalid TOML (internal bug): %w", err)
	}
	if err := semanticCheck(out); err != nil {
		if semanticCheck(old) == nil {
			d.src = old
			_ = d.reindex()
			return fmt.Errorf("tomldoc: edit produced invalid TOML (internal bug): %w", err)
		}
		// The document was already semantically broken before this edit, so
		// the edit is not the cause; blaming it would send the user hunting a
		// byre bug, and refusing would strand the file unrepairable.
	}
	return nil
}

func semanticCheck(src []byte) error {
	var m map[string]any
	return toml.Unmarshal(src, &m)
}

// ---- line geometry ---------------------------------------------------------

// lineSpan expands s to full line boundaries: from the start of the line
// containing s.start to the end of the line containing s.end (INCLUDING the
// trailing newline when present). This is the removal unit -- it carries an
// entry's trailing inline comment with it.
func (d *Doc) lineSpan(s span) span {
	start := s.start
	for start > 0 && d.src[start-1] != '\n' {
		start--
	}
	end := s.end
	for end < len(d.src) && d.src[end] != '\n' {
		end++
	}
	if end < len(d.src) {
		end++ // include the newline
	}
	return span{start, end}
}

// lineStart returns the offset of the start of the line containing off.
func (d *Doc) lineStart(off int) int {
	for off > 0 && d.src[off-1] != '\n' {
		off--
	}
	return off
}

// blankLineBetween reports whether the bytes between two offsets contain a
// blank line (two newlines with only whitespace between).
func (d *Doc) blankLineBetween(a, b int) bool {
	sawNL := false
	for i := a; i < b && i < len(d.src); i++ {
		switch d.src[i] {
		case '\n':
			if sawNL {
				return true
			}
			sawNL = true
		case ' ', '\t', '\r':
		default:
			sawNL = false
		}
	}
	return false
}

// eq reports whether two key paths are equal.
func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
