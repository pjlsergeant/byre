package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/packages"
)

// presetApplyYesRefusedNothingWritten asserts the shared aftermath of every
// `apply --yes` refusal: no config, no marker, nothing installed.
func presetApplyYesRefusedNothingWritten(t *testing.T, dir, home, store string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "byre.config"))
	switch {
	case store == "" && !os.IsNotExist(err):
		t.Errorf("a refused apply must not write byre.config (err=%v, %s)", err, b)
	case store != "" && string(b) != store:
		t.Errorf("a refused apply must leave the stored byre.config untouched, got %s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "applied")); !os.IsNotExist(err) {
		t.Error("a refused apply must not write the applied marker")
	}
	if idx, _ := packages.ReadIndex(home); len(idx) != 0 {
		t.Errorf("a refused apply must install nothing, index has %v", idx)
	}
}

// Off a TTY, --yes is the consent: the config and the applied marker land,
// the chauffeur installs from a digested hint without a prompt, and the full
// review is on stderr -- into the CI log -- even though nobody answers it.
func TestPresetApplyYesOffTTYAppliesAndChauffeurs(t *testing.T) {
	p, proj := onboardPaths(t)
	uri, digest := publishSkill(t, "pete/linter", "1.0.0", "")
	shipPreset(t, proj, PresetName, `agent = "none"
skills = ["pete/linter"]
run_args = ["--privileged"]

[sources]
"pete/linter" = { uri = "`+uri+`", digest = "sha256:`+digest+`" }
`)
	s, stdout, stderr := testStreams("", false)
	if err := PresetApply(s, proj, "", true); err != nil {
		t.Fatalf("apply --yes off a TTY must succeed, got %v\n%s", err, stderr)
	}
	idx, err := packages.ReadIndex(p.Home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := idx["pete/linter"]; !ok {
		t.Fatal("the chauffeured install must land under --yes")
	}
	b, err := os.ReadFile(filepath.Join(p.Dir, "byre.config"))
	if err != nil || !strings.Contains(string(b), "pete/linter") {
		t.Fatalf("config not written: %v / %s", err, b)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "applied")); err != nil {
		t.Fatalf("applied marker not written: %v", err)
	}
	review := stderr.String()
	if !strings.Contains(review, "Apply preset -- the box this composes") || !strings.Contains(review, "⚠") || !strings.Contains(review, "--privileged") {
		t.Errorf("the full review must print to stderr under --yes:\n%s", review)
	}
	if strings.Contains(review, "\x1b[") {
		t.Errorf("off a TTY the review must be un-highlighted:\n%q", review)
	}
	if strings.Contains(stdout.String(), "the box this composes") {
		t.Errorf("the review belongs on stderr, not stdout:\n%s", stdout)
	}
}

// On a TTY with no credential rows, --yes skips the confirm: nothing is read
// from stdin and the apply lands.
func TestPresetApplyYesOnTTYSkipsTheConfirm(t *testing.T) {
	p, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "agent = \"codex\"\n")
	s, _, stderr := testStreams("", true)
	if err := PresetApply(s, proj, "", true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "[y/N]") {
		t.Errorf("--yes must not prompt:\n%s", stderr)
	}
	if b, err := os.ReadFile(filepath.Join(p.Dir, "byre.config")); err != nil || !strings.Contains(string(b), "codex") {
		t.Fatalf("config not written: %v / %s", err, b)
	}
}

// A digest-less https hint refuses under --yes, naming the row, before any
// fetch: unattended, nobody inspects what the URL serves. The URL is
// unroutable, so a fetch attempt would surface as a different error.
func TestPresetApplyYesRefusesUndigestedHTTPSHint(t *testing.T) {
	p, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, `agent = "none"
skills = ["pete/linter"]

[sources]
"pete/linter" = { uri = "https://example.invalid/linter/skill.toml" }
`)
	s, _, _ := testStreams("", false)
	err := PresetApply(s, proj, "", true)
	if err == nil || !strings.Contains(err.Error(), undigestedSourceRule) || !strings.Contains(err.Error(), `"pete/linter"`) || !strings.Contains(err.Error(), "https://example.invalid/linter/skill.toml") {
		t.Fatalf("a digest-less https hint must refuse naming the row, got %v", err)
	}
	presetApplyYesRefusedNothingWritten(t, p.Dir, p.Home, "")
}

