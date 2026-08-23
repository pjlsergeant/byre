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
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"), true)
	if err != nil {
		t.Fatalf("edited default.config must still parse: %v", err)
	}
	return cfg
}

// A yes with a companion creates the file and stores the pick. A yes
// WITHOUT a companion records nothing: yes-without-pick is parse-only
// legacy state (ADR 0049 amendment 2026-08-23), so there is nothing true
// to write and the offer re-asks.
func TestSaveSharedAuthDefaultYesCreatesFileAndPick(t *testing.T) {
	home := t.TempDir()
	if err := SaveSharedAuthDefaultPick(home, "claude", "claude-shared-auth", true); err != nil {
		t.Fatal(err)
	}
	got := parsedDefault(t, home).StoredSharedAuth()
	if got.CompanionPick("claude") != "claude-shared-auth" {
		t.Fatalf("shared_auth = %+v", got)
	}
	if !SharedAuthPreference(home, "claude") {
		t.Fatal("saved yes must read back as the preference")
	}
}

func TestSaveSharedAuthDefaultYesWithoutCompanionRecordsNothing(t *testing.T) {
	home := t.TempDir()
	if err := SaveSharedAuthDefaultPick(home, "claude", "", true); err != nil {
		t.Fatal(err)
	}
	// Nothing to record means nothing to write: no default.config appears.
	if _, err := os.Stat(filepath.Join(home, "default.config")); !os.IsNotExist(err) {
		t.Fatalf("a no-op answer must not create the file (stat err: %v):\n%s",
			err, readDefault(t, home))
	}
	if SharedAuthPreference(home, "claude") {
		t.Fatal("no preference may read back")
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
	got := parsedDefault(t, home).StoredSharedAuth()
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
	if err := SaveSharedAuthDefaultPick(home, "claude", "claude-shared-auth", true); err != nil {
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
	if !cfg.StoredSharedAuth().HasYes("claude") {
		t.Fatalf("shared_auth = %+v", cfg.StoredSharedAuth())
	}
}

// Saving no removes the agent's pick (and removes the line once the picks
// empty); saving the stored answer again rewrites nothing. Any save also
// drops legacy yes-only entries wholesale (presence-triggered cleanup):
// they encode a state that is no longer saveable.
func TestSaveSharedAuthDefaultNoRemovesAndIdempotent(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home,
		"[defaults]\nshared_auth = { \"claude\" = \"c\", \"codex\" = \"x\" }\n")
	if err := SaveSharedAuthDefaultPick(home, "claude", "", false); err != nil {
		t.Fatal(err)
	}
	if got := parsedDefault(t, home).StoredSharedAuth(); !got.HasYes("codex") || got.HasYes("claude") {
		t.Fatalf("shared_auth = %+v", got)
	}
	if err := SaveSharedAuthDefaultPick(home, "codex", "", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readDefault(t, home), "shared_auth") {
		t.Fatalf("an emptied preference must remove the line:\n%s", readDefault(t, home))
	}

	before := readDefault(t, home)
	if err := SaveSharedAuthDefaultPick(home, "claude", "", false); err != nil {
		t.Fatal(err)
	}
	if got := readDefault(t, home); got != before {
		t.Fatalf("saving the stored answer must not rewrite the file:\n%s", got)
	}
}

// SaveDefault is the OTHER default.config writer (favourites only), and
// "the next save drops it" must be true of it too: an onboarding that saves
// favourites with no shared-auth offer still cleans legacy state out of the
// file it rewrites — the top-level spelling's complete picks migrate under
// [defaults], its yes-only entries drop, and a canonical file's construct
// is left byte-untouched.
func TestSaveDefaultCleansLegacySharedAuthState(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home,
		"base = \"node:22\"\n[shared_auth]\nclaude = \"claude-shared-auth\"\ncodex = \"\"\n")
	if err := SaveDefault(home, "go", "claude"); err != nil {
		t.Fatal(err)
	}
	got := readDefault(t, home)
	sec := strings.Index(got, "[defaults]")
	if sec < 0 || strings.Contains(got[:sec], "shared_auth") {
		t.Fatalf("the top-level spelling must migrate under [defaults]:\n%s", got)
	}
	cfg := parsedDefault(t, home)
	if !cfg.SharedAuthLegacy.Empty() {
		t.Errorf("the legacy home must be empty after the cleanup: %+v", cfg.SharedAuthLegacy)
	}
	pref := cfg.StoredSharedAuth()
	if pref.CompanionPick("claude") != "claude-shared-auth" {
		t.Errorf("the complete pick must survive the migration: %+v", pref)
	}
	if pref.HasYes("codex") {
		t.Errorf("the empty-pick half-answer must be dropped: %+v", pref)
	}
	if cfg.Template != "go" || cfg.Agent != "claude" {
		t.Errorf("the favourites save must still land: %+v", cfg)
	}

	// Canonical file: the shared_auth construct is untouched by a
	// favourites save (presence is the trigger, ADR 0044 keeps the bytes).
	home2 := t.TempDir()
	writeDefault(t, home2,
		"[defaults]\nshared_auth = { \"claude\" = \"claude-shared-auth\" } # my note\n")
	if err := SaveDefault(home2, "go", "claude"); err != nil {
		t.Fatal(err)
	}
	if got := readDefault(t, home2); !strings.Contains(got, "# my note") {
		t.Errorf("a canonical construct must keep its bytes:\n%s", got)
	}
}

