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
