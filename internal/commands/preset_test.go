package commands

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pjlsergeant/byre/internal/packages"
)

// shipPreset writes a repo-shipped preset file (byre.preset by default).
func shipPreset(t *testing.T, projectDir, name, content string) string {
	t.Helper()
	p := filepath.Join(projectDir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPresetApplyWritesConfigAndMarker(t *testing.T) {
	p, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "agent = \"codex\"\nrun_args = [\"--privileged\"]\n")

	// One yes: the apply confirm (no missing packages, no chauffeur stops).
	s, _, out := testStreams("y\n", true)
	if err := PresetApply(s, proj, ""); err != nil {
		t.Fatal(err)
	}
	// The review must surface the dangerous run_args.
	if !strings.Contains(out.String(), "--privileged") {
		t.Errorf("apply review should surface run_args grant:\n%s", out.String())
	}
	b, err := os.ReadFile(filepath.Join(p.Dir, "byre.config"))
	if err != nil || !strings.Contains(string(b), "codex") {
		t.Fatalf("config not written: %v / %s", err, b)
	}
	rec, err := os.ReadFile(filepath.Join(p.Dir, "applied"))
	if err != nil {
		t.Fatalf("applied marker not written: %v", err)
	}
	// Marker = hash + source (D16c step 6).
	if !strings.Contains(string(rec), PresetName) {
		t.Errorf("marker should record the source: %q", rec)
	}
	// Steady state (D17 state 2): no note.
	if note := presetNote(proj, p); note != "" {
		t.Errorf("applied+matching preset must be silent, got %q", note)
	}
}

func TestPresetApplyRefusesNonTTY(t *testing.T) {
	p, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "agent = \"none\"\n")
	s := discardStreams()
	if err := PresetApply(s, proj, ""); err == nil || !strings.Contains(err.Error(), "TTY") {
		t.Fatalf("non-TTY apply must refuse (the review is the point), got %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "applied")); !os.IsNotExist(err) {
		t.Error("nothing may be written on refusal")
	}
}

func TestPresetApplyDeclineWritesNothing(t *testing.T) {
	p, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "agent = \"codex\"\n")
	s, _, _ := testStreams("n\n", true)
	if err := PresetApply(s, proj, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); !os.IsNotExist(err) {
		t.Error("declined apply must not write byre.config")
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "applied")); !os.IsNotExist(err) {
		t.Error("declined apply must not write the marker")
	}
	// No sticky decline exists anymore (D17): the state stays "unapplied"
	// and the passive note keeps showing. Nothing re-prompts on its own.
	if state, _ := presetState(proj, p); state != "unapplied" {
		t.Errorf("state = %q, want unapplied", state)
	}
}

// mutateOnRead runs fn just before the first Read — the moment apply reads
// the confirmation — modeling a preset edited while the human was reviewing.
type mutateOnRead struct {
	r    io.Reader
	fn   func()
	once sync.Once
}

func (m *mutateOnRead) Read(p []byte) (int, error) {
	m.once.Do(m.fn)
	return m.r.Read(p)
}

// Consent is to the bytes that were reviewed: if the preset changes between
// the review and the under-lock write, apply must abort.
func TestPresetApplyAbortsOnChangeUnderReview(t *testing.T) {
	p, proj := onboardPaths(t)
	path := shipPreset(t, proj, PresetName, "agent = \"codex\"\n")
	in := &mutateOnRead{r: strings.NewReader("y\n"), fn: func() {
		os.WriteFile(path, []byte("agent = \"codex\"\nrun_args = [\"--privileged\"]\n"), 0o644)
	}}
	s := Streams{Out: io.Discard, Err: io.Discard, In: in, TTY: true}
	err := PresetApply(s, proj, "")
	if err == nil || !strings.Contains(err.Error(), "changed while you were reviewing") {
		t.Fatalf("changed preset must abort, got %v", err)
	}
	if _, serr := os.Stat(filepath.Join(p.Dir, "byre.config")); !os.IsNotExist(serr) {
		t.Error("aborted apply must not write")
	}
}

