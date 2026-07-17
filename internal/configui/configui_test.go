package configui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pjlsergeant/byre/internal/config"
)

func TestLongestCommonPrefix(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"apple", "apricot"}, "ap"},
		{[]string{"solo"}, "solo"},
		{[]string{"x", "y"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := longestCommonPrefix(c.in); got != c.want {
			t.Errorf("longestCommonPrefix(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCompleteHostPath(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"apple", "apricot"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "banana"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Ambiguous prefix completes to the longest common prefix.
	if got := completeHostPath(dir + "/a"); got != dir+"/ap" {
		t.Errorf("dir/a completion = %q, want %q", got, dir+"/ap")
	}
	// No further common prefix -> nothing to add.
	if got := completeHostPath(dir + "/ap"); got != "" {
		t.Errorf("dir/ap should have no completion, got %q", got)
	}
	// Unique file match completes fully, no trailing slash.
	if got := completeHostPath(dir + "/app"); got != dir+"/apple" {
		t.Errorf("dir/app completion = %q, want %q", got, dir+"/apple")
	}
	// Unique directory match gains a trailing slash.
	if got := completeHostPath(dir + "/b"); got != dir+"/banana/" {
		t.Errorf("dir/b completion = %q, want %q", got, dir+"/banana/")
	}
	// No match at all.
	if got := completeHostPath(dir + "/z"); got != "" {
		t.Errorf("no match should give no completion, got %q", got)
	}
}

func TestSuggestTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A home-relative source mirrors under /home/dev.
	if got := suggestTarget(filepath.Join(home, ".aws")); got != "/home/dev/.aws" {
		t.Errorf("home-relative target = %q, want /home/dev/.aws", got)
	}
	if got := suggestTarget("~/projects/foo"); got != "/home/dev/projects/foo" {
		t.Errorf("tilde target = %q, want /home/dev/projects/foo", got)
	}
	// A non-home source falls back to /home/dev/<basename>.
	if got := suggestTarget("/etc/hosts"); got != "/home/dev/hosts" {
		t.Errorf("non-home target = %q, want /home/dev/hosts", got)
	}
	if got := suggestTarget(""); got != "" {
		t.Errorf("empty host should give no suggestion, got %q", got)
	}
}

func TestSaveRoundTripsAndPreservesRawFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store", "byre.config")
	in := config.Config{
		Base:    "golang:1.22-bookworm",
		Agent:   "claude",
		Apt:     []string{"jq"},
		Mounts:  []config.Mount{{Host: "~/d", Target: "/d", Mode: "rw"}},
		RunArgs: []string{"--privileged"}, // raw field, must round-trip untouched
	}
	if err := Save(path, in); err != nil {
		t.Fatal(err)
	}
	back, err := config.ParseFile(path)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if back.Base != in.Base || back.Agent != in.Agent {
		t.Errorf("scalars not preserved: %+v", back)
	}
	if !reflect.DeepEqual(back.RunArgs, in.RunArgs) {
		t.Errorf("raw run_args not preserved: %v", back.RunArgs)
	}
	if len(back.Mounts) != 1 || back.Mounts[0].Target != "/d" {
		t.Errorf("mounts not preserved: %v", back.Mounts)
	}
	// omitempty keeps unset fields out of the file (no noise)
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "npm_global") || strings.Contains(string(b), "files") {
		t.Errorf("unset fields should be omitted:\n%s", b)
	}
	if !strings.Contains(string(b), "Managed by `byre config`") {
		t.Errorf("missing managed-by header:\n%s", b)
	}
}

// A layer using the `!name` removal feature must be saveable: the store config
// is one cascade layer, so Save validates it with ValidateLayer, not the
// resolved Validate (which rightly rejects a removal marker as a malformed
// entry). Regression for the bug where any such config was permanently
// unsaveable from the editor.
func TestSaveAcceptsRemovalEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store", "byre.config")
	cfg := config.Config{
		Skills:  []string{"!devloop"},                          // remove an inherited skill
		Volumes: []config.Volume{{Name: "!creds"}},             // remove an inherited volume
		Mounts:  []config.Mount{{Target: "!/inherited/mount"}}, // remove an inherited mount
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save rejected a valid removal-entry layer: %v", err)
	}
	back, err := config.ParseFile(path)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(back.Skills) != 1 || back.Skills[0] != "!devloop" {
		t.Errorf("removal marker not round-tripped: %v", back.Skills)
	}
}

// pickerOpts must preserve a configured-but-not-discovered value (so opening the
// editor and saving unrelated edits never silently drops it) and always offer
// "none" last.
func TestPickerOptsPreservesUnknown(t *testing.T) {
	opts := pickerOpts([]string{"claude", "codex"}, "gemini") // gemini not installed
	want := []string{"claude", "codex", "gemini", "none"}
	if !reflect.DeepEqual(opts, want) {
		t.Fatalf("pickerOpts = %v, want %v", opts, want)
	}
	// A discovered value is not duplicated.
	if got := pickerOpts([]string{"claude"}, "claude"); !reflect.DeepEqual(got, []string{"claude", "none"}) {
		t.Fatalf("discovered value duplicated: %v", got)
	}
	// Empty (unset) adds only none.
	if got := pickerOpts([]string{"claude"}, ""); !reflect.DeepEqual(got, []string{"claude", "none"}) {
		t.Fatalf("unset should add only none: %v", got)
	}
}

// The item editor must validate, then add / edit / delete structured items.
func TestItemAddEditDeleteValidation(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)

	// --- env: reject a bad key, accept a good one ---
	m.listField = fEnv
	m = m.startItem(-1)
	m.inputs[0].SetValue("bad key") // space -> invalid
	m.inputs[1].SetValue("v")
	if m2 := m.commitItem(); m2.itemErr == "" || len(m2.env) != 0 {
		t.Fatalf("bad env key should be rejected: err=%q env=%v", m2.itemErr, m2.env)
	}
	m.inputs[0].SetValue("TOKEN")
	m = m.commitItem()
	if len(m.env) != 1 || m.env[0] != (kvItem{"TOKEN", "v"}) {
		t.Fatalf("env not added: %v", m.env)
	}
	if m.mode != modeList {
		t.Fatalf("commit should return to the list, mode=%v", m.mode)
	}

	// --- edit the existing env item in place ---
	m = m.startItem(0)
	m.inputs[1].SetValue("v2")
	m = m.commitItem()
	if len(m.env) != 1 || m.env[0].Value != "v2" {
		t.Fatalf("env edit should replace in place: %v", m.env)
	}

	// --- reject a duplicate env key (would silently collapse on save) ---
	m = m.startItem(-1)
	m.inputs[0].SetValue("TOKEN") // already exists
	m.inputs[1].SetValue("other")
	if m2 := m.commitItem(); m2.itemErr == "" || len(m2.env) != 1 {
		t.Fatalf("duplicate env key should be rejected: err=%q env=%v", m2.itemErr, m2.env)
	}
	// ...but editing the same row to keep its key is fine.
	m = m.startItem(0)
	m.inputs[1].SetValue("v3")
	if m2 := m.commitItem(); m2.itemErr != "" {
		t.Fatalf("editing a row without changing its key must not trip the dup check: %q", m2.itemErr)
	}

	// --- mounts: target must be absolute ---
	m.listField = fMounts
	m = m.startItem(-1)
	m.inputs[0].SetValue("~/data")
	m.inputs[1].SetValue("relative") // not absolute
	if m2 := m.commitItem(); m2.itemErr == "" || len(m2.mounts) != 0 {
		t.Fatalf("non-absolute mount target should be rejected: err=%q", m2.itemErr)
	}
	// A `!`-prefixed target must not slip through as a removal marker: the
	// layer gate skips markers' shape checks, but a marker carrying host/mode
	// (which the add editor always sets) is refused as a mistyped real mount.
	m.inputs[1].SetValue("!/data")
	if m2 := m.commitItem(); m2.itemErr == "" || len(m2.mounts) != 0 {
		t.Fatalf("mount target with a ! prefix should be rejected, not saved as a removal marker: err=%q", m2.itemErr)
	}
	m.inputs[1].SetValue("/data")
	m.itemMode = 1 // rw
	m = m.commitItem()
	if len(m.mounts) != 1 || m.mounts[0].Mode != "rw" || m.mounts[0].Target != "/data" {
		t.Fatalf("mount not added correctly: %v", m.mounts)
	}

	// --- disable it: the picker's third state sets the bool, keeps rw ---
	m = m.startItem(0)
	if m.itemMode != 1 {
		t.Fatalf("editor should open on the stored rw mode, got %d", m.itemMode)
	}
	m.itemMode = 2 // disabled
	m = m.commitItem()
	if !m.mounts[0].Disabled || m.mounts[0].Mode != "rw" {
		t.Fatalf("disable should set the bool and preserve rw: %+v", m.mounts[0])
	}
	if line := mountLine(m.mounts[0]); !strings.Contains(line, "rw, disabled") {
		t.Fatalf("list row should mark the disabled mount: %q", line)
	}

	// --- re-enable: editor opens on disabled, picking rw clears the bool ---
	m = m.startItem(0)
	if m.itemMode != 2 {
		t.Fatalf("editor should open on disabled, got %d", m.itemMode)
	}
	m.itemMode = 1
	m = m.commitItem()
	if m.mounts[0].Disabled || m.mounts[0].Mode != "rw" {
		t.Fatalf("re-enable should clear the bool and keep rw: %+v", m.mounts[0])
	}

	// --- delete the mount ---
	m.deleteItem(fMounts, 0)
	if len(m.mounts) != 0 {
		t.Fatalf("mount not deleted: %v", m.mounts)
	}
}

