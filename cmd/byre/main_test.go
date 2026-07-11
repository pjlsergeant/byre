package main

import (
	"bytes"
	"errors"
	"io"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/commands"
	"github.com/pjlsergeant/byre/internal/deliver"
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
		shell:         func(_ commands.Streams, dir string) error { return note("shell", dir) },
		ejectfirewall: func(_ commands.Streams, dir string) error { return note("ejectfirewall", dir) },
		deliver: func(_ commands.Streams, dir string, opts deliver.Options, paths []string) error {
			return note("deliver", strings.Join([]string{dir, opts.Box, opts.Name,
				boolStr(opts.SkipUIDCheck), boolStr(opts.NoClip), strings.Join(paths, ",")}, " "))
		},
		installApp: func(_ commands.Streams, box string) error { return note("install-app", box) },
		worktree: func(_ commands.Streams, dir, name, path string, selfEdit bool) error {
			return note("worktree", strings.Join([]string{dir, name, path, boolStr(selfEdit)}, " "))
		},
		skillUpdate:      func(_ commands.Streams) error { return note("skill update", "-") },
		rebuild:          func(_ commands.Streams, dir string) error { return note("rebuild", dir) },
		rehome:           func(_ commands.Streams, dir, oldID string) error { return note("rehome", dir+" "+oldID) },
		rehomeCandidates: func(_ commands.Streams, dir string) error { return note("rehome candidates", dir) },
		version:          func(_ commands.Streams) error { return note("version", "-") },
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
		// A value-taking flag consumes a following --help (standard
		// docker/kubectl behavior; ADR 0022, Pete-ratified) — this DISPATCHES,
		// it does not print help. Do not restore a pre-parse help scan.
		{[]string{"develop", "--template", "--help"}, "develop", "/proj --help  false"},
		{[]string{"config"}, "config", "/proj false"},
		{[]string{"config", "--global"}, "config", "/proj true"},
		{[]string{"status"}, "status", "/proj false"},
		{[]string{"status", "--self-edit"}, "status", "/proj true"},
		{[]string{"reset"}, "reset", "/proj false"},
		{[]string{"reset", "--force"}, "reset", "/proj true"},
		{[]string{"reset", "-y"}, "reset", "/proj true"},
		{[]string{"forget", "--force"}, "forget", "/proj true"},
		{[]string{"shell"}, "shell", "/proj"},
		{[]string{"ejectfirewall"}, "ejectfirewall", "/proj"},
		{[]string{"deliver", "a.txt", "b.txt"}, "deliver", "/proj   false false a.txt,b.txt"},
		{[]string{"deliver", "--box", "x", "--no-clip", "f"}, "deliver", "/proj x  false true f"},
		{[]string{"deliver", "--box=x", "--name=n.txt", "--skip-uid-check", "-"}, "deliver", "/proj x n.txt true false -"},
		{[]string{"deliver", "--install-app"}, "install-app", ""},
		{[]string{"deliver", "--install-app", "--box", "abc"}, "install-app", "abc"},
		{[]string{"worktree", "feat"}, "worktree", "/proj feat  false"},
		{[]string{"worktree", "feat", "--path", "/tmp/x", "--self-edit"}, "worktree", "/proj feat /tmp/x true"},
		{[]string{"skill", "update"}, "skill update", "-"},
		{[]string{"rebuild"}, "rebuild", "/proj"},
		{[]string{"rehome", "old-id"}, "rehome", "/proj old-id"},
		{[]string{"rehome"}, "rehome candidates", "/proj"}, // bare = list likely old ids
		{[]string{"version"}, "version", "-"},
		{[]string{"--version"}, "version", "-"}, // alias for the table entry
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
		{},                                    // no command
		{"bogus"},                             // unknown command
		{"dockerfile", "extra"},               // operands after a no-arg command
		{"develop", "--template"},             // flag missing its value
		{"develop", "--bogus"},                // unknown flag
		{"config", "--bogus"},                 // unknown flag
		{"status", "--bogus"},                 // unknown flag
		{"reset", "--bogus"},                  // unknown flag
		{"worktree"},                          // missing name
		{"worktree", "--bogus"},               // unknown flag
		{"worktree", "a", "b"},                // extra operand
		{"deliver", "--bogus"},                // unknown flag
		{"deliver", "-", "x.txt"},             // stdin mixed with paths
		{"deliver", "--install-app", "x.txt"}, // install-app takes no paths
		{"deliver", "--install-app", "--no-clip=false"}, // supplied flag, even =false
		{"skill"},                  // missing subcommand
		{"skill", "bogus"},         // unknown subcommand
		{"rehome", "old", "extra"}, // extra operand (bare rehome is valid: it lists candidates)
		{"version", "extra"},       // operands after a no-arg command
		{"--version", "extra"},     // the alias gets the same operand check
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
		if !strings.Contains(out.String(), "Available Commands:") {
			t.Errorf("%v: expected usage on stdout, got %q", argv, out.String())
		}
	}
}

