package onboard

import (
	"os"
	"path/filepath"
	"testing"
)

func writeDefault(t *testing.T, home, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "default.config"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSharedAuthAlreadyOn(t *testing.T) {
	home := t.TempDir()
	if SharedAuthAlreadyOn(home, "claude-shared-auth") {
		t.Fatal("no default.config — the companion can't be on machine-wide")
	}
	writeDefault(t, home, "skills = [\"claude-shared-auth\"]\n")
	if !SharedAuthAlreadyOn(home, "claude-shared-auth") {
		t.Fatal("companion in default.config skills = already on machine-wide")
	}
	writeDefault(t, home, "skills = [\"devloop\"]\n")
	if SharedAuthAlreadyOn(home, "claude-shared-auth") {
		t.Fatal("other skills enabled ≠ the companion is on")
	}
	// An unparsable file reads as not-on: the offer's answer never edits
	// default.config, so there is nothing here the picker must not touch —
	// and a broken default.config fails the develop loudly anyway.
	writeDefault(t, home, "skills = [\"broken\n")
	if SharedAuthAlreadyOn(home, "claude-shared-auth") {
		t.Fatal("an unreadable default.config must not claim the companion is on")
	}
}
