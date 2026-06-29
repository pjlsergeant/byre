package commands

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"byre/internal/project"
)

type fakeResetRunner struct {
	live       []string
	liveSecond []string // returned from the 2nd RunningContainersByLabel call on (the re-check)
	liveCalls  int
	vols       []string
	removed    []string
	failRemove map[string]bool
}

func (f *fakeResetRunner) RunningContainersByLabel(string) ([]string, error) {
	f.liveCalls++
	if f.liveCalls >= 2 && f.liveSecond != nil {
		return f.liveSecond, nil
	}
	return f.live, nil
}
func (f *fakeResetRunner) VolumesByPrefix(string) ([]string, error) { return f.vols, nil }
func (f *fakeResetRunner) VolumeRemove(name string) error {
	if f.failRemove[name] {
		return fmt.Errorf("boom")
	}
	f.removed = append(f.removed, name)
	return nil
}

func resetPaths(t *testing.T) project.Paths {
	t.Helper()
	t.Setenv("BYRE_HOME", t.TempDir())
	p, err := project.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResetForceWipesAll(t *testing.T) {
	p := resetPaths(t)
	f := &fakeResetRunner{vols: []string{"byre-x-.claude", "byre-x-cache"}}
	var out bytes.Buffer
	if err := reset(&out, strings.NewReader(""), p, f, true); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 2 {
		t.Fatalf("force should remove all volumes, got %v", f.removed)
	}
}

func TestResetRefusesWhenLive(t *testing.T) {
	p := resetPaths(t)
	f := &fakeResetRunner{live: []string{"abcdef0123456789"}, vols: []string{"byre-x-cache"}}
	if err := reset(&bytes.Buffer{}, strings.NewReader(""), p, f, true); err == nil {
		t.Fatal("expected refusal while a session is live")
	}
	if len(f.removed) != 0 {
		t.Fatal("must not remove volumes while live")
	}
}

func TestResetNoVolumes(t *testing.T) {
	p := resetPaths(t)
	f := &fakeResetRunner{}
	var out bytes.Buffer
	if err := reset(&out, strings.NewReader(""), p, f, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no volumes to reset") {
		t.Errorf("expected 'no volumes' message: %s", out.String())
	}
}

func TestResetPromptAbortsOnNo(t *testing.T) {
	p := resetPaths(t)
	f := &fakeResetRunner{vols: []string{"byre-x-cache"}}
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
	p := resetPaths(t)
	// Not live at the first check, but a session appears by the re-check.
	f := &fakeResetRunner{vols: []string{"byre-x-cache"}, liveSecond: []string{"abcdef0123456789"}}
	var out bytes.Buffer
	if err := reset(&out, strings.NewReader(""), p, f, true); err == nil {
		t.Fatal("expected abort when a session starts before deletion")
	}
	if len(f.removed) != 0 {
		t.Fatal("must not remove volumes if a session appeared")
	}
}

func TestResetPartialWipeReported(t *testing.T) {
	p := resetPaths(t)
	f := &fakeResetRunner{
		vols:       []string{"byre-x-a", "byre-x-b", "byre-x-c"},
		failRemove: map[string]bool{"byre-x-b": true},
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
	if !strings.Contains(err.Error(), "byre-x-b") {
		t.Errorf("error should name the failed volume: %v", err)
	}
}

func TestResetPromptProceedsOnYes(t *testing.T) {
	p := resetPaths(t)
	f := &fakeResetRunner{vols: []string{"byre-x-cache"}}
	var out bytes.Buffer
	if err := reset(&out, strings.NewReader("y\n"), p, f, false); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 1 {
		t.Fatalf("'y' should proceed, removed=%v", f.removed)
	}
}
