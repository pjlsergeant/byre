package packages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pjlsergeant/byre/internal/testtools"
)

func writeLocalSkill(t *testing.T, home, id string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(home, "skills", filepath.FromSlash(id))
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPackEmitsExhaustiveManifest(t *testing.T) {
	home := t.TempDir()
	writeLocalSkill(t, home, "pete/tool", map[string]string{
		"skill.toml": `[package]
id = "pete/tool"
version = "1.0.0"
requires_byre = ">=0.1.0"
description = "a tool"

[context]
text = "hi"
`,
		"hooks/login.sh": "#!/bin/sh\necho hi\n",
		"CONTEXT.md":     "notes\n",
	})
	cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	ent, err := cat.ResolveName("pete/tool")
	if err != nil {
		t.Fatal(err)
	}
	manifest, digest, err := Pack(ent)
	if err != nil {
		t.Fatal(err)
	}
	s := string(manifest)
	for _, want := range []string{
		`id = "pete/tool"`, `kind = "skill"`, "package_api = 1",
		`src = "hooks/login.sh"`, "executable = true", `src = "CONTEXT.md"`,
		"[context]",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, `dest = "skill.toml"`) {
		t.Error("primary must not appear in its own files list")
	}
	if len(digest) != 64 {
		t.Errorf("digest = %q", digest)
	}
	// The emitted manifest round-trips: files parse, digest recomputes.
	entries, err := ParseManifestFiles(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	if PackageDigest(manifest, RecordsFromEntries(entries)) != digest {
		t.Fatal("digest must recompute from the emitted manifest")
	}
}

func TestPackRefusesMissingIdentity(t *testing.T) {
	home := t.TempDir()
	writeLocalSkill(t, home, "tool", map[string]string{
		"skill.toml": "description = \"no package block\"\n",
	})
	cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	ent, err := cat.ResolveName("tool")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Pack(ent)
	if err == nil || !strings.Contains(err.Error(), "qualified id") {
		t.Fatalf("want missing-identity refusal, got %v", err)
	}
}

func TestPackRefusesSymlink(t *testing.T) {
	home := t.TempDir()
	dir := writeLocalSkill(t, home, "pete/sym", map[string]string{
		"skill.toml": `[package]
id = "pete/sym"
version = "1.0.0"
requires_byre = ">=0.1.0"
`,
	})
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link")); err != nil {
		testtools.Unavailable(t, "symlink", err)
	}
	cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	ent, err := cat.ResolveName("pete/sym")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Pack(ent); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("want symlink refusal, got %v", err)
	}
}

