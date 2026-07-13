package version

import "testing"

func TestSemverDevel(t *testing.T) {
	// Unstamped: String is "(devel)" or similar; Semver must be parseable.
	got := Semver()
	if got == "" || got[0] == '(' {
		t.Fatalf("Semver() = %q, want a parseable semver (not a paren form)", got)
	}
	// Force the devel path.
	old := Version
	Version = ""
	t.Cleanup(func() { Version = old })
	// With empty stamp and no useful build info in this test binary, Semver
	// should still not start with '('.
	if s := Semver(); s == "" || (len(s) > 0 && s[0] == '(') {
		t.Fatalf("Semver on unstamped = %q", s)
	}
}

func TestSemverStamped(t *testing.T) {
	old := Version
	Version = "v0.2.1"
	t.Cleanup(func() { Version = old })
	if got := Semver(); got != "0.2.1" {
		t.Fatalf("Semver() = %q, want 0.2.1", got)
	}
	if got := String(); got != "v0.2.1" {
		t.Fatalf("String() = %q, want v0.2.1", got)
	}
}
