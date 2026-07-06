package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
