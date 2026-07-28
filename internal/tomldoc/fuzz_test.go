package tomldoc

// byre's ONE fuzz target, and the reason is specific to this package: tomldoc
// edits a document it did not write and hands the result back to be SAVED
// over the user's file. A wrong answer here is silent -- valid-looking bytes
// that lose or duplicate a key -- and the loss is the user's, not byre's.
// Fuzzing the other parsers (config decode, skill manifests, index.toml) was
// considered and declined: they read, they fail loudly at parse, and a
// rejected file changes nothing on disk.
//
// The invariants, against a random document crossed with a random edit:
//
//  1. the result parses under the STRICT decoder -- the one the product loads
//     config with, which refuses things the expression parser accepts;
//  2. every key the edit did not address survives with its value;
//  3. a refused edit leaves the document byte-identical;
//  4. the value an edit wrote is the value that reads back -- the target key
//     is what invariant 2 excludes, so a renderer that mangles its input
//     would show up nowhere else.

import (
	"fmt"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

// twoMCP is the ownership shape: an array-of-tables whose FIRST element holds
// an inline table. A member belongs to the element that declares it, and
// position is that element's whole identity.
const twoMCP = "[[mcp]]\nname = \"first\"\nheaders = { Authorization = \"tok\", Accept = \"json\" }\n\n[[mcp]]\nname = \"second\"\n"

func FuzzEdit(f *testing.F) {
	seeds := []struct {
		src   string
		op    uint8
		table string
		key   string
		value string
	}{
		{nasty, 0, "", "base", "alpine"},
		{nasty, 0, "env", "FOO", "x"},
		{nasty, 2, "unknown_things", "weird", ""},
		{nasty, 3, "env", "FOO", ""},
		{"defaults = { skip_questions = true }\n", 0, "defaults", "skip_questions", "y"},
		{"defaults = { skip_questions = true, template = 'go' }\n", 2, "defaults", "skip_questions", ""},
		{"d = { a = { b = 1 }, c = {}, z = 0xBB8 }\n", 0, "d.a", "b", "deep"},
		{"d = { a = 1 } # trail\nbase = \"x\"\n", 1, "d", "a", "v"},
		{"[foo]\nbar = { x = 1 }\nbaz = 2\n", 0, "foo.bar", "x", "v"},
		{"# glued\nenv.FOO = \"bar\"\n", 2, "env", "FOO", ""},
		{"[[mcp]]\nname = \"x\"\n", 0, "mcp", "name", "y"},
		{twoMCP, 0, "mcp.headers", "Authorization", "v"},
		{twoMCP, 2, "mcp.headers", "Accept", ""},
		{twoMCP, 1, "mcp.headers", "New", "v"},
		{twoMCP, 0, "mcp.auth", "token", "v"},                             // creation under an array: refused
		{"defaults = { skip_questions = true }\n", 3, "", "defaults", ""}, // RemoveTable over an inline construct
		{"base = \"x\"\n", 0, "", "base", "bad\xc2"},                      // a value with no TOML spelling
		{"base = \"x\"\n", 0, "", "base", "prose\nwith\nlines"},
		{"", 0, "a.b", "c", "v"},
	}
	for _, s := range seeds {
		f.Add(s.src, s.op, s.table, s.key, s.value)
	}

	f.Fuzz(func(t *testing.T, src string, op uint8, table, key, value string) {
		d, err := Load([]byte(src))
		if err != nil {
			return // not a document; Load is the gate, not this test
		}
		var before map[string]any
		if err := toml.Unmarshal([]byte(src), &before); err != nil {
			return // already broken before the edit; nothing to attribute
		}
		var path []string
		if table != "" {
			path = strings.Split(table, ".")
		}
		target := fullPath(path, key)

		switch op % 4 {
		case 0:
			err = d.SetKey(path, key, String(value))
		case 1:
			err = d.SetKey(path, key, InlineStringMap(map[string]string{key: value}))
		case 2:
			err = d.RemoveKey(path, key)
		case 3:
			err = d.RemoveTable(target)
		}
		out := string(d.Bytes())
		if err != nil {
			if out != src {
				t.Fatalf("a refused edit must leave the document alone:\ngot:  %q\nwant: %q", out, src)
			}
			return
		}
		var after map[string]any
		if uerr := toml.Unmarshal([]byte(out), &after); uerr != nil {
			t.Fatalf("edit produced a document the strict decoder refuses: %v\nop=%d target=%v\n%s", uerr, op%4, target, out)
		}
		if a, b := withoutPath(before, target), withoutPath(after, target); a != b {
			t.Fatalf("keys the edit did not address changed\nop=%d target=%v\nbefore: %s\nafter:  %s\nsrc:\n%s\nout:\n%s", op%4, target, a, b, src, out)
		}
		// The key the edit DID address: what was written is what reads back.
		// A renderer that mangles its input -- an escape spelled wrong, a
		// byte substituted -- shows up here and nowhere else, since the
		// target path is exactly what the comparison above excludes.
		if op%4 == 0 {
			got, ok := lookupPath(after, target)
			if !ok || got != value {
				t.Fatalf("the value written is not the value that reads back\ntarget=%v\nwrote: %q\nread:  %#v (found=%v)\nout:\n%s", target, value, got, ok, out)
			}
		}
	})
}

// withoutPath renders m for comparison with the edit's own path taken out --
// including anything that path displaced, since a key cannot coexist with a
// deeper key spelled through it. Empty tables left behind by the deletion are
// pruned: an edit may legitimately be the reason a table exists at all.
func withoutPath(m map[string]any, path []string) string {
	c := cloneAny(m).(map[string]any)
	deletePath(c, path)
	return fmt.Sprintf("%#v", c)
}

// deletePath descends ARRAYS as well as tables, applying the rest of the path
// to every element: a path through an array-of-tables addresses at most one
// element, so an edit that lands in a DIFFERENT element than the one it
// targeted still shows up as a difference between the two sides.
func deletePath(v any, path []string) {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			deletePath(e, path)
		}
	case map[string]any:
		head := path[0]
		if len(path) == 1 {
			delete(t, head)
			return
		}
		switch sub := t[head].(type) {
		case map[string]any:
			deletePath(sub, path[1:])
			if len(sub) == 0 {
				delete(t, head)
			}
		case []any:
			deletePath(sub, path[1:])
		default:
			delete(t, head) // a scalar the deeper path displaces
		}
	}
}

// lookupPath finds the value at a key path, taking the FIRST element that
// resolves it where the path runs through an array -- the same first-match
// resolution the engine itself uses for an existing target.
func lookupPath(v any, path []string) (any, bool) {
	if len(path) == 0 {
		return v, true
	}
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			if got, ok := lookupPath(e, path); ok {
				return got, true
			}
		}
	case map[string]any:
		if sub, ok := t[path[0]]; ok {
			return lookupPath(sub, path[1:])
		}
	}
	return nil, false
}

func cloneAny(v any) any {
	switch t := v.(type) {
	case map[string]any:
		c := make(map[string]any, len(t))
		for k, e := range t {
			c[k] = cloneAny(e)
		}
		return c
	case []any:
		c := make([]any, len(t))
		for i, e := range t {
			c[i] = cloneAny(e)
		}
		return c
	default:
		return v
	}
}