// Every --yes refusal happens before byre's own store maintenance: a
// refused apply against a fresh BYRE_HOME leaves no AGENTS.md and no bundled
// mirror, so "before anything is written" holds as ADR 0063 states it.
func TestPresetApplyYesRefusesBeforeStoreMaintenance(t *testing.T) {
	for name, preset := range map[string]string{
		"credential row":        "agent = \"none\"\n[env_from_host]\nSTRIPE_KEY = \"" + credRow("Zm9vYmFy") + "\"\n",
		"undigested https hint": "agent = \"none\"\nskills = [\"pete/linter\"]\n\n[sources]\n\"pete/linter\" = { uri = \"https://example.invalid/linter/skill.toml\" }\n",
	} {
		t.Run(name, func(t *testing.T) {
			p, proj := onboardPaths(t)
			shipPreset(t, proj, PresetName, preset)
			s, _, _ := testStreams("", false)
			if err := PresetApply(s, proj, "", true); err == nil {
				t.Fatal("the apply must refuse")
			}
			for _, f := range []string{"AGENTS.md", "bundled"} {
				if _, err := os.Stat(filepath.Join(p.Home, f)); !os.IsNotExist(err) {
					t.Errorf("a refused --yes apply must not write %s into BYRE_HOME (err=%v)", f, err)
				}
			}
		})
	}
}

// An https preset URI refuses under --yes before any fetch: unattended,
// nobody inspects what it serves, and nothing committed pins it. The URL is
// unroutable, so a fetch attempt would surface as a different error.
func TestPresetApplyYesRefusesHTTPSPresetURI(t *testing.T) {
	p, proj := onboardPaths(t)
	s, _, _ := testStreams("", false)
	err := PresetApply(s, proj, "https://example.invalid/team/byre.preset", true)
	if err == nil || !strings.Contains(err.Error(), remotePresetRule) || !strings.Contains(err.Error(), "https://example.invalid/team/byre.preset") {
		t.Fatalf("an https preset URI must refuse naming the URI, got %v", err)
	}
	presetApplyYesRefusedNothingWritten(t, p.Dir, p.Home, "")
}

// An explicit preset path passes --yes: it names bytes the caller controls.
func TestPresetApplyYesAllowsPresetPath(t *testing.T) {
	p, proj := onboardPaths(t)
	path := filepath.Join(t.TempDir(), "team.preset")
	mustWriteFile(t, path, []byte("agent = \"none\"\n"), 0o644)
	s, _, stderr := testStreams("", false)
	if err := PresetApply(s, proj, path, true); err != nil {
		t.Fatalf("a preset path must pass under --yes, got %v\n%s", err, stderr)
	}
	if b, err := os.ReadFile(filepath.Join(p.Dir, "byre.config")); err != nil || !strings.Contains(string(b), "none") {
		t.Fatalf("config not written: %v / %s", err, b)
	}
}

// A path source needs no digest: it names local bytes, and the install lands.
func TestPresetApplyYesAllowsUndigestedPathSource(t *testing.T) {
	p, proj := onboardPaths(t)
	uri, _ := publishSkill(t, "pete/linter", "1.0.0", "")
	shipPreset(t, proj, PresetName, `agent = "none"
skills = ["pete/linter"]

[sources]
"pete/linter" = { uri = "`+uri+`" }
`)
	s, _, stderr := testStreams("", false)
	if err := PresetApply(s, proj, "", true); err != nil {
		t.Fatalf("a path source must pass under --yes, got %v\n%s", err, stderr)
	}
	if idx, _ := packages.ReadIndex(p.Home); idx["pete/linter"].Digest == "" {
		t.Fatal("the path-sourced install must land")
	}
}

