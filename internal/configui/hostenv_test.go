package configui

import (
	"encoding/base64"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/credentials"
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

// A read-only screen paints no add row, so the cursor must not reach one:
// walking past the last row onto a slot that renders nothing is the same
// paint-vs-state desync this batch has already paid for.
func TestReadOnlyScreenCursorCannotLeaveTheRows(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"firewall": {Files: map[string]string{"firewall.sh": "/usr/local/bin/byre-firewall"}},
	}}
	m := newModel("t", "/tmp/x", config.Config{Skills: []string{"firewall"}}, nil, nil, []string{"firewall"}, nil, inh, nil, TargetProject)
	m.listField = fSkillFiles
	n := len(m.fieldRows(fSkillFiles))
	if n == 0 {
		t.Fatal("fixture produced no skill rows")
	}
	// Exactly n presses from row 0: with the add slot reachable that lands
	// ON it; without, it wraps back to the first row. (n+5 presses would
	// wrap either way and prove nothing -- checked by mutation.)
	for i := 0; i < n; i++ {
		next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(model)
	}
	if m.listCur >= n {
		t.Fatalf("listCur = %d with %d rows -- the cursor reached the add slot a read-only screen does not paint", m.listCur, n)
	}
}

// A key set BOTH ways is legal config, and the explicit [env] wins (ADR
// 0026) -- so the passthrough row is dead. byre status already reported that
// as hostEnvOverridden; the editor showed two rows for one name with no hint
// which one did anything.
func TestEnvShadowedPassthroughIsMarkedAndNotCounted(t *testing.T) {
	cfg := config.Config{
		Env:         map[string]string{"FOO": "literal"},
		EnvFromHost: map[string]string{"FOO": "env:FOO", "BAR": "env:BAR"},
	}
	m := newModel("t", "/tmp/x", cfg, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fEnv

	var foo, bar listRow
	for _, r := range m.fieldRows(fEnv) {
		if r.kind == rowHostEnv {
			switch r.ident {
			case "FOO":
				foo = r
			case "BAR":
				bar = r
			}
		}
	}
	if foo.ident == "" || bar.ident == "" {
		t.Fatal("expected passthrough rows for both keys")
	}
	if !foo.closed {
		t.Error("the shadowed passthrough must not count as effective")
	}
	if bar.closed {
		t.Error("an unshadowed passthrough must stay effective")
	}
	if ann := rowAnnotation(foo); !strings.Contains(ann, "overridden by [env]") {
		t.Errorf("shadowed annotation = %q, want it to say the passthrough is not passed", ann)
	}
	if ann := rowAnnotation(bar); strings.Contains(ann, "overridden") {
		t.Errorf("unshadowed annotation = %q, want no override note", ann)
	}

	// The summary must not count a row that does nothing (ADR 0050: summaries
	// count only rows marked effective).
	withShadow, _, _, _ := rowCounts(m.fieldRows(fEnv))
	clean := newModel("t", "/tmp/x", config.Config{EnvFromHost: map[string]string{"BAR": "env:BAR"}}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	clean.listField = fEnv
	wantSame, _, _, _ := rowCounts(clean.fieldRows(fEnv))
	if withShadow != wantSame+1 { // +1 for the [env] FOO literal row itself
		t.Errorf("effective = %d with a shadowed passthrough, want %d (the dead row must not add to it)", withShadow, wantSame+1)
	}
}

// hostEnvRow finds one passthrough row by key.
func hostEnvRow(m model, key string) (listRow, bool) {
	for _, r := range m.fieldRows(fEnv) {
		if r.kind == rowHostEnv && r.ident == key {
			return r, true
		}
	}
	return listRow{}, false
}

// The screen renders the LIVE edit list, not the file as opened. A key added
// through the Source picker used to be written by the save and shown by
// nothing: no row, no change in either count, and a second add of the same
// key answered "duplicate key" while naming something the screen did not
// contain. Every other list field already read its live slice.
func TestEnvAddedPassthroughRendersBeforeSave(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fEnv
	before, _, _, _ := rowCounts(m.fieldRows(fEnv))
	beforeExposure := m.exposureNow().Env

	// What the item editor does on accept: append to the live slice.
	m.hostEnv = append(m.hostEnv, kvItem{Key: "QA_NEW", Value: "env:HOME"})

	r, ok := hostEnvRow(m, "QA_NEW")
	if !ok {
		t.Fatal("a passthrough added in this session must have a row before any save")
	}
	if r.idx < 0 {
		t.Errorf("idx = %d, want an index into the live slice so Edit/Delete reach it", r.idx)
	}
	if !strings.Contains(r.text, "env:HOME") {
		t.Errorf("row text = %q, want the scheme it was given", r.text)
	}
	if after, _, _, _ := rowCounts(m.fieldRows(fEnv)); after != before+1 {
		t.Errorf("effective = %d after adding a passthrough, want %d", after, before+1)
	}
	if after := m.exposureNow().Env; after != beforeExposure+1 {
		t.Errorf("exposure Env = %d after adding a passthrough, want %d", after, beforeExposure+1)
	}
}

// Disabling a passthrough writes KEY = "". The row must SURVIVE that, in the
// session and after a reload: a key with no row has no menu, so the only way
// back was hand-editing the TOML -- the dead end the Source picker was built
// to remove. Shown but not counted, exactly as a disabled mount is.
func TestEnvDisabledPassthroughStaysVisibleAndUncounted(t *testing.T) {
	// As reloaded from a file that already carries the off-switch.
	cfg := config.Config{EnvFromHost: map[string]string{"TZ": "", "TERM": "env:TERM"}}
	m := newModel("t", "/tmp/x", cfg, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fEnv

	r, ok := hostEnvRow(m, "TZ")
	if !ok {
		t.Fatal("a disabled passthrough must still have a row after reload, or nothing can re-enable it")
	}
	if !r.disabled {
		t.Error("the row must carry the disabled flag, so the tallies can skip it")
	}
	if !strings.Contains(r.text, "disabled") {
		t.Errorf("row text = %q, want it to say the key is switched off", r.text)
	}
	// Actionable: idx >= 0 is what rowChoices keys off for Edit/Delete.
	if r.idx < 0 {
		t.Errorf("idx = %d, want the row to reach the menu that re-enables it", r.idx)
	}
	if r.closed {
		t.Error("disabled is not shadowed -- closed would annotate it 'overridden by [env]', which nothing did")
	}

	// Counted by neither summary: it grants nothing, and byre status omits it.
	// Measured as a delta against the shipped core set, which every model
	// carries, so the numbers don't move when byre changes what it ships.
	base := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	base.listField = fEnv
	baseEff, _, _, _ := rowCounts(base.fieldRows(fEnv))
	eff, _, _, _ := rowCounts(m.fieldRows(fEnv))
	if eff != baseEff-1 {
		t.Errorf("effective = %d, want %d: disabling one of the shipped keys must drop exactly one grant", eff, baseEff-1)
	}
	if got, want := m.exposureNow().Env, base.exposureNow().Env-1; got != want {
		t.Errorf("exposure Env = %d, want %d", got, want)
	}

	// The other half: switched off by a LOWER layer rather than here. This is
	// the case the merge-side filter governs -- a local off-switch survives on
	// the live-overlay path regardless of it, so testing only the local one
	// asserts nothing about the filter. "Override here" is the way back, and
	// it needs a row to hang off.
	lower := Inherited{HasLower: true, Default: config.Config{EnvFromHost: map[string]string{"TZ": ""}}}
	lm := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, lower, nil, TargetProject)
	lm.listField = fEnv
	lr, ok := hostEnvRow(lm, "TZ")
	if !ok {
		t.Fatal("a passthrough a lower layer disabled must still have a row -- otherwise nothing can override it back on")
	}
	if !lr.disabled {
		t.Error("the inherited-disabled row must carry the disabled flag")
	}
	if lr.idx >= 0 {
		t.Errorf("idx = %d, want -1: this file does not set it, so the menu offers Override here", lr.idx)
	}
	if lEff, _, _, _ := rowCounts(lm.fieldRows(fEnv)); lEff != baseEff-1 {
		t.Errorf("effective = %d, want %d: a lower layer's off-switch removes a grant", lEff, baseEff-1)
	}
}

// The summary's "(N inherited)" is about where a row came FROM. A passthrough
// this file pins is not inherited, and idx >= 0 is the same discriminator the
// row's own "(set here)" annotation uses -- they disagreed, so a screen
// showing one row "set here" summarized it as "6 vars (6 inherited)".
func TestEnvLocalPassthroughIsNotCountedInherited(t *testing.T) {
	base := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	base.listField = fEnv
	baseEff, baseInherited, _, _ := rowCounts(base.fieldRows(fEnv))

	m := newModel("t", "/tmp/x", config.Config{EnvFromHost: map[string]string{"MINE": "env:MINE"}}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fEnv
	eff, inherited, _, _ := rowCounts(m.fieldRows(fEnv))
	if eff != baseEff+1 {
		t.Errorf("effective = %d, want %d: the pinned key is a grant", eff, baseEff+1)
	}
	if inherited != baseInherited {
		t.Errorf("inherited = %d, want %d: a key THIS file sets is not inherited, however many are", inherited, baseInherited)
	}
}

// A skill's [runtime].env does NOT shadow a passthrough: the runner writes
// skill env first and addEnvFromHost overwrites it, so the passthrough is the
// value the box gets. Marking it dead hid a live host->box grant on the one
// screen whose question is where a value comes from. gemini's TERM against
// byre's shipped TERM passthrough is the real instance.
func TestEnvSkillEnvDoesNotShadowPassthrough(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"gemini": {Env: map[string]string{"TERM": "xterm-256color"}},
	}}
	cfg := config.Config{Skills: []string{"gemini"}, EnvFromHost: map[string]string{"TERM": "env:TERM"}}
	m := newModel("t", "/tmp/x", cfg, nil, nil, []string{"gemini"}, nil, inh, nil, TargetProject)
	m.listField = fEnv

	r, ok := hostEnvRow(m, "TERM")
	if !ok {
		t.Fatal("expected a TERM passthrough row")
	}
	if r.closed {
		t.Error("a skill setting the same key must not mark the passthrough dead -- the passthrough wins at runtime")
	}
	if ann := rowAnnotation(r); strings.Contains(ann, "overridden") {
		t.Errorf("annotation = %q, want no override claim: nothing overrode it", ann)
	}
}

// Disabled and shadowed at once. resolveHostEnv tests the empty source
// BEFORE the [env] override, so a key that is both resolves disabled, not
// overridden -- and the row must not claim "[env]" overrode something that
// was already switched off. The two flags are independent fields, so nothing
// but this ordering keeps them from both being set.
func TestEnvDisabledOutranksShadowedOnOnePassthrough(t *testing.T) {
	cfg := config.Config{
		Env:         map[string]string{"TZ": "UTC"},
		EnvFromHost: map[string]string{"TZ": ""},
	}
	m := newModel("t", "/tmp/x", cfg, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fEnv

	r, ok := hostEnvRow(m, "TZ")
	if !ok {
		t.Fatal("expected a TZ passthrough row")
	}
	if !r.disabled {
		t.Error("an empty source is disabled, whatever else is set")
	}
	if r.closed {
		t.Error("disabled outranks shadowed: resolveHostEnv never reaches the override arm for an empty source")
	}
	if ann := rowAnnotation(r); strings.Contains(ann, "overridden") {
		t.Errorf("annotation = %q, want no override claim on a switched-off key", ann)
	}
}

// A skill's env_docs suggestion appears only while NOTHING supplies the
// variable. A disabled passthrough supplies nothing, so it must not retire
// the suggestion -- keeping disabled keys in the effective map (which the
// rows need) silently made every one of them look like a provider.
func TestEnvDisabledPassthroughDoesNotRetireASkillSuggestion(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"tool": {EnvDocs: map[string]string{"TOOL_TOKEN": "what the tool reads"}},
	}}
	cfg := config.Config{Skills: []string{"tool"}, EnvFromHost: map[string]string{"TOOL_TOKEN": ""}}
	m := newModel("t", "/tmp/x", cfg, nil, nil, []string{"tool"}, nil, inh, nil, TargetProject)
	m.listField = fEnv

	var sawDoc bool
	for _, r := range m.fieldRows(fEnv) {
		if r.kind == rowEnvDoc && r.ident == "TOOL_TOKEN" {
			sawDoc = true
		}
	}
	if !sawDoc {
		t.Error("a disabled passthrough provides nothing, so the skill's suggestion must still show")
	}

	// The live case is the control: once it actually carries a scheme, the
	// suggestion has done its job and goes.
	live := newModel("t", "/tmp/x", config.Config{Skills: []string{"tool"}, EnvFromHost: map[string]string{"TOOL_TOKEN": "env:TOOL_TOKEN"}}, nil, nil, []string{"tool"}, nil, inh, nil, TargetProject)
	live.listField = fEnv
	for _, r := range live.fieldRows(fEnv) {
		if r.kind == rowEnvDoc && r.ident == "TOOL_TOKEN" {
			t.Error("a live passthrough provides the var, so the suggestion must retire")
		}
	}
}

