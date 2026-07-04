package commands

import (
	"bytes"
	"strings"
	"testing"

	"byre/internal/project"
)

// liveFamily marks a session live for the project's family label.
func liveFamily(p project.Paths, ids ...string) map[string][]string {
	return map[string][]string{labelKey + "=" + p.ID: ids}
}

func TestResetForceWipesAll(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{vols: map[string]bool{VolumeName(p.ID, ".claude"): true, VolumeName(p.ID, "cache"): true}}
	var out bytes.Buffer
	if err := reset(&out, strings.NewReader(""), p, f, true); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 2 {
		t.Fatalf("force should remove all volumes, got %v", f.removed)
	}
}

func TestResetRefusesWhenLive(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{live: liveFamily(p, "abcdef0123456789"), vols: map[string]bool{VolumeName(p.ID, "cache"): true}}
	if err := reset(&bytes.Buffer{}, strings.NewReader(""), p, f, true); err == nil {
		t.Fatal("expected refusal while a session is live")
	}
	if len(f.removed) != 0 {
		t.Fatal("must not remove volumes while live")
	}
}

func TestResetNoVolumes(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{}
	var out bytes.Buffer
	if err := reset(&out, strings.NewReader(""), p, f, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no volumes to reset") {
		t.Errorf("expected 'no volumes' message: %s", out.String())
	}
}

func TestResetPromptAbortsOnNo(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{vols: map[string]bool{VolumeName(p.ID, "cache"): true}}
	var out bytes.Buffer
	if err := reset(&out, strings.NewReader("n\n"), p, f, false); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 0 {
		t.Fatal("'n' must abort without removing")
	}
	if !strings.Contains(out.String(), "aborted") {
		t.Errorf("expected abort message: %s", out.String())
	}
}

func TestResetRechecksLiveUnderLock(t *testing.T) {
	p, _ := testPaths(t)
	// Not live at the first check, but a session appears by the re-check.
	f := &fakeRunner{vols: map[string]bool{VolumeName(p.ID, "cache"): true}, liveSecond: liveFamily(p, "abcdef0123456789")}
	var out bytes.Buffer
	if err := reset(&out, strings.NewReader(""), p, f, true); err == nil {
		t.Fatal("expected abort when a session starts before deletion")
	}
	if len(f.removed) != 0 {
		t.Fatal("must not remove volumes if a session appeared")
	}
}

func TestResetPartialWipeReported(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{
		vols:       map[string]bool{VolumeName(p.ID, "a"): true, VolumeName(p.ID, "b"): true, VolumeName(p.ID, "c"): true},
		failRemove: map[string]bool{VolumeName(p.ID, "b"): true},
	}
	var out bytes.Buffer
	err := reset(&out, strings.NewReader(""), p, f, true)
	if err == nil {
		t.Fatal("expected error reporting partial wipe")
	}
	// It continues past the failure (a and c removed, b reported failed).
	if len(f.removed) != 2 {
		t.Fatalf("should continue past failure, removed=%v", f.removed)
	}
	if !strings.Contains(err.Error(), VolumeName(p.ID, "b")) {
		t.Errorf("error should name the failed volume: %v", err)
	}
}

func TestResetPromptProceedsOnYes(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{vols: map[string]bool{VolumeName(p.ID, "cache"): true}}
	var out bytes.Buffer
	if err := reset(&out, strings.NewReader("y\n"), p, f, false); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 1 {
		t.Fatalf("'y' should proceed, removed=%v", f.removed)
	}
}
