package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/hostopen"
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

// The worktree handoff (developCommand with mayEnroll=false) forwards --agent
// as the run-scoped override ONLY. On a project with no byre.config the flag
// would otherwise fall through to onboarding and durably configure the whole
// repo — refused by name instead, before any onboarding question, and the
// store stays config-less.
func TestWorktreeHandoffAgentRefusesUnconfiguredProject(t *testing.T) {
	p, proj := testPaths(t)

	err := developCommand(discardStreams(), proj, "", "codex", nil, false, CredentialAsk, false)
	if err == nil || !strings.Contains(err.Error(), "cannot be a run-scoped override") {
		t.Fatalf("an unconfigured handoff with --agent must refuse naming the rule, got: %v", err)
	}
	if ok, perr := hostopen.ExistsNoFollow(filepath.Join(p.Dir, "byre.config")); perr != nil || ok {
		t.Fatalf("no config may be written by the refusal (exists=%v, err=%v)", ok, perr)
	}
}

// The refusal's sibling: a CONFIGURED handoff with --agent must get PAST the
// guard and onboarding and take the override path — an over-broad guard
// (refuse whenever the handoff carries the flag) would pass the refusal test
// above and kill the feature. In this environment develop then dies at engine
// detection, which is exactly the evidence wanted: it advanced beyond the
// config-existence guard and the already-configured onboarding check, and the
// config's bytes were never touched.
func TestWorktreeHandoffAgentProceedsOnConfiguredProject(t *testing.T) {
	p, proj := testPaths(t)
	body := "agent = \"claude\"\n"
	cfgPath := writeAgentOverrideConfig(t, p.Dir, body)
	t.Setenv("PATH", t.TempDir()) // no engine: develop stops right after the override seam
	// The resolver pins per PROCESS; a `docker` pinned by an earlier test in
	// this binary would answer past the PATH this test just narrowed. Drop the
	// pins on the way in and on the way out, so this test's not-found pin
	// cannot poison a later one either.
	hostexec.ResetPins()
	t.Cleanup(hostexec.ResetPins)

	err := developCommand(discardStreams(), proj, "", "codex", nil, false, CredentialAsk, false)
	if err == nil || !strings.Contains(err.Error(), "no container engine found") {
		t.Fatalf("a configured handoff must carry the override into develop (and here die at engine detection), got: %v", err)
	}
	for _, refusal := range []string{"cannot be a run-scoped override", "already configured"} {
		if strings.Contains(err.Error(), refusal) {
			t.Fatalf("the guard misfired on a configured project: %v", err)
		}
	}
	if got, rerr := os.ReadFile(cfgPath); rerr != nil || string(got) != body {
		t.Fatalf("byre.config must be byte-untouched, got %q (%v)", got, rerr)
	}
}