// The field summary and the exposure line must not disagree about how many
// variables a box gets. They count differently by construction -- one walks
// rows, the other distinct keys -- so a key named by two layers double-counted
// in the first. Live on any gemini project: the skill sets TERM and byre
// ships a TERM passthrough, which read "7 vars" beside an exposure of 6 once
// the passthrough stopped being (wrongly) marked dead.
func TestEnvFieldSummaryAgreesWithExposure(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"gemini": {Env: map[string]string{"TERM": "xterm-256color"}},
	}}
	for _, tc := range []struct {
		name string
		cfg  config.Config
	}{
		{"skill restates a passthrough", config.Config{Skills: []string{"gemini"}}},
		{"skill restates an [env] literal", config.Config{Skills: []string{"gemini"}, Env: map[string]string{"TERM": "dumb"}}},
		{"passthrough shadowed by a literal", config.Config{Env: map[string]string{"TZ": "UTC"}}},
		{"a disabled passthrough", config.Config{EnvFromHost: map[string]string{"TZ": ""}}},
		{"a pinned passthrough", config.Config{EnvFromHost: map[string]string{"MINE": "env:MINE"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel("t", "/tmp/x", tc.cfg, nil, nil, []string{"gemini"}, nil, inh, nil, TargetProject)
			m.listField = fEnv
			eff, inherited, fromSkills := m.envCounts()
			if got := m.exposureNow().Env; got != eff {
				t.Errorf("field summary says %d vars, exposure line says %d -- one variable, two tallies", eff, got)
			}
			if inherited+fromSkills > eff {
				t.Errorf("shares (%d inherited + %d from skills) exceed the total %d", inherited, fromSkills, eff)
			}
		})
	}
}

