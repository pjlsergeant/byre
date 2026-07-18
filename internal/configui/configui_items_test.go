package configui

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// itemModel returns a fresh model focused on the given structured-list field,
// the shared starting point for the item-editor tests below.
func itemModel(field fieldID) model {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = field
	return m
}

// withEnv commits key=value through the env item editor, failing the test if
// the editor rejects it — fixture setup for tests that need an existing row.
func withEnv(t *testing.T, m model, key, value string) model {
	t.Helper()
	m.listField = fEnv
	m = m.startItem(-1)
	m.inputs[0].SetValue(key)
	m.inputs[1].SetValue(value)
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("env fixture %s=%s rejected: %q", key, value, m.itemErr)
	}
	return m
}

// withMount commits an rw mount through the item editor, failing the test if
// the editor rejects it.
func withMount(t *testing.T, m model, host, target string) model {
	t.Helper()
	m.listField = fMounts
	m = m.startItem(-1)
	m.inputs[0].SetValue(host)
	m.inputs[1].SetValue(target)
	m.itemMode = 1 // rw
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("mount fixture %s -> %s rejected: %q", host, target, m.itemErr)
	}
	return m
}

// The env item editor rejects a malformed key and adds a valid pair.
func TestEnvItemValidation(t *testing.T) {
	m := itemModel(fEnv)
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
}

// Editing an existing env item replaces it in place, not appends.
func TestEnvItemEditReplacesInPlace(t *testing.T) {
	m := withEnv(t, itemModel(fEnv), "TOKEN", "v")
	m = m.startItem(0)
	m.inputs[1].SetValue("v2")
	m = m.commitItem()
	if len(m.env) != 1 || m.env[0].Value != "v2" {
		t.Fatalf("env edit should replace in place: %v", m.env)
	}
}

// A duplicate env key is rejected (it would silently collapse on save) --
// but re-committing a row under its own key must not trip the check.
func TestEnvItemDuplicateRejected(t *testing.T) {
	m := withEnv(t, itemModel(fEnv), "TOKEN", "v")
	m = m.startItem(-1)
	m.inputs[0].SetValue("TOKEN") // already exists
	m.inputs[1].SetValue("other")
	if m2 := m.commitItem(); m2.itemErr == "" || len(m2.env) != 1 {
		t.Fatalf("duplicate env key should be rejected: err=%q env=%v", m2.itemErr, m2.env)
	}
	m = m.startItem(0)
	m.inputs[1].SetValue("v3")
	if m2 := m.commitItem(); m2.itemErr != "" {
		t.Fatalf("editing a row without changing its key must not trip the dup check: %q", m2.itemErr)
	}
}

// The mount item editor requires an absolute target and refuses a `!` prefix
// slipping through as a removal marker.
func TestMountItemTargetValidation(t *testing.T) {
	m := itemModel(fMounts)
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
}

// Disabling a mount (the mode picker's third state) sets the bool, preserves
// the stored rw mode, and marks the list row.
func TestMountItemDisablePreservesMode(t *testing.T) {
	m := withMount(t, itemModel(fMounts), "~/data", "/data")
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
}

// Re-enabling: the editor opens on the disabled state, and picking rw clears
// the bool while keeping the mode.
func TestMountItemReenable(t *testing.T) {
	m := withMount(t, itemModel(fMounts), "~/data", "/data")
	m = m.startItem(0)
	m.itemMode = 2 // disabled
	m = m.commitItem()
	m = m.startItem(0)
	if m.itemMode != 2 {
		t.Fatalf("editor should open on disabled, got %d", m.itemMode)
	}
	m.itemMode = 1
	m = m.commitItem()
	if m.mounts[0].Disabled || m.mounts[0].Mode != "rw" {
		t.Fatalf("re-enable should clear the bool and keep rw: %+v", m.mounts[0])
	}
}

func TestMountItemDelete(t *testing.T) {
	m := withMount(t, itemModel(fMounts), "~/data", "/data")
	m.deleteItem(fMounts, 0)
	if len(m.mounts) != 0 {
		t.Fatalf("mount not deleted: %v", m.mounts)
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
