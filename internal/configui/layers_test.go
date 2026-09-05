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

// A hand-written template key in a layer file can't be repaired in the UI
// (no picker there) and must not be written back: save refuses with the
// remedy, and the file stays untouched.
func TestLayerEditorRefusesToSaveTemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layer.config")
	if err := os.WriteFile(path, []byte("template = \"go\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModel("t", path, config.Config{Template: "go"}, nil, nil, nil, nil, Inherited{HasLower: true}, nil, TargetLayer)
	m = m.save()
	if m.errMsg == "" || !strings.Contains(m.errMsg, "template is not allowed in a layer file") {
		t.Fatalf("save must refuse a template key in a layer, got errMsg=%q", m.errMsg)
	}
	if m.savedOnce {
		t.Error("nothing may have been written")
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "template = \"go\"") {
		t.Errorf("file must be untouched, got: %s (%v)", b, err)
	}
}

// pickerFlipModel is the scalar-picker test bed: a layer that sets the agent
// and engine, a template that sets the engine, and a project saying nothing
// about any of them. Only a layer can put an agent below a project (templates
// are banned from composition keys; the resolver blanks default.config's
// favourites), so that is the axis the agent rows are tested on.
func pickerFlipModel(cfg config.Config) model {
	inh := Inherited{
		HasLower: true,
		Templates: map[string]config.Config{
			"go": {Engine: "podman"},
		},
		Layers: map[string]config.Config{
			"team":  {Agent: "codex", Engine: "podman"},
			"quiet": {Agent: config.NoneLabel, Engine: "auto"},
		},
		LayerNames: []string{"quiet", "team"},
	}
	return newModel("t", "/tmp/x", cfg, []string{"go"}, []string{"claude", "codex"}, nil, nil, inh, nil, TargetProject)
}

// focusOn puts the form cursor on field f so cycle drives that picker.
func focusOn(t *testing.T, m *model, f fieldID) {
	t.Helper()
	i := indexOfField(m.order, f)
	if i < 0 {
		t.Fatalf("field %v is not in the focus order %v", f, m.order)
	}
	m.setFocus(i)
}

// cycleTo drives the focused picker with the arrow keys until it reads want.
func cycleTo(t *testing.T, m *model, opts func() []string, sel func() int, want string) {
	t.Helper()
	for range len(opts()) + 1 {
		if opts()[sel()] == want {
			return
		}
		m.cycle(1)
	}
	t.Fatalf("could not cycle to %q in %v", want, opts())
}

// The scalar pickers' inherit rows describe the cascade BELOW this file, and
// the extends and template pickers CHANGE that cascade mid-session. Baked
// once at open, the agent picker kept offering "none" with no inherit row
// after a layer that sets an agent was picked, so "none" wrote absent and
// the next develop handed the box the layer's agent despite the explicit no
// (the editor misreporting effective state -- P0).
func TestScalarPickersFollowExtendsFlip(t *testing.T) {
	m := pickerFlipModel(config.Config{})
	if hasInheritRow(m.agentOpts) || m.agentNow() != "" {
		t.Fatalf("nothing below sets an agent yet: opts=%v now=%q", m.agentOpts, m.agentNow())
	}
	agentOpts := func() []string { return m.agentOpts }
	agentSel := func() int { return m.agentSel }

	// Pick the layer: the agent picker grows an inherit row naming the
	// layer's agent, absence lands on it, and the effective agent follows.
	focusOn(t, &m, fExtends)
	cycleTo(t, &m, func() []string { return m.extOpts }, func() int { return m.extSel }, "team")
	if !isInheritRow(m.agentOpts[m.agentSel]) || !strings.Contains(m.agentOpts[m.agentSel], "codex") {
		t.Fatalf("after picking a layer that sets the agent, absence must sit on its inherit row: %q in %v", m.agentOpts[m.agentSel], m.agentOpts)
	}
	if m.agentNow() != "codex" {
		t.Errorf("effective agent must follow the flip, got %q", m.agentNow())
	}
	if !isInheritRow(m.engineOpts[m.engineSel]) || m.engineNow() != "podman" {
		t.Errorf("the engine row must follow the same flip: %q / %q", m.engineOpts[m.engineSel], m.engineNow())
	}

	// The reported data loss: choosing none against the freshly inherited
	// agent must WRITE the off-switch, so the next develop keeps it.
	focusOn(t, &m, fAgent)
	cycleTo(t, &m, agentOpts, agentSel, noneOption)
	if got := m.assemble().Agent; got != config.NoneLabel {
		t.Fatalf("none over a layer's agent must write %q, got %q", config.NoneLabel, got)
	}
	if m.agentNow() != "" {
		t.Errorf("the effective agent under none is empty, got %q", m.agentNow())
	}

	// A deliberate none survives the layer going away and coming back, and
	// so does a concrete pick: the flip changes what the rows mean, never
	// what this file says.
	focusOn(t, &m, fExtends)
	cycleTo(t, &m, func() []string { return m.extOpts }, func() int { return m.extSel }, noneOption)
	if hasInheritRow(m.agentOpts) {
		t.Errorf("no layer, no inherit row: %v", m.agentOpts)
	}
	if m.agentOpts[m.agentSel] != noneOption {
		t.Errorf("a chosen none stays on none when the lower value goes: %q", m.agentOpts[m.agentSel])
	}
	cycleTo(t, &m, func() []string { return m.extOpts }, func() int { return m.extSel }, "team")
	if m.agentOpts[m.agentSel] != noneOption || m.assemble().Agent != config.NoneLabel {
		t.Errorf("a chosen none must survive the layer's return: %q writes %q", m.agentOpts[m.agentSel], m.assemble().Agent)
	}
	focusOn(t, &m, fAgent)
	cycleTo(t, &m, agentOpts, agentSel, "claude")
	focusOn(t, &m, fExtends)
	cycleTo(t, &m, func() []string { return m.extOpts }, func() int { return m.extSel }, noneOption)
	if m.agentOpts[m.agentSel] != "claude" {
		t.Errorf("a concrete pick must survive the flip: %q", m.agentOpts[m.agentSel])
	}
}

// Absence stays absence across a flip: a file that says nothing about the
// agent keeps saying nothing, whichever row now displays that, and a flip
// there and back leaves the form clean.
func TestScalarPickersAbsenceSurvivesFlipRoundTrip(t *testing.T) {
	m := pickerFlipModel(config.Config{Extends: "team"})
	if !isInheritRow(m.agentOpts[m.agentSel]) {
		t.Fatalf("absent agent under a layer that sets one opens on inherit, got %q in %v", m.agentOpts[m.agentSel], m.agentOpts)
	}
	before := m.sig()
	focusOn(t, &m, fExtends)
	cycleTo(t, &m, func() []string { return m.extOpts }, func() int { return m.extSel }, noneOption)
	if m.agentOpts[m.agentSel] != noneOption || m.assemble().Agent != "" {
		t.Errorf("with nothing below, absence shows as none and still writes absent: %q writes %q", m.agentOpts[m.agentSel], m.assemble().Agent)
	}
	cycleTo(t, &m, func() []string { return m.extOpts }, func() int { return m.extSel }, "team")
	if !isInheritRow(m.agentOpts[m.agentSel]) || m.assemble().Agent != "" {
		t.Errorf("absence returns to the inherit row and writes nothing: %q writes %q", m.agentOpts[m.agentSel], m.assemble().Agent)
	}
	if m.sig() != before {
		t.Errorf("a flip there and back must leave the form clean")
	}
}

// The template picker moves the engine's lower value the same way.
func TestScalarPickersFollowTemplateFlip(t *testing.T) {
	m := pickerFlipModel(config.Config{})
	if hasInheritRow(m.engineOpts) {
		t.Fatalf("no template selected: no inherited engine, got %v", m.engineOpts)
	}
	focusOn(t, &m, fTemplate)
	cycleTo(t, &m, func() []string { return m.tmplOpts }, func() int { return m.tmplSel }, "go")
	if !isInheritRow(m.engineOpts[m.engineSel]) || m.engineNow() != "podman" {
		t.Fatalf("picking a template that sets the engine must surface it: %q / %q", m.engineOpts[m.engineSel], m.engineNow())
	}
	focusOn(t, &m, fEngine)
	cycleTo(t, &m, func() []string { return m.engineOpts }, func() int { return m.engineSel }, "auto")
	if got := m.assemble().Engine; got != "auto" {
		t.Errorf("auto over a template's engine must write itself, got %q", got)
	}
}

// A layer's agent is below the project from the moment the editor opens,
// not only after the extends picker moves: the chain is part of the lower
// fold the inherit rows are computed from.
func TestScalarPickersFoldTheChainAtOpen(t *testing.T) {
	m := pickerFlipModel(config.Config{Extends: "team"})
	if m.agentNow() != "codex" || !strings.Contains(m.agentOpts[m.agentSel], "codex") {
		t.Fatalf("a layer's agent must be inherited at open: now=%q row=%q", m.agentNow(), m.agentOpts[m.agentSel])
	}
	// The explicit off-switch opens on none and keeps writing itself.
	m = pickerFlipModel(config.Config{Extends: "team", Agent: config.NoneLabel})
	if m.agentOpts[m.agentSel] != noneOption || m.assemble().Agent != config.NoneLabel {
		t.Errorf("a stored none opens on none and round-trips: %q writes %q", m.agentOpts[m.agentSel], m.assemble().Agent)
	}
}

// A none the user rests the picker on is a deliberate answer and writes the
// sentinel even with nothing below -- the standing onboarding gives its own
// explicit no -- where a zero-edit save of an unstated key keeps writing
// absent (TestScalarPickersPreserveAStoredSentinelWithNothingBelow). Without
// it a none chosen before the extends picker moves reads as never chosen.
func TestScalarPickersChosenNoneIsExplicit(t *testing.T) {
	m := pickerFlipModel(config.Config{})
	if m.agentOpts[m.agentSel] != noneOption || m.assemble().Agent != "" {
		t.Fatalf("an unstated agent with nothing below displays as none and writes absent: %q writes %q", m.agentOpts[m.agentSel], m.assemble().Agent)
	}
	focusOn(t, &m, fAgent)
	m.cycle(1) // off none...
	if m.agentOpts[m.agentSel] == noneOption {
		t.Fatalf("cycle did not move: %v", m.agentOpts)
	}
	cycleTo(t, &m, func() []string { return m.agentOpts }, func() int { return m.agentSel }, noneOption) // ...and back by hand
	if got := m.assemble().Agent; got != config.NoneLabel {
		t.Fatalf("a none the user chose must be written, got %q", got)
	}
	// And it holds against a layer picked afterwards.
	focusOn(t, &m, fExtends)
	cycleTo(t, &m, func() []string { return m.extOpts }, func() int { return m.extSel }, "team")
	if m.agentOpts[m.agentSel] != noneOption || m.agentNow() != "" {
		t.Errorf("a chosen none must not turn into the layer's agent: row %q now %q", m.agentOpts[m.agentSel], m.agentNow())
	}
}

// The deliberate-sentinel bit follows the user's LAST move: choosing
// inherit withdraws a stored none, so dropping the layer afterwards leaves
// absence, not a resurrected off-switch.
func TestScalarPickersInheritWithdrawsAStoredNone(t *testing.T) {
	m := pickerFlipModel(config.Config{Extends: "team", Agent: config.NoneLabel})
	focusOn(t, &m, fAgent)
	cycleTo(t, &m, func() []string { return m.agentOpts }, func() int { return m.agentSel }, inheritRow("codex"))
	if got := m.assemble().Agent; got != "" {
		t.Fatalf("inherit writes nothing, got %q", got)
	}
	focusOn(t, &m, fExtends)
	cycleTo(t, &m, func() []string { return m.extOpts }, func() int { return m.extSel }, noneOption)
	if got := m.assemble().Agent; got != "" {
		t.Errorf("the layer going away must not bring the withdrawn none back, got %q", got)
	}
	cycleTo(t, &m, func() []string { return m.extOpts }, func() int { return m.extSel }, "team")
	if !isInheritRow(m.agentOpts[m.agentSel]) {
		t.Errorf("and with the layer back, absence is inherit again: %q", m.agentOpts[m.agentSel])
	}
}

// Marking a sentinel deliberate changes what a save writes, so it is a
// change the form must own up to: away from a displayed none and back
// again is dirty, because ctrl+s would now write the key.
func TestScalarPickersDeliberateNoneIsDirty(t *testing.T) {
	m := pickerFlipModel(config.Config{})
	if m.dirty() {
		t.Fatal("a fresh open is clean")
	}
	focusOn(t, &m, fAgent)
	m.cycle(1)
	cycleTo(t, &m, func() []string { return m.agentOpts }, func() int { return m.agentSel }, noneOption)
	if !m.dirty() {
		t.Errorf("the row reads as it opened, but a save now writes %q: the form must be dirty", m.assemble().Agent)
	}
	// A stored none cycled off and back on is the file as opened: clean.
	m = pickerFlipModel(config.Config{Agent: config.NoneLabel})
	focusOn(t, &m, fAgent)
	m.cycle(1)
	cycleTo(t, &m, func() []string { return m.agentOpts }, func() int { return m.agentSel }, noneOption)
	if m.dirty() {
		t.Error("returning to the stored none is no change")
	}
}

// A lower layer's explicit off-switch is a decision, and the inherit row
// names it: "(inherit: none)" rather than a bare none that reads as nothing
// below. Its effect is still off, choosing it still writes absent, and the
// plain none row still writes the key.
func TestScalarPickersInheritRowNamesALayersOffSwitch(t *testing.T) {
	m := pickerFlipModel(config.Config{Extends: "quiet"})
	if got := m.agentOpts[m.agentSel]; got != inheritRow(config.NoneLabel) {
		t.Fatalf("absence under a layer saying none opens on its inherit row, got %q in %v", got, m.agentOpts)
	}
	if m.agentNow() != "" || m.assemble().Agent != "" {
		t.Errorf("inheriting an off-switch is off and writes nothing: now=%q writes %q", m.agentNow(), m.assemble().Agent)
	}
	if got := m.engineOpts[m.engineSel]; got != inheritRow("auto") || m.engineNow() != "" {
		t.Errorf("engine reads the same way: row %q now %q", got, m.engineNow())
	}
	focusOn(t, &m, fAgent)
	cycleTo(t, &m, func() []string { return m.agentOpts }, func() int { return m.agentSel }, noneOption)
	if got := m.assemble().Agent; got != config.NoneLabel {
		t.Errorf("this file's own none is still a written key, got %q", got)
	}
	cycleTo(t, &m, func() []string { return m.agentOpts }, func() int { return m.agentSel }, "claude")
	if m.agentNow() != "claude" {
		t.Errorf("a concrete pick over the layer's none is that pick, got %q", m.agentNow())
	}
	// Attribution follows the flip like every other lower value.
	focusOn(t, &m, fExtends)
	cycleTo(t, &m, func() []string { return m.extOpts }, func() int { return m.extSel }, "team")
	if !contains(m.agentOpts, inheritRow("codex")) || contains(m.agentOpts, inheritRow(config.NoneLabel)) {
		t.Errorf("the inherit row follows the selected layer: %v", m.agentOpts)
	}
}
