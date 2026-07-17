package commands

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/deliver"
	"github.com/pjlsergeant/byre/internal/project"
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

// workdirIDOf's error taxonomy: an operational Resolve failure (here, a
// worktree pointer whose target is gone) must ABORT selection — never wrap
// deliver.ErrNoWorkdirID, whose "skip this level" meaning would let the
// sole-session/picker fallbacks hand the delivery to an unrelated box. Same
// for a collision. A plain directory resolves normally.
func TestWorkdirIDOfErrorTaxonomy(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())

	plain := t.TempDir()
	if id, err := workdirIDOf(plain); err != nil || id == "" {
		t.Fatalf("plain dir: (%q, %v), want an id", id, err)
	}

	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, ".git"),
		[]byte("gitdir: /nonexistent/worktrees/wt1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := workdirIDOf(broken)
	if err == nil {
		t.Fatal("broken worktree metadata: want an error")
	}
	if errors.Is(err, deliver.ErrNoWorkdirID) {
		t.Fatalf("operational failure wrapped as the skip sentinel: %v", err)
	}

	collided := t.TempDir()
	paths, err := project.Resolve(collided)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PathRecord, []byte("/some/other/project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = workdirIDOf(collided)
	if err == nil || !strings.Contains(err.Error(), "collision") || errors.Is(err, deliver.ErrNoWorkdirID) {
		t.Fatalf("collision must be a non-sentinel refusal, got: %v", err)
	}
}
