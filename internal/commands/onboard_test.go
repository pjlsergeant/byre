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
	if err == nil {
		t.Fatal("expected an error when a flag is passed to an already-configured project")
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

// On a non-TTY an un-flagged axis has nobody to answer for it: refuse loudly
// rather than fill it from the machine favourite — a favourite is what Enter
// means at a prompt, and there is no Enter on a pipe (audit finding 4).
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
	cfg, err := config.ParseFile(filepath.Join(p.Dir, "byre.config"))
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
	cfg2, err := config.ParseFile(filepath.Join(p2.Dir, "byre.config"))
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
	cfg, err := config.ParseFile(filepath.Join(p.Dir, "byre.config"))
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
	cfg, err := config.ParseFile(filepath.Join(p.Dir, "byre.config"))
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
	cfg, err := config.ParseFile(filepath.Join(p.Home, "default.config"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SharedAuth.HasYes("claude") {
		t.Fatalf("a saved yes must store the preference, shared_auth = %+v", cfg.SharedAuth)
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
	cfg2, err := config.ParseFile(filepath.Join(p2.Dir, "byre.config"))
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
	cfg, err := config.ParseFile(filepath.Join(p.Home, "default.config"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SharedAuth.Empty() {
		t.Fatalf("a saved no must remove the preference, shared_auth = %+v", cfg.SharedAuth)
	}
	// And the box itself was not opted in.
	pcfg, err := config.ParseFile(filepath.Join(p.Dir, "byre.config"))
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
	cfg, err := config.ParseFile(filepath.Join(p.Dir, "byre.config"))
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
	cfg, err := config.ParseFile(filepath.Join(p.Dir, "byre.config"))
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
	got, err := config.ParseFile(filepath.Join(p.Home, "default.config"))
	if err != nil {
		t.Fatal(err)
	}
	if got.SharedAuth.CompanionPick("claude") != "claude-shared-auth" {
		t.Fatalf("stored pick must survive a no-offer save, got %+v", got.SharedAuth)
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
	if err := builtins.EnsureStore(p.Home); err != nil {
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
