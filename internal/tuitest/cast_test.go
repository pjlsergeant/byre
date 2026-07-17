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

func TestTrimCastTailCutsAtTheFirstMatch(t *testing.T) {
	// After the intended final frame the terminal may keep moving — the
	// field case: an error line, then a full-screen repaint painting the
	// sentinel AGAIN above the error. Everything after the FIRST paint is
	// the footage the trim exists to drop.
	raw := mkCast(
		`[0.1,"o","done"]`,
		`[0.2,"o","unwanted error"]`,
		`[0.3,"o","repaint: done above unwanted error"]`,
		`[0.4,"o","[server exited]"]`,
	)
	got, err := trimCastTail(raw, "done")
	if err != nil {
		t.Fatal(err)
	}
	for _, dropped := range []string{"unwanted error", "repaint", "server exited"} {
		if strings.Contains(got, dropped) {
			t.Fatalf("footage after the first sentinel paint survived (%q):\n%s", dropped, got)
		}
	}
}

func TestTrimCastTailMissingSentinelIsLoud(t *testing.T) {
	raw := mkCast(`[0.1,"o","hello"]`)
	if _, err := trimCastTail(raw, "absent"); err == nil {
		t.Fatal("a sentinel appearing in no event must error, not ship the untrimmed cast")
	}
}

func TestTrimCastTailCutsInsideACoalescedEvent(t *testing.T) {
	// The field failure: a pty read coalesced the sentinel's line and the
	// next line into ONE event, so event-granular trimming shipped the
	// engine-boundary error inside the kept event. The trim must cut at the
	// sentinel line's end within the event.
	raw := mkCast(
		`[0.1,"o","asking questions"]`,
		`[0.2,"o","byre: wrote config (skills=x)[K\r\nbyre: no container engine found\r\n"]`,
	)
	got, err := trimCastTail(raw, "skills=x")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "no container engine") {
		t.Fatalf("text after the sentinel's line survived inside the kept event:\n%s", got)
	}
	if !strings.Contains(got, `(skills=x)[K\r\n`) {
		t.Fatalf("the sentinel's own line ending was lost:\n%s", got)
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

func TestAssembleDemoShipsSanitizedHeader(t *testing.T) {
	// The regression the review caught: sanitizing the header and then
	// writing the ORIGINAL joined cast — the published artifact must carry
	// the sanitized header, not just compute it.
	recorded := `{"version":3,"term":{"cols":100,"rows":30},"idle_time_limit":2.0,"command":"tmux -L sock attach -t main","env":{"SHELL":"/bin/bash"}}` + "\n" +
		`[0.1,"o","frame"]` + "\n"
	cast, meta, err := assembleDemo([]string{recorded})
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"tmux", "SHELL"} {
		if strings.Contains(cast, leaked) {
			t.Fatalf("published cast leaks recorder metadata (%q):\n%s", leaked, cast)
		}
	}
	if !strings.Contains(cast, `"cols":100`) || !strings.Contains(cast, "frame") {
		t.Fatalf("assembly lost geometry or events:\n%s", cast)
	}
	if !strings.Contains(meta, `"duration":0.1`) {
		t.Fatalf("meta = %q", meta)
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
