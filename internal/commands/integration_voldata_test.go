package commands

// Gated engine-side arm for the editors' volume Delete / Clear, run
// host-side or through the sacrificial runner:
//
//	byre-inttest ./internal/commands/ -run TestIntegrationVolumeDataDeleteAndClear -v
//
// What no fake can vouch for: that the engine's own answer to "who mounts
// this volume" is what the delete refusal names, and that the clear empties
// the volume a RUNNING container is using -- dotfiles, nested directories and
// a symlink included, the symlink's target untouched -- without removing it.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/gen"
)

func TestIntegrationVolumeDataDeleteAndClear(t *testing.T) {
	r := requireEngineRunner(t)
	eng := string(r.Engine())
	if err := r.ImagePull(gen.DefaultBase); err != nil {
		t.Fatalf("pulling %s on %s: %v", gen.DefaultBase, eng, err)
	}
	// A name no real byre volume can have, so the test never touches a
	// person's shared login.
	suffix := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	vol := "byre-inttest-voldata-" + suffix
	box := "byre-inttest-voldata-box-" + suffix
	t.Cleanup(func() {
		_ = exec.Command(eng, "rm", "-f", box).Run()
		_ = r.VolumeRemove(vol)
	})

	// The holder: a long-running container with the volume mounted and
	// filled with what agent state contains.
	fill := "mkdir -p /v/dir/sub && echo x > /v/cred && echo y > /v/.hidden && echo z > /v/dir/sub/f && ln -s /etc/hostname /v/link && sleep 600"
	if out, err := exec.Command(eng, "run", "-d", "--name", box, "--network", "none",
		"-v", vol+":/v", "--entrypoint", "sh", gen.DefaultBase, "-c", fill).CombinedOutput(); err != nil {
		t.Fatalf("starting the holder: %v\n%s", err, out)
	}
	inBox := func(script string) string {
		t.Helper()
		out, err := exec.Command(eng, "exec", box, "sh", "-c", script).CombinedOutput()
		if err != nil {
			t.Fatalf("exec in holder: %v\n%s", err, out)
		}
		return string(out)
	}
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(inBox("ls -A /v"), "link") {
		if time.Now().After(deadline) {
			t.Fatal("holder never filled the volume")
		}
		time.Sleep(200 * time.Millisecond)
	}

	hs, err := r.VolumeHolders(vol)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 1 || hs[0].Name != box || !hs[0].Running || hs[0].ImageID == "" {
		t.Fatalf("holders = %+v, want the one running box %s", hs, box)
	}

	t.Run("DeleteRefusesNamingTheHolder", func(t *testing.T) {
		err := deleteVolumeData(r, vol)
		if err == nil || !strings.Contains(err.Error(), box) {
			t.Fatalf("delete of a held volume = %v, want a refusal naming %s", err, box)
		}
		if ok, _ := r.VolumeExists(vol); !ok {
			t.Fatal("refused delete removed the volume")
		}
	})

	t.Run("ClearEmptiesInPlaceWhileRunning", func(t *testing.T) {
		note, err := emptyVolumeData(r, vol, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(note, box) {
			t.Errorf("note %q must name the box still using it", note)
		}
		if got := strings.TrimSpace(inBox("ls -A /v")); got != "" {
			t.Fatalf("running box still sees %q in the cleared volume", got)
		}
		if got := strings.TrimSpace(inBox("cat /etc/hostname")); got == "" {
			t.Fatal("the symlink's target was emptied: clear followed a link out of the volume")
		}
		if ok, _ := r.VolumeExists(vol); !ok {
			t.Fatal("clear removed the volume it should keep")
		}
	})

	t.Run("DeleteSucceedsOnceNothingHoldsIt", func(t *testing.T) {
		if out, err := exec.Command(eng, "rm", "-f", box).CombinedOutput(); err != nil {
			t.Fatalf("removing the holder: %v\n%s", err, out)
		}
		if hs, err := r.VolumeHolders(vol); err != nil || len(hs) != 0 {
			t.Fatalf("holders after rm = %+v, %v", hs, err)
		}
		// Clear with nothing mounting it keeps the volume: removed and
		// created again empty, never left absent.
		if _, err := emptyVolumeData(r, vol, nil); err != nil {
			t.Fatal(err)
		}
		if ok, _ := r.VolumeExists(vol); !ok {
			t.Fatal("an idle clear left the volume absent")
		}
		if err := deleteVolumeData(r, vol); err != nil {
			t.Fatal(err)
		}
		if ok, _ := r.VolumeExists(vol); ok {
			t.Fatal("volume still exists after delete")
		}
	})
}
