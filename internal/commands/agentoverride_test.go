package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolveWithAgent is the seam `develop --agent` rides on a configured
// project: the loaded config's agent is replaced BEFORE skill resolution and
// nothing is written. These tests assert the rule at that seam — the override
// resolves exactly like a written key, survives the under-lock re-read, and
// leaves byre.config's bytes untouched.

func writeAgentOverrideConfig(t *testing.T, storeDir, body string) string {
	t.Helper()
	cfgPath := filepath.Join(storeDir, "byre.config")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func resolvedSkillNames(rv resolved) []string {
	var names []string
	for _, sk := range rv.skills.Skills {
		names = append(names, sk.Name)
	}
	return names
}

func TestAgentOverrideResolvesLikeAWrittenKeyWithoutWriting(t *testing.T) {
	p, proj := testPaths(t)
	body := "agent = \"claude\"\n"
	cfgPath := writeAgentOverrideConfig(t, p.Dir, body)

	rv, err := resolveWithAgent(p, proj, nil, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if rv.cfg.Agent != "byre/codex" {
		t.Fatalf("cfg.Agent = %q, want the canonical override", rv.cfg.Agent)
	}
	if rv.agentOverride != "byre/codex" {
		t.Fatalf("agentOverride = %q, want the canonical override carried on the view", rv.agentOverride)
	}
	if rv.skills.Agent == nil || rv.skills.Agent.Command == "" {
		t.Fatal("the override's [agent] command must be resolved (the skill enabled implicitly)")
	}
	names := resolvedSkillNames(rv)
	if !contains(names, "byre/codex") {
		t.Fatalf("override skill must be in the resolved set, got %v", names)
	}
	// Replace semantics: the config's agent was only implicitly enabled, so
	// the override drops it from this run's box — the composition is exactly
	// what `agent = "codex"` would have produced.
	if contains(names, "byre/claude") {
		t.Fatalf("the config's implicitly-enabled agent must not ride along, got %v", names)
	}
	// No shared auth for a one-off agent: companions enter via the written
	// config only, and nothing was written.
	if contains(names, "byre/codex-shared-auth") {
		t.Fatalf("a shared-auth companion must never ride an override, got %v", names)
	}
	if got, err := os.ReadFile(cfgPath); err != nil || string(got) != body {
		t.Fatalf("byre.config must be byte-untouched, got %q (%v)", got, err)
	}

	// The under-lock re-read keeps the override: a save landing while develop
	// waits must not resurrect the config's agent for this launch.
	fresh, err := rv.refresh()
	if err != nil {
		t.Fatal(err)
	}
	if fresh.cfg.Agent != "byre/codex" || fresh.agentOverride != "byre/codex" {
		t.Fatalf("refresh dropped the override: agent=%q override=%q", fresh.cfg.Agent, fresh.agentOverride)
	}
}

func TestAgentOverrideNoneRunsAgentless(t *testing.T) {
	p, proj := testPaths(t)
	writeAgentOverrideConfig(t, p.Dir, "agent = \"claude\"\n")

	rv, err := resolveWithAgent(p, proj, nil, "none")
	if err != nil {
		t.Fatal(err)
	}
	if rv.cfg.Agent != "" || rv.skills.Agent != nil {
		t.Fatalf("--agent none must resolve agentless, got agent=%q", rv.cfg.Agent)
	}
	if rv.agentOverride != "none" {
		t.Fatalf("agentOverride = %q, want the sentinel kept on the view (an override happened)", rv.agentOverride)
	}
}

func TestAgentOverrideUnknownSkillFailsNamingIt(t *testing.T) {
	p, proj := testPaths(t)
	writeAgentOverrideConfig(t, p.Dir, "agent = \"claude\"\n")

	_, err := resolveWithAgent(p, proj, nil, "no-such-agent")
	if err == nil || !strings.Contains(err.Error(), "no-such-agent") {
		t.Fatalf("an unknown override must fail naming the skill, got: %v", err)
	}
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestAgentOverrideBlankValueIsRejected(t *testing.T) {
	p, proj := testPaths(t)
	writeAgentOverrideConfig(t, p.Dir, "agent = \"claude\"\n")

	// A blank flag must not canonicalize into an unmarked agentless run —
	// the deliberate spelling for agentless is the "none" sentinel.
	_, err := resolveWithAgent(p, proj, nil, "   ")
	if err == nil || !strings.Contains(err.Error(), "--agent: blank value") {
		t.Fatalf("a blank override must be rejected naming the rule, got: %v", err)
	}
}
