package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pjlsergeant/byre/internal/builtins"
)

// scanReferences must cover the whole stored cascade: a reference that lives
// only in a named layer still shows up in install/uninstall warnings.
func TestScanReferencesCoversLayers(t *testing.T) {
	home := t.TempDir()
	writeCfg := func(rel, content string) {
		path := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeCfg("layers/base/layer.config", "skills = [\"pete/tool\"]\n")
	writeCfg("layers/broken/layer.config", "skills = [not toml\n")
	writeCfg("layers/quiet/layer.config", "skills = [\"pete/other\"]\n")
	writeCfg("projects/app/byre.config", "skills = [\"pete/tool\"]\n")
	// Resolution follows symlinked layer dirs, so the scan must too; a
	// stray plain file in layers/ is skipped (no layer.config under it).
	writeCfg("elsewhere/layer.config", "skills = [\"pete/tool\"]\n")
	if err := os.Symlink(filepath.Join(home, "elsewhere"), filepath.Join(home, "layers", "linked")); err != nil {
		t.Fatal(err)
	}
	writeCfg("layers/stray", "not a layer dir\n")
	// Names resolution refuses are never loaded into any cascade, so even
	// an unparsable config under one must NOT count as a guarded hit: an
	// invalid name (grammar), and a bundled bare name (reserved squatter).
	writeCfg("layers/Bad_Name/layer.config", "skills = [not toml\n")
	writeCfg("layers/go/layer.config", "skills = [not toml\n")

	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		t.Fatal(err)
	}
	hits := scanReferences(home, cat, "pete/tool")

	got := map[string]bool{} // Where -> Guarded
	for _, h := range hits {
		got[h.Where] = h.Guarded
	}
	want := map[string]bool{
		"layer base":   false,
		"layer broken": true, // unparsable counts as a reference
		"layer linked": false,
		"project app":  false,
	}
	if len(got) != len(want) {
		t.Fatalf("hits = %+v, want %+v", got, want)
	}
	for where, guarded := range want {
		g, ok := got[where]
		if !ok || g != guarded {
			t.Fatalf("hits = %+v, want %+v", got, want)
		}
	}
}

// Only a PROOF of absence earns silence. A config byre cannot probe, or a
// cascade directory it cannot list, is "cannot prove there are no references"
// -- and that is the guarded hit the whole scanner is built on, because the
// alternative is an uninstall proceeding past a reference it never saw.
func TestScanReferencesGuardsWhatItCannotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads through a 0000 directory")
	}
	t.Run("unprobeable config", func(t *testing.T) {
		home := t.TempDir()
		locked := filepath.Join(home, "projects", "app")
		if err := os.MkdirAll(locked, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(locked, "byre.config"), []byte("skills = [\"pete/tool\"]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o755) })

		cat, err := builtins.LoadCatalogRaw(home)
		if err != nil {
			t.Fatal(err)
		}
		hits := scanReferences(home, cat, "pete/tool")
		if len(hits) != 1 || hits[0].Where != "project app" || !hits[0].Guarded {
			t.Fatalf("hits = %+v, want one guarded hit for project app", hits)
		}
	})

	t.Run("unlistable cascade dir", func(t *testing.T) {
		home := t.TempDir()
		layers := filepath.Join(home, "layers")
		if err := os.MkdirAll(filepath.Join(layers, "base"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(layers, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(layers, 0o755) })

		cat, err := builtins.LoadCatalogRaw(home)
		if err != nil {
			t.Fatal(err)
		}
		hits := scanReferences(home, cat, "pete/tool")
		if len(hits) != 1 || hits[0].Where != "layers" || !hits[0].Guarded {
			t.Fatalf("hits = %+v, want one guarded hit for the layers dir", hits)
		}
	})
}
