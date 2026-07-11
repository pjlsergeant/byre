package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/deliver"
)

// TestDeliverWiring drives the real cascade + transport through the commands
// adapter over fakeRunner: label vocabulary, identity, and the exec-stream
// argv all cross the seam correctly. Behavior depth lives in internal/deliver.
func TestDeliverWiring(t *testing.T) {
	f := &fakeRunner{
		live:   map[string][]string{labelKey: {"ctr1"}},
		env:    map[string]string{"BYRE_UID": "501", "BYRE_GID": "20"},
		labels: map[string]string{labelKey: "proj-abc123", workdirKey: "proj-abc123"},
	}
	src := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	s := Streams{Out: &out, Err: &errw, In: strings.NewReader(""), TTY: false}
	_, err := deliverWith(s, t.TempDir(), deliver.Options{}, deliver.PathSources([]string{src}), []sessionRunner{f}, 501, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "/inbox/report.pdf\n" {
		t.Fatalf("stdout = %q", got)
	}
	if !strings.Contains(errw.String(), "delivering to proj-abc123 (docker, ctr1)") {
		t.Fatalf("no target line: %q", errw.String())
	}
	if len(f.execInputs) != 1 {
		t.Fatalf("execInputs = %v", f.execInputs)
	}
	rec := f.execInputs[0]
	if !strings.HasPrefix(rec, "ctr1 501:20 ") || !strings.Contains(rec, "byre-deliver /inbox report .pdf") ||
		!strings.HasSuffix(rec, "<-hello") {
		t.Fatalf("exec record = %q", rec)
	}
}

// TestDeliverWiringUIDMismatch pins that the accident filter reads the
// container's identity, not the caller's, through the real adapter.
func TestDeliverWiringUIDMismatch(t *testing.T) {
	f := &fakeRunner{
		live:   map[string][]string{labelKey: {"ctr1"}},
		env:    map[string]string{"BYRE_UID": "777", "BYRE_GID": "20"},
		labels: map[string]string{labelKey: "proj-abc123", workdirKey: "proj-abc123"},
	}
	var out, errw bytes.Buffer
	s := Streams{Out: &out, Err: &errw, In: strings.NewReader(""), TTY: false}
	_, err := deliverWith(s, t.TempDir(), deliver.Options{}, deliver.PathSources([]string{"x"}), []sessionRunner{f}, 501, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "--skip-uid-check") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeliverZeroEnginesIsHonest(t *testing.T) {
	var out, errw bytes.Buffer
	s := Streams{Out: &out, Err: &errw, In: strings.NewReader(""), TTY: false}
	_, err := deliverWith(s, t.TempDir(), deliver.Options{}, deliver.PathSources([]string{"x"}), nil, 501, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no container engine") || strings.Contains(err.Error(), "no running byre boxes") {
		t.Fatalf("err = %v (zero engines must not claim zero boxes)", err)
	}
}
