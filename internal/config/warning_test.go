package config

import (
	"strings"
	"testing"
)

// The two compat warnings detect from PARSED LAYER state, before resolution
// strips the picker-owned section (the reason collection is per-layer at
// all): the legacy array shape fires shared-auth-array naming the agents
// and the drop-on-save remedy; the top-level spelling fires
// shared-auth-top-level naming the migration. A canonical config fires
// neither.
func TestLayerWarningsDetectLegacySharedAuthShapes(t *testing.T) {
	parse := func(t *testing.T, raw string) Config {
		t.Helper()
		c, err := Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	t.Run("array-under-defaults", func(t *testing.T) {
		got := LayerWarnings("default", "/h/default.config",
			parse(t, "[defaults]\nshared_auth = [\"claude\"]\n"))
		if len(got) != 1 || got[0].Kind != WarnSharedAuthArray {
			t.Fatalf("warnings = %+v", got)
		}
		for _, frag := range []string{`"claude"`, "no companion package recorded", "the next save drops it", SharedAuthReRecordRemedy} {
			if !strings.Contains(got[0].Text, frag) {
				t.Errorf("array warning must carry %q, got %q", frag, got[0].Text)
			}
		}
		if got[0].Layer != "default" || got[0].Path != "/h/default.config" {
			t.Errorf("attribution = %q %q", got[0].Layer, got[0].Path)
		}
	})

	t.Run("top-level-table", func(t *testing.T) {
		got := LayerWarnings("default", "/h/default.config",
			parse(t, "[shared_auth]\nclaude = \"claude-shared-auth\"\n"))
		if len(got) != 1 || got[0].Kind != WarnSharedAuthTopLevel {
			t.Fatalf("warnings = %+v", got)
		}
		if !strings.Contains(got[0].Text, "the next save moves it under [defaults]") {
			t.Errorf("top-level warning must carry the migration remedy, got %q", got[0].Text)
		}
	})

	t.Run("top-level-array-fires-both", func(t *testing.T) {
		got := LayerWarnings("default", "",
			parse(t, "shared_auth = [\"codex\"]\n"))
		kinds := map[string]bool{}
		for _, w := range got {
			kinds[w.Kind] = true
		}
		if len(got) != 2 || !kinds[WarnSharedAuthTopLevel] || !kinds[WarnSharedAuthArray] {
			t.Fatalf("a top-level array carries both legacy facts, got %+v", got)
		}
	})

	t.Run("canonical-is-clean", func(t *testing.T) {
		got := LayerWarnings("default", "",
			parse(t, "[defaults]\nshared_auth = { \"claude\" = \"claude-shared-auth\" }\n"))
		if len(got) != 0 {
			t.Fatalf("canonical shape must not warn: %+v", got)
		}
	})
}

// FileWarnings keeps cascade order and per-file attribution; Attribution
// renders label-plus-path, label alone for a bundled (pathless) layer.
func TestFileWarningsAttribution(t *testing.T) {
	def, err := Parse([]byte("[defaults]\nshared_auth = [\"claude\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	files := []CascadeFile{
		{Label: "default", Path: "/h/default.config", Cfg: def},
		{Label: "project", Path: "/p/byre.config"},
	}
	got := FileWarnings(files)
	if len(got) != 1 {
		t.Fatalf("warnings = %+v", got)
	}
	if a := got[0].Attribution(); a != "default (/h/default.config)" {
		t.Errorf("attribution = %q", a)
	}
	if a := (Warning{Layer: "template:go"}).Attribution(); a != "template:go" {
		t.Errorf("pathless attribution = %q", a)
	}
}
