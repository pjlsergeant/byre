package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/deliver"
)

// TestGrabWiring drives the real discovery + transport through the commands
// adapter over fakeRunner: the classify exec, the streamed-out content, and
// the landed host path all cross the seam correctly. Behavior depth lives in
// internal/deliver.
func TestGrabWiring(t *testing.T) {
	f := &fakeRunner{
		live:              map[string][]string{labelKey: {"ctr1"}},
		env:               map[string]string{"BYRE_UID": "501", "BYRE_GID": "20"},
		labels:            map[string]string{labelKey: "proj-abc123", workdirKey: "proj-abc123"},
		execOutputContent: "GRABBED",
	}
	dest := t.TempDir()
	var out, errw bytes.Buffer
	s := Streams{Out: &out, Err: &errw, In: strings.NewReader(""), TTY: false}
	err := grabWith(s, t.TempDir(), deliver.Options{}, "out/report.pdf", dest, []sessionRunner{f}, 501, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dest, "report.pdf")
	if got := out.String(); got != want+"\n" {
		t.Fatalf("stdout = %q, want %q", got, want+"\n")
	}
	if b, rerr := os.ReadFile(want); rerr != nil || string(b) != "GRABBED" {
		t.Fatalf("landed content = %q, %v", b, rerr)
	}
	if !strings.Contains(errw.String(), "grabbing /workspace/out/report.pdf from proj-abc123 (docker, ctr1)") {
		t.Fatalf("no target line: %q", errw.String())
	}
	// The cat exec ran as the box's dev identity with the resolved path.
	joined := strings.Join(f.execInputs, "\n")
	if !strings.Contains(joined, "ctr1 501:20 out byre-grab /workspace/out/report.pdf") {
		t.Fatalf("exec records = %q", joined)
	}
}

func TestGrabZeroEnginesIsHonest(t *testing.T) {
	var out, errw bytes.Buffer
	s := Streams{Out: &out, Err: &errw, In: strings.NewReader("")}
	err := grabWith(s, t.TempDir(), deliver.Options{}, "a.txt", ".", nil, 501, nil)
	if err == nil || !strings.Contains(err.Error(), "no container engine") {
		t.Fatalf("err = %v", err)
	}
}
