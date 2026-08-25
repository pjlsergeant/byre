package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
		develop: func(_ commands.Streams, dir, tmpl, agent string, sharedAuth *bool, selfEdit bool, _ commands.CredentialMode) error {
			sa := "unset"
			if sharedAuth != nil {
				sa = boolStr(*sharedAuth)
			}
			return note("develop", strings.Join([]string{dir, tmpl, agent, sa, boolStr(selfEdit)}, " "))
		},
		config: func(_ commands.Streams, dir string, global bool, layer string) error {
			return note("config", dir+" "+boolStr(global)+" "+layer)
		},
		status: func(_ commands.Streams, dir string, opts commands.StatusOptions) error {
			return note("status", strings.Join([]string{dir,
				boolStr(opts.SelfEdit), boolStr(opts.Full), boolStr(opts.Data)}, " "))
		},
		reset: func(_ commands.Streams, dir string, force bool) error {
			return note("reset", dir+" "+boolStr(force))
		},
		forget: func(_ commands.Streams, dir string, force bool) error {
			return note("forget", dir+" "+boolStr(force))
		},
		shell: func(_ commands.Streams, dir string, skipUID bool) error {
			return note("shell", dir+" "+boolStr(skipUID))
		},
		ejectfirewall: func(_ commands.Streams, dir string) error { return note("ejectfirewall", dir) },
		deliver: func(_ commands.Streams, dir string, opts deliver.Options, paths []string) error {
			return note("deliver", strings.Join([]string{dir, opts.Box, opts.Name,
				boolStr(opts.SkipUIDCheck), boolStr(opts.NoClip),
				boolStr(opts.Boxes), boolStr(opts.Tar), fmt.Sprintf("p%d", opts.Proto), opts.RemoteByre,
				strings.Join(paths, ",")}, " "))
		},
		grab: func(_ commands.Streams, dir string, opts deliver.Options, boxPath, hostPath string) error {
			return note("grab", strings.Join([]string{dir, opts.Box,
				boolStr(opts.SkipUIDCheck), boxPath, hostPath}, " "))
		},
		installApp: func(_ commands.Streams, o commands.InstallAppOptions) error {
			return note("install-app", fmt.Sprintf("%q %q %q %q", o.Box, o.Name, o.RemoteByre, o.SSH))
		},
		worktree: func(_ commands.Streams, dir, name, path, agent string, selfEdit bool, _ commands.CredentialMode) error {
			return note("worktree", strings.Join([]string{dir, name, path, agent, boolStr(selfEdit)}, " "))
		},
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
		{[]string{"develop"}, "develop", "/proj   unset false"},
		{[]string{"develop", "--template", "go", "--agent", "codex", "--self-edit"}, "develop", "/proj go codex unset true"},
		// --shared-auth is tri-state: unset (ask when interactive), an explicit
		// yes, or an explicit no — the wiring must preserve which one it was.
		{[]string{"develop", "--agent", "claude", "--shared-auth"}, "develop", "/proj  claude true false"},
		{[]string{"develop", "--agent", "claude", "--shared-auth=false"}, "develop", "/proj  claude false false"},
		// A value-taking flag consumes a following --help (standard
		// docker/kubectl behavior; ADR 0022) — this DISPATCHES,
		// it does not print help. Do not restore a pre-parse help scan.
		{[]string{"develop", "--template", "--help"}, "develop", "/proj --help  unset false"},
		{[]string{"config"}, "config", "/proj false "},
		{[]string{"config", "--global"}, "config", "/proj true "},
		{[]string{"config", "--layer", "torn"}, "config", "/proj false torn"},
		{[]string{"status"}, "status", "/proj false false false"},
		{[]string{"status", "--self-edit"}, "status", "/proj true false false"},
		{[]string{"status", "--full"}, "status", "/proj false true false"},
		{[]string{"status", "--data"}, "status", "/proj false false true"},
		{[]string{"reset"}, "reset", "/proj false"},
		{[]string{"reset", "--force"}, "reset", "/proj true"},
		{[]string{"reset", "-y"}, "reset", "/proj true"},
		{[]string{"forget", "--force"}, "forget", "/proj true"},
		{[]string{"shell"}, "shell", "/proj false"},
		{[]string{"shell", "--skip-uid-check"}, "shell", "/proj true"},
		{[]string{"ejectfirewall"}, "ejectfirewall", "/proj"},
		{[]string{"deliver", "a.txt", "b.txt"}, "deliver", "/proj   false false false false p0  a.txt,b.txt"},
		{[]string{"deliver", "--box", "x", "--no-clip", "f"}, "deliver", "/proj x  false true false false p0  f"},
		{[]string{"deliver", "--box=x", "--name=n.txt", "--skip-uid-check", "-"}, "deliver", "/proj x n.txt true false false false p0  -"},
		// The remote-facing surface (ADR 0037): enumeration, tar transport,
		// and the protocol handshake reach Deliver as options.
		{[]string{"deliver", "--boxes", "--proto", "1"}, "deliver", "/proj   false false true false p1  "},
		{[]string{"deliver", "--boxes", "--skip-uid-check"}, "deliver", "/proj   true false true false p0  "},
		{[]string{"deliver", "--tar", "--proto=1", "--box", "abc", "--no-clip", "-"}, "deliver", "/proj abc  false true false true p1  -"},
		{[]string{"deliver", "ssh://dev@far", "shot.png"}, "deliver", "/proj   false false false false p0  ssh://dev@far,shot.png"},
		{[]string{"deliver", "--remote-byre", "/opt/byre", "ssh://far", "f"}, "deliver", "/proj   false false false false p0 /opt/byre ssh://far,f"},
		{[]string{"deliver", "--install-app"}, "install-app", `"" "" "" ""`},
		{[]string{"deliver", "--install-app", "--box", "abc"}, "install-app", `"abc" "" "" ""`},
		{[]string{"deliver", "--install-app", "ssh://u@h:2222", "--name", "n", "--remote-byre", "/x", "--box", "b"}, "install-app", `"b" "n" "/x" "ssh://u@h:2222"`},
		{[]string{"grab", "out/report.pdf"}, "grab", "/proj  false out/report.pdf ."},
		{[]string{"grab", "/workspace/a.txt", "~/dl"}, "grab", "/proj  false /workspace/a.txt ~/dl"},
		{[]string{"grab", "--box", "x", "--skip-uid-check", "a.txt", "-"}, "grab", "/proj x true a.txt -"},
		{[]string{"worktree", "feat"}, "worktree", "/proj feat   false"},
		{[]string{"worktree", "feat", "--path", "/tmp/x", "--self-edit"}, "worktree", "/proj feat /tmp/x  true"},
		{[]string{"worktree", "feat", "-a", "codex"}, "worktree", "/proj feat  codex false"},
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

