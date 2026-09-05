package packages

import (
	"strings"
	"testing"
)

func TestParseManifestCore(t *testing.T) {
	raw := []byte(`
[package]
id = "pete/claude"
version = "1.0.0"
kind = "skill"
package_api = 1
requires_byre = ">=0.2.0"
description = "hi"

[build]
apt = ["curl"]
unknown_future_key = true
`)
	m, ok, err := ParseManifestCore(raw)
	if err != nil || !ok {
		t.Fatalf("ParseManifestCore: ok=%v err=%v", ok, err)
	}
	if m.ID != "pete/claude" || m.PackageAPI != 1 || m.RequiresByre != ">=0.2.0" {
		t.Fatalf("manifest: %+v", m)
	}
}

func TestParseManifestCoreAbsent(t *testing.T) {
	m, ok, err := ParseManifestCore([]byte(`description = "x"`))
	if err != nil || ok || m.ID != "" {
		t.Fatalf("want absent: ok=%v m=%+v err=%v", ok, m, err)
	}
}

// An EMPTY package table is present, however it is spelled. The text search
// this replaced saw only the one unquoted spelling, so `["package"]` with
// nothing under it read as "no package block at all" -- and an installed
// package with an empty core would have been treated as a local directory.
func TestParseManifestCoreEmptyTableIsPresent(t *testing.T) {
	for _, src := range []string{
		"[package]\n",
		"[\"package\"]\n",
		"['package']\n",
		"[\"package\".meta]\nnote = \"x\"\n",
		"[[package.files]]\nsrc = \"a\"\ndest = \"a\"\nsha256 = \"\"\n",
	} {
		m, ok, err := ParseManifestCore([]byte(src))
		if err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		if !ok {
			t.Errorf("%q: reported absent; an empty package tree is still declared", src)
		}
		if m != (Manifest{}) {
			t.Errorf("%q: core = %+v, want zero", src, m)
		}
	}
}

// Dotted and inline spellings of the core decode like any other, and are
// present.
func TestParseManifestCoreDottedAndInline(t *testing.T) {
	for _, src := range []string{
		"package.id = \"pete/x\"\n",
		"package = { id = \"pete/x\" }\n",
	} {
		m, ok, err := ParseManifestCore([]byte(src))
		if err != nil || !ok || m.ID != "pete/x" {
			t.Errorf("%q: ok=%v m=%+v err=%v", src, ok, m, err)
		}
	}
}

// Malformed TOML is an ERROR on the stage-1 path, never "package absent":
// reading it as absent would let an unparseable manifest through as a
// package-less local directory instead of failing at the door.
func TestParseManifestCoreMalformedIsError(t *testing.T) {
	for _, src := range []string{
		"[package\nid = \"x\"\n",
		"id = \n",
		"[package]\nid = \"x\"\n[package]\nid = \"y\"\n",
		"description = \"unterminated\n",
	} {
		m, ok, err := ParseManifestCore([]byte(src))
		if err == nil {
			t.Errorf("%q: want an error, got ok=%v m=%+v", src, ok, m)
			continue
		}
		if ok {
			t.Errorf("%q: an error must not also report presence", src)
		}
		if !strings.Contains(err.Error(), "parse [package]") {
			t.Errorf("%q: error does not name the stage-1 parse: %v", src, err)
		}
	}
}

func TestCheckCompatibility(t *testing.T) {
	m := Manifest{PackageAPI: 1, RequiresByre: ">=0.2.0"}
	if err := CheckCompatibility(m, "0.2.1"); err != nil {
		t.Fatal(err)
	}
	if err := CheckCompatibility(m, "0.1.0"); err == nil || !strings.Contains(err.Error(), "requires byre") {
		t.Fatalf("want requires_byre failure, got %v", err)
	}
	if err := CheckCompatibility(Manifest{PackageAPI: 99}, "0.2.1"); err == nil || !strings.Contains(err.Error(), "package_api") {
		t.Fatalf("want package_api failure, got %v", err)
	}
	// Dev binary passes every requires_byre.
	if err := CheckCompatibility(Manifest{RequiresByre: ">=99.0.0"}, "0.0.0-devel"); err != nil {
		t.Fatalf("devel should pass any requires_byre: %v", err)
	}
}

func TestStripPackageTable(t *testing.T) {
	raw := []byte(`description = "hi"

[package]
id = "x"
version = "1"

[build]
apt = ["a"]
`)
	out := string(StripPackageTable(raw))
	if strings.Contains(out, "[package]") || strings.Contains(out, `id = "x"`) {
		t.Fatalf("package table not stripped: %s", out)
	}
	if !strings.Contains(out, `description = "hi"`) || !strings.Contains(out, "[build]") {
		t.Fatalf("body damaged: %s", out)
	}
}

