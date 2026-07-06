package main

import (
	"bytes"
	"errors"
	"io"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/commands"
)

// recorderApp returns an app whose every command records its call into calls
// (keyed by command name, value = the parsed arguments it received).
func recorderApp(calls map[string]string) app {
	note := func(k, v string) error { calls[k] = v; return nil }
	return app{
		dockerfile: func(_ commands.Streams, dir string) error { return note("dockerfile", dir) },
		dockerrun:  func(_ commands.Streams, dir string) error { return note("dockerrun", dir) },
		develop: func(_ commands.Streams, dir, tmpl, agent string, selfEdit bool) error {
			return note("develop", strings.Join([]string{dir, tmpl, agent, boolStr(selfEdit)}, " "))
		},
		config: func(_ commands.Streams, dir string, global bool) error {
			return note("config", dir+" "+boolStr(global))
		},
		status: func(_ commands.Streams, dir string, selfEdit bool) error {
			return note("status", dir+" "+boolStr(selfEdit))
		},
		reset: func(_ commands.Streams, dir string, force bool) error {
			return note("reset", dir+" "+boolStr(force))
		},
		forget: func(_ commands.Streams, dir string, force bool) error {
			return note("forget", dir+" "+boolStr(force))
		},
		shell: func(_ commands.Streams, dir string) error { return note("shell", dir) },
		worktree: func(_ commands.Streams, dir, name, path string, selfEdit bool) error {
			return note("worktree", strings.Join([]string{dir, name, path, boolStr(selfEdit)}, " "))
		},
		skillUpdate: func(_ commands.Streams) error { return note("skill update", "-") },
		rebuild:     func(_ commands.Streams, dir string) error { return note("rebuild", dir) },
		rehome:      func(_ commands.Streams, dir, oldID string) error { return note("rehome", dir+" "+oldID) },
	}
}

// testStreams is a buffer-backed Streams; the returned buffer captures Out.
func testStreams() (commands.Streams, *bytes.Buffer) {
	var out bytes.Buffer
	return commands.Streams{Out: &out, Err: io.Discard, In: strings.NewReader("")}, &out
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
		{[]string{"skill", "update"}, "skill update", "-"},
		{[]string{"rebuild"}, "rebuild", "/proj"},
		{[]string{"rehome", "old-id"}, "rehome", "/proj old-id"},
	}
	for _, tc := range cases {
		calls := map[string]string{}
		s, _ := testStreams()
		if err := run(recorderApp(calls), tc.argv, "/proj", s); err != nil {
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
		{"version", "extra"},       // operands after a no-arg command
	}
	for _, argv := range cases {
		calls := map[string]string{}
		s, _ := testStreams()
		err := run(recorderApp(calls), argv, "/proj", s)
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
		s, out := testStreams()
		if err := run(recorderApp(map[string]string{}), argv, "/proj", s); err != nil {
			t.Errorf("%v: help must not error: %v", argv, err)
		}
		if !strings.Contains(out.String(), "Usage: byre <command>") {
			t.Errorf("%v: expected usage on stdout, got %q", argv, out.String())
		}
	}
}

// TestRunVersion pins that `byre version` and `byre --version` print a
// version line without dispatching any command.
func TestRunVersion(t *testing.T) {
	for _, argv := range [][]string{{"version"}, {"--version"}} {
		calls := map[string]string{}
		s, out := testStreams()
		if err := run(recorderApp(calls), argv, "/proj", s); err != nil {
			t.Errorf("%v: must not error: %v", argv, err)
		}
		if len(calls) != 0 {
			t.Errorf("%v: must not dispatch, got %v", argv, calls)
		}
		if !strings.HasPrefix(out.String(), "byre ") {
			t.Errorf("%v: expected a 'byre <version>' line, got %q", argv, out.String())
		}
	}
}

// TestVersionString pins the resolution order: stamped tag, then module
// version, then (devel) with the VCS revision when recorded.
func TestVersionString(t *testing.T) {
	withRev := &debug.BuildInfo{}
	withRev.Main.Version = "(devel)"
	withRev.Settings = []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}}
	fromModule := &debug.BuildInfo{}
	fromModule.Main.Version = "v0.2.1"
	cases := []struct {
		stamped string
		bi      *debug.BuildInfo
		want    string
	}{
		{"v1.0.0", fromModule, "v1.0.0"},      // stamped wins over build info
		{"", fromModule, "v0.2.1"},            // go install ...@vX.Y.Z
		{"", withRev, "(devel) 0123456789ab"}, // local build with VCS info
		{"", &debug.BuildInfo{}, "(devel)"},   // build info without a version
		{"", nil, "(devel)"},                  // no build info at all
	}
	for _, tc := range cases {
		if got := versionString(tc.stamped, tc.bi); got != tc.want {
			t.Errorf("versionString(%q, %+v) = %q, want %q", tc.stamped, tc.bi, got, tc.want)
		}
	}
}

// TestRunSubcommandHelp pins per-subcommand --help: prints that command's
// usage, dispatches nothing, exits clean — for every table entry, -h included.
func TestRunSubcommandHelp(t *testing.T) {
	for _, c := range cmdTable {
		for _, flag := range []string{"--help", "-h"} {
			calls := map[string]string{}
			s, out := testStreams()
			if err := run(recorderApp(calls), []string{c.name, flag}, "/proj", s); err != nil {
				t.Errorf("byre %s %s must not error: %v", c.name, flag, err)
			}
			if len(calls) != 0 {
				t.Errorf("byre %s %s must not dispatch: %v", c.name, flag, calls)
			}
			if !strings.Contains(out.String(), "Usage: byre "+c.name) {
				t.Errorf("byre %s %s output missing its usage line: %q", c.name, flag, out.String())
			}
		}
	}
}

// TestUsageTextCoversTable pins that the generated top-level usage lists every
// command, and that develop's flags are documented in its help — the omission
// that motivated generating usage from the table.
func TestUsageTextCoversTable(t *testing.T) {
	u := usageText()
	for _, c := range cmdTable {
		if !strings.Contains(u, "\n  "+c.name) {
			t.Errorf("top-level usage missing command %q:\n%s", c.name, u)
		}
	}
	for _, c := range cmdTable {
		if c.name != "develop" {
			continue
		}
		for _, flag := range []string{"--template", "--agent", "--self-edit"} {
			if !strings.Contains(c.help, flag) {
				t.Errorf("develop help missing %s", flag)
			}
		}
	}
}

// TestRunCommandErrorPassesThrough pins that a command's own error is returned
// as-is (main maps it to exit 1 / the agent's code), not wrapped as usage.
func TestRunCommandErrorPassesThrough(t *testing.T) {
	boom := errors.New("boom")
	a := recorderApp(map[string]string{})
	a.shell = func(commands.Streams, string) error { return boom }
	s, _ := testStreams()
	if err := run(a, []string{"shell"}, "/proj", s); !errors.Is(err, boom) {
		t.Fatalf("expected the command error back, got %v", err)
	}
}
