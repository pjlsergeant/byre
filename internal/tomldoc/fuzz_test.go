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
//  3. a refused edit leaves the document byte-identical.

import (
	"fmt"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

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

func deletePath(m map[string]any, path []string) {
	head := path[0]
	if len(path) == 1 {
		delete(m, head)
		return
	}
	sub, ok := m[head].(map[string]any)
	if !ok {
		delete(m, head) // a scalar the deeper path displaces
		return
	}
	deletePath(sub, path[1:])
	if len(sub) == 0 {
		delete(m, head)
	}
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
