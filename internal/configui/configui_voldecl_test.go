package configui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pjlsergeant/byre/internal/config"
)

// volDeclModel is the [[volumes]] test bed: one inherited declaration, one
// skill-contributed one, one of this layer's own.
func volDeclModel() model {
	inh := Inherited{
		HasLower: true,
		Default: config.Config{
			Volumes: []config.Volume{{Name: "shared-cache", Role: "cache", Target: "/home/dev/.cache"}},
		},
		Skills: map[string]SkillRuntime{
			"claude": {Volumes: []config.Volume{{Name: ".claude", Role: "state", Target: "/home/dev/.claude"}}},
		},
	}
	cfg := config.Config{
		Skills:  []string{"claude"},
		Volumes: []config.Volume{{Name: "deps", Role: "cache", Target: "/workspace/node_modules"}},
	}
	return newModel("t", "/tmp/x", cfg, nil, nil, []string{"claude"}, nil, inh, nil, TargetProject)
}

// The declaration screen is the ADR 0018 effective view: this layer's entries,
// the lower layer's (attributed), and the skill contributions that make up most
// of a real box's storage.
func TestVolumeDeclarationRowsClassify(t *testing.T) {
	m := volDeclModel()
	rows := m.fieldRows(fVolumes)

	if r := rowByText(t, rows, "shared-cache -> /home/dev/.cache (cache)"); r.kind != rowInherited || r.source != "default" {
		t.Errorf("inherited volume misclassified: %+v", r)
	}
	if r := rowByText(t, rows, "deps -> /workspace/node_modules (cache)"); r.kind != rowLocal {
		t.Errorf("this layer's volume should be local: %+v", r)
	}
	if r := rowByText(t, rows, ".claude -> /home/dev/.claude (state)"); r.kind != rowSkill || r.source != "skill:claude" {
		t.Errorf("skill volume should show read-only, attributed: %+v", r)
	}
}

// The machine scope and a seed are the two things that change what a
// declaration MEANS, so the row says both rather than reading as an ordinary
// per-project volume.
func TestVolumeRowFlagsScopeAndSeed(t *testing.T) {
	line := volumeLine(config.Volume{Name: "claude-identity", Role: "state", Target: "/x", Scope: "machine"})
	if !strings.Contains(line, "machine") || !strings.Contains(line, "all your projects") {
		t.Errorf("machine-scoped row must name its blast radius: %q", line)
	}
	seeded := volumeLine(config.Volume{Name: "cfg", Role: "state", Target: "/x", Seed: &config.Seed{Host: "~/s"}})
	if !strings.Contains(seeded, "seeded") {
		t.Errorf("seeded row must say so: %q", seeded)
	}
}

// "Remove in this project" on an inherited declaration writes this layer's
// `!name` marker, and the row then reads as removed.
func TestVolumeRemoveHereWritesMarker(t *testing.T) {
	m := volDeclModel()
	m.listField = fVolumes
	row := rowByText(t, m.fieldRows(fVolumes), "shared-cache -> /home/dev/.cache (cache)")
	mm, _ := m.applyRowAct(actRemoveHere, row)
	m = mm.(model)

	got := m.assemble().Volumes
	found := false
	for _, v := range got {
		if v.Name == "!shared-cache" {
			found = true
		}
	}
	if !found {
		t.Fatalf("removing an inherited volume should write the marker: %+v", got)
	}
	if err := m.assemble().ValidateLayer(); err != nil {
		t.Fatalf("the marker this editor writes must pass the layer gate: %v", err)
	}
	if r := rowByText(t, m.fieldRows(fVolumes), "shared-cache -> /home/dev/.cache (cache)"); r.kind != rowRemoved {
		t.Errorf("the marked row should read removed: %+v", r)
	}
}

