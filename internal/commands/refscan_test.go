package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/skills"
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

	cat, err := packages.LoadCatalog(home, builtins.FS(), "v0.2.0", "0.2.0", packages.Stage2Hooks{Skill: skills.ValidatePrimaryBytes, Template: config.ValidateTemplateBytes})
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
