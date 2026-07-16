package configui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// chainModel is the named-layers test bed: default + template (go) + a
// two-layer chain (torn <- torn-frontend) under a project extending the leaf.
func chainModel() model {
	inh := Inherited{
		HasLower: true,
		Default:  config.Config{Apt: []string{"ripgrep"}},
		Templates: map[string]config.Config{
			"go": {Apt: []string{"golang"}},
		},
		Layers: map[string]config.Config{
			"torn":          {Apt: []string{"jq"}, Env: map[string]string{"TORN": "1"}},
			"torn-frontend": {Extends: "torn", Apt: []string{"nodejs"}},
		},
		LayerNames: []string{"torn", "torn-frontend"},
	}
	cfg := config.Config{
		Template: "go",
		Extends:  "torn-frontend",
		Apt:      []string{"build-essential"},
	}
	return newModel("t", "/tmp/x", cfg, []string{"go"}, nil, nil, nil, inh, nil, TargetProject)
}

func TestChainRowsAttributedToLayers(t *testing.T) {
	m := chainModel()
	rows := m.aptRows()

	if r := rowByText(t, rows, "jq"); r.kind != rowInherited || r.source != "layer:torn" {
		t.Errorf("jq should be inherited from layer torn: %+v", r)
	}
	if r := rowByText(t, rows, "nodejs"); r.kind != rowInherited || r.source != "layer:torn-frontend" {
		t.Errorf("nodejs should be inherited from layer torn-frontend: %+v", r)
	}
	if r := rowByText(t, rows, "golang"); r.kind != rowInherited || r.source != "template:go" {
		t.Errorf("template attribution must survive beside the chain: %+v", r)
	}
	if r := rowByText(t, rows, "ripgrep"); r.source != "default" {
		t.Errorf("default attribution must survive beside the chain: %+v", r)
	}
}

// The EXTENDS picker is a live field: cycling to none drops the whole
// chain's contributions from the effective view.
func TestExtendsPickerFlipsChainLive(t *testing.T) {
	m := chainModel()

	countApt := func(m model) int { return len(m.aptRows()) }
	withChain := countApt(m)

	// Cycle the extends picker to "none".
	m.extSel = indexOf(m.extOpts, noneOption)
	if got := countApt(m); got >= withChain {
		t.Errorf("dropping the chain should drop its rows: %d -> %d", withChain, got)
	}
	if src := m.lowerSource(func(c config.Config) bool { return contains(c.Apt, "jq") }); src == "layer:torn" {
		t.Error("no chain selected: nothing should attribute to a layer")
	}
}

// assemble writes the picked parent as extends; none writes nothing.
func TestAssembleWritesExtends(t *testing.T) {
	m := chainModel()
	if got := m.assemble().Extends; got != "torn-frontend" {
		t.Errorf("assemble extends: got %q", got)
	}
	m.extSel = indexOf(m.extOpts, noneOption)
	if got := m.assemble().Extends; got != "" {
		t.Errorf("assemble extends after clearing: got %q", got)
	}
	// Changing the picker is a dirty state (savable via ctrl+s).
	if !m.dirty() {
		t.Error("changing extends should mark the form dirty")
	}
}

// The global editor has no EXTENDS section, and round-trips a hand-written
// extends untouched rather than silently dropping it (the resolver refuses
// it loudly at develop).
func TestGlobalEditorHasNoExtends(t *testing.T) {
	cfg := config.Config{Extends: "torn"}
	m := newModel("t", "/tmp/x", cfg, nil, nil, nil, nil, Inherited{}, nil, TargetGlobal)
	for _, f := range m.order {
		if f == fExtends {
			t.Fatal("global editor must not offer the extends picker")
		}
	}
	if got := m.assemble().Extends; got != "torn" {
		t.Errorf("global assemble must round-trip extends untouched, got %q", got)
	}
}

// The layer editor: no template picker (shape selection has one owner), the
// EXTENDS section present, and assemble writes no template.
func TestLayerEditorShape(t *testing.T) {
	inh := Inherited{
		HasLower:   true,
		Default:    config.Config{Apt: []string{"ripgrep"}},
		Layers:     map[string]config.Config{"torn": {Apt: []string{"jq"}}},
		LayerNames: []string{"torn"},
	}
	cfg := config.Config{Extends: "torn", Apt: []string{"nodejs"}}
	m := newModel("t", "/tmp/x", cfg, nil, nil, nil, nil, inh, nil, TargetLayer)

	hasTemplate, hasExtends := false, false
	for _, f := range m.order {
		switch f {
		case fTemplate:
			hasTemplate = true
		case fExtends:
			hasExtends = true
		}
	}
	if hasTemplate {
		t.Error("layer editor must not offer the template picker")
	}
	if !hasExtends {
		t.Error("layer editor must offer the extends picker")
	}
	if got := m.assemble().Template; got != "" {
		t.Errorf("layer assemble must not write a template, got %q", got)
	}
	// Ancestor attribution works in the layer editor too.
	rows := m.aptRows()
	if r := rowByText(t, rows, "jq"); r.kind != rowInherited || r.source != "layer:torn" {
		t.Errorf("ancestor attribution in layer editor: %+v", r)
	}
	if r := rowByText(t, rows, "ripgrep"); r.source != "default" {
		t.Errorf("default should sit under a layer's editor: %+v", r)
	}
}

// A dangling extends (layer deleted since) still shows in the picker so an
// unrelated open-and-save round-trips it instead of silently dropping it.
func TestDanglingExtendsSurvivesRoundTrip(t *testing.T) {
	cfg := config.Config{Extends: "gone"}
	m := newModel("t", "/tmp/x", cfg, nil, nil, nil, nil, Inherited{HasLower: true}, nil, TargetProject)
	if got := m.assemble().Extends; got != "gone" {
		t.Errorf("dangling extends must round-trip, got %q", got)
	}
}

// Saving a config with extends actually persists it through Save's
// ValidateLayer (extends is layer-legal).
func TestSavePersistsExtends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "byre.config")
	inh := Inherited{
		HasLower:   true,
		Layers:     map[string]config.Config{"torn": {}},
		LayerNames: []string{"torn"},
	}
	m := newModel("t", path, config.Config{}, nil, nil, nil, nil, inh, nil, TargetProject)
	m.extSel = indexOf(m.extOpts, "torn")
	m = m.save()
	if m.errMsg != "" {
		t.Fatalf("save failed: %s", m.errMsg)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "extends = \"torn\"") {
		t.Errorf("saved file should carry extends, got:\n%s", b)
	}
}

// The extends row renders the full chain when the pointer pulls in ancestors.
func TestExtendsRowShowsChain(t *testing.T) {
	m := chainModel()
	v := m.renderValue(fExtends, false)
	if !strings.Contains(v, "torn -> torn-frontend") {
		t.Errorf("extends row should show the resolved chain, got %q", v)
	}
}
