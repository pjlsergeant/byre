package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/pjlsergeant/byre/internal/packages"
)

func TestAliasEquivalenceAndBangCancel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	// Wire a minimal bundled FS so aliases exist.
	bundled := fstest.MapFS{
		"skills/claude/skill.toml":     &fstest.MapFile{Data: []byte("description = \"c\"\n")},
		"templates/go/template.config": &fstest.MapFile{Data: []byte("base = \"golang:1.22\"\n")},
	}
	CatalogLoader = func(h string) (*packages.Catalog, error) {
		return packages.LoadCatalog(h, bundled, "0.2.0", "0.2.0", packages.Stage2Hooks{Template: ValidateTemplateBytes})
	}
	t.Cleanup(func() { CatalogLoader = nil })

	// default enables bare claude; project cancels with !byre/claude.
	if err := os.WriteFile(filepath.Join(home, "default.config"),
		[]byte("skills = [\"base\", \"claude\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Use resolveWithCatalog directly (avoids project.Resolve store layout).
	cat, err := CatalogLoader(home)
	if err != nil {
		t.Fatal(err)
	}
	projCfg := Config{Skills: []string{"!byre/claude", "extra"}}
	// Canonicalize + merge manually via resolveWithCatalog with empty template.
	got, err := resolveWithCatalog(home, projCfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	// After expand: default has byre/claude; project !byre/claude removes it.
	if reflect.DeepEqual(got.Skills, []string{"base", "byre/claude", "extra"}) {
		t.Fatalf("!byre/claude should cancel bare claude, got %v", got.Skills)
	}
	want := []string{"base", "extra"}
	if !reflect.DeepEqual(got.Skills, want) {
		t.Fatalf("skills = %v, want %v", got.Skills, want)
	}
}

func TestBareAndCanonicalAgentEquivalent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	bundled := fstest.MapFS{
		"skills/claude/skill.toml": &fstest.MapFile{Data: []byte("description = \"c\"\n")},
	}
	CatalogLoader = func(h string) (*packages.Catalog, error) {
		return packages.LoadCatalog(h, bundled, "0.2.0", "0.2.0", packages.Stage2Hooks{Template: ValidateTemplateBytes})
	}
	t.Cleanup(func() { CatalogLoader = nil })

	cat, err := CatalogLoader(home)
	if err != nil {
		t.Fatal(err)
	}
	a, err := resolveWithCatalog(home, Config{Agent: "claude"}, cat)
	if err != nil {
		t.Fatal(err)
	}
	b, err := resolveWithCatalog(home, Config{Agent: "byre/claude"}, cat)
	if err != nil {
		t.Fatal(err)
	}
	if a.Agent != "byre/claude" || b.Agent != "byre/claude" {
		t.Fatalf("agent bare=%q canon=%q, both want byre/claude", a.Agent, b.Agent)
	}
}
