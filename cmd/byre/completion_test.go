package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCompletion drives `byre completion <argv...>` through the real tree and
// returns stdout and the error. No command dispatches (completion isn't in
// the app struct), which TestCompletion* rely on implicitly.
func runCompletion(t *testing.T, argv ...string) (string, error) {
	t.Helper()
	s, out := testStreams()
	err := run(recorderApp(map[string]string{}), append([]string{"completion"}, argv...), "/proj", s)
	return out.String(), err
}

func TestCompletionPrintsScript(t *testing.T) {
	cases := []struct {
		shell  string
		header string
	}{
		{"bash", "bash completion V2 for byre"},
		{"zsh", "#compdef byre"},
		{"fish", "fish completion for byre"},
		{"powershell", "powershell completion for byre"},
	}
	for _, tc := range cases {
		out, err := runCompletion(t, tc.shell)
		if err != nil {
			t.Errorf("completion %s: %v", tc.shell, err)
			continue
		}
		if !strings.Contains(out, tc.header) {
			t.Errorf("completion %s missing %q:\n%.200s", tc.shell, tc.header, out)
		}
	}
}

func TestCompletionUsageErrors(t *testing.T) {
	for _, argv := range [][]string{
		{},                          // bare completion
		{"tcsh"},                    // unknown shell
		{"zsh", "extra"},            // operand after the shell
		{"powershell", "--install"}, // powershell deliberately has no --install
	} {
		_, err := runCompletion(t, argv...)
		var uerr usageError
		if !errors.As(err, &uerr) {
			t.Errorf("completion %v: expected usageError, got %v", argv, err)
		}
	}
}

// TestCompletionInstallFish pins the fish path (XDG_CONFIG_HOME honored),
// the printed-path contract, idempotent re-install, and the foreign-file
// refusal (a byre failure, NOT a usage error).
func TestCompletionInstallFish(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	target := filepath.Join(cfg, "fish", "completions", "byre.fish")

	out, err := runCompletion(t, "fish", "--install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if strings.TrimSpace(out) != target {
		t.Errorf("stdout = %q, want the target path %q", out, target)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("target not written: %v", err)
	}
	if !strings.Contains(string(body), "fish completion for byre") {
		t.Errorf("written file isn't the completion script:\n%.200s", body)
	}

	if _, err := runCompletion(t, "fish", "--install"); err != nil {
		t.Fatalf("re-install over byre's own file must succeed: %v", err)
	}

	if err := os.WriteFile(target, []byte("someone else's file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = runCompletion(t, "fish", "--install")
	if err == nil {
		t.Fatal("foreign file: expected a refusal")
	}
	var uerr usageError
	if errors.As(err, &uerr) {
		t.Errorf("foreign-file refusal must be a byre failure (exit 1), not usage: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "someone else's file\n" {
		t.Errorf("foreign file was modified: %q", got)
	}
}

func TestCompletionInstallBashHonorsXDG(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	target := filepath.Join(data, "bash-completion", "completions", "byre")
	out, err := runCompletion(t, "bash", "--install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if strings.TrimSpace(out) != target {
		t.Errorf("stdout = %q, want %q", out, target)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("target not written: %v", err)
	}
}

func TestCompletionInstallZshSiteDir(t *testing.T) {
	site := t.TempDir()
	restore := zshSiteFunctionDirs
	zshSiteFunctionDirs = []string{filepath.Join(site, "missing"), site}
	defer func() { zshSiteFunctionDirs = restore }()

	out, err := runCompletion(t, "zsh", "--install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	target := filepath.Join(site, "_byre")
	if strings.TrimSpace(out) != target {
		t.Errorf("stdout = %q, want %q", out, target)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("target not written: %v", err)
	}
}

// TestCompletionInstallZshFallback pins the ~/.zfunc fallback when no
// site-functions dir exists, and that a foreign _byre in a site dir REFUSES
// rather than falling through to a shadowed second copy.
func TestCompletionInstallZshFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	restore := zshSiteFunctionDirs
	zshSiteFunctionDirs = []string{filepath.Join(home, "no-such-dir")}
	defer func() { zshSiteFunctionDirs = restore }()

	out, err := runCompletion(t, "zsh", "--install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	target := filepath.Join(home, ".zfunc", "_byre")
	if strings.TrimSpace(out) != target {
		t.Errorf("stdout = %q, want %q", out, target)
	}

	site := t.TempDir()
	zshSiteFunctionDirs = []string{site}
	if err := os.WriteFile(filepath.Join(site, "_byre"), []byte("not ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCompletion(t, "zsh", "--install"); err == nil {
		t.Fatal("foreign _byre in the operative site dir must refuse, not shadow")
	}
}
