package configui

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

func hostEnvModel(t *testing.T, local map[string]string) model {
	t.Helper()
	return newModel("t", "/tmp/x", config.Config{EnvFromHost: local}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
}

// openHostEnvRow finds the passthrough row for key and opens its editor the
// way the UI does: the ROW decides which item editor opens, since [env]
// literals and passthroughs share one screen.
func openHostEnvRow(t *testing.T, m model, key string) model {
	t.Helper()
	m.listField = fEnv
	for _, r := range m.fieldRows(fEnv) {
		if r.kind == rowHostEnv && r.ident == key {
			m.itemHostEnv = true
			if r.idx >= 0 {
				return m.startItem(r.idx)
			}
			return m.startOverride(r)
		}
	}
	t.Fatalf("no passthrough row for %q", key)
	return m
}

// byre ships six env_from_host keys as a real cascade layer, and every one of
// them used to be a dead end in the editor: the row said "hand-edit the TOML".
func TestHostEnvRowsAreActionable(t *testing.T) {
	m := hostEnvModel(t, nil)
	var seen int
	for _, r := range m.fieldRows(fEnv) {
		if r.kind != rowHostEnv {
			continue
		}
		seen++
		if len(m.rowChoices(fEnv, r)) == 0 {
			t.Errorf("passthrough row %q offers no actions -- still a dead end", r.text)
		}
	}
	if seen == 0 {
		t.Fatal("no passthrough rows at all; the shipped defaults should be visible")
	}
}

// Un-pinning is Delete on the row -- the same thing Delete means on every
// other list field -- not a sixth picker option. Without SOME path back, a
// user who pinned a key could only undo it by hand-editing the TOML.
func TestHostEnvDeleteRemovesTheLocalPin(t *testing.T) {
	m := hostEnvModel(t, map[string]string{"GIT_AUTHOR_NAME": "git:committer.name"})
	m.listField = fEnv
	var pinned listRow
	for _, r := range m.fieldRows(fEnv) {
		if r.kind == rowHostEnv && r.ident == "GIT_AUTHOR_NAME" {
			pinned = r
		}
	}
	if pinned.idx < 0 {
		t.Fatal("a key set in this file should carry a local index")
	}
	var hasDelete bool
	for _, c := range m.rowChoices(fEnv, pinned) {
		if c.act == actDelete {
			hasDelete = true
		}
	}
	if !hasDelete {
		t.Fatal("a pinned passthrough offers no Delete, so there is no way to un-pin")
	}

	m.itemHostEnv = true
	m.deleteItem(fEnv, pinned.idx)
	if len(m.hostEnv) != 0 {
		t.Fatalf("hostEnv = %+v, want the pin removed", m.hostEnv)
	}
	// Un-pinning must write NOTHING, not an explicit restatement: the cascade
	// cannot tell a restatement from a deliberate pin afterwards.
	if m.assemble().EnvFromHost != nil {
		t.Fatalf("EnvFromHost = %+v, want nothing written", m.assemble().EnvFromHost)
	}
}

// The Env screen asks ONE question -- where does this value come from -- so
// adding a passthrough is a picker move, not a different screen. Before this,
// the add key built a literal editor and there was no way to add one at all.
func TestEnvAddCanCreateAPassthrough(t *testing.T) {
	m := hostEnvModel(t, nil)
	m.listField = fEnv
	m.itemHostEnv = false
	m = m.startItem(-1)
	if !m.itemHasMode {
		t.Fatal("the Env add editor has no source picker")
	}
	if m.itemMode != schemeValue {
		t.Fatalf("add opened on mode %d, want value (the common answer)", m.itemMode)
	}
	m.itemMode = schemeEnv
	m.inputs[0].SetValue("EDITOR")
	m.inputs[1].SetValue("EDITOR")
	got := m.commitItem()
	if got.itemErr != "" {
		t.Fatalf("adding a passthrough refused: %s", got.itemErr)
	}
	if v := got.assemble().EnvFromHost["EDITOR"]; v != "env:EDITOR" {
		t.Fatalf("EnvFromHost[EDITOR] = %q, want env:EDITOR", v)
	}
	if _, isLiteral := got.assemble().Env["EDITOR"]; isLiteral {
		t.Fatal("a passthrough also landed in [env]")
	}
}

// Switching the picker MOVES the entry rather than leaving a twin behind.
func TestEnvSwitchingSourceMovesTheEntry(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{Env: map[string]string{"TERM": "xterm"}}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fEnv
	m.itemHostEnv = false
	m = m.startItem(0) // the [env] literal
	if m.itemMode != schemeValue {
		t.Fatalf("editing a literal opened on mode %d, want value", m.itemMode)
	}
	m.itemMode = schemeEnv
	m.inputs[1].SetValue("TERM")
	got := m.commitItem()
	if got.itemErr != "" {
		t.Fatalf("conversion refused: %s", got.itemErr)
	}
	if _, still := got.assemble().Env["TERM"]; still {
		t.Fatal("the literal survived in [env] after being converted")
	}
	if v := got.assemble().EnvFromHost["TERM"]; v != "env:TERM" {
		t.Fatalf("EnvFromHost[TERM] = %q, want env:TERM", v)
	}
}

// An inherited key is only ever written through a door the user picked. The
// hazard this guards against is the one the onboarding picker shipped once --
// a picker writing an explicit value where the user meant "don't decide here"
// -- and the shape of the guard is that there is no PASSIVE path: enter on a
// passthrough row opens the menu, whose only inherited-row choice is
// "Override here", which says what it does. Committing that pins, and Inherit
// is the way back off.
func TestHostEnvInheritedKeysAreOnlyWrittenThroughAnExplicitDoor(t *testing.T) {
	m := hostEnvModel(t, nil)

	var inherited listRow
	for _, r := range m.fieldRows(fEnv) {
		if r.kind == rowHostEnv && r.ident == "TZ" {
			inherited = r
		}
	}
	choices := m.rowChoices(fEnv, inherited)
	if len(choices) != 1 || choices[0].act != actOverride {
		t.Fatalf("inherited passthrough offers %+v; want exactly the override door", choices)
	}

	// Taking that door pins it -- that is what the door says.
	m.listField = fEnv
	m.itemHostEnv = true
	pinned := m.startOverride(inherited).commitItem()
	if pinned.itemErr != "" {
		t.Fatalf("override refused: %s", pinned.itemErr)
	}
	if got := pinned.assemble().EnvFromHost["TZ"]; got != "tz:" {
		t.Fatalf("EnvFromHost[TZ] = %q, want the inherited scheme pinned", got)
	}

	// And nothing else on the screen wrote anything: only TZ moved.
	if n := len(pinned.assemble().EnvFromHost); n != 1 {
		t.Fatalf("EnvFromHost has %d keys, want only the one the user acted on", n)
	}
}

func TestHostEnvSchemeRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		src    string
		scheme int
		arg    string
	}{
		{"git:user.email", schemeGit, "user.email"},
		{"env:TERM", schemeEnv, "TERM"},
		{"tz:", schemeTZ, ""},
		{"", schemeDisabled, ""},
	} {
		gotScheme, gotArg := hostEnvScheme(tc.src)
		if gotScheme != tc.scheme || gotArg != tc.arg {
			t.Errorf("hostEnvScheme(%q) = %d,%q; want %d,%q", tc.src, gotScheme, gotArg, tc.scheme, tc.arg)
		}
		if back := hostEnvSource(tc.scheme, tc.arg); back != tc.src {
			t.Errorf("hostEnvSource(%d,%q) = %q; want %q", tc.scheme, tc.arg, back, tc.src)
		}
	}
}