// A legacy array (yes-only entries) is cleaned out by ANY save of the file,
// including one whose own answer records nothing — the state it encodes is
// unrepresentable, and presence is the trigger.
func TestSaveSharedAuthDefaultDropsLegacyYesEntriesOnAnySave(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "[defaults]\nshared_auth = [\"claude\", \"codex\"]\n")
	if err := SaveSharedAuthDefaultPick(home, "grok", "", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readDefault(t, home), "shared_auth") {
		t.Fatalf("legacy yes-only entries must be dropped on save:\n%s", readDefault(t, home))
	}
	if got := parsedDefault(t, home).StoredSharedAuth(); !got.Empty() {
		t.Fatalf("shared_auth = %+v", got)
	}
}

// A file the editor can't parse is refused — never a guessed write. P6's
// scope note governs what the refusal must then say: no byre editor can be
// the remedy (each one parses this file before it opens), so the message
// itself has to be precise enough to fix the file by hand — the path, and
// where in it.
func TestSaveSharedAuthDefaultRefusesUnparsableFile(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "skills = [\"unclosed\n")
	err := SaveSharedAuthDefaultPick(home, "claude", "", true)
	if err == nil {
		t.Fatal("an unparsable default.config must be refused, not guessed at")
	}
	for _, want := range []string{filepath.Join(home, "default.config"), "line 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must carry %q, got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "byre config") {
		t.Errorf("the remedy must not name a command that refuses this file too: %v", err)
	}
	if got := readDefault(t, home); !strings.Contains(got, "unclosed") {
		t.Fatalf("a refused edit must leave the file untouched:\n%s", got)
	}
}

