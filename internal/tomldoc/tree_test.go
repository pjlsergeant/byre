package tomldoc

import (
	"strings"
	"testing"
)

// RemoveTableTree is byte-exact by construction, so the assertions are too:
// what these pin is which SPANS the removal claims, and the surviving bytes
// are the whole evidence.
func TestRemoveTableTree(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"quoted header",
			"[\"package\"]\nid = \"x\"\n\n[build]\napt = []\n",
			"\n[build]\napt = []\n",
		},
		{
			"empty quoted header",
			"['package']\n\n[build]\napt = []\n",
			"\n[build]\napt = []\n",
		},
		{
			"empty table alone",
			"[package]\n",
			"",
		},
		{
			"trailing comment on the header",
			"[package] # the frozen core\nid = \"x\"\n\n[build]\napt = []\n",
			"\n[build]\napt = []\n",
		},
		{
			// A files list that resumes AFTER a foreign table is still the
			// package tree; a scanner that ended the section at [build] left it
			// for the stage-2 parser to choke on.
			"non-contiguous files blocks, quoted and bare",
			"[package]\nid = \"x\"\n\n[build]\napt = []\n\n[[\"package\".files]]\nsrc = \"a\"\n\n[[package.files]]\nsrc = \"b\"\n",
			"\n[build]\napt = []\n\n\n",
		},
		{
			"nested subtable declared before the parent",
			"[package.meta]\nz = 1\n\n[package]\nid = \"x\"\n",
			"\n",
		},
		{
			"top-level dotted key",
			"package.id = \"x\"\nother = 1\n\n[build]\napt = []\n",
			"other = 1\n\n[build]\napt = []\n",
		},
		{
			"inline table",
			"package = { id = \"x\" }\nother = 1\n",
			"other = 1\n",
		},
		{
			// The root of meta.package is meta: the construct belongs to meta,
			// and a package strip has no business rewriting it.
			"nested inline member is not the package tree",
			"meta = { package = { id = \"x\" } }\nother = 1\n",
			"meta = { package = { id = \"x\" } }\nother = 1\n",
		},
		{
			"header-shaped text inside a multiline string is data",
			"[build]\nd = \"\"\"\n[package]\nid = \"nope\"\n\"\"\"\n\n[package]\nid = \"x\"\n",
			"[build]\nd = \"\"\"\n[package]\nid = \"nope\"\n\"\"\"\n\n",
		},
		{
			// The comment above [package] describes it and goes; the one above
			// [build] is separated by a blank line from the removed block, so
			// it belongs to [build] and survives byte for byte.
			"glued comments stop at the blank line",
			"# describes the core\n[package]\nid = \"x\"\n\n# describes the build\n[build]\napt = []\n",
			"\n# describes the build\n[build]\napt = []\n",
		},
		{
			// The other side of the same rule: no blank line means the comment
			// is glued to the package block's last key, and goes with it.
			"a comment glued to the block's tail goes with the block",
			"[package]\nid = \"x\"\n# glued to the key above\n[build]\napt = []\n",
			"[build]\napt = []\n",
		},
		{
			"absent tree is a no-op",
			"[build]\napt = []\n",
			"[build]\napt = []\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Load([]byte(c.src))
			if err != nil {
				t.Fatal(err)
			}
			if err := d.RemoveTableTree("package"); err != nil {
				t.Fatal(err)
			}
			if got := string(d.Bytes()); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestHasTableTree(t *testing.T) {
	for _, c := range []struct {
		src  string
		want bool
	}{
		{"[package]\nid = \"x\"\n", true},
		{"[\"package\"]\n", true}, // empty AND quoted: still declared
		{"['package']\n", true},   // ditto, literal quotes
		{"[package.files]\nsrc = 1\n", true},
		{"[[package.files]]\nsrc = \"a\"\n", true},
		{"package.id = \"x\"\n", true},
		{"package = { id = \"x\" }\n", true},
		{"meta = { package = { id = \"x\" } }\n", false},
		{"[build]\npackage.id = \"x\"\n", false}, // root is build
		{"description = \"x\"\n", false},
		{"[build]\nd = \"\"\"\n[package]\n\"\"\"\n", false}, // data, not a header
	} {
		d, err := Load([]byte(c.src))
		if err != nil {
			t.Fatalf("%q: %v", c.src, err)
		}
		if got := d.HasTableTree("package"); got != c.want {
			t.Errorf("HasTableTree(%q) = %v, want %v", c.src, got, c.want)
		}
	}
}

func TestFirstTableHeaderOffset(t *testing.T) {
	for _, c := range []struct {
		name, src, pre string
		ok             bool
	}{
		{"no header", "a = 1\nb = 2\n", "a = 1\nb = 2\n", false},
		{"header first", "[build]\napt = []\n", "", true},
		{"keys then header", "a = 1\n\n[build]\n", "a = 1\n\n", true},
		{"comment glued above the header stays in the preamble", "# hi\n[build]\n", "# hi\n", true},
		{
			// A continuation line of a multiline array can BEGIN with '[': the
			// line scanner this replaced split the document there and emitted
			// half an array before the [package] header.
			"multiline array continuation is not a header",
			"a = [\n  [1, 2],\n  [3, 4],\n]\n\n[build]\n",
			"a = [\n  [1, 2],\n  [3, 4],\n]\n\n",
			true,
		},
		{
			"header-shaped line inside a multiline string is not a header",
			"a = \"\"\"\n[build]\n\"\"\"\n\n[real]\n",
			"a = \"\"\"\n[build]\n\"\"\"\n\n",
			true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Load([]byte(c.src))
			if err != nil {
				t.Fatal(err)
			}
			at, ok := d.FirstTableHeaderOffset()
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if !ok {
				return
			}
			if got := c.src[:at]; got != c.pre {
				t.Errorf("preamble = %q, want %q", got, c.pre)
			}
		})
	}
}

func TestInValueSpan(t *testing.T) {
	src := "a = \"\"\"\n# inside\n\"\"\"\n# outside\nb = [\n  # in array\n  1,\n]\n"
	d, err := Load([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		marker string
		want   bool
	}{
		{"# inside", true},
		{"# outside", false},
		{"  # in array", true},
	} {
		at := strings.Index(src, c.marker)
		if at < 0 {
			t.Fatalf("marker %q not in fixture", c.marker)
		}
		if got := d.InValueSpan(at); got != c.want {
			t.Errorf("InValueSpan(%q) = %v, want %v", c.marker, got, c.want)
		}
	}
}

func TestArrayTableHeaderOffsets(t *testing.T) {
	src := "[[package.files]]\nsrc = \"a\"\n\n[build]\nd = \"\"\"\n[[package.files]]\n\"\"\"\n\n[[\"package\".files]]\nsrc = \"b\"\n\n[['package'.files]]\nsrc = \"c\"\n\n[[package.other]]\nsrc = \"d\"\n"
	d, err := Load([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := d.ArrayTableHeaderOffsets([]string{"package", "files"})
	want := []int{
		0,
		strings.Index(src, "[[\"package\".files]]"),
		strings.Index(src, "[['package'.files]]"),
	}
	if len(got) != len(want) {
		t.Fatalf("offsets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("offsets = %v, want %v", got, want)
		}
	}
}
