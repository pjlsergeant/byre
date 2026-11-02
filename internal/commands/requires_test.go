package commands

import (
	"strings"
	"testing"
)

// `develop -a` resolves exactly as a written agent key would, so a skill's
// requires is judged against the OVERRIDE's set: replace semantics drop the
// config's implicitly-enabled agent, and a skill that required it refuses
// rather than running without it. Nothing is enabled to satisfy it.
func TestRequiresUnderAgentOverride(t *testing.T) {
	p, proj := testPaths(t)
	writeLocalSkill(t, p.Home, "wrapper", "requires = [\"codex\"]\n")
	writeAgentOverrideConfig(t, p.Dir, "agent = \"codex\"\nskills = [\"wrapper\"]\n")

	if _, err := resolveWithAgent(p, proj, nil, ""); err != nil {
		t.Fatalf("the written config enables codex as its agent: %v", err)
	}
	_, err := resolveWithAgent(p, proj, nil, "claude")
	if err == nil || !strings.Contains(err.Error(), `"wrapper" requires "codex"`) || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("an override that drops the required agent must refuse naming the requirement, got: %v", err)
	}

	writeAgentOverrideConfig(t, p.Dir, "agent = \"claude\"\nskills = [\"wrapper\"]\n")
	rv, err := resolveWithAgent(p, proj, nil, "codex")
	if err != nil {
		t.Fatalf("an override naming the required skill meets the requirement: %v", err)
	}
	if names := resolvedSkillNames(rv); !contains(names, "byre/codex") || contains(names, "byre/claude") {
		t.Fatalf("resolved %v, want the override's set", names)
	}
}
