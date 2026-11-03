package configui

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

const unmetCodex = "(requires codex -- not enabled)"

// skillLine returns the rendered skills-screen line naming skill.
func skillLine(t *testing.T, view, skill string) string {
	t.Helper()
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "] "+skill+" ") || strings.HasSuffix(l, "] "+skill) {
			return l
		}
	}
	t.Fatalf("no row for %q:\n%s", skill, view)
	return ""
}

// toggle presses space on the named row.
func toggle(t *testing.T, m model, name string) model {
	t.Helper()
	found := false
	for i, e := range m.skillEntries() {
		if e.name == name {
			m.skillCur, found = i, true
		}
	}
	if !found {
		t.Fatalf("no row for %q", name)
	}
	mm, _ := m.updateSkills(key(" "))
	return mm.(model)
}

// An enabled skill whose requirement is off says so beside its state; turning
// the requirement on clears it on the next render, and turning the skill
// itself off still works -- the annotation never takes the disabled slot.
func TestSkillsScreenAnnotatesUnmetRequires(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"claude": {},
		"codex":  {},
		"zai":    {Requires: []string{"codex"}},
	}}
	cfg := config.Config{Agent: "claude", Skills: []string{"zai"}}
	m := newModel("t", "/tmp/x", cfg, nil, []string{"claude", "codex"}, []string{"claude", "codex", "zai"}, nil, inh, nil, TargetProject)

	if l := skillLine(t, m.viewSkills(), "zai"); !strings.Contains(l, "[x] zai") || !strings.Contains(l, unmetCodex) {
		t.Fatalf("an enabled zai without codex must read on, annotated: %q", l)
	}
	for _, e := range m.skillEntries() {
		if e.disabled != "" {
			t.Fatalf("an unmet requirement must not disable a row: %+v", e)
		}
	}

	on := toggle(t, m, "codex")
	if l := skillLine(t, on.viewSkills(), "zai"); strings.Contains(l, "requires") {
		t.Fatalf("enabling codex must clear the annotation: %q", l)
	}

	off := toggle(t, m, "zai")
	if contains(off.skills, "zai") {
		t.Fatalf("zai must still toggle off with its requirement unmet: %v", off.skills)
	}
	if l := skillLine(t, off.viewSkills(), "zai"); strings.Contains(l, "requires") {
		t.Fatalf("a skill that is off needs nothing: %q", l)
	}
}

// The primary agent meets a requirement the same way the skills list does,
// and the required name renders as data.
func TestSkillsScreenRequiresMetByAgentAndEscaped(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"codex": {},
		"zai":   {Requires: []string{"codex"}},
		"evil":  {Requires: []string{"x\x1b]0;pwned\x07"}},
	}}
	cfg := config.Config{Agent: "codex", Skills: []string{"zai", "evil"}}
	m := newModel("t", "/tmp/x", cfg, nil, []string{"codex"}, []string{"codex", "evil", "zai"}, nil, inh, nil, TargetProject)
	view := m.viewSkills()
	if l := skillLine(t, view, "zai"); strings.Contains(l, "requires") {
		t.Fatalf("the primary agent meets the requirement: %q", l)
	}
	l := skillLine(t, view, "evil")
	if strings.Contains(l, "\x1b]") || !strings.Contains(l, "(requires x") {
		t.Fatalf("the requirement must render escaped: %q", l)
	}
}
