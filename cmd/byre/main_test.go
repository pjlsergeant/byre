package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// recorderApp returns an app whose every command records its call into calls
// (keyed by command name, value = the parsed arguments it received).
func recorderApp(calls map[string]string) app {
	note := func(k, v string) error { calls[k] = v; return nil }
	return app{
		dockerfile: func(_ io.Writer, dir string) error { return note("dockerfile", dir) },
		dockerrun:  func(_ io.Writer, dir string) error { return note("dockerrun", dir) },
		develop: func(dir, tmpl, agent string, selfEdit bool) error {
			return note("develop", strings.Join([]string{dir, tmpl, agent, boolStr(selfEdit)}, " "))
		},
		config: func(dir string, global bool) error { return note("config", dir+" "+boolStr(global)) },
		status: func(_ io.Writer, dir string, selfEdit bool) error {
			return note("status", dir+" "+boolStr(selfEdit))
		},
		reset: func(_ io.Writer, _ io.Reader, dir string, force bool) error {
			return note("reset", dir+" "+boolStr(force))
		},
		forget: func(_ io.Writer, _ io.Reader, dir string, force bool) error {
			return note("forget", dir+" "+boolStr(force))
		},
		shell: func(_ io.Writer, dir string) error { return note("shell", dir) },
		worktree: func(dir, name, path string, selfEdit bool) error {
			return note("worktree", strings.Join([]string{dir, name, path, boolStr(selfEdit)}, " "))
		},
		skillUpdate: func(_ io.Writer, dir string) error { return note("skill update", dir) },
		rebuild:     func(_ io.Writer, dir string) error { return note("rebuild", dir) },
		rehome:      func(_ io.Writer, dir, oldID string) error { return note("rehome", dir+" "+oldID) },
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestRunDispatch pins the flag->function wiring: each argv reaches exactly
// one command, with the flags parsed into the right arguments.
func TestRunDispatch(t *testing.T) {
	cases := []struct {
		argv []string
		cmd  string // recorded key
		args string // recorded value
	}{
		{[]string{"dockerfile"}, "dockerfile", "/proj"},
		{[]string{"dockerrun"}, "dockerrun", "/proj"},
		{[]string{"develop"}, "develop", "/proj   false"},
		{[]string{"develop", "--template", "go", "--agent", "codex", "--self-edit"}, "develop", "/proj go codex true"},
		{[]string{"config"}, "config", "/proj false"},
		{[]string{"config", "--global"}, "config", "/proj true"},
		{[]string{"status"}, "status", "/proj false"},
		{[]string{"status", "--self-edit"}, "status", "/proj true"},
		{[]string{"reset"}, "reset", "/proj false"},
		{[]string{"reset", "--force"}, "reset", "/proj true"},
		{[]string{"reset", "-y"}, "reset", "/proj true"},
		{[]string{"forget", "--force"}, "forget", "/proj true"},
		{[]string{"shell"}, "shell", "/proj"},
		{[]string{"worktree", "feat"}, "worktree", "/proj feat  false"},
		{[]string{"worktree", "feat", "--path", "/tmp/x", "--self-edit"}, "worktree", "/proj feat /tmp/x true"},
		{[]string{"skill", "update"}, "skill update", "/proj"},
		{[]string{"rebuild"}, "rebuild", "/proj"},
		{[]string{"rehome", "old-id"}, "rehome", "/proj old-id"},
	}
	for _, tc := range cases {
		calls := map[string]string{}
		if err := run(recorderApp(calls), tc.argv, "/proj", io.Discard, strings.NewReader("")); err != nil {
			t.Errorf("%v: unexpected error %v", tc.argv, err)
			continue
		}
		if len(calls) != 1 {
			t.Errorf("%v: expected exactly one command called, got %v", tc.argv, calls)
			continue
		}
		if got := calls[tc.cmd]; got != tc.args {
			t.Errorf("%v: %s called with %q, want %q", tc.argv, tc.cmd, got, tc.args)
		}
	}
}

// TestRunUsageErrors pins that parse failures come back as usageError (exit 2
// in main) without dispatching any command.
func TestRunUsageErrors(t *testing.T) {
	cases := [][]string{
		{},                         // no command
		{"bogus"},                  // unknown command
		{"dockerfile", "extra"},    // operands after a no-arg command
		{"develop", "--template"},  // flag missing its value
		{"develop", "--bogus"},     // unknown flag
		{"config", "--bogus"},      // unknown flag
		{"status", "--bogus"},      // unknown flag
		{"reset", "--bogus"},       // unknown flag
		{"worktree"},               // missing name
		{"worktree", "--bogus"},    // unknown flag
		{"worktree", "a", "b"},     // extra operand
		{"skill"},                  // missing subcommand
		{"skill", "bogus"},         // unknown subcommand
		{"rehome"},                 // missing old id
		{"rehome", "old", "extra"}, // extra operand
	}
	for _, argv := range cases {
		calls := map[string]string{}
		err := run(recorderApp(calls), argv, "/proj", io.Discard, strings.NewReader(""))
		var uerr usageError
		if !errors.As(err, &uerr) {
			t.Errorf("%v: expected usageError, got %v", argv, err)
		}
		if len(calls) != 0 {
			t.Errorf("%v: usage error must not dispatch, got %v", argv, calls)
		}
	}
}

func TestRunHelpPrintsUsage(t *testing.T) {
	for _, argv := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		var out bytes.Buffer
		if err := run(recorderApp(map[string]string{}), argv, "/proj", &out, strings.NewReader("")); err != nil {
			t.Errorf("%v: help must not error: %v", argv, err)
		}
		if !strings.Contains(out.String(), "Usage: byre <command>") {
			t.Errorf("%v: expected usage on stdout, got %q", argv, out.String())
		}
	}
}

// TestRunCommandErrorPassesThrough pins that a command's own error is returned
// as-is (main maps it to exit 1 / the agent's code), not wrapped as usage.
func TestRunCommandErrorPassesThrough(t *testing.T) {
	boom := errors.New("boom")
	a := recorderApp(map[string]string{})
	a.shell = func(io.Writer, string) error { return boom }
	if err := run(a, []string{"shell"}, "/proj", io.Discard, strings.NewReader("")); !errors.Is(err, boom) {
		t.Fatalf("expected the command error back, got %v", err)
	}
}