func TestPresetDriftStates(t *testing.T) {
	p, proj := onboardPaths(t)
	// No preset file at all.
	if state, _ := presetState(proj, p); state != "" {
		t.Fatalf("no file: state %q", state)
	}
	// State 1: shipped, not applied.
	shipPreset(t, proj, PresetName, "agent = \"none\"\n")
	if state, _ := presetState(proj, p); state != "unapplied" {
		t.Fatalf("want unapplied, got %q", state)
	}
	if note := presetNote(proj, p); !strings.Contains(note, "not applied") || !strings.Contains(note, "byre preset apply") {
		t.Fatalf("state-1 note: %q", note)
	}
	// Apply it -> state 2, silent.
	s, _, _ := testStreams("y\n", true)
	if err := PresetApply(s, proj, ""); err != nil {
		t.Fatal(err)
	}
	if state, _ := presetState(proj, p); state != "applied" {
		t.Fatalf("want applied, got %q", state)
	}
	// Edit the repo preset -> state 3, the outdated-lockfile note.
	shipPreset(t, proj, PresetName, "agent = \"none\"\napt = [\"jq\"]\n")
	if state, _ := presetState(proj, p); state != "diverged" {
		t.Fatalf("want diverged, got %q", state)
	}
	if note := presetNote(proj, p); !strings.Contains(note, "differs from the version you applied") {
		t.Fatalf("state-3 note: %q", note)
	}
	// Live-config edits are NOT drift: rewriting the store byre.config alone
	// must not change the state.
	if err := os.WriteFile(filepath.Join(p.Dir, "byre.config"), []byte("agent = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, _ := presetState(proj, p); state != "diverged" {
		t.Fatalf("live edits are not drift; state = %q", state)
	}
}

func TestPresetLegacyNameAccepted(t *testing.T) {
	p, proj := onboardPaths(t)
	shipPreset(t, proj, "byre.config", "agent = \"none\"\n")
	state, legacy := presetState(proj, p)
	if state != "unapplied" || !legacy {
		t.Fatalf("legacy byre.config must count as an unapplied preset (state=%q legacy=%v)", state, legacy)
	}
	if note := presetNote(proj, p); !strings.Contains(note, "legacy name") {
		t.Fatalf("legacy note must carry the rename hint: %q", note)
	}
	s, _, out := testStreams("y\n", true)
	if err := PresetApply(s, proj, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "legacy name") {
		t.Errorf("apply must print the rename note:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); err != nil {
		t.Fatal("legacy-named preset must still apply")
	}
}

func TestPresetPrefersConventionalName(t *testing.T) {
	_, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "agent = \"none\"\napt = [\"preset-marker\"]\n")
	shipPreset(t, proj, "byre.config", "agent = \"none\"\napt = [\"legacy-marker\"]\n")
	content, source, legacy, err := readPreset(proj, "")
	if err != nil {
		t.Fatal(err)
	}
	if legacy || !strings.Contains(source, PresetName) || !strings.Contains(string(content), "preset-marker") {
		t.Fatalf("byre.preset must win over the legacy name: %q %v", source, legacy)
	}
}

func TestPresetApplyRejectsInvalidLayer(t *testing.T) {
	_, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "agent = \"none\"\nnot_a_key = true\n")
	s, _, _ := testStreams("y\n", true)
	if err := PresetApply(s, proj, ""); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("invalid preset must refuse before any prompt, got %v", err)
	}
}

