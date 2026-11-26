package configui

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// globalVolModel is the --global editor with one enabled skill contributing a
// machine volume (a shared login) and a per-project one, over a machine
// VolumeAdmin holding the machine volume's data.
func globalVolModel(fv *fakeVols) model {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"zai": {Volumes: []config.Volume{
			{Name: ".zai-codex", Role: "state", Target: "/home/dev/.zai-codex-home"},
			{Name: "zai-identity", Role: "state", Target: "/home/dev/.byre-identity/zai", Scope: "machine"},
		}},
	}}
	cfg := config.Config{Skills: []string{"zai"}}
	m := newModel("t", "/tmp/x", cfg, nil, nil, []string{"zai"}, nil, inh, fv, TargetGlobal)
	m.mode = modeList
	m.listField = fVolumes
	return m
}

func rowIndex(t *testing.T, rows []listRow, prefix string) int {
	t.Helper()
	for i, r := range rows {
		if strings.HasPrefix(r.text, prefix) {
			return i
		}
	}
	t.Fatalf("no row starting %q", prefix)
	return -1
}

// A skill-declared machine volume is the shared login a person wants rid of:
// enter on its row in the --global editor offers Delete and Clear, and Clear
// lands on the data screen with the confirm asked and the machine-wide
// consequence stated. esc from there returns to the declaration list.
func TestGlobalMachineVolumeRowOffersDeleteAndClear(t *testing.T) {
	fv := &fakeVols{vols: []VolumeStatus{{Name: "zai-identity", Exists: true, Machine: true, Engine: "docker"}}}
	m := globalVolModel(fv)
	m.listCur = rowIndex(t, m.fieldRows(fVolumes), "zai-identity")

	mm, _ := m.Update(key("enter"))
	m = mm.(model)
	if m.mode != modeMenu {
		t.Fatalf("enter on a machine volume row must open its actions, mode=%v status=%q", m.mode, m.status)
	}
	acts := map[rowAct]bool{}
	for _, c := range m.rowChoices(fVolumes, m.menuRow) {
		acts[c.act] = true
	}
	if !acts[actVolDelete] || !acts[actVolClear] {
		t.Fatalf("menu must offer delete and clear: %+v", m.rowChoices(fVolumes, m.menuRow))
	}

	mm, _ = m.Update(key("c"))
	m = mm.(model)
	if m.mode != modeVolumes || m.volPendClear < 0 || !m.volPendEmpty {
		t.Fatalf("clear must arm the data screen's clear confirm: mode=%v pend=%d empty=%v err=%q", m.mode, m.volPendClear, m.volPendEmpty, m.volErr)
	}
	if v := m.viewVolumes(); !strings.Contains(v, "Clear \"zai-identity\"") || !strings.Contains(v, "logs out everywhere") {
		t.Fatalf("confirm must name the volume and its machine-wide reach:\n%s", v)
	}
	mm, _ = m.Update(key("y"))
	m = mm.(model)
	if len(fv.emptied) != 1 || fv.emptied[0] != "zai-identity" {
		t.Fatalf("y must clear the machine volume: %v", fv.emptied)
	}
	mm, _ = m.Update(key("esc"))
	m = mm.(model)
	if m.mode != modeList || m.listField != fVolumes {
		t.Fatalf("esc must return to the declaration list it came from, mode=%v", m.mode)
	}
}

// A per-project declaration in the --global editor is one volume per project,
// none of them this editor's to name: no data actions, and nothing is
// deleted.
func TestGlobalProjectVolumeRowOffersNoDataActions(t *testing.T) {
	fv := &fakeVols{}
	m := globalVolModel(fv)
	row := m.fieldRows(fVolumes)[rowIndex(t, m.fieldRows(fVolumes), ".zai-codex")]
	for _, c := range m.rowChoices(fVolumes, row) {
		if c.act == actVolDelete || c.act == actVolClear {
			t.Fatalf("project-scoped row in --global offered %q", c.label)
		}
	}
}

// Without an engine there is nothing to act on: no data actions anywhere.
func TestVolumeRowsOfferNoDataActionsWithoutAnAdmin(t *testing.T) {
	m := globalVolModel(nil)
	m.vols = nil
	row := m.fieldRows(fVolumes)[rowIndex(t, m.fieldRows(fVolumes), "zai-identity")]
	if len(m.rowChoices(fVolumes, row)) != 0 {
		t.Fatalf("no admin, but data actions offered: %+v", m.rowChoices(fVolumes, row))
	}
}

// The list's own keys reach the data actions on a skill row: x deletes, c
// clears, and d -- which has nothing to delete in the config there -- says
// where both are instead of only "disable the skill".
func TestSkillVolumeRowKeysReachTheDataActions(t *testing.T) {
	fv := &fakeVols{vols: []VolumeStatus{{Name: "zai-identity", Exists: true, Machine: true, Engine: "docker"}}}
	m := globalVolModel(fv)
	m.listCur = rowIndex(t, m.fieldRows(fVolumes), "zai-identity")

	mm, _ := m.Update(key("d"))
	if got := mm.(model).status; !strings.Contains(got, "x deletes") || !strings.Contains(got, "c clears") {
		t.Fatalf("d on a skill volume row must point at the data actions, got %q", got)
	}
	mm, _ = m.Update(key("x"))
	if d := mm.(model); d.mode != modeVolumes || d.volPendClear < 0 || d.volPendEmpty {
		t.Fatalf("x must arm the delete confirm: mode=%v pend=%d empty=%v", d.mode, d.volPendClear, d.volPendEmpty)
	}
	mm, _ = m.Update(key("c"))
	if c := mm.(model); c.mode != modeVolumes || c.volPendClear < 0 || !c.volPendEmpty {
		t.Fatalf("c must arm the clear confirm: mode=%v pend=%d empty=%v", c.mode, c.volPendClear, c.volPendEmpty)
	}
}

// With two engines the declared volume lists once per engine; the row's
// action aims at a copy that is ON DISK, not whichever engine listed first.
func TestVolumeRowActionPicksTheCopyOnDisk(t *testing.T) {
	fv := &fakeVols{vols: []VolumeStatus{
		{Name: "zai-identity", Exists: false, Machine: true, Engine: "docker"},
		{Name: "zai-identity", Exists: true, Machine: true, Engine: "podman"},
	}}
	m := globalVolModel(fv)
	m.listCur = rowIndex(t, m.fieldRows(fVolumes), "zai-identity")
	mm, _ := m.Update(key("x"))
	d := mm.(model)
	if d.volPendClear < 0 || d.volList[d.volPendClear].Engine != "podman" {
		t.Fatalf("delete must aim at the podman copy on disk: pend=%d err=%q", d.volPendClear, d.volErr)
	}
}

// With no engine behind the editor (a layer), c on a volume row explains
// itself instead of doing nothing.
func TestVolumeRowClearKeyExplainsWithoutAnEngine(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{Volumes: []config.Volume{{Name: "deps", Role: "cache", Target: "/w/nm"}}}, nil, nil, nil, nil, Inherited{}, nil, TargetLayer)
	m.mode = modeList
	m.listField = fVolumes
	m.listCur = rowIndex(t, m.fieldRows(fVolumes), "deps")
	mm, _ := m.Update(key("c"))
	if got := mm.(model).status; !strings.Contains(got, "no engine here") {
		t.Fatalf("c without an engine must say why, got %q", got)
	}
}