// TestArityUsageErrorsNameTheShape pins the usage SHAPE each arity rejection
// prints, per the strict tier (CLAUDE.md): an arity rejection must name the rule
// that fired, not merely produce some usageError. These commands used cobra's
// validators, which exited 1 with "accepts 1 arg(s), received 0" -- so a wrong
// or typo'd usage string, or a different rule rejecting first, would be
// invisible to a presence-only check. Sixteen Args: sites, eighteen resolved
// commands: installCmd/uninstallCmd each bind both package nouns.
func TestArityUsageErrorsNameTheShape(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"skill", "inspect"}, "usage: byre skill inspect <id|uri>"},
		{[]string{"skill", "inspect", "a", "b"}, "usage: byre skill inspect <id|uri>"},
		{[]string{"skill", "pack"}, "usage: byre skill pack <name>"},
		{[]string{"skill", "fork", "one"}, "usage: byre skill fork <id> <new-id>"},
		{[]string{"skill", "init"}, "usage: byre skill init <name>"},
		{[]string{"skill", "validate", "a", "b"}, "usage: byre skill validate [name]"},
		{[]string{"skill", "install"}, "usage: byre skill install <manifest-uri>"},
		{[]string{"skill", "uninstall"}, "usage: byre skill uninstall <id>"},
		{[]string{"template", "inspect"}, "usage: byre template inspect <id|uri>"},
		{[]string{"template", "pack"}, "usage: byre template pack <name>"},
		{[]string{"template", "fork", "one"}, "usage: byre template fork <id> <new-id>"},
		{[]string{"template", "init"}, "usage: byre template init <name>"},
		{[]string{"template", "validate", "a", "b"}, "usage: byre template validate [name]"},
		{[]string{"template", "install"}, "usage: byre template install <manifest-uri>"},
		{[]string{"template", "uninstall"}, "usage: byre template uninstall <id>"},
		{[]string{"preset", "apply", "a", "b"}, "usage: byre preset apply [<uri>|<path>]"},
		{[]string{"preset", "inspect", "a", "b"}, "usage: byre preset inspect [<uri>|<path>]"},
		{[]string{"layer", "new"}, "usage: byre layer new <name>"},
		{[]string{"layer", "validate", "a", "b"}, "usage: byre layer validate [name]"},
	}
	for _, tc := range cases {
		calls := map[string]string{}
		s, _ := testStreams()
		err := run(recorderApp(calls), tc.argv, "/proj", s)
		var uerr usageError
		if !errors.As(err, &uerr) {
			t.Errorf("%v: expected usageError (exit 2), got %v", tc.argv, err)
			continue
		}
		if got := string(uerr); !strings.Contains(got, tc.want) {
			t.Errorf("%v: usage error = %q, want it to name %q", tc.argv, got, tc.want)
		}
		if len(calls) != 0 {
			t.Errorf("%v: usage error must not dispatch, got %v", tc.argv, calls)
		}
	}
}

