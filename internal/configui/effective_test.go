package configui

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// A model over a project layer with a default layer, a template layer (go),
// and one skill contributing runtime state -- the ADR 0018 test bed.
func effectiveModel() model {
	inh := Inherited{
		HasLower: true,
		Default: config.Config{
			Apt:    []string{"ripgrep", "htop"},
			Env:    map[string]string{"GIT_EDITOR": "vim"},
			Mounts: []config.Mount{{Host: "~/notes", Target: "/home/dev/notes", Mode: "ro"}},
			Ports:  []config.Port{{Container: 5432}},
		},
		Templates: map[string]config.Config{
			"go": {Apt: []string{"golang"}},
		},
		Skills: map[string]SkillRuntime{
			"docker": {
				Mounts: []config.Mount{{Host: "/var/run/docker.sock", Target: "/var/run/docker.sock", Mode: "rw"}},
				Env:    map[string]string{"DOCKER_HOST": "unix:///var/run/docker.sock"},
			},
		},
	}
	cfg := config.Config{
		Template: "go",
		Apt:      []string{"build-essential", "!htop"},
		Skills:   []string{"docker"},
	}
	return newModel("t", "/tmp/x", cfg, []string{"go"}, nil, []string{"docker"}, nil, inh, nil, false)
}

func rowByText(t *testing.T, rows []listRow, text string) listRow {
	t.Helper()
	for _, r := range rows {
		if r.text == text {
			return r
		}
	}
	t.Fatalf("row %q not found in %+v", text, rows)
	return listRow{}
}

func TestAptRowsClassification(t *testing.T) {
	m := effectiveModel()
	rows := m.aptRows()

	if r := rowByText(t, rows, "ripgrep"); r.kind != rowInherited || r.source != "default" {
		t.Errorf("ripgrep should be inherited from default: %+v", r)
	}
	if r := rowByText(t, rows, "golang"); r.kind != rowInherited || r.source != "template:go" {
		t.Errorf("golang should be inherited from the template: %+v", r)
	}
	if r := rowByText(t, rows, "htop"); r.kind != rowRemoved || r.source != "default" {
		t.Errorf("htop should be removed-here: %+v", r)
	}
	if r := rowByText(t, rows, "build-essential"); r.kind != rowLocal || r.source != "" {
		t.Errorf("build-essential should be pure local: %+v", r)
	}
}

func TestAptRemoveHereAndRestore(t *testing.T) {
	m := effectiveModel()
	m.listField = fApt

	// d on the inherited row writes the marker...
	rows := m.fieldRows(fApt)
	for i, r := range rows {
		if r.text == "ripgrep" {
			m.listCur = i
		}
	}
	mm, _ := m.updateList(key("d"))
	m = mm.(model)
	if !contains(m.apt, "!ripgrep") {
		t.Fatalf("remove-here should append the marker: %v", m.apt)
	}
	if r := rowByText(t, m.fieldRows(fApt), "ripgrep"); r.kind != rowRemoved {
		t.Fatalf("row should flip to removed-here: %+v", r)
	}

	// ...and d on the removed row clears it (restore).
	rows = m.fieldRows(fApt)
	for i, r := range rows {
		if r.text == "ripgrep" {
			m.listCur = i
		}
	}
	mm, _ = m.updateList(key("d"))
	m = mm.(model)
	if contains(m.apt, "!ripgrep") {
		t.Fatalf("restore should drop the marker: %v", m.apt)
	}
}

func TestAptStaleMarkerVisibleAndClearable(t *testing.T) {
	m := effectiveModel()
	m.apt = append(m.apt, "!nothere")
	m.listField = fApt
	rows := m.fieldRows(fApt)
	r := rowByText(t, rows, "nothere")
	if r.kind != rowStaleMarker {
		t.Fatalf("marker matching nothing should be stale: %+v", r)
	}
	for i, rr := range rows {
		if rr.text == "nothere" {
			m.listCur = i
		}
	}
	mm, _ := m.updateList(key("d"))
	m = mm.(model)
	if contains(m.apt, "!nothere") {
		t.Fatalf("clear should drop the stale marker: %v", m.apt)
	}
}

func TestEnvRowsOverrideAndSkill(t *testing.T) {
	m := effectiveModel()
	rows := m.envRows()

	if r := rowByText(t, rows, "GIT_EDITOR=vim"); r.kind != rowInherited || r.source != "default" {
		t.Errorf("inherited env row wrong: %+v", r)
	}
	if r := rowByText(t, rows, "DOCKER_HOST=unix:///var/run/docker.sock"); r.kind != rowSkill || r.source != "skill:docker" {
		t.Errorf("skill env row wrong: %+v", r)
	}

	// A local entry with the inherited key shows as an override, in place.
	m.env = []kvItem{{Key: "GIT_EDITOR", Value: "emacs"}}
	if r := rowByText(t, m.envRows(), "GIT_EDITOR=emacs"); r.kind != rowOverride || r.source != "default" {
		t.Errorf("override row wrong: %+v", r)
	}
}

