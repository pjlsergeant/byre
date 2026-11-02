package configui

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

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

// Every agent row says whether the agent has a headless mode, beside the
// row's state and before its description; plain skills and nested
// companions say nothing, and the suffix never makes a row disabled.
func TestSkillsScreenShowsAgentHeadlessMode(t *testing.T) {
	cfg := config.Config{Agent: "claude"}
	descs := map[string]string{"claude": "The Claude agent."}
	inh := Inherited{Skills: map[string]SkillRuntime{
		"claude":             {AgentHeadless: "claude-headless --json"},
		"codex":              {},
		"claude-shared-auth": {CompanionFor: "claude"},
		"moarcode":           {},
	}}
	all := []string{"claude", "claude-shared-auth", "codex", "moarcode"}
	m := newModel("t", "/tmp/x", cfg, nil, []string{"claude", "codex"}, all, descs, inh, nil, TargetProject)
	view := m.viewSkills()

	claude := skillLine(t, view, "claude")
	state, head, desc := strings.Index(claude, "(primary agent)"), strings.Index(claude, "headless: claude-headless --json"), strings.Index(claude, "The Claude agent.")
	if state < 0 || head < 0 || desc < 0 || !(state < head && head < desc) {
		t.Errorf("claude row must read state, then headless, then description: %q", claude)
	}
	if codex := skillLine(t, view, "codex"); !strings.Contains(codex, "no headless command") {
		t.Errorf("an agent without a headless command must say so: %q", codex)
	}
	for _, plain := range []string{"moarcode", "claude-shared-auth"} {
		if l := skillLine(t, view, plain); strings.Contains(l, "headless") {
			t.Errorf("%s is not an agent and must carry no headless suffix: %q", plain, l)
		}
	}

	for _, e := range m.skillEntries() {
		if e.disabled != "" {
			t.Errorf("headless suffix must not disable a row: %+v", e)
		}
	}
	// Toggling the non-primary agent still works with the suffix present.
	for i, e := range m.skillEntries() {
		if e.name == "codex" {
			m.skillCur = i
		}
	}
	mm, _ := m.updateSkills(key(" "))
	if !contains(mm.(model).skills, "codex") {
		t.Errorf("space should still enable codex: %v", mm.(model).skills)
	}
}

func TestSkillsScreenEscapesHeadlessCommand(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{"evil": {AgentHeadless: "go\x1b]0;pwned\x07 --now"}}}
	m := newModel("t", "/tmp/x", config.Config{Agent: "evil"}, nil, []string{"evil"}, []string{"evil"}, nil, inh, nil, TargetProject)
	line := skillLine(t, m.viewSkills(), "evil")
	if strings.Contains(line, "]0;pwned\x07") || strings.Contains(line, "\x1b]") {
		t.Errorf("control characters in the headless command reached the screen: %q", line)
	}
	if !strings.Contains(line, "headless: go") {
		t.Errorf("escaped command still renders: %q", line)
	}
}

// A skill setting a BYRE_RUN_* key takes the headless claim away; the agent
// row hedges, live with the toggle that enabled the skill.
func TestSkillsScreenHedgesHeadlessUnderReservedEnv(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"claude": {AgentHeadless: "claude-headless"},
		"sneaky": {Env: map[string]string{"BYRE_RUN_MODEL": "x"}},
	}}
	m := newModel("t", "/tmp/x", config.Config{Agent: "claude"}, nil, []string{"claude"}, []string{"claude", "sneaky"}, nil, inh, nil, TargetProject)
	if l := skillLine(t, m.viewSkills(), "claude"); strings.Contains(l, skills.HeadlessHedge) {
		t.Errorf("hedged before the skill is enabled: %q", l)
	}
	for i, e := range m.skillEntries() {
		if e.name == "sneaky" {
			m.skillCur = i
		}
	}
	mm, _ := m.updateSkills(key(" "))
	m = mm.(model)
	if l := skillLine(t, m.viewSkills(), "claude"); !strings.Contains(l, "headless: claude-headless") || !strings.Contains(l, skills.HeadlessHedge) {
		t.Errorf("enabling a BYRE_RUN_* skill must hedge the headless command: %q", l)
	}
}
