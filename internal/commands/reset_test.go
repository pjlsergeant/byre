package commands

import (
	"os"
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
	if err := reset(s, p, engines(f), true); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 2 {
		t.Fatalf("force should remove all volumes, got %v", f.removed)
	}
}

// reset must leave machine-scoped volumes alone AND say so, naming the
// deliberate route (ADR 0017: the machine-wide agent login never dies as a
// side effect of resetting one project).
func TestResetSparesAndNamesMachineVolumes(t *testing.T) {
	p, _ := testPaths(t)
	machineVol := machineVolumeName(os.Getuid(), "claude-identity")
	f := &fakeRunner{vols: map[string]bool{
		volumeName(p.ID, ".claude"): true,
		machineVol:                  true,
	}}
	s, _, out := testStreams("", false)
	if err := reset(s, p, engines(f), true); err != nil {
		t.Fatal(err)
	}
	for _, rm := range f.removed {
		if rm == machineVol {
			t.Fatalf("reset removed a machine-scoped volume: %v", f.removed)
		}
	}
	if !strings.Contains(out.String(), "NOT touched") || !strings.Contains(out.String(), machineVol) {
		t.Errorf("reset should name the spared machine volume:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "byre config") {
		t.Errorf("reset should point at the deliberate-delete route:\n%s", out.String())
	}
}

func TestResetRefusesWhenLive(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{live: liveProject(p, "abcdef0123456789"), vols: map[string]bool{volumeName(p.ID, "cache"): true}}
	s, _, _ := testStreams("", false)
	if err := reset(s, p, engines(f), true); err == nil {
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
	if err := reset(s, p, engines(f), true); err != nil {
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
	if err := reset(s, p, engines(f), false); err != nil {
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
	if err := reset(s, p, engines(f), true); err == nil {
		t.Fatal("expected abort when a session starts before deletion")
	}
	if len(f.removed) != 0 {
		t.Fatal("must not remove volumes if a session appeared")
	}
}

// A develop between its container create (under the lock) and start leaves a
// created-but-not-started container: reset must remove that ownership marker
// (making the develop's start fail loudly) before wiping volumes.
func TestResetRemovesPreStartMarker(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{
		vols:          map[string]bool{volumeName(p.ID, "cache"): true},
		allContainers: map[string][]string{labelKey + "=" + p.ID: {"feed0000beef"}},
	}
	s, _, _ := testStreams("", false)
	if err := reset(s, p, engines(f), true); err != nil {
		t.Fatal(err)
	}
	if len(f.rmContainers) != 1 || f.rmContainers[0] != "feed0000beef" {
		t.Fatalf("pre-start marker not removed: %v", f.rmContainers)
	}
	if len(f.removed) != 1 {
		t.Fatalf("volumes should still be wiped after the marker dissolves: %v", f.removed)
	}
}

// If the marker can't be removed (forceless rm refuses: the session started in
// the window), reset must abort without touching volumes.
func TestResetAbortsWhenMarkerRemovalFails(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{
		vols:          map[string]bool{volumeName(p.ID, "cache"): true},
		allContainers: map[string][]string{labelKey + "=" + p.ID: {"feed0000beef"}},
		failRmCont:    map[string]bool{"feed0000beef": true},
	}
	s, _, _ := testStreams("", false)
	if err := reset(s, p, engines(f), true); err == nil {
		t.Fatal("expected abort when the marker can't be removed")
	}
	if len(f.removed) != 0 {
		t.Fatalf("must not wipe volumes when the marker survives: %v", f.removed)
	}
}

// With both engines installed, reset wipes the project's volumes from BOTH —
// state can live in an engine the config no longer names — and labels the
// lines by engine.
func TestResetWipesEveryInstalledEngine(t *testing.T) {
	p, _ := testPaths(t)
	docker := &fakeRunner{vols: map[string]bool{volumeName(p.ID, ".claude"): true}}
	podman := &fakeRunner{engine: "podman", vols: map[string]bool{volumeName(p.ID, "cache"): true}}
	s, _, out := testStreams("", false)
	if err := reset(s, p, engines(docker, podman), true); err != nil {
		t.Fatal(err)
	}
	if len(docker.removed) != 1 || len(podman.removed) != 1 {
		t.Fatalf("both engines' volumes must be wiped: docker=%v podman=%v", docker.removed, podman.removed)
	}
	if !strings.Contains(out.String(), "[docker]") || !strings.Contains(out.String(), "[podman]") {
		t.Errorf("multi-engine listing should label lines by engine:\n%s", out.String())
	}
}

// A live session on the SECOND engine must block the wipe of both.
func TestResetRefusesWhenLiveOnAnyEngine(t *testing.T) {
	p, _ := testPaths(t)
	docker := &fakeRunner{vols: map[string]bool{volumeName(p.ID, ".claude"): true}}
	podman := &fakeRunner{engine: "podman", live: liveProject(p, "deadbeef0000")}
	s, _, _ := testStreams("", false)
	if err := reset(s, p, engines(docker, podman), true); err == nil {
		t.Fatal("expected refusal while a session is live on podman")
	}
	if len(docker.removed) != 0 {
		t.Fatalf("must not wipe docker volumes while podman has a session: %v", docker.removed)
	}
}

func TestResetPartialWipeReported(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{
		vols:       map[string]bool{volumeName(p.ID, "a"): true, volumeName(p.ID, "b"): true, volumeName(p.ID, "c"): true},
		failRemove: map[string]bool{volumeName(p.ID, "b"): true},
	}
	s, _, _ := testStreams("", false)
	err := reset(s, p, engines(f), true)
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
	if err := reset(s, p, engines(f), false); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 1 {
		t.Fatalf("'y' should proceed, removed=%v", f.removed)
	}
}

// Garbage at the confirm reprompts instead of silently taking the default
// (QA pass-2); the explicit answer after it lands.
func TestResetPromptRepromptsOnGarbage(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{vols: map[string]bool{volumeName(p.ID, "cache"): true}}
	s, _, errw := testStreams("banana\nn\n", false)
	if err := reset(s, p, engines(f), false); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 0 {
		t.Fatalf("'banana' then 'n' must abort, removed=%v", f.removed)
	}
	out := errw.String()
	if !strings.Contains(out, "unrecognized") || strings.Count(out, "Proceed? [y/N]") != 2 {
		t.Errorf("garbage must reprompt with a hint:\n%s", out)
	}
	if !strings.Contains(out, "aborted.") {
		t.Errorf("the n must abort:\n%s", out)
	}
}

// A project whose ONLY volumes are machine-scoped still hears what reset
// spared — the note must precede the no-volumes early return.
func TestResetNotesMachineVolumesEvenWithNoProjectVolumes(t *testing.T) {
	p, _ := testPaths(t)
	machineVol := machineVolumeName(os.Getuid(), "claude-identity")
	f := &fakeRunner{vols: map[string]bool{machineVol: true}}
	s, _, out := testStreams("", false)
	if err := reset(s, p, engines(f), true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "NOT touched") || !strings.Contains(out.String(), machineVol) {
		t.Errorf("machine-volume note missing when no project volumes exist:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "no volumes to reset") {
		t.Errorf("empty-case message lost:\n%s", out.String())
	}
	if len(f.removed) != 0 {
		t.Fatalf("nothing should be removed: %v", f.removed)
	}
}
