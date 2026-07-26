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
	if err := SaveSharedAuthDefaultPick(home, "claude", "", true); err != nil {
		t.Fatal(err)
	}
	got := parsedDefault(t, home).SharedAuth
	if !got.HasYes("claude") {
		t.Fatalf("shared_auth = %+v", got)
	}
	if !SharedAuthPreference(home, "claude") {
		t.Fatal("saved yes must read back as the preference")
	}
}

// Installed third-party agents carry qualified owner/name IDs. The written
// table key must be quoted ('/' is illegal bare in TOML) or the semantic
// verify rejects the edit and onboarding aborts before writing byre.config.
func TestSaveSharedAuthDefaultPickQualifiedAgent(t *testing.T) {
	home := t.TempDir()
	if err := SaveSharedAuthDefaultPick(home, "acme/agent", "acme/agent-shared-auth", true); err != nil {
		t.Fatal(err)
	}
	got := parsedDefault(t, home).SharedAuth
	if pick := got.CompanionPick("acme/agent"); pick != "acme/agent-shared-auth" {
		t.Fatalf("saved pick round-trip: got %q", pick)
	}
	if SharedAuthPick(home, "acme/agent") != "acme/agent-shared-auth" {
		t.Fatal("saved pick must read back")
	}
}

// The saved preference must never leak into config with teeth: saving yes
// touches shared_auth only — skills stays exactly as the user left it.
func TestSaveSharedAuthDefaultNeverTouchesSkills(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "# my comment\nbase = \"debian:bookworm\"\nskills = [\"devloop\"] # keep\n\n[env]\nK = \"v\"\n")
	if err := SaveSharedAuthDefaultPick(home, "claude", "", true); err != nil {
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
	if !cfg.SharedAuth.HasYes("claude") {
		t.Fatalf("shared_auth = %+v", cfg.SharedAuth)
	}
}

// Saving no removes the agent from the list (and removes the line once the
// list empties); saving the stored answer again rewrites nothing.
func TestSaveSharedAuthDefaultNoRemovesAndIdempotent(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "shared_auth = [\"claude\", \"codex\"]\n")
	if err := SaveSharedAuthDefaultPick(home, "claude", "", false); err != nil {
		t.Fatal(err)
	}
	if got := parsedDefault(t, home).SharedAuth; !got.HasYes("codex") || got.HasYes("claude") {
		t.Fatalf("shared_auth = %+v", got)
	}
	if err := SaveSharedAuthDefaultPick(home, "codex", "", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readDefault(t, home), "shared_auth") {
		t.Fatalf("an emptied preference list must remove the line:\n%s", readDefault(t, home))
	}

	before := readDefault(t, home)
	if err := SaveSharedAuthDefaultPick(home, "claude", "", false); err != nil {
		t.Fatal(err)
	}
	if got := readDefault(t, home); got != before {
		t.Fatalf("saving the stored answer must not rewrite the file:\n%s", got)
	}
}

// A file the editor can't parse is refused with a remedy naming the config
// UI (P6: no error sends the user into the file) — never a guessed write.
func TestSaveSharedAuthDefaultRefusesUnparsableFile(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "skills = [\"unclosed\n")
	err := SaveSharedAuthDefaultPick(home, "claude", "", true)
	if err == nil || !strings.Contains(err.Error(), "byre config --global") {
		t.Fatalf("err = %v, want the config-UI remedy", err)
	}
	if got := readDefault(t, home); !strings.Contains(got, "unclosed") {
		t.Fatalf("a refused edit must leave the file untouched:\n%s", got)
	}
}

// A hand-formatted multi-line shared_auth list was a shape the old one-line
// rewriter refused; the style-preserving editor (ADR 0044) handles it — the
// whole construct is rewritten canonically because the edit targets it, and
// surrounding content survives.
func TestSaveSharedAuthDefaultHandlesMultilineList(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "# keep me\nshared_auth = [\n  \"codex\",\n]\nbase = \"node:22\"\n")
	if err := SaveSharedAuthDefaultPick(home, "claude", "", true); err != nil {
		t.Fatal(err)
	}
	got := readDefault(t, home)
	if !strings.Contains(got, "# keep me") || !strings.Contains(got, "base = \"node:22\"") {
		t.Fatalf("surrounding content must survive:\n%s", got)
	}
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SharedAuth.HasYes("codex") || !cfg.SharedAuth.HasYes("claude") {
		t.Fatalf("both answers must be stored: %+v", cfg.SharedAuth)
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
