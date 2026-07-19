package configui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pjlsergeant/byre/internal/config"
)

func TestSaveRoundTripsAndPreservesRawFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store", "byre.config")
	// Callers own the parent dir (in the product, Bootstrap creates it with
	// the path record; AtomicWrite deliberately never does).
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
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
	// Callers own the parent dir (in the product, Bootstrap creates it with
	// the path record; AtomicWrite deliberately never does).
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
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

// TestCommentWarnOnLoad pins Q7: opening a hand-commented file warns that
// saving rewrites it; byre's own boilerplate headers don't cry wolf.
func TestCommentWarnOnLoad(t *testing.T) {
	dir := t.TempDir()
	hand := filepath.Join(dir, "hand.config")
	mustWriteFile(t, hand, []byte("# remember: the LAN port is for the demo\nagent = \"claude\"\n"), 0o644)
	if v := newModel("t", hand, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject).View(); !strings.Contains(v, "hand-written comments") {
		t.Errorf("hand-commented file should warn on load:\n%s", v)
	}

	managed := filepath.Join(dir, "managed.config")
	mustWriteFile(t, managed, []byte("# Managed by `byre config`. Structured fields are edited there;\n# raw blocks (run_args, dockerfile_pre/post) are edited here by hand.\n\nagent = \"claude\"\n"), 0o644)
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
	mustWriteFile(t, path, []byte("agent = \"claude\"\n"), 0o644)
	m := newModel("t", path, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	if m.commentWarn {
		t.Fatal("clean file must not warn at open")
	}
	// User adds a hand comment in $EDITOR, then the TUI reloads.
	mustWriteFile(t, path, []byte("# my note\nagent = \"claude\"\n"), 0o644)
	m = m.onEditorClosed(nil)
	if !m.commentWarn {
		t.Error("comments added via $EDITOR must arm the warning")
	}
	// And removing them disarms it.
	mustWriteFile(t, path, []byte("agent = \"claude\"\n"), 0o644)
	m = m.onEditorClosed(nil)
	if m.commentWarn {
		t.Error("warning must clear once the comments are gone")
	}

	// A successful ^s re-marshals the file — the comments it warned about are
	// gone, so the warning must clear rather than nag about the file just written.
	mustWriteFile(t, path, []byte("# note\nagent = \"claude\"\n"), 0o644)
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
	// Deleted inside the editor: a mutation — "config unchanged" would claim
	// the config is intact when it is gone.
	m.preEditorRaw, m.preEditorErr = os.ReadFile(path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got := m.onEditorClosed(nil)
	if !got.savedOnce {
		t.Fatal("a deletion in the editor must mark savedOnce")
	}
	if !strings.Contains(got.status, "deleted") {
		t.Fatalf("deletion must be named in the status, got %q", got.status)
	}
}
