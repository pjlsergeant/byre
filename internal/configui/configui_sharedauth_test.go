package configui

import (
	"strings"
	"testing"
	"testing/fstest"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/onboard"
	"github.com/pjlsergeant/byre/internal/packages"
)

// A REAL catalog, because the staleness check is skills.SharedAuthPickLive
// over the catalog -- the same call the two apply paths make. A hand-built
// map would test a shape production never produces.
func sharedAuthCatalog(t *testing.T) *packages.Catalog {
	t.Helper()
	bundled := fstest.MapFS{
		"skills/claude/skill.toml":               &fstest.MapFile{Data: []byte("description = \"c\"\n[agent]\ncommand = \"claude\"\n")},
		"skills/claude-shared-auth/skill.toml":   &fstest.MapFile{Data: []byte("shared_auth_for = \"claude\"\n")},
		"skills/opencode-shared-auth/skill.toml": &fstest.MapFile{Data: []byte("shared_auth_for = \"opencode\"\n")},
	}
	cat, err := packages.LoadCatalog(t.TempDir(), bundled, "v0.2.0", "0.2.0", packages.Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func sharedAuthModel(t *testing.T, pref config.SharedAuthPref) model {
	t.Helper()
	inh := Inherited{Catalog: sharedAuthCatalog(t)}
	cfg := config.Config{Defaults: config.Defaults{SharedAuth: pref}}
	m := newModel("t", "/x", cfg, nil, []string{"claude"},
		[]string{"claude", "claude-shared-auth", "opencode-shared-auth"}, nil, inh, nil, TargetGlobal)
	m.width = 200 // the row is the subject, not the clip
	return m
}

// The stored shared-credentials answer had no surface at all: the only way to
// read which companion a new project would be given was to open the file. It
// shows read-only, next to the checkbox that decides whether it is applied
// without asking.
func TestSharedAuthRowShowsTheStoredPick(t *testing.T) {
	m := sharedAuthModel(t, config.SharedAuthPref{Pick: map[string]string{"claude": "claude-shared-auth"}})
	if indexOfField(m.order, fSharedAuth) < 0 {
		t.Fatal("the global editor has no shared-credentials row")
	}
	got := m.renderValue(fSharedAuth, false)
	if !strings.Contains(got, "claude") || !strings.Contains(got, "claude-shared-auth") {
		t.Errorf("the row must name the agent and its companion: %q", got)
	}
	if strings.Contains(got, "no longer installed") {
		t.Errorf("a live pick must not be flagged stale: %q", got)
	}
	// It sits beside the checkbox that decides whether it applies unasked.
	skip, shared := indexOfField(m.order, fSkipQuestions), indexOfField(m.order, fSharedAuth)
	if shared != skip+1 {
		t.Errorf("the row must be adjacent to skip_questions (%d vs %d)", skip, shared)
	}
}

// The point of showing it: a pick is a NAME, and the next new project applies
// it. A name nothing claims any more is flagged where it can still be acted on.
func TestSharedAuthRowFlagsAStalePick(t *testing.T) {
	m := sharedAuthModel(t, config.SharedAuthPref{Pick: map[string]string{"claude": "gone-shared-auth"}})
	got := m.renderValue(fSharedAuth, false)
	if !strings.Contains(got, onboard.StalePickNotice("gone-shared-auth")) {
		t.Errorf("a pick no live skill claims must be flagged: %q", got)
	}
	// A companion that claims a DIFFERENT agent does not answer for this one.
	m2 := sharedAuthModel(t, config.SharedAuthPref{Pick: map[string]string{"claude": "opencode-shared-auth"}})
	if got := m2.renderValue(fSharedAuth, false); !strings.Contains(got, "no longer installed") {
		t.Errorf("a companion claiming another agent is not this agent's: %q", got)
	}
}

// Legacy yes-inclinations name no companion, so there is nothing to check and
// nothing that gets applied unasked -- the row says which it is, and since
// the 2026-08-23 ADR 0049 amendment carries the compat warning (drop on
// save, re-answer to re-record) the other surfaces print.
func TestSharedAuthRowDistinguishesALegacyYes(t *testing.T) {
	m := sharedAuthModel(t, config.SharedAuthPref{Yes: []string{"claude"}})
	got := m.renderValue(fSharedAuth, false)
	if !strings.Contains(got, "no companion recorded") {
		t.Errorf("a yes with no pick must say so: %q", got)
	}
	for _, frag := range []string{"the next save drops it", "answer the shared-auth question again"} {
		if !strings.Contains(got, frag) {
			t.Errorf("the legacy entry must carry the compat warning (%q): %q", frag, got)
		}
	}
	if strings.Contains(got, "no longer installed") {
		t.Errorf("nothing to check, so nothing to flag: %q", got)
	}

	empty := sharedAuthModel(t, config.SharedAuthPref{})
	if got := empty.renderValue(fSharedAuth, false); !strings.Contains(got, "nothing stored") {
		t.Errorf("an unanswered preference must read as unanswered: %q", got)
	}
}

// The retired TOP-LEVEL spelling has no row of its own (P0's named
// exemption), so the shared-auth row that owns the key carries its warning:
// parseable, retired, migrated under [defaults] by the next save. Both
// legacy facts warn independently — a top-level file also carrying picks
// still discloses its spelling.
func TestSharedAuthRowWarnsOnTheTopLevelSpelling(t *testing.T) {
	m := sharedAuthModel(t, config.SharedAuthPref{})
	m.base.SharedAuthLegacy = config.SharedAuthPref{Pick: map[string]string{"claude": "claude-shared-auth"}}
	got := m.renderValue(fSharedAuth, false)
	for _, frag := range []string{"legacy top-level shared_auth", "the next save moves it under [defaults]"} {
		if !strings.Contains(got, frag) {
			t.Errorf("top-level spelling must warn with %q: %q", frag, got)
		}
	}
	// Canonical home only: no top-level warning.
	clean := sharedAuthModel(t, config.SharedAuthPref{Pick: map[string]string{"claude": "claude-shared-auth"}})
	if got := clean.renderValue(fSharedAuth, false); strings.Contains(got, "top-level") {
		t.Errorf("a canonical preference must not carry the top-level warning: %q", got)
	}
}

// Read-only, and it says so where a user would otherwise press keys at it: the
// answer is a consent the first-run question takes with the machine-wide
// credential consequence stated, not something a picker row authors here.
func TestSharedAuthRowIsReadOnly(t *testing.T) {
	pref := config.SharedAuthPref{Pick: map[string]string{"claude": "claude-shared-auth"}}
	m := sharedAuthModel(t, pref)
	if got := m.renderValue(fSharedAuth, true); !strings.Contains(got, "read-only") {
		t.Errorf("the focused row must say it cannot be edited: %q", got)
	}
	m.setFocus(indexOfField(m.order, fSharedAuth))
	before := m.sig()
	for _, k := range []tea.KeyMsg{{Type: tea.KeyLeft}, {Type: tea.KeyRight}, {Type: tea.KeyEnter}} {
		mm, _ := m.updateForm(k)
		m = mm.(model)
	}
	if m.sig() != before {
		t.Error("keys at the shared-credentials row must change nothing")
	}
	if got := m.assemble().StoredSharedAuth(); !got.Equal(pref) {
		t.Errorf("the stored answer must round-trip untouched: %+v", got)
	}
}
