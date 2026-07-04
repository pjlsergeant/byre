package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func proposeConfig(t *testing.T, projectDir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(projectDir, "byre.config"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptYesCopiesToStore(t *testing.T) {
	p, proj := onboardPaths(t)
	proposeConfig(t, proj, "agent = \"codex\"\nrun_args = [\"--privileged\"]\n")

	s, _, out := testStreams("y\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	// The grant summary must surface the dangerous run_args.
	if !strings.Contains(out.String(), "--privileged") {
		t.Errorf("adopt prompt should surface run_args grant:\n%s", out.String())
	}
	// Copied into the store + record written.
	b, err := os.ReadFile(filepath.Join(p.Dir, "byre.config"))
	if err != nil || !strings.Contains(string(b), "codex") {
		t.Fatalf("config not adopted into the store: %v / %s", err, b)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "adopted")); err != nil {
		t.Errorf("adoption record not written: %v", err)
	}

	// Second call with the same proposal is a no-op (unchanged): no prompt output.
	s2, _, out2 := testStreams("", true)
	if err := adoptIfProposed(s2, proj, p); err != nil {
		t.Fatal(err)
	}
	if out2.Len() != 0 {
		t.Errorf("unchanged proposal should not re-prompt: %s", out2.String())
	}
}

// A proposal that only SELECTS a template must still surface the grants that
// template contributes — the adoption summary reflects the effective config.
func TestAdoptShowsTemplateContributedGrants(t *testing.T) {
	p, proj := onboardPaths(t)
	tmplDir := filepath.Join(p.Home, "templates", "danger")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "template.config"), []byte("run_args = [\"--privileged\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proposeConfig(t, proj, "template = \"danger\"\n") // proposal itself looks innocent

	s, _, out := testStreams("n\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--privileged") {
		t.Errorf("adoption must surface template-contributed grants:\n%s", out.String())
	}
}

func TestAdoptNoLeavesStoreUntouched(t *testing.T) {
	p, proj := onboardPaths(t)
	proposeConfig(t, proj, "agent = \"codex\"\n")

	s, _, _ := testStreams("n\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); !os.IsNotExist(err) {
		t.Errorf("declined proposal must not be written to the store")
	}
}

func TestAdoptNonTTYNeverAdopts(t *testing.T) {
	p, proj := onboardPaths(t)
	proposeConfig(t, proj, "agent = \"codex\"\n")

	s, _, out := testStreams("y\n", false)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); !os.IsNotExist(err) {
		t.Errorf("non-TTY must never adopt, even with 'y' piped in")
	}
	if !strings.Contains(out.String(), "interactively") {
		t.Errorf("non-TTY should tell the user to run interactively:\n%s", out.String())
	}
}

func TestAdoptChangedReprompts(t *testing.T) {
	p, proj := onboardPaths(t)
	proposeConfig(t, proj, "agent = \"codex\"\n")
	s, _, _ := testStreams("y\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	// Change the proposal: it must prompt again (hash differs).
	proposeConfig(t, proj, "agent = \"claude\"\n")
	s, _, out := testStreams("y\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "changed") {
		t.Errorf("a changed proposal should re-prompt as changed:\n%s", out.String())
	}
	b, _ := os.ReadFile(filepath.Join(p.Dir, "byre.config"))
	if !strings.Contains(string(b), "claude") {
		t.Errorf("re-adopt should update the store: %s", b)
	}
}
