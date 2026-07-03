package configui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"byre/internal/config"
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
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil)

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
	m.inputs[1].SetValue("/data")
	m.itemMode = 1 // rw
	m = m.commitItem()
	if len(m.mounts) != 1 || m.mounts[0].Mode != "rw" || m.mounts[0].Target != "/data" {
		t.Fatalf("mount not added correctly: %v", m.mounts)
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
func (f *fakeVols) Clear(name string) error {
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
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, fv)

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
	m := newModel("t", "/x", config.Config{WorktreeBase: "sibling"}, nil, nil, nil, nil)
	if !m.wtSibling {
		t.Error("sibling config should check the box")
	}
	if got := m.assemble().WorktreeBase; got != "sibling" {
		t.Errorf("assemble = %q, want sibling", got)
	}
	// A path -> checkbox off, path loaded, round-trips.
	m = newModel("t", "/x", config.Config{WorktreeBase: "/w"}, nil, nil, nil, nil)
	if m.wtSibling || m.wtBase.Value() != "/w" {
		t.Errorf("path config: sibling=%v base=%q", m.wtSibling, m.wtBase.Value())
	}
	if got := m.assemble().WorktreeBase; got != "/w" {
		t.Errorf("assemble = %q, want /w", got)
	}
	// Unset -> checkbox off, empty -> writes "" (byre worktree refuses).
	m = newModel("t", "/x", config.Config{}, nil, nil, nil, nil)
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

	// The form actually renders the WORKTREES section + a checkbox state.
	on := newModel("t", "/x", config.Config{WorktreeBase: "sibling"}, nil, nil, nil, nil).View()
	if !strings.Contains(on, "WORKTREES") || !strings.Contains(on, "[x] sibling of repo") {
		t.Errorf("form should show a checked worktree checkbox:\n%s", on)
	}
	off := newModel("t", "/x", config.Config{}, nil, nil, nil, nil).View()
	if !strings.Contains(off, "[ ] sibling of repo") || !strings.Contains(off, "refuse") {
		t.Errorf("unset form should show an unchecked box and a refuse hint:\n%s", off)
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
	m := newModel("t", "/tmp/x", cfg, nil, agents, all, nil)

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

// A skill listed in `skills` that becomes the primary agent must not be written
// back into `skills` (the agent field implies it).
func TestSkillsPrimaryNotDoubleWritten(t *testing.T) {
	cfg := config.Config{Agent: "claude", Skills: []string{"codex"}} // codex enabled as a skill
	m := newModel("t", "/tmp/x", cfg, nil, []string{"claude", "codex"}, []string{"claude", "codex"}, nil)
	// Promote codex to the primary agent.
	m.agentSel = indexOf(m.agentOpts, "codex")
	if out := m.assemble(); contains(out.Skills, "codex") {
		t.Fatalf("primary agent must be stripped from skills, got %v", out.Skills)
	}
}

// The ports editor validates the container port and treats a blank host as
// its container port; grants lead the form and focus starts there.
func TestPortsEditorAndSectionOrder(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil)

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
// buffer into the config, esc discards; both flip/leave dirty correctly.
func TestRawTextFieldEditRoundTrip(t *testing.T) {
	// An indented, blank-line-containing dockerfile_pre must survive untouched.
	cfg := config.Config{
		RunArgs:       []string{"--privileged"},
		DockerfilePre: []string{"RUN foo \\", "    && bar", "", "RUN baz"},
	}
	m := newModel("t", "/tmp/x", cfg, nil, nil, nil, nil)
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

	// Edit run_args and accept.
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
	if !m.dirty() {
		t.Fatal("editing run_args should mark the model dirty")
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

// newModel must not report the freshly-opened config as dirty, an unknown engine
// must round-trip (not coerce to podman), and touching a field flips dirty.
func TestModelDirtyAndUnknownEngineRoundTrip(t *testing.T) {
	cfg := config.Config{Base: "debian:bookworm", Engine: "containerd", Agent: "claude"}
	m := newModel("t", "/tmp/x", cfg, []string{"claude", "codex"}, []string{"claude", "codex"}, nil, nil)
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
