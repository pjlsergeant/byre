package tuitest

// The cast surgery is pure string→string, so it gets ordinary ungated unit
// tests: the demo GATE needs tmux+asciinema, the trim/concat logic does not.

import (
	"strings"
	"testing"
)

const testHeader = `{"version":3,"term":{"cols":100,"rows":30}}`

func mkCast(events ...string) string {
	return testHeader + "\n" + strings.Join(events, "\n") + "\n"
}

func TestTrimCastTailDropsAfterSentinel(t *testing.T) {
	raw := mkCast(
		`[0.1,"o","hello"]`,
		`[0.2,"o","byre: config unchanged."]`,
		`[0.3,"o","[server exited]"]`,
	)
	got, err := trimCastTail(raw, "config unchanged")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "server exited") {
		t.Fatalf("server-exited frame survived the trim:\n%s", got)
	}
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), `[0.2,"o","byre: config unchanged."]`) {
		t.Fatalf("trim did not end on the sentinel event:\n%s", got)
	}
}

func TestTrimCastTailKeepsTheLastMatch(t *testing.T) {
	// The sentinel painting twice (a repaint) must keep everything up to the
	// LAST occurrence, not cut at the first.
	raw := mkCast(
		`[0.1,"o","done"]`,
		`[0.2,"o","more"]`,
		`[0.3,"o","done"]`,
		`[0.4,"o","[server exited]"]`,
	)
	got, err := trimCastTail(raw, "done")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "more") {
		t.Fatalf("trim cut at the first match:\n%s", got)
	}
	if strings.Contains(got, "server exited") {
		t.Fatalf("tail survived:\n%s", got)
	}
}

func TestTrimCastTailMissingSentinelIsLoud(t *testing.T) {
	raw := mkCast(`[0.1,"o","hello"]`)
	if _, err := trimCastTail(raw, "absent"); err == nil {
		t.Fatal("a sentinel appearing in no event must error, not ship the untrimmed cast")
	}
}

func TestTrimCastTailIgnoresInputEvents(t *testing.T) {
	// The sentinel in an input ("i") event is typed text, not a painted
	// frame; only output events count.
	raw := mkCast(
		`[0.1,"o","screen"]`,
		`[0.2,"i","screen"]`,
	)
	got, err := trimCastTail(raw, "screen")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, `"i"`) {
		t.Fatalf("trim kept events past the last matching OUTPUT event:\n%s", got)
	}
}

func TestConcatCastsInsertsSceneBreak(t *testing.T) {
	a := mkCast(`[0.1,"o","scene one"]`)
	b := mkCast(`[0.2,"o","scene two"]`)
	got, err := concatCasts(1.5, a, b)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 4 { // header + one + break + two
		t.Fatalf("got %d lines, want 4:\n%s", len(lines), got)
	}
	if !strings.Contains(lines[2], "1.5") || !strings.Contains(lines[2], `[2J`) {
		t.Fatalf("scene break missing or unpaused: %s", lines[2])
	}
	if !strings.Contains(lines[3], "scene two") {
		t.Fatalf("second scene lost: %s", lines[3])
	}
}

func TestConcatCastsRefusesGeometryMismatch(t *testing.T) {
	a := mkCast(`[0.1,"o","x"]`)
	b := `{"version":3,"term":{"cols":80,"rows":24}}` + "\n" + `[0.1,"o","y"]` + "\n"
	if _, err := concatCasts(1, a, b); err == nil {
		t.Fatal("scenes with different geometry must refuse to concat")
	}
}

func TestSanitizeHeaderKeepsOnlyPlayerFields(t *testing.T) {
	in := `{"version":3,"term":{"cols":100,"rows":30},"timestamp":123,"idle_time_limit":2.0,"command":"tmux -L secret attach","env":{"SHELL":"/bin/bash"}}`
	got, err := sanitizeHeader(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"tmux", "SHELL", "timestamp"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("sanitized header still carries %q: %s", leaked, got)
		}
	}
	cols, rows, err := castGeometry(got)
	if err != nil || cols != 100 || rows != 30 {
		t.Fatalf("geometry lost in sanitize: %dx%d, %v", cols, rows, err)
	}
	if !strings.Contains(got, "idle_time_limit") {
		t.Fatalf("idle cap lost: %s", got)
	}
}

func TestCastDuration(t *testing.T) {
	_, events, err := parseCast(mkCast(`[0.5,"o","a"]`, `[1.25,"o","b"]`))
	if err != nil {
		t.Fatal(err)
	}
	if d := castDuration(events); d != 1.75 {
		t.Fatalf("duration = %v, want 1.75", d)
	}
}