// The share labels attribute a colliding key to the contributor that WINS in
// the box, and the winner is not cascade order: [env] bakes as image ENV,
// skill env rides -e, and the engine's -e overrides ENV. So TERM set by both
// gemini and an [env] literal is gemini's at runtime, and the summary must
// say "from skills" -- the first version of the rank put [env] on top by
// cascade instinct, and one of two reviewers endorsed the same instinct while
// the other checked the engine.
func TestEnvShareAttributionFollowsRuntimeWinner(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"gemini": {Env: map[string]string{"TERM": "xterm-256color"}},
	}}

	// skill vs [env]: -e beats baked ENV, so the skill's row is the winner.
	m := newModel("t", "/tmp/x", config.Config{Skills: []string{"gemini"}, Env: map[string]string{"TERM": "dumb"}}, nil, nil, []string{"gemini"}, nil, inh, nil, TargetProject)
	m.listField = fEnv
	_, _, fromSkills := m.envCounts()
	if fromSkills != 1 {
		t.Errorf("fromSkills = %d, want 1: the skill's -e value is what the box gets for TERM", fromSkills)
	}

	// skill vs delivered passthrough: addEnvFromHost writes after skill env,
	// so the passthrough wins and the key is NOT a skill share.
	p := newModel("t", "/tmp/x", config.Config{Skills: []string{"gemini"}}, nil, nil, []string{"gemini"}, nil, inh, nil, TargetProject)
	p.listField = fEnv
	_, _, pSkills := p.envCounts()
	if pSkills != 0 {
		t.Errorf("fromSkills = %d, want 0: the delivered TERM passthrough beats the skill's value", pSkills)
	}
}

