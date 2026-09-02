package hostopen

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// The in-tree judgment is by file IDENTITY (os.SameFile over the ancestor
// chain), not spelling — that is what makes a case-variant spelling on a
// case-insensitive filesystem (untestable here on ext4) classify correctly.
// Pin the mechanism with a symlink alias, and the lexical fallback for an
// unstattable tree.
func TestInTreeByIdentity(t *testing.T) {
	tree := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(tree, alias); err != nil {
		t.Fatal(err)
	}
	if !InTreeByIdentity(tree, filepath.Join(alias, "sub")) {
		t.Error("an alias spelling through a symlinked ancestor must classify in-tree")
	}
	// Alias to a SUBDIRECTORY: the spelled chain never meets the root, only the
	// resolved chain above the subdir does (the codex-review regression).
	sub := filepath.Join(tree, "deep", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasSub := filepath.Join(t.TempDir(), "aliassub")
	if err := os.Symlink(sub, aliasSub); err != nil {
		t.Fatal(err)
	}
	if !InTreeByIdentity(tree, filepath.Join(aliasSub, "x")) {
		t.Error("an alias into a subdirectory must classify in-tree")
	}
	if InTreeByIdentity(tree, filepath.Dir(tree)) {
		t.Error("the tree's parent must not classify in-tree")
	}
	// A file INSIDE the tree that is a symlink pointing out of it still
	// classifies in-tree: the caller's refusal must not be escapable by
	// spelling the shadowing entry as a link to the real binary.
	outside := filepath.Join(t.TempDir(), "real")
	if err := os.WriteFile(outside, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tree, "shadow")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if !InTreeByIdentity(tree, link) {
		t.Error("an in-tree symlink to an outside target must still classify in-tree")
	}
	// tree unstattable: degrade to the lexical judgment, not a panic or a
	// blanket false (which would skip the escape check for lexically-in-tree
	// spellings).
	gone := filepath.Join(t.TempDir(), "gone")
	if !InTreeByIdentity(gone, filepath.Join(gone, "sub")) {
		t.Error("lexical fallback must still classify a spelled-under path in-tree")
	}
}

func TestRealUnder(t *testing.T) {
	base := t.TempDir()
	plain := filepath.Join(base, "file")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := RealUnder(base, "file")
	if err != nil {
		t.Fatalf("contained plain file: %v", err)
	}
	want, err := filepath.EvalSymlinks(plain)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("contained plain file: got %q want %q", got, want)
	}

	// A symlink inside the tree whose target is also inside: the resolved
	// path is the target, still contained.
	if err := os.Symlink("file", filepath.Join(base, "alias")); err != nil {
		t.Fatal(err)
	}
	got, err = RealUnder(base, "alias")
	if err != nil {
		t.Fatalf("contained-via-in-tree-symlink: %v", err)
	}
	if got != want {
		t.Fatalf("contained-via-in-tree-symlink: got %q want %q", got, want)
	}

	outside := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(outside, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := RealUnder(base, "escape"); !errors.Is(err, ErrEscapes) {
		t.Fatalf("escape via symlink to outside: err = %v, want ErrEscapes", err)
	}

	if _, err := RealUnder(base, "gone"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing target: err = %v, want fs.ErrNotExist", err)
	}

	aliasBase := filepath.Join(t.TempDir(), "basealias")
	if err := os.Symlink(base, aliasBase); err != nil {
		t.Fatal(err)
	}
	got, err = RealUnder(aliasBase, "file")
	if err != nil {
		t.Fatalf("base given through a symlink alias: %v", err)
	}
	if got != want {
		t.Fatalf("base given through a symlink alias: got %q want %q", got, want)
	}
}
