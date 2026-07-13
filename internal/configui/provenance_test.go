package configui

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
)

// A LEGACY materialized copy of a bundled skill shares its bare name. The
// picker must NOT let that problem row disable the valid bundled option —
// exactly the state every upgraded store is in.
func TestLegacyRowDoesNotDisableBundledAlias(t *testing.T) {
	home := t.TempDir()
	// Legacy materialized claude dir + a genuinely broken local skill.
	for name, body := range map[string]string{
		"claude": "description = \"old copy\"\n",
		"typo":   "typo_key = true\n",
	} {
		dir := filepath.Join(home, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "skill.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := packages.Stage2Skill
	packages.Stage2Skill = func(raw []byte) error {
		if string(raw) == "typo_key = true\n" {
			return os.ErrInvalid
		}
		return nil
	}
	t.Cleanup(func() { packages.Stage2Skill = old })
	bundled := fstest.MapFS{
		"skills/claude/skill.toml": &fstest.MapFile{Data: []byte("description = \"c\"\n[agent]\ncommand = \"claude\"\n")},
	}
	cat, err := packages.LoadCatalog(home, bundled, "v0.2.0", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{Catalog: cat}, nil, false)
	if d := m.optDisabled("claude"); d != "" {
		t.Fatalf("bundled alias claude must stay selectable, got disabled: %q", d)
	}
	if p := m.optProv("claude"); p == "" || p == "LEGACY" {
		t.Fatalf("claude should show its bundled label, got %q", p)
	}
	if d := m.optDisabled("typo"); d == "" {
		t.Fatal("broken local skill should be disabled-with-reason")
	}
}
