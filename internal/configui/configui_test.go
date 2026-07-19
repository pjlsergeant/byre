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
	// A configured literal "none" IS the sentinel, never a preserved value —
	// the QA pass-2 double-[none] rendering bug.
	if got := pickerOpts([]string{"claude"}, "none"); !reflect.DeepEqual(got, []string{"claude", "none"}) {
		t.Fatalf("configured none duplicated the sentinel: %v", got)
	}
}

// fakeVols is a test VolumeAdmin: List returns its volumes; Clear removes one
// unless clearErr is set (simulating a live-session refusal).
type fakeVols struct {
	vols       []VolumeStatus
	notes      []string
	clearErr   error
	cleared    []string
	sharedNote string
}

func (f *fakeVols) List() ([]VolumeStatus, []string, error) { return f.vols, f.notes, nil }

func (f *fakeVols) SharedNote() string { return f.sharedNote }

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

// A resize triggers a full repaint: the inline renderer can't clear
// previously-drawn lines that wrapped when the terminal SHRANK, so stale
// fragments linger above the frame without it.
func TestResizeClearsScreen(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if cmd == nil {
		t.Fatal("WindowSizeMsg must return a clear-screen cmd, got nil")
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
		out[i] = fieldLabel(f)
	}
	return out
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
