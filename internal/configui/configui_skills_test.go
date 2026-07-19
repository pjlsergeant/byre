package configui

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// The skills multi-select groups agent skills separately, locks the primary
// agent (ticked, can't toggle), toggles the rest, preserves enabled-unknowns,
// and round-trips through assemble.
func TestSkillsMultiSelect(t *testing.T) {
	cfg := config.Config{Agent: "claude", Skills: []string{"moarcode", "ghost-skill"}} // ghost not installed
	agents := []string{"claude", "codex"}
	all := []string{"claude", "codex", "moarcode", "shem"}
	m := newModel("t", "/tmp/x", cfg, nil, agents, all, nil, Inherited{}, nil, TargetProject)

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

// The skills screen shows a skill's one-line description beside its name (so
// near-namesakes like claude vs claude-shared-auth are tellable apart) and
// renders undesc'd skills as just the name.
func TestSkillsScreenShowsDescriptions(t *testing.T) {
	cfg := config.Config{Agent: "claude"}
	descs := map[string]string{"claude-shared-auth": "Share one Claude login across all your projects."}
	m := newModel("t", "/tmp/x", cfg, nil, []string{"claude"}, []string{"claude", "claude-shared-auth"}, descs, Inherited{}, nil, TargetProject)
	view := m.viewSkills()
	if !strings.Contains(view, "Share one Claude login") {
		t.Fatalf("description not rendered:\n%s", view)
	}
	if !strings.Contains(view, "claude-shared-auth") {
		t.Fatalf("skill name missing:\n%s", view)
	}
}

// A companion skill (one paired to an agent via companion_for, or via the
// pairing shared_auth_for implies — CompanionFor carries the resolved fact
// either way, ADR 0034) nests as an indented child directly under its
// agent's row in the agent-skills section, so the pairing is visible where
// you enable it. Nesting rides the pairing alone — a gate-pending companion
// (no vouch, no offer) nests exactly like a vouched one. A companion whose
// agent has no row stays a plain skill.
func TestSkillsCompanionNestedUnderAgent(t *testing.T) {
	cfg := config.Config{Agent: "claude"}
	agents := []string{"claude", "codex"}
	all := []string{"claude", "claude-shared-auth", "codex", "moarcode", "orphan-shared-auth"}
	inh := Inherited{Skills: map[string]SkillRuntime{
		"claude-shared-auth": {CompanionFor: "claude"},
		"orphan-shared-auth": {CompanionFor: "gemini"}, // no gemini row anywhere
	}}
	m := newModel("t", "/tmp/x", cfg, nil, agents, all, nil, inh, nil, TargetProject)

	entries := m.skillEntries()
	idx := map[string]int{}
	for i, e := range entries {
		idx[e.name] = i
	}
	comp := entries[idx["claude-shared-auth"]]
	if !comp.child || !comp.agent {
		t.Fatalf("companion should be a nested child in the agent section: %+v", comp)
	}
	if idx["claude-shared-auth"] != idx["claude"]+1 {
		t.Fatalf("companion should sit directly under its agent: %v", entries)
	}
	orphan := entries[idx["orphan-shared-auth"]]
	if orphan.child || orphan.agent {
		t.Fatalf("companion with no agent row should stay a plain skill: %+v", orphan)
	}
	if idx["moarcode"] > idx["claude"] {
		t.Fatalf("plain skills should still sort before the agent section: %v", entries)
	}

	// The nested row renders indented under its agent.
	view := m.viewSkills()
	if !strings.Contains(view, "└ [ ] claude-shared-auth") {
		t.Fatalf("nested companion not rendered indented:\n%s", view)
	}

	// Toggling the nested row still enables the companion by name.
	m.skillCur = idx["claude-shared-auth"]
	mm, _ := m.updateSkills(key(" "))
	m = mm.(model)
	if !contains(m.skills, "claude-shared-auth") {
		t.Fatalf("space should enable the nested companion: %v", m.skills)
	}
}

// A skill listed in `skills` that becomes the primary agent must not be written
// back into `skills` (the agent field implies it).
func TestSkillsPrimaryNotDoubleWritten(t *testing.T) {
	cfg := config.Config{Agent: "claude", Skills: []string{"codex"}} // codex enabled as a skill
	m := newModel("t", "/tmp/x", cfg, nil, []string{"claude", "codex"}, []string{"claude", "codex"}, nil, Inherited{}, nil, TargetProject)
	// Promote codex to the primary agent.
	m.agentSel = indexOf(m.agentOpts, "codex")
	if out := m.assemble(); contains(out.Skills, "codex") {
		t.Fatalf("primary agent must be stripped from skills, got %v", out.Skills)
	}
}

// Inherited skills (enabled by default.config / the template) must show as ON
// with an "(inherited)" mark -- an unchecked box for an effectively-on skill
// is a lie (found live 2026-07-07). Toggling one writes the cascade's real
// off-switch (`!name`) into THIS layer; toggling again removes it.
func TestSkillsInheritedShownOnAndToggledViaRemoval(t *testing.T) {
	inherited := Inherited{HasLower: true, Default: config.Config{Skills: []string{"claude-shared-auth", "devloop"}}}
	m := newModel("t", "/tmp/x", config.Config{Agent: "claude"}, nil,
		[]string{"claude"}, []string{"claude", "devloop", "claude-shared-auth"}, nil, inherited, nil, TargetProject)

	find := func(mm model, name string) skillEntry {
		for _, e := range mm.skillEntries() {
			if e.name == name {
				return e
			}
		}
		t.Fatalf("skill %q not in entries", name)
		return skillEntry{}
	}
	idx := func(mm model, name string) int {
		for i, e := range mm.skillEntries() {
			if e.name == name {
				return i
			}
		}
		t.Fatalf("skill %q not in entries", name)
		return -1
	}

	e := find(m, "claude-shared-auth")
	if !e.inherited || !e.on() {
		t.Fatalf("inherited skill should render ON: %+v", e)
	}
	if !strings.Contains(m.viewSkills(), "(inherited)") {
		t.Fatalf("inherited mark missing:\n%s", m.viewSkills())
	}

	// Toggle OFF -> a !name removal marker in this layer, entry shows off.
	m.skillCur = idx(m, "claude-shared-auth")
	mm, _ := m.updateSkills(key(" "))
	m = mm.(model)
	if !contains(m.skills, "!claude-shared-auth") {
		t.Fatalf("toggle should write the removal marker: %v", m.skills)
	}
	if e := find(m, "claude-shared-auth"); e.on() || !e.removedHere {
		t.Fatalf("removed-here entry should render OFF: %+v", e)
	}

	// Toggle again -> marker gone, back to inherited-on.
	m.skillCur = idx(m, "claude-shared-auth")
	mm, _ = m.updateSkills(key(" "))
	m = mm.(model)
	if contains(m.skills, "!claude-shared-auth") || !find(m, "claude-shared-auth").on() {
		t.Fatalf("second toggle should re-inherit: %v", m.skills)
	}

	// A redundant local entry peels one layer per press: local entry first
	// (still inherited-on), then the removal marker.
	m.skills = append(m.skills, "devloop")
	m.skillCur = idx(m, "devloop")
	mm, _ = m.updateSkills(key(" "))
	m = mm.(model)
	if contains(m.skills, "devloop") {
		t.Fatalf("first press should drop the redundant local entry: %v", m.skills)
	}
	if !find(m, "devloop").on() {
		t.Fatal("devloop should still be inherited-on after peeling the local entry")
	}
	m.skillCur = idx(m, "devloop")
	mm, _ = m.updateSkills(key(" "))
	m = mm.(model)
	if !contains(m.skills, "!devloop") {
		t.Fatalf("second press should write the removal marker: %v", m.skills)
	}

	// assemble round-trips the marker (Save already accepts removal layers).
	if out := m.assemble(); !contains(out.Skills, "!devloop") {
		t.Fatalf("assemble dropped the removal marker: %v", out.Skills)
	}
}

// The form's Skills summary counts EFFECTIVE state (what the skills screen
// shows checked), not raw layer entries: a `!name` removal marker is not an
// enabled skill, and inherited-on skills count even with an empty local list.
func TestSkillsSummaryCountsEffectiveState(t *testing.T) {
	inherited := Inherited{HasLower: true, Default: config.Config{Skills: []string{"claude-shared-auth", "devloop"}}}
	m := newModel("t", "/tmp/x", config.Config{Agent: "claude"}, nil,
		[]string{"claude"}, []string{"claude", "devloop", "claude-shared-auth"}, nil, inherited, nil, TargetProject)

	// Empty local layer, but primary agent + two inherited skills are on.
	if got := m.renderValue(fSkills, false); !strings.Contains(got, "3 enabled") {
		t.Fatalf("summary should count inherited-on skills: %q", got)
	}

	// A removal marker turns one inherited skill off; it must not be counted
	// as enabled itself (the raw layer holds exactly one entry: the marker).
	m.skills = []string{"!claude-shared-auth"}
	if got := m.renderValue(fSkills, false); !strings.Contains(got, "2 enabled") {
		t.Fatalf("summary should not count removal markers: %q", got)
	}
}

// A pre-existing `!name` marker in the loaded config must render as a row
// (removed state), not as a bogus skill named "!devloop".
func TestSkillsExistingRemovalMarkerRendered(t *testing.T) {
	inherited := Inherited{HasLower: true, Default: config.Config{Skills: []string{"devloop"}}}
	cfg := config.Config{Agent: "claude", Skills: []string{"!devloop"}}
	m := newModel("t", "/tmp/x", cfg, nil, []string{"claude"}, []string{"claude", "devloop"}, nil, inherited, nil, TargetProject)
	view := m.viewSkills()
	if strings.Contains(view, "] !devloop") {
		t.Fatalf("marker rendered as a bogus skill name:\n%s", view)
	}
	if !strings.Contains(view, "(inherited — removed here)") {
		t.Fatalf("removed-here mark missing:\n%s", view)
	}
}

// Same-layer enable+remove resolves OFF (Merge applies removals last), so the
// checkbox must show OFF; and a stale !primary marker must stay visible and
// clearable on the locked row (review findings).
func TestSkillsMarkerEdgeCases(t *testing.T) {
	// ["devloop", "!devloop"] in one layer -> effectively off.
	m := newModel("t", "/tmp/x", config.Config{Agent: "claude", Skills: []string{"devloop", "!devloop"}}, nil,
		[]string{"claude"}, []string{"claude", "devloop"}, nil, Inherited{}, nil, TargetProject)
	for _, e := range m.skillEntries() {
		if e.name == "devloop" && e.on() {
			t.Fatalf("same-layer enable+remove must render OFF: %+v", e)
		}
	}

	// agent = claude + skills = ["!claude"]: marker visible on the locked row
	// and one toggle clears it.
	m2 := newModel("t", "/tmp/x", config.Config{Agent: "claude", Skills: []string{"!claude"}}, nil,
		[]string{"claude"}, []string{"claude"}, nil, Inherited{}, nil, TargetProject)
	if !strings.Contains(m2.viewSkills(), "stale !claude marker") {
		t.Fatalf("stale primary marker invisible:\n%s", m2.viewSkills())
	}
	for i, e := range m2.skillEntries() {
		if e.name == "claude" {
			m2.skillCur = i
		}
	}
	mm, _ := m2.updateSkills(key(" "))
	m2 = mm.(model)
	if contains(m2.skills, "!claude") {
		t.Fatalf("toggle should clear the stale marker: %v", m2.skills)
	}
}
