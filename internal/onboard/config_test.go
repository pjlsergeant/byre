package onboard

import (
	"os"
	"path/filepath"
	"slices"
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

func parsedDefault(t *testing.T, home string) config.Config {
	t.Helper()
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"))
	if err != nil {
		t.Fatalf("edited default.config must still parse: %v", err)
	}
	return cfg
}

func TestSaveSharedAuthDefaultYesCreatesFileAndList(t *testing.T) {
	home := t.TempDir()
	if err := SaveSharedAuthDefault(home, "claude", true); err != nil {
		t.Fatal(err)
	}
	if got := parsedDefault(t, home).SharedAuth; len(got) != 1 || got[0] != "claude" {
		t.Fatalf("shared_auth = %v", got)
	}
	if !SharedAuthPreference(home, "claude") {
		t.Fatal("saved yes must read back as the preference")
	}
}

// The saved preference must never leak into config with teeth: saving yes
// touches shared_auth only — skills stays exactly as the user left it.
func TestSaveSharedAuthDefaultNeverTouchesSkills(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "# my comment\nbase = \"debian:bookworm\"\nskills = [\"devloop\"] # keep\n\n[env]\nK = \"v\"\n")
	if err := SaveSharedAuthDefault(home, "claude", true); err != nil {
		t.Fatal(err)
	}
	got := readDefault(t, home)
	for _, want := range []string{"# my comment", `base = "debian:bookworm"`, `skills = ["devloop"] # keep`, `K = "v"`} {
		if !strings.Contains(got, want) {
			t.Errorf("surgical edit lost %q:\n%s", want, got)
		}
	}
	cfg := parsedDefault(t, home)
	if !slices.Equal(cfg.Skills, []string{"devloop"}) {
		t.Fatalf("saving a preference must not write skills: %v", cfg.Skills)
	}
	if !slices.Equal(cfg.SharedAuth, []string{"claude"}) {
		t.Fatalf("shared_auth = %v", cfg.SharedAuth)
	}
}

// Saving no removes the agent from the list (and removes the line once the
// list empties); saving the stored answer again rewrites nothing.
func TestSaveSharedAuthDefaultNoRemovesAndIdempotent(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "shared_auth = [\"claude\", \"codex\"]\n")
	if err := SaveSharedAuthDefault(home, "claude", false); err != nil {
		t.Fatal(err)
	}
	if got := parsedDefault(t, home).SharedAuth; !slices.Equal(got, []string{"codex"}) {
		t.Fatalf("shared_auth = %v", got)
	}
	if err := SaveSharedAuthDefault(home, "codex", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readDefault(t, home), "shared_auth") {
		t.Fatalf("an emptied preference list must remove the line:\n%s", readDefault(t, home))
	}

	before := readDefault(t, home)
	if err := SaveSharedAuthDefault(home, "claude", false); err != nil {
		t.Fatal(err)
	}
	if got := readDefault(t, home); got != before {
		t.Fatalf("saving the stored answer must not rewrite the file:\n%s", got)
	}
}

// A file the editor can't parse is refused with a point-at-the-file error —
// never a guessed write.
func TestSaveSharedAuthDefaultRefusesUnparsableFile(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "skills = [\"unclosed\n")
	err := SaveSharedAuthDefault(home, "claude", true)
	if err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("err = %v, want a manual-edit instruction", err)
	}
	if got := readDefault(t, home); !strings.Contains(got, "unclosed") {
		t.Fatalf("a refused edit must leave the file untouched:\n%s", got)
	}
}

// A hand-formatted multi-line shared_auth list is a shape the one-line
// rewrite can't follow: the re-parse verification must refuse it loudly
// (do-it-by-hand) rather than mangle the file.
func TestSaveSharedAuthDefaultRefusesMultilineList(t *testing.T) {
	home := t.TempDir()
	content := "shared_auth = [\n  \"codex\",\n]\n"
	writeDefault(t, home, content)
	err := SaveSharedAuthDefault(home, "claude", true)
	if err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("err = %v, want a manual-edit refusal", err)
	}
	if got := readDefault(t, home); got != content {
		t.Fatalf("a refused edit must leave the file untouched:\n%s", got)
	}
}

func TestSharedAuthPreference(t *testing.T) {
	home := t.TempDir()
	if SharedAuthPreference(home, "claude") {
		t.Fatal("no file — no saved preference")
	}
	writeDefault(t, home, "shared_auth = [\"claude\"]\n")
	if !SharedAuthPreference(home, "claude") {
		t.Fatal("agent in shared_auth = saved yes")
	}
	if SharedAuthPreference(home, "codex") {
		t.Fatal("another agent's preference must not apply")
	}
	// Unparsable file = no preference; the offer just defaults No.
	writeDefault(t, home, "shared_auth = [\"broken\n")
	if SharedAuthPreference(home, "claude") {
		t.Fatal("unreadable file must not claim a preference")
	}
}

func TestSharedAuthAlreadyOn(t *testing.T) {
	home := t.TempDir()
	if SharedAuthAlreadyOn(home, "claude-shared-auth") {
		t.Fatal("no default.config — nothing is granted machine-wide")
	}
	writeDefault(t, home, "skills = [\"claude-shared-auth\"]\n")
	if !SharedAuthAlreadyOn(home, "claude-shared-auth") {
		t.Fatal("companion in default.config skills = granted machine-wide")
	}
	writeDefault(t, home, "skills = [\"devloop\"]\nshared_auth = [\"claude\"]\n")
	if SharedAuthAlreadyOn(home, "claude-shared-auth") {
		t.Fatal("a saved PREFERENCE is not a grant and must not suppress the offer")
	}
	// An unparsable file counts as on: never offer through (or, on a save,
	// edit) a file we can't read.
	writeDefault(t, home, "skills = [\"broken\n")
	if !SharedAuthAlreadyOn(home, "claude-shared-auth") {
		t.Fatal("unreadable file must suppress the offer")
	}
}
