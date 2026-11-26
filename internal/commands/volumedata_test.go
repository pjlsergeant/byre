package commands

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/configui"
	"github.com/pjlsergeant/byre/internal/runner"
)

// Delete refuses over the boxes that actually mount the volume, naming them,
// and leaves it alone.
func TestDeleteVolumeDataNamesTheHolders(t *testing.T) {
	vol := machineVolumeName(os.Getuid(), "zai-identity")
	f := &fakeRunner{vols: map[string]bool{vol: true}, holders: map[string][]runner.VolumeHolder{
		vol: {{ID: "aaa", Name: "byre-web-1", Running: true}},
	}}
	err := deleteVolumeData(f, vol)
	if err == nil || !strings.Contains(err.Error(), "byre-web-1 (running)") {
		t.Fatalf("delete must name the holding box, got %v", err)
	}
	if len(f.removed) != 0 {
		t.Fatalf("a held volume was removed: %v", f.removed)
	}
}

// A byre box running in some OTHER project, not mounting the volume, is no
// reason to refuse: the project editor's machine-volume delete used to refuse
// over any running byre box on the machine.
func TestVolumeAdminDeleteIgnoresBoxesNotUsingTheVolume(t *testing.T) {
	p, dir := testPaths(t)
	vol := machineVolumeName(os.Getuid(), "zai-identity")
	f := &fakeRunner{vols: map[string]bool{vol: true}, live: map[string][]string{labelKey: {"other-project-box"}}}
	a := &volumeAdmin{rs: []engineRunner{f}, paths: p, projectDir: dir}
	if err := a.Delete(configui.VolumeStatus{Name: "zai-identity", Machine: true, Exists: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 1 || f.removed[0] != vol {
		t.Fatalf("removed = %v", f.removed)
	}
}

// Clear on a held volume empties it in a helper from the holding box's image,
// with the volume mounted read-write and nothing removed.
func TestEmptyVolumeDataRunsInTheHoldersImage(t *testing.T) {
	vol := machineVolumeName(os.Getuid(), "zai-identity")
	f := &fakeRunner{vols: map[string]bool{vol: true}, holders: map[string][]runner.VolumeHolder{
		vol: {
			{ID: "bbb", Name: "byre-api-2", ImageID: "sha256:stopped", Running: false},
			{ID: "aaa", Name: "byre-web-1", ImageID: "sha256:running", Running: true},
		},
	}}
	note, err := emptyVolumeData(f, vol, []string{helperKey + "=" + vol})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.helperRuns) != 1 {
		t.Fatalf("helper runs = %d", len(f.helperRuns))
	}
	h := f.helperRuns[0]
	if h.Image != "sha256:running" || h.Volume != vol || h.ReadOnly {
		t.Fatalf("helper = %+v", h)
	}
	if !strings.Contains(h.Script, "-mindepth 1") || !strings.Contains(h.Script, h.MountPath) {
		t.Fatalf("helper must empty the mount, not remove it: %q", h.Script)
	}
	if len(f.removed) != 0 {
		t.Fatalf("clear removed the volume: %v", f.removed)
	}
	if !strings.Contains(note, "byre-web-1") {
		t.Fatalf("note must name who still mounts it: %q", note)
	}
}

// With nothing mounting it, clear removes the volume and creates it again
// empty: the volume stays, as the confirm promised, and no helper runs.
func TestEmptyVolumeDataWithNoHolderRecreatesEmpty(t *testing.T) {
	vol := machineVolumeName(os.Getuid(), "zai-identity")
	f := &fakeRunner{vols: map[string]bool{vol: true}}
	if _, err := emptyVolumeData(f, vol, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.helperRuns) != 0 || len(f.removed) != 1 || len(f.created) != 1 || f.created[0] != vol {
		t.Fatalf("helpers=%d removed=%v created=%v", len(f.helperRuns), f.removed, f.created)
	}
	if !f.vols[vol] {
		t.Fatal("clear must leave the volume in place")
	}
}

// A failed clear may be the client losing the engine while the helper keeps
// deleting: the helper is taken away before the failure is reported.
func TestEmptyVolumeDataRemovesItsHelperOnFailure(t *testing.T) {
	vol := machineVolumeName(os.Getuid(), "zai-identity")
	f := &fakeRunner{vols: map[string]bool{vol: true}, allContainers: map[string][]string{}, holders: map[string][]runner.VolumeHolder{
		vol: {{ID: "aaa", Name: "byre-web-1", ImageID: "sha256:running", Running: true}},
	}}
	f.helperHook = func(h runner.Helper) {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, l := range h.Labels {
			if strings.HasPrefix(l, helperRunKey+"=") {
				f.allContainers[l] = []string{"helper-1"}
			}
		}
	}
	f.helperErr = func(runner.Helper) error { return fmt.Errorf("unexpected EOF") }
	if _, err := emptyVolumeData(f, vol, nil); err == nil {
		t.Fatal("a failed helper must fail the clear")
	}
	if len(f.forceRemoved) != 1 || f.forceRemoved[0] != "helper-1" {
		t.Fatalf("the run's helper must be removed, got %v", f.forceRemoved)
	}
}

// The --global editor's admin lists every machine volume on every engine and
// acts on the row's own engine copy only.
func TestMachineVolumeAdminListsAndActsPerEngine(t *testing.T) {
	vol := machineVolumeName(os.Getuid(), "zai-identity")
	docker := &fakeRunner{engine: "docker", vols: map[string]bool{vol: true, "byre-web-abc-.claude": true}}
	podman := &fakeRunner{engine: "podman", vols: map[string]bool{vol: true}}
	a := &machineVolumeAdmin{rs: []engineRunner{docker, podman}}
	list, _, err := a.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "zai-identity" || !list[0].Machine || list[0].Engine == list[1].Engine {
		t.Fatalf("list = %+v", list)
	}
	if err := a.Delete(configui.VolumeStatus{Name: "zai-identity", Machine: true, Engine: "podman"}); err != nil {
		t.Fatal(err)
	}
	if len(podman.removed) != 1 || len(docker.removed) != 0 {
		t.Fatalf("podman removed %v, docker removed %v", podman.removed, docker.removed)
	}
	if err := a.Delete(configui.VolumeStatus{Name: ".claude", Engine: "docker"}); err == nil {
		t.Fatal("the global admin must refuse a project volume")
	}
}

// A clear whose re-create failed left the volume gone; clearing again
// finishes it with the create alone instead of failing on the remove.
func TestEmptyVolumeDataFinishesAnAbsentVolume(t *testing.T) {
	vol := machineVolumeName(os.Getuid(), "zai-identity")
	f := &fakeRunner{vols: map[string]bool{}}
	if _, err := emptyVolumeData(f, vol, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 0 || len(f.created) != 1 || !f.vols[vol] {
		t.Fatalf("removed=%v created=%v", f.removed, f.created)
	}
}

// A clear helper left on the engine (byre died while it ran) blocks the next
// delete or clear of that machine volume, from either editor, naming its
// rm -f line.
func TestMachineVolumeActionsRefuseALeftoverClearHelper(t *testing.T) {
	vol := machineVolumeName(os.Getuid(), "zai-identity")
	left := map[string][]string{helperMachineLabel(vol): {"deadbeef0000"}}
	f := &fakeRunner{vols: map[string]bool{vol: true}, allContainers: left}
	g := &machineVolumeAdmin{rs: []engineRunner{f}}
	row := configui.VolumeStatus{Name: "zai-identity", Machine: true, Exists: true}
	if err := g.Delete(row); err == nil || !strings.Contains(err.Error(), "rm -f deadbeef0000") {
		t.Fatalf("global delete = %v", err)
	}
	if _, err := g.Empty(row); err == nil || !strings.Contains(err.Error(), "rm -f deadbeef0000") {
		t.Fatalf("global clear = %v", err)
	}
	p, dir := testPaths(t)
	a := &volumeAdmin{rs: []engineRunner{f}, paths: p, projectDir: dir}
	if _, err := a.Empty(row); err == nil || !strings.Contains(err.Error(), "rm -f deadbeef0000") {
		t.Fatalf("project-editor clear = %v", err)
	}
	if len(f.removed) != 0 || len(f.helperRuns) != 0 {
		t.Fatalf("refused, yet acted: removed=%v helpers=%d", f.removed, len(f.helperRuns))
	}
}
