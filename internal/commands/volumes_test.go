package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pjlsergeant/byre/internal/configui"
)

func TestProjectVolumesDisambiguatesByLongestID(t *testing.T) {
	home := t.TempDir()
	// Two projects whose ids are in a prefix relationship.
	for _, id := range []string{"web-a1b2c3", "web-a1b2c3-x-9f8e7d"} {
		if err := os.MkdirAll(filepath.Join(home, "projects", id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := &fakeRunner{vols: map[string]bool{
		"byre-web-a1b2c3-cache":            true, // belongs to web-a1b2c3
		"byre-web-a1b2c3-x-9f8e7d-cache":   true, // belongs to the longer id, but the short prefix matches it too
		"byre-web-a1b2c3-x-9f8e7d-.claude": true, //   "
	}}

	got, err := projectVolumes(r, home, "web-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "byre-web-a1b2c3-cache" {
		t.Fatalf("short id captured the longer project's volumes: %v", got)
	}

	got2, _ := projectVolumes(r, home, "web-a1b2c3-x-9f8e7d")
	if len(got2) != 2 {
		t.Fatalf("longer id should own its 2 volumes, got %v", got2)
	}
}

// A project whose id begins with "machine" (a repo directory literally named
// that) must not capture machine-scoped volumes in its listings — reset/forget
// build their kill list from projectVolumes (ADR 0017). The exclusion matches
// ^byre-machine-u<digits>-, so it errs FAIL-SAFE: in the pathological corner
// where a unix username itself looks like u<digits> (id machine-u501-...),
// the project's own volumes are over-excluded (never deleted) rather than a
// machine-scoped volume ever being under-excluded (wrongly deleted).
func TestProjectVolumesExcludesMachineScoped(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects", "machine-pjl-abc123"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{vols: map[string]bool{
		"byre-machine-pjl-abc123-cache":     true, // the project's own volume (dir named "machine")
		"byre-machine-u501-claude-identity": true, // a machine-scoped volume — MUST be excluded
		"byre-machine-u501-abc123-cache":    true, // the u<digits>-username corner's own volume (see below)
	}}
	got, err := projectVolumes(r, home, "machine-pjl-abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "byre-machine-pjl-abc123-cache" {
		t.Fatalf("machine-scoped volume captured by a project listing: %v", got)
	}
	// The fail-safe direction of the u<digits> corner, pinned:
	if err := os.MkdirAll(filepath.Join(home, "projects", "machine-u501-abc123"), 0o755); err != nil {
		t.Fatal(err)
	}
	got2, err := projectVolumes(r, home, "machine-u501-abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 0 {
		t.Fatalf("u<digits> corner must over-exclude (fail-safe), got %v", got2)
	}
}

// The config-UI VolumeAdmin lists ORPHANED machine-scoped volumes (on the
// engine, no longer declared by any enabled skill/config) so the
// deliberate-delete route reset/forget advertises keeps working after e.g.
// shared-auth is disabled — and Clear removes them under the machine name.
func TestVolumeAdminListsAndClearsOrphanedMachineVolumes(t *testing.T) {
	p, dir := testPaths(t)
	orphan := machineVolumeName(os.Getuid(), "claude-identity")
	f := &fakeRunner{vols: map[string]bool{orphan: true}}
	a := &volumeAdmin{rs: []engineRunner{f}, paths: p, projectDir: dir}

	list, err := a.List()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, v := range list {
		if v.Name == "claude-identity" && v.Machine && v.Orphan && v.Exists {
			found = true
		}
	}
	if !found {
		t.Fatalf("orphaned machine volume not listed: %+v", list)
	}
	if err := a.Clear(configui.VolumeStatus{Name: "claude-identity", Machine: true, Orphan: true, Exists: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 1 || f.removed[0] != orphan {
		t.Fatalf("orphan cleared under the wrong name: %v", f.removed)
	}
}

// With BOTH engines installed a machine volume can live on each: the volumes
// screen — the advertised deliberate-delete route — must show one row per
// engine copy and clear exactly the row's engine, or its "logged out
// everywhere" claim is false while the other engine keeps the login alive
// (the lifecycle-batch bug class; audit finding).
func TestVolumeAdminListsAndClearsPerEngine(t *testing.T) {
	p, dir := testPaths(t)
	orphan := machineVolumeName(os.Getuid(), "claude-identity")
	docker := &fakeRunner{engine: "docker", vols: map[string]bool{orphan: true}}
	podman := &fakeRunner{engine: "podman", vols: map[string]bool{orphan: true}}
	a := &volumeAdmin{rs: []engineRunner{docker, podman}, paths: p, projectDir: dir}

	list, err := a.List()
	if err != nil {
		t.Fatal(err)
	}
	var engines []string
	for _, v := range list {
		if v.Name == "claude-identity" && v.Orphan {
			engines = append(engines, v.Engine)
		}
	}
	if len(engines) != 2 || engines[0] == engines[1] {
		t.Fatalf("each engine's copy must be its own row, got %v", engines)
	}

	if err := a.Clear(configui.VolumeStatus{Name: "claude-identity", Machine: true, Orphan: true, Engine: "podman"}); err != nil {
		t.Fatal(err)
	}
	if len(podman.removed) != 1 || podman.removed[0] != orphan {
		t.Fatalf("podman row must clear on podman: %v", podman.removed)
	}
	if len(docker.removed) != 0 {
		t.Fatalf("clearing one engine's row must not touch the other: %v", docker.removed)
	}
}
