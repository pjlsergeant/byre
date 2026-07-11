package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

func writeDefault(t *testing.T, home, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "default.config"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readDefault(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "default.config"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func defaultSkills(t *testing.T, home string) []string {
	t.Helper()
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"))
	if err != nil {
		t.Fatalf("edited default.config must still parse: %v", err)
	}
	return cfg.Skills
}

func TestEnableSharedAuthCreatesFileAndList(t *testing.T) {
	home := t.TempDir()
	if err := EnableSharedAuth(home, "claude-shared-auth"); err != nil {
		t.Fatal(err)
	}
	if got := defaultSkills(t, home); len(got) != 1 || got[0] != "claude-shared-auth" {
		t.Fatalf("skills = %v", got)
	}
}

func TestEnableSharedAuthAppendsPreservingContent(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "# my comment\nbase = \"debian:bookworm\"\nskills = [\"devloop\"] # keep\n\n[env]\nK = \"v\"\n")
	if err := EnableSharedAuth(home, "claude-shared-auth"); err != nil {
		t.Fatal(err)
	}
	got := readDefault(t, home)
	for _, want := range []string{"# my comment", `base = "debian:bookworm"`, "# keep", `K = "v"`} {
		if !strings.Contains(got, want) {
			t.Errorf("surgical edit lost %q:\n%s", want, got)
		}
	}
	if s := defaultSkills(t, home); len(s) != 2 || s[0] != "devloop" || s[1] != "claude-shared-auth" {
		t.Fatalf("skills = %v", s)
	}
}

// The list editor must follow the array shapes a hand-edited file can carry:
// multi-line (with and without trailing comma), empty, and comment-bearing.
func TestEnableSharedAuthArrayShapes(t *testing.T) {
	cases := map[string]string{
		"multiline trailing comma": "skills = [\n  \"devloop\",\n]\n",
		"multiline no comma":       "skills = [\n  \"devloop\"\n]\n",
		"empty":                    "skills = []\n",
		"comment inside":           "skills = [ # bracket in comment ]\n  \"devloop\",\n]\n",
		"bracket in string":        "skills = [\"devloop\", \"!not]a-real[skill\"]\n",
	}
	for name, content := range cases {
		home := t.TempDir()
		writeDefault(t, home, content)
		if err := EnableSharedAuth(home, "claude-shared-auth"); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		s := defaultSkills(t, home)
		if len(s) == 0 || s[len(s)-1] != "claude-shared-auth" {
			t.Errorf("%s: skills = %v", name, s)
		}
	}
}

func TestEnableSharedAuthIdempotent(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "skills = [\"claude-shared-auth\"]\n")
	before := readDefault(t, home)
	if err := EnableSharedAuth(home, "claude-shared-auth"); err != nil {
		t.Fatal(err)
	}
	if got := readDefault(t, home); got != before {
		t.Fatalf("already-enabled must not rewrite the file:\n%s", got)
	}
}

// A file the editor can't safely follow is refused with a point-at-the-file
// error — never a guessed write.
func TestEnableSharedAuthRefusesUnparsableFile(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "skills = [\"unclosed\n")
	err := EnableSharedAuth(home, "claude-shared-auth")
	if err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("err = %v, want a manual-edit instruction", err)
	}
	if got := readDefault(t, home); !strings.Contains(got, "unclosed") {
		t.Fatalf("a refused edit must leave the file untouched:\n%s", got)
	}
}

func TestDeclineSharedAuthRecords(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "agent = \"claude\"\n")
	if err := DeclineSharedAuth(home, "claude"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SharedAuthDeclined) != 1 || cfg.SharedAuthDeclined[0] != "claude" {
		t.Fatalf("shared_auth_declined = %v", cfg.SharedAuthDeclined)
	}
	// And the favourites survived the surgical edit.
	if _, agent := Favourites(home); agent != "claude" {
		t.Fatalf("agent favourite lost: %q", agent)
	}
}

func TestSharedAuthAnswered(t *testing.T) {
	home := t.TempDir()
	if SharedAuthAnswered(home, "claude", "claude-shared-auth") {
		t.Fatal("no file yet — the offer is unanswered")
	}
	writeDefault(t, home, "skills = [\"claude-shared-auth\"]\n")
	if !SharedAuthAnswered(home, "claude", "claude-shared-auth") {
		t.Fatal("companion enabled = answered yes")
	}
	writeDefault(t, home, "shared_auth_declined = [\"claude\"]\n")
	if !SharedAuthAnswered(home, "claude", "claude-shared-auth") {
		t.Fatal("declined = answered no")
	}
	// An unparsable file counts as answered: never nag through (or edit) a
	// file we can't read.
	writeDefault(t, home, "skills = [\"broken\n")
	if !SharedAuthAnswered(home, "claude", "claude-shared-auth") {
		t.Fatal("unreadable file must suppress the offer")
	}
}