// The bundled display digest is deterministic and moves with anything that
// would move a pack of the same bytes: payload content and the synthesized
// [package] core (whose version is the byre release).
func TestDisplayDigestBundled(t *testing.T) {
	digestFor := func(payload, displayVer string) string {
		t.Helper()
		fsys := fstest.MapFS{
			"skills/tool/skill.toml": &fstest.MapFile{Data: []byte("description = \"a tool\"\n")},
			"skills/tool/CONTEXT.md": &fstest.MapFile{Data: []byte(payload)},
		}
		cat, err := LoadCatalog(t.TempDir(), fsys, displayVer, strings.TrimPrefix(displayVer, "v"), Stage2Hooks{})
		if err != nil {
			t.Fatal(err)
		}
		ent, err := cat.ResolveName("tool")
		if err != nil {
			t.Fatal(err)
		}
		d, err := DisplayDigest(ent)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	base := digestFor("notes\n", "v0.2.0")
	if len(base) != 64 {
		t.Fatalf("digest = %q", base)
	}
	if digestFor("notes\n", "v0.2.0") != base {
		t.Error("same bytes, same version must reproduce the digest")
	}
	if digestFor("other\n", "v0.2.0") == base {
		t.Error("a payload change must move the digest")
	}
	if digestFor("notes\n", "v0.3.0") == base {
		t.Error("a byre version change must move the digest (it is in the manifest core)")
	}
}

func TestDisplayDigestRefusesNonBundled(t *testing.T) {
	home := t.TempDir()
	writeLocalSkill(t, home, "pete/tool", map[string]string{
		"skill.toml": "description = \"local\"\n",
	})
	cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	ent, err := cat.ResolveName("pete/tool")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DisplayDigest(ent); err == nil || !strings.Contains(err.Error(), "bundled") {
		t.Fatalf("want bundled-only refusal, got %v", err)
	}
}

// Pack output must be a fixed point of pack: the README publishing flow
// writes `pack` output over the primary in place, so packing a previously
// packed manifest has to reproduce it -- and its digest -- byte for byte.
// Pre-fix, each round accreted a duplicate marker comment (and the trailing
// blank shape of the source leaked into the bytes, shifting the digest).
func TestPackIsFixedPoint(t *testing.T) {
	home := t.TempDir()
	dir := writeLocalSkill(t, home, "pete/tool", map[string]string{
		"skill.toml": `[package]
id = "pete/tool"
version = "1.0.0"
requires_byre = ">=0.1.0"

[build.files]
"install.sh" = "/etc/byre/firstrun.d/tool"

` + "# Distribution payload list -- generated by `byre skill pack`.\n" + `[[package.files]]
src = "stale"
dest = "stale"
sha256 = "0000000000000000000000000000000000000000000000000000000000000000"
`,
		"install.sh": "#!/bin/sh\n",
	})
	cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	ent, err := cat.ResolveName("pete/tool")
	if err != nil {
		t.Fatal(err)
	}
	first, digest1, err := Pack(ent)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(first), "# Distribution payload list"); got != 1 {
		t.Fatalf("marker comment appears %d times, want exactly 1:\n%s", got, first)
	}
	if strings.Contains(string(first), `src = "stale"`) {
		t.Fatalf("stale files list survived the re-pack:\n%s", first)
	}

	// Write the output over the primary (the publishing flow) and pack again.
	if err := os.WriteFile(filepath.Join(dir, "skill.toml"), first, 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err = LoadCatalog(home, nil, "v0.2.0", "0.2.0", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	ent, err = cat.ResolveName("pete/tool")
	if err != nil {
		t.Fatal(err)
	}
	second, digest2, err := Pack(ent)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Errorf("re-pack changed the manifest:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if digest2 != digest1 {
		t.Errorf("re-pack changed the digest: %s -> %s", digest1, digest2)
	}
}

// Sources that differ only in their trailing-blank shape (LF blanks, spaces,
// tabs, CRLF line endings on blank tails) must pack to identical bytes and
// digests: the payload is what's published, not the author's file tail.
func TestPackNormalizesTrailingShape(t *testing.T) {
	const src = `[package]
id = "pete/tool"
version = "1.0.0"
requires_byre = ">=0.1.0"

[build.files]
"install.sh" = "/etc/byre/firstrun.d/tool"
`
	pack := func(t *testing.T, primary string) ([]byte, string) {
		t.Helper()
		home := t.TempDir()
		writeLocalSkill(t, home, "pete/tool", map[string]string{
			"skill.toml": primary,
			"install.sh": "#!/bin/sh\n",
		})
		cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0", Stage2Hooks{})
		if err != nil {
			t.Fatal(err)
		}
		ent, err := cat.ResolveName("pete/tool")
		if err != nil {
			t.Fatal(err)
		}
		manifest, digest, err := Pack(ent)
		if err != nil {
			t.Fatal(err)
		}
		return manifest, digest
	}
	base, baseDigest := pack(t, src)
	for name, tail := range map[string]string{
		"lf blanks":     "\n\n\n",
		"spaces":        "\n   \n",
		"tabs":          "\n\t\n",
		"crlf blank":    "\r\n\r\n",
		"trailing bare": "",
	} {
		got, gotDigest := pack(t, strings.TrimRight(src, "\n")+tail)
		if string(got) != string(base) {
			t.Errorf("%s: manifest bytes differ from base:\n%q\nvs\n%q", name, got, base)
		}
		if gotDigest != baseDigest {
			t.Errorf("%s: digest %s != base %s", name, gotDigest, baseDigest)
		}
	}
}

// A marker-shaped line that is DATA -- inside a multiline string, or an
// author comment not attached to a files block -- must survive packing; only
// markers attached to a [[package.files]] header are pack's own leftovers.
func TestPackKeepsMarkerLookalikesInBody(t *testing.T) {
	marker := "# Distribution payload list -- generated by `byre skill pack`."
	home := t.TempDir()
	writeLocalSkill(t, home, "pete/tool", map[string]string{
		"skill.toml": `[package]
id = "pete/tool"
version = "1.0.0"
requires_byre = ">=0.1.0"

[context]
text = """
` + marker + `
"""

` + marker + `
# ^ an author comment with no files block after it
`,
		"install.sh": "#!/bin/sh\n",
	})
	cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	ent, err := cat.ResolveName("pete/tool")
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := Pack(ent)
	if err != nil {
		t.Fatal(err)
	}
	// Both lookalikes survive; pack adds exactly one real marker for the
	// generated list.
	if got := strings.Count(string(manifest), marker); got != 3 {
		t.Errorf("marker-shaped lines = %d, want 3 (string data + author comment + generated):\n%s", got, manifest)
	}
}

// A pack marker is dropped only when it is ATTACHED to a package.files array
// header. What counts as one of those headers is the PARSER's answer, so the
// quoted spellings answer too -- with literal-text matching, a manifest whose
// files headers were quoted accreted a fresh marker on every re-pack.
func TestStripPackMarkersAttachment(t *testing.T) {
	marker := packMarker(KindSkill)
	other := packMarker(KindTemplate)
	for _, c := range []struct{ name, src, want string }{
		{
			"bare header",
			marker + "\n[[package.files]]\nsrc = \"a\"\n",
			"[[package.files]]\nsrc = \"a\"\n",
		},
		{
			"basic-quoted header",
			marker + "\n[[\"package\".files]]\nsrc = \"a\"\n",
			"[[\"package\".files]]\nsrc = \"a\"\n",
		},
		{
			"literal-quoted header",
			marker + "\n[['package'.files]]\nsrc = \"a\"\n",
			"[['package'.files]]\nsrc = \"a\"\n",
		},
		{
			"stacked markers across blank lines",
			marker + "\n\n" + other + "\n\n[[package.files]]\nsrc = \"a\"\n",
			"\n\n[[package.files]]\nsrc = \"a\"\n",
		},
		{
			// A foreign table between the marker and the files list breaks the
			// attachment: the comment describes [build], not pack's output.
			"foreign table in between",
			marker + "\n[build]\napt = []\n\n[[package.files]]\nsrc = \"a\"\n",
			marker + "\n[build]\napt = []\n\n[[package.files]]\nsrc = \"a\"\n",
		},
		{
			"foreign comment in between",
			marker + "\n# an author's note\n[[package.files]]\nsrc = \"a\"\n",
			marker + "\n# an author's note\n[[package.files]]\nsrc = \"a\"\n",
		},
		{
			"no files list at all",
			"[build]\napt = []\n\n" + marker + "\n",
			"[build]\napt = []\n\n" + marker + "\n",
		},
		{
			"marker-shaped line inside a multiline string is data",
			"d = \"\"\"\n" + marker + "\n\"\"\"\n\n[[package.files]]\nsrc = \"a\"\n",
			"d = \"\"\"\n" + marker + "\n\"\"\"\n\n[[package.files]]\nsrc = \"a\"\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := stripPackMarkers(c.src); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// The body splits at its first REAL header. A multiline array's continuation
// line may begin with '[' -- the line scanner this replaced split there,
// cutting the array in half and emitting the fragments on either side of the
// [package] header.
func TestSplitLeadingTopLevel(t *testing.T) {
	for _, c := range []struct{ name, body, pre, tables string }{
		{"no header", "a = 1\nb = 2\n", "a = 1\nb = 2\n", ""},
		{"header first", "[build]\napt = []\n", "", "[build]\napt = []\n"},
		{
			"multiline array continuation",
			"matrix = [\n  [1, 2],\n  [3, 4],\n]\n\n[build]\napt = []\n",
			"matrix = [\n  [1, 2],\n  [3, 4],\n]\n\n",
			"[build]\napt = []\n",
		},
		{
			"header-shaped line in a multiline string",
			"d = \"\"\"\n[build]\n\"\"\"\n\n[real]\nx = 1\n",
			"d = \"\"\"\n[build]\n\"\"\"\n\n",
			"[real]\nx = 1\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			pre, tables := splitLeadingTopLevel(c.body)
			if pre != c.pre || tables != c.tables {
				t.Errorf("pre  = %q, want %q\ntables = %q, want %q", pre, c.pre, tables, c.tables)
			}
		})
	}
}

// Pack is a fixed point over NONCANONICAL sources too: a quoted [package]
// header, quoted files headers with a stale marker above them, and a preamble
// bare key whose multiline array has continuation lines starting with '['.
// The FIRST pack of such a source is deliberately not its own input -- pack
// normalizes the core and regenerates the list -- but the SECOND must
// reproduce the first, bytes and digest.
func TestPackIsFixedPointOverNoncanonicalInput(t *testing.T) {
	home := t.TempDir()
	dir := writeLocalSkill(t, home, "pete/tool", map[string]string{
		"skill.toml": `# a companion of the codex agent skill
companion_for = "codex"
matrix = [
  [1, 2],
  [3, 4],
]

["package"]
id = "pete/tool"
version = "1.0.0"
requires_byre = ">=0.1.0"

[build]
apt = ["curl"]

` + packMarker(KindSkill) + `
[["package".files]]
src = "stale"
dest = "stale"
sha256 = "0000000000000000000000000000000000000000000000000000000000000000"
`,
		"install.sh": "#!/bin/sh\n",
	})
	packOnce := func(t *testing.T) ([]byte, string) {
		t.Helper()
		cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0", Stage2Hooks{})
		if err != nil {
			t.Fatal(err)
		}
		ent, err := cat.ResolveName("pete/tool")
		if err != nil {
			t.Fatal(err)
		}
		manifest, digest, err := Pack(ent)
		if err != nil {
			t.Fatal(err)
		}
		return manifest, digest
	}
	first, digest1 := packOnce(t)
	s := string(first)
	if got := strings.Count(s, packMarker(KindSkill)); got != 1 {
		t.Fatalf("marker appears %d times, want exactly 1 (the quoted files header let it accrete):\n%s", got, s)
	}
	if strings.Contains(s, `src = "stale"`) || strings.Contains(s, `["package"]`) {
		t.Fatalf("the quoted package tree survived the strip:\n%s", s)
	}
	// The preamble key and its multiline array come through whole, and BEFORE
	// the [package] header -- after it they would read as package.* keys.
	if !strings.Contains(s, "matrix = [\n  [1, 2],\n  [3, 4],\n]\n") {
		t.Fatalf("the multiline array was cut:\n%s", s)
	}
	keyAt, hdrAt := strings.Index(s, `companion_for = "codex"`), strings.Index(s, "[package]")
	if keyAt < 0 || hdrAt < 0 || keyAt > hdrAt {
		t.Fatalf("preamble keys must precede [package] (key %d, header %d):\n%s", keyAt, hdrAt, s)
	}

	if err := os.WriteFile(filepath.Join(dir, "skill.toml"), first, 0o644); err != nil {
		t.Fatal(err)
	}
	second, digest2 := packOnce(t)
	if string(second) != string(first) {
		t.Errorf("re-pack changed the manifest:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if digest2 != digest1 {
		t.Errorf("re-pack changed the digest: %s -> %s", digest1, digest2)
	}
}

// A top-level bare key (companion_for, shared_auth_for) is only such while it
// precedes every table header: TOML scopes a bare key to the most recent
// header, so the same line emitted after [package] reads as a package.* key,
// which StripPackageTable removes before the stage-2 parse — the pairing
// would vanish silently from every packed companion skill.
func TestPackKeepsTopLevelKeysBeforePackageHeader(t *testing.T) {
	home := t.TempDir()
	dir := writeLocalSkill(t, home, "pete/sec", map[string]string{
		"skill.toml": `# a companion of the codex agent skill
companion_for = "codex"

[package]
id = "pete/sec"
version = "1.0.0"
requires_byre = ">=1.2.0"
description = "a companion"

[build]
apt = ["python3"]
`,
	})
	cat, err := LoadCatalog(home, nil, "v1.2.0", "1.2.0", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	ent, err := cat.ResolveName("pete/sec")
	if err != nil {
		t.Fatal(err)
	}
	first, digest1, err := Pack(ent)
	if err != nil {
		t.Fatal(err)
	}
	s := string(first)
	keyAt := strings.Index(s, `companion_for = "codex"`)
	hdrAt := strings.Index(s, "[package]")
	if keyAt < 0 {
		t.Fatalf("companion_for dropped from packed manifest:\n%s", s)
	}
	if keyAt > hdrAt {
		t.Fatalf("companion_for emitted after [package] (offset %d > %d) — it parses as package.companion_for there:\n%s", keyAt, hdrAt, s)
	}
	if !strings.Contains(s, "# a companion of the codex agent skill") {
		t.Fatalf("comment attached to the top-level key dropped:\n%s", s)
	}

	// Write the output over the primary (the publishing flow) and pack again.
	if err := os.WriteFile(filepath.Join(dir, "skill.toml"), first, 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err = LoadCatalog(home, nil, "v1.2.0", "1.2.0", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	ent, err = cat.ResolveName("pete/sec")
	if err != nil {
		t.Fatal(err)
	}
	second, digest2, err := Pack(ent)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Errorf("re-pack changed the manifest:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if digest2 != digest1 {
		t.Errorf("re-pack changed the digest: %s -> %s", digest1, digest2)
	}
}