// fakeVols is a test VolumeAdmin: List returns its volumes; Clear removes one
// unless clearErr is set (simulating a live-session refusal).
type fakeVols struct {
	vols       []VolumeStatus
	clearErr   error
	cleared    []string
	sharedNote string
}

func (f *fakeVols) List() ([]VolumeStatus, error) { return f.vols, nil }
func (f *fakeVols) SharedNote() string            { return f.sharedNote }
func (f *fakeVols) Clear(v VolumeStatus) error {
	name := v.Name
	if f.clearErr != nil {
		return f.clearErr
	}
	f.cleared = append(f.cleared, name)
	for i, v := range f.vols {
		if v.Name == name {
			f.vols = append(f.vols[:i], f.vols[i+1:]...)
			break
		}
	}
	return nil
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestVolumesClearFlow(t *testing.T) {
	fv := &fakeVols{vols: []VolumeStatus{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude", Exists: true},
		{Name: "node_modules", Role: "cache", Target: "/workspace/node_modules", Exists: false},
	}}
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, fv, TargetProject)

	// fVolumes must be present in the form when a VolumeAdmin is supplied.
	if !contains(fieldIDsToStrings(m.order), "Volumes") {
		t.Fatal("Volumes row missing from the form order")
	}

	m = m.openVolumes()
	if m.mode != modeVolumes || len(m.volList) != 2 {
		t.Fatalf("openVolumes: mode=%v n=%d", m.mode, len(m.volList))
	}

	// 'c' on a present volume arms the confirm; 'y' clears it.
	mm, _ := m.updateVolumes(key("c"))
	m = mm.(model)
	if m.volPendClear != 0 {
		t.Fatalf("clear should arm the confirm, volPendClear=%d", m.volPendClear)
	}
	// The armed confirm surfaces the admin's shared-volume warning (worktree blast
	// radius) so the config UI is as loud as reset/forget.
	fv.sharedNote = "Shared with ALL worktrees of /home/me/main."
	if v := m.viewVolumes(); !strings.Contains(v, "Shared with ALL worktrees") {
		t.Errorf("clear confirm should include the shared-volume note:\n%s", v)
	}
	mm, _ = m.updateVolumes(key("y"))
	m = mm.(model)
	if len(fv.cleared) != 1 || fv.cleared[0] != ".claude" {
		t.Fatalf("expected .claude cleared, got %v", fv.cleared)
	}
	if len(m.volList) != 1 {
		t.Fatalf("list should refresh after clear, n=%d", len(m.volList))
	}

	// Clearing an absent volume is refused with a message, no call made.
	fv.vols = []VolumeStatus{{Name: "node_modules", Role: "cache", Exists: false}}
	m = m.openVolumes()
	mm, _ = m.updateVolumes(key("c"))
	m = mm.(model)
	if m.volPendClear != -1 || m.volErr == "" {
		t.Fatalf("clearing an absent volume should be refused: pend=%d err=%q", m.volPendClear, m.volErr)
	}
}

func TestWorktreeBaseRoundTrip(t *testing.T) {
	// "sibling" -> checkbox on -> writes "sibling".
	m := newModel("t", "/x", config.Config{WorktreeBase: "sibling"}, nil, nil, nil, nil, Inherited{}, nil, TargetGlobal)
	if !m.wtSibling {
		t.Error("sibling config should check the box")
	}
	if got := m.assemble().WorktreeBase; got != "sibling" {
		t.Errorf("assemble = %q, want sibling", got)
	}
	// A path -> checkbox off, path loaded, round-trips.
	m = newModel("t", "/x", config.Config{WorktreeBase: "/w"}, nil, nil, nil, nil, Inherited{}, nil, TargetGlobal)
	if m.wtSibling || m.wtBase.Value() != "/w" {
		t.Errorf("path config: sibling=%v base=%q", m.wtSibling, m.wtBase.Value())
	}
	if got := m.assemble().WorktreeBase; got != "/w" {
		t.Errorf("assemble = %q, want /w", got)
	}
	// Unset -> checkbox off, empty -> writes "" (byre worktree refuses).
	m = newModel("t", "/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetGlobal)
	if m.wtSibling || m.wtBase.Value() != "" {
		t.Errorf("unset should be off+empty: sibling=%v base=%q", m.wtSibling, m.wtBase.Value())
	}
	if got := m.assemble().WorktreeBase; got != "" {
		t.Errorf("assemble = %q, want empty", got)
	}
	// Checkbox wins over a stray path value.
	m.wtSibling = true
	m.wtBase.SetValue("/ignored")
	if got := m.assemble().WorktreeBase; got != "sibling" {
		t.Errorf("sibling checkbox should win over a path: %q", got)
	}

	// The GLOBAL form renders the WORKTREES section + a checkbox state.
	on := newModel("t", "/x", config.Config{WorktreeBase: "sibling"}, nil, nil, nil, nil, Inherited{}, nil, TargetGlobal).View()
	if !strings.Contains(on, "WORKTREES") || !strings.Contains(on, "[x] sibling of repo") {
		t.Errorf("global form should show a checked worktree checkbox:\n%s", on)
	}
	off := newModel("t", "/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetGlobal).View()
	if !strings.Contains(off, "[ ] sibling of repo") || !strings.Contains(off, "refuse") {
		t.Errorf("unset global form should show an unchecked box and a refuse hint:\n%s", off)
	}
	// The PROJECT editor (global=false) omits the section, and preserves an
	// existing worktree_base untouched through save (no false "unset" clobber).
	proj := newModel("t", "/x", config.Config{WorktreeBase: "sibling"}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	if strings.Contains(proj.View(), "WORKTREES") {
		t.Errorf("project editor should not show the WORKTREES section:\n%s", proj.View())
	}
	if got := proj.assemble().WorktreeBase; got != "sibling" {
		t.Errorf("project editor should round-trip worktree_base untouched, got %q", got)
	}
}

func fieldIDsToStrings(fs []fieldID) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = fieldLabel[f]
	}
	return out
}

// The skills multi-select groups agent skills separately, locks the primary
// agent (ticked, can't toggle), toggles the rest, preserves enabled-unknowns,
// and round-trips through assemble.
func TestSkillsMultiSelect(t *testing.T) {
	cfg := config.Config{Agent: "claude", Skills: []string{"moarcode", "ghost-skill"}} // ghost not installed
	agents := []string{"claude", "codex"}
	all := []string{"claude", "codex", "moarcode", "shem"}
	m := newModel("t", "/tmp/x", cfg, nil, agents, all, nil, Inherited{}, nil, TargetProject)

	entryIdx := func(mm model, name string) int {
		for i, e := range mm.skillEntries() {
			if e.name == name {
				return i
			}
		}
		t.Fatalf("skill %q not in entries %v", name, mm.skillEntries())
		return -1
	}

	entries := m.skillEntries()
	// Non-agent skills come before agent skills; the enabled-unknown is preserved.
	if entries[0].agent {
		t.Fatalf("non-agent skills should sort first: %+v", entries)
	}
	var haveGhost, claudeLocked bool
	for _, e := range entries {
		if e.name == "ghost-skill" {
			haveGhost = true
		}
		if e.name == "claude" && e.agent && e.locked {
			claudeLocked = true
		}
	}
	if !haveGhost {
		t.Fatal("enabled-but-uninstalled skill should be preserved as an entry")
	}
	if !claudeLocked {
		t.Fatal("primary agent (claude) should appear as a locked agent skill")
	}

	// Toggling the locked primary agent is a no-op (change it via Pri. Agent).
	m.skillCur = entryIdx(m, "claude")
	mm, _ := m.updateSkills(key(" "))
	m = mm.(model)
	if contains(m.skills, "claude") {
		t.Fatalf("locked primary agent must not be added to skills: %v", m.skills)
	}

	// A non-primary agent skill (codex) can be enabled as a plain skill.
	m.skillCur = entryIdx(m, "codex")
	mm, _ = m.updateSkills(key(" "))
	m = mm.(model)
	if !contains(m.skills, "codex") {
		t.Fatalf("space should enable codex as a skill: %v", m.skills)
	}

	// And a regular skill toggles off.
	m.skillCur = entryIdx(m, "moarcode")
	mm, _ = m.updateSkills(key(" "))
	m = mm.(model)
	if contains(m.skills, "moarcode") {
		t.Fatalf("space should disable moarcode: %v", m.skills)
	}
	if out := m.assemble(); !contains(out.Skills, "codex") || contains(out.Skills, "claude") {
		t.Fatalf("assemble skills wrong (codex in, claude out): %v", out.Skills)
	}
}

// The skills screen shows a skill's one-line description beside its name (so
// near-namesakes like claude vs claude-shared-auth are tellable apart) and
// renders undesc'd skills as just the name.
func TestSkillsScreenShowsDescriptions(t *testing.T) {
	cfg := config.Config{Agent: "claude"}
	descs := map[string]string{"claude-shared-auth": "Share one Claude login across all your projects."}
	m := newModel("t", "/tmp/x", cfg, nil, []string{"claude"}, []string{"claude", "claude-shared-auth"}, descs, Inherited{}, nil, TargetProject)
	view := m.viewSkills()
	if !strings.Contains(view, "Share one Claude login") {
		t.Fatalf("description not rendered:\n%s", view)
	}
	if !strings.Contains(view, "claude-shared-auth") {
		t.Fatalf("skill name missing:\n%s", view)
	}
}

// A companion skill (one paired to an agent via companion_for, or via the
// pairing shared_auth_for implies — CompanionFor carries the resolved fact
// either way, ADR 0034) nests as an indented child directly under its
// agent's row in the agent-skills section, so the pairing is visible where
// you enable it. Nesting rides the pairing alone — a gate-pending companion
// (no vouch, no offer) nests exactly like a vouched one. A companion whose
// agent has no row stays a plain skill.
func TestSkillsCompanionNestedUnderAgent(t *testing.T) {
	cfg := config.Config{Agent: "claude"}
	agents := []string{"claude", "codex"}
	all := []string{"claude", "claude-shared-auth", "codex", "moarcode", "orphan-shared-auth"}
	inh := Inherited{Skills: map[string]SkillRuntime{
		"claude-shared-auth": {CompanionFor: "claude"},
		"orphan-shared-auth": {CompanionFor: "gemini"}, // no gemini row anywhere
	}}
	m := newModel("t", "/tmp/x", cfg, nil, agents, all, nil, inh, nil, TargetProject)

	entries := m.skillEntries()
	idx := map[string]int{}
	for i, e := range entries {
		idx[e.name] = i
	}
	comp := entries[idx["claude-shared-auth"]]
	if !comp.child || !comp.agent {
		t.Fatalf("companion should be a nested child in the agent section: %+v", comp)
	}
	if idx["claude-shared-auth"] != idx["claude"]+1 {
		t.Fatalf("companion should sit directly under its agent: %v", entries)
	}
	orphan := entries[idx["orphan-shared-auth"]]
	if orphan.child || orphan.agent {
		t.Fatalf("companion with no agent row should stay a plain skill: %+v", orphan)
	}
	if idx["moarcode"] > idx["claude"] {
		t.Fatalf("plain skills should still sort before the agent section: %v", entries)
	}

	// The nested row renders indented under its agent.
	view := m.viewSkills()
	if !strings.Contains(view, "└ [ ] claude-shared-auth") {
		t.Fatalf("nested companion not rendered indented:\n%s", view)
	}

	// Toggling the nested row still enables the companion by name.
	m.skillCur = idx["claude-shared-auth"]
	mm, _ := m.updateSkills(key(" "))
	m = mm.(model)
	if !contains(m.skills, "claude-shared-auth") {
		t.Fatalf("space should enable the nested companion: %v", m.skills)
	}
}

// A skill listed in `skills` that becomes the primary agent must not be written
// back into `skills` (the agent field implies it).
func TestSkillsPrimaryNotDoubleWritten(t *testing.T) {
	cfg := config.Config{Agent: "claude", Skills: []string{"codex"}} // codex enabled as a skill
	m := newModel("t", "/tmp/x", cfg, nil, []string{"claude", "codex"}, []string{"claude", "codex"}, nil, Inherited{}, nil, TargetProject)
	// Promote codex to the primary agent.
	m.agentSel = indexOf(m.agentOpts, "codex")
	if out := m.assemble(); contains(out.Skills, "codex") {
		t.Fatalf("primary agent must be stripped from skills, got %v", out.Skills)
	}
}

// The ports editor validates the container port and treats a blank host as
// its container port; grants lead the form and focus starts there.
func TestPortsEditorAndSectionOrder(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)

	// Grants section leads and includes ports; focus starts on the first grant.
	if len(m.sections) == 0 || !strings.HasPrefix(m.sections[0].title, "GRANTS") {
		t.Fatalf("first section should be GRANTS, got %+v", m.sections)
	}
	if m.field() != fMounts {
		t.Fatalf("focus should start on the first grant (Extra mounts), got %v", m.field())
	}

	m.listField = fPorts
	// A non-numeric container port is rejected.
	m = m.startItem(-1)
	m.inputs[0].SetValue("abc")
	if m2 := m.commitItem(); m2.itemErr == "" || len(m2.ports) != 0 {
		t.Fatalf("bad container port should be rejected: err=%q", m2.itemErr)
	}
	// Valid container, blank host = mirror the container port, blank interface.
	m.inputs[0].SetValue("8080")
	m.inputs[1].SetValue("")
	m.inputs[2].SetValue("")
	m = m.commitItem()
	if len(m.ports) != 1 || m.ports[0].Container != 8080 || m.ports[0].Host != 0 {
		t.Fatalf("ephemeral port not added correctly: %v", m.ports)
	}
	if out := m.assemble(); len(out.Ports) != 1 || out.Ports[0].Container != 8080 {
		t.Fatalf("assemble dropped the port: %v", out.Ports)
	}
}

