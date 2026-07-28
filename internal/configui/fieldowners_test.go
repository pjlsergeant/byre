package configui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// The third reflection guard, and P0's first arm: a config key without a
// widget is a hole in the product, not a deferred nicety. Its two siblings
// cover the other halves -- TestReconcileCoversEveryField holds that a key can
// be WRITTEN, TestFieldInfosCoverEveryField that a row has its metadata. This
// one holds that a key has a row at all, and that the row is reachable in some
// editor. Between them, adding a key to config.Config and stopping there fails
// three tests, each naming its own missing piece.
//
// configFieldOwners maps each toml-visible config.Config field to the rows
// that surface it. Several keys share a row (env and env_from_host are one
// screen: both answer "where does this variable's value come from"), and
// several rows share a key (worktree_base is a checkbox plus a path input).
var configFieldOwners = map[string][]fieldID{
	"engine":          {fEngine},
	"template":        {fTemplate},
	"agent":           {fAgent},
	"base":            {fBase},
	"extends":         {fExtends},
	"seed_prefs":      {fSeedPrefs},
	"worktree_base":   {fWorktreeSibling, fWorktreeBase},
	"defaults":        {fSkipQuestions, fSharedAuth},
	"sources":         {fSources},
	"apt":             {fApt},
	"env":             {fEnv},
	"env_from_host":   {fEnv},
	"files":           {fFiles, fSkillFiles},
	"skills":          {fSkills},
	"mounts":          {fMounts},
	"volumes":         {fVolumes},
	"ports":           {fPorts},
	"egress":          {fEgress},
	"egress_offered":  {fEgress},
	"mcp":             {fMCP},
	"claude_skills":   {fClaudeSkills},
	"context":         {fContext},
	"dockerfile_pre":  {fDockerfilePre},
	"dockerfile_post": {fDockerfilePost},
	"run_args":        {fRunArgs},
}

// noWidgetKeys names the keys that deliberately have NO row, with the reason
// stated here rather than left as a silent absence. An exemption is a
// decision; the map is where it is written down.
var noWidgetKeys = map[string]string{
	"shared_auth": "the pre-2026-07-28 top-level spelling: read once and migrated into [defaults] on the next write, so a row would offer to edit a key byre is removing (the [defaults] one has the row -- fSharedAuth)",
}

// uiOnlyRows names rows that are not a config key at all, for the reverse
// direction of the guard.
var uiOnlyRows = map[fieldID]string{
	fVolumeData: "the engine side of volumes: what exists on disk right now, and clearing one — no config key, and no file to write",
}

// reachableFields is every row any editor target actually puts in its focus
// order. A row nobody can reach is the same hole as no row at all.
func reachableFields(t *testing.T) map[fieldID]bool {
	t.Helper()
	out := map[fieldID]bool{}
	for _, tc := range []struct {
		target Target
		vols   VolumeAdmin
	}{
		{TargetProject, &fakeVols{}},
		{TargetProject, nil},
		{TargetGlobal, nil},
		{TargetLayer, nil},
	} {
		m := newModel("t", "/x", config.Config{}, nil, nil, nil, nil, Inherited{}, tc.vols, tc.target)
		for _, f := range m.order {
			out[f] = true
		}
	}
	return out
}

func TestEveryConfigKeyHasAReachableRow(t *testing.T) {
	reachable := reachableFields(t)
	rt := reflect.TypeOf(config.Config{})
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		tag := strings.Split(rt.Field(i).Tag.Get("toml"), ",")[0]
		if tag == "" || tag == "-" {
			continue // cascade-internal: never in a layer file
		}
		owners, ok := configFieldOwners[tag]
		if !ok {
			if why := noWidgetKeys[tag]; why != "" {
				continue
			}
			t.Errorf("config.%s (toml %q) has no editor row.\n"+
				"  Give it one: a fieldID in form.go, a fieldInfos entry in fields.go, a case in renderValue "+
				"(or fieldRows + the listitem switches for a list), and the field in newModel's sections for "+
				"every target it belongs to — then add it to configFieldOwners here.\n"+
				"  If it deliberately gets none, say why in noWidgetKeys.", name, tag)
			continue
		}
		if len(owners) == 0 {
			t.Errorf("config.%s (toml %q): configFieldOwners entry is empty — name the row(s), or move it to noWidgetKeys with a reason", name, tag)
		}
		for _, f := range owners {
			if _, ok := fieldInfos[f]; !ok {
				t.Errorf("config.%s (toml %q) names fieldID %d, which has no fieldInfos row", name, tag, f)
				continue
			}
			if !reachable[f] {
				t.Errorf("config.%s (toml %q) names the %q row, but no editor target puts it in its focus order — "+
					"add it to a section in newModel, or the key is unreachable in every editor", name, tag, fieldLabel(f))
			}
		}
	}
}