// TestRunUsageErrors pins that parse failures come back as usageError (exit 2
// in main) without dispatching any command. Rows carrying a want fragment are
// byre's OWN rules -- the fragment names which rule fired, because several
// rules can reject one argv and the wrong one keeps a presence-only check
// green (the strict tier; TestArityUsageErrorsNameTheShape states the rule).
// An empty want is a rejection cobra itself produces (unknown flag, missing
// flag value, the root usage): "cobra rejected it as usage" is the whole
// contract there, and pinning its prose would couple the suite to a
// dependency's wording.
func TestRunUsageErrors(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{}, ""}, // no command (root usage)
		{[]string{"bogus"}, `unknown command "bogus"`},
		{[]string{"dockerfile", "extra"}, "unexpected arguments"},
		{[]string{"develop", "--template"}, ""}, // flag missing its value
		{[]string{"develop", "--bogus"}, ""},    // unknown flag
		// An explicitly blank --agent must refuse naming the rule and the
		// "none" remedy — downstream it reads as "flag absent" and would be
		// silently ignored, on both commands that carry the flag.
		{[]string{"develop", "--agent="}, "--agent: blank value"},
		{[]string{"develop", "-a", "   "}, "--agent: blank value"},
		{[]string{"worktree", "feat", "--agent="}, "--agent: blank value"},
		{[]string{"config", "--bogus"}, ""}, // unknown flag
		{[]string{"status", "--bogus"}, ""}, // unknown flag
		// --data already carries everything --full does; the pair is a
		// misunderstanding of --data, not a combination to guess at.
		{[]string{"status", "--full", "--data"}, "--full and --data are exclusive"},
		{[]string{"reset", "--bogus"}, ""}, // unknown flag
		{[]string{"worktree"}, "usage: byre worktree <name>"},
		{[]string{"worktree", "--bogus"}, ""}, // unknown flag
		{[]string{"worktree", "a", "b"}, `unexpected argument "b"`},
		{[]string{"deliver", "--bogus"}, ""}, // unknown flag
		{[]string{"deliver", "-", "x.txt"}, "cannot be mixed with path arguments"},
		{[]string{"deliver", "--install-app", "x.txt"}, `the only allowed argument is an ssh:// target ("x.txt")`},
		{[]string{"deliver", "--install-app", "ssh://h", "x.txt"}, "takes at most one argument"},
		{[]string{"deliver", "--install-app", "--remote-byre", "/x"}, "--remote-byre applies only to an ssh:// target"},
		{[]string{"deliver", "--install-app", "ssh://"}, "names no host"},
		{[]string{"deliver", "--install-app", "ssh://u:p@h"}, "embeds a password"},
		// --no-clip=false is still a supplied flag the exclusivity promise rejects.
		{[]string{"deliver", "--install-app", "--no-clip=false"}, "takes an optional ssh:// target, --box, --name, and --remote-byre"},
		{[]string{"deliver", "--install-app", "--proto", "1"}, "takes an optional ssh:// target, --box, --name, and --remote-byre"},
		{[]string{"deliver", "--install-app", "--tar", "-"}, "takes an optional ssh:// target, --box, --name, and --remote-byre"},
		{[]string{"deliver", "--install-app", "--boxes"}, "takes an optional ssh:// target, --box, --name, and --remote-byre"},
		{[]string{"deliver", "--install-app", "--skip-uid-check"}, "takes an optional ssh:// target, --box, --name, and --remote-byre"},
		{[]string{"deliver", "--boxes", "x.txt"}, "--boxes: takes no paths"},
		{[]string{"deliver", "--boxes", "--tar", "-"}, "takes only --proto and --skip-uid-check"},
		{[]string{"deliver", "--boxes", "--box", "x"}, "takes only --proto and --skip-uid-check"},
		{[]string{"deliver", "--tar"}, "takes exactly '-'"},
		{[]string{"deliver", "--tar", "x.txt"}, "takes exactly '-'"},
		{[]string{"deliver", "--tar", "--name", "n", "-"}, "--name does not apply"},
		{[]string{"grab"}, "usage: byre grab <box-path>"},
		{[]string{"grab", "a", "b", "c"}, `unexpected argument "c"`},
		{[]string{"grab", "--bogus", "a"}, ""}, // unknown flag
		{[]string{"skill"}, "usage: byre skill list|inspect"},
		{[]string{"skill", "bogus"}, "usage: byre skill list|inspect"},
		// bare rehome is valid: it lists candidates.
		{[]string{"rehome", "old", "extra"}, "usage: byre rehome [<old-id>]"},
		{[]string{"version", "extra"}, "unexpected arguments"},
		{[]string{"--version", "extra"}, "unexpected arguments"},
	}
	for _, tc := range cases {
		calls := map[string]string{}
		s, _ := testStreams()
		err := run(recorderApp(calls), tc.argv, "/proj", s)
		var uerr usageError
		if !errors.As(err, &uerr) {
			t.Errorf("%v: expected usageError, got %v", tc.argv, err)
		} else if tc.want != "" && !strings.Contains(string(uerr), tc.want) {
			t.Errorf("%v: wrong rule fired: got %q, want a message containing %q", tc.argv, string(uerr), tc.want)
		}
		if len(calls) != 0 {
			t.Errorf("%v: usage error must not dispatch, got %v", tc.argv, calls)
		}
	}
}

