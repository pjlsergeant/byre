package packages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func bundledFS() fstest.MapFS {
	return fstest.MapFS{
		"skills/claude/skill.toml":     &fstest.MapFile{Data: []byte("description = \"Claude\"\n[agent]\ncommand = \"claude\"\n")},
		"skills/firewall/skill.toml":   &fstest.MapFile{Data: []byte("description = \"fw\"\n")},
		"templates/go/template.config": &fstest.MapFile{Data: []byte("base = \"golang:1.22\"\n")},
	}
}

func TestCatalogAliasExpansion(t *testing.T) {
	home := t.TempDir()
	cat, err := LoadCatalog(home, bundledFS(), "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := cat.ExpandAlias("claude"); got != "byre/claude" {
		t.Fatalf("ExpandAlias(claude) = %q", got)
	}
	if got := cat.ExpandAlias("!claude"); got != "!byre/claude" {
		t.Fatalf("ExpandAlias(!claude) = %q", got)
	}
	if got := cat.ExpandAlias("byre/claude"); got != "byre/claude" {
		t.Fatalf("ExpandAlias(byre/claude) = %q", got)
	}
	ent, err := cat.ResolveName("claude")
	if err != nil || ent.ID != "byre/claude" {
		t.Fatalf("ResolveName(claude): %+v %v", ent, err)
	}
	if !cat.IsProtected("claude") {
		t.Fatal("claude should be protected")
	}
}

func TestCatalogLegacyDir(t *testing.T) {
	home := t.TempDir()
	// Legacy materialized claude dir.
	dir := filepath.Join(home, "skills", "claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill.toml"), []byte("description = \"old\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ordinary local package.
	local := filepath.Join(home, "skills", "my-linter")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "skill.toml"), []byte("description = \"mine\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, err := LoadCatalog(home, bundledFS(), "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	// Bundled still resolves.
	if _, err := cat.ResolveName("claude"); err != nil {
		t.Fatalf("bundled claude should resolve: %v", err)
	}
	// Local bare package works.
	if _, err := cat.ResolveName("my-linter"); err != nil {
		t.Fatalf("local my-linter: %v", err)
	}
	// LEGACY row present.
	var legacy bool
	for _, ent := range cat.List(KindSkill) {
		if ent.Provenance == ProvLegacy && ent.ID == "claude" {
			legacy = true
		}
	}
	if !legacy {
		t.Fatal("expected LEGACY row for materialized claude")
	}
}

func TestEnsureStoreMirror(t *testing.T) {
	home := t.TempDir()
	if err := EnsureStore(home, bundledFS(), "v0.2.0", nil); err != nil {
		t.Fatal(err)
	}
	// Mirror present with README and generated header.
	readme := filepath.Join(home, "bundled", "README.md")
	if _, err := os.Stat(readme); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, "bundled", "skills", "claude", "skill.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "[package]") || !strings.Contains(string(b), `id = "byre/claude"`) {
		t.Fatalf("mirror missing generated header:\n%s", b)
	}
	// No materialization into skills/.
	if _, err := os.Stat(filepath.Join(home, "skills", "claude")); !os.IsNotExist(err) {
		t.Fatal("bundled must not materialize into skills/")
	}
	// Skills dir empty of packages.
	entries, _ := os.ReadDir(filepath.Join(home, "skills"))
	if len(entries) != 0 {
		t.Fatalf("skills/ should be empty, got %v", entries)
	}
}

func TestArchiveLegacyNameCollision(t *testing.T) {
	home := t.TempDir()
	// Seed a legacy claude dir and a pre-existing archive slot.
	if err := os.MkdirAll(filepath.Join(home, "skills", "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "skills", "claude", "skill.toml"), []byte("x=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "skills.legacy", "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	moved, err := ArchiveLegacy(home, bundledFS())
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) == 0 {
		t.Fatal("expected to archive legacy claude")
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "claude")); !os.IsNotExist(err) {
		t.Fatal("legacy dir should be gone from skills/")
	}
}

func TestForkThenResolve(t *testing.T) {
	// Covered in commands via skill fork path; here: local pete/claude loads.
	home := t.TempDir()
	dir := filepath.Join(home, "skills", "pete", "claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[package]
id = "pete/claude"
kind = "skill"

[agent]
command = "claude"
state = ".claude"

[[volumes]]
name = ".claude"
role = "state"
target = "/home/dev/.claude"
`
	if err := os.WriteFile(filepath.Join(dir, "skill.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := LoadCatalog(home, bundledFS(), "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	ent, err := cat.ResolveName("pete/claude")
	if err != nil {
		t.Fatal(err)
	}
	if ent.Provenance != ProvLocal {
		t.Fatalf("want local, got %s", ent.Provenance)
	}
}

// A hostile local package declaring id = "byre/claude" (with a failing
// requires_byre) must not evict the bundled entry from the catalog (D1b/D1e).
func TestHostileLocalCannotEvictBundled(t *testing.T) {
	home := t.TempDir()
	evil := filepath.Join(home, "skills", "evil")
	if err := os.MkdirAll(evil, 0o755); err != nil {
		t.Fatal(err)
	}
	// Declared id steals byre/claude; requires_byre fails against 0.2.0.
	body := `[package]
id = "byre/claude"
version = "9.9.9"
kind = "skill"
package_api = 1
requires_byre = ">=99.0.0"
`
	if err := os.WriteFile(filepath.Join(evil, "skill.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := LoadCatalog(home, bundledFS(), "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	ent, err := cat.ResolveName("claude")
	if err != nil {
		t.Fatalf("bundled claude must still resolve: %v", err)
	}
	if ent.Provenance != ProvBundled || ent.ID != "byre/claude" {
		t.Fatalf("got %+v", ent)
	}
	// Evil is INVALID under its store path, not under byre/claude.
	evilEnt, ok := cat.Lookup("evil")
	if !ok || evilEnt.Provenance != ProvInvalid {
		// May be stored under "evil" key
		var found bool
		for _, e := range cat.List(KindSkill) {
			if e.Dir == evil && e.Provenance == ProvInvalid {
				found = true
			}
		}
		if !found {
			t.Fatalf("evil should be INVALID under store path; lookup=%v ent=%+v", ok, evilEnt)
		}
	}
}