// Adding a volume through the item editor writes a well-formed declaration and
// flips the form dirty (a widget whose edits never reach assemble/sig is worse
// than no widget).
func TestVolumeItemEditorAddsDeclaration(t *testing.T) {
	m := volDeclModel()
	m.listField = fVolumes
	m = m.startItem(-1)
	m.inputs[0].SetValue("scratch")
	m.inputs[1].SetValue("/home/dev/scratch")
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("commit refused a valid volume: %s", m.itemErr)
	}
	out := m.assemble()
	var got *config.Volume
	for i, v := range out.Volumes {
		if v.Name == "scratch" {
			got = &out.Volumes[i]
		}
	}
	if got == nil {
		t.Fatalf("assemble dropped the new volume: %+v", out.Volumes)
	}
	if got.Target != "/home/dev/scratch" || got.Role != "state" {
		t.Errorf("new volume = %+v, want target /home/dev/scratch role state", *got)
	}
	if err := out.ValidateLayer(); err != nil {
		t.Fatalf("the editor wrote a volume the layer gate refuses: %v", err)
	}
	if !m.dirty() {
		t.Error("adding a volume must mark the form dirty")
	}
	// The role picker's other option.
	m2 := volDeclModel()
	m2.listField = fVolumes
	m2 = m2.startItem(-1)
	m2.inputs[0].SetValue("bin-cache")
	m2.inputs[1].SetValue("/home/dev/.cache/go-build")
	m2.itemMode = 1
	m2 = m2.commitItem()
	if m2.itemErr != "" {
		t.Fatalf("commit refused a cache volume: %s", m2.itemErr)
	}
	for _, v := range m2.assemble().Volumes {
		if v.Name == "bin-cache" && v.Role != "cache" {
			t.Errorf("the Role picker did not take: %+v", v)
		}
	}
}

// The form authors neither scope nor seed, so an edit must carry both through
// untouched -- retyping a target must not silently un-share a machine volume
// or un-seed a state one -- and it must SAY they are there.
func TestVolumeEditPreservesScopeAndSeed(t *testing.T) {
	cfg := config.Config{Volumes: []config.Volume{
		{Name: "ident", Role: "state", Target: "/home/dev/.id", Scope: "machine"},
		{Name: "cfg", Role: "state", Target: "/home/dev/.cfg", Seed: &config.Seed{Host: "~/seed"}},
	}}
	m := newModel("t", "/tmp/x", cfg, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fVolumes

	m = m.startItem(0)
	notes := strings.Join(m.itemNotes(), "\n")
	if !strings.Contains(notes, "machine") {
		t.Errorf("editing a machine-scoped volume must disclose the scope:\n%s", notes)
	}
	m.inputs[1].SetValue("/home/dev/.identity")
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("commit: %s", m.itemErr)
	}
	if got := m.assemble().Volumes[0]; got.Scope != "machine" || got.Target != "/home/dev/.identity" {
		t.Errorf("edit lost the scope: %+v", got)
	}

	m = m.startItem(1)
	if notes := strings.Join(m.itemNotes(), "\n"); !strings.Contains(notes, "seed") {
		t.Errorf("editing a seeded volume must disclose the seed:\n%s", notes)
	}
	m.inputs[1].SetValue("/home/dev/.config")
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("commit: %s", m.itemErr)
	}
	if got := m.assemble().Volumes[1]; got.Seed == nil || got.Seed.Host != "~/seed" {
		t.Errorf("edit lost the seed: %+v", got)
	}
}

// Declarations are ordinary cascade grammar, so the global and layer editors
// carry them; only the engine-backed DATA row needs a VolumeAdmin.
func TestVolumeDeclarationsReachEveryTarget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target Target
		vols   VolumeAdmin
		data   bool
	}{
		{"global", TargetGlobal, nil, false},
		{"layer", TargetLayer, nil, false},
		{"project without an engine", TargetProject, nil, false},
		{"project with an engine", TargetProject, &fakeVols{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{Volumes: []config.Volume{{Name: "deps", Role: "cache", Target: "/w/node_modules"}}}
			m := newModel("t", "/tmp/x", cfg, nil, nil, nil, nil, Inherited{}, tc.vols, tc.target)
			hasDecl, hasData := false, false
			for _, f := range m.order {
				switch f {
				case fVolumes:
					hasDecl = true
				case fVolumeData:
					hasData = true
				}
			}
			if !hasDecl {
				t.Error("the volumes declaration screen must be reachable in every editor")
			}
			if hasData != tc.data {
				t.Errorf("volume data row present=%v, want %v", hasData, tc.data)
			}
			// The declaration round-trips even where there is no engine.
			if got := m.assemble().Volumes; len(got) != 1 || got[0].Name != "deps" {
				t.Errorf("declarations must round-trip: %+v", got)
			}
		})
	}
}