// TestPrintVersion pins the real implementation's output shape; dispatch of
// `version` and `--version` to it is pinned in TestRunDispatch.
func TestPrintVersion(t *testing.T) {
	s, out := testStreams()
	if err := printVersion(s); err != nil {
		t.Fatalf("printVersion: %v", err)
	}
	if !strings.HasPrefix(out.String(), "byre ") {
		t.Errorf("expected a 'byre <version>' line, got %q", out.String())
	}
}

// TestVersionString pins the resolution order: stamped tag, then module
// version, then (devel) with the VCS revision when recorded.
func TestVersionString(t *testing.T) {
	withRev := &debug.BuildInfo{}
	withRev.Main.Version = "(devel)"
	withRev.Settings = []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}}
	shortRev := &debug.BuildInfo{}
	shortRev.Settings = []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}
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
		{"", shortRev, "(devel) abc"},         // revision shorter than display width
		{"", &debug.BuildInfo{}, "(devel)"},   // build info without a version
		{"", nil, "(devel)"},                  // no build info at all
	}
	for _, tc := range cases {
		if got := versionString(tc.stamped, tc.bi); got != tc.want {
			t.Errorf("versionString(%q, %+v) = %q, want %q", tc.stamped, tc.bi, got, tc.want)
		}
	}
}

// commandNames enumerates the registered subcommands off a throwaway tree —
// the successor to iterating the old command table.
func commandNames() []string {
	s, _ := testStreams()
	root := newRootCmd(recorderApp(map[string]string{}), "/proj", s)
	var names []string
	for _, c := range root.Commands() {
		names = append(names, c.Name())
	}
	return names
}

// TestRunSubcommandHelp pins per-subcommand --help: prints that command's
// usage, dispatches nothing, exits clean — for every command, -h included.
func TestRunSubcommandHelp(t *testing.T) {
	for _, name := range commandNames() {
		for _, flag := range []string{"--help", "-h"} {
			calls := map[string]string{}
			s, out := testStreams()
			if err := run(recorderApp(calls), []string{name, flag}, "/proj", s); err != nil {
				t.Errorf("byre %s %s must not error: %v", name, flag, err)
			}
			if len(calls) != 0 {
				t.Errorf("byre %s %s must not dispatch: %v", name, flag, calls)
			}
			if !strings.Contains(out.String(), "byre "+name) {
				t.Errorf("byre %s %s output missing its usage line: %q", name, flag, out.String())
			}
		}
	}
}

// TestRootHelpCoversCommands pins that the top-level help lists every
// registered command, and that develop's flags are documented in its help —
// the omission that motivated generating usage in the first place.
func TestRootHelpCoversCommands(t *testing.T) {
	s, out := testStreams()
	if err := run(recorderApp(map[string]string{}), []string{"--help"}, "/proj", s); err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, name := range commandNames() {
		if !strings.Contains(out.String(), name) {
			t.Errorf("top-level help missing command %q:\n%s", name, out.String())
		}
	}
	s2, out2 := testStreams()
	if err := run(recorderApp(map[string]string{}), []string{"develop", "--help"}, "/proj", s2); err != nil {
		t.Fatalf("develop --help: %v", err)
	}
	for _, flag := range []string{"--template", "--agent", "--self-edit"} {
		if !strings.Contains(out2.String(), flag) {
			t.Errorf("develop help missing %s", flag)
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
