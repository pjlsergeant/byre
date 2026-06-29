package project

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCanonicalizeTrailingSlashIdentical(t *testing.T) {
	dir := t.TempDir()
	withSlash, err := Canonicalize(dir + "/")
	if err != nil {
		t.Fatal(err)
	}
	without, err := Canonicalize(dir)
	if err != nil {
		t.Fatal(err)
	}
	if withSlash != without {
		t.Fatalf("trailing slash changed canonical path: %q vs %q", withSlash, without)
	}
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*-[0-9a-f]{6}$`)

func TestIDShapeStableAndDistinct(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()

	idA1, err := ID(a)
	if err != nil {
		t.Fatal(err)
	}
	idA2, err := ID(a)
	if err != nil {
		t.Fatal(err)
	}
	if idA1 != idA2 {
		t.Fatalf("id not stable for same dir: %q vs %q", idA1, idA2)
	}
	if !idRe.MatchString(idA1) {
		t.Fatalf("id %q does not match <slug>-<6hex>", idA1)
	}
	idB, err := ID(b)
	if err != nil {
		t.Fatal(err)
	}
	if idA1 == idB {
		t.Fatalf("distinct dirs share id %q", idA1)
	}
}

func TestIDReadableSlug(t *testing.T) {
	id, err := ID("/Users/me/dev/byre")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "byre-dev-") {
		t.Fatalf("expected leaf-then-parent slug 'byre-dev-...', got %q", id)
	}
}

func TestIDSanitizesAndDisambiguates(t *testing.T) {
	// Same leaf+parent names in different locations must still differ (hash).
	id1, _ := ID("/a/My Project")
	id2, _ := ID("/b/My Project")
	if id1 == id2 {
		t.Fatalf("same-named dirs in different paths share id: %q", id1)
	}
	if !idRe.MatchString(id1) {
		t.Fatalf("sanitized id invalid: %q", id1)
	}
}

func TestBootstrapRecordsPathAndIsIdempotent(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()

	paths, err := Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	rec, err := os.ReadFile(paths.PathRecord)
	if err != nil {
		t.Fatalf("path record not written: %v", err)
	}
	if got := string(rec); got != paths.Canonical+"\n" {
		t.Fatalf("path record = %q, want %q", got, paths.Canonical+"\n")
	}
	// Second bootstrap with the same path must succeed (idempotent).
	if err := paths.Bootstrap(); err != nil {
		t.Fatalf("second bootstrap failed: %v", err)
	}
}

func TestBootstrapDetectsCollision(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	paths, err := Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate a different project having claimed this id.
	if err := os.WriteFile(paths.PathRecord, []byte("/some/other/path\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := paths.Bootstrap(); err == nil {
		t.Fatal("expected collision error, got nil")
	}
}

func TestHomeRespectsEnv(t *testing.T) {
	want := filepath.Join(t.TempDir(), "byrehome")
	t.Setenv("BYRE_HOME", want)
	got, err := Home()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Home() = %q, want %q", got, want)
	}
}