// ADR 0025's second suppression discloses AT the switch, because it acts on
// projects that do not exist yet. The consequence that matters -- the
// shared-credentials answer is one of the ones that stops being asked, and
// answering it grants -- must be readable BEFORE the box is ticked. It used
// to appear only once it was, so the reader had to opt in to learn what they
// were opting into.
func TestSkipQuestionsCheckboxDisclosesCredentialsUnticked(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetGlobal)
	if m.skipQuestions {
		t.Fatal("fixture must start unticked")
	}
	unticked := m.renderValue(fSkipQuestions, false)
	if !strings.Contains(unticked, "[ ]") {
		t.Fatalf("expected an unticked box, got %q", unticked)
	}
	if !strings.Contains(strings.ToLower(unticked), "credential") {
		t.Errorf("the unticked checkbox must name what stops being asked: %q", unticked)
	}

	m.skipQuestions = true
	if ticked := m.renderValue(fSkipQuestions, false); !strings.Contains(strings.ToLower(ticked), "credential") {
		t.Errorf("the disclosure must survive ticking too: %q", ticked)
	}
}

// The checkbox is a savable field, so its toggle must reach sig(): a field
// assemble() writes but sig() omits is silently lost -- esc quits with no
// discard confirm, ctrl+e reloads the file over it, and the footer reports a
// save that wrote nothing new.
func TestSkipQuestionsToggleMarksTheFormDirty(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetGlobal)
	if m.dirty() {
		t.Fatal("setup: a freshly-opened config must be clean")
	}
	m.setFocus(indexOfField(m.order, fSkipQuestions))
	mm, _ := m.updateForm(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(model)
	if !m.skipQuestions {
		t.Fatal("setup: enter must tick the checkbox")
	}
	if !m.dirty() {
		t.Error("ticking skip_questions must mark the form dirty")
	}
	if !m.assemble().Defaults.SkipQuestions {
		t.Error("assemble must carry the ticked checkbox")
	}
}