func TestEveryEditorRowNamesAConfigKey(t *testing.T) {
	owned := map[fieldID]string{}
	for tag, fs := range configFieldOwners {
		for _, f := range fs {
			owned[f] = tag
		}
	}
	for f := fBase; f < fCount; f++ {
		if _, ok := owned[f]; ok {
			continue
		}
		if why := uiOnlyRows[f]; why != "" {
			continue
		}
		t.Errorf("the %q row (fieldID %d) names no config key.\n"+
			"  Add it to configFieldOwners under the toml key it edits, or to uiOnlyRows with the reason it edits none.",
			fieldLabel(f), f)
	}
}

// The read-only rows are SHOWN, not hidden: a flow-owned key the user cannot
// edit here is still a key whose value decides what their next box gets.
func TestFlowOwnedKeysAreShownReadOnlyNotHidden(t *testing.T) {
	for _, f := range []fieldID{fSources, fSharedAuth, fSkillFiles} {
		if _, ok := fieldInfos[f]; !ok {
			t.Errorf("fieldID %d has no row", f)
		}
	}
	if !isReadOnlyField(fSources) || !isReadOnlyField(fSkillFiles) {
		t.Error("the read-only list screens must be marked readOnly, or they grow an add row")
	}
	reachable := reachableFields(t)
	for _, f := range []fieldID{fSources, fSharedAuth, fSkillFiles} {
		if !reachable[f] {
			t.Errorf("%q is read-only AND unreachable, which is just hidden", fieldLabel(f))
		}
	}
}

// The --global editor's own sections (ONBOARDING FAVOURITES, WORKTREES,
// DEFAULTS) are historically untested as a shape: only the worktree checkbox
// had coverage. Every row it puts on screen must render its label and a value,
// and walking the whole focus order must not desync paint from state -- the
// class of bug a row with no renderValue case produces silently.
func TestGlobalEditorRendersEveryRowItOffers(t *testing.T) {
	yes := true
	cfg := config.Config{
		Base:         "debian:bookworm",
		WorktreeBase: "sibling",
		SeedPrefs:    &yes,
		Sources:      map[string]config.SourceHint{"acme/tool": {URI: "https://example.invalid/t.tgz"}},
		Defaults: config.Defaults{
			SkipQuestions: true,
			SharedAuth:    config.SharedAuthPref{Pick: map[string]string{"claude": "claude-shared-auth"}},
		},
	}
	inh := Inherited{Skills: map[string]SkillRuntime{"claude-shared-auth": {SharedAuthFor: "claude"}}}
	m := newModel("byre global config", "/x", cfg, []string{"go"}, []string{"claude"},
		[]string{"claude-shared-auth"}, nil, inh, nil, TargetGlobal)
	m.width, m.height = 200, 200 // the rows are the subject, not the clip

	for _, want := range []string{
		"ONBOARDING FAVOURITES", "WORKTREES", "DEFAULTS", "BUILD", "GRANTS", "ADVANCED",
	} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("the global form is missing its %s section:\n%s", want, m.View())
		}
	}
	// Every row in the order renders a label AND a non-empty value, focused and
	// not: a fieldID with no renderValue case paints a bare label instead.
	for i, f := range m.order {
		m.setFocus(i)
		for _, focused := range []bool{false, true} {
			got := m.renderValue(f, focused)
			if strings.TrimSpace(got) == "" {
				t.Errorf("%q renders no value (focused=%v) — a fieldID with no renderValue case", fieldLabel(f), focused)
			}
		}
		if !strings.Contains(m.View(), fieldLabel(f)) {
			t.Errorf("%q is in the focus order but not on screen", fieldLabel(f))
		}
	}
	// Walking the whole order changes nothing: focus is not an edit.
	before := m.sig()
	for i := range m.order {
		m.setFocus(i)
	}
	if m.sig() != before || m.dirty() {
		t.Error("moving the cursor through the global form must not dirty it")
	}
}