func TestEnvInheritedDeadEndAndOverridePrefill(t *testing.T) {
	m := effectiveModel()
	m.listField = fEnv
	rows := m.fieldRows(fEnv)
	for i, r := range rows {
		if r.kind == rowInherited {
			m.listCur = i
		}
	}

	// d is a dead-end: env has no unset -- it must explain, not mutate.
	mm, _ := m.updateList(key("d"))
	m2 := mm.(model)
	if len(m2.env) != 0 || m2.status == "" || !strings.Contains(m2.status, "override") {
		t.Fatalf("env d should be a status-line dead-end: env=%v status=%q", m2.env, m2.status)
	}

	// e opens the item editor prefilled with the inherited pair.
	mm, _ = m.updateList(key("e"))
	m3 := mm.(model)
	if m3.mode != modeItem || m3.editIndex != -1 {
		t.Fatalf("override should open an add editor: mode=%v idx=%d", m3.mode, m3.editIndex)
	}
	if m3.inputs[0].Value() != "GIT_EDITOR" || m3.inputs[1].Value() != "vim" {
		t.Fatalf("override editor not prefilled: %q=%q", m3.inputs[0].Value(), m3.inputs[1].Value())
	}
}

func TestMountRowsAndRemoveHere(t *testing.T) {
	m := effectiveModel()
	m.listField = fMounts
	rows := m.fieldRows(fMounts)

	inheritedLine := mountLine(config.Mount{Host: "~/notes", Target: "/home/dev/notes", Mode: "ro"})
	if r := rowByText(t, rows, inheritedLine); r.kind != rowInherited || r.source != "default" {
		t.Errorf("inherited mount row wrong: %+v", r)
	}
	skillLine := mountLine(config.Mount{Host: "/var/run/docker.sock", Target: "/var/run/docker.sock", Mode: "rw"})
	if r := rowByText(t, rows, skillLine); r.kind != rowSkill || r.source != "skill:docker" {
		t.Errorf("skill mount row wrong: %+v", r)
	}

	for i, r := range rows {
		if r.kind == rowInherited {
			m.listCur = i
		}
	}
	mm, _ := m.updateList(key("d"))
	m = mm.(model)
	want := config.Mount{Target: "!/home/dev/notes"}
	found := false
	for _, mt := range m.mounts {
		if mt == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("remove-here should append a !target marker: %+v", m.mounts)
	}
	// The layer with the marker still saves (ValidateLayer skips markers).
	if err := m.assemble().ValidateLayer(); err != nil {
		t.Fatalf("layer with mount marker should validate: %v", err)
	}
}

func TestMountOverridePrefillsEditor(t *testing.T) {
	m := effectiveModel()
	m.listField = fMounts
	for i, r := range m.fieldRows(fMounts) {
		if r.kind == rowInherited {
			m.listCur = i
		}
	}
	mm, _ := m.updateList(key("e"))
	m2 := mm.(model)
	if m2.mode != modeItem || m2.inputs[0].Value() != "~/notes" || m2.inputs[1].Value() != "/home/dev/notes" {
		t.Fatalf("mount override editor not prefilled: %q -> %q", m2.inputs[0].Value(), m2.inputs[1].Value())
	}
}

func TestPortRowsRemoveHere(t *testing.T) {
	m := effectiveModel()
	m.listField = fPorts
	rows := m.fieldRows(fPorts)
	if r := rowByText(t, rows, portLine(config.Port{Container: 5432})); r.kind != rowInherited {
		t.Fatalf("inherited port row wrong: %+v", r)
	}
	for i, r := range rows {
		if r.kind == rowInherited {
			m.listCur = i
		}
	}
	mm, _ := m.updateList(key("d"))
	m = mm.(model)
	if len(m.ports) != 1 || !m.ports[0].Remove || m.ports[0].Container != 5432 {
		t.Fatalf("remove-here should append a remove marker: %+v", m.ports)
	}
	if r := rowByText(t, m.fieldRows(fPorts), portLine(config.Port{Container: 5432})); r.kind != rowRemoved {
		t.Fatalf("row should flip to removed-here: %+v", r)
	}
}