// Enter on the data row opens the engine screen; enter on the declarations row
// opens the list. Two rows, two screens -- the admin surface is not replaced.
func TestVolumeRowsOpenTheirOwnScreens(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, &fakeVols{}, TargetProject)
	m.setFocus(indexOfField(m.order, fVolumeData))
	mm, _ := m.updateForm(tea.KeyMsg{Type: tea.KeyEnter})
	if got := mm.(model).mode; got != modeVolumes {
		t.Errorf("Volume data should open the engine screen, mode=%v", got)
	}
	m.setFocus(indexOfField(m.order, fVolumes))
	mm, _ = m.updateForm(tea.KeyMsg{Type: tea.KeyEnter})
	next := mm.(model)
	if next.mode != modeList || next.listField != fVolumes {
		t.Errorf("Volumes should open the declaration list, mode=%v field=%v", next.mode, next.listField)
	}
}

// Overriding an inherited volume opens the ADD editor, so the scope and seed
// the form does not author have nothing to be read from unless the inherited
// declaration rides along. Without that, shadowing a machine-scoped identity
// volume silently rescoped it to this project -- a different volume, on a
// different name, with the agent's shared login not in it -- and the
// machine-scope warning never showed, because it keyed on editing an entry
// this file already had.
func TestVolumeOverrideCarriesScopeAndSeed(t *testing.T) {
	inh := Inherited{
		HasLower: true,
		Default: config.Config{Volumes: []config.Volume{
			{Name: "claude-identity", Role: "state", Target: "/home/dev/.byre-identity/claude", Scope: "machine"},
			{Name: "cfg", Role: "state", Target: "/home/dev/.cfg", Seed: &config.Seed{Host: "~/seed"}},
		}},
	}
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, inh, nil, TargetProject)
	m.listField = fVolumes

	rows := m.fieldRows(fVolumes)
	ident := rowByText(t, rows, volumeLine(inh.Default.Volumes[0]))
	if ident.kind != rowInherited {
		t.Fatalf("expected an inherited row to override: %+v", ident)
	}
	next := m.startOverride(ident)
	// The warning must show HERE, at the moment of overriding -- it used to
	// appear only when editing an entry this file already had.
	if notes := strings.Join(next.itemNotes(), "\n"); !strings.Contains(notes, "machine") {
		t.Errorf("overriding a machine-scoped volume must disclose the scope:\n%s", notes)
	}
	next.inputs[1].SetValue("/home/dev/.identity")
	next = next.commitItem()
	if next.itemErr != "" {
		t.Fatalf("commit: %s", next.itemErr)
	}
	got := next.assemble().Volumes
	if len(got) != 1 {
		t.Fatalf("the override must be this layer's only entry: %+v", got)
	}
	if got[0].Scope != "machine" {
		t.Errorf("the override silently rescoped a machine volume: %+v", got[0])
	}

	// Same for a seed.
	seeded := rowByText(t, m.fieldRows(fVolumes), volumeLine(inh.Default.Volumes[1]))
	sn := m.startOverride(seeded)
	if notes := strings.Join(sn.itemNotes(), "\n"); !strings.Contains(notes, "seed") {
		t.Errorf("overriding a seeded volume must disclose the seed:\n%s", notes)
	}
	sn.inputs[1].SetValue("/home/dev/.config")
	sn = sn.commitItem()
	if sn.itemErr != "" {
		t.Fatalf("commit: %s", sn.itemErr)
	}
	if v := sn.assemble().Volumes[0]; v.Seed == nil || v.Seed.Host != "~/seed" {
		t.Errorf("the override dropped the seed: %+v", v)
	}

	// A plain ADD is project-scoped and unseeded: nothing to carry.
	add := m.startItem(-1)
	add.inputs[0].SetValue("scratch")
	add.inputs[1].SetValue("/home/dev/scratch")
	add = add.commitItem()
	if add.itemErr != "" {
		t.Fatalf("commit: %s", add.itemErr)
	}
	if v := add.assemble().Volumes[0]; v.Scope != "" || v.Seed != nil {
		t.Errorf("a new volume must be project-scoped and unseeded: %+v", v)
	}
}