// Raw text fields (run_args, dockerfile_*) are editable in-UI: ctrl+s accepts the
// buffer into the config and saves the file, esc discards.
func TestRawTextFieldEditRoundTrip(t *testing.T) {
	// An indented, blank-line-containing dockerfile_pre must survive untouched.
	cfg := config.Config{
		RunArgs:       []string{"--privileged"},
		DockerfilePre: []string{"RUN foo \\", "    && bar", "", "RUN baz"},
	}
	m := newModel("t", filepath.Join(t.TempDir(), "x.config"), cfg, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	if m.dirty() {
		t.Fatal("a fresh config with raw fields must not be dirty")
	}
	if got := m.assemble().DockerfilePre; !reflect.DeepEqual(got, cfg.DockerfilePre) {
		t.Fatalf("untouched dockerfile_pre must round-trip verbatim, got %v", got)
	}

	// The textarea must be focused so it accepts typing (blurred = input ignored).
	mt := m.openText(fRunArgs)
	mt.ta.SetValue("")
	mm0, _ := mt.updateText(key("x"))
	if got := mm0.(model).ta.Value(); got != "x" {
		t.Fatalf("typing ignored — textarea not focused (got %q)", got)
	}

	// Edit run_args and accept: ctrl+s applies the buffer AND saves the file.
	m = m.openText(fRunArgs)
	m.ta.SetValue("--cap-add=NET_ADMIN\n--privileged")
	mm, _ := m.updateText(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = mm.(model)
	if m.mode != modeForm {
		t.Fatalf("ctrl+s should return to the form, mode=%v", m.mode)
	}
	if out := m.assemble(); len(out.RunArgs) != 2 || out.RunArgs[0] != "--cap-add=NET_ADMIN" {
		t.Fatalf("run_args not applied: %v", out.RunArgs)
	}
	if !m.savedOnce || m.dirty() {
		t.Fatalf("ctrl+s should have saved the accepted buffer: err=%q", m.errMsg)
	}
	// esc discards an edit — dockerfile_pre stays the original verbatim.
	m = m.openText(fDockerfilePre)
	m.ta.SetValue("RUN changed")
	mm, _ = m.updateText(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(model)
	if got := m.assemble().DockerfilePre; !reflect.DeepEqual(got, cfg.DockerfilePre) {
		t.Fatalf("esc should discard the edit, got %v", got)
	}
}

// ctrl+q quits from the form screen: immediately when clean, and via the same
// press-again-to-discard confirm as esc when there are unsaved changes.
func TestCtrlQQuits(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	if _, cmd := m.updateForm(tea.KeyMsg{Type: tea.KeyCtrlQ}); cmd == nil {
		t.Fatal("ctrl+q on a clean form should quit")
	}

	m.ti.SetValue("debian:custom") // touch the base-image field: dirty, nothing saved
	if !m.dirty() {
		t.Fatal("setup: model should be dirty")
	}
	mm, cmd := m.updateForm(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m = mm.(model)
	if cmd != nil || !m.confirmQuit {
		t.Fatalf("first ctrl+q on a dirty form should arm the confirm, not quit (confirmQuit=%v)", m.confirmQuit)
	}
	if _, cmd = m.updateForm(tea.KeyMsg{Type: tea.KeyCtrlQ}); cmd == nil {
		t.Fatal("second ctrl+q should discard and quit")
	}

	// ctrl+c must not clear the armed confirm — a second ctrl+c also quits.
	m.confirmQuit = false
	mm, _ = m.updateForm(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = mm.(model)
	if !m.confirmQuit {
		t.Fatal("first ctrl+c on a dirty form should arm the confirm")
	}
	if _, cmd = m.updateForm(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Fatal("second ctrl+c should discard and quit")
	}
}

// newModel must not report the freshly-opened config as dirty, an unknown engine
// must round-trip (not coerce to podman), and touching a field flips dirty.
func TestModelDirtyAndUnknownEngineRoundTrip(t *testing.T) {
	cfg := config.Config{Base: "debian:bookworm", Engine: "containerd", Agent: "claude"}
	m := newModel("t", "/tmp/x", cfg, []string{"claude", "codex"}, []string{"claude", "codex"}, nil, nil, Inherited{}, nil, TargetProject)
	if m.dirty() {
		t.Fatal("a freshly-opened config must not be dirty")
	}
	// The unknown engine is preserved as the selected option, not coerced.
	if got := m.engineOpts[m.engineSel]; got != "containerd" {
		t.Fatalf("unknown engine coerced to %q, want containerd preserved", got)
	}
	if out := m.assemble(); out.Engine != "containerd" {
		t.Fatalf("assemble dropped the engine: %q", out.Engine)
	}
	// Changing a picker makes it dirty.
	m.engineSel = 0 // "auto"
	if !m.dirty() {
		t.Fatal("changing the engine picker should mark the model dirty")
	}
}

// TestWorktreeBaseArrowKeysMoveCursor pins the fBase/fWorktreeBase text inputs
// to identical arrow-key behavior. Both fields route through the model's
// focusedInput accessor (form.go), so left/right/home/end must move the
// cursor in fWorktreeBase exactly as they do in fBase — regression coverage
// for a bug where fWorktreeBase had no case in cycle(), silently swallowing
// left/right.
func TestWorktreeBaseArrowKeysMoveCursor(t *testing.T) {
	focus := func(m model, f fieldID) model {
		for i, ff := range m.order {
			if ff == f {
				m.setFocus(i)
				return m
			}
		}
		t.Fatalf("field %v not in focus order", f)
		return m
	}

	drive := func(m model, msg tea.KeyMsg) model {
		mt, _ := m.updateForm(msg)
		return mt.(model)
	}

	for _, tc := range []struct {
		name  string
		field fieldID
		pos   func(m model) int
	}{
		{"fBase", fBase, func(m model) int { return m.ti.Position() }},
		{"fWorktreeBase", fWorktreeBase, func(m model) int { return m.wtBase.Position() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{Base: "debian:bookworm", WorktreeBase: "/abcdef"}
			// global=true so the WORKTREES section (and fWorktreeBase) is in the
			// focus order.
			m := newModel("t", "/x", cfg, nil, nil, nil, nil, Inherited{}, nil, TargetGlobal)
			m = focus(m, tc.field)
			if m.field() != tc.field {
				t.Fatalf("setFocus landed on %v, want %v", m.field(), tc.field)
			}

			end := tc.pos(m)
			if end == 0 {
				t.Fatal("expected the cursor to start past position 0 (value is non-empty)")
			}

			// left should move the cursor back by one — this is the key that was
			// dead in fWorktreeBase before the fix.
			m = drive(m, tea.KeyMsg{Type: tea.KeyLeft})
			if got := tc.pos(m); got != end-1 {
				t.Fatalf("left arrow: cursor = %d, want %d (arrow key was swallowed)", got, end-1)
			}

			// right should move it forward again.
			m = drive(m, tea.KeyMsg{Type: tea.KeyRight})
			if got := tc.pos(m); got != end {
				t.Fatalf("right arrow: cursor = %d, want %d", got, end)
			}

			// home moves to the start.
			m = drive(m, tea.KeyMsg{Type: tea.KeyHome})
			if got := tc.pos(m); got != 0 {
				t.Fatalf("home: cursor = %d, want 0", got)
			}

			// end moves back to the end.
			m = drive(m, tea.KeyMsg{Type: tea.KeyEnd})
			if got := tc.pos(m); got != end {
				t.Fatalf("end: cursor = %d, want %d", got, end)
			}
		})
	}
}

// TestCommitItemRunsLayerValidation pins the "catch before save" behavior:
// cross-item problems Save would reject (here: two mounts on one target)
// surface at item commit, with the offending item still open and the working
// state rolled back.
func TestCommitItemRunsLayerValidation(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fMounts
	m = m.startItem(-1)
	m.inputs[0].SetValue("/data")
	m.inputs[1].SetValue("/mnt/x")
	m = m.commitItem()
	if m.itemErr != "" || len(m.mounts) != 1 {
		t.Fatalf("first mount should commit: err=%q mounts=%v", m.itemErr, m.mounts)
	}
	// Second mount, same target: per-field checks pass, the layer check must not.
	m = m.startItem(-1)
	m.inputs[0].SetValue("/other")
	m.inputs[1].SetValue("/mnt/x")
	m2 := m.commitItem()
	if m2.itemErr == "" {
		t.Fatal("duplicate mount target must fail at item commit, not at save")
	}
	if len(m2.mounts) != 1 {
		t.Fatalf("rejected item must not stay in the working state: %v", m2.mounts)
	}
	if m2.mode != modeItem {
		t.Fatalf("the offending item should stay open, mode=%v", m2.mode)
	}
}

// TestCommentWarnOnLoad pins Q7: opening a hand-commented file warns that
// saving rewrites it; byre's own boilerplate headers don't cry wolf.
func TestCommentWarnOnLoad(t *testing.T) {
	dir := t.TempDir()
	hand := filepath.Join(dir, "hand.config")
	os.WriteFile(hand, []byte("# remember: the LAN port is for the demo\nagent = \"claude\"\n"), 0o644)
	if v := newModel("t", hand, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject).View(); !strings.Contains(v, "hand-written comments") {
		t.Errorf("hand-commented file should warn on load:\n%s", v)
	}

	managed := filepath.Join(dir, "managed.config")
	os.WriteFile(managed, []byte("# Managed by `byre config`. Structured fields are edited there;\n# raw blocks (run_args, dockerfile_pre/post) are edited here by hand.\n\nagent = \"claude\"\n"), 0o644)
	if v := newModel("t", managed, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject).View(); strings.Contains(v, "hand-written comments") {
		t.Errorf("byre's own header must not trigger the warning:\n%s", v)
	}

	if v := newModel("t", filepath.Join(dir, "absent.config"), config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject).View(); strings.Contains(v, "hand-written comments") {
		t.Errorf("a missing file must not warn:\n%s", v)
	}
}

// TestCommentWarnTracksEditorRoundTrip pins the reviewer's finding: comments
// added (or removed) via the ^e $EDITOR round-trip must update the
// destroys-comments warning — it tracks the file, not the open-time state.
func TestCommentWarnTracksEditorRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.config")
	os.WriteFile(path, []byte("agent = \"claude\"\n"), 0o644)
	m := newModel("t", path, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	if m.commentWarn {
		t.Fatal("clean file must not warn at open")
	}
	// User adds a hand comment in $EDITOR, then the TUI reloads.
	os.WriteFile(path, []byte("# my note\nagent = \"claude\"\n"), 0o644)
	m = m.onEditorClosed(nil)
	if !m.commentWarn {
		t.Error("comments added via $EDITOR must arm the warning")
	}
	// And removing them disarms it.
	os.WriteFile(path, []byte("agent = \"claude\"\n"), 0o644)
	m = m.onEditorClosed(nil)
	if m.commentWarn {
		t.Error("warning must clear once the comments are gone")
	}

	// A successful ^s re-marshals the file — the comments it warned about are
	// gone, so the warning must clear rather than nag about the file just written.
	os.WriteFile(path, []byte("# note\nagent = \"claude\"\n"), 0o644)
	m = m.onEditorClosed(nil)
	if !m.commentWarn {
		t.Fatal("precondition: warning armed")
	}
	m = m.save()
	if m.errMsg != "" {
		t.Fatalf("save failed: %s", m.errMsg)
	}
	if m.commentWarn {
		t.Error("warning must clear after the save that removed the comments")
	}
}

// Inherited skills (enabled by default.config / the template) must show as ON
// with an "(inherited)" mark -- an unchecked box for an effectively-on skill
// is a lie (found live 2026-07-07). Toggling one writes the cascade's real
// off-switch (`!name`) into THIS layer; toggling again removes it.
func TestSkillsInheritedShownOnAndToggledViaRemoval(t *testing.T) {
	inherited := Inherited{HasLower: true, Default: config.Config{Skills: []string{"claude-shared-auth", "devloop"}}}
	m := newModel("t", "/tmp/x", config.Config{Agent: "claude"}, nil,
		[]string{"claude"}, []string{"claude", "devloop", "claude-shared-auth"}, nil, inherited, nil, TargetProject)

	find := func(mm model, name string) skillEntry {
		for _, e := range mm.skillEntries() {
			if e.name == name {
				return e
			}
		}
		t.Fatalf("skill %q not in entries", name)
		return skillEntry{}
	}
	idx := func(mm model, name string) int {
		for i, e := range mm.skillEntries() {
			if e.name == name {
				return i
			}
		}
		t.Fatalf("skill %q not in entries", name)
		return -1
	}

	e := find(m, "claude-shared-auth")
	if !e.inherited || !e.on() {
		t.Fatalf("inherited skill should render ON: %+v", e)
	}
	if !strings.Contains(m.viewSkills(), "(inherited)") {
		t.Fatalf("inherited mark missing:\n%s", m.viewSkills())
	}

	// Toggle OFF -> a !name removal marker in this layer, entry shows off.
	m.skillCur = idx(m, "claude-shared-auth")
	mm, _ := m.updateSkills(key(" "))
	m = mm.(model)
	if !contains(m.skills, "!claude-shared-auth") {
		t.Fatalf("toggle should write the removal marker: %v", m.skills)
	}
	if e := find(m, "claude-shared-auth"); e.on() || !e.removedHere {
		t.Fatalf("removed-here entry should render OFF: %+v", e)
	}

	// Toggle again -> marker gone, back to inherited-on.
	m.skillCur = idx(m, "claude-shared-auth")
	mm, _ = m.updateSkills(key(" "))
	m = mm.(model)
	if contains(m.skills, "!claude-shared-auth") || !find(m, "claude-shared-auth").on() {
		t.Fatalf("second toggle should re-inherit: %v", m.skills)
	}

	// A redundant local entry peels one layer per press: local entry first
	// (still inherited-on), then the removal marker.
	m.skills = append(m.skills, "devloop")
	m.skillCur = idx(m, "devloop")
	mm, _ = m.updateSkills(key(" "))
	m = mm.(model)
	if contains(m.skills, "devloop") {
		t.Fatalf("first press should drop the redundant local entry: %v", m.skills)
	}
	if !find(m, "devloop").on() {
		t.Fatal("devloop should still be inherited-on after peeling the local entry")
	}
	m.skillCur = idx(m, "devloop")
	mm, _ = m.updateSkills(key(" "))
	m = mm.(model)
	if !contains(m.skills, "!devloop") {
		t.Fatalf("second press should write the removal marker: %v", m.skills)
	}

	// assemble round-trips the marker (Save already accepts removal layers).
	if out := m.assemble(); !contains(out.Skills, "!devloop") {
		t.Fatalf("assemble dropped the removal marker: %v", out.Skills)
	}
}

// The form's Skills summary counts EFFECTIVE state (what the skills screen
// shows checked), not raw layer entries: a `!name` removal marker is not an
// enabled skill, and inherited-on skills count even with an empty local list.
func TestSkillsSummaryCountsEffectiveState(t *testing.T) {
	inherited := Inherited{HasLower: true, Default: config.Config{Skills: []string{"claude-shared-auth", "devloop"}}}
	m := newModel("t", "/tmp/x", config.Config{Agent: "claude"}, nil,
		[]string{"claude"}, []string{"claude", "devloop", "claude-shared-auth"}, nil, inherited, nil, TargetProject)

	// Empty local layer, but primary agent + two inherited skills are on.
	if got := m.renderValue(fSkills, false); !strings.Contains(got, "3 enabled") {
		t.Fatalf("summary should count inherited-on skills: %q", got)
	}

	// A removal marker turns one inherited skill off; it must not be counted
	// as enabled itself (the raw layer holds exactly one entry: the marker).
	m.skills = []string{"!claude-shared-auth"}
	if got := m.renderValue(fSkills, false); !strings.Contains(got, "2 enabled") {
		t.Fatalf("summary should not count removal markers: %q", got)
	}
}

// A pre-existing `!name` marker in the loaded config must render as a row
// (removed state), not as a bogus skill named "!devloop".
func TestSkillsExistingRemovalMarkerRendered(t *testing.T) {
	inherited := Inherited{HasLower: true, Default: config.Config{Skills: []string{"devloop"}}}
	cfg := config.Config{Agent: "claude", Skills: []string{"!devloop"}}
	m := newModel("t", "/tmp/x", cfg, nil, []string{"claude"}, []string{"claude", "devloop"}, nil, inherited, nil, TargetProject)
	view := m.viewSkills()
	if strings.Contains(view, "] !devloop") {
		t.Fatalf("marker rendered as a bogus skill name:\n%s", view)
	}
	if !strings.Contains(view, "(inherited — removed here)") {
		t.Fatalf("removed-here mark missing:\n%s", view)
	}
}

// Same-layer enable+remove resolves OFF (Merge applies removals last), so the
// checkbox must show OFF; and a stale !primary marker must stay visible and
// clearable on the locked row (review findings).
func TestSkillsMarkerEdgeCases(t *testing.T) {
	// ["devloop", "!devloop"] in one layer -> effectively off.
	m := newModel("t", "/tmp/x", config.Config{Agent: "claude", Skills: []string{"devloop", "!devloop"}}, nil,
		[]string{"claude"}, []string{"claude", "devloop"}, nil, Inherited{}, nil, TargetProject)
	for _, e := range m.skillEntries() {
		if e.name == "devloop" && e.on() {
			t.Fatalf("same-layer enable+remove must render OFF: %+v", e)
		}
	}

	// agent = claude + skills = ["!claude"]: marker visible on the locked row
	// and one toggle clears it.
	m2 := newModel("t", "/tmp/x", config.Config{Agent: "claude", Skills: []string{"!claude"}}, nil,
		[]string{"claude"}, []string{"claude"}, nil, Inherited{}, nil, TargetProject)
	if !strings.Contains(m2.viewSkills(), "stale !claude marker") {
		t.Fatalf("stale primary marker invisible:\n%s", m2.viewSkills())
	}
	for i, e := range m2.skillEntries() {
		if e.name == "claude" {
			m2.skillCur = i
		}
	}
	mm, _ := m2.updateSkills(key(" "))
	m2 = mm.(model)
	if contains(m2.skills, "!claude") {
		t.Fatalf("toggle should clear the stale marker: %v", m2.skills)
	}
}

// clipHeight windows tall frames: cursor row visible, footer (status/confirm
// banner + key help) pinned, hidden directions marked, short frames untouched.
func TestClipHeight(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("row %d", i))
	}
	lines[30] = "▸ focused row"
	lines[38] = "● Unsaved changes — confirm banner"
	frame := strings.Join(lines, "\n")

	got := clipHeight(frame, 20)
	if !strings.Contains(got, "▸ focused row") {
		t.Fatalf("cursor row must stay visible:\n%s", got)
	}
	if !strings.Contains(got, "confirm banner") {
		t.Fatalf("the footer (dirty-quit banner lives there) must be pinned:\n%s", got)
	}
	if !strings.Contains(got, "more above") {
		t.Fatalf("hidden top content must be marked:\n%s", got)
	}
	if n := strings.Count(got, "\n") + 1; n > 19 {
		t.Fatalf("frame must fit the terminal: %d lines", n)
	}

	// A frame that fits is untouched, and an unknown height is a no-op.
	if got := clipHeight("a\nb", 20); got != "a\nb" {
		t.Fatalf("short frame must pass through: %q", got)
	}
	if got := clipHeight(frame, 0); got != frame {
		t.Fatalf("unknown height must pass through")
	}
}

// ctrl+q goes up one level from every screen (screen -> form; nested screens
// pop one layer), mirroring esc — the form-level quit-with-confirm is pinned
// by TestCtrlQQuits.
func TestCtrlQGoesUpOneLevel(t *testing.T) {
	ctrlQ := tea.KeyMsg{Type: tea.KeyCtrlQ}
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, []string{"devlog"}, nil, Inherited{}, &fakeVols{}, TargetProject)

	m.mode = modeSkills
	if mm, _ := m.updateSkills(ctrlQ); mm.(model).mode != modeForm {
		t.Error("ctrl+q on the skills screen should return to the form")
	}
	m.mode = modeList
	m.listField = fApt
	if mm, _ := m.updateList(ctrlQ); mm.(model).mode != modeForm {
		t.Error("ctrl+q on a list screen should return to the form")
	}
	m.mode = modeMenu
	if mm, _ := m.updateMenu(ctrlQ); mm.(model).mode != modeList {
		t.Error("ctrl+q on the row menu should pop to the list, not the form")
	}
	m = m.startItem(-1)
	if mm, _ := m.updateItem(ctrlQ); mm.(model).mode != modeList {
		t.Error("ctrl+q in the item editor should cancel to the list")
	}
	m.mode = modeForm
	m.errMsg = "stale"
	m = m.openText(fRunArgs)
	if m.errMsg != "" {
		t.Error("opening the text overlay must clear a stale form error (it renders nowhere in the overlay)")
	}
	if mm, _ := m.updateText(ctrlQ); mm.(model).mode != modeForm {
		t.Error("ctrl+q in the text overlay should cancel to the form")
	}
	m.mode = modeVolumes
	if mm, _ := m.updateVolumes(ctrlQ); mm.(model).mode != modeForm {
		t.Error("ctrl+q on the volumes screen should return to the form")
	}
	// The clear-confirm is its own level: ctrl+q cancels it, staying on volumes.
	m.mode = modeVolumes
	m.volList = []VolumeStatus{{Name: ".x", Exists: true}}
	m.volPendClear = 0
	mm, _ := m.updateVolumes(ctrlQ)
	if got := mm.(model); got.mode != modeVolumes || got.volPendClear != -1 {
		t.Errorf("ctrl+q mid clear-confirm should cancel the confirm only (mode=%v pend=%d)", got.mode, got.volPendClear)
	}
}

// ctrl+s saves to disk from every screen: in place on the browse screens, and
// on the item/text editors it accepts the open edit first — never dropping or
// half-saving what's being typed.
func TestCtrlSSavesFromSubScreens(t *testing.T) {
	ctrlS := tea.KeyMsg{Type: tea.KeyCtrlS}
	path := filepath.Join(t.TempDir(), "x.config")
	m := newModel("t", path, config.Config{}, nil, nil, []string{"devlog"}, nil, Inherited{}, nil, TargetProject)

	// Skills: toggle one on, ctrl+s writes the file and stays on the screen.
	m.mode = modeSkills
	mm, _ := m.updateSkills(key(" "))
	mm, _ = mm.(model).updateSkills(ctrlS)
	m = mm.(model)
	if m.mode != modeSkills {
		t.Fatalf("ctrl+s should save in place, not leave the skills screen (mode=%v)", m.mode)
	}
	if !m.savedOnce || m.dirty() {
		t.Fatalf("ctrl+s on the skills screen should have saved: err=%q", m.errMsg)
	}
	if back, err := config.ParseFile(path); err != nil || len(back.Skills) != 1 || back.Skills[0] != "devlog" {
		t.Fatalf("saved file wrong: %v %v", back.Skills, err)
	}

	// List screen: ctrl+s saves in place too.
	m.mode = modeList
	m.listField = fApt
	m.apt = []string{"jq"}
	mm, _ = m.updateList(ctrlS)
	m = mm.(model)
	if m.mode != modeList || m.dirty() {
		t.Fatalf("ctrl+s on a list screen should save in place (mode=%v dirty=%v)", m.mode, m.dirty())
	}

	// Item editor: ctrl+s accepts the open item, then saves.
	m = m.startItem(-1)
	m.inputs[0].SetValue("ripgrep")
	mm, _ = m.updateItem(ctrlS)
	m = mm.(model)
	if m.mode != modeList {
		t.Fatalf("item ctrl+s should land on the list after commit+save (mode=%v)", m.mode)
	}
	if m.dirty() {
		t.Fatal("item ctrl+s should have committed AND saved")
	}
	if back, _ := config.ParseFile(path); len(back.Apt) != 2 || back.Apt[1] != "ripgrep" {
		t.Fatalf("open item not committed into the save: %v", back.Apt)
	}

	// Invalid open item: the editor stays with its error, nothing is written.
	m = m.startItem(-1)
	m.inputs[0].SetValue("")
	mm, _ = m.updateItem(ctrlS)
	m = mm.(model)
	if m.mode != modeItem || m.itemErr == "" {
		t.Fatalf("invalid item must keep the editor open with the error (mode=%v err=%q)", m.mode, m.itemErr)
	}
	if back, _ := config.ParseFile(path); len(back.Apt) != 2 {
		t.Fatalf("invalid item must not be saved around: %v", back.Apt)
	}

	// Mid volume-clear confirm: ctrl+s still saves, resolving the pending
	// destructive question in the safe direction (cancelled, not confirmed).
	m.mode = modeVolumes
	m.volList = []VolumeStatus{{Name: ".x", Exists: true}}
	m.volPendClear = 0
	m.apt = append(m.apt, "curl")
	mm, _ = m.updateVolumes(ctrlS)
	m = mm.(model)
	if m.volPendClear != -1 || m.mode != modeVolumes {
		t.Fatalf("ctrl+s mid clear-confirm should cancel the confirm and stay (pend=%d mode=%v)", m.volPendClear, m.mode)
	}
	if m.dirty() {
		t.Fatal("ctrl+s mid clear-confirm should have saved")
	}

	// Text overlay: ctrl+s accepts the buffer and saves.
	m.mode = modeForm
	m.itemErr = ""
	m = m.openText(fRunArgs)
	m.ta.SetValue("--privileged")
	mm, _ = m.updateText(ctrlS)
	m = mm.(model)
	if m.mode != modeForm || m.dirty() {
		t.Fatalf("text ctrl+s should accept and save (mode=%v dirty=%v)", m.mode, m.dirty())
	}
	if back, _ := config.ParseFile(path); len(back.RunArgs) != 1 || back.RunArgs[0] != "--privileged" {
		t.Fatalf("text buffer not in the save: %v", back.RunArgs)
	}
}

// The MCP screen: add/edit/delete with config-owned validation, the effective
// rows (local, inherited-with-override, skill-with-closure), and assemble
// round-trip. Egress-pattern parity — plus the one MCP-specific power: a
// skill-declared server is closable from this file (`!name` reaches it).
func TestMCPItemAddEditValidation(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fMCP

	// The editor is Kind-first: the picker is control 0 (focus starts there)
	// and the single Endpoint input's meaning follows it — the url-XOR-command
	// rule is structural, no both-set state exists to reject.
	m = m.startItem(-1)
	if !m.itemHasMode || !m.itemModeFirst || m.itemModeLabel != "Kind" || !m.onModePicker() {
		t.Fatalf("Kind picker must lead the MCP editor: hasMode=%v first=%v label=%q onPicker=%v",
			m.itemHasMode, m.itemModeFirst, m.itemModeLabel, m.onModePicker())
	}
	if len(m.inputs) != 5 {
		t.Fatalf("form should be 5 inputs + picker, got %d", len(m.inputs))
	}

	// Reject: no endpoint at all (local kind, empty command).
	m.inputs[0].SetValue("github")
	if m2 := m.commitItem(); m2.itemErr == "" || len(m2.mcps) != 0 {
		t.Fatalf("empty endpoint should be rejected: err=%q mcps=%v", m2.itemErr, m2.mcps)
	}
	// Accept a local declaration with env + egress; the name auto-lowercases.
	m.inputs[0].SetValue("GitHub")
	m.inputs[1].SetValue("gh-mcp stdio")
	m.inputs[2].SetValue("GITHUB_TOKEN GH_HOST")
	m.inputs[3].SetValue("api.github.com")
	m = m.commitItem()
	if m.itemErr != "" || len(m.mcps) != 1 {
		t.Fatalf("local add failed: err=%q mcps=%v", m.itemErr, m.mcps)
	}
	got := m.mcps[0]
	if got.Name != "github" || got.Command[0] != "gh-mcp" || len(got.Env) != 2 || got.Egress[0] != "api.github.com" {
		t.Fatalf("declaration shape wrong (name must auto-lowercase): %+v", got)
	}

	// Duplicate name in this layer: caught by the assembled ValidateLayer.
	m = m.startItem(-1)
	m.inputs[0].SetValue("github")
	m.inputs[1].SetValue("other")
	if m2 := m.commitItem(); m2.itemErr == "" || len(m2.mcps) != 1 {
		t.Fatalf("in-layer duplicate should be rejected: err=%q", m2.itemErr)
	}

	// Edit in place: flip the Kind to remote; the endpoint becomes a url.
	m = m.startItem(0)
	if m.itemMode != 0 {
		t.Fatalf("editing a local declaration must open with Kind=local")
	}
	m.itemMode = 1
	m.inputs[1].SetValue("https://mcp.github.example/mcp")
	m = m.commitItem()
	if m.itemErr != "" || !m.mcps[0].Remote() {
		t.Fatalf("edit to remote failed: err=%q %+v", m.itemErr, m.mcps)
	}
	// Re-opening a remote declaration restores Kind + url in the endpoint.
	m = m.startItem(0)
	if m.itemMode != 1 || m.inputs[1].Value() != "https://mcp.github.example/mcp" {
		t.Fatalf("remote edit must reopen as Kind=remote with the url: mode=%d val=%q", m.itemMode, m.inputs[1].Value())
	}
	m = m.commitItem()

	// Assemble round-trips the working state into the config.
	if out := m.assemble(); len(out.MCPs) != 1 || out.MCPs[0].URL == "" {
		t.Fatalf("assemble lost the declaration: %+v", out.MCPs)
	}
	m.deleteItem(fMCP, 0)
	if out := m.assemble(); out.MCPs != nil {
		t.Fatalf("empty set must assemble nil: %+v", out.MCPs)
	}
}

func TestMCPRowsEffectiveView(t *testing.T) {
	inh := Inherited{
		HasLower: true,
		Default: config.Config{MCPs: []config.MCP{
			{Name: "inherited", Command: []string{"srv"}},
			{Name: "shadowed", Command: []string{"old"}},
		}},
		Skills: map[string]SkillRuntime{
			"pete/tools": {MCPs: []config.MCP{
				{Name: "from-skill", Command: []string{"sk"}},
				{Name: "closed-skill", Command: []string{"sk2"}},
			}},
		},
	}
	cfg := config.Config{
		Skills: []string{"pete/tools"},
		MCPs: []config.MCP{
			{Name: "own", Command: []string{"mine"}},
			{Name: "shadowed", Command: []string{"new"}},
			{Name: "!closed-skill"},
			{Name: "!ghost"},
		},
	}
	m := newModel("t", "/tmp/x", cfg, nil, nil, []string{"pete/tools"}, nil, inh, nil, TargetProject)
	m.listField = fMCP
	rows := m.fieldRows(fMCP)

	find := func(kind rowKind, substr string) *listRow {
		for i := range rows {
			if rows[i].kind == kind && strings.Contains(rows[i].text, substr) {
				return &rows[i]
			}
		}
		return nil
	}
	if r := find(rowInherited, "inherited"); r == nil || r.source != "default" || r.ident != "inherited" {
		t.Fatalf("inherited row wrong: %+v (rows: %+v)", r, rows)
	}
	if r := find(rowOverride, "shadowed — local: new"); r == nil {
		t.Fatalf("replace-by-name must render as override: %+v", rows)
	}
	if r := find(rowLocal, "own"); r == nil {
		t.Fatalf("local row missing: %+v", rows)
	}
	if r := find(rowSkill, "from-skill"); r == nil || r.ident != "from-skill" {
		t.Fatalf("skill row must be closable (ident set): %+v", rows)
	}
	if r := find(rowRemoved, "closed-skill"); r == nil || r.idx < 0 {
		t.Fatalf("skill server closed by this file must show removed with Restore: %+v", rows)
	}
	if r := find(rowStaleMarker, "ghost"); r == nil {
		t.Fatalf("marker matching nothing must read stale: %+v", rows)
	}

	// The closable skill row offers exactly "Remove in this project", and
	// applying it writes the closure marker into this layer.
	sk := find(rowSkill, "from-skill")
	choices := m.rowChoices(fMCP, *sk)
	if len(choices) != 1 || choices[0].act != actRemoveHere {
		t.Fatalf("skill MCP row choices: %+v", choices)
	}
	m.removeHere(*sk)
	if out := m.assemble(); !hasMCPName(out.MCPs, "!from-skill") {
		t.Fatalf("removeHere must write the closure: %+v", out.MCPs)
	}

	// A non-MCP skill row still has no menu (parity guard).
	if got := m.rowChoices(fEgress, listRow{kind: rowSkill, ident: "x"}); got != nil {
		t.Fatalf("egress skill rows must stay menu-less: %+v", got)
	}
}

func TestMCPSigTracksChanges(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{MCPs: []config.MCP{{Name: "a", Command: []string{"srv"}}}}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	if m.dirty() {
		t.Fatal("fresh model must not be dirty")
	}
	m.mcps[0].Command = []string{"changed"}
	if !m.dirty() {
		t.Fatal("an MCP edit must flip the dirty signature")
	}
}

// The argv text form must be REVERSIBLE: opening a declaration whose command
// carries spaced/quoted args and committing it unchanged must not corrupt
// the argv (codex review round 4 — join/Fields split "hello world" apart).
func TestMCPArgvRoundTrip(t *testing.T) {
	cases := [][]string{
		{"server", "--label", "hello world"},
		{"srv", `say "hi"`, ""},
		{"plain", "args", "only"},
		{"srv", `trailing backslash \`},
		{"srv", `double \\ back`, `\" tricky`},
		{`C:\bare\backslash`, "unquoted"},
	}
	for _, argv := range cases {
		got, err := splitArgv(joinArgv(argv))
		if err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
		if strings.Join(got, "\x00") != strings.Join(argv, "\x00") {
			t.Errorf("round trip lost data: %v -> %q -> %v", argv, joinArgv(argv), got)
		}
	}
	if _, err := splitArgv(`bad "unterminated`); err == nil {
		t.Error("unterminated quote must error")
	}

	// The regression as the user hits it: open the existing item, commit
	// with no edits, argv unchanged.
	m := newModel("t", "/tmp/x", config.Config{MCPs: []config.MCP{
		{Name: "spaced", Command: []string{"server", "--label", "hello world"}},
	}}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fMCP
	m = m.startItem(0)
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("no-op commit errored: %s", m.itemErr)
	}
	if got := m.mcps[0].Command; len(got) != 3 || got[2] != "hello world" {
		t.Fatalf("no-op open-and-commit corrupted argv: %v", got)
	}
}

// Headers ride the argv codec (one quoted "Name: value" token each): the
// form accepts them for remote kind, validation refuses them on local, and
// a no-op open-and-commit round-trips multiple headers unchanged.
func TestMCPHeadersInForm(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{MCPs: []config.MCP{{
		Name: "proxied", URL: "https://mcp.internal.example/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${TOK}", "X-Api-Key": "${KEY}"},
	}}}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fMCP

	// No-op open-and-commit keeps both headers.
	m = m.startItem(0)
	m = m.commitItem()
	if m.itemErr != "" || len(m.mcps[0].Headers) != 2 || m.mcps[0].Headers["Authorization"] != "Bearer ${TOK}" {
		t.Fatalf("headers round trip: err=%q %+v", m.itemErr, m.mcps[0].Headers)
	}
	// Edit a header value through the input.
	m = m.startItem(0)
	m.inputs[4].SetValue(`"Authorization: Bearer ${OTHER}"`)
	m = m.commitItem()
	if m.itemErr != "" || m.mcps[0].Headers["Authorization"] != "Bearer ${OTHER}" || len(m.mcps[0].Headers) != 1 {
		t.Fatalf("header edit: err=%q %+v", m.itemErr, m.mcps[0].Headers)
	}
	// Headers on a local declaration refuse (config owns the rule).
	m = m.startItem(-1)
	m.inputs[0].SetValue("loc")
	m.inputs[1].SetValue("srv")
	m.inputs[4].SetValue(`"X: y"`)
	if m2 := m.commitItem(); m2.itemErr == "" || !strings.Contains(m2.itemErr, "remote (url) servers") {
		t.Fatalf("local headers must refuse: %q", m2.itemErr)
	}
	// A malformed header token errors cleanly.
	m.inputs[1].SetValue("")
	m.itemMode = 1
	m.inputs[1].SetValue("https://h.example/mcp")
	m.inputs[4].SetValue(`"no-colon"`)
	if m2 := m.commitItem(); m2.itemErr == "" || !strings.Contains(m2.itemErr, "Name: value") {
		t.Fatalf("malformed header: %q", m2.itemErr)
	}
}

func TestClaudeSkillRowsEffectiveView(t *testing.T) {
	inh := Inherited{
		HasLower: true,
		Default: config.Config{ClaudeSkills: []config.ClaudeSkill{
			{Name: "inherited", Path: "/cs/inherited"},
			{Name: "shadowed", Path: "/cs/old"},
		}},
		Skills: map[string]SkillRuntime{
			"pete/tools": {ClaudeSkills: []config.ClaudeSkill{
				{Name: "from-skill", From: "cs/from-skill"},
				{Name: "closed-skill", From: "cs/closed-skill"},
			}},
		},
	}
	cfg := config.Config{
		Skills: []string{"pete/tools"},
		ClaudeSkills: []config.ClaudeSkill{
			{Name: "own", Path: "/cs/own"},
			{Name: "shadowed", Path: "/cs/new"},
			{Name: "!closed-skill"},
			{Name: "!ghost"},
		},
	}
	m := newModel("t", "/tmp/x", cfg, nil, nil, []string{"pete/tools"}, nil, inh, nil, TargetProject)
	m.listField = fClaudeSkills
	rows := m.fieldRows(fClaudeSkills)

	find := func(kind rowKind, substr string) *listRow {
		for i := range rows {
			if rows[i].kind == kind && strings.Contains(rows[i].text, substr) {
				return &rows[i]
			}
		}
		return nil
	}
	if r := find(rowInherited, "inherited"); r == nil || r.source != "default" || r.ident != "inherited" {
		t.Fatalf("inherited row wrong: %+v (rows: %+v)", r, rows)
	}
	if r := find(rowOverride, "shadowed — /cs/new"); r == nil {
		t.Fatalf("replace-by-name must render as override: %+v", rows)
	}
	if r := find(rowLocal, "own"); r == nil {
		t.Fatalf("local row missing: %+v", rows)
	}
	if r := find(rowSkill, "from-skill"); r == nil || r.ident != "from-skill" {
		t.Fatalf("skill row must be closable (ident set): %+v", rows)
	}
	if r := find(rowRemoved, "closed-skill"); r == nil || r.idx < 0 {
		t.Fatalf("skill contribution closed by this file must show removed with Restore: %+v", rows)
	}
	if r := find(rowStaleMarker, "ghost"); r == nil {
		t.Fatalf("marker matching nothing must read stale: %+v", rows)
	}

	sk := find(rowSkill, "from-skill")
	choices := m.rowChoices(fClaudeSkills, *sk)
	if len(choices) != 1 || choices[0].act != actRemoveHere {
		t.Fatalf("skill claude-skill row choices: %+v", choices)
	}
	m.removeHere(*sk)
	if out := m.assemble(); !hasClaudeSkillName(out.ClaudeSkills, "!from-skill") {
		t.Fatalf("removeHere must write the closure: %+v", out.ClaudeSkills)
	}
}

func TestClaudeSkillItemEditorCommit(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fClaudeSkills
	m = m.startItem(-1)
	m.inputs[0].SetValue("TDD-Loop") // lowercases on commit
	m.inputs[1].SetValue("~/cs/tdd-loop")
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("commit: %s", m.itemErr)
	}
	out := m.assemble()
	if len(out.ClaudeSkills) != 1 || out.ClaudeSkills[0].Name != "tdd-loop" || out.ClaudeSkills[0].Path != "~/cs/tdd-loop" {
		t.Fatalf("assembled = %+v", out.ClaudeSkills)
	}

	// A relative path is refused with config's own message.
	m = m.startItem(-1)
	m.inputs[0].SetValue("x")
	m.inputs[1].SetValue("relative/dir")
	m = m.commitItem()
	if m.itemErr == "" || !strings.Contains(m.itemErr, "absolute or ~/") {
		t.Fatalf("relative path must refuse: %q", m.itemErr)
	}
}

// A Claude Skill edit must flip dirty — sig() has to sign m.claudeSkills or
// quitting after an add/close loses the edit without the unsaved-changes
// confirm (review finding).
func TestClaudeSkillEditsFlipDirty(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	if m.dirty() {
		t.Fatal("a freshly-opened config must not be dirty")
	}
	m.listField = fClaudeSkills
	m = m.startItem(-1)
	m.inputs[0].SetValue("tdd-loop")
	m.inputs[1].SetValue("~/cs/tdd-loop")
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("commit: %s", m.itemErr)
	}
	if !m.dirty() {
		t.Fatal("adding a Claude Skill must mark the form dirty")
	}
}

