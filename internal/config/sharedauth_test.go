package config

import "testing"

// Installed agents have qualified owner/name IDs; '/' is illegal in a bare
// TOML key, so the encoder must quote pick keys or the emitted value is
// unparsable and every save for a third-party agent fails its semantic
// verify.
func TestEncodeTOMLValueQuotesQualifiedPickKeys(t *testing.T) {
	pref := SharedAuthPref{Pick: map[string]string{
		"acme/agent": "acme/agent-shared-auth",
		"claude":     "claude-shared-auth",
	}}
	line := "shared_auth = " + pref.EncodeTOMLValue()
	cfg, err := Parse([]byte(line + "\n"))
	if err != nil {
		t.Fatalf("emitted line %q must parse: %v", line, err)
	}
	if got := cfg.StoredSharedAuth().CompanionPick("acme/agent"); got != "acme/agent-shared-auth" {
		t.Fatalf("acme/agent pick round-trip: got %q", got)
	}
	if got := cfg.StoredSharedAuth().CompanionPick("claude"); got != "claude-shared-auth" {
		t.Fatalf("claude pick round-trip: got %q", got)
	}
}

// [defaults] is picker state and must never reach a box: the whole section
// is stripped by resolution, so no member of it can acquire teeth by
// accident — the structural version of the per-key strip shared_auth used
// to need.
func TestDefaultsSectionIsStrippedFromResolvedConfigs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	proj := Config{
		Defaults: Defaults{
			SharedAuth:    SharedAuthPref{Pick: map[string]string{"claude": "claude-shared-auth"}},
			SkipQuestions: true,
		},
		SharedAuthLegacy: SharedAuthPref{Yes: []string{"codex"}},
	}
	got, err := ResolveProposed(proj)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Defaults.Empty() {
		t.Errorf("[defaults] must not survive resolution: %+v", got.Defaults)
	}
	if !got.SharedAuthLegacy.Empty() {
		t.Errorf("the legacy spelling must not survive resolution either: %+v", got.SharedAuthLegacy)
	}
}

// One accessor, two homes: the section wins, the pre-2026-07-28 top-level
// spelling is the fallback so an upgrade keeps working.
func TestStoredSharedAuthPrefersTheSectionOverTheLegacySpelling(t *testing.T) {
	both := Config{
		Defaults:         Defaults{SharedAuth: SharedAuthPref{Pick: map[string]string{"claude": "new"}}},
		SharedAuthLegacy: SharedAuthPref{Pick: map[string]string{"claude": "old"}},
	}
	if got := both.StoredSharedAuth().CompanionPick("claude"); got != "new" {
		t.Errorf("the section must win, got %q", got)
	}
	legacyOnly := Config{SharedAuthLegacy: SharedAuthPref{Pick: map[string]string{"claude": "old"}}}
	if got := legacyOnly.StoredSharedAuth().CompanionPick("claude"); got != "old" {
		t.Errorf("the legacy spelling must still be read, got %q", got)
	}
}

// A config can carry BOTH homes -- hand-edited, or mid-migration. Picking
// one wholesale drops the other's agents, and the next write then clones the
// winner and deletes the loser, losing a preference the user set. Union per
// agent; canonical wins only where they actually collide.
func TestStoredSharedAuthUnionsBothHomes(t *testing.T) {
	both := Config{
		SharedAuthLegacy: SharedAuthPref{Yes: []string{"codex"}, Pick: map[string]string{"gemini": "old-companion"}},
		Defaults: Defaults{SharedAuth: SharedAuthPref{
			Pick: map[string]string{"claude": "claude-shared-auth", "gemini": "new-companion"},
		}},
	}
	got := both.StoredSharedAuth()
	if got.CompanionPick("claude") != "claude-shared-auth" {
		t.Errorf("the canonical home's agent must survive: %+v", got)
	}
	if got.CompanionPick("gemini") != "new-companion" {
		t.Errorf("canonical must win a genuine collision: %+v", got)
	}
	if !got.HasYes("codex") {
		t.Errorf("an agent only the legacy home knows must NOT be lost: %+v", got)
	}
}
