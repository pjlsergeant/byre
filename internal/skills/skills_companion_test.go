package skills

// Companion skills and the shared-auth pairing: declarations on the
// bundled skills, auto-composition, and ambiguity refusals.

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/packages"
	"io/fs"
	"os"
)

// SharedAuthCompanion maps an agent to the skill VOUCHING itself ready as
// that agent's shared-auth companion (shared_auth_for). No declaration — a
// broken or gate-pending companion — means no onboarding offer.
func TestSharedAuthCompanion(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "claude", "[agent]\ncommand = \"claude\"\nstate = \"s\"\n\n[[volumes]]\nname = \"s\"\nrole = \"state\"\ntarget = \"/home/dev/.claude\"\n", nil)
	writeSkill(t, dir, "claude-shared-auth", "shared_auth_for = \"claude\"\n", nil)
	writeSkill(t, dir, "grok-shared-auth", "description = \"RETIRED — no shared_auth_for, so never offered\"\n", nil)

	if got, n := SharedAuthCompanion(catFor(t, dir), "claude"); got != "claude-shared-auth" || n != 1 {
		t.Fatalf("SharedAuthCompanion(claude) = %q (%d claimants), want claude-shared-auth (1)", got, n)
	}
	// No claimant and several claimants are DIFFERENT answers: the count is
	// what keeps a caller from reporting an ambiguity as an absence.
	if got, n := SharedAuthCompanion(catFor(t, dir), "grok"); got != "" || n != 0 {
		t.Fatalf("an undeclared companion must not be offered, got %q (%d claimants)", got, n)
	}
	if got, n := SharedAuthCompanion(catFor(t, dir), ""); got != "" || n != 0 {
		t.Fatalf("no agent, no companion, got %q (%d claimants)", got, n)
	}
}

// The builtin declarations are load-bearing: claude/codex/opencode offer at
// onboarding (opencode vouched 2026-07-17); gemini (two-box OAuth gate
// pending) and grok (broker rollover gate pending, ADR 0036) deliberately
// must NOT.
func TestBuiltinSharedAuthDeclarations(t *testing.T) {
	home := t.TempDir()
	cat, err := packages.LoadCatalog(home, bundledSrcFS(t), "0.2.0", "0.2.0", packages.Stage2Hooks{Skill: ValidatePrimaryBytes})
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
		if got, _ := SharedAuthCompanion(cat, agent); got != want {
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
	cat, err := packages.LoadCatalog(home, bundledSrcFS(t), "0.2.0", "0.2.0", packages.Stage2Hooks{Skill: ValidatePrimaryBytes})
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
// (parse-time has no catalog).
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
	got, n := SharedAuthCompanion(catFor(t, dir), "claude")
	if got != "" {
		t.Fatalf("two declarers must yield no companion, got %q", got)
	}
	// ...and the caller must be able to tell that apart from nobody claiming,
	// or the remedy it prints sends the user to install what they already have.
	if n != 2 {
		t.Fatalf("claimant count = %d, want 2", n)
	}
}

// bundledSrcFS serves the bundled packages straight from the source tree —
// the same skills/ and templates/ dirs builtins embeds. skills tests cannot
// import builtins (builtins imports skills since the stage-2 hooks became
// explicit), and these tests pin the CONTENT, which is identical.
func bundledSrcFS(t *testing.T) fs.FS {
	t.Helper()
	return os.DirFS("../builtins")
}

// The stored-pick predicate is shared by three surfaces (the offer, the
// skip_questions apply path, the config editor's row), so it is pinned here
// rather than at each of them: two implementations would drift into a grant
// one surface allows and another flags.
func TestSharedAuthPickLive(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "claude", "[agent]\ncommand = \"claude\"\n", nil)
	writeSkill(t, dir, "claude-shared-auth", "shared_auth_for = \"claude\"\n", nil)
	writeSkill(t, dir, "opencode-shared-auth", "shared_auth_for = \"opencode\"\n", nil)
	cat := catFor(t, dir)

	if !SharedAuthPickLive(cat, "claude", "claude-shared-auth") {
		t.Error("an installed claimant must read live")
	}
	// A name nothing claims: uninstalled, renamed, or never real.
	if SharedAuthPickLive(cat, "claude", "gone-shared-auth") {
		t.Error("a pick no skill claims must not read live")
	}
	// A companion that claims a DIFFERENT agent does not answer for this one --
	// the case where a name survives but the pairing it stood for did not.
	if SharedAuthPickLive(cat, "claude", "opencode-shared-auth") {
		t.Error("a claimant of another agent must not satisfy this agent's pick")
	}
	for _, tc := range []struct{ agent, pick string }{
		{"", "claude-shared-auth"}, {"claude", ""}, {"claude", "claude"},
	} {
		if SharedAuthPickLive(cat, tc.agent, tc.pick) {
			t.Errorf("(%q, %q) must not read live", tc.agent, tc.pick)
		}
	}
	if SharedAuthPickLive(nil, "claude", "claude-shared-auth") {
		t.Error("no catalog is no evidence of a live claimant")
	}
}

// The spelling rule liveness and prefill share: a pick stored as the canonical
// id and a row displaying the alias are one package, and a byte comparison
// would call them two -- which is how a pick read live and selected nothing.
func TestSameSkillRef(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "local-auth", "shared_auth_for = \"claude\"\n", nil)
	cat := catFor(t, dir)

	if !SameSkillRef(cat, "local-auth", "local-auth") {
		t.Error("identical names must match")
	}
	if SameSkillRef(cat, "local-auth", "other-auth") {
		t.Error("different names must not match")
	}
	for _, tc := range []struct{ a, b string }{{"", "x"}, {"x", ""}, {"", ""}} {
		if SameSkillRef(cat, tc.a, tc.b) {
			t.Errorf("(%q, %q) must not match", tc.a, tc.b)
		}
	}
	// Without a catalog there is no alias table, so only byte equality holds.
	if !SameSkillRef(nil, "x", "x") || SameSkillRef(nil, "byre/x", "x") {
		t.Error("no catalog means no alias expansion, and identity still matches")
	}

	// The case the rule exists for: a bundled skill's canonical id against the
	// bare alias every surface displays it under.
	bcat, err := packages.LoadCatalog(t.TempDir(), bundledSrcFS(t), "0.2.0", "0.2.0", packages.Stage2Hooks{Skill: ValidatePrimaryBytes})
	if err != nil {
		t.Fatal(err)
	}
	if !SameSkillRef(bcat, "byre/claude-shared-auth", "claude-shared-auth") {
		t.Error("a canonical id and its bare alias name one package")
	}
	if !SameSkillRef(bcat, "claude-shared-auth", "byre/claude-shared-auth") {
		t.Error("the rule must hold in both directions")
	}
	if SameSkillRef(bcat, "byre/claude-shared-auth", "byre/codex-shared-auth") {
		t.Error("two different bundled skills must not match")
	}
}
