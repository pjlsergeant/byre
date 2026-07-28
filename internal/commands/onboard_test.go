package commands

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/onboard"
	"github.com/pjlsergeant/byre/internal/project"
)

// isTTY must report false for /dev/null and regular files — /dev/null is a
// character device, so the old ModeCharDevice check wrongly called it a terminal,
// which made `byre develop < /dev/null` emit `docker run -t` and fail.
func TestIsTTYRejectsDevNullAndFiles(t *testing.T) {
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	if isTTY(devnull) {
		t.Error("isTTY(/dev/null) = true, want false (not an interactive terminal)")
	}

	f, err := os.CreateTemp(t.TempDir(), "f")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTTY(f) {
		t.Error("isTTY(regular file) = true, want false")
	}
}

func onboardPaths(t *testing.T) (project.Paths, string) {
	t.Helper()
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()
	p, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	return p, proj
}

// An existing byre.config + a --template/--agent flag must error (pointing at
// the file), not silently ignore the flag.
func TestOnboardExistingConfigWithFlagErrors(t *testing.T) {
	p, proj := onboardPaths(t)
	cfg := filepath.Join(p.Dir, "byre.config") // host-side store
	if err := os.WriteFile(cfg, []byte("agent = \"claude\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := onboardIfNeeded(discardStreams(), proj, p, "", "codex", nil)
	if err == nil || !strings.Contains(err.Error(), "already configured") {
		t.Fatalf("a flag against a configured project must refuse with already-configured, got: %v", err)
	}
	// Names the current agent (canonical byre/claude after catalog expand)
	// and the full path to the file.
	if (!strings.Contains(err.Error(), "agent=claude") && !strings.Contains(err.Error(), "agent=byre/claude")) || !strings.Contains(err.Error(), cfg) {
		t.Fatalf("error should name the current agent and the file path: %v", err)
	}
	// Without a flag, an existing config is fine (no error, no prompt).
	if err := onboardIfNeeded(discardStreams(), proj, p, "", "", nil); err != nil {
		t.Fatalf("no-flag develop on a configured project should be a no-op: %v", err)
	}
}

// "I could not look" is not "this project is unconfigured": an unreadable
// store makes the existence probe inconclusive, and falling through would run
// the first-run picker over a config that may well be there.
func TestOnboardUnreadableStoreRefusesInsteadOfReonboarding(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root traverses a 0000 directory")
	}
	p, proj := onboardPaths(t)
	cfg := filepath.Join(p.Dir, "byre.config")
	if err := os.WriteFile(cfg, []byte("agent = \"claude\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.Dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p.Dir, 0o700) })

	err := onboardIfNeeded(discardStreams(), proj, p, "", "", nil)
	if err == nil || !strings.Contains(err.Error(), "cannot tell whether") || !strings.Contains(err.Error(), cfg) {
		t.Fatalf("an inconclusive probe must refuse and name the path, got: %v", err)
	}
	if err := os.Chmod(p.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if b, rerr := os.ReadFile(cfg); rerr != nil || !strings.Contains(string(b), "claude") {
		t.Fatalf("the existing config must be untouched: %q %v", string(b), rerr)
	}
}

// On a non-TTY an un-flagged axis has nobody to answer for it: refuse loudly
// rather than fill it from the machine favourite — a favourite is what Enter
// means at a prompt, and there is no Enter on a pipe.
func TestOnboardPartialFlagNonTTYErrors(t *testing.T) {
	p, proj := onboardPaths(t)
	err := onboardIfNeeded(discardStreams(), proj, p, "", "codex", nil)
	if err == nil || !strings.Contains(err.Error(), "--template") || !strings.Contains(err.Error(), `"none"`) {
		t.Fatalf("partial flags without a TTY must error naming the fix, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); !os.IsNotExist(err) {
		t.Fatalf("a refused onboarding must write nothing: %v", err)
	}
	// Both flags explicit: the zero-prompt contract, unchanged.
	if err := onboardIfNeeded(discardStreams(), proj, p, "none", "codex", nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(p.Dir, "byre.config"))
	if err != nil {
		t.Fatalf("expected byre.config written: %v", err)
	}
	if !strings.Contains(string(b), `agent = "codex"`) {
		t.Fatalf("the --agent flag must be honored: %s", b)
	}
}

// --shared-auth IS the offer's answer: no question in any mode, yes opts the
// box in via its own byre.config, and a yes for an agent with no ready
// companion refuses loudly instead of silently granting nothing.
func TestOnboardSharedAuthFlag(t *testing.T) {
	yes, no := true, false

	p, proj := onboardPaths(t)
	s, _, errBuf := testStreams("", true) // empty stdin: any prompt would EOF
	if err := onboardIfNeeded(s, proj, p, "none", "claude", &yes); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), onboard.SharedAuthPrompt("claude")) {
		t.Fatalf("a given --shared-auth must suppress the question:\n%s", errBuf.String())
	}
	cfg, err := config.ParseFile(filepath.Join(p.Dir, "byre.config"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cfg.Skills, "claude-shared-auth") {
		t.Fatalf("--shared-auth must opt the box in: %v", cfg.Skills)
	}
	if _, err := os.Stat(filepath.Join(p.Home, "default.config")); !os.IsNotExist(err) {
		t.Fatalf("the flag answers for THIS box only — nothing machine-level: %v", err)
	}

	// Explicit no: suppressed question, nothing opted in.
	p2, proj2 := onboardPaths(t)
	s2, _, errBuf2 := testStreams("", true)
	if err := onboardIfNeeded(s2, proj2, p2, "none", "claude", &no); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf2.String(), onboard.SharedAuthPrompt("claude")) {
		t.Fatalf("--shared-auth=false must suppress the question:\n%s", errBuf2.String())
	}
	cfg2, err := config.ParseFile(filepath.Join(p2.Dir, "byre.config"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg2.Skills) != 0 {
		t.Fatalf("an explicit no opts nothing in: %v", cfg2.Skills)
	}

	// A yes with no ready companion (grok declares none) errors loudly.
	p3, proj3 := onboardPaths(t)
	s3, _, _ := testStreams("", true)
	err = onboardIfNeeded(s3, proj3, p3, "none", "grok", &yes)
	if err == nil || !strings.Contains(err.Error(), "no ready shared-auth companion") {
		t.Fatalf("--shared-auth for a companion-less agent must refuse loudly: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(p3.Dir, "byre.config")); !os.IsNotExist(serr) {
		t.Fatalf("a refused onboarding must write nothing: %v", serr)
	}
}

// Full picker on a TTY: declining the shared-auth offer WITHOUT saving
// records nothing — the offer is per box (ADR 0025), so a later project's
// onboarding asks about its own box again.
func TestOnboardSharedAuthDeclineRecordsNothingAndReasks(t *testing.T) {
	p, proj := onboardPaths(t)
	// Template: none, Agent: claude, shared auth: n, save-as-default: n.
	s, _, errBuf := testStreams("\nclaude\nn\nn\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), onboard.SharedAuthPrompt("claude")) {
		t.Fatalf("expected the shared-auth offer:\n%s", errBuf.String())
	}
	// A "no" leaves no trace: nothing was saved, so no default.config at all.
	if _, err := os.Stat(filepath.Join(p.Home, "default.config")); !os.IsNotExist(err) {
		t.Fatalf("declining must record nothing machine-level: %v", err)
	}
	cfg, err := config.ParseFile(filepath.Join(p.Dir, "byre.config"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Skills) != 0 {
		t.Fatalf("declining must not enable the companion for this box: %v", cfg.Skills)
	}

	// A second project, same home: its box gets its own offer.
	proj2 := t.TempDir()
	p2, err := project.Resolve(proj2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	s2, _, errBuf2 := testStreams("\nclaude\nn\nn\n", true)
	if err := onboardIfNeeded(s2, proj2, p2, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf2.String(), onboard.SharedAuthPrompt("claude")) {
		t.Fatalf("the offer is per box — the next box must be asked:\n%s", errBuf2.String())
	}
}

// Accepting the offer WITHOUT saving opts only THIS box in: the companion
// lands in the project's byre.config skills — the same representation as a
// hand-enabled skill — and nothing machine-level is touched.
func TestOnboardSharedAuthAcceptEnablesCompanionForThisBox(t *testing.T) {
	p, proj := onboardPaths(t)
	// Template: none, Agent: claude, shared auth: y, save-as-default: n.
	s, _, errBuf := testStreams("\nclaude\ny\nn\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseFile(filepath.Join(p.Dir, "byre.config"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cfg.Skills, "claude-shared-auth") {
		t.Fatalf("accepting must enable the companion in this box's byre.config, skills = %v", cfg.Skills)
	}
	if _, err := os.Stat(filepath.Join(p.Home, "default.config")); !os.IsNotExist(err) {
		t.Fatalf("a per-box yes must not write default.config: %v", err)
	}
	if !strings.Contains(errBuf.String(), "skills=claude-shared-auth") {
		t.Fatalf("the wrote-line must show the opted skill:\n%s", errBuf.String())
	}

	// The ADR's central claim: a yes is NOT machine-wide. A second project,
	// same home, must still be asked about its own box.
	proj2 := t.TempDir()
	p2, err := project.Resolve(proj2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	s2, _, errBuf2 := testStreams("\nclaude\nn\nn\n", true)
	if err := onboardIfNeeded(s2, proj2, p2, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf2.String(), onboard.SharedAuthPrompt("claude")) {
		t.Fatalf("one box's yes must not settle the question for the next box:\n%s", errBuf2.String())
	}
}

// A shared_auth_declined left behind by v0.1.7 is vestigial: the offer's
// default is already No, a decline needs no record, and the key must not
// suppress the per-box question (or break onboarding).
func TestOnboardVestigialDeclinedKeyDoesNotSuppressOffer(t *testing.T) {
	p, proj := onboardPaths(t)
	if err := os.WriteFile(filepath.Join(p.Home, "default.config"), []byte("shared_auth_declined = [\"claude\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Template: none, Agent: claude, shared auth: n, save-as-default: n.
	s, _, errBuf := testStreams("\nclaude\nn\nn\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), onboard.SharedAuthPrompt("claude")) {
		t.Fatalf("a v0.1.7 decline must not silence the per-box offer:\n%s", errBuf.String())
	}
}

// Accepting the offer and SAVING stores a PREFERENCE, not a grant: the agent
// lands in the picker-owned shared_auth list, default.config's skills stays
// untouched, and the next box is still asked — prefilled [Y/n], so Enter
// opts it in and the grant lands in that box's own byre.config.
func TestOnboardAcceptSavedPrefillsNextBox(t *testing.T) {
	p, proj := onboardPaths(t)
	// Template: none, Agent: claude, shared auth: y, save-as-default: y.
	s, _, _ := testStreams("\nclaude\ny\ny\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseFile(filepath.Join(p.Home, "default.config"), true)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.StoredSharedAuth().HasYes("claude") {
		t.Fatalf("a saved yes must store the preference, shared_auth = %+v", cfg.StoredSharedAuth())
	}
	if len(cfg.Skills) != 0 {
		t.Fatalf("the picker must NEVER write default.config's skills: %v", cfg.Skills)
	}

	// Next box: asked, prefilled — Enter accepts, and nothing is news so no
	// save question consumes input.
	proj2 := t.TempDir()
	p2, err := project.Resolve(proj2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	s2, _, errBuf2 := testStreams("\n\n\n", true)
	if err := onboardIfNeeded(s2, proj2, p2, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf2.String(), onboard.SharedAuthPrompt("claude")) ||
		!strings.Contains(errBuf2.String(), "[Y/n, i for info]") {
		t.Fatalf("the next box must be asked, prefilled from the preference:\n%s", errBuf2.String())
	}
	cfg2, err := config.ParseFile(filepath.Join(p2.Dir, "byre.config"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cfg2.Skills, "claude-shared-auth") {
		t.Fatalf("Enter on [Y/n] must opt THIS box in via its own byre.config: %v", cfg2.Skills)
	}
}

// Overriding a saved yes with an explicit n and saving removes the
// preference: the box after that is back to [y/N].
func TestOnboardSaveNoRemovesPreference(t *testing.T) {
	p, proj := onboardPaths(t)
	if err := os.WriteFile(filepath.Join(p.Home, "default.config"), []byte("agent = \"claude\"\nshared_auth = [\"claude\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Template: none (Enter), Agent: claude (favourite), shared auth:
	// explicit n (news vs the stored yes), save: y.
	s, _, errBuf := testStreams("\n\nn\ny\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "[Y/n, i for info]") {
		t.Fatalf("the stored yes must prefill the offer:\n%s", errBuf.String())
	}
	cfg, err := config.ParseFile(filepath.Join(p.Home, "default.config"), true)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.StoredSharedAuth().Empty() {
		t.Fatalf("a saved no must remove the preference, shared_auth = %+v", cfg.StoredSharedAuth())
	}
	// And the box itself was not opted in.
	pcfg, err := config.ParseFile(filepath.Join(p.Dir, "byre.config"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcfg.Skills) != 0 {
		t.Fatalf("n must not opt the box in: %v", pcfg.Skills)
	}
}

// The flag path prompts too: --agent fixes the agent, the template is asked on
// a TTY, and the shared-auth offer follows — landing in this box's config.
func TestOnboardFlagPathOffersSharedAuth(t *testing.T) {
	p, proj := onboardPaths(t)
	// Template: none (Enter), shared auth: y.
	s, _, _ := testStreams("\ny\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "claude", nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseFile(filepath.Join(p.Dir, "byre.config"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cfg.Skills, "claude-shared-auth") {
		t.Fatalf("skills = %v", cfg.Skills)
	}
}

// A companion already enabled machine-wide (hand-set, or a v0.1.7 "y") means
// this box gets shared credentials from the cascade regardless — asking would
// be offering a switch already thrown, so the offer is suppressed.
func TestOnboardNoOfferWhenCompanionAlreadyOnMachineWide(t *testing.T) {
	p, proj := onboardPaths(t)
	if err := os.WriteFile(filepath.Join(p.Home, "default.config"), []byte("skills = [\"claude-shared-auth\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Template: none, Agent: claude, save-as-default: n — no offer between.
	s, _, errBuf := testStreams("\nclaude\nn\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), onboard.SharedAuthPrompt("claude")) {
		t.Fatalf("companion already on machine-wide — no offer:\n%s", errBuf.String())
	}
	cfg, err := config.ParseFile(filepath.Join(p.Dir, "byre.config"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Skills) != 0 {
		t.Fatalf("no offer — byre.config must not duplicate the machine-wide skill: %v", cfg.Skills)
	}
}

// A save-as-default after a NO-OFFER onboard must not touch the stored
// shared-auth favourite: the preference belongs to a question that was
// never asked this time.
func TestOnboardSaveWithoutOfferKeepsPreference(t *testing.T) {
	p, proj := onboardPaths(t)
	// Companion machine-wide (suppresses the offer) + a stored pick.
	def := "skills = [\"claude-shared-auth\"]\nshared_auth = { claude = \"claude-shared-auth\" }\n"
	if err := os.WriteFile(filepath.Join(p.Home, "default.config"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	// Template: none, Agent: claude, save-as-default: y — no offer between.
	s, _, errBuf := testStreams("\nclaude\ny\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), onboard.SharedAuthPrompt("claude")) {
		t.Fatalf("companion already on machine-wide — no offer:\n%s", errBuf.String())
	}
	got, err := config.ParseFile(filepath.Join(p.Home, "default.config"), true)
	if err != nil {
		t.Fatal(err)
	}
	if got.StoredSharedAuth().CompanionPick("claude") != "claude-shared-auth" {
		t.Fatalf("stored pick must survive a no-offer save, got %+v", got.StoredSharedAuth())
	}
}

// An agent with no READY companion (grok's is retired and declares no
// shared_auth_for) gets no offer.
func TestOnboardNoOfferWithoutReadyCompanion(t *testing.T) {
	p, proj := onboardPaths(t)
	s, _, errBuf := testStreams("\ngrok\nn\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), onboard.SharedAuthPrompt("grok")) {
		t.Fatalf("no ready companion — no offer:\n%s", errBuf.String())
	}
}

// Both flags given = the caller asked for non-interactive onboarding: no
// shared-auth offer, no stdin reads, even on a TTY.
func TestOnboardFullyFlaggedMakesNoOffer(t *testing.T) {
	p, proj := onboardPaths(t)
	s, _, errBuf := testStreams("", true) // empty stdin: any prompt would EOF
	if err := onboardIfNeeded(s, proj, p, "none", "claude", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), onboard.SharedAuthPrompt("claude")) {
		t.Fatalf("fully-flagged onboarding must stay non-interactive:\n%s", errBuf.String())
	}
	if _, err := os.Stat(filepath.Join(p.Home, "default.config")); !os.IsNotExist(err) {
		t.Fatalf("no offer — nothing may be recorded in default.config: %v", err)
	}
}

// EOF (Ctrl-D) anywhere in the picker — including at the shared-auth offer —
// aborts onboarding BEFORE anything is written: all answers are collected
// first, so an aborted run leaves no half-done state.
func TestOnboardEOFMidPickerWritesNothing(t *testing.T) {
	p, proj := onboardPaths(t)
	// Template and agent answered; input ends at the shared-auth offer.
	s, _, _ := testStreams("\nclaude\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err == nil {
		t.Fatal("EOF mid-picker should abort onboarding")
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); !os.IsNotExist(err) {
		t.Fatalf("aborted onboarding must write no byre.config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.Home, "default.config")); !os.IsNotExist(err) {
		t.Fatalf("aborted onboarding must record nothing: %v", err)
	}
}

// A failed default.config write (saving the favourites) must abort onboarding
// BEFORE byre.config is written: once byre.config exists this project never
// onboards again, so the machine-level record goes first and a failure leaves
// the whole flow re-runnable.
func TestOnboardSaveDefaultWriteFailureLeavesProjectUnonboarded(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("read-only mode is not enforceable as root")
	}
	p, proj := onboardPaths(t)
	// Materialize the store first, then make home read-only: default.config's
	// atomic write (a temp file in home) fails, while byre.config (in the
	// project's store subdir) stays writable — exactly the wedge that would
	// strand a half-onboarded project.
	if err := builtins.EnsureStoreOut(p.Home, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.Home, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p.Home, 0o755) })
	// Template: none, agent: claude, shared auth: n, save-as-default: y.
	s, _, _ := testStreams("\nclaude\nn\ny\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err == nil {
		t.Fatal("a failed save-default must abort onboarding")
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); !os.IsNotExist(err) {
		t.Fatalf("byre.config must not exist after an aborted onboarding (it would never re-run): %v", err)
	}
}

// defaults.skip_questions: a standing machine-scope instruction (usually the
// global editor's checkbox, which is why the checkbox names the credential
// consequence before it is ticked) that new projects take their stored
// answers unasked. Onboarding must honour it
// WITHOUT prompting -- including the shared-auth pick, which grants (the
// companion lands in the new project's skills) -- and must say out loud that
// it did, so a box configured without a question is never a silent one.
func TestOnboardSkipQuestionsUsesStoredAnswersAndSaysSo(t *testing.T) {
	p, proj := onboardPaths(t)
	mustWriteFile(t, filepath.Join(p.Home, "default.config"), []byte(
		"template = \"go\"\nagent = \"claude\"\n\n[defaults]\nshared_auth = { claude = \"claude-shared-auth\" }\nskip_questions = true\n"), 0o644)

	// No input at all: a prompt would block or consume nothing and misconfigure.
	s, _, errBuf := testStreams("", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err != nil {
		t.Fatal(err)
	}
	out := errBuf.String()
	if strings.Contains(out, onboard.SharedAuthPrompt("claude")) {
		t.Errorf("skip_questions must not ask the shared-auth question:\n%s", out)
	}
	for _, want := range []string{"without asking", "skip_questions", "shared credentials enabled"} {
		if !strings.Contains(out, want) {
			t.Errorf("acting on skip_questions must be said out loud, missing %q:\n%s", want, out)
		}
	}
	got, err := config.ParseFile(filepath.Join(p.Dir, config.ProjectConfigName), true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Template != "go" || got.Agent != "claude" {
		t.Errorf("stored answers must configure the project: %+v", got)
	}
	if !slices.Contains(got.Skills, "claude-shared-auth") {
		t.Errorf("the stored shared-auth pick must be applied: %+v", got.Skills)
	}
}

// Without the key, nothing changes: the picker still runs.
func TestOnboardWithoutSkipQuestionsStillAsks(t *testing.T) {
	p, proj := onboardPaths(t)
	mustWriteFile(t, filepath.Join(p.Home, "default.config"), []byte("template = \"go\"\nagent = \"claude\"\n"), 0o644)
	s, _, errBuf := testStreams("\n\nn\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), "without asking") {
		t.Errorf("no skip_questions means the picker runs:\n%s", errBuf.String())
	}
}

// First-run onboarding shows a broken agent skill with its reason instead of
// leaving it out. Both tiers of broken are covered, because they become
// problem rows by different routes: badposture fails stage 2 at catalog
// ingest, brokenmount parses and fails the FULL load (MarkLoadFailures). A
// user whose agent is either one saw a picker with it simply absent.
func TestOnboardShowsBrokenAgentsWithTheirReason(t *testing.T) {
	p, proj := onboardPaths(t)
	writeLocalSkill(t, p.Home, "brokenmount",
		"description = \"b\"\n[agent]\ncommand = \"x\"\n\n[[runtime.mounts]]\nhost = \"/tmp\"\ntarget = \"relative\"\n")
	writeLocalSkill(t, p.Home, "badposture",
		"description = \"b\"\n[agent]\ncommand = \"x\"\n\n[runtime]\nnetwork_posture = \"Deny-Default\"\n")

	// Template: none. Agent: try each broken row, then the working one.
	// Shared auth: n. Save: n.
	s, _, errBuf := testStreams("\nbrokenmount\nbadposture\nclaude\nn\nn\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err != nil {
		t.Fatal(err)
	}
	out := errBuf.String()
	for _, want := range []string{"brokenmount", "mount target", "badposture", "Deny-Default"} {
		if !strings.Contains(out, want) {
			t.Errorf("first-run must list the broken agent and why, missing %q:\n%s", want, out)
		}
	}
	// Selecting one reprompts with the reason rather than landing on it.
	if n := strings.Count(out, "is unavailable:"); n != 2 {
		t.Errorf("each attempt at a broken agent must repeat its reason (got %d):\n%s", n, out)
	}
	// And the healthy pick still lands.
	cfg, err := config.ParseFile(filepath.Join(p.Dir, config.ProjectConfigName), true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent != "claude" {
		t.Fatalf("a working agent must still be selectable, got %q", cfg.Agent)
	}
}

// A problem row sharing a name with a package that IS offered must not be
// listed: a bundled skill keeps its bare alias while a legacy materialized
// copy of it gets a scoped problem row under the same display name, so
// listing both would print "claude — unavailable" directly above a prompt
// offering claude. Every store upgraded past a materialized bundled copy is
// in exactly that state, which is what makes this the common case rather than
// a corner.
func TestOnboardDoesNotListAProblemRowThatIsAlsoOffered(t *testing.T) {
	p, proj := onboardPaths(t)
	// A materialized copy of the bundled claude: LEGACY row, display name
	// "claude", and its primary declares [agent] so the agent axis sees it.
	writeLocalSkill(t, p.Home, "claude", "description = \"old copy\"\n[agent]\ncommand = \"claude\"\n")

	s, _, errBuf := testStreams("\nclaude\nn\nn\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err != nil {
		t.Fatal(err)
	}
	out := errBuf.String()
	if strings.Contains(out, "unavailable") {
		t.Errorf("nothing is unavailable here -- claude is offered:\n%s", out)
	}
	// And the name is still selectable: the healthy bundled package.
	cfg, err := config.ParseFile(filepath.Join(p.Dir, config.ProjectConfigName), true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent != "claude" {
		t.Fatalf("the offered package of that name must still be pickable, got %q", cfg.Agent)
	}
}

// The flag path reads the same rows: --agent naming a broken skill gets the
// reason, not "unknown agent", which would send the user hunting a typo in a
// name they spelled right.
func TestOnboardAgentFlagNamingABrokenSkillSaysWhy(t *testing.T) {
	p, proj := onboardPaths(t)
	writeLocalSkill(t, p.Home, "badposture",
		"description = \"b\"\n[agent]\ncommand = \"x\"\n\n[runtime]\nnetwork_posture = \"Deny-Default\"\n")
	s, _, _ := testStreams("", false)
	err := onboardIfNeeded(s, proj, p, "none", "badposture", nil)
	if err == nil {
		t.Fatal("a broken agent must not be accepted from a flag")
	}
	for _, want := range []string{"badposture", "unavailable", "network_posture"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name the package and the reason, missing %q: %v", want, err)
		}
	}
}

// writeLocalSkill drops a loadable skill into the store's skills/ dir, so a
// test can put a second claimant in front of the catalog.
func writeLocalSkill(t *testing.T, home, name, body string) {
	t.Helper()
	dir := filepath.Join(home, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(dir, "skill.toml"), []byte(body), 0o644)
}

// The stored shared-auth pick is a name, and a name is not a skill. The
// skip_questions path used to write whatever was stored straight into the new
// project's skills: an uninstalled companion failed the next develop on an
// unknown skill, and a different package that had taken the id in the meantime
// received the machine-wide credential grant with nobody asked. It gets the
// same live-claimants check the interactive offer applies.
func TestOnboardSkipQuestionsRefusesAStalePick(t *testing.T) {
	p, proj := onboardPaths(t)
	mustWriteFile(t, filepath.Join(p.Home, "default.config"), []byte(
		"template = \"none\"\nagent = \"claude\"\n\n[defaults]\nshared_auth = { claude = \"gone-shared-auth\" }\nskip_questions = true\n"), 0o644)

	s, _, errBuf := testStreams("", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err != nil {
		t.Fatal(err)
	}
	got, err := config.ParseFile(filepath.Join(p.Dir, config.ProjectConfigName), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Skills) != 0 {
		t.Fatalf("a pick nothing claims must not be applied: %+v", got.Skills)
	}
	out := errBuf.String()
	if !strings.Contains(out, onboard.StalePickNotice("gone-shared-auth")) {
		t.Errorf("the skipped grant must be disclosed:\n%s", out)
	}
	if !strings.Contains(out, "NOT enabled") {
		t.Errorf("the disclosure must say what did not happen:\n%s", out)
	}
	// The stored preference is left alone: this path answers nothing.
	def, err := config.ParseFile(filepath.Join(p.Home, "default.config"), true)
	if err != nil {
		t.Fatal(err)
	}
	if def.StoredSharedAuth().CompanionPick("claude") != "gone-shared-auth" {
		t.Errorf("a stale pick is reported, not rewritten: %+v", def.StoredSharedAuth())
	}
}

// A live pick still applies -- the check filters, it does not disable.
func TestOnboardSkipQuestionsAppliesALivePick(t *testing.T) {
	p, proj := onboardPaths(t)
	mustWriteFile(t, filepath.Join(p.Home, "default.config"), []byte(
		"template = \"none\"\nagent = \"claude\"\n\n[defaults]\nshared_auth = { claude = \"claude-shared-auth\" }\nskip_questions = true\n"), 0o644)
	s, _, errBuf := testStreams("", true)
	if err := onboardIfNeeded(s, proj, p, "", "", nil); err != nil {
		t.Fatal(err)
	}
	got, err := config.ParseFile(filepath.Join(p.Dir, config.ProjectConfigName), true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got.Skills, "claude-shared-auth") {
		t.Fatalf("a live stored pick must still apply: %+v", got.Skills)
	}
	if strings.Contains(errBuf.String(), "no longer installed") {
		t.Errorf("a live pick must not be reported stale:\n%s", errBuf.String())
	}
}

// --shared-auth with SEVERAL claimants is an ambiguity byre refuses to
// resolve, not an absence: reporting "no ready shared-auth companion skill"
// sent the user to install a package they already had two of.
func TestSharedAuthFlagTellsAmbiguityFromAbsence(t *testing.T) {
	p, proj := onboardPaths(t)
	writeLocalSkill(t, p.Home, "aa-auth", "shared_auth_for = \"claude\"\n")
	yes := true
	s, _, _ := testStreams("", true)
	err := onboardIfNeeded(s, proj, p, "none", "claude", &yes)
	if err == nil {
		t.Fatal("two claimants must refuse --shared-auth")
	}
	if strings.Contains(err.Error(), "no ready shared-auth companion") {
		t.Fatalf("an ambiguity reported as an absence: %v", err)
	}
	for _, want := range []string{"claim", "aa-auth", "claude-shared-auth"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name the rivals, missing %q: %v", want, err)
		}
	}

	// The absence case keeps its own message (grok's companion declares no
	// shared_auth_for vouch).
	p2, proj2 := onboardPaths(t)
	s2, _, _ := testStreams("", true)
	err = onboardIfNeeded(s2, proj2, p2, "none", "grok", &yes)
	if err == nil || !strings.Contains(err.Error(), "no ready shared-auth companion") {
		t.Fatalf("no claimant at all must still say so: %v", err)
	}
}

// The offer's staleness verdict must be the SAME verdict the apply paths and
// the config editor reach (skills.SharedAuthPickLive). Matching the stored
// pick against offer.Claimants answers a different question: that list is
// display names already filtered of anything enabled machine-wide, so a pick
// that is installed AND granted read as "no longer installed" here while every
// other surface read it live.
func TestSharedAuthOfferDoesNotCallAGrantedPickStale(t *testing.T) {
	p, _ := onboardPaths(t)
	// Two claimants; the stored pick is the one already enabled machine-wide,
	// so it is filtered out of the displayed list but is emphatically live.
	writeLocalSkill(t, p.Home, "aa-auth", "shared_auth_for = \"claude\"\n")
	mustWriteFile(t, filepath.Join(p.Home, "default.config"), []byte(
		"skills = [\"claude-shared-auth\"]\n\n[defaults]\nshared_auth = { claude = \"claude-shared-auth\" }\n"), 0o644)

	cat, err := builtins.LoadCatalogRaw(p.Home)
	if err != nil {
		t.Fatal(err)
	}
	offer := buildSharedAuthOffer(p.Home, cat, "claude")
	if offer.StalePickNotice != "" {
		t.Errorf("an installed, already-granted pick must not be reported missing: %q", offer.StalePickNotice)
	}
	// No prefill: a prefill has to point at a row the picker shows, and this
	// one is not among them.
	if offer.PrefPick != "" {
		t.Errorf("prefill must name a row the picker shows, got %q (claimants %v)", offer.PrefPick, offer.Claimants)
	}
	if slices.Contains(offer.Claimants, "claude-shared-auth") {
		t.Errorf("a machine-wide-enabled claimant must stay out of the offer: %v", offer.Claimants)
	}
	// And the yes must NOT stand. With the picked companion absent and exactly
	// one RIVAL left, a prefilled Yes turns the single-claim [Y/n] into Enter
	// granting machine-wide credentials to a package the user never chose.
	if offer.PrefYes {
		t.Errorf("a pick that is not on offer must not leave Yes prefilled (claimants %v)", offer.Claimants)
	}

	// A pick nothing claims is still stale, through the shared predicate.
	mustWriteFile(t, filepath.Join(p.Home, "default.config"), []byte(
		"[defaults]\nshared_auth = { claude = \"gone-shared-auth\" }\n"), 0o644)
	cat2, err := builtins.LoadCatalogRaw(p.Home)
	if err != nil {
		t.Fatal(err)
	}
	gone := buildSharedAuthOffer(p.Home, cat2, "claude")
	if gone.StalePickNotice != onboard.StalePickNotice("gone-shared-auth") {
		t.Errorf("a pick no skill claims must still be reported: %q", gone.StalePickNotice)
	}
	if gone.PrefYes {
		t.Error("a stale pick must not prefill yes")
	}
}

// A pick stored as the CANONICAL id whose claimant displays as its alias is
// the same package. Byte-matching it against the displayed rows found nothing,
// so the picker defaulted to N despite a valid stored preference -- while the
// liveness check, which does expand aliases, called it live. Prefill and
// liveness now share one spelling rule (skills.SameSkillRef).
func TestSharedAuthOfferPrefillsACanonicallyStoredPick(t *testing.T) {
	p, _ := onboardPaths(t)
	// Two claimants, so the offer is a picker and the prefill decides its row.
	writeLocalSkill(t, p.Home, "aa-auth", "shared_auth_for = \"claude\"\n")
	mustWriteFile(t, filepath.Join(p.Home, "default.config"), []byte(
		"[defaults]\nshared_auth = { claude = \"byre/claude-shared-auth\" }\n"), 0o644)

	cat, err := builtins.LoadCatalogRaw(p.Home)
	if err != nil {
		t.Fatal(err)
	}
	offer := buildSharedAuthOffer(p.Home, cat, "claude")
	if offer.StalePickNotice != "" {
		t.Errorf("a canonically-stored pick is not missing: %q", offer.StalePickNotice)
	}
	// The row's own DISPLAY name, since that is what the picker matches on.
	if offer.PrefPick != "claude-shared-auth" {
		t.Errorf("PrefPick = %q, want the displayed row claude-shared-auth (claimants %v)", offer.PrefPick, offer.Claimants)
	}
	if !offer.PrefYes {
		t.Error("a live, offered pick must keep its yes")
	}
	// And the alias spelling still works, unchanged.
	mustWriteFile(t, filepath.Join(p.Home, "default.config"), []byte(
		"[defaults]\nshared_auth = { claude = \"claude-shared-auth\" }\n"), 0o644)
	cat2, err := builtins.LoadCatalogRaw(p.Home)
	if err != nil {
		t.Fatal(err)
	}
	if got := buildSharedAuthOffer(p.Home, cat2, "claude"); got.PrefPick != "claude-shared-auth" {
		t.Errorf("the alias spelling must still prefill, got %q", got.PrefPick)
	}
}
