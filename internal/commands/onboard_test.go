package commands

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

// Full picker on a TTY: declining the shared-auth offer is recorded in
// default.config (shared_auth_declined), and a later project's onboarding
// must not re-ask — the offer happens at most once per agent.
func TestOnboardSharedAuthDeclineRecordedAndNotReasked(t *testing.T) {
	p, proj := onboardPaths(t)
	// Template: none, Agent: claude, save-as-default: n, shared auth: n.
	s, _, errBuf := testStreams("\nclaude\nn\nn\n", true)
	if err := onboardIfNeeded(s, proj, p, "", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "Share one claude login across all your projects?") {
		t.Fatalf("expected the shared-auth offer:\n%s", errBuf.String())
	}
	cfg, err := config.ParseFile(filepath.Join(p.Home, "default.config"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SharedAuthDeclined) != 1 || cfg.SharedAuthDeclined[0] != "claude" {
		t.Fatalf("shared_auth_declined = %v", cfg.SharedAuthDeclined)
	}

	// A second project, same home: the offer must not reappear (the input
	// carries no answer for it; a re-ask would show in the output).
	proj2 := t.TempDir()
	p2, err := project.Resolve(proj2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	s2, _, errBuf2 := testStreams("\nclaude\nn\n", true)
	if err := onboardIfNeeded(s2, proj2, p2, "", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf2.String(), "shared auth") {
		t.Fatalf("declined offer must not be re-asked:\n%s", errBuf2.String())
	}
}

// Accepting the offer enables the companion skill machine-wide: it lands in
// default.config's skills list — the same representation as a hand-enabled
// companion, so there is no second source of truth.
func TestOnboardSharedAuthAcceptEnablesCompanion(t *testing.T) {
	p, proj := onboardPaths(t)
	s, _, errBuf := testStreams("\nclaude\nn\ny\n", true)
	if err := onboardIfNeeded(s, proj, p, "", ""); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseFile(filepath.Join(p.Home, "default.config"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cfg.Skills, "claude-shared-auth") {
		t.Fatalf("accepting must enable the companion in default.config, skills = %v", cfg.Skills)
	}
	if !strings.Contains(errBuf.String(), "every project on this machine") {
		t.Fatalf("the confirmation must state the machine-wide scope:\n%s", errBuf.String())
	}
}

// The flag path prompts too: --agent fixes the agent, the template is asked on
// a TTY, and the shared-auth offer follows.
func TestOnboardFlagPathOffersSharedAuth(t *testing.T) {
	p, proj := onboardPaths(t)
	// Template: none (Enter), shared auth: y.
	s, _, _ := testStreams("\ny\n", true)
	if err := onboardIfNeeded(s, proj, p, "", "claude"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseFile(filepath.Join(p.Home, "default.config"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cfg.Skills, "claude-shared-auth") {
		t.Fatalf("skills = %v", cfg.Skills)
	}
}

// An agent with no READY companion (grok's is broken and declares no
// shared_auth_for) gets no offer.
func TestOnboardNoOfferWithoutReadyCompanion(t *testing.T) {
	p, proj := onboardPaths(t)
	s, _, errBuf := testStreams("\ngrok\nn\n", true)
	if err := onboardIfNeeded(s, proj, p, "", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), "shared auth") {
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
	if strings.Contains(errBuf.String(), "shared auth") {
		t.Fatalf("fully-flagged onboarding must stay non-interactive:\n%s", errBuf.String())
	}
	if _, err := os.Stat(filepath.Join(p.Home, "default.config")); !os.IsNotExist(err) {
		t.Fatalf("no offer — nothing may be recorded in default.config: %v", err)
	}
}

// Ctrl-D (EOF) at the optional shared-auth question must not fail a develop
// whose onboarding already succeeded — and records nothing.
func TestOnboardSharedAuthEOFSkips(t *testing.T) {
	p, proj := onboardPaths(t)
	// Template, agent, save-default answered; input ends before the offer.
	s, _, _ := testStreams("\nclaude\nn\n", true)
	if err := onboardIfNeeded(s, proj, p, "", ""); err != nil {
		t.Fatalf("EOF at the offer must not fail onboarding: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); err != nil {
		t.Fatalf("byre.config should have been written: %v", err)
	}
	cfg, err := config.ParseFile(filepath.Join(p.Home, "default.config"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SharedAuthDeclined) != 0 || slices.Contains(cfg.Skills, "claude-shared-auth") {
		t.Fatalf("no answer — nothing may be recorded: %+v", cfg)
	}
}
