package packages

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The store notices must name the path byre actually used: a hardcoded
// "~/.byre" lies under a BYRE_HOME override (field-QA 2026-07-17, finding 1).
// DisplayPath contracts the real $HOME to "~" and leaves foreign roots alone.
func TestDisplayPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := DisplayPath(filepath.Join(home, "qa-home", "AGENTS.md")); got != "~/qa-home/AGENTS.md" {
		t.Errorf("home-rooted path: got %q", got)
	}
	if got := DisplayPath(home); got != "~" {
		t.Errorf("home itself: got %q", got)
	}
	if got := DisplayPath("/srv/elsewhere/.byre"); got != "/srv/elsewhere/.byre" {
		t.Errorf("foreign root must print as-is: got %q", got)
	}
	// Sibling of home ("/home/dev2") must NOT contract.
	if got := DisplayPath(home + "2/x"); got != home+"2/x" {
		t.Errorf("home-prefix sibling must not contract: got %q", got)
	}
}

// The notices themselves carry the resolved store path, not a literal
// "~/.byre", when the store lives elsewhere.
func TestStoreNoticesNameTheRealHome(t *testing.T) {
	store := t.TempDir() // a BYRE_HOME override outside ~/.byre
	var out bytes.Buffer
	if err := EnsureStore(store, nil, "test-ver", &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.Contains(s, "~/.byre") {
		t.Fatalf("notice hardcodes ~/.byre for an overridden store:\n%s", s)
	}
	if !strings.Contains(s, DisplayPath(filepath.Join(store, "AGENTS.md"))) {
		t.Fatalf("notice must name the real AGENTS.md path %q:\n%s", DisplayPath(filepath.Join(store, "AGENTS.md")), s)
	}
}
