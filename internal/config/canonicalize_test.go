package config

import (
	"io/fs"
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
	BundledFS = func() fs.FS {
		return fstest.MapFS{
			"skills/claude/skill.toml":     &fstest.MapFile{Data: []byte("description = \"c\"\n")},
			"templates/go/template.config": &fstest.MapFile{Data: []byte("base = \"golang:1.22\"\n")},
		}
	}
	ByreVersion = func() string { return "0.2.0" }
	t.Cleanup(func() { BundledFS = nil; ByreVersion = nil })

	// default enables bare claude; project cancels with !byre/claude.
	if err := os.WriteFile(filepath.Join(home, "default.config"),
		[]byte("skills = [\"base\", \"claude\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Use resolveWithCatalog directly (avoids project.Resolve store layout).
	cat, err := packages.LoadCatalog(home, BundledFS(), "0.2.0")
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
	BundledFS = func() fs.FS {
		return fstest.MapFS{
			"skills/claude/skill.toml": &fstest.MapFile{Data: []byte("description = \"c\"\n")},
		}
	}
	ByreVersion = func() string { return "0.2.0" }
	t.Cleanup(func() { BundledFS = nil; ByreVersion = nil })

	cat, err := packages.LoadCatalog(home, BundledFS(), "0.2.0")
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