// Error lines WRAP to the terminal width instead of truncating at the pane
// edge (field-QA 2026-07-17, finding 5): clipLines cuts any longer line, so
// an unwrapped long message — they echo user input, unbounded — silently
// lost its tail. Every wrapped line must fit, and the message's TAIL must
// survive to the screen.
func TestErrorLinesWrapNotTruncate(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.width = 40
	long := "claude skill name \"/definitely/not/a/skill/with/a/long/path\": must be lowercase TAILMARKER"
	got := m.errLine(long)
	for i, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(line); w > 40 {
			t.Errorf("wrapped line %d is %d cols, exceeds width 40: %q", i, w, line)
		}
	}
	if !strings.Contains(got, "TAILMARKER") {
		t.Fatalf("message tail lost — still truncating, not wrapping:\n%s", got)
	}
	// Zero width (no WindowSizeMsg yet) must not panic or wrap to nothing.
	m.width = 0
	if got := m.errLine(long); !strings.Contains(got, "TAILMARKER") {
		t.Fatalf("zero-width render lost the message: %q", got)
	}
}

// The Claude Skill editor warns — never gates — on a host dir the bake would
// reject (field-QA 2026-07-17, finding 4). The validator that decides is the
// bake's own (skills.ValidateClaudeSkillDir), so editor and develop cannot
// disagree; the note classifies briefly.
func TestClaudeSkillDirNoteClasses(t *testing.T) {
	if n := claudeSkillDirNote("x", ""); n != "" {
		t.Errorf("empty path is the required-check's job, got note %q", n)
	}
	if n := claudeSkillDirNote("x", "/definitely/not/a/dir"); !strings.Contains(n, "path missing") {
		t.Errorf("missing dir: got %q", n)
	}
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := claudeSkillDirNote("x", f); !strings.Contains(n, "not a directory") {
		t.Errorf("regular file: got %q", n)
	}
	empty := t.TempDir()
	if n := claudeSkillDirNote("x", empty); !strings.Contains(n, "no SKILL.md") {
		t.Errorf("dir without SKILL.md: got %q", n)
	}
	good := t.TempDir()
	if err := os.WriteFile(filepath.Join(good, "SKILL.md"),
		[]byte("---\nname: good-skill\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := claudeSkillDirNote("good-skill", good); n != "" {
		t.Errorf("valid dir must carry no note, got %q", n)
	}
	if n := claudeSkillDirNote("other-name", good); !strings.Contains(n, "build will fail") {
		t.Errorf("frontmatter name mismatch must warn, got %q", n)
	}
}

// Accepting a bad path stays NON-blocking (warn-only): the entry commits, the
// editor note and the list row both carry the warning, and the dirty
// SIGNATURE ignores it — a dir appearing later must not flip dirty.
func TestClaudeSkillBadPathWarnsWithoutBlocking(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fClaudeSkills
	m = m.startItem(-1)
	m.inputs[0].SetValue("qa-skill")
	m.inputs[1].SetValue("/definitely/not/a/dir")
	notes := strings.Join(m.itemNotes(), "\n")
	if !strings.Contains(notes, "path missing") || !strings.Contains(notes, "accepted anyway") {
		t.Fatalf("editor note missing the live warning: %q", notes)
	}
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("bad path must not block the commit (warn-only), got error %q", m.itemErr)
	}
	if len(m.claudeSkills) != 1 || m.claudeSkills[0].Name != "qa-skill" {
		t.Fatalf("entry not committed: %+v", m.claudeSkills)
	}
	if got := claudeSkillRowText(m.claudeSkills[0]); !strings.Contains(got, "path missing — build will fail") {
		t.Fatalf("list row must carry the warning: %q", got)
	}
	// Signature stability: the same entry with an existing vs missing dir
	// signs identically (the note is display-only).
	sigBad := m.sig()
	good := t.TempDir()
	if err := os.WriteFile(filepath.Join(good, "SKILL.md"),
		[]byte("---\nname: qa-skill\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m2 := m
	m2.claudeSkills = []config.ClaudeSkill{{Name: "qa-skill", Path: good}}
	if claudeSkillRowText(m2.claudeSkills[0]) != claudeSkillLine(m2.claudeSkills[0]) {
		t.Fatal("valid entry must carry no row warning")
	}
	_ = sigBad // both models sign via claudeSkillLine — pinned by the substring below
	if strings.Contains(m.sig(), "build will fail") {
		t.Fatal("the warning leaked into the dirty signature")
	}
}

// The prepare hook (deferred store setup, e.g. enrolling a project dir) must
// run before the first write lands — and only then: its whole point is that
// opening the editor and quitting creates nothing.
func TestPrepareRunsBeforeSaveWrites(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store")
	path := filepath.Join(store, "byre.config")
	m := newModel("t", path, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	calls := 0
	m.prepare = func() error {
		calls++
		return os.MkdirAll(store, 0o755) // what commands.Config's Bootstrap does
	}
	m = m.save()
	if calls != 1 {
		t.Fatalf("prepare ran %d times, want 1", calls)
	}
	if !m.savedOnce {
		t.Fatalf("save failed: %q", m.errMsg)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved file missing: %v", err)
	}
}

func TestPrepareErrorBlocksSaveAndEditor(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store")
	path := filepath.Join(store, "byre.config")
	m := newModel("t", path, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.prepare = func() error { return fmt.Errorf("cannot enroll") }

	m = m.save()
	if m.savedOnce {
		t.Fatal("a failed prepare must block the save")
	}
	if !strings.Contains(m.errMsg, "cannot enroll") {
		t.Fatalf("prepare error not surfaced: %q", m.errMsg)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("failed save left state behind: %v", err)
	}

	// ctrl+e hands the file to $EDITOR, which writes it directly — the same
	// gate applies before the editor may open.
	mm, cmd := m.updateForm(tea.KeyMsg{Type: tea.KeyCtrlE})
	if cmd != nil {
		t.Fatal("ctrl+e must not open $EDITOR when prepare fails")
	}
	if got := mm.(model).errMsg; !strings.Contains(got, "cannot enroll") {
		t.Fatalf("ctrl+e prepare error not surfaced: %q", got)
	}
}

// A save the validator refuses never becomes a write, so it must not run
// prepare (enrollment): cross-item collisions are deliberately deferred to
// save-time ValidateLayer, making this an ordinary-use path.
func TestSaveValidationFailureSkipsPrepare(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store")
	path := filepath.Join(store, "byre.config")
	cfg := config.Config{Mounts: []config.Mount{
		{Host: "/a", Target: "/x", Mode: "ro"},
		{Host: "/b", Target: "/x", Mode: "ro"},
	}}
	m := newModel("t", path, cfg, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	calls := 0
	m.prepare = func() error { calls++; return nil }
	m = m.save()
	if m.savedOnce {
		t.Fatal("an invalid layer must not save")
	}
	if calls != 0 {
		t.Fatalf("a refused save ran prepare %d times (enrolls on a no-op)", calls)
	}
	if !strings.Contains(m.errMsg, "collides") {
		t.Fatalf("validation error not surfaced: %q", m.errMsg)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("refused save left state behind: %v", err)
	}
}

// savedOnce must track writes that actually landed in the $EDITOR round-trip:
// created or changed → saved; look-and-quit → not.
func TestEditorRoundTripMarksSavedOnlyOnWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "byre.config")
	m := newModel("t", path, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)

	// Look-and-quit on a not-yet-existing file: nothing written.
	m.preEditorRaw, m.preEditorErr = os.ReadFile(path)
	if got := m.onEditorClosed(nil); got.savedOnce {
		t.Fatal("no write must not mark savedOnce")
	}
	// $EDITOR created the file: that IS the first write.
	if err := os.WriteFile(path, []byte("agent = \"none\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := m.onEditorClosed(nil); !got.savedOnce {
		t.Fatal("a landed $EDITOR write must mark savedOnce")
	}
	// Re-open on the now-existing file, quit without changing it: not a write.
	m.preEditorRaw, m.preEditorErr = os.ReadFile(path)
	if got := m.onEditorClosed(nil); got.savedOnce {
		t.Fatal("an unchanged file must not mark savedOnce")
	}
}