// The chauffeur (D16c step 3): a preset referencing a missing package with a
// [sources] hint walks the user through that package's own install consent;
// the apply then reviews a complete catalog.
func TestPresetApplyChauffeursHintedInstall(t *testing.T) {
	p, proj := onboardPaths(t)
	uri, digest := publishSkill(t, "pete/linter", "1.0.0", "")
	shipPreset(t, proj, PresetName, `agent = "none"
skills = ["pete/linter"]

[sources]
"pete/linter" = { uri = "`+uri+`", digest = "sha256:`+digest+`" }
`)
	// Two answers: the chauffeured install's confirm, then the apply confirm.
	s, _, out := testStreams("y\ny\n", true)
	if err := PresetApply(s, proj, ""); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "pete/linter") || !strings.Contains(text, "not installed") {
		t.Fatalf("chauffeur must announce the missing package:\n%s", text)
	}
	idx, err := packages.ReadIndex(p.Home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := idx["pete/linter"]; !ok {
		t.Fatal("chauffeured install must land the package")
	}
	if strings.Contains(text, "grants unknown") {
		t.Errorf("nothing should stay unknown after a successful chauffeur:\n%s", text)
	}
	b, _ := os.ReadFile(filepath.Join(p.Dir, "byre.config"))
	if !strings.Contains(string(b), "pete/linter") {
		t.Fatalf("applied config must carry the reference: %s", b)
	}
}

// Declining a chauffeured install still completes the apply honestly: the
// reference stays in the written config, the review marks it, the box fails
// loudly at develop (D16c).
func TestPresetApplyDeclinedInstallStillApplies(t *testing.T) {
	p, proj := onboardPaths(t)
	uri, digest := publishSkill(t, "pete/linter", "1.0.0", "")
	shipPreset(t, proj, PresetName, `agent = "none"
skills = ["pete/linter"]

[sources]
"pete/linter" = { uri = "`+uri+`", digest = "sha256:`+digest+`" }
`)
	// Decline the install, accept the apply.
	s, _, out := testStreams("n\ny\n", true)
	if err := PresetApply(s, proj, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "grants unknown") {
		t.Fatalf("still-missing package must be marked in the review:\n%s", out.String())
	}
	b, err := os.ReadFile(filepath.Join(p.Dir, "byre.config"))
	if err != nil || !strings.Contains(string(b), "pete/linter") {
		t.Fatalf("the reference must stay in the written config: %v %s", err, b)
	}
	idx, _ := packages.ReadIndex(p.Home)
	if _, ok := idx["pete/linter"]; ok {
		t.Fatal("declined install must not land")
	}
}

// Inspect is the review without the write: read-only, works in a pipe,
// prints exact install commands instead of prompting (the solicitation rule).
func TestPresetInspectReadOnly(t *testing.T) {
	p, proj := onboardPaths(t)
	uri, digest := publishSkill(t, "pete/linter", "1.0.0", "")
	shipPreset(t, proj, PresetName, `agent = "none"
skills = ["pete/linter"]

[sources]
"pete/linter" = { uri = "`+uri+`", digest = "sha256:`+digest+`" }
`)
	s, outBuf, errBuf := testStreams("", false) // non-TTY: inspect still works
	if err := PresetInspect(s, proj, ""); err != nil {
		t.Fatal(err)
	}
	text := outBuf.String() + errBuf.String()
	if !strings.Contains(text, "grants unknown") {
		t.Fatalf("inspect must mark missing packages:\n%s", text)
	}
	if !strings.Contains(text, "byre skill install "+uri) {
		t.Fatalf("inspect must print the exact install command:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); !os.IsNotExist(err) {
		t.Error("inspect must write nothing")
	}
	idx, _ := packages.ReadIndex(p.Home)
	if len(idx) != 0 {
		t.Error("inspect must install nothing")
	}
}

// The D17 record sweep: pre-preset `adopted` records migrate to `applied`
// markers (history preserved into the drift states); `declined` records are
// deleted (nothing left to decline).
func TestAdoptionRecordSweep(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	pdir := filepath.Join(home, "projects", "someproj")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(pdir, "adopted"), []byte("deadbeef"), 0o644)
	os.WriteFile(filepath.Join(pdir, "declined"), []byte("cafef00d"), 0o644)
	if err := packages.EnsureStore(home, nil, "v9.9.9", nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(pdir, "applied"))
	if err != nil || !strings.HasPrefix(string(b), "deadbeef") {
		t.Fatalf("adopted must migrate to applied: %v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(pdir, "adopted")); !os.IsNotExist(err) {
		t.Error("adopted record must be removed after migration")
	}
	if _, err := os.Stat(filepath.Join(pdir, "declined")); !os.IsNotExist(err) {
		t.Error("declined record must be swept")
	}
}

// Develop never prompts about a repo preset (D17): passive note only.
func TestDevelopPresetNoteIsPassive(t *testing.T) {
	p, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "agent = \"none\"\n")
	note := presetNote(proj, p)
	if strings.Contains(note, "?") || strings.Contains(strings.ToLower(note), "[y/n]") {
		t.Fatalf("the note must never be a question: %q", note)
	}
	if !strings.Contains(note, "byre preset apply") {
		t.Fatalf("the note must point at the solicited flow: %q", note)
	}
}