func indexOfField(order []fieldID, want fieldID) int {
	for i, f := range order {
		if f == want {
			return i
		}
	}
	return -1
}

// The other suppression: a vouched shared-auth companion sitting in
// default.config stops onboarding offering it at all. Legitimate (an "n"
// could not have removed it) and invisible, so the Skills screen says so
// where the switch is -- and only in the GLOBAL editor, since the same skill
// in a project file grants that box and suppresses no question.
func TestGlobalSkillsScreenDisclosesSharedAuthSuppression(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"claude-shared-auth": {SharedAuthFor: "claude"},
		"firewall":           {},
	}}
	cfg := config.Config{Skills: []string{"claude-shared-auth", "firewall"}}
	opts := []string{"claude-shared-auth", "firewall"}

	g := newModel("t", "/tmp/x", cfg, nil, nil, opts, nil, inh, nil, TargetGlobal)
	view := g.viewSkills()
	if !strings.Contains(view, "no longer asked about shared credentials") {
		t.Errorf("the global Skills screen must disclose the suppression:\n%s", view)
	}

	// Not in a project file: there it is an ordinary grant on one box.
	p := newModel("t", "/tmp/x", cfg, nil, nil, opts, nil, inh, nil, TargetProject)
	if pv := p.viewSkills(); strings.Contains(pv, "no longer asked about shared credentials") {
		t.Errorf("a project file suppresses no question; the note must not appear:\n%s", pv)
	}

	// And not for a skill that isn't a vouched companion.
	if strings.Count(view, "no longer asked about shared credentials") != 1 {
		t.Errorf("only the vouched companion earns the note:\n%s", view)
	}
}

