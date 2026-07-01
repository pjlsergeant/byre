package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"byre/internal/project"
)

type fakeForget struct {
	liveIDs map[string]bool
	vols    []string
	images  map[string]bool // tag -> exists
	removed []string
}

func (f *fakeForget) RunningContainersByLabel(label string) ([]string, error) {
	id := strings.TrimPrefix(label, labelKey+"=")
	if f.liveIDs[id] {
		return []string{"deadbeef0000"}, nil
	}
	return nil, nil
}
func (f *fakeForget) VolumesByPrefix(string) ([]string, error) { return f.vols, nil }
func (f *fakeForget) VolumeRemove(name string) error           { f.removed = append(f.removed, name); return nil }
func (f *fakeForget) ImageExists(tag string) (bool, error)     { return f.images[tag], nil }
func (f *fakeForget) ImageRemove(tag string) error             { f.removed = append(f.removed, tag); return nil }

func forgetSetup(t *testing.T) (project.Paths, string) {
	t.Helper()
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()
	p, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	return p, proj
}

func TestForgetRemovesHostStateLeavesProjectTree(t *testing.T) {
	p, proj := forgetSetup(t)
	// host-side config (in the store) + a committed project-tree config
	os.WriteFile(filepath.Join(p.Dir, "byre.config"), []byte("agent=\"claude\"\n"), 0o644)
	projCfg := filepath.Join(proj, "byre.config")
	os.WriteFile(projCfg, []byte("agent=\"claude\"\n"), 0o644)
	f := &fakeForget{vols: []string{"byre-x-.claude", "byre-x-cache"}, images: map[string]bool{ImageTag(p.ID, os.Getuid(), os.Getgid()): true}}

	var out bytes.Buffer
	if err := forget(&out, strings.NewReader(""), p, f, true); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 3 {
		t.Fatalf("expected 2 volumes + 1 image removed, got %v", f.removed)
	}
	// host-side store (incl its byre.config + adoption record) is gone
	if _, err := os.Stat(p.Dir); !os.IsNotExist(err) {
		t.Errorf("host-side project dir should be removed: %v", err)
	}
	// the project tree is NOT touched
	if _, err := os.Stat(projCfg); err != nil {
		t.Errorf("forget must NOT delete the project tree's byre.config: %v", err)
	}
}

func TestForgetRefusesLive(t *testing.T) {
	p, _ := forgetSetup(t)
	f := &fakeForget{liveIDs: map[string]bool{p.ID: true}, vols: []string{"byre-x-cache"}}
	if err := forget(&bytes.Buffer{}, strings.NewReader(""), p, f, true); err == nil {
		t.Fatal("expected refusal while a session is live")
	}
	if len(f.removed) != 0 {
		t.Fatal("must not remove anything while live")
	}
	if _, err := os.Stat(p.Dir); err != nil {
		t.Errorf("projects dir must be kept when refusing: %v", err)
	}
}

func TestForgetPromptAborts(t *testing.T) {
	p, _ := forgetSetup(t)
	f := &fakeForget{vols: []string{"byre-x-cache"}}
	var out bytes.Buffer
	if err := forget(&out, strings.NewReader("n\n"), p, f, false); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 0 || !strings.Contains(out.String(), "aborted") {
		t.Fatalf("'n' should abort: removed=%v out=%s", f.removed, out.String())
	}
	if _, err := os.Stat(p.Dir); err != nil {
		t.Errorf("projects dir must survive abort: %v", err)
	}
}
