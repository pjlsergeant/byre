package config

import (
	"reflect"
	"testing"
)

// The saveable projection: complete picks survive; yes-inclinations and
// empty-valued picks — the same yes-without-companion half-answer in two
// spellings — are both dropped (ADR 0049 amendment 2026-08-23).
func TestSaveableDropsBothHalfAnswerSpellings(t *testing.T) {
	got := SharedAuthPref{
		Yes:  []string{"grok"},
		Pick: map[string]string{"claude": "claude-shared-auth", "codex": ""},
	}.Saveable()
	want := SharedAuthPref{Pick: map[string]string{"claude": "claude-shared-auth"}}
	if !got.Equal(want) {
		t.Fatalf("Saveable = %+v, want %+v", got, want)
	}
	if s := (SharedAuthPref{Pick: map[string]string{"codex": ""}}).Saveable(); !s.Empty() {
		t.Fatalf("an all-empty-picks preference must project to empty: %+v", s)
	}
}

// Incomplete names the half-answer agents across both spellings, sorted.
func TestIncompleteNamesBothSpellings(t *testing.T) {
	got := SharedAuthPref{
		Yes:  []string{"grok"},
		Pick: map[string]string{"claude": "claude-shared-auth", "codex": ""},
	}.Incomplete()
	if want := []string{"codex", "grok"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Incomplete = %v, want %v", got, want)
	}
}

// Canonical wins per agent in BOTH directions (the union's documented rule):
// a canonical yes-inclination replaces a LEGACY pick for the same agent, so
// the half-answer stays visible to Incomplete(), the compat warning, and the
// writers' cleanup — a legacy companion must never stand over (or be
// resurrected in place of) the canonical answer.
func TestStoredSharedAuthCanonicalYesBeatsLegacyPick(t *testing.T) {
	cfg, err := Parse([]byte(
		"[shared_auth]\nclaude = \"old-shared-auth\"\n\n[defaults]\nshared_auth = [\"claude\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.StoredSharedAuth()
	if got.CompanionPick("claude") != "" {
		t.Fatalf("the legacy pick must lose to the canonical yes: %+v", got)
	}
	if inc := got.Incomplete(); len(inc) != 1 || inc[0] != "claude" {
		t.Fatalf("the canonical half-answer must be reported incomplete: %v", inc)
	}
	warns := LayerWarnings("default", "", cfg)
	kinds := map[string]bool{}
	for _, w := range warns {
		kinds[w.Kind] = true
	}
	if !kinds[WarnSharedAuthArray] || !kinds[WarnSharedAuthTopLevel] {
		t.Fatalf("both legacy facts must warn: %+v", warns)
	}
	if s := got.Saveable(); !s.Empty() {
		t.Fatalf("nothing saveable survives — the legacy pick must not be resurrected: %+v", s)
	}
}