// Config values are DATA on the list screens, and data does not get to drive
// the terminal. A value smuggling \r overwrote its own row with a forged one
// (the real key vanished, a key that exists nowhere appeared); an SGR escape
// terminated byre's styling mid-row. Literal control bytes never parse --
// TOML's \uXXXX escapes are the live path -- and the fix is display-only:
// r.vals prefills the editor and must stay raw or a save writes back a
// mangled value. Found by the 2026-07-27 exploratory pass with capture-pane.
func TestListRowsNeverLeakControlSequences(t *testing.T) {
	cfg := config.Config{
		Env: map[string]string{
			"SPOOF": "x\r  FAKE_ROW=totally-real",
			"EVIL":  "\x1b[31mRED\x1b[0m done",
		},
		Files: map[string]string{"a.txt": "/opt/\x1b[7mreverse\x1b[0m"},
	}
	m := newModel("t", "/tmp/x", cfg, nil, nil, nil, nil, Inherited{}, nil, TargetProject)

	for _, f := range []fieldID{fEnv, fFiles} {
		m.listField = f
		view := m.viewList()
		if strings.ContainsAny(view, "\r") {
			t.Errorf("%v view carries a raw CR -- a value can overwrite its own row:\n%q", f, view)
		}
		// byre's own dim/cursor styling is legitimate SGR, so the assertion
		// is not "no escapes": it is that the VALUES' payloads survive only
		// stripped. The reverse-video code rode inside the files value.
		if strings.Contains(view, "[7mreverse") {
			t.Errorf("%v view carries the value's own SGR sequence:\n%q", f, view)
		}
	}

	m.listField = fEnv
	view := m.viewList()
	if !strings.Contains(view, "SPOOF") {
		t.Errorf("the real key must render:\n%q", view)
	}
	if !strings.Contains(view, "FAKE_ROW=totally-real") {
		// The payload TEXT survives (stripped, inert, on SPOOF's own row) --
		// only the control character that made it a separate row is gone.
		t.Errorf("stripping must keep the printable payload:\n%q", view)
	}

	// The action menu re-paints the selected row, so the value neutralized in
	// the list must not come back to life on Enter (the funnel's closest
	// sibling; the strip originally stopped one screen short).
	for _, r := range m.fieldRows(fEnv) {
		if r.ident == "" && !strings.Contains(r.text, "SPOOF") {
			continue
		}
		m.menuRow = r
		if menu := m.viewMenu(); strings.ContainsAny(menu, "\r") {
			t.Errorf("menu view carries a raw CR for row %q:\n%q", r.text, menu)
		}
	}
}

// Egress's sibling of the env agreement rule: the field summary counts
// NORMALIZED doors, exactly as the exposure line and the launch tally do --
// "github.com" restated by a skill as "github.com:443" is one door, and it
// read as two in the summary while the exposure line said one. A door the
// user's own layer opens attributes to them, never "from skills", however
// many skills restate it (doors union; there is no runtime winner).
func TestEgressFieldSummaryAgreesWithExposure(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"fw": {Egress: []string{"github.com:443"}, Posture: "deny-by-default"},
	}}
	cfg := config.Config{Skills: []string{"fw"}, Egress: []string{"github.com"}}
	m := newModel("t", "/tmp/x", cfg, nil, nil, []string{"fw"}, nil, inh, nil, TargetProject)
	m.listField = fEgress

	eff, _, fromSkills := m.egressCounts()
	if got := m.exposureNow().Egress; got != eff {
		t.Errorf("field summary says %d doors, exposure line says %d -- one door, two tallies", eff, got)
	}
	if eff != 1 {
		t.Errorf("effective = %d, want 1: two spellings of one door", eff)
	}
	if fromSkills != 0 {
		t.Errorf("fromSkills = %d, want 0: the user's own layer opens this door", fromSkills)
	}
}

// errLine strips control sequences PER LINE: validation remedies are
// deliberately multiline (the BYRE_ refusal carries its run_args remedy on
// its own line), and a whole-message strip ate the newlines and ran remedy
// into refusal ("[env]to override" -- caught by review, after the identical
// trap was fixed in proseBlock without being generalized).
func TestErrLineKeepsDeliberateNewlinesAndStripsControls(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.width = 200
	got := m.errLine("refused in [env]\nto override: run_args\x1b[31m")
	if strings.Contains(got, "[env]to") {
		t.Errorf("refusal and remedy ran together -- the newline was eaten: %q", got)
	}
	if !strings.Contains(got, "\n") {
		t.Errorf("the remedy's own line must survive (wrap may pad it): %q", got)
	}
	if strings.Contains(got, "[31m") {
		t.Errorf("the smuggled SGR must not: %q", got)
	}
}

