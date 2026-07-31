package configui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pjlsergeant/byre/internal/config"
)

// seed_prefs is a THREE-state key (ADR 0045): unset inherits, an explicit
// false turns an inherited opt-in back off. The widget has to write all three,
// and "unset" has to survive an open-and-save untouched -- writing
// `seed_prefs = false` for "I didn't say" is a different instruction.
func TestSeedPrefsWidgetWritesAllThreeStates(t *testing.T) {
	if got, want := seedPrefsOpts, []string{"on", "off", "inherit"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("picker order = %v, want %v", got, want)
	}
	yes, no := true, false
	for _, tc := range []struct {
		name string
		in   *bool
		sel  int
	}{
		{"unset loads inherit", nil, 2},
		{"true loads on", &yes, 0},
		{"false loads off", &no, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel("t", "/x", config.Config{SeedPrefs: tc.in}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
			if m.seedPrefsSel != tc.sel {
				t.Fatalf("loaded selection %d, want %d", m.seedPrefsSel, tc.sel)
			}
			if m.dirty() {
				t.Error("loading a stored value must not read as an edit")
			}
			got := m.assemble().SeedPrefs
			switch {
			case tc.in == nil && got != nil:
				t.Errorf("unset round-tripped as %v — the editor invented an answer", *got)
			case tc.in != nil && (got == nil || *got != *tc.in):
				t.Errorf("round-trip lost the value: %v", got)
			}
		})
	}

	// Cycling reaches every state and each one is dirty against the last.
	m := newModel("t", "/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.setFocus(indexOfField(m.order, fSeedPrefs))
	drive := func(m model, k tea.KeyMsg) model {
		mm, _ := m.updateForm(k)
		return mm.(model)
	}
	// A fresh unset value starts at the final inherit row and wraps to on.
	m = drive(m, tea.KeyMsg{Type: tea.KeyRight})
	if got := m.assemble().SeedPrefs; got == nil || !*got {
		t.Fatalf("inherit -> right should be an explicit on, got %v", got)
	}
	if !m.dirty() {
		t.Error("changing the tri-state must mark the form dirty")
	}
	m = drive(m, tea.KeyMsg{Type: tea.KeyRight})
	if got := m.assemble().SeedPrefs; got == nil || *got {
		t.Fatalf("on -> right should be an explicit off, got %v", got)
	}
	m = drive(m, tea.KeyMsg{Type: tea.KeyRight})
	if got := m.assemble().SeedPrefs; got != nil {
		t.Fatalf("off -> right should return to inherit (key removed), got %v", *got)
	}
	// Enter cycles too (the checkbox rows' verb).
	m = drive(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.assemble().SeedPrefs; got == nil || !*got {
		t.Fatalf("enter should advance the picker, got %v", got)
	}
}

// The row states the perishability where the choice is made: the seed only
// fires into a volume being created, so ticking it on a project whose agent
// volume already exists does nothing, ever.
func TestSeedPrefsRowStatesPerishability(t *testing.T) {
	m := newModel("t", "/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.width = 200 // the explainer is what's under test, not the clip
	out := m.View()
	if !strings.Contains(out, "Seed prefs") {
		t.Fatalf("seed_prefs has no row in the form:\n%s", out)
	}
	if !strings.Contains(out, "CREATED") || !strings.Contains(out, "existing one is left alone") {
		t.Errorf("the row must say the seed only fires into a fresh volume:\n%s", out)
	}
}

// The inherit row names what it inherits, like every other picker's inherit row.
func TestSeedPrefsInheritRowNamesTheInheritedValue(t *testing.T) {
	yes := true
	inh := Inherited{HasLower: true, Default: config.Config{SeedPrefs: &yes}}
	m := newModel("t", "/x", config.Config{}, nil, nil, nil, nil, inh, nil, TargetProject)
	if got := m.renderValue(fSeedPrefs, false); !strings.Contains(got, "inherited: on") {
		t.Errorf("inherit row should name the inherited value: %q", got)
	}
	// An explicit selection is the answer; nothing to report about the cascade.
	m.seedPrefsSel = 1
	if got := m.renderValue(fSeedPrefs, false); strings.Contains(got, "inherited:") {
		t.Errorf("an explicit choice must not also claim to inherit: %q", got)
	}
}
