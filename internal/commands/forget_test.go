package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForgetRemovesHostStateLeavesProjectTree(t *testing.T) {
	p, proj := testPaths(t)
	// host-side config (in the store) + a committed project-tree config
	os.WriteFile(filepath.Join(p.Dir, "byre.config"), []byte("agent=\"claude\"\n"), 0o644)
	projCfg := filepath.Join(proj, "byre.config")
	os.WriteFile(projCfg, []byte("agent=\"claude\"\n"), 0o644)
	f := &fakeRunner{
		vols:   map[string]bool{volumeName(p.ID, ".claude"): true, volumeName(p.ID, "cache"): true},
		images: map[string]bool{imageTag(p.ID, os.Getuid(), os.Getgid()): true},
	}

	s, _, _ := testStreams("", false)
	if err := forget(s, p, engines(f), true); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 2 || len(f.rmImages) != 1 {
		t.Fatalf("expected 2 volumes + 1 image removed, got vols=%v images=%v", f.removed, f.rmImages)
	}
	// host-side store (incl its byre.config + applied marker) is gone
	if _, err := os.Stat(p.Dir); !os.IsNotExist(err) {
		t.Errorf("host-side project dir should be removed: %v", err)
	}
	// the project tree is NOT touched
	if _, err := os.Stat(projCfg); err != nil {
		t.Errorf("forget must NOT delete the project tree's byre.config: %v", err)
	}
}

// "Completely remove" must hold across engines: forget cleans volumes and
// images from every installed engine before deleting the store.
func TestForgetCleansEveryInstalledEngine(t *testing.T) {
	p, _ := testPaths(t)
	docker := &fakeRunner{vols: map[string]bool{volumeName(p.ID, ".claude"): true}}
	podman := &fakeRunner{
		engine: "podman",
		vols:   map[string]bool{volumeName(p.ID, "cache"): true},
		images: map[string]bool{"byre-" + p.ID: true}, // built under podman, legacy tag
	}
	s, _, _ := testStreams("", false)
	if err := forget(s, p, engines(docker, podman), true); err != nil {
		t.Fatal(err)
	}
	if len(docker.removed) != 1 || len(podman.removed) != 1 || len(podman.rmImages) != 1 {
		t.Fatalf("both engines must be cleaned: docker vols=%v podman vols=%v podman images=%v",
			docker.removed, podman.removed, podman.rmImages)
	}
	if _, err := os.Stat(p.Dir); !os.IsNotExist(err) {
		t.Errorf("store should be removed once every engine is clean: %v", err)
	}
}

// An engine that can't be queried can't be declared clean — the store must
// survive, or forget would falsely claim complete removal while that engine
// still holds state.
func TestForgetKeepsStoreWhenAnEngineCannotBeQueried(t *testing.T) {
	p, _ := testPaths(t)
	docker := &fakeRunner{vols: map[string]bool{volumeName(p.ID, ".claude"): true}}
	podman := &fakeRunner{engine: "podman", liveErr: errors.New("cannot connect to podman")}
	s, _, _ := testStreams("", false)
	if err := forget(s, p, engines(docker, podman), true); err == nil {
		t.Fatal("expected an error when an engine can't be queried")
	}
	if len(docker.removed) != 0 {
		t.Fatalf("must not delete anything when an engine can't be queried: %v", docker.removed)
	}
	if _, err := os.Stat(p.Dir); err != nil {
		t.Errorf("store must survive an unqueryable engine: %v", err)
	}
}

func TestForgetRefusesLive(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{live: liveProject(p, "deadbeef0000"), vols: map[string]bool{volumeName(p.ID, "cache"): true}}
	s, _, _ := testStreams("", false)
	if err := forget(s, p, engines(f), true); err == nil {
		t.Fatal("expected refusal while a session is live")
	}
	if len(f.removed) != 0 || len(f.rmImages) != 0 {
		t.Fatal("must not remove anything while live")
	}
	if _, err := os.Stat(p.Dir); err != nil {
		t.Errorf("projects dir must be kept when refusing: %v", err)
	}
}

func TestForgetPromptAborts(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{vols: map[string]bool{volumeName(p.ID, "cache"): true}}
	s, _, out := testStreams("n\n", false)
	if err := forget(s, p, engines(f), false); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 0 || !strings.Contains(out.String(), "aborted") {
		t.Fatalf("'n' should abort: removed=%v out=%s", f.removed, out.String())
	}
	if _, err := os.Stat(p.Dir); err != nil {
		t.Errorf("projects dir must survive abort: %v", err)
	}
}