// A preset naming a bundled template applies on a fresh home (the catalog
// serves bundled from embed; nothing needs materializing).
func TestPresetBuiltinTemplateOnFreshHome(t *testing.T) {
	p, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "template = \"go\"\nagent = \"none\"\n")
	s, _, _ := testStreams("y\n", true)
	if err := PresetApply(s, proj, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); err != nil {
		t.Fatal(err)
	}
	if state, _ := presetState(proj, p); state != "applied" {
		t.Fatalf("state = %q", state)
	}
}

// An explicit path argument works (a preset can come from anywhere, D16a).
func TestPresetApplyExplicitPath(t *testing.T) {
	p, proj := onboardPaths(t)
	elsewhere := filepath.Join(t.TempDir(), "team.preset")
	if err := os.WriteFile(elsewhere, []byte("agent = \"none\"\napt = [\"jq\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _, _ := testStreams("y\n", true)
	if err := PresetApply(s, proj, elsewhere); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(p.Dir, "byre.config"))
	if err != nil || !strings.Contains(string(b), "jq") {
		t.Fatalf("explicit-path preset not applied: %v %s", err, b)
	}
	rec, _ := os.ReadFile(filepath.Join(p.Dir, "applied"))
	if !strings.Contains(string(rec), "team.preset") {
		t.Errorf("marker must record the explicit source: %q", rec)
	}
}

// The rendered preset body must keep its line structure (EscapeTerminal
// alone strips newlines) while still neutralizing ANSI in hostile content.
func TestPresetReviewBodyKeepsNewlines(t *testing.T) {
	_, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "agent = \"none\"\napt = [\"jq\"]\n")
	s, _, errBuf := testStreams("n\n", true)
	if err := PresetApply(s, proj, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "agent = \"none\"\napt = [\"jq\"]") {
		t.Fatalf("preset body must render with newlines intact:\n%s", errBuf.String())
	}
	if got := escapeMultiline("a\x1b[31mred\nb"); got != "ared\nb" {
		t.Fatalf("escapeMultiline = %q", got)
	}
}

// Inspect must mutate NOTHING in the store -- no mirror regen, no record
// sweep (its "Nothing written" line is a promise, codex P1).
func TestPresetInspectMutatesNothing(t *testing.T) {
	p, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "agent = \"none\"\n")
	// Plant records the store-ensure sweep would touch.
	os.WriteFile(filepath.Join(p.Dir, "adopted"), []byte("deadbeef"), 0o644)
	os.WriteFile(filepath.Join(p.Dir, "declined"), []byte("cafef00d"), 0o644)
	s, _, _ := testStreams("", false)
	if err := PresetInspect(s, proj, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "adopted")); err != nil {
		t.Error("inspect must not run the record sweep")
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "declined")); err != nil {
		t.Error("inspect must not delete declined records")
	}
	if _, err := os.Stat(filepath.Join(p.Home, "bundled")); !os.IsNotExist(err) {
		t.Error("inspect must not regenerate the bundled mirror")
	}
}

