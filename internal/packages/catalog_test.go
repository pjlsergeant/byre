package packages

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/BurntSushi/toml"
)

func bundledFS() fstest.MapFS {
	return fstest.MapFS{
		"skills/claude/skill.toml":     &fstest.MapFile{Data: []byte("description = \"Claude\"\n[agent]\ncommand = \"claude\"\n")},
		"skills/firewall/skill.toml":   &fstest.MapFile{Data: []byte("description = \"fw\"\n")},
		"templates/go/template.config": &fstest.MapFile{Data: []byte("base = \"golang:1.22\"\n")},
	}
}

func TestDisplayVsCompatVersion(t *testing.T) {
	home := t.TempDir()
	cat, err := LoadCatalog(home, bundledFS(), "v9.9.9", "9.9.9", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	ent, err := cat.ResolveName("claude")
	if err != nil {
		t.Fatal(err)
	}
	if ent.Version != "v9.9.9" {
		t.Fatalf("display version = %q, want v9.9.9", ent.Version)
	}
	if !strings.Contains(ent.ProvenanceLabel(), "v9.9.9") {
		t.Fatalf("provenance label = %q", ent.ProvenanceLabel())
	}
	// Compat path: local package requiring >=9.0.0 loads under compat 9.9.9.
	dir := filepath.Join(home, "skills", "need9")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[package]
id = "need9"
package_api = 1
requires_byre = ">=9.0.0"
kind = "skill"
`
	if err := os.WriteFile(filepath.Join(dir, "skill.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cat2, err := LoadCatalog(home, bundledFS(), "v9.9.9", "9.9.9", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat2.ResolveName("need9"); err != nil {
		t.Fatalf("compat should accept requires_byre: %v", err)
	}
	// Devel bypass: requires >=99 still loads.
	body2 := `[package]
id = "need99"
package_api = 1
requires_byre = ">=99.0.0"
kind = "skill"
`
	dir2 := filepath.Join(home, "skills", "need99")
	mustMkdirAll(t, dir2, 0o755)
	mustWriteFile(t, filepath.Join(dir2, "skill.toml"), []byte(body2), 0o644)
	cat3, err := LoadCatalog(home, nil, "(devel)", "0.0.0-devel", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	// need99 only
	mustRemoveAll(t, filepath.Join(home, "skills", "need9"))
	mustRemoveAll(t, filepath.Join(home, "skills", "claude"))
	cat3, err = LoadCatalog(home, nil, "(devel)", "0.0.0-devel", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat3.ResolveName("need99"); err != nil {
		t.Fatalf("devel must load requires_byre >=99: %v", err)
	}
}

func TestEagerStage2UnknownKey(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "skills", "typo")
	mustMkdirAll(t, dir, 0o755)
	hooks := Stage2Hooks{Skill: func(raw []byte) error {
		body := StripPackageTable(raw)
		type empty struct{}
		md, err := toml.Decode(string(body), &empty{})
		if err != nil {
			return err
		}
		if und := md.Undecoded(); len(und) > 0 {
			return fmt.Errorf("unknown key(s) in skill.toml: %v", und)
		}
		return nil
	}}
	mustWriteFile(t, filepath.Join(dir, "skill.toml"), []byte("typo_key = true\n"), 0o644)
	cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0", hooks)
	if err != nil {
		t.Fatal(err)
	}
	var ent *Entry
	for _, e := range cat.List(KindSkill) {
		if e.ID == "typo" && e.Provenance == ProvInvalid {
			ent = e
			break
		}
	}
	if ent == nil {
		t.Fatal("expected INVALID typo skill")
	}
	if !strings.Contains(ent.Reason, "unknown key") {
		t.Fatalf("want unknown key reason, got %q", ent.Reason)
	}
	if _, err := cat.ResolveName("typo"); err == nil || !strings.Contains(err.Error(), `package "typo" is invalid`) {
		t.Fatalf("resolve should hard-error on INVALID, got %v", err)
	}
}

func TestCatalogAliasExpansion(t *testing.T) {
	home := t.TempDir()
	cat, err := LoadCatalog(home, bundledFS(), "v0.2.0", "0.2.0", Stage2Hooks{})
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

	cat, err := LoadCatalog(home, bundledFS(), "v0.2.0", "0.2.0", Stage2Hooks{})
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
	cat, err := LoadCatalog(home, bundledFS(), "v0.2.0", "0.2.0", Stage2Hooks{})
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
// requires_byre) must not evict the bundled entry from the catalog.
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
	cat, err := LoadCatalog(home, bundledFS(), "v0.2.0", "0.2.0", Stage2Hooks{})
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

// A hostile file where a primary should be must degrade to a scoped INVALID
// row, never wedge the catalog: load runs on almost every command, so a FIFO
// (blocks a plain open forever), a symlink resolving to a device, or an
// oversized file each becomes a problem row within a deadline. A symlink to
// a REAL primary is the user's own arrangement of their store and loads
// normally — the judgment is on what the link resolves to.
func TestHostileLocalPrimaryDegradesNotBlocks(t *testing.T) {
	home := t.TempDir()
	mk := func(name string) string {
		dir := filepath.Join(home, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	if err := syscall.Mkfifo(filepath.Join(mk("fifo"), "skill.toml"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	if err := os.Symlink("/dev/tty", filepath.Join(mk("linked"), "skill.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mk("huge"), "skill.toml"),
		make([]byte, MaxManifestBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(t.TempDir(), "real-skill.toml")
	if err := os.WriteFile(real, []byte("# a fine skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(mk("linkok"), "skill.toml")); err != nil {
		t.Fatal(err)
	}

	done := make(chan *Catalog, 1)
	go func() {
		cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0", Stage2Hooks{})
		if err != nil {
			t.Errorf("LoadCatalog: %v", err)
		}
		done <- cat
	}()
	var cat *Catalog
	select {
	case cat = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("LoadCatalog blocked on a hostile primary — the exact hang load must never have")
	}
	if cat == nil {
		t.FailNow()
	}
	for _, id := range []string{"fifo", "linked", "huge"} {
		ent, ok := cat.Lookup(id)
		if !ok || ent.Provenance != ProvInvalid {
			t.Fatalf("%s: want INVALID row, got ok=%v ent=%+v", id, ok, ent)
		}
	}
	if ent, _ := cat.Lookup("huge"); !strings.Contains(ent.Reason, "limit") {
		t.Fatalf("huge reason should name the limit, got %q", ent.Reason)
	}
	if ent, ok := cat.Lookup("linkok"); !ok || ent.Provenance != ProvLocal {
		t.Fatalf("a symlink to a real primary must load as the user's local package, got ok=%v ent=%+v", ok, ent)
	}
}
