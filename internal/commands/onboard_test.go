package commands

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
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
	err := onboardIfNeeded(discardStreams(), proj, p, "", "codex")
	if err == nil {
		t.Fatal("expected an error when a flag is passed to an already-configured project")
	}
	// Names the current agent and the full path to the file.
	if !strings.Contains(err.Error(), "agent=claude") || !strings.Contains(err.Error(), cfg) {
		t.Fatalf("error should name the current agent and the file path: %v", err)
	}
	// Without a flag, an existing config is fine (no error, no prompt).
	if err := onboardIfNeeded(discardStreams(), proj, p, "", ""); err != nil {
		t.Fatalf("no-flag develop on a configured project should be a no-op: %v", err)
	}
}

// A flag fixes its axis; on a non-TTY the un-flagged axis falls back to the
// favourite rather than prompting, and the flagged axis is honored.
func TestOnboardPartialFlagWritesConfig(t *testing.T) {
	p, proj := onboardPaths(t)
	// Pin stdin to a non-TTY (a pipe) so the un-flagged template axis takes the
	// favourite fallback deterministically instead of trying to prompt.
	r, _, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()

	if err := onboardIfNeeded(discardStreams(), proj, p, "", "codex"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(p.Dir, "byre.config")) // host-side store
	if err != nil {
		t.Fatalf("expected byre.config written: %v", err)
	}
	if !strings.Contains(string(b), `agent = "codex"`) {
		t.Fatalf("the --agent flag must be honored: %s", b)
	}
}