// Consent is to replacing the REVIEWED store config: a concurrent write to
// byre.config between review and the locked landing must abort (codex P1).
func TestPresetApplyAbortsOnStoreConfigChange(t *testing.T) {
	p, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "agent = \"codex\"\n")
	storePath := filepath.Join(p.Dir, "byre.config")
	os.MkdirAll(p.Dir, 0o755)
	os.WriteFile(storePath, []byte("agent = \"claude\"\n"), 0o644)
	in := &mutateOnRead{r: strings.NewReader("y\n"), fn: func() {
		os.WriteFile(storePath, []byte("agent = \"grok\"\n"), 0o644)
	}}
	s := Streams{Out: io.Discard, Err: io.Discard, In: in, TTY: true}
	err := PresetApply(s, proj, "")
	if err == nil || !strings.Contains(err.Error(), "byre.config changed while you were reviewing") {
		t.Fatalf("concurrent store-config change must abort, got %v", err)
	}
	b, _ := os.ReadFile(storePath)
	if string(b) != "agent = \"grok\"\n" {
		t.Fatalf("aborted apply must not overwrite the concurrent edit: %s", b)
	}
}

// The sweep must never delete the only history copy: a failed applied-write
// keeps the adopted record for the next sweep (both reviewers, P1).
func TestAdoptionRecordSweepKeepsHistoryOnWriteFailure(t *testing.T) {
	home := t.TempDir()
	pdir := filepath.Join(home, "projects", "someproj")
	os.MkdirAll(pdir, 0o755)
	os.WriteFile(filepath.Join(pdir, "adopted"), []byte("deadbeef"), 0o644)
	// Make the applied write fail: applied's target is an unwritable dir --
	// simulate by making the project dir read-only.
	if err := os.Chmod(pdir, 0o555); err != nil {
		t.Skip("cannot chmod")
	}
	t.Cleanup(func() { os.Chmod(pdir, 0o755) })
	if err := packages.EnsureStore(home, nil, "v9.9.9", nil); err != nil {
		t.Fatal(err)
	}
	os.Chmod(pdir, 0o755)
	if _, err := os.Stat(filepath.Join(pdir, "adopted")); err != nil {
		t.Fatal("adopted must survive a failed migration write")
	}
	if _, err := os.Stat(filepath.Join(pdir, "applied")); !os.IsNotExist(err) {
		t.Fatal("no applied marker should exist after the failed write")
	}
	// Next sweep (writable again) completes the migration.
	if err := packages.EnsureStore(home, nil, "v9.9.8", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pdir, "applied")); err != nil {
		t.Fatal("retry sweep must complete the migration")
	}
	if _, err := os.Stat(filepath.Join(pdir, "adopted")); !os.IsNotExist(err) {
		t.Fatal("adopted removed only after a successful write")
	}
}

// file: preset sources get the real URI parse, not a prefix trim (grok).
func TestPresetReadFileURI(t *testing.T) {
	_, proj := onboardPaths(t)
	elsewhere := filepath.Join(t.TempDir(), "team.preset")
	os.WriteFile(elsewhere, []byte("agent = \"none\"\n"), 0o644)
	for _, arg := range []string{elsewhere, "file://" + elsewhere, "file://localhost" + elsewhere} {
		if _, _, _, err := readPreset(proj, arg); err != nil {
			t.Errorf("readPreset(%q): %v", arg, err)
		}
	}
	if _, _, _, err := readPreset(proj, "file://evil.example/x"); err == nil {
		t.Error("non-local file host must be rejected")
	}
	// Exact-basename legacy detection: not-byre.config is NOT legacy-named.
	notLegacy := filepath.Join(t.TempDir(), "not-byre.config")
	os.WriteFile(notLegacy, []byte("agent = \"none\"\n"), 0o644)
	if _, _, legacy, err := readPreset(proj, notLegacy); err != nil || legacy {
		t.Errorf("suffix match must not trigger the legacy note (legacy=%v err=%v)", legacy, err)
	}
}