func TestSkillRowEnterIsPointer(t *testing.T) {
	m := effectiveModel()
	m.mode = modeList
	m.listField = fMounts
	for i, r := range m.fieldRows(fMounts) {
		if r.kind == rowSkill {
			m.listCur = i
		}
	}
	mm, _ := m.updateList(key("enter"))
	m2 := mm.(model)
	if m2.mode != modeList || !strings.Contains(m2.status, "skill:docker") {
		t.Fatalf("skill row enter should stay in list with a pointer: mode=%v status=%q", m2.mode, m2.status)
	}
}

func TestMenuChoicesPerKind(t *testing.T) {
	labels := func(f fieldID, r listRow) string {
		var out []string
		for _, c := range rowChoices(f, r) {
			out = append(out, c.label)
		}
		return strings.Join(out, ",")
	}
	if got := labels(fApt, listRow{kind: rowInherited}); got != "Remove in this project" {
		t.Errorf("apt inherited menu: %q", got)
	}
	if got := labels(fEnv, listRow{kind: rowInherited}); got != "Override here" {
		t.Errorf("env inherited menu: %q", got)
	}
	if got := labels(fMounts, listRow{kind: rowInherited}); got != "Override here,Remove in this project" {
		t.Errorf("mounts inherited menu: %q", got)
	}
	if got := labels(fPorts, listRow{kind: rowInherited}); got != "Remove in this project" {
		t.Errorf("ports inherited menu: %q", got)
	}
	if got := labels(fApt, listRow{kind: rowLocal}); got != "Edit,Delete" {
		t.Errorf("local menu: %q", got)
	}
	if got := labels(fApt, listRow{kind: rowRemoved}); got != "Restore" {
		t.Errorf("removed menu: %q", got)
	}
	if got := labels(fMounts, listRow{kind: rowSkill}); got != "" {
		t.Errorf("skill rows must have no menu: %q", got)
	}
}

func TestMenuApplyRemoveHere(t *testing.T) {
	m := effectiveModel()
	m.listField = fApt
	rows := m.fieldRows(fApt)
	for i, r := range rows {
		if r.text == "ripgrep" {
			m.listCur = i
		}
	}
	// enter opens the menu on the row; enter applies its only action.
	mm, _ := m.updateList(key("enter"))
	m = mm.(model)
	if m.mode != modeMenu {
		t.Fatalf("enter should open the action menu, mode=%v", m.mode)
	}
	if v := m.viewMenu(); !strings.Contains(v, "Set in: default") {
		t.Fatalf("menu missing attribution:\n%s", v)
	}
	mm, _ = m.updateMenu(key("enter"))
	m = mm.(model)
	if m.mode != modeList || !contains(m.apt, "!ripgrep") {
		t.Fatalf("menu apply should remove-here and return: mode=%v apt=%v", m.mode, m.apt)
	}
}

func TestListSummariesCountEffectiveState(t *testing.T) {
	m := effectiveModel()
	// apt: ripgrep + golang inherited (htop removed), build-essential local = 3.
	if got := m.renderValue(fApt, false); !strings.Contains(got, "3 packages") || !strings.Contains(got, "2 inherited") {
		t.Errorf("apt summary: %q", got)
	}
	// env: GIT_EDITOR inherited + DOCKER_HOST from the docker skill = 2.
	if got := m.renderValue(fEnv, false); !strings.Contains(got, "2 vars") || !strings.Contains(got, "1 from skills") {
		t.Errorf("env summary: %q", got)
	}
	// mounts: 1 inherited + 1 skill; ports: 1 inherited.
	if got := m.renderValue(fMounts, false); !strings.Contains(got, "2 mounts") {
		t.Errorf("mounts summary: %q", got)
	}
	if got := m.renderValue(fPorts, false); !strings.Contains(got, "1 port ") && !strings.HasSuffix(got, "1 port") && !strings.Contains(got, "1 port  (") {
		t.Errorf("ports summary: %q", got)
	}
}

func TestViewListAnnotations(t *testing.T) {
	m := effectiveModel()
	m.listField = fApt
	v := m.viewList()
	for _, want := range []string{"(default)", "(template:go)", "(default — removed here)"} {
		if !strings.Contains(v, want) {
			t.Errorf("annotation %q missing:\n%s", want, v)
		}
	}
	m.listField = fMounts
	if v := m.viewList(); !strings.Contains(v, "(skill:docker)") {
		t.Errorf("skill annotation missing:\n%s", v)
	}
}

// The template picker is live: switching it away from "go" must drop the
// template's inherited rows on the spot.
func TestRowsFollowTemplatePicker(t *testing.T) {
	m := effectiveModel()
	for i, o := range m.tmplOpts {
		if o == noneOption {
			m.tmplSel = i
		}
	}
	for _, r := range m.aptRows() {
		if r.text == "golang" {
			t.Fatalf("template row survived deselecting the template: %+v", r)
		}
	}
}
