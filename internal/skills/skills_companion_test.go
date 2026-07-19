package skills

// Companion skills and the shared-auth pairing: declarations on the
// bundled skills, auto-composition, and ambiguity refusals.

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/packages"
)

// SharedAuthCompanion maps an agent to the skill VOUCHING itself ready as
// that agent's shared-auth companion (shared_auth_for). No declaration — a
// broken or gate-pending companion — means no onboarding offer.
func TestSharedAuthCompanion(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "claude", "[agent]\ncommand = \"claude\"\nstate = \"s\"\n\n[[volumes]]\nname = \"s\"\nrole = \"state\"\ntarget = \"/home/dev/.claude\"\n", nil)
	writeSkill(t, dir, "claude-shared-auth", "shared_auth_for = \"claude\"\n", nil)
	writeSkill(t, dir, "grok-shared-auth", "description = \"RETIRED — no shared_auth_for, so never offered\"\n", nil)

	if got := SharedAuthCompanion(catFor(t, dir), "claude"); got != "claude-shared-auth" {
		t.Fatalf("SharedAuthCompanion(claude) = %q, want claude-shared-auth", got)
	}
	if got := SharedAuthCompanion(catFor(t, dir), "grok"); got != "" {
		t.Fatalf("an undeclared companion must not be offered, got %q", got)
	}
	if got := SharedAuthCompanion(catFor(t, dir), ""); got != "" {
		t.Fatalf("no agent, no companion, got %q", got)
	}
}

// The builtin declarations are load-bearing: claude/codex/opencode offer at
// onboarding (opencode vouched 2026-07-17); gemini (two-box OAuth gate
// pending) and grok (broker rollover gate pending, ADR 0036) deliberately
// must NOT.
func TestBuiltinSharedAuthDeclarations(t *testing.T) {
	home := t.TempDir()
	cat, err := packages.LoadCatalog(home, builtins.FS(), "0.2.0", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	for agent, want := range map[string]string{
		"claude":   "claude-shared-auth",
		"codex":    "codex-shared-auth",
		"gemini":   "",                     // two-box OAuth field gate pending (companion_for only, no shared_auth_for vouch)
		"grok":     "",                     // ~6h broker-rollover field gate pending (companion_for only; ADR 0036)
		"opencode": "opencode-shared-auth", // vouched 2026-07-17: two-box API-key gate passed live (TestOpencodeSharedAuthLiveGate)
	} {
		if got := SharedAuthCompanion(cat, agent); got != want {
			t.Errorf("SharedAuthCompanion(%s) = %q, want %q", agent, got, want)
		}
	}
}

// The companion PAIRING (ADR 0034) is a fact every live companion declares —
// via companion_for when gate-pending, or implied by shared_auth_for once
// vouched — and is what the config UI's nesting rides. Distinct from the
// vouch table above: gemini and grok pair here while offering nothing there
// (each's shared_auth_for vouch waits on its field gate); claude, codex and
// opencode pair through their vouch.
func TestBuiltinCompanionDeclarations(t *testing.T) {
	home := t.TempDir()
	cat, err := packages.LoadCatalog(home, builtins.FS(), "0.2.0", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	for skill, want := range map[string]string{
		"claude-shared-auth":   "claude",
		"codex-shared-auth":    "codex",
		"gemini-shared-auth":   "gemini",
		"opencode-shared-auth": "opencode",
		"grok-shared-auth":     "grok",
	} {
		sk, err := Load(cat, skill)
		if err != nil {
			t.Errorf("Load(%s): %v", skill, err)
			continue
		}
		if got := sk.File.CompanionAgent(); got != want {
			t.Errorf("%s CompanionAgent() = %q, want %q", skill, got, want)
		}
	}
}

// The pairing is declared exactly once — companion_for or shared_auth_for
// (which subsumes it), never both. Refusing coexistence outright (rather
// than comparing values) means two spellings of one fact can't drift, and
// sidesteps the alias-vs-canonical-ID comparison a value check would need
// (parse-time has no catalog — external review finding, 2026-07-16).
func TestCompanionForSharedAuthForBothSetRefused(t *testing.T) {
	dir := testHome(t)
	// Matching values are just as refused as mismatched ones: the redundancy
	// itself is the error, so alias-vs-canonical spelling never matters.
	for name, toml := range map[string]string{
		"confused-auth":  "companion_for = \"gemini\"\nshared_auth_for = \"claude\"\n",
		"redundant-auth": "companion_for = \"claude\"\nshared_auth_for = \"claude\"\n",
	} {
		writeSkill(t, dir, name, toml, nil)
		if _, err := Load(catFor(t, dir), name); err == nil || !strings.Contains(err.Error(), "both set") {
			t.Errorf("%s: both pairing keys must refuse to load, got err=%v", name, err)
		}
	}
	// Install preflight (ParsePrimaryBytes) refuses the same shape — a
	// package must not pass ingest checks only to be unloadable after.
	if _, err := ParsePrimaryBytes([]byte("companion_for = \"claude\"\nshared_auth_for = \"claude\"\n")); err == nil || !strings.Contains(err.Error(), "both set") {
		t.Fatalf("ParsePrimaryBytes must refuse both pairing keys, got err=%v", err)
	}
}

// Two skills claiming the same agent is refused (no offer), not resolved by
// sort order — a hand-dropped near-namesake must not shadow the builtin.
func TestSharedAuthCompanionRefusesAmbiguity(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "aa-auth", "shared_auth_for = \"claude\"\n", nil)
	writeSkill(t, dir, "claude-shared-auth", "shared_auth_for = \"claude\"\n", nil)
	if got := SharedAuthCompanion(catFor(t, dir), "claude"); got != "" {
		t.Fatalf("two declarers must yield no companion, got %q", got)
	}
}