// Full picker on a TTY: declining the shared-auth offer WITHOUT saving
// records nothing — the offer is per box (ADR 0025), so a later project's
// onboarding asks about its own box again.
func TestOnboardSharedAuthDeclineRecordsNothingAndReasks(t *testing.T) {
	p, proj := onboardPaths(t)
	// Template: none, Agent: claude, shared auth: n, save-as-default: n.
	s, _, errBuf := testStreams("\nclaude\nn\nn\n", true)
	if err := onboardIfNeeded(s, proj, p, "", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "Opt this box into claude shared credentials?") {
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
	if err := onboardIfNeeded(s2, proj2, p2, "", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf2.String(), "Opt this box into claude shared credentials?") {
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
	if err := onboardIfNeeded(s, proj, p, "", ""); err != nil {
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
	if err := onboardIfNeeded(s2, proj2, p2, "", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf2.String(), "Opt this box into claude shared credentials?") {
		t.Fatalf("one box's yes must not settle the question for the next box:\n%s", errBuf2.String())
	}
}

// A shared_auth_declined in default.config — saved by the picker, hand-set,
// or left behind by v0.1.7 — is the "don't offer on new boxes" default: the
// offer must not appear (delete the entry to be re-asked).
func TestOnboardSavedDeclineKeySuppressesOffer(t *testing.T) {
	p, proj := onboardPaths(t)
	if err := os.WriteFile(filepath.Join(p.Home, "default.config"), []byte("shared_auth_declined = [\"claude\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Template: none, Agent: claude, save-as-default: n — no offer between.
	s, _, errBuf := testStreams("\nclaude\nn\n", true)
	if err := onboardIfNeeded(s, proj, p, "", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), "Opt this box") {
		t.Fatalf("a saved decline must suppress the offer:\n%s", errBuf.String())
	}
}

// Declining the offer and SAVING makes "don't offer" the machine default:
// shared_auth_declined is written (alongside the favourites) and the next
// box's onboarding skips the question entirely.
func TestOnboardDeclineSavedBecomesDefault(t *testing.T) {
	p, proj := onboardPaths(t)
	// Template: none, Agent: claude, shared auth: n, save-as-default: y.
	s, _, errBuf := testStreams("\nclaude\nn\ny\n", true)
	if err := onboardIfNeeded(s, proj, p, "", ""); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseFile(filepath.Join(p.Home, "default.config"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SharedAuthDeclined) != 1 || cfg.SharedAuthDeclined[0] != "claude" {
		t.Fatalf("shared_auth_declined = %v", cfg.SharedAuthDeclined)
	}
	if !strings.Contains(errBuf.String(), "won't be offered") {
		t.Fatalf("the save confirmation must state the new-box effect:\n%s", errBuf.String())
	}

	// Next box: favourites pre-answer template/agent, the offer is settled,
	// and saving would change nothing — no questions consume input at all.
	proj2 := t.TempDir()
	p2, err := project.Resolve(proj2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	s2, _, errBuf2 := testStreams("\n\n", true)
	if err := onboardIfNeeded(s2, proj2, p2, "", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf2.String(), "Opt this box") {
		t.Fatalf("a saved decline must suppress the next box's offer:\n%s", errBuf2.String())
	}
}

// Accepting the offer and SAVING makes shared credentials the machine
// default: the companion lands in default.config's skills (the hand-enable
// representation), so the cascade covers every new box and the offer stops.
func TestOnboardAcceptSavedBecomesMachineDefault(t *testing.T) {
	p, proj := onboardPaths(t)
	// Template: none, Agent: claude, shared auth: y, save-as-default: y.
	s, _, errBuf := testStreams("\nclaude\ny\ny\n", true)
	if err := onboardIfNeeded(s, proj, p, "", ""); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseFile(filepath.Join(p.Home, "default.config"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cfg.Skills, "claude-shared-auth") {
		t.Fatalf("a saved yes must enable the companion machine-wide, skills = %v", cfg.Skills)
	}
	if !strings.Contains(errBuf.String(), "New boxes share the claude login") {
		t.Fatalf("the save confirmation must state the new-box effect:\n%s", errBuf.String())
	}

	// Next box: the machine default covers it — no offer.
	proj2 := t.TempDir()
	p2, err := project.Resolve(proj2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	s2, _, errBuf2 := testStreams("\n\n", true)
	if err := onboardIfNeeded(s2, proj2, p2, "", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf2.String(), "Opt this box") {
		t.Fatalf("a saved yes must suppress the next box's offer:\n%s", errBuf2.String())
	}
}

// The flag path prompts too: --agent fixes the agent, the template is asked on
// a TTY, and the shared-auth offer follows — landing in this box's config.
func TestOnboardFlagPathOffersSharedAuth(t *testing.T) {
	p, proj := onboardPaths(t)
	// Template: none (Enter), shared auth: y.
	s, _, _ := testStreams("\ny\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "claude"); err != nil {
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
	if err := onboardIfNeeded(s, proj, p, "", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), "Opt this box") {
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

// An agent with no READY companion (grok's is retired and declares no
// shared_auth_for) gets no offer.
func TestOnboardNoOfferWithoutReadyCompanion(t *testing.T) {
	p, proj := onboardPaths(t)
	s, _, errBuf := testStreams("\ngrok\nn\n", true)
	if err := onboardIfNeeded(s, proj, p, "", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), "Opt this box") {
		t.Fatalf("no ready companion — no offer:\n%s", errBuf.String())
	}
}

// Both flags given = the caller asked for non-interactive onboarding: no
// shared-auth offer, no stdin reads, even on a TTY.
func TestOnboardFullyFlaggedMakesNoOffer(t *testing.T) {
	p, proj := onboardPaths(t)
	s, _, errBuf := testStreams("", true) // empty stdin: any prompt would EOF
	if err := onboardIfNeeded(s, proj, p, "none", "claude"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), "Opt this box") {
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
	if err := onboardIfNeeded(s, proj, p, "", ""); err == nil {
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
	if err := onboardIfNeeded(s, proj, p, "", ""); err == nil {
		t.Fatal("a failed save-default must abort onboarding")
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); !os.IsNotExist(err) {
		t.Fatalf("byre.config must not exist after an aborted onboarding (it would never re-run): %v", err)
	}
}
