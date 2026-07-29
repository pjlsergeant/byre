package tomldoc

// byre's ONLY fuzzed package, and the reason is specific to this one: tomldoc
// edits a document it did not write and hands the result back to be SAVED
// over the user's file. A wrong answer here is silent -- valid-looking bytes
// that lose or duplicate a key -- and the loss is the user's, not byre's.
// Fuzzing the other parsers (config decode, skill manifests, index.toml) was
// considered and declined: they read, they fail loudly at parse, and a
// rejected file changes nothing on disk.
//
// Two targets, because the two mutation shapes carry different contracts and
// so need different oracles. FuzzEdit crosses a random document with a random
// single-key edit:
//
//  1. the result parses under the STRICT decoder -- the one the product loads
//     config with, which refuses things the expression parser accepts;
//  2. every key the edit did not address survives with its value;
//  3. a refused edit leaves the document byte-identical;
//  4. the value an edit wrote is the value that reads back -- the target key
//     is what invariant 2 excludes, so a renderer that mangles its input
//     would show up nowhere else.
//
// FuzzRemoveTableTree crosses a random document with a whole-tree removal,
// which writes nothing and so can be held to a byte-level oracle instead: see
// its own comment.

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

// FuzzRemoveTableTree fuzzes the whole-tree removal, whose contract is
// stronger than an edit's and needs a stronger oracle to match. "The output
// parses clean" is NOT that oracle: a rebuild that dropped a byte from a line
// it meant to keep, or re-emitted one, would still parse.
//
// The oracle is RETAINED SOURCE SPANS. Every span the removal takes is a whole
// line or a run of them, so what the output may be is exactly the input's line
// sequence with some lines deleted: each surviving line must be byte-identical
// to the input line it came from, and the survivors must appear in the input's
// own order. Alongside that, the semantic pair -- the tree is gone, everything
// else decodes to what it decoded to before -- and idempotence, since a second
// removal has nothing left to target.
func FuzzRemoveTableTree(f *testing.F) {
	seeds := []struct {
		src  string
		name string
	}{
		{nasty, "package"},
		{"[package]\nid = \"x\"\n\n[build]\napt = []\n", "package"},
		{"[\"package\"]\nid = \"x\"\n[build]\n", "package"},
		{"['package']\n\n[build]\n", "package"},
		{"[package] # trailing\nid = \"x\"\n", "package"},
		{"[package]\nid = \"x\"\n\n[build]\n\n[[package.files]]\nsrc = \"a\"\n", "package"},
		{"[[\"package\".files]]\nsrc = \"a\"\n\n[['package'.files]]\nsrc = \"b\"\n", "package"},
		{"package.id = \"x\"\nother = 1\n", "package"},
		{"package = { id = \"x\" }\nother = 1\n", "package"},
		{"meta = { package = { id = \"x\" } }\n", "package"},
		{"# glued\n[package]\nid = \"x\"\n\n# for build\n[build]\napt = []\n", "package"},
		{"[build]\nd = \"\"\"\n[package]\nid = \"no\"\n\"\"\"\n\n[package]\nid = \"x\"\n", "package"},
		{"a = [\n  [1, 2],\n]\n\n[package]\n", "package"},
		{twoMCP, "mcp"},
		{"[package]\n", "package"},
		{"", "package"},
	}
	for _, s := range seeds {
		f.Add(s.src, s.name)
	}

	f.Fuzz(func(t *testing.T, src, name string) {
		d, err := Load([]byte(src))
		if err != nil {
			return // not a document; Load is the gate, not this test
		}
		var before map[string]any
		if err := toml.Unmarshal([]byte(src), &before); err != nil {
			return // already broken before the removal; nothing to attribute
		}
		if err := d.RemoveTableTree(name); err != nil {
			t.Fatalf("RemoveTableTree(%q) on a clean document: %v\nsrc:\n%s", name, err, src)
		}
		out := string(d.Bytes())

		if !linesAreSubsequence(out, src) {
			t.Fatalf("output lines are not retained source lines\nname=%q\nsrc:\n%q\nout:\n%q", name, src, out)
		}
		var after map[string]any
		if uerr := toml.Unmarshal([]byte(out), &after); uerr != nil {
			t.Fatalf("removal produced a document the strict decoder refuses: %v\nname=%q\n%s", uerr, name, out)
		}
		if _, still := after[name]; still && name != "" {
			t.Fatalf("the tree %q survived the removal\nsrc:\n%s\nout:\n%s", name, src, out)
		}
		if a, b := withoutPath(before, []string{name}), withoutPath(after, []string{name}); a != b {
			t.Fatalf("keys outside the tree changed\nname=%q\nbefore: %s\nafter:  %s\nsrc:\n%s\nout:\n%s", name, a, b, src, out)
		}
		// Idempotent: the second pass has nothing to target, so it must not
		// move a byte.
		d2, err := Load([]byte(out))
		if err != nil {
			t.Fatalf("removal output does not reload: %v\n%s", err, out)
		}
		if err := d2.RemoveTableTree(name); err != nil {
			t.Fatalf("second removal: %v\n%s", err, out)
		}
		if again := string(d2.Bytes()); again != out {
			t.Fatalf("removal is not idempotent\nfirst:  %q\nsecond: %q", out, again)
		}
	})
}

// linesAreSubsequence reports whether out's lines are, in order and byte for
// byte, a subsequence of src's. Greedy matching decides it: a line that can be
// matched earlier never has to be matched later, so failing greedily means
// failing outright.
func linesAreSubsequence(out, src string) bool {
	o, s := linesWithTerminators(out), linesWithTerminators(src)
	i := 0
	for _, line := range o {
		for i < len(s) && s[i] != line {
			i++
		}
		if i == len(s) {
			return false
		}
		i++
	}
	return true
}

// linesWithTerminators splits into lines that KEEP their newline, so an empty
// document is no lines rather than one empty one, and a final line without a
// terminator stays distinguishable from one with it. strings.Split cannot say
// either, and both are ordinary shapes for a document a removal emptied or a
// file saved without a trailing newline.
func linesWithTerminators(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			return append(out, s)
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
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
