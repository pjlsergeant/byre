package main

import (
	"errors"
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
		{},                   // bare completion
		{"tcsh"},             // unknown shell
		{"zsh", "extra"},     // operand after the shell
		{"zsh", "--install"}, // removed post-v0.1.5 (walked back to the eval line); must be an unknown flag
	} {
		_, err := runCompletion(t, argv...)
		var uerr usageError
		if !errors.As(err, &uerr) {
			t.Errorf("completion %v: expected usageError, got %v", argv, err)
		}
	}
}

// TestCompletionHelpCarriesLoadLines pins that the help IS the setup
// instructions: each shell's recommended rc line appears in the parent help
// and in that shell's own --help.
func TestCompletionHelpCarriesLoadLines(t *testing.T) {
	lines := map[string]string{
		"bash":       `eval "$(byre completion bash)"`,
		"zsh":        "source <(byre completion zsh)",
		"fish":       "byre completion fish | source",
		"powershell": "byre completion powershell | Out-String | Invoke-Expression",
	}
	parent, err := runCompletion(t, "--help")
	if err != nil {
		t.Fatalf("completion --help: %v", err)
	}
	for shell, line := range lines {
		if !strings.Contains(parent, line) {
			t.Errorf("completion --help missing the %s load line %q", shell, line)
		}
		own, err := runCompletion(t, shell, "--help")
		if err != nil {
			t.Errorf("completion %s --help: %v", shell, err)
			continue
		}
		if !strings.Contains(own, line) {
			t.Errorf("completion %s --help missing its load line %q", shell, line)
		}
	}
}

// TestCompletionNoDescriptions pins the flag reaches the generators: the
// no-desc scripts drive the hidden __completeNoDesc command instead.
func TestCompletionNoDescriptions(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		out, err := runCompletion(t, shell, "--no-descriptions")
		if err != nil {
			t.Errorf("%s --no-descriptions: %v", shell, err)
			continue
		}
		if !strings.Contains(out, "__completeNoDesc") {
			t.Errorf("%s --no-descriptions script doesn't use __completeNoDesc", shell)
		}
	}
}
