package configui

import (
	"reflect"
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
	return newModel("t", "/tmp/x", cfg, []string{"go"}, nil, []string{"docker"}, nil, inh, nil, TargetProject)
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

// The preview and the resolver must classify markers by the same rule. A bare
// "!" is not a marker (config.IsRemoval requires an identity), so it must not
// render as a stale marker -- it used to be classified by a bare CutPrefix
// here, which reported a marker for the empty name and rendered the entry
// twice.
func TestBareBangIsNotAMarkerInTheEffectiveView(t *testing.T) {
	m := effectiveModel()
	m.apt = append(m.apt, "!")
	var stale int
	for _, r := range m.aptRows() {
		if r.kind == rowStaleMarker {
			stale++
		}
	}
	if r := rowByText(t, m.aptRows(), "!"); r.kind != rowLocal {
		t.Errorf(`bare "!" must render as a local entry, not a marker: %+v`, r)
	}
	if stale != 0 {
		t.Errorf(`bare "!" produced %d stale-marker row(s)`, stale)
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
		for _, c := range (model{}).rowChoices(f, r) {
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
	// The offered-door action's label states the scope of the write: the
	// project editor writes this project; the --global editor writes
	// default.config — every project — and must say so, emphasized.
	if got := labels(fEgress, listRow{kind: rowOffered}); got != "Open in this project" {
		t.Errorf("project-mode offered menu: %q", got)
	}
	var g []string
	for _, c := range (model{target: TargetGlobal}).rowChoices(fEgress, listRow{kind: rowOffered}) {
		g = append(g, c.label)
	}
	if len(g) != 1 || !strings.Contains(g[0], "every project on this machine") {
		t.Errorf("global-mode offered menu must state machine scope: %q", g)
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
	// env: GIT_EDITOR inherited + DOCKER_HOST from the docker skill + the 6
	// shipped env_from_host keys (4 git-identity, ADR 0026, + TERM/TZ) = 8.
	if got := m.renderValue(fEnv, false); !strings.Contains(got, "8 vars") || !strings.Contains(got, "1 from skills") {
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

// Disabled mounts grant nothing, so the field summary (what the box actually
// gets) must not count them — local or inherited.
func TestListSummariesSkipDisabledMounts(t *testing.T) {
	m := effectiveModel()
	// Baseline from the fixture: 1 inherited + 1 skill = 2 effective.
	baseEff, _, _, _ := rowCounts(m.fieldRows(fMounts))
	if baseEff != 2 {
		t.Fatalf("fixture mounts baseline: got %d, want 2", baseEff)
	}

	// A disabled local mount is shown but not effective.
	m.mounts = []config.Mount{{Host: "/h/off", Target: "/off", Mode: "rw", Disabled: true}}
	if eff, _, _, _ := rowCounts(m.fieldRows(fMounts)); eff != baseEff {
		t.Errorf("disabled local mount counted as effective: got %d, want baseline %d", eff, baseEff)
	}
	// An enabled local mount adds one.
	m.mounts = []config.Mount{{Host: "/h/on", Target: "/on", Mode: "rw"}}
	if eff, _, _, _ := rowCounts(m.fieldRows(fMounts)); eff != baseEff+1 {
		t.Errorf("enabled local mount must count: got %d, want %d", eff, baseEff+1)
	}

	// A disabled inherited mount drops out of the tally (the fixture's
	// default /home/dev/src becomes a non-grant).
	m.mounts = nil
	m.inh.Default.Mounts[0].Disabled = true
	if eff, inh, _, _ := rowCounts(m.fieldRows(fMounts)); eff != 1 || inh != 0 {
		t.Errorf("disabled inherited mount must not count: eff=%d inh=%d, want 1 (skill only)", eff, inh)
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

// env_docs suggestion rows: a skill-documented consumed var nothing provides
// renders as a dim suggestion attributed to the skill; any provider (a layer,
// a skill's own env, the passthrough) makes it disappear; it is never counted
// as effective env; enter opens the add editor with the key prefilled.
func TestEnvDocSuggestionRows(t *testing.T) {
	m := effectiveModel()
	m.inh.Skills["gemini"] = SkillRuntime{EnvDocs: map[string]string{
		"GEMINI_API_KEY": "API key from aistudio.google.com",
		"DOCKER_HOST":    "consumed if set", // provided by the docker skill: no row
		"GIT_EDITOR":     "consumed if set", // provided by the default layer: no row
	}}
	m.skills = append(m.skills, "gemini")

	rows := m.envRows()
	sug := rowByText(t, rows, "GEMINI_API_KEY")
	if sug.kind != rowEnvDoc || sug.source != "skill:gemini" || sug.ident != "GEMINI_API_KEY" {
		t.Fatalf("suggestion row: %+v", sug)
	}
	for _, r := range rows {
		if r.kind == rowEnvDoc && r.text != "GEMINI_API_KEY" {
			t.Fatalf("provided vars must not render suggestions: %+v", r)
		}
	}
	if ann := rowAnnotation(sug); !strings.Contains(ann, "aistudio.google.com") || !strings.Contains(ann, "suggested by skill:gemini") {
		t.Fatalf("suggestion annotation = %q", ann)
	}
	// Not effective state: the summary counts and exposure ignore it.
	base := effectiveModel()
	if eff, _, _, _ := rowCounts(rows); eff != func() int { e, _, _, _ := rowCounts(base.envRows()); return e }() {
		t.Fatalf("suggestion counted as effective: %+v", rows)
	}
	if m.exposureNow().Env != base.exposureNow().Env {
		t.Fatalf("suggestion counted in exposure")
	}

	// Enter prefills the add editor with the key, cursor on the value.
	m.listField = fEnv
	for i, r := range m.fieldRows(fEnv) {
		if r.kind == rowEnvDoc {
			m.listCur = i
		}
	}
	mm, _ := m.updateList(key("enter"))
	got := mm.(model)
	if got.mode != modeItem || got.inputs[0].Value() != "GEMINI_API_KEY" || got.itemFocus != 1 {
		t.Fatalf("enter on a suggestion: mode=%v key=%q focus=%d", got.mode, got.inputs[0].Value(), got.itemFocus)
	}

	// Setting the var locally satisfies the suggestion — the row disappears.
	m.env = []kvItem{{Key: "GEMINI_API_KEY", Value: "secret"}}
	for _, r := range m.envRows() {
		if r.kind == rowEnvDoc {
			t.Fatalf("satisfied suggestion must disappear: %+v", r)
		}
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

// Same-layer add+remove resolves OFF (Merge applies removals last), so the
// rows and counts must not show the local entry as effective — and the marker
// is NOT stale: it's doing real work.
func TestSameLayerMarkerBeatsSameLayerEntry(t *testing.T) {
	m := effectiveModel()
	m.apt = []string{"foo", "!foo"}
	rows := m.aptRows()
	r := rowByText(t, rows, "foo")
	if r.kind != rowRemoved {
		t.Fatalf("same-layer add+remove should render removed: %+v", r)
	}
	for _, rr := range rows {
		if rr.kind == rowStaleMarker {
			t.Fatalf("a marker removing a same-layer entry is not stale: %+v", rr)
		}
	}
	// Replacing m.apt dropped the fixture's "!htop" too, so the inherited set
	// is ripgrep+htop+golang = 3; counting foo as effective would make it 4.
	if eff, _, _, _ := rowCounts(rows); eff != 3 {
		t.Fatalf("same-layer add+remove counted as effective: eff=%d rows=%+v", eff, rows)
	}

	m.mounts = []config.Mount{
		{Host: "/h", Target: "/x", Mode: "ro"},
		{Target: "!/x"},
	}
	mrows := m.mountRows()
	if r := rowByText(t, mrows, mountLine(config.Mount{Host: "/h", Target: "/x", Mode: "ro"})); r.kind != rowRemoved {
		t.Fatalf("same-layer mount add+remove should render removed: %+v", r)
	}

	m.ports = []config.Port{
		{Container: 8080},
		{Container: 8080, Remove: true},
	}
	prows := m.portRows()
	if r := rowByText(t, prows, portLine(config.Port{Container: 8080})); r.kind != rowRemoved {
		t.Fatalf("same-layer port add+remove should render removed: %+v", r)
	}
	for _, rr := range prows {
		if rr.kind == rowStaleMarker {
			t.Fatalf("port marker removing a same-layer binding is not stale: %+v", rr)
		}
	}
}

// A port removal marker must not share a dirty-detection signature with the
// real binding it removes.
func TestSigDistinguishesPortMarker(t *testing.T) {
	m := effectiveModel()
	m.ports = []config.Port{{Container: 5432, Remove: true}}
	a := m.sig()
	m.ports = []config.Port{{Container: 5432}}
	if b := m.sig(); a == b {
		t.Fatal("marker and real binding must sign differently")
	}
}

// A later layer binding a container port REPLACES the earlier layer's binding
// of it (ADR 0018's replace-by-container-port), so the screen shows one row
// for one container port, attributed to the layer that won. Each layer's
// binding is still identified by its full identity -- that is what makes the
// attribution name the right layer when two of them bind different ports.
func TestPortAttributionAfterReplacement(t *testing.T) {
	m := effectiveModel()
	m.inh.Templates["go"] = config.Config{
		Ports: []config.Port{{Container: 5432, Interface: "0.0.0.0", Host: 15432}},
	}
	rows := m.portRows()
	tmplLine := portLine(config.Port{Container: 5432, Interface: "0.0.0.0", Host: 15432})
	if r := rowByText(t, rows, tmplLine); r.source != "template:go" {
		t.Errorf("template's binding misattributed: %+v", r)
	}
	for _, r := range rows {
		if r.text == portLine(config.Port{Container: 5432}) {
			t.Errorf("the replaced binding must not read as effective: %+v", r)
		}
	}
	// Different container ports still coexist, each named by its own layer.
	m.inh.Templates["go"] = config.Config{Ports: []config.Port{{Container: 5433}}}
	rows = m.portRows()
	if r := rowByText(t, rows, portLine(config.Port{Container: 5432})); r.source != "default" {
		t.Errorf("default's binding misattributed: %+v", r)
	}
	if r := rowByText(t, rows, portLine(config.Port{Container: 5433})); r.source != "template:go" {
		t.Errorf("template's binding misattributed: %+v", r)
	}
}

// The Egress screen (ADR 0019): inherited/local/removed rows, skill endpoints
// read-only, and the unenforced note when no posture skill is on.
func TestEgressRowsAndRemoveHere(t *testing.T) {
	m := effectiveModel()
	m.inh.Default.Egress = []string{"grafana.com"}
	sk := m.inh.Skills["docker"]
	sk.Egress = []string{"registry.example.com:5000"}
	m.inh.Skills["docker"] = sk
	m.egress = []string{"api.stripe.com"}
	m.listField = fEgress

	rows := m.fieldRows(fEgress)
	if r := rowByText(t, rows, "grafana.com"); r.kind != rowInherited || r.source != "default" {
		t.Errorf("inherited egress row wrong: %+v", r)
	}
	if r := rowByText(t, rows, "api.stripe.com"); r.kind != rowLocal {
		t.Errorf("local egress row wrong: %+v", r)
	}
	if r := rowByText(t, rows, "registry.example.com:5000"); r.kind != rowSkill || r.source != "skill:docker" {
		t.Errorf("skill egress row wrong: %+v", r)
	}

	for i, r := range rows {
		if r.kind == rowInherited {
			m.listCur = i
		}
	}
	mm, _ := m.updateList(key("d"))
	m = mm.(model)
	if !contains(m.egress, "!grafana.com") {
		t.Fatalf("remove-here should append the marker: %v", m.egress)
	}
	if err := m.assemble().ValidateLayer(); err != nil {
		t.Fatalf("layer with egress marker should validate: %v", err)
	}
}

// Egress `!` markers are closures: they reach skill-declared endpoints (which
// no cascade merge could touch) and match on the parsed grammar — a portless
// closure closes every port. The rows must tell that story.
func TestEgressClosureRows(t *testing.T) {
	base := func() model {
		m := effectiveModel()
		sk := m.inh.Skills["docker"]
		sk.Egress = []string{"statsig.example.com", "api.example.com"}
		m.inh.Skills["docker"] = sk
		m.listField = fEgress
		return m
	}

	t.Run("local marker closes a skill endpoint, Restore clears it", func(t *testing.T) {
		m := base()
		m.egress = []string{"!statsig.example.com"}
		rows := m.fieldRows(fEgress)
		r := rowByText(t, rows, "statsig.example.com:443")
		if r.kind != rowRemoved || r.source != "skill:docker" || r.idx != 0 {
			t.Errorf("closed skill row wrong (want removed, marker idx 0): %+v", r)
		}
		if r := rowByText(t, rows, "api.example.com:443"); r.kind != rowSkill {
			t.Errorf("unclosed skill row should stay plain: %+v", r)
		}
		for _, rr := range rows {
			if rr.kind == rowStaleMarker {
				t.Errorf("a closure reaching a skill endpoint is not stale: %+v", rr)
			}
		}
	})
	t.Run("portless marker closes an inherited entry on any port", func(t *testing.T) {
		m := base()
		m.inh.Default.Egress = []string{"internal:8443"}
		m.egress = []string{"!internal"}
		rows := m.fieldRows(fEgress)
		if r := rowByText(t, rows, "internal:8443"); r.kind != rowRemoved {
			t.Errorf("portless closure should reach internal:8443: %+v", r)
		}
		for _, rr := range rows {
			if rr.kind == rowStaleMarker {
				t.Errorf("marker did real work, not stale: %+v", rr)
			}
		}
	})
	t.Run("lower-layer closure closes a skill endpoint read-only", func(t *testing.T) {
		m := base()
		m.inh.Default.Egress = []string{"!statsig.example.com"}
		rows := m.fieldRows(fEgress)
		r := rowByText(t, rows, "statsig.example.com:443")
		if r.kind != rowSkill || !strings.Contains(r.source, "closed by '!statsig.example.com'") {
			t.Errorf("skill row closed by a lower closure should say so, menu-less: %+v", r)
		}
		if !r.closed {
			t.Errorf("lower-closed skill row must carry closed=true: %+v", r)
		}
	})
	// WHO closed a door must not change whether it counts: a lower layer's
	// closure and this file's own marker land in different row kinds
	// (menu-less vs restorable), but the effective tally and the enforced
	// allowlist count must agree -- the review reproduced them disagreeing
	// (closed-by-lower kept rowSkill kind and kept counting as a grant).
	t.Run("closed doors tally identically regardless of closing layer", func(t *testing.T) {
		lower := base()
		lower.inh.Default.Egress = []string{"!statsig.example.com"}
		local := base()
		local.egress = []string{"!statsig.example.com"}
		lEff, _, lSk, _ := rowCounts(lower.fieldRows(fEgress))
		oEff, _, oSk, _ := rowCounts(local.fieldRows(fEgress))
		if lEff != oEff || lSk != oSk {
			t.Errorf("tally depends on which layer closed: lower(eff=%d,skill=%d) vs local(eff=%d,skill=%d)", lEff, lSk, oEff, oSk)
		}
		if lower.exposureNow().Egress != local.exposureNow().Egress {
			t.Errorf("exposure egress count depends on which layer closed: %d vs %d",
				lower.exposureNow().Egress, local.exposureNow().Egress)
		}
	})
	t.Run("local plain entry re-opens a lower closure", func(t *testing.T) {
		m := base()
		m.inh.Default.Egress = []string{"!statsig.example.com"}
		m.egress = []string{"statsig.example.com"}
		rows := m.fieldRows(fEgress)
		if r := rowByText(t, rows, "statsig.example.com:443"); r.kind != rowSkill || strings.Contains(r.source, "closed") {
			t.Errorf("re-opened skill row should be plain: %+v", r)
		}
		if r := rowByText(t, rows, "statsig.example.com"); r.kind != rowLocal {
			t.Errorf("the re-opening entry is this file's own row: %+v", r)
		}
	})
	t.Run("marker matching nothing anywhere is stale", func(t *testing.T) {
		m := base()
		m.egress = []string{"!nothing.example.com"}
		if r := rowByText(t, m.fieldRows(fEgress), "nothing.example.com"); r.kind != rowStaleMarker {
			t.Errorf("unmatched closure should be stale: %+v", r)
		}
	})
	t.Run("closed endpoint's offered door prints closed, not suppressed", func(t *testing.T) {
		m := base()
		m.inh.Default.EgressOffered = []string{"statsig.example.com"}
		m.egress = []string{"!statsig.example.com"}
		rows := m.fieldRows(fEgress)
		found := false
		for _, r := range rows {
			if r.kind == rowOffered && r.ident == "statsig.example.com" {
				found = true
			}
		}
		if !found {
			t.Errorf("offered door for a closed endpoint is truthfully closed — show it: %+v", rows)
		}
	})
}

func TestEgressSummaryUnenforcedNote(t *testing.T) {
	m := effectiveModel()
	m.egress = []string{"grafana.com"}
	// The docker fixture skill declares no posture -> unenforced.
	if got := m.renderValue(fEgress, false); !strings.Contains(got, "unenforced") {
		t.Errorf("egress summary should carry the unenforced note: %q", got)
	}
	sk := m.inh.Skills["docker"]
	sk.Posture = "deny-by-default"
	m.inh.Skills["docker"] = sk
	if got := m.renderValue(fEgress, false); strings.Contains(got, "unenforced") {
		t.Errorf("posture skill on -> no unenforced note: %q", got)
	}
	if got := m.renderValue(fEgress, false); !strings.Contains(got, "1 host") {
		t.Errorf("egress summary count: %q", got)
	}
}

func TestEgressItemEditorValidates(t *testing.T) {
	m := effectiveModel()
	m.listField = fEgress
	m = m.startItem(-1)
	m.inputs[0].SetValue("bad host")
	m = m.commitItem()
	if m.itemErr == "" || !strings.Contains(m.itemErr, "not a valid host") || len(m.egress) != 0 {
		t.Fatalf("malformed egress entry should be refused by host rule: err=%q egress=%v", m.itemErr, m.egress)
	}
	m.inputs[0].SetValue("internal:8443")
	m = m.commitItem()
	if m.itemErr != "" || len(m.egress) != 1 || m.egress[0] != "internal:8443" {
		t.Fatalf("valid egress entry should commit: err=%q egress=%v", m.itemErr, m.egress)
	}
}

// Under open-denylist an unmatched closure is this file's live row (Edit+
// Delete). Commit must accept the layer grammar: CutRemoval then ParseEgress
// on the name, store the raw entry with '!' intact — same path ValidateLayer
// runs, so the editor cannot drift from what a layer will accept.
func TestEgressClosureEditAndAdd(t *testing.T) {
	m := effectiveModel()
	m.inh.Skills["firewall-open"] = SkillRuntime{Posture: "open-denylist"}
	m.skills = append(m.skills, "firewall-open")
	m.egress = []string{"!statsig.example.com"}
	m.listField = fEgress

	r := rowByText(t, m.fieldRows(fEgress), "!statsig.example.com")
	if r.kind != rowLocal || r.idx != 0 {
		t.Fatalf("unmatched open-denylist closure should be rowLocal idx 0: %+v", r)
	}
	var acts []rowAct
	for _, c := range m.rowChoices(fEgress, r) {
		acts = append(acts, c.act)
	}
	if !reflect.DeepEqual(acts, []rowAct{actEdit, actDelete}) {
		t.Fatalf("rowLocal closure menu acts = %v, want Edit+Delete", acts)
	}

	// Edit: prefill is the raw entry; commit unchanged succeeds and preserves '!'.
	m = m.startItem(r.idx)
	if got := m.inputs[0].Value(); got != "!statsig.example.com" {
		t.Fatalf("edit prefill = %q, want raw closure", got)
	}
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("commit of unchanged closure should succeed: %q", m.itemErr)
	}
	if len(m.egress) != 1 || m.egress[0] != "!statsig.example.com" {
		t.Fatalf("unchanged commit should keep raw entry: %v", m.egress)
	}

	// Edit to a different closure: stored entry keeps the marker.
	m = m.startItem(0)
	m.inputs[0].SetValue("!other.example.com")
	m = m.commitItem()
	if m.itemErr != "" || len(m.egress) != 1 || m.egress[0] != "!other.example.com" {
		t.Fatalf("edited closure should commit with '!': err=%q egress=%v", m.itemErr, m.egress)
	}

	// Add path: a typed closure is authorable (the only UI path to close an
	// undeclared host under open-denylist).
	m = m.startItem(-1)
	m.inputs[0].SetValue("!blocked.example.com")
	m = m.commitItem()
	if m.itemErr != "" || len(m.egress) != 2 || m.egress[1] != "!blocked.example.com" {
		t.Fatalf("add of typed closure should commit: err=%q egress=%v", m.itemErr, m.egress)
	}

	// Malformed closure name: host rule. Bare '!' is not a closure (CutRemoval
	// requires an identity) so ParseEgress sees "!" — same refusal as the
	// layer validator, fragment "not a valid host".
	m = m.startItem(-1)
	m.inputs[0].SetValue("!bad host")
	if got := m.commitItem(); got.itemErr == "" || !strings.Contains(got.itemErr, "not a valid host") {
		t.Fatalf("malformed closure should name host rule: %q", got.itemErr)
	}
	m.inputs[0].SetValue("!")
	if got := m.commitItem(); got.itemErr == "" || !strings.Contains(got.itemErr, "not a valid host") {
		t.Fatalf("bare '!' should be refused by host rule: %q", got.itemErr)
	}
}

// Long rows must clip to the terminal width: a wrapped line corrupts the
// inline renderer's repaint accounting and strands stale rows (found live).
func TestViewClipsToWidth(t *testing.T) {
	m := effectiveModel()
	m.egress = []string{"a-very-long-hostname-that-overflows.example.internal:8443"}
	m.width = 40
	for _, line := range strings.Split(m.View(), "\n") {
		if w := len([]rune(stripANSI(line))); w > 40 {
			t.Fatalf("line wider than terminal (%d): %q", w, line)
		}
	}
}

// stripANSI removes CSI sequences for width-checking rendered lines.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case inEsc:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
		case r == 0x1b:
			inEsc = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestItemEditorTitles(t *testing.T) {
	m := effectiveModel()
	m.listField = fEgress
	m = m.startItem(-1)
	if v := m.viewItem(); !strings.Contains(v, "Add Egress host") || strings.Contains(v, "Egres\n") {
		t.Fatalf("egress item title wrong:\n%s", v)
	}
}

// Offered doors (ADR 0020): closed switches attributed to their source,
// suppressed once the entry is open, opened into THIS layer with one action.
func TestEgressOfferedRowsAndOpen(t *testing.T) {
	m := effectiveModel()
	m.inh.Templates["go"] = config.Config{EgressOffered: []string{"proxy.golang.org"}}
	sk := m.inh.Skills["docker"]
	sk.Offered = []string{"registry.example.com:5000"}
	m.inh.Skills["docker"] = sk
	m.listField = fEgress

	rows := m.fieldRows(fEgress)
	if r := rowByText(t, rows, "proxy.golang.org"); r.kind != rowOffered || r.source != "template:go" {
		t.Fatalf("template offered row wrong: %+v", r)
	}
	if r := rowByText(t, rows, "registry.example.com:5000"); r.kind != rowOffered || r.source != "skill:docker" {
		t.Fatalf("skill offered row wrong: %+v", r)
	}

	// Open the template's door: the entry lands in THIS layer's egress...
	for i, r := range rows {
		if r.text == "proxy.golang.org" {
			m.listCur = i
		}
	}
	mm, _ := m.updateList(key("o"))
	m = mm.(model)
	if !contains(m.egress, "proxy.golang.org") {
		t.Fatalf("open should write the entry into this layer: %v", m.egress)
	}
	// ...and the offered row disappears in favor of the open (local) one.
	rows = m.fieldRows(fEgress)
	if r := rowByText(t, rows, "proxy.golang.org"); r.kind != rowLocal {
		t.Fatalf("opened door should show as a local entry: %+v", r)
	}
	// Deleting the local entry re-surfaces the offer (peel-consistent).
	for i, r := range rows {
		if r.text == "proxy.golang.org" {
			m.listCur = i
		}
	}
	mm, _ = m.updateList(key("d"))
	m = mm.(model)
	if r := rowByText(t, m.fieldRows(fEgress), "proxy.golang.org"); r.kind != rowOffered {
		t.Fatalf("closing the door should re-surface the offer: %+v", r)
	}
}

func TestEgressOfferedNeverEnforced(t *testing.T) {
	// Offered entries must not reach the resolved allowlist: resolvedEgress is
	// commands-side, but the config merge must also keep them out of Egress.
	got := config.Merge(
		config.Config{EgressOffered: []string{"proxy.golang.org"}},
		config.Config{Egress: []string{"grafana.com"}},
	)
	if contains(got.Egress, "proxy.golang.org") {
		t.Fatalf("offered leaked into open egress: %v", got.Egress)
	}
	if !contains(got.EgressOffered, "proxy.golang.org") {
		t.Fatalf("offered should survive the merge: %v", got.EgressOffered)
	}
}

func TestEgressSummaryCountsOffered(t *testing.T) {
	m := effectiveModel()
	m.inh.Templates["go"] = config.Config{EgressOffered: []string{"proxy.golang.org", "sum.golang.org"}}
	if got := m.renderValue(fEgress, false); !strings.Contains(got, "2 offered") {
		t.Errorf("summary should count offered doors: %q", got)
	}
}

// Offered-door suppression compares normalized host:port and counts skill
// egress as open: "github.com" offered vs "github.com:443"
// open is the same door.
func TestEgressOfferedSuppressionNormalized(t *testing.T) {
	m := effectiveModel()
	m.inh.Templates["go"] = config.Config{EgressOffered: []string{"github.com", "api.anthropic.com"}}
	m.egress = []string{"github.com:443"} // equivalent spelling of the offer
	sk := m.inh.Skills["docker"]
	sk.Egress = append(sk.Egress, "api.anthropic.com") // skill already opens it
	m.inh.Skills["docker"] = sk
	for _, r := range m.fieldRows(fEgress) {
		if r.kind == rowOffered {
			t.Fatalf("offered row for an already-open door: %+v", r)
		}
	}
}

// The one-line exposure summary tallies the same effective rows the per-field
// summaries count, and speaks in config.Exposure's shared words — the launch
// lines and this line must tell the same story.
func TestExposureNowAndFormLine(t *testing.T) {
	m := effectiveModel()
	e := m.exposureNow()
	// 1 inherited mount (default) + 1 skill mount; 1 inherited port;
	// GIT_EDITOR inherited + DOCKER_HOST from the skill + 6 shipped
	// env_from_host keys (git identity + TERM/TZ); no posture skill.
	if e.Mounts != 2 || e.DisabledMounts != 0 {
		t.Errorf("mounts = %d (+%d disabled), want 2 (+0)", e.Mounts, e.DisabledMounts)
	}
	if e.Ports != 1 || e.Env != 8 {
		t.Errorf("ports/env = %d/%d, want 1/8 (incl. the 6 shipped env_from_host keys)", e.Ports, e.Env)
	}
	if e.Posture != "" || e.Egress != 0 {
		t.Errorf("no posture skill enabled, got posture %q egress %d", e.Posture, e.Egress)
	}
	if e.RawRunArgs || e.RawBuild {
		t.Errorf("no raw config in the test bed: %+v", e)
	}
	want := "exposure: 2 host mounts · 1 port · 8 env vars · network open"
	if got := m.viewForm(); !strings.Contains(got, want) {
		t.Errorf("form missing %q:\n%s", want, got)
	}
}

// Disabled mounts split out of the exposure count (no bind), whichever layer
// they live in; a posture skill flips the network segment to the allowlist.
func TestExposureNowDisabledMountsAndPosture(t *testing.T) {
	m := effectiveModel()
	// Switch the inherited mount off in the default layer, add a local live one.
	m.inh.Default.Mounts[0].Disabled = true
	m.mounts = []config.Mount{{Host: "/h/src", Target: "/src", Mode: "rw"}}
	// Enable a firewall skill declaring the posture and one endpoint. The
	// user's "github.com" restates the skill's door in another spelling —
	// normalized dedup counts one enforced host, matching launch's tally.
	m.inh.Skills["firewall"] = SkillRuntime{Posture: "deny-by-default", Egress: []string{"github.com:443"}}
	m.skills = append(m.skills, "firewall")
	m.egress = []string{"example.com", "github.com"}
	// A local env entry restating the skill's key is one variable, not two.
	m.env = []kvItem{{Key: "DOCKER_HOST", Value: "unix:///x"}}

	e := m.exposureNow()
	// Local /src + the docker skill's socket stay live; the default mount is off.
	if e.Mounts != 2 || e.DisabledMounts != 1 {
		t.Errorf("mounts = %d (+%d disabled), want 2 (+1)", e.Mounts, e.DisabledMounts)
	}
	if e.Env != 8 { // GIT_EDITOR + DOCKER_HOST (restated key folds) + 6 shipped env_from_host
		t.Errorf("env = %d, want 8", e.Env)
	}
	if e.Posture != "deny-by-default" {
		t.Errorf("posture = %q, want deny-by-default", e.Posture)
	}
	// The skill's endpoint + the user's own example.com; the dup spelling folds.
	if e.Egress != 2 {
		t.Errorf("egress = %d, want 2", e.Egress)
	}
	if !strings.Contains(m.viewForm(), "network deny-by-default · egress 2 hosts") {
		t.Errorf("form missing the posture segment:\n%s", m.viewForm())
	}
}

// Under open-denylist the network is open: the summary must count the
// closures (the enforced list), never the allowlist (unenforced there) —
// and an unmatched closure is a live entry, not a stale marker (it blocks a
// real host whether or not anything declared it).
func TestExposureNowOpenDenylist(t *testing.T) {
	m := effectiveModel()
	m.inh.Skills["firewall-open"] = SkillRuntime{Posture: "open-denylist"}
	m.skills = append(m.skills, "firewall-open")
	sk := m.inh.Skills["docker"]
	sk.Egress = []string{"registry.example.com:5000"}
	m.inh.Skills["docker"] = sk
	m.egress = []string{"!statsig.example.com", "!telemetry.example.com:443"}
	e := m.exposureNow()
	if e.Egress != 0 {
		t.Errorf("allowlist count must not render under open-denylist: %d", e.Egress)
	}
	if e.Closed != 2 {
		t.Errorf("closed = %d, want 2", e.Closed)
	}
	if !strings.Contains(e.NetworkLine(), "network open-denylist · 2 hosts blocked") {
		t.Errorf("summary must carry the blocked count: %q", e.NetworkLine())
	}
	for _, r := range m.fieldRows(fEgress) {
		if r.kind == rowStaleMarker {
			t.Errorf("no closure is stale under open-denylist: %+v", r)
		}
	}
	if r := rowByText(t, m.fieldRows(fEgress), "!statsig.example.com"); r.kind != rowLocal {
		t.Errorf("unmatched closure should render as this file's live entry: %+v", r)
	}
}

// Raw escape hatches — this layer's or an inherited layer's — degrade the
// posture claim in the summary, mirroring status's networkLine honesty rule.
func TestExposureNowRawConfigDegradesPosture(t *testing.T) {
	m := effectiveModel()
	m.inh.Skills["firewall"] = SkillRuntime{Posture: "deny-by-default"}
	m.skills = append(m.skills, "firewall")
	m.runArgs = "--privileged"
	m.inh.Default.DockerfilePre = []string{"RUN true"}
	e := m.exposureNow()
	if !e.RawRunArgs || !e.RawBuild {
		t.Errorf("raw flags = %v/%v, want true/true", e.RawRunArgs, e.RawBuild)
	}
	if !strings.Contains(e.NetworkLine(), "not guaranteed") {
		t.Errorf("degraded posture must say so: %q", e.NetworkLine())
	}
}

// The off-switch and absence are DIFFERENT states, and the editor must be able
// to tell them apart, set either, and round-trip both. `agent = "none"` beats a
// lower layer's agent in the merge; absence inherits it. Rendering both through
// one picker row let a zero-edit save delete the sentinel and silently hand an
// agentless project the layer's agent.
func TestScalarPickersDistinguishInheritFromOff(t *testing.T) {
	base := func(cfg config.Config) model {
		m := effectiveModel()
		m.inh.Default.Agent = "claude"
		m.agents = []string{"claude", "codex"}
		return m.loadConfig(cfg)
	}

	t.Run("absent selects inherit and writes nothing", func(t *testing.T) {
		m := base(config.Config{})
		if got, want := m.agentOpts[len(m.agentOpts)-2:], []string{noneOption, "(inherit: claude)"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("none and inherit must finish the picker in that order: got %v, want %v", got, want)
		}
		if !isInheritRow(m.agentOpts[m.agentSel]) {
			t.Fatalf("absent agent must select the inherit row, got %q in %v", m.agentOpts[m.agentSel], m.agentOpts)
		}
		if !strings.Contains(m.agentOpts[m.agentSel], "claude") {
			t.Errorf("the inherit row must name the inherited value: %q", m.agentOpts[m.agentSel])
		}
		if got := m.assemble().Agent; got != "" {
			t.Errorf("inherit must write nothing, got %q", got)
		}
		if m.agentNow() != "claude" {
			t.Errorf("the EFFECTIVE agent under inherit is the inherited one, got %q", m.agentNow())
		}
	})

	t.Run("explicit none survives a zero-edit save", func(t *testing.T) {
		m := base(config.Config{Agent: config.NoneLabel})
		if m.agentOpts[m.agentSel] != noneOption {
			t.Fatalf("agent=none must select the none row, got %q", m.agentOpts[m.agentSel])
		}
		if got := m.assemble().Agent; got != config.NoneLabel {
			t.Errorf("a zero-edit save must preserve the off-switch, got %q (this is the reported data loss)", got)
		}
		if m.agentNow() != "" {
			t.Errorf("the EFFECTIVE agent under none is empty, got %q", m.agentNow())
		}
	})

	t.Run("choosing none over an inherited agent writes the sentinel", func(t *testing.T) {
		m := base(config.Config{})
		m.agentSel = indexOf(m.agentOpts, noneOption)
		if got := m.assemble().Agent; got != config.NoneLabel {
			t.Errorf("turning the agent off against a lower layer must write %q, got %q", config.NoneLabel, got)
		}
	})

	// With nothing below, absence and the sentinel mean the same thing, so the
	// picker offers no inherit row and byre keeps writing absent -- an ordinary
	// save must not start churning files with a redundant key.
	t.Run("no lower value means no inherit row and no churn", func(t *testing.T) {
		m := effectiveModel()
		m.inh.HasLower = false
		m.agents = []string{"claude"}
		m = m.loadConfig(config.Config{})
		if hasInheritRow(m.agentOpts) {
			t.Errorf("no lower agent must mean no inherit row: %v", m.agentOpts)
		}
		m.agentSel = indexOf(m.agentOpts, noneOption)
		if got := m.assemble().Agent; got != "" {
			t.Errorf("with nothing below, the none row still writes absent, got %q", got)
		}
	})

	// engine carries the same shape with "auto" as its sentinel.
	t.Run("engine auto is an off-switch against an inherited engine", func(t *testing.T) {
		m := effectiveModel()
		m.inh.Default.Engine = "podman"
		m = m.loadConfig(config.Config{})
		if !isInheritRow(m.engineOpts[m.engineSel]) {
			t.Fatalf("absent engine must select inherit, got %q in %v", m.engineOpts[m.engineSel], m.engineOpts)
		}
		if m.engineOpts[0] != "auto" {
			t.Errorf("the engine picker keeps auto first: %v", m.engineOpts)
		}
		m.engineSel = indexOf(m.engineOpts, "auto")
		if got := m.assemble().Engine; got != "auto" {
			t.Errorf("choosing auto against an inherited engine must write it, got %q", got)
		}
	})
}

// A binding replaces an inherited one of the same container port, so the
// screen must not show two live publishes where the box gets one. The
// replaced row stays (it is config, and Remove still writes a marker) but
// counts as nothing.
func TestPortRowsShowReplacementNotUnion(t *testing.T) {
	m := effectiveModel() // default binds 5432
	m.ports = []config.Port{{Container: 5432, Host: 15432}}
	rows := m.portRows()

	replaced := rowByText(t, rows, portLine(config.Port{Container: 5432}))
	if replaced.kind != rowInherited || !replaced.closed {
		t.Errorf("the inherited binding must read as replaced, not live: %+v", replaced)
	}
	if ann := rowAnnotation(replaced); !strings.Contains(ann, "replaced by this file") {
		t.Errorf("the replaced row must say what happened to it: %q", ann)
	}
	mine := rowByText(t, rows, portLine(config.Port{Container: 5432, Host: 15432}))
	if mine.kind != rowLocal || mine.closed {
		t.Errorf("this file's binding is the live one: %+v", mine)
	}
	if eff, _, _, _ := rowCounts(rows); eff != 1 {
		t.Errorf("one container port, one effective publish: got %d", eff)
	}

	// The same rule inside one file: the later binding wins, the earlier one
	// is shown replaced rather than silently counted.
	m.ports = []config.Port{{Container: 4000, Host: 8080}, {Container: 4000, Host: 9090}}
	rows = m.portRows()
	first := rowByText(t, rows, portLine(config.Port{Container: 4000, Host: 8080}))
	if !first.closed {
		t.Errorf("an earlier same-file binding must not read as effective: %+v", first)
	}
	if ann := rowAnnotation(first); !strings.Contains(ann, "later entry in this file") {
		t.Errorf("the shadowed row must say why: %q", ann)
	}
	if last := rowByText(t, rows, portLine(config.Port{Container: 4000, Host: 9090})); last.closed {
		t.Errorf("the last binding of a port is the live one: %+v", last)
	}
}

// The editor's exposure line is the THIRD rendering of the posture claim
// `byre status` and develop's launch banner also make, so it degrades on the
// same input: a skill holding one of byre's own BYRE_ network knobs (ADR
// 0050 tier 2). Sibling of internal/commands'
// TestLaunchBannerAndStatusDegradeOnOneReservedEnvSet, deliberately carrying
// the SAME table of keys: three surfaces, one answer per key, and a table
// that disagrees with its sibling fails against the shared owner
// (skills.ReservedEnvClaims) rather than drifting quietly.
//
// The wanted values are literals, never read back from the predicate under
// test: computing them would pin only that this surface agrees with itself.
func TestEditorExposureDegradesOnTheSameReservedEnvSet(t *testing.T) {
	for _, tc := range []struct {
		key   string
		hedge bool
	}{
		{"BYRE_EGRESS", true},
		{"BYRE_EGRESS_DENY", true},
		{"BYRE_LAUNCH_GATE_FILE", true},
		{"BYRE_LAUNCH_GATE_TIMEOUT", true},
		{"BYRE_MCP_CONFIG", false},    // MCP delivery's knob, not the network's
		{"BYRE_CONTEXT_DIR", false},   // context delivery's
		{"BYRE_WORKSPACE_DIR", false}, // launch-only
		{"BYRE_KNOB_FROM_A_LATER", true},
	} {
		inh := Inherited{Skills: map[string]SkillRuntime{
			"fw":    {Posture: "deny-by-default", Egress: []string{"example.com:443"}},
			"knobs": {Env: map[string]string{tc.key: "/somewhere/else"}},
		}}
		cfg := config.Config{Skills: []string{"fw", "knobs"}}
		m := newModel("t", "/x", cfg, nil, nil, []string{"fw", "knobs"}, nil, inh, nil, TargetProject)

		e := m.exposureNow()
		if e.SkillNetControls != tc.hedge {
			t.Errorf("%s: the editor's degradation input = %v, want %v", tc.key, e.SkillNetControls, tc.hedge)
		}
		// The input has to reach the claim the user reads, not just the tally.
		if got := strings.Contains(e.NetworkLine(), "not guaranteed"); got != tc.hedge {
			t.Errorf("%s: editor exposure qualified = %v, want %v: %s", tc.key, got, tc.hedge, e.NetworkLine())
		}
	}
}

// The Env screen is where a user MEETS a skill's reserved BYRE_ key, and it
// showed the key attributed to its skill and nothing about what holding it
// costs -- a claim `byre status` spells out on its own row. The annotation
// closes that gap on the EXISTING row (P0: a TUI claim gap ranks with an
// engine one) and speaks skills.ReservedEnvSkew, the owner status's row
// speaks, so the two surfaces cannot disagree about a key.
//
// The wanted values are literals, never read back from the helper under test.
func TestSkillReservedEnvRowNamesTheClaimsItSkews(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"knobs": {Env: map[string]string{
			"BYRE_EGRESS":  "example.com:443",
			"BYRE_SCRATCH": "/home/dev/scratch",
			"EDITOR":       "vim",
		}},
	}}
	m := newModel("t", "/x", config.Config{Skills: []string{"knobs"}}, nil, nil,
		[]string{"knobs"}, nil, inh, nil, TargetProject)
	rows := m.envRows()

	// A chassis knob names the claims that stop asserting while it is set.
	known := rowByText(t, rows, "BYRE_EGRESS=example.com:443")
	if ann := rowAnnotation(known); ann != "  (skill:knobs — skews: network)" {
		t.Errorf("a reserved knob's row must name its skill AND its claims: %q", ann)
	}

	// A key wearing the prefix byre does not read gets the honest register,
	// never an announcement of a control byre has never heard of.
	unknown := rowAnnotation(rowByText(t, rows, "BYRE_SCRATCH=/home/dev/scratch"))
	if !strings.Contains(unknown, "not a control byre recognizes") {
		t.Errorf("an unrecognized reserved key must say so: %q", unknown)
	}
	if !strings.Contains(unknown, "skews: network + launch") {
		t.Errorf("an unrecognized reserved key still degrades conservatively: %q", unknown)
	}
	if strings.Contains(unknown, "runtime control") {
		t.Errorf("an unrecognized key must not read as a byre control: %q", unknown)
	}

	// An ordinary skill env var is unchanged: the note is about byre's own
	// namespace, not about skill env in general.
	plain := rowByText(t, rows, "EDITOR=vim")
	if plain.skews != "" || rowAnnotation(plain) != "  (skill:knobs)" {
		t.Errorf("a plain skill env row must be untouched: %+v %q", plain, rowAnnotation(plain))
	}

	// The note rides the existing row: one skill row per skill env key, no
	// extra row and nothing reordered, so the cursor still indexes what it
	// indexed before. (The rest of the screen is byre's shipped passthrough.)
	skillRows := 0
	for _, r := range rows {
		if r.kind == rowSkill {
			skillRows++
		}
	}
	if skillRows != 3 {
		t.Errorf("annotating a row must not mint one: %d skill rows, want 3", skillRows)
	}
}

// Ticking the skill is what arms the hedge: the editor's claim answers for
// the state on screen, so a skill present but switched OFF asserts the
// posture, and switching it on stops asserting in the same keystroke. This is
// the one thing the launch and status renderings cannot check -- they resolve
// once, and only this surface has a "not yet saved" state to get wrong.
func TestEditorExposureFollowsTheLiveSkillToggle(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"fw":    {Posture: "deny-by-default", Egress: []string{"example.com:443"}},
		"knobs": {Env: map[string]string{"BYRE_LAUNCH_GATE_FILE": "/dev/null"}},
	}}
	m := newModel("t", "/x", config.Config{Skills: []string{"fw"}}, nil, nil,
		[]string{"fw", "knobs"}, nil, inh, nil, TargetProject)
	if e := m.exposureNow(); e.SkillNetControls {
		t.Fatalf("a skill that is not enabled skews nothing: %s", e.NetworkLine())
	}
	m.skills = append(m.skills, "knobs")
	if e := m.exposureNow(); !e.SkillNetControls {
		t.Errorf("enabling the skill must stop the claim asserting: %s", e.NetworkLine())
	}
}

// A box already running for the project qualifies the headline WITHOUT
// re-scoping it: the exposure line still describes the next launch (it
// describes the config being edited, which is the only thing this editor can
// change), and the note says when that launch is.
func TestExposureHeadlineQualifiedByALiveBox(t *testing.T) {
	m := effectiveModel()
	base := m.viewForm()
	if strings.Contains(base, "box running") {
		t.Fatalf("no note without a live box:\n%s", base)
	}
	m.liveNote = "box running -- changes apply at next launch"
	got := m.viewForm()
	// The exposure line itself is untouched; the note rides after it.
	if !strings.Contains(got, "exposure: 2 host mounts · 1 port · 8 env vars · network open · box running") {
		t.Errorf("headline must keep its next-launch line and gain the note:\n%s", got)
	}
}