// Credential rows on EITHER side of the replacement refuse --yes, naming the
// row and pointing at env_from_host -- on a TTY too: the rule is about the
// human's confirm (ADR 0057), not the terminal.
func TestPresetApplyYesRefusesCredentialRows(t *testing.T) {
	row := credRow("Zm9vYmFy")
	cases := []struct {
		name, preset, store, side string
	}{
		{"preset carries a row", "agent = \"none\"\n[env_from_host]\nSTRIPE_KEY = \"" + row + "\"\n", "", "the preset sets STRIPE_KEY"},
		{"stored config carries a row", "agent = \"none\"\n", "agent = \"none\"\n[env_from_host]\nSTRIPE_KEY = \"" + row + "\"\n", "your current byre.config sets STRIPE_KEY"},
		{"file row in the preset", "agent = \"none\"\n[env_from_host]\nGCP_JSON = \"" + "encrypted-file:Zm9v" + "\"\n", "", "the preset sets GCP_JSON"},
	}
	for _, tc := range cases {
		for _, tty := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s tty=%v", tc.name, tty), func(t *testing.T) {
				p, proj := onboardPaths(t)
				shipPreset(t, proj, PresetName, tc.preset)
				if tc.store != "" {
					mustWriteFile(t, filepath.Join(p.Dir, "byre.config"), []byte(tc.store), 0o644)
				}
				// A "y" waiting on stdin proves the refusal does not fall back
				// to asking: --yes never reaches the confirm with rows present.
				s, _, _ := testStreams("y\n", tty)
				err := PresetApply(s, proj, "", true)
				if err == nil || !strings.Contains(err.Error(), unattendedCredentialsRule) || !strings.Contains(err.Error(), tc.side) || !strings.Contains(err.Error(), "env_from_host") {
					t.Fatalf("tty=%v: credential rows must refuse --yes naming %q and env_from_host, got %v", tty, tc.side, err)
				}
				presetApplyYesRefusedNothingWritten(t, p.Dir, p.Home, tc.store)
			})
		}
	}
}

// A ciphertext shadowed by an [env] literal on BOTH sides, with identical
// [credentials] blocks, draws no credential review line -- the literal owns
// the key on each side -- but the file still stores the row, so --yes still
// refuses naming it.
func TestPresetApplyYesRefusesARowShadowedOnBothSides(t *testing.T) {
	doc := "agent = \"none\"\n[env]\nSTRIPE_KEY = \"literal\"\n[env_from_host]\nSTRIPE_KEY = \"" + credRow("Zm9vYmFy") + "\"\n" + credBlock(credIdentityA, credRecipientA)
	if lines := credentialReviewLines(presetSubject, []byte(doc), []byte(doc)); len(lines) != 0 {
		t.Fatalf("precondition: the review must have nothing to say about this pair, got %v", lines)
	}
	p, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, doc)
	mustWriteFile(t, filepath.Join(p.Dir, "byre.config"), []byte(doc), 0o644)
	s, _, _ := testStreams("", false)
	err := PresetApply(s, proj, "", true)
	if err == nil || !strings.Contains(err.Error(), unattendedCredentialsRule) || !strings.Contains(err.Error(), "the preset sets STRIPE_KEY") || !strings.Contains(err.Error(), "your current byre.config sets STRIPE_KEY") {
		t.Fatalf("a shadowed credential row must refuse --yes naming the row on each side, got %v", err)
	}
	presetApplyYesRefusedNothingWritten(t, p.Dir, p.Home, doc)
}

// A [credentials] identity arriving with no rows is still a credential review
// line only a human can answer, so --yes refuses it too.
func TestPresetApplyYesRefusesAnIdentityArriving(t *testing.T) {
	p, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "agent = \"none\"\n"+credBlock(credIdentityA, credRecipientA))
	s, _, _ := testStreams("", false)
	err := PresetApply(s, proj, "", true)
	if err == nil || !strings.Contains(err.Error(), unattendedCredentialsRule) || !strings.Contains(err.Error(), "brings its own credentials identity") {
		t.Fatalf("an arriving identity must refuse --yes, got %v", err)
	}
	presetApplyYesRefusedNothingWritten(t, p.Dir, p.Home, "")
}

// Without --yes, the TTY path is unchanged by the credential rule: rows are
// shown and the human's confirm decides.
func TestPresetApplyWithoutYesStillConfirmsCredentialRows(t *testing.T) {
	p, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "agent = \"none\"\n[env_from_host]\nSTRIPE_KEY = \""+credRow("Zm9vYmFy")+"\"\n")
	s, _, stderr := testStreams("y\n", true)
	if err := PresetApply(s, proj, "", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "STRIPE_KEY: credential row appeared") {
		t.Errorf("the credential line must render for the human:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); err != nil {
		t.Fatalf("a confirmed TTY apply with credential rows must write: %v", err)
	}
}