// A hand-formatted multi-line shared_auth list was a shape the old one-line
// rewriter refused; the style-preserving editor (ADR 0044) handles it — the
// whole construct is rewritten canonically because the edit targets it, and
// surrounding content survives. The rewrite also MIGRATES a top-level
// spelling under [defaults], taking any comment glued to it (that is what
// glued means) — and since the 2026-08-23 ADR 0049 amendment the legacy
// yes-only entries themselves are DROPPED, so what lands is the new pick
// alone.
func TestSaveSharedAuthDefaultHandlesMultilineList(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "# keep me\nbase = \"node:22\"\n\nshared_auth = [\n  \"codex\",\n]\n")
	if err := SaveSharedAuthDefaultPick(home, "claude", "claude-shared-auth", true); err != nil {
		t.Fatal(err)
	}
	got := readDefault(t, home)
	if !strings.Contains(got, "# keep me") || !strings.Contains(got, "base = \"node:22\"") {
		t.Fatalf("surrounding content must survive:\n%s", got)
	}
	// Position, not presence: both homes spell the key the same, so the
	// migration is proved by every occurrence sitting UNDER [defaults].
	sec := strings.Index(got, "[defaults]")
	if sec < 0 {
		t.Fatalf("the preference must land under [defaults]:\n%s", got)
	}
	if strings.Contains(got[:sec], "shared_auth") {
		t.Errorf("the top-level spelling must be migrated away, not left beside its new home:\n%s", got)
	}
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"), true)
	if err != nil {
		t.Fatal(err)
	}
	got2 := cfg.StoredSharedAuth()
	if got2.CompanionPick("claude") != "claude-shared-auth" {
		t.Fatalf("the new pick must be stored: %+v", got2)
	}
	if got2.HasYes("codex") {
		t.Errorf("the legacy yes-only entry must be dropped by the save: %+v", got2)
	}
}

// A legacy top-level spelling is cleaned on the next write even when the
// answer is UNCHANGED: presence is the trigger. Gated on a changed answer,
// a user who keeps answering the same way keeps the legacy construct
// indefinitely -- exactly what "the next save cleans it" must not mean.
// The top-level array here carries only yes-only entries, so the cleanup
// removes the construct outright (nothing saveable survives to migrate).
func TestSaveSharedAuthDefaultCleansLegacySpellingOnAnUnchangedAnswer(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "base = \"node:22\"\nshared_auth = [\"claude\"]\n")
	// Same answer that is already stored: yes for claude, no companion.
	if err := SaveSharedAuthDefaultPick(home, "claude", "", true); err != nil {
		t.Fatal(err)
	}
	got := readDefault(t, home)
	if strings.Contains(got, "shared_auth") {
		t.Errorf("nothing saveable survives: the legacy construct must be removed, not migrated:\n%s", got)
	}
	cfg := parsedDefault(t, home)
	if !cfg.SharedAuthLegacy.Empty() {
		t.Errorf("the legacy home must be empty after the cleanup: %+v", cfg.SharedAuthLegacy)
	}
	if !cfg.StoredSharedAuth().Empty() {
		t.Errorf("yes-without-pick must not be re-persisted anywhere: %+v", cfg.StoredSharedAuth())
	}
	if !strings.Contains(got, "base = \"node:22\"") {
		t.Errorf("surrounding content must survive:\n%s", got)
	}
	// Cleaned once, the next identical answer writes nothing.
	before := readDefault(t, home)
	if err := SaveSharedAuthDefaultPick(home, "claude", "", true); err != nil {
		t.Fatal(err)
	}
	if after := readDefault(t, home); after != before {
		t.Errorf("an unchanged answer with nothing left to clean must not rewrite the file:\n%s", after)
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

// SaveDefault's parse door is tomldoc's, not config.Parse's, so it was the
// one refusal left saying "fix this file" without saying where. Same rule as
// its shared-auth sibling: no byre command can be the remedy, so the message
// has to be enough to fix the file by hand.
func TestSaveDefaultRefusesUnparsableFileWithPosition(t *testing.T) {
	home := t.TempDir()
	writeDefault(t, home, "template = \"go\"\nagent = \"unclosed\n")
	err := SaveDefault(home, "go", "claude")
	if err == nil {
		t.Fatal("an unparsable default.config must be refused, not guessed at")
	}
	for _, want := range []string{filepath.Join(home, "default.config"), "line 2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must carry %q, got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "byre config") {
		t.Errorf("the remedy must not name a command that refuses this file too: %v", err)
	}
	if got := readDefault(t, home); !strings.Contains(got, "unclosed") {
		t.Fatalf("a refused edit must leave the file untouched:\n%s", got)
	}
}