// An EXISTING but unreadable byre.config must abort apply before the review
// -- both reads failing must never read as "no current config" (codex P1).
func TestPresetApplyAbortsOnUnreadableStoreConfig(t *testing.T) {
	p, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "agent = \"none\"\n")
	os.MkdirAll(p.Dir, 0o755)
	storePath := filepath.Join(p.Dir, "byre.config")
	os.WriteFile(storePath, []byte("agent = \"claude\"\n"), 0o644)
	if err := os.Chmod(storePath, 0o000); err != nil {
		t.Skip("cannot chmod")
	}
	t.Cleanup(func() { os.Chmod(storePath, 0o644) })
	s, _, _ := testStreams("y\n", true)
	err := PresetApply(s, proj, "")
	if err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("unreadable existing config must abort, got %v", err)
	}
	os.Chmod(storePath, 0o644)
	b, _ := os.ReadFile(storePath)
	if string(b) != "agent = \"claude\"\n" {
		t.Fatalf("config must be untouched: %s", b)
	}
}

// The conventional ./byre.preset gets the same size bound as explicit
// sources (codex P2).
func TestPresetConventionalPathIsBounded(t *testing.T) {
	_, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, strings.Repeat("# pad\n", packages.MaxManifestBytes/6+1))
	if _, _, _, err := readPreset(proj, ""); err == nil {
		t.Fatal("oversized conventional preset must be rejected")
	}
}

// createExclusive must not clobber a marker landed in the race window.
func TestSweepDoesNotClobberConcurrentMarker(t *testing.T) {
	home := t.TempDir()
	pdir := filepath.Join(home, "projects", "someproj")
	os.MkdirAll(pdir, 0o755)
	os.WriteFile(filepath.Join(pdir, "adopted"), []byte("stalehash"), 0o644)
	// A current marker lands "concurrently" (before the sweep's write).
	os.WriteFile(filepath.Join(pdir, "applied"), []byte("freshhash\n./byre.preset"), 0o644)
	if err := packages.EnsureStore(home, nil, "v9.9.9", nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(pdir, "applied"))
	if !strings.HasPrefix(string(b), "freshhash") {
		t.Fatalf("sweep must never replace a live marker: %q", b)
	}
}

// Grant-summary lines carry preset-controlled bytes: the review escapes them
// before styling, so hostile run_args cannot forge rows (codex round 3).
func TestPresetReviewEscapesGrantLines(t *testing.T) {
	_, proj := onboardPaths(t)
	// Raw control bytes fail TOML parsing; the realistic vector is a TOML
	// unicode escape that DECODES to ESC.
	shipPreset(t, proj, PresetName, "agent = \"none\"\nrun_args = [\"--cap\\u001B[32madd=FAKE\"]\n")
	s, _, errBuf := testStreams("n\n", true)
	if err := PresetApply(s, proj, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), "\x1b[32m") {
		t.Fatalf("grant line must not carry preset-controlled ANSI:\n%q", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "--capadd=FAKE") {
		t.Fatalf("escaped grant content must still render:\n%s", errBuf.String())
	}
}

// Inspect treats only ABSENCE as no-config; other read failures abort
// instead of silently omitting the promised diff (codex round 3).
func TestPresetInspectAbortsOnUnreadableStoreConfig(t *testing.T) {
	p, proj := onboardPaths(t)
	shipPreset(t, proj, PresetName, "agent = \"none\"\n")
	os.MkdirAll(p.Dir, 0o755)
	storePath := filepath.Join(p.Dir, "byre.config")
	os.WriteFile(storePath, []byte("agent = \"claude\"\n"), 0o644)
	if err := os.Chmod(storePath, 0o000); err != nil {
		t.Skip("cannot chmod")
	}
	t.Cleanup(func() { os.Chmod(storePath, 0o644) })
	s := discardStreams()
	if err := PresetInspect(s, proj, ""); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("unreadable config must abort inspect, got %v", err)
	}
}