func TestGenerateBundledHeader(t *testing.T) {
	h := GenerateBundledHeader("byre/claude", "skill", "v0.2.1", "The agent")
	if !strings.Contains(h, `id = "byre/claude"`) || !strings.Contains(h, `version = "v0.2.1"`) {
		t.Fatal(h)
	}
	if !strings.Contains(h, `requires_byre = ">=0.2.1"`) {
		t.Fatal(h)
	}
}

func TestStripPackageTableMidFile(t *testing.T) {
	raw := []byte(`description = "hi"

[build]
apt = ["a"]

[package]
id = "x"
version = "1"

[runtime]
env = { K = "v" }
`)
	out := string(StripPackageTable(raw))
	if strings.Contains(out, "[package]") || strings.Contains(out, `id = "x"`) {
		t.Fatalf("package table not stripped: %s", out)
	}
	if !strings.Contains(out, "[build]") || !strings.Contains(out, "[runtime]") {
		t.Fatalf("body damaged: %s", out)
	}
}

// A `[package]`-shaped line inside a multiline string is DATA (a Dockerfile
// heredoc is the canonical case): the strip must neither truncate the string
// nor mistake it for the real table — and must still strip the real one.
func TestStripPackageTableIgnoresMultilineStrings(t *testing.T) {
	for _, c := range []struct{ name, quote string }{
		{"literal", "'''"},
		{"basic", `"""`},
	} {
		raw := []byte(`[build]
dockerfile = [` + c.quote + `
RUN cat <<'EOF'
[package]
payload text
[other]
EOF
` + c.quote + `]

[package]
id = "owner/example"
version = "1"

[runtime]
env = { K = "v" }
`)
		out := string(StripPackageTable(raw))
		for _, want := range []string{"payload text", "EOF", c.quote + "]", "[runtime]"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: string content lost (%q missing):\n%s", c.name, want, out)
			}
		}
		if strings.Contains(out, `id = "owner/example"`) {
			t.Errorf("%s: real [package] table survived:\n%s", c.name, out)
		}
	}
}