// Disabled is a VALUE (KEY = ""), not an absence -- that is how a config
// switches a lower layer's passthrough off.
func TestHostEnvDisabledWritesTheSentinel(t *testing.T) {
	m := hostEnvModel(t, nil)
	m = openHostEnvRow(t, m, "TERM")
	m.itemMode = schemeDisabled
	got := m.commitItem()
	if got.itemErr != "" {
		t.Fatalf("disable refused: %s", got.itemErr)
	}
	if v, ok := got.assemble().EnvFromHost["TERM"]; !ok || v != "" {
		t.Fatalf("EnvFromHost[TERM] = %q,%v; want the empty-string disable sentinel", v, ok)
	}
}

// The scheme picker drives the form: the second field means something
// different per scheme, so its label has to follow the selection.
func TestHostEnvArgLabelFollowsTheScheme(t *testing.T) {
	m := hostEnvModel(t, nil)
	m = openHostEnvRow(t, m, "TZ")
	m.itemMode = schemeGit
	m = m.syncHostEnvLabel()
	if !strings.Contains(m.inputLabels[1], "git config key") {
		t.Errorf("label = %q, want it to name a git config key", m.inputLabels[1])
	}
	m.itemMode = schemeTZ
	m = m.syncHostEnvLabel()
	if !strings.Contains(m.inputLabels[1], "no argument") {
		t.Errorf("label = %q, want it to say tz takes no argument", m.inputLabels[1])
	}
}

// config owns the grammar; the editor calls the same validator Save runs.
func TestHostEnvRefusesWhatConfigRefuses(t *testing.T) {
	m := hostEnvModel(t, nil)
	m.listField = fEnv
	m = m.startItem(-1)
	m.itemMode = schemeGit
	m.inputs[0].SetValue("not a valid name!")
	m.inputs[1].SetValue("user.name")
	if got := m.commitItem(); got.itemErr == "" {
		t.Fatal("accepted an invalid environment variable name")
	}
}

// A conversion that collides must leave the working state UNTOUCHED. It used
// to remove the entry from the map it was leaving before checking the
// destination, so a refused conversion reported "duplicate" and had already
// deleted the row it was converting -- escape, and the deletion survived to
// be saved.
func TestEnvFailedConversionKeepsTheOriginalRow(t *testing.T) {
	cfg := config.Config{
		Env:         map[string]string{"FOO": "literal"},
		EnvFromHost: map[string]string{"FOO": "env:FOO"},
	}
	m := newModel("t", "/tmp/x", cfg, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fEnv
	m.itemHostEnv = false
	m = m.startItem(0) // the [env] literal
	m.itemMode = schemeEnv
	m.inputs[1].SetValue("FOO")

	got := m.commitItem()
	if got.itemErr == "" {
		t.Fatal("converting onto an occupied key must be refused")
	}
	if v := got.assemble().Env["FOO"]; v != "literal" {
		t.Fatalf("Env[FOO] = %q after a refused conversion, want the literal intact", v)
	}
	if v := got.assemble().EnvFromHost["FOO"]; v != "env:FOO" {
		t.Fatalf("EnvFromHost[FOO] = %q, want the passthrough untouched", v)
	}
}
