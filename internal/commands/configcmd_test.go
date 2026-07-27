package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The --global editor's title names the REAL file. It used to be the literal
// "~/.byre/default.config", which under a BYRE_HOME override put a path the
// session was not using in the title, five lines above a footer showing the
// one it was (2026-07-27 QA finding; the playbook pins the store notices to
// the same never-lie rule).
func TestDisplayPathNeverInventsASpelling(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir in this environment")
	}
	under := filepath.Join(home, ".byre", "default.config")
	if got := displayPath(under); got != "~/.byre/default.config" {
		t.Errorf("under home: %q, want the tilde spelling", got)
	}
	outside := filepath.Join(t.TempDir(), "home", "default.config")
	if got := displayPath(outside); got != outside {
		t.Errorf("outside home: %q, want it verbatim -- a tilde here would name a file that does not exist", got)
	}
	if got := displayPath(home); got != home {
		t.Errorf("home itself: %q, want verbatim (rel == \".\")", got)
	}
	if strings.HasPrefix(displayPath(filepath.Dir(home)), "~") {
		t.Error("a parent of home must not abbreviate")
	}
}
