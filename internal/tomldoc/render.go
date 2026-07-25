package tomldoc

// byre's house rendering for NEW TOML content -- the designed emission P7
// demands (ADR 0044). Existing bytes are never re-rendered (that's the
// splice engine's guarantee); these shapes apply to content byre itself
// writes: a fresh file, an appended block, an edited entry.

import (
	"fmt"
	"sort"
	"strings"
)

// String renders a TOML string value. Prose -- anything with a newline --
// comes out as a multiline literal string (”'), the shape the format
// designed for it: no escape processing, so backslashes in code snippets
// survive verbatim and the file stays human-readable. Text a literal string
// cannot carry (a ”' run, control characters, a trailing quote hugging the
// delimiter) falls back to the escaped basic string.
func String(s string) string {
	if strings.Contains(s, "\n") && literalSafe(s) {
		// TOML trims only the newline immediately after the opening
		// delimiter, so the value round-trips exactly; when the prose
		// doesn't end in a newline the closing delimiter hugs the last
		// character rather than inventing one.
		return "'''\n" + s + "'''"
	}
	return escaped(s)
}

// literalSafe reports whether s can ride a multiline literal string
// unescaped: no ”' run, no quote adjacent to the delimiters, and no control
// characters beyond newline and tab.
func literalSafe(s string) bool {
	if strings.Contains(s, "'''") {
		return false
	}
	if strings.HasPrefix(s, "'") || strings.HasSuffix(strings.TrimSuffix(s, "\n"), "'") {
		return false
	}
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// escaped renders a basic (double-quoted, escape-processed) TOML string.
func escaped(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				b.WriteString(fmt.Sprintf(`\u%04X`, r))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Bool renders a TOML boolean.
func Bool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// Int renders a TOML integer.
func Int(v int) string { return fmt.Sprintf("%d", v) }

// StringArray renders a single-line string array.
func StringArray(vs []string) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = escaped(v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// Lines renders a multi-line string array, one element per line -- the house
// shape for raw blocks (dockerfile_pre/post, run_args), whose elements are
// long commands.
func Lines(vs []string) string {
	if len(vs) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteString("[\n")
	for _, v := range vs {
		b.WriteString("  " + escaped(v) + ",\n")
	}
	b.WriteString("]")
	return b.String()
}

// InlineStringMap renders an inline table of string values, keys sorted.
func InlineStringMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = encodeKey(k) + " = " + escaped(m[k])
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// KV renders one `key = value` line for a block body.
func KV(key, rendered string) string {
	return encodeKey(key) + " = " + rendered + "\n"
}