// TestUnexpectedArgumentsRenderArgvAsData pins that an operand byre echoes back
// stays inside its one line. The print boundary escapes per line
// (commands.EscapeMultiline, shared with fatal(), because cobra usage text is
// legitimately multiline), so a newline in argv would frame a line of the
// typist's own choosing under byre's name -- and this message is the one exit
// path that prints argv at all.
func TestUnexpectedArgumentsRenderArgvAsData(t *testing.T) {
	argv := []string{"dockerfile", "extra\nbyre: everything is fine\r\x1b[2K"}
	s, _ := testStreams()
	err := run(recorderApp(map[string]string{}), argv, "/proj", s)

	var uerr usageError
	if !errors.As(err, &uerr) {
		t.Fatalf("expected usageError, got %v", err)
	}
	got := string(uerr)
	if !strings.Contains(got, "unexpected arguments") {
		t.Errorf("wrong rule fired: %q", got)
	}
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("argv framed a line of its own: %q", got)
	}
	if strings.IndexByte(got, 0x1b) >= 0 {
		t.Errorf("argv reached the terminal as control bytes: %q", got)
	}
	// Rendered, not censored: the typist still sees what byre rejected.
	if !strings.Contains(got, "extra") {
		t.Errorf("the rejected operand vanished: %q", got)
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

// commandNames enumerates the registered subcommands off a throwaway tree —
// the successor to iterating the old command table.
func commandNames() []string {
	s, _ := testStreams()
	root := newRootCmd(recorderApp(map[string]string{}), "/proj", s)
	var names []string
	for _, c := range root.Commands() {
		if c.Hidden { // hidden plumbing (commands-page) stays out of help
			continue
		}
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
	a.shell = func(commands.Streams, string, bool) error { return boom }
	s, _ := testStreams()
	if err := run(a, []string{"shell"}, "/proj", s); !errors.Is(err, boom) {
		t.Fatalf("expected the command error back, got %v", err)
	}
}
