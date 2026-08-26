package hostexec

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fakeLook(m map[string]string) (func(string) (string, error), *int) {
	calls := 0
	return func(name string) (string, error) {
		calls++
		if p, ok := m[name]; ok {
			return p, nil
		}
		return "", exec.ErrNotFound
	}, &calls
}

// A binary PATH resolved out of a box-writable directory is declined, and the
// refusal names the three things the ten-second fix needs: which tool, where
// PATH found it, and which writable root it fell under.
func TestLookDeclinesShadowedBinary(t *testing.T) {
	tree := t.TempDir()
	shadow := filepath.Join(tree, ".bin", "docker")
	if err := os.MkdirAll(filepath.Dir(shadow), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shadow, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	look, _ := fakeLook(map[string]string{"docker": shadow})
	r := NewResolver(look)

	got, err := r.Look("docker", NewRoots(tree))
	if got != "" {
		t.Errorf("a declined lookup must return no path, got %q", got)
	}
	var se *ShadowError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *ShadowError", err)
	}
	if se.Name != "docker" || se.Path != shadow || se.Root != tree {
		t.Errorf("ShadowError = %+v, want name=docker path=%s root=%s", se, shadow, tree)
	}
	for _, want := range []string{"declines to run", "docker", shadow, tree} {
		if !strings.Contains(se.Error(), want) {
			t.Errorf("refusal text missing %q: %s", want, se.Error())
		}
	}
}

// An in-tree entry spelled as a symlink to the real system binary is still
// declined: the agent can re-point the link after byre resolved it, so the
// LOCUS of resolution is the test, not what it happens to point at today.
func TestLookDeclinesInTreeSymlink(t *testing.T) {
	tree := t.TempDir()
	real := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tree, "git")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	look, _ := fakeLook(map[string]string{"git": link})
	if _, err := NewResolver(look).Look("git", NewRoots(tree)); !errors.As(err, new(*ShadowError)) {
		t.Fatalf("err = %v, want *ShadowError", err)
	}
}

// A system binary that resolves INTO the tree is the other direction of the
// same shadow and is declined too.
func TestLookDeclinesResolvedIntoTree(t *testing.T) {
	tree := t.TempDir()
	target := filepath.Join(tree, "planted")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sysdir := t.TempDir()
	sysbin := filepath.Join(sysdir, "ssh")
	if err := os.Symlink(target, sysbin); err != nil {
		t.Fatal(err)
	}
	look, _ := fakeLook(map[string]string{"ssh": sysbin})
	if _, err := NewResolver(look).Look("ssh", NewRoots(tree)); !errors.As(err, new(*ShadowError)) {
		t.Fatalf("err = %v, want *ShadowError", err)
	}
}

// Everything outside the root set pins silently and runs -- byre does not
// judge the user's PATH. An empty root set (a caller with no project in
// hand) is the same case.
func TestLookPinsOutsideRoots(t *testing.T) {
	sys := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(sys, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	look, _ := fakeLook(map[string]string{"docker": sys})
	r := NewResolver(look)
	for _, roots := range []Roots{NewRoots(t.TempDir()), NewRoots(), NewRoots("", "")} {
		got, err := r.Look("docker", roots)
		if err != nil || got != sys {
			t.Errorf("Look = (%q, %v), want (%q, nil)", got, err, sys)
		}
	}
}

// A root of "/" is dropped at construction: the filesystem root is never
// box-writable, and as a root it would decline every binary on the machine
// (field-found: a Dock-icon drop launches the deliver app with cwd "/", and
// the cwd fallback declined docker AND podman as "inside /"). The lookup
// must pin silently, exactly as an empty root set does.
func TestNewRootsDropsFilesystemRoot(t *testing.T) {
	sys := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(sys, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	look, _ := fakeLook(map[string]string{"docker": sys})
	r := NewResolver(look)
	for _, roots := range []Roots{NewRoots("/"), NewRoots("//"), NewRoots("/", "")} {
		got, err := r.Look("docker", roots)
		if err != nil || got != sys {
			t.Errorf("Look = (%q, %v), want (%q, nil)", got, err, sys)
		}
	}
}

// The resolution is pinned once per binary for the process: PATH is read one
// time however many spawns follow, so the path byre checked is the path byre
// runs. Failures pin too (installedEngines asks about both engines
// repeatedly).
func TestLookPinsOncePerBinary(t *testing.T) {
	sys := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(sys, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	look, calls := fakeLook(map[string]string{"git": sys})
	r := NewResolver(look)
	for range 3 {
		if _, err := r.Look("git", NewRoots()); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Look("podman", NewRoots()); !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("err = %v, want exec.ErrNotFound", err)
		}
	}
	if *calls != 2 {
		t.Errorf("PATH read %d times, want 2 (one pin per binary)", *calls)
	}
}

// A name pinned by a caller holding no roots must not exempt itself from a
// later caller's check: the pin is the RESOLUTION, the containment test runs
// per call.
func TestPinDoesNotExemptLaterRootSet(t *testing.T) {
	tree := t.TempDir()
	shadow := filepath.Join(tree, "git")
	if err := os.WriteFile(shadow, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	look, _ := fakeLook(map[string]string{"git": shadow})
	r := NewResolver(look)
	if _, err := r.Look("git", NewRoots()); err != nil {
		t.Fatalf("no-roots lookup must pin silently: %v", err)
	}
	if _, err := r.Look("git", NewRoots(tree)); !errors.As(err, new(*ShadowError)) {
		t.Fatalf("err = %v, want *ShadowError", err)
	}
}

// A tool that isn't installed reports as itself, not as a shadow: callers
// that degrade on absence (the clipboard probe, installedEngines) must be
// able to tell the two apart.
func TestLookMissingBinary(t *testing.T) {
	look, _ := fakeLook(nil)
	_, err := NewResolver(look).Look("zenity", NewRoots(t.TempDir()))
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("err = %v, want exec.ErrNotFound", err)
	}
	if errors.As(err, new(*ShadowError)) {
		t.Error("an absent tool must not report as a shadow")
	}
}

// Looker binds a root set to the bare lookup signature the injectable seams
// take.
func TestLooker(t *testing.T) {
	if _, err := Looker(NewRoots())("definitely-not-a-real-binary-xyz"); err == nil {
		t.Error("want an error for a binary that is not installed")
	}
}
