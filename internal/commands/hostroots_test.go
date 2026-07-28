package commands

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/project"
)

// The root set is the four directories byre resolved itself. Pinned by
// membership, because a missing entry is a silent hole: the check would pass
// on a binary sitting in a directory the box writes.
func TestBoxWritableRootsMembership(t *testing.T) {
	base := t.TempDir()
	paths := project.Paths{
		WorkDir:          filepath.Join(base, "worktree"),
		Canonical:        filepath.Join(base, "main"),
		CommonGitDirHost: filepath.Join(base, "main", ".git"),
		Dir:              filepath.Join(base, "store"),
	}
	roots := boxWritableRoots(paths)
	for _, d := range []string{paths.WorkDir, paths.Canonical, paths.CommonGitDirHost, paths.Dir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(d, "tool")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		r := hostexec.NewResolver(func(string) (string, error) { return bin, nil })
		if _, err := r.Look("tool", roots); !errors.As(err, new(*hostexec.ShadowError)) {
			t.Errorf("a binary in %s must be declined, got %v", d, err)
		}
	}
	// A sibling of the tree is not a root: byre does not judge the user's PATH.
	outside := filepath.Join(base, "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(outside, "tool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := hostexec.NewResolver(func(string) (string, error) { return bin, nil })
	if got, err := r.Look("tool", roots); err != nil || got != bin {
		t.Errorf("Look outside the roots = (%q, %v), want the path and no error", got, err)
	}
}

// Empty Paths fields (a plain project carries no CommonGitDirHost) must not
// become a root that swallows everything.
func TestBoxWritableRootsDropsEmptyFields(t *testing.T) {
	roots := boxWritableRoots(project.Paths{})
	bin := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := hostexec.NewResolver(func(string) (string, error) { return bin, nil })
	if got, err := r.Look("docker", roots); err != nil || got != bin {
		t.Errorf("Look = (%q, %v), want the path and no error", got, err)
	}
}
