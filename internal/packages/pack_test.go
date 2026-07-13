package packages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0")
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
	cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0")
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
		t.Skip("symlinks unavailable")
	}
	cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0")
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