// A credential row shares the Env screen with the passthroughs, and it reads
// as what it is: the payload elides, "host" would name a source it does not
// have, and the value-state cell speaks `byre credentials list`'s word. This
// pins the RENDER; the editing half moved to credentials_test.go when the
// editor grew its own write path (an editor with none — the --global one, and
// this fixture — still refuses the row, and must not name a CLI verb that
// cannot target its file).
func TestEnvScreenRendersACredentialRowAndRefusesItWithNoWritePath(t *testing.T) {
	credentials.SetWorkFactorForTesting(10)
	_, recipient, err := credentials.NewIdentity("pw")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := credentials.EncryptValue(recipient, "STRIPE_KEY", credentials.KindEnv, []byte("sk"))
	if err != nil {
		t.Fatal(err)
	}
	row, err := config.FormatEncryptedRow(credentials.KindEnv, blob)
	if err != nil {
		t.Fatal(err)
	}
	m := hostEnvModel(t, map[string]string{"STRIPE_KEY": row})
	// The row says what it is and elides the payload: "host" would name a
	// source this value does not have, and the ciphertext would push every
	// other row off the screen.
	line := hostEnvLine("STRIPE_KEY", row)
	if !strings.Contains(line, "credential") || !strings.Contains(line, config.EncryptedScheme+"[…]") {
		t.Fatalf("credential row line = %q", line)
	}
	if !strings.Contains(line, credentials.ValueState(true)) {
		t.Fatalf("the row must say whether it holds a value, in list's own word: %q", line)
	}
	if strings.Contains(line, "host ") || len(line) > 60 {
		t.Fatalf("the row must not claim a host source nor carry the blob: %q", line)
	}
	m.target = TargetGlobal // the one target production leaves without a writer
	m = openHostEnvRow(t, m, "STRIPE_KEY")
	if m.mode == modeItem {
		t.Fatal("a credential row must not open the picker editor where nothing can write one")
	}
	// The remedy must be one this user can actually carry out: NO credentials
	// verb targets default.config, so naming `byre credentials set` here would
	// send them to a command that writes a shadowing row in another file.
	if !strings.Contains(m.status, credentialNoWritePathNote) {
		t.Fatalf("the refusal must say nothing here can write one: %q", m.status)
	}
	if strings.Contains(m.status, "byre credentials") {
		t.Fatalf("the refusal named a command that cannot target this file: %q", m.status)
	}
	// The value survives a save of everything else on the screen.
	if got := m.assemble().EnvFromHost["STRIPE_KEY"]; got != row {
		t.Fatalf("the ciphertext must round-trip untouched: %q", got)
	}
}

// An [env] literal takes a key out of env_from_host entirely (ADR 0026), so a
// credential row it beats reaches no box: EncryptedRows drops it from the
// launch set, and status and `byre credentials list` exclude it too. The
// editor's tally counted it anyway — "Credentials 1" on the screen against
// "credentials 0" at launch, over one key. Both sides now ask
// config.DeliversCredential, so this pins them together on exactly that shape.
func TestCredentialTallyExcludesAnEnvShadowedRowLikeTheLaunch(t *testing.T) {
	row := config.EncryptedScheme + base64.StdEncoding.EncodeToString([]byte("ciphertext"))
	// The launch side, from its own source of truth: the cascade's winning
	// credential rows, which is what develop's exposure line counts.
	launchRows := func(cfg config.Config) int {
		groups, err := config.EncryptedRows([]config.CascadeFile{{Label: "project", Cfg: cfg}})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, g := range groups {
			n += len(g.Rows)
		}
		return n
	}
	editorRows := func(cfg config.Config) int {
		return newModel("t", "/tmp/x", cfg, nil, nil, nil, nil, Inherited{}, nil, TargetProject).exposureNow().Credentials
	}

	shadowed := config.Config{Env: map[string]string{"FOO": "x"}, EnvFromHost: map[string]string{"FOO": row}}
	if got, want := editorRows(shadowed), launchRows(shadowed); got != want || want != 0 {
		t.Fatalf("an [env]-shadowed credential: editor counted %d, launch %d, want 0 on both", got, want)
	}
	// The control: the same row with no literal beating it is a credential on
	// both surfaces — a tally that simply stopped counting would pass above.
	live := config.Config{EnvFromHost: map[string]string{"FOO": row}}
	if got, want := editorRows(live), launchRows(live); got != want || want != 1 {
		t.Fatalf("an unshadowed credential: editor counted %d, launch %d, want 1 on both", got, want)
	}
}

