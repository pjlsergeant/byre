package project

import (
	"os"
	"path/filepath"
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

// The shape assertions below ride the production idRe (project.go), so the
// tests pin the same grammar ValidID enforces.

func TestValidID(t *testing.T) {
	dir := t.TempDir()
	id, err := ID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidID(id) {
		t.Fatalf("generated id %q must validate", id)
	}
	for _, bad := range []string{
		"",
		"../../etc",
		"byre/dev-0877d7",
		"byre-dev-0877D7", // hash is lowercase hex
		"byre-dev-0877d",  // hash too short
		"-0877d7",         // empty slug
		"foo--0877d7",     // adjacent hyphens: sanitize collapses runs, never emits these
		"byre dev-0877d7",
	} {
		if ValidID(bad) {
			t.Errorf("%q must not validate", bad)
		}
	}
}

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

// Two concurrent FIRST enrollments whose paths collide on one id must never
// both pass the fence: the record claim is an atomic no-replace publish, so
// exactly one wins and the loser gets the loud collision error (external
// review, 2026-07-19 — a check-then-WriteFile let both through and silently
// share the id's config, image, and volumes).
func TestBootstrapConcurrentFirstEnrollmentSingleWinner(t *testing.T) {
	for i := 0; i < 100; i++ {
		t.Setenv("BYRE_HOME", t.TempDir())
		a, err := Resolve(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		// The colliding twin: same id-derived paths, different canonical.
		b := a
		b.Canonical = a.Canonical + "-evil-twin"

		start := make(chan struct{})
		errs := make(chan error, 2)
		for _, p := range []Paths{a, b} {
			p := p
			go func() {
				<-start
				errs <- p.Bootstrap()
			}()
		}
		close(start)
		var succeeded int
		for j := 0; j < 2; j++ {
			if err := <-errs; err == nil {
				succeeded++
			} else if !strings.Contains(err.Error(), "collision") {
				t.Fatalf("iteration %d: loser must fail with the collision error, got: %v", i, err)
			}
		}
		if succeeded != 1 {
			t.Fatalf("iteration %d: want exactly one winner, got %d", i, succeeded)
		}
		// The surviving record is the winner's, intact — never empty/partial.
		rec, err := os.ReadFile(a.PathRecord)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSuffix(string(rec), "\n"); got != a.Canonical && got != b.Canonical {
			t.Fatalf("iteration %d: record holds neither claimant: %q", i, got)
		}
	}
}

// Two racing enrollments of the SAME project (same canonical) both succeed —
// the no-replace claim must not turn idempotent re-enrollment into an error.
func TestBootstrapConcurrentSameProjectBothSucceed(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	p, err := Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	for j := 0; j < 2; j++ {
		go func() {
			<-start
			errs <- p.Bootstrap()
		}()
	}
	close(start)
	for j := 0; j < 2; j++ {
		if err := <-errs; err != nil {
			t.Fatalf("same-project concurrent bootstrap must succeed: %v", err)
		}
	}
}

func TestValidateExistingIsReadOnly(t *testing.T) {
	t.Setenv("BYRE_HOME", filepath.Join(t.TempDir(), "home"))
	proj := t.TempDir()

	paths, err := Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	// No record yet: passes, and creates nothing — not even the home.
	if err := paths.ValidateExisting(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.Home); !os.IsNotExist(err) {
		t.Fatalf("ValidateExisting created state under %s: %v", paths.Home, err)
	}
	// An existing matching record still passes.
	if err := paths.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if err := paths.ValidateExisting(); err != nil {
		t.Fatalf("matching record must validate: %v", err)
	}
	// A record claiming another path is the same collision Bootstrap fails on.
	if err := os.WriteFile(paths.PathRecord, []byte("/some/other/path\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := paths.ValidateExisting(); err == nil {
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
