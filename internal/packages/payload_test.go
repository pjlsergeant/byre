package packages

import (
	"strings"
	"testing"
)

func TestParseManifestFiles(t *testing.T) {
	m := `
[package]
id = "pete/tool"
version = "1.0.0"
kind = "skill"
package_api = 1
requires_byre = ">=0.1.0"

[[package.files]]
src = "hooks/login.sh"
dest = "hooks/login.sh"
sha256 = "` + strings.Repeat("ab", 32) + `"
executable = true

[[package.files]]
src = "CONTEXT.md"
dest = "CONTEXT.md"
sha256 = "` + strings.Repeat("cd", 32) + `"
`
	entries, err := ParseManifestFiles([]byte(m))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !entries[0].Executable || entries[1].Executable {
		t.Fatalf("entries = %+v", entries)
	}
	if err := ValidateFilesList(entries, "skill.toml"); err != nil {
		t.Fatal(err)
	}
	// Stage 2 must not see the files list.
	body := StripPackageTable([]byte(m))
	if strings.Contains(string(body), "package.files") {
		t.Fatalf("strip left package.files behind:\n%s", body)
	}
}

func TestValidateFilesListRejects(t *testing.T) {
	good := strings.Repeat("ab", 32)
	cases := []struct {
		name    string
		entries []FileEntry
	}{
		{"traversal dest", []FileEntry{{Src: "x", Dest: "../evil", SHA256: good}}},
		{"absolute dest", []FileEntry{{Src: "x", Dest: "/etc/passwd", SHA256: good}}},
		{"traversal src", []FileEntry{{Src: "../x", Dest: "x", SHA256: good}}},
		{"unclean dest", []FileEntry{{Src: "x", Dest: "a//b", SHA256: good}}},
		{"backslash", []FileEntry{{Src: "x", Dest: `a\b`, SHA256: good}}},
		{"bad hash", []FileEntry{{Src: "x", Dest: "x", SHA256: "zz"}}},
		{"primary self-entry", []FileEntry{{Src: "skill.toml", Dest: "skill.toml", SHA256: good}}},
		{"case collision", []FileEntry{
			{Src: "a", Dest: "Readme.md", SHA256: good},
			{Src: "b", Dest: "readme.md", SHA256: good},
		}},
		{"control char", []FileEntry{{Src: "x", Dest: "a\x1b[31mb", SHA256: good}}},
		{"encoded traversal", []FileEntry{{Src: "%2e%2e/x", Dest: "x", SHA256: good}}},
		{"primary case collision", []FileEntry{{Src: "x", Dest: "SKILL.TOML", SHA256: good}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateFilesList(tc.entries, "skill.toml"); err == nil {
				t.Fatalf("want error for %+v", tc.entries)
			}
		})
	}
	over := make([]FileEntry, MaxPayloadFiles+1)
	for i := range over {
		over[i] = FileEntry{Src: "s", Dest: "d" + strings.Repeat("x", i%50), SHA256: good}
	}
	if err := ValidateFilesList(over, "skill.toml"); err == nil {
		t.Fatal("want count-limit error")
	}
}

func TestPackageDigestStableAndSensitive(t *testing.T) {
	manifest := []byte("[package]\nid = \"pete/x\"\n")
	recs := []PayloadRecord{
		{Dest: "b.sh", SHA256: strings.Repeat("22", 32), Executable: true},
		{Dest: "a.md", SHA256: strings.Repeat("11", 32)},
	}
	d1 := PackageDigest(manifest, recs)
	// Order-insensitive (sorted internally).
	d2 := PackageDigest(manifest, []PayloadRecord{recs[1], recs[0]})
	if d1 != d2 {
		t.Fatal("digest must not depend on entry order")
	}
	// Manifest bytes are in the preimage.
	if PackageDigest([]byte("[package]\nid = \"pete/y\"\n"), recs) == d1 {
		t.Fatal("manifest change must change the digest")
	}
	// Executable bit is in the preimage.
	flip := append([]PayloadRecord{}, recs...)
	flip[0].Executable = false
	if PackageDigest(manifest, flip) == d1 {
		t.Fatal("exec-bit change must change the digest")
	}
	// Hash case is canonicalized.
	upper := append([]PayloadRecord{}, recs...)
	upper[0].SHA256 = strings.ToUpper(upper[0].SHA256)
	if PackageDigest(manifest, upper) != d1 {
		t.Fatal("hash case must not change the digest")
	}
}
