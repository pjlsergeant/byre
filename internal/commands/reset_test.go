package commands

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/project"
)

// liveProject marks a session live for the project label.
func liveProject(p project.Paths, ids ...string) map[string][]string {
	return map[string][]string{labelKey + "=" + p.ID: ids}
}

func TestResetForceWipesAll(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{vols: map[string]bool{volumeName(p.ID, ".claude"): true, volumeName(p.ID, "cache"): true}}
	s, _, _ := testStreams("", false)
	if err := reset(s, p, f, true); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 2 {
		t.Fatalf("force should remove all volumes, got %v", f.removed)
	}
}

func TestResetRefusesWhenLive(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{live: liveProject(p, "abcdef0123456789"), vols: map[string]bool{volumeName(p.ID, "cache"): true}}
	s, _, _ := testStreams("", false)
	if err := reset(s, p, f, true); err == nil {
		t.Fatal("expected refusal while a session is live")
	}
	if len(f.removed) != 0 {
		t.Fatal("must not remove volumes while live")
	}
}

func TestResetNoVolumes(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{}
	s, _, out := testStreams("", false)
	if err := reset(s, p, f, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no volumes to reset") {
		t.Errorf("expected 'no volumes' message: %s", out.String())
	}
}

func TestResetPromptAbortsOnNo(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{vols: map[string]bool{volumeName(p.ID, "cache"): true}}
	s, _, out := testStreams("n\n", false)
	if err := reset(s, p, f, false); err != nil {
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
	f := &fakeRunner{vols: map[string]bool{volumeName(p.ID, "cache"): true}, liveSecond: liveProject(p, "abcdef0123456789")}
	s, _, _ := testStreams("", false)
	if err := reset(s, p, f, true); err == nil {
		t.Fatal("expected abort when a session starts before deletion")
	}
	if len(f.removed) != 0 {
		t.Fatal("must not remove volumes if a session appeared")
	}
}

func TestResetPartialWipeReported(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{
		vols:       map[string]bool{volumeName(p.ID, "a"): true, volumeName(p.ID, "b"): true, volumeName(p.ID, "c"): true},
		failRemove: map[string]bool{volumeName(p.ID, "b"): true},
	}
	s, _, _ := testStreams("", false)
	err := reset(s, p, f, true)
	if err == nil {
		t.Fatal("expected error reporting partial wipe")
	}
	// It continues past the failure (a and c removed, b reported failed).
	if len(f.removed) != 2 {
		t.Fatalf("should continue past failure, removed=%v", f.removed)
	}
	if !strings.Contains(err.Error(), volumeName(p.ID, "b")) {
		t.Errorf("error should name the failed volume: %v", err)
	}
}

func TestResetPromptProceedsOnYes(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{vols: map[string]bool{volumeName(p.ID, "cache"): true}}
	s, _, _ := testStreams("y\n", false)
	if err := reset(s, p, f, false); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 1 {
		t.Fatalf("'y' should proceed, removed=%v", f.removed)
	}
}