// The rows that are credentials and DON'T parse: one on the reserved
// `manifest` key, one with a damaged payload. The list renders both as
// credentials (scheme prefix), so the picker and the exposure tally must
// agree — through ParseEncryptedRow's ok they did not, and such a row opened
// into the picker that writes "" over a ciphertext and counted as env. One
// predicate, config.IsCredentialSource, answers "is this a credential row"
// for every surface; `byre credentials unset` is still the repair.
//
// The boundary MOVED when the editor grew a write path (phase C): a
// well-formed credential row now opens into the masked form. These rows do
// not, and the model here is given that write path on purpose, so the refusal
// is pinned on their DAMAGE and not on a missing writer. Opening them would
// offer to re-encrypt over a row whose problem is not the value.
func TestEnvScreenTreatsUnparsableCredentialRowsAsCredentials(t *testing.T) {
	for _, tc := range []struct{ name, key, row string }{
		{"reserved key", config.ReservedCredentialItem, config.EncryptedScheme + "AAAA"},
		{"damaged payload", "STRIPE_KEY", config.EncryptedScheme + "!!"},
		{"damaged file payload", "TLS_CERT", config.EncryptedFileScheme + "!!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The premise: these are exactly the rows a successful parse
			// misses. If that stops being true this test is testing nothing.
			if _, ok, err := config.ParseEncryptedRow(tc.key, tc.row); ok || err == nil {
				t.Fatalf("premise gone: %q now parses as a credential row", tc.row)
			}
			m := hostEnvModel(t, map[string]string{tc.key: tc.row})
			m.creds = newFakeCredAdmin()
			if line := hostEnvLine(tc.key, tc.row); !strings.Contains(line, "credential") {
				t.Fatalf("the row must still read as a credential: %q", line)
			}
			opened := openHostEnvRow(t, m, tc.key)
			if opened.mode == modeItem {
				t.Fatal("a credential row must not open the picker editor, damaged or not")
			}
			if !strings.Contains(opened.status, "byre credentials set "+tc.key) {
				t.Fatalf("the refusal must name the surface that can change it: %q", opened.status)
			}
			if got := opened.assemble().EnvFromHost[tc.key]; got != tc.row {
				t.Fatalf("the row must round-trip untouched: %q", got)
			}
			// And it tallies as a credential, not as env: counting one grant
			// on both lines would make the two disagree.
			if e := m.exposureNow(); e.Credentials != 1 {
				t.Fatalf("exposure tally counted %d credential(s), want 1 (env=%d)", e.Credentials, e.Env)
			}
		})
	}
	// The control the boundary needs: the SAME screen, the same write path, a
	// row that parses — that one opens, or "damaged rows refuse" would pass on
	// an editor that refuses every credential row.
	t.Run("well-formed row opens", func(t *testing.T) {
		admin := newFakeCredAdmin()
		admin.identity, admin.recipient = mintFor(t, "pw")
		row := encryptedRow(t, admin.recipient, "STRIPE_KEY", credentials.KindEnv, "sk-live-1")
		m := credModel(t, admin, map[string]string{"STRIPE_KEY": row})
		if opened := openHostEnvRow(t, m, "STRIPE_KEY"); opened.mode != modeItem {
			t.Fatalf("a well-formed credential row must open the form; status: %q", opened.status)
		}
	})
}