// Every spelling of the package tree is the package tree: quoted headers,
// quoted-dotted subtables, a trailing comment on the header line, dotted and
// inline forms, and files blocks that resume after an intervening foreign
// table. The line scanner this replaced missed all of them.
func TestStripPackageTableSpellings(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"quoted header",
			"[\"package\"]\nid = \"x\"\n\n[build]\napt = [\"a\"]\n",
			"\n[build]\napt = [\"a\"]\n",
		},
		{
			"literal-quoted header",
			"['package']\nid = \"x\"\n\n[build]\napt = [\"a\"]\n",
			"\n[build]\napt = [\"a\"]\n",
		},
		{
			"quoted dotted subtable",
			"[\"package\".meta]\nnote = \"x\"\n\n[build]\napt = [\"a\"]\n",
			"\n[build]\napt = [\"a\"]\n",
		},
		{
			"trailing comment on the header",
			"[package] # the frozen core\nid = \"x\"\n\n[build]\napt = [\"a\"]\n",
			"\n[build]\napt = [\"a\"]\n",
		},
		{
			"non-contiguous files blocks after a foreign table",
			"[package]\nid = \"x\"\n\n[build]\napt = [\"a\"]\n\n[[package.files]]\nsrc = \"a\"\n\n[[\"package\".files]]\nsrc = \"b\"\n",
			"\n[build]\napt = [\"a\"]\n\n\n",
		},
		{
			"top-level dotted key",
			"package.id = \"x\"\ndescription = \"hi\"\n\n[build]\napt = [\"a\"]\n",
			"description = \"hi\"\n\n[build]\napt = [\"a\"]\n",
		},
		{
			"inline table",
			"package = { id = \"x\" }\ndescription = \"hi\"\n",
			"description = \"hi\"\n",
		},
		{
			// meta.package's root is meta: the strip has no claim on it.
			"nested inline member is not the package tree",
			"meta = { package = { id = \"x\" } }\ndescription = \"hi\"\n",
			"meta = { package = { id = \"x\" } }\ndescription = \"hi\"\n",
		},
		{
			// A multiline array's continuation line can begin with '[', and a
			// header-shaped line can sit inside a multiline string; neither is
			// structure.
			"header-shaped text in values",
			"matrix = [\n  [1, 2],\n]\nd = \"\"\"\n[package]\nid = \"no\"\n\"\"\"\n\n[package]\nid = \"x\"\n",
			"matrix = [\n  [1, 2],\n]\nd = \"\"\"\n[package]\nid = \"no\"\n\"\"\"\n\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := string(StripPackageTable([]byte(c.src))); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// The comment above the table that FOLLOWS a removed package block describes
// that table, not the block: it comes through byte for byte.
func TestStripPackageTableKeepsTheNextTablesComment(t *testing.T) {
	src := "# describes the core\n[package]\nid = \"x\"\n\n# describes the build\n[build]\napt = [\"a\"]\n"
	want := "\n# describes the build\n[build]\napt = [\"a\"]\n"
	if got := string(StripPackageTable([]byte(src))); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// The signature cannot report a parse failure, so it reports nothing: input it
// could not read comes back unchanged, for the caller's strict stage-2 parse
// to refuse with a position.
func TestStripPackageTableReturnsUnreadableInputUnchanged(t *testing.T) {
	src := "[package\nid = \"x\"\n"
	if got := string(StripPackageTable([]byte(src))); got != src {
		t.Errorf("got %q, want the input unchanged (%q)", got, src)
	}
}

// A multiline string VALUE inside the real [package] table is stripped with
// its table, including its continuation lines.
func TestStripPackageTableStripsMultilineValueInPackage(t *testing.T) {
	raw := []byte(`[package]
id = "x"
description = """
multi [build]
line"""

[build]
apt = ["a"]
`)
	out := string(StripPackageTable(raw))
	if strings.Contains(out, "multi") || strings.Contains(out, `id = "x"`) {
		t.Fatalf("package table's multiline value survived:\n%s", out)
	}
	if !strings.Contains(out, `apt = ["a"]`) {
		t.Fatalf("body damaged:\n%s", out)
	}
}

// A body key written below the [package] header is package.<key> by TOML's
// scoping, stripped with the tree before stage 2 sees it: the check names
// the key and the move, and stays quiet for everything the table defines,
// for header-shaped text that is data, and for bytes stage 1 already
// refused.
func TestCheckPackageScoping(t *testing.T) {
	hdr := "[package]\nid = \"pete/x\"\nversion = \"1.0.0\"\nkind = \"skill\"\ndescription = \"d\"\n"
	cases := []struct {
		name    string
		kind    Kind
		content string
		want    []string // fragments of the refusal; nil = must pass
	}{
		{"defined keys only", KindSkill, hdr, nil},
		{"the pack tool's files list", KindSkill, hdr + "[[package.files]]\nsrc = \"a\"\ndest = \"a\"\nsha256 = \"00\"\n", nil},
		{"no package table", KindSkill, "description = \"d\"\n[build]\nfiles = { \"a\" = \"/b\" }\n", nil},
		{"body keys above the header", KindSkill, "companion_for = \"claude\"\n" + hdr, nil},
		{"header-shaped text inside a string", KindSkill, hdr + "[build]\ndockerfile = [\"\"\"\n[package]\nfiles = 1\n\"\"\"]\n", nil},
		{"multiline array continuation", KindTemplate, "[package]\nid = \"pete/t\"\nkind = \"template\"\n[env]\nX = \"\"\"\n[package]\napt = 1\n\"\"\"\n", nil},
		{"unparseable is stage 1's", KindSkill, "[package\n", nil},
		{"an inline files map", KindSkill, hdr + "files = { \"hook.sh\" = \"/etc/byre/firstrun.d/50-x.sh\" }\n", []string{"files", "package.files", "move it above [package]", "[build] files"}},
		{"a files array of strings", KindSkill, hdr + "files = [\"hook.sh\"]\n", []string{"files"}},
		{"an empty files list", KindSkill, hdr + "files = []\n", nil},
		{"a template body key", KindTemplate, "[package]\nid = \"pete/t\"\nkind = \"template\"\napt = [\"jq\"]\n", []string{"apt", "package.apt", "template", "move it above [package]"}},
		{"a quoted header", KindSkill, "[\"package\"]\nid = \"pete/x\"\nfiles = { \"a\" = \"/b\" }\n", []string{"files"}},
		{"a dotted key", KindSkill, "description = \"d\"\npackage.id = \"pete/x\"\npackage.apt = [\"jq\"]\n", []string{"apt"}},
		{"several, sorted", KindSkill, hdr + "runtime = 1\napt = 1\n", []string{"apt, runtime"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckPackageScoping(c.kind, []byte(c.content))
			if c.want == nil {
				if err != nil {
					t.Fatalf("must pass, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("must refuse")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("refusal lacks %q: %v", w, err)
				}
			}
		})
	}
}
