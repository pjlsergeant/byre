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
	for _, c := range (model{global: true}).rowChoices(fEgress, listRow{kind: rowOffered}) {
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

// Same-layer add+remove resolves OFF (Merge applies removals last), so the
// rows and counts must not show the local entry as effective — and the marker
// is NOT stale: it's doing real work (review finding, round 1).
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
// real binding it removes (review finding, round 1).
func TestSigDistinguishesPortMarker(t *testing.T) {
	m := effectiveModel()
	m.ports = []config.Port{{Container: 5432, Remove: true}}
	a := m.sig()
	m.ports = []config.Port{{Container: 5432}}
	if b := m.sig(); a == b {
		t.Fatal("marker and real binding must sign differently")
	}
}

// Two lower layers binding the same container port on different interfaces
// must each be attributed to their own layer (review finding, round 3).
func TestPortAttributionByFullIdentity(t *testing.T) {
	m := effectiveModel()
	m.inh.Templates["go"] = config.Config{
		Ports: []config.Port{{Container: 5432, Interface: "0.0.0.0", Host: 15432}},
	}
	rows := m.portRows()
	if r := rowByText(t, rows, portLine(config.Port{Container: 5432})); r.source != "default" {
		t.Errorf("default's binding misattributed: %+v", r)
	}
	tmplLine := portLine(config.Port{Container: 5432, Interface: "0.0.0.0", Host: 15432})
	if r := rowByText(t, rows, tmplLine); r.source != "template:go" {
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
	if m.itemErr == "" || len(m.egress) != 0 {
		t.Fatalf("malformed egress entry should be rejected: err=%q egress=%v", m.itemErr, m.egress)
	}
	m.inputs[0].SetValue("internal:8443")
	m = m.commitItem()
	if m.itemErr != "" || len(m.egress) != 1 || m.egress[0] != "internal:8443" {
		t.Fatalf("valid egress entry should commit: err=%q egress=%v", m.itemErr, m.egress)
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
// egress as open (review finding): "github.com" offered vs "github.com:443"
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
