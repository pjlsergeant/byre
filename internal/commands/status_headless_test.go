package commands

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

func TestStatusAgentRowCarriesHeadlessMode(t *testing.T) {
	render := func(s statusInfo) string {
		var b strings.Builder
		renderStatusTest(&b, s)
		return b.String()
	}

	with := render(statusInfo{Agent: "claude", AgentHeadless: "claude-headless --json", AgentHeadlessKnown: true})
	assertRow(t, with, "Agent", "claude, headless: claude-headless --json")

	without := render(statusInfo{Agent: "claude", AgentHeadlessKnown: true})
	assertRow(t, without, "Agent", "claude, no headless command")

	// No agent, or skills that did not resolve: the row cannot say, so it
	// says nothing rather than "no headless command".
	assertRow(t, render(statusInfo{AgentHeadlessKnown: true}), "Agent", "(none)")
	assertRow(t, render(statusInfo{Agent: "claude", SkillErr: "boom"}), "Agent", "claude")
}

func TestStatusAgentRowHedgesHeadlessUnderReservedEnv(t *testing.T) {
	s := statusInfo{
		Agent: "claude", AgentHeadless: "claude-headless", AgentHeadlessKnown: true,
		SkillReservedEnv: []skills.ReservedEnvSet{{Skill: "sneaky", Key: "BYRE_RUN_TASK_FILE"}},
	}
	var b strings.Builder
	renderStatusTest(&b, s)
	row := strings.Join(statusRows(b.String())["Agent"], " ")
	if !strings.Contains(row, "headless: claude-headless") || !strings.Contains(row, skills.HeadlessHedge) {
		t.Errorf("Agent row must hedge the headless command: %q", row)
	}

	// A reserved key outside the headless claim leaves the row unqualified.
	s.SkillReservedEnv = []skills.ReservedEnvSet{{Skill: "mcp", Key: "BYRE_MCP_CONFIG"}}
	b.Reset()
	renderStatusTest(&b, s)
	if row := strings.Join(statusRows(b.String())["Agent"], " "); strings.Contains(row, skills.HeadlessHedge) {
		t.Errorf("a non-headless reserved key must not hedge the headless command: %q", row)
	}
}

func TestStatusAgentRowEscapesHeadlessCommand(t *testing.T) {
	var b strings.Builder
	renderStatusTest(&b, statusInfo{Agent: "claude", AgentHeadless: "run\x1b]0;pwned\x07 --x", AgentHeadlessKnown: true})
	if out := b.String(); strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("control characters in the headless command reached the terminal: %q", out)
	}
}

// A launch record naming another agent swaps the row's subject; the headless
// command resolved for the config's agent must not ride along.
func TestStatusHeadlessDropsWhenTheLaunchedAgentDiffers(t *testing.T) {
	s := statusInfo{Agent: "byre/claude", AgentHeadless: "claude-headless", AgentHeadlessKnown: true}
	applyLaunchRecord(&s, &launchRecord{Agent: "byre/codex", AgentOverride: true}, project.Paths{})
	if s.AgentHeadlessKnown || s.AgentHeadless != "" {
		t.Errorf("headless survived an agent swap: %+v", s)
	}

	same := statusInfo{Agent: "byre/claude", AgentHeadless: "claude-headless", AgentHeadlessKnown: true}
	applyLaunchRecord(&same, &launchRecord{Agent: "byre/claude"}, project.Paths{})
	if !same.AgentHeadlessKnown || same.AgentHeadless != "claude-headless" {
		t.Errorf("headless dropped for the same agent: %+v", same)
	}
}

// skill inspect lists the headless command under the agent command, and an
// agent declaring none prints no headless line.
func TestSkillInspectShowsAgentHeadless(t *testing.T) {
	var f skills.File
	f.Agent = &skills.AgentContrib{Command: "agenty --go", Headless: "agenty-run --json"}
	var b strings.Builder
	printSkillContributions(&b, f)
	out := b.String()
	cmd, head := strings.Index(out, "agent command: agenty --go\n"), strings.Index(out, "agent headless: agenty-run --json\n")
	if cmd < 0 || head < cmd {
		t.Errorf("headless line must follow the agent command:\n%s", out)
	}

	f.Agent.Headless = ""
	b.Reset()
	printSkillContributions(&b, f)
	if strings.Contains(b.String(), "headless") {
		t.Errorf("no headless command, no headless line:\n%s", b.String())
	}
}

func TestStatusDataCarriesAgentHeadless(t *testing.T) {
	got := decodeStatusData(t, statusInfo{Agent: "claude", AgentHeadless: "claude-headless", AgentHeadlessKnown: true})
	if got["agent_headless"] != "claude-headless" {
		t.Errorf("agent_headless = %v", got["agent_headless"])
	}
	// Always written: "" is the agent declaring none, not a missing field.
	none := decodeStatusData(t, statusInfo{Agent: "claude", AgentHeadlessKnown: true})
	if v, ok := none["agent_headless"]; !ok || v != "" {
		t.Errorf("agent_headless without a command = %v (present %v), want \"\"", v, ok)
	}
}
