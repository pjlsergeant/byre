// Command byre runs an AI coding agent in a throwaway, project-scoped box.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pjlsergeant/byre/internal/commands"
	"github.com/pjlsergeant/byre/internal/deliver"
	"github.com/pjlsergeant/byre/internal/packages"
	byreversion "github.com/pjlsergeant/byre/internal/version"
)

// app is the set of command implementations the CLI dispatches to. A struct
// (not direct calls) so tests can pin the flag->function wiring with
// recorders instead of executing real commands. Its BOUNDARY: the original
// command set below rides the seam; the newer verb families (skill,
// template, preset, layer, mcp, claude-skill, context) bind commands.*
// directly and are wiring-tested through cobra execution instead -- a new
// command may pick either side, but this comment is the map of which is
// which.
type app struct {
	dockerfile    func(s commands.Streams, dir string) error
	dockerrun     func(s commands.Streams, dir string) error
	ejectfirewall func(s commands.Streams, dir string) error
	develop       func(s commands.Streams, dir, tmpl, agent string, sharedAuth *bool, selfEdit bool) error
	config        func(s commands.Streams, dir string, global bool, layer string) error
	status        func(s commands.Streams, dir string, selfEdit bool) error
	reset         func(s commands.Streams, dir string, force bool) error
	forget        func(s commands.Streams, dir string, force bool) error
	shell         func(s commands.Streams, dir string, skipUIDCheck bool) error
	deliver       func(s commands.Streams, dir string, opts deliver.Options, paths []string) error
	grab          func(s commands.Streams, dir string, opts deliver.Options, boxPath, hostPath string) error
	installApp    func(s commands.Streams, box string) error
	worktree      func(s commands.Streams, dir, name, path string, selfEdit bool) error
	rebuild       func(s commands.Streams, dir string) error
	rehome        func(s commands.Streams, dir, oldID string) error
	// rehomeCandidates is bare `byre rehome`: list stored projects whose
	// recorded path no longer exists (the likely rehome sources).
	rehomeCandidates func(s commands.Streams, dir string) error
	version          func(s commands.Streams) error
}

var realApp = app{
	dockerfile:       commands.Dockerfile,
	dockerrun:        commands.DockerRun,
	ejectfirewall:    commands.EjectFirewall,
	develop:          commands.Develop,
	config:           commands.Config,
	status:           commands.Status,
	reset:            commands.Reset,
	forget:           commands.Forget,
	shell:            commands.Shell,
	deliver:          commands.Deliver,
	grab:             commands.Grab,
	installApp:       commands.InstallApp,
	worktree:         commands.Worktree,
	rebuild:          commands.Rebuild,
	rehome:           commands.Rehome,
	rehomeCandidates: commands.RehomeCandidates,
	version:          printVersion,
}

func init() {
	// Commands list in the order they're registered (develop first), not
	// alphabetically — the top of the help is the happy path.
	cobra.EnableCommandSorting = false
}

// usageError is a command-line parse failure: main prints it to stderr and
// exits 2, distinct from a byre failure (1) and an agent/refusal code.
type usageError string

func (e usageError) Error() string { return string(e) }

// noArgsU rejects unexpected operands after a subcommand, as a usageError so
// main exits 2 without dispatching (cobra's own validators return plain
// errors, which would be misreported as byre failures).
func noArgsU(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageError(fmt.Sprintf("%s: unexpected arguments %v", cmd.CommandPath(), args))
	}
	return nil
}

// exactArgsU / maxArgsU are cobra's arity validators returning byre usage
// errors, so main exits 2 with a usage line naming the shape. cobra's own
// validators return plain errors, which main reports as byre failures at exit 1
// -- the very hazard noArgsU exists for, walked into by every arity-checking
// command until now. usage is the full command shape, without a leading "byre".
func exactArgsU(n int, usage string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usageError("usage: byre " + usage)
		}
		return nil
	}
}

func maxArgsU(n int, usage string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return usageError("usage: byre " + usage)
		}
		return nil
	}
}

// newRootCmd builds the byre command tree wired to a's implementations.
// Built fresh per invocation: flag state lives in the closures, and tests
// exercise the real tree with recorder apps.
func newRootCmd(a app, dir string, s commands.Streams) *cobra.Command {
	root := &cobra.Command{
		Use:   "byre",
		Short: "Run an AI coding agent in a throwaway, project-scoped box.",
		Long: `byre — run an AI coding agent in a throwaway, project-scoped box.

Run byre in the project directory you want to develop.`,
		// byre owns error printing and the exit-code contract (usage = 2,
		// byre failure = 1, crash = 70, agent/refusal codes passed through):
		// cobra must neither print errors nor dump usage after them.
		SilenceUsage:  true,
		SilenceErrors: true,
		// ArbitraryArgs so unknown commands reach RunE (instead of cobra's
		// untyped legacyArgs error) and come back as usageError.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageError(fmt.Sprintf("byre: unknown command %q\n\n%s", args[0], strings.TrimRight(cmd.UsageString(), "\n")))
			}
			return usageError(strings.TrimRight(cmd.UsageString(), "\n"))
		},
	}
	root.SetOut(s.Out)
	root.SetErr(s.Err)
	// cobra's default usage template (v1.10.2), with one change: the
	// runnable use-line is skipped for the ROOT command. Root carries a RunE
	// only so bare/unknown invocations become usageErrors (exit 2) — showing
	// "byre [flags]" would advertise a bare invocation that does nothing.
	// Children inherit this template and keep their use-lines (HasParent).
	root.SetUsageTemplate(`Usage:{{if and .Runnable .HasParent}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Additional Commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`)
	// Flag parse failures (unknown flag, missing value) become usageErrors,
	// prefixed with the command path so the message names the culprit.
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageError(cmd.CommandPath() + ": " + err.Error())
	})

	root.AddCommand(
		developCmd(a, dir, s),
		configCmd(a, dir, s),
		dockerfileCmd(a, dir, s),
		dockerrunCmd(a, dir, s),
		ejectfirewallCmd(a, dir, s),
		statusCmd(a, dir, s),
		shellCmd(a, dir, s),
		deliverCmd(a, dir, s),
		grabCmd(a, dir, s),
		worktreeCmd(a, dir, s),
		skillCmd(s),
		templateCmd(s),
		layerCmd(s),
		mcpCmd(dir, s),
		claudeSkillCmd(dir, s),
		contextCmd(dir, s),
		presetCmd(dir, s),
		resetCmd(a, dir, s),
		rebuildCmd(a, dir, s),
		rehomeCmd(a, dir, s),
		forgetCmd(a, dir, s),
		versionCmd(a, s),
		completionCmd(s),
		commandsPageCmd(s),
		configRefDocCmd(s),
	)
	// byre ships its own completion command (above) so its help carries the
	// per-shell setup lines and bare/unknown invocations stay usage errors;
	// the hidden __complete machinery the scripts call is unaffected by
	// disabling the stock visible command.
	root.CompletionOptions.DisableDefaultCmd = true
	return root
}

func developCmd(a app, dir string, s commands.Streams) *cobra.Command {
	var tmpl, agent string
	var selfEdit bool
	var sharedAuth bool
	c := &cobra.Command{
		Use:   "develop",
		Short: "Set up and run the project box in the foreground.",
		Long: `Set up (generate + build the image) and run the project box in the
foreground. First run onboards the project (creates its host-side config).`,
		Args: noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			var sharedAuthFlag *bool
			if cmd.Flags().Changed("shared-auth") {
				sharedAuthFlag = &sharedAuth
			}
			return a.develop(s, dir, tmpl, agent, sharedAuthFlag, selfEdit)
		},
	}
	c.Flags().StringVar(&tmpl, "template", "", `template for a NEW project's config (first run only; "none" to skip)`)
	c.Flags().StringVar(&agent, "agent", "", `agent for a NEW project's config (first run only; "none" to skip)`)
	c.Flags().BoolVar(&sharedAuth, "shared-auth", false, `opt a NEW project's box into the chosen agent's shared credentials without the question (=false declines it; first run only)`)
	c.Flags().BoolVar(&selfEdit, "self-edit", false, "mount this project's host-side store read-write so the agent can edit its own byre.config — a deliberate grant, applied on the next develop")
	return c
}

func configCmd(a app, dir string, s commands.Streams) *cobra.Command {
	var global bool
	var layer string
	c := &cobra.Command{
		Use:   "config",
		Short: "Edit this project's config interactively.",
		Long: `Open the interactive editor for this project's host-side config
(~/.byre/projects/<id>/byre.config). Raw fields are shown, not edited.`,
		Args: noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.config(s, dir, global, layer)
		},
	}
	c.Flags().BoolVar(&global, "global", false, "edit your global defaults (~/.byre/default.config) instead")
	c.Flags().StringVar(&layer, "layer", "", "edit a named layer (~/.byre/layers/<name>/layer.config) instead")
	return c
}

func dockerfileCmd(a app, dir string, s commands.Streams) *cobra.Command {
	return &cobra.Command{
		Use:   "dockerfile",
		Short: "Print the generated Dockerfile for this directory.",
		Long:  `Print the Dockerfile byre would build for this directory. Side-effect-free.`,
		Args:  noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.dockerfile(s, dir)
		},
	}
}

func dockerrunCmd(a app, dir string, s commands.Streams) *cobra.Command {
	return &cobra.Command{
		Use:   "dockerrun",
		Short: "Print the docker/podman run command byre would use.",
		Long: `Print the exact docker/podman run invocation byre would use for this project —
the run-time counterpart to 'byre dockerfile'. Side-effect-free.`,
		Args: noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.dockerrun(s, dir)
		},
	}
}

func ejectfirewallCmd(a app, dir string, s commands.Streams) *cobra.Command {
	return &cobra.Command{
		Use:   "ejectfirewall",
		Short: "Print the firewall netns helper as a standalone script.",
		Long: `Print, as a shell script, the firewall netns helper byre runs for this project —
the one piece of the box 'byre dockerfile' + 'byre dockerrun' can't carry.
Run the printed script right after starting the box; it applies the resolved
egress allowlist from outside and opens the launch gate. Side-effect-free;
errors if no firewall (netns hook) is enabled.`,
		Args: noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.ejectfirewall(s, dir)
		},
	}
}

func statusCmd(a app, dir string, s commands.Streams) *cobra.Command {
	var selfEdit bool
	c := &cobra.Command{
		Use:   "status",
		Short: "Show resolved config, mounts, skills, session state.",
		Long: `Show the resolved view of this project: agent, engine, mounts, ports, volumes,
skill grants, and whether a session is running.`,
		Args: noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.status(s, dir, selfEdit)
		},
	}
	c.Flags().BoolVar(&selfEdit, "self-edit", false, "also show the grant 'develop --self-edit' would add")
	return c
}

func shellCmd(a app, dir string, s commands.Streams) *cobra.Command {
	var skipUIDCheck bool
	c := &cobra.Command{
		Use:   "shell",
		Short: "Open a shell (as the dev user) in the running session.",
		Long: `Open an interactive shell in this project's running box, as the dev
user — for agent logins, running tests, poking around. Needs a session
started by 'byre develop'.

On a shared rootful daemon a box started by another Unix user is hidden;
--skip-uid-check enters it anyway (it runs as that box's dev identity).`,
		Args: noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.shell(s, dir, skipUIDCheck)
		},
	}
	c.Flags().BoolVar(&skipUIDCheck, "skip-uid-check", false, "enter a session owned by another Unix user (shared rootful daemon)")
	return c
}

func deliverCmd(a app, dir string, s commands.Streams) *cobra.Command {
	var opts deliver.Options
	var installApp bool
	c := &cobra.Command{
		Use:   "deliver [<path>... | -]",
		Short: "Deliver files from the host into a running box's /inbox.",
		Long: `Get files into a running box: each path streams into the box's /inbox
(names preserved, collisions uniquified, never overwritten) and the landed
in-box path prints to stdout, one per line — paste it into the agent prompt.
Directories deliver recursively, preserving structure, as one path.

With no paths, byre delivers your CLIPBOARD: on a terminal it waits for a
paste gesture (Ctrl-V or Cmd-V — the beat where you check what's on the
clipboard), then reads the system clipboard directly, so copied files,
screenshots, and text all work. Import priority: file references → image →
text; captures land as clipboard-<timestamp> named for their actual format.
'-' (or piped stdin) streams stdin into a single file.

The box is found machine-wide: --box picks explicitly (unique id or project
prefix); otherwise a box whose workdir contains the current directory wins;
otherwise the only running box owned by you; otherwise the candidates are
listed. Boxes started by other users are hidden unless --skip-uid-check.

An ssh:// FIRST argument ('byre deliver ssh://host shot.png') delivers
through another machine running byre: its boxes are listed remotely, picked
locally, and the sources stream over one ssh exec into that box's /inbox —
every local input mode works unchanged, and the landed paths come back to
YOUR stdout and clipboard. --remote-byre names the remote binary when sshd's
non-interactive PATH hides it. Authentication is your own ssh.

After a delivery the landed paths also go to your clipboard (pbcopy /
wl-copy / xclip, or OSC 52 through SSH), ready to paste; --no-clip skips
that, and when no clipboard path exists byre says so — the printed path is
always the contract.

'byre deliver --install-app' installs the DELIVER APP instead: a generated
"Byre Deliver" drag target (macOS: a Dock/Finder drop target plus a right-click
"Deliver to Byre" Quick Action; Linux: a .desktop launcher). Drop files on
it, or open it plain to deliver the clipboard; outcomes arrive as
notifications. Re-run it after moving byre; --box bakes a fixed target in.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if installApp {
				// Changed(), not the parsed values: --no-clip=false is still
				// a supplied flag the exclusivity promise rejects.
				for _, f := range []string{"name", "skip-uid-check", "no-clip", "boxes", "tar", "proto", "remote-byre"} {
					if cmd.Flags().Changed(f) {
						return usageError("byre deliver --install-app: takes only an optional --box")
					}
				}
				if len(args) > 0 {
					return usageError("byre deliver --install-app: takes only an optional --box")
				}
				return a.installApp(s, opts.Box)
			}
			// The remote-facing modes (ADR 0037) keep their surfaces frozen
			// and small: --boxes answers exactly one question, --tar takes
			// exactly one stream.
			if opts.Boxes {
				for _, f := range []string{"tar", "name", "box", "no-clip", "remote-byre"} {
					if cmd.Flags().Changed(f) {
						return usageError("byre deliver --boxes: takes only --proto and --skip-uid-check")
					}
				}
				if len(args) > 0 {
					return usageError("byre deliver --boxes: takes no paths")
				}
			}
			if opts.Tar {
				if len(args) != 1 || args[0] != "-" {
					return usageError("byre deliver --tar: takes exactly '-' (the archive arrives on stdin)")
				}
				if cmd.Flags().Changed("name") {
					return usageError("byre deliver --tar: --name does not apply (names ride the archive)")
				}
			}
			// The '-'-mixing rule applies to SOURCES: an ssh:// target in
			// first position is the destination, not a source, so
			// `byre deliver ssh://host -` stays legal.
			srcs := args
			if len(srcs) > 0 && strings.HasPrefix(srcs[0], "ssh://") {
				srcs = srcs[1:]
			}
			if len(srcs) > 1 {
				for _, p := range srcs {
					if p == "-" {
						return usageError("byre deliver: '-' (stdin) cannot be mixed with path arguments")
					}
				}
			}
			return a.deliver(s, dir, opts, args)
		},
	}
	c.Flags().StringVar(&opts.Box, "box", "", "deliver to this box (unique id or project prefix)")
	c.Flags().StringVar(&opts.Name, "name", "", "landing filename for stdin ('-') content")
	c.Flags().BoolVar(&opts.SkipUIDCheck, "skip-uid-check", false, "include (and permit) boxes owned by other users")
	c.Flags().BoolVar(&opts.NoClip, "no-clip", false, "don't copy the landed paths to the clipboard")
	c.Flags().BoolVar(&installApp, "install-app", false, "install the deliver app instead of delivering")
	c.Flags().BoolVar(&opts.Boxes, "boxes", false, "list deliverable boxes headlessly, one line each (remote delivery's enumeration)")
	c.Flags().BoolVar(&opts.Tar, "tar", false, "unpack a tar archive from stdin into /inbox (remote delivery's transport)")
	c.Flags().IntVar(&opts.Proto, "proto", 0, "remote-delivery protocol handshake (fails on version skew)")
	c.Flags().StringVar(&opts.RemoteByre, "remote-byre", "", "byre binary path on the ssh:// remote (when it isn't on the ssh PATH)")
	return c
}

func grabCmd(a app, dir string, s commands.Streams) *cobra.Command {
	var opts deliver.Options
	c := &cobra.Command{
		Use:   "grab <box-path> [<host-path> | -]",
		Short: "Grab a file or directory out of a running box onto the host.",
		Long: `Get a file (or directory, recursively) out of a running box: deliver's
mirror. <box-path> is a path inside the box — relative paths count from
/workspace — and it lands at <host-path> (default: the current directory),
with the landed host path printed to stdout.

Grab never overwrites host files: an existing <host-path> directory receives
the file under its box name; any name collision uniquifies (report.pdf →
report-2.pdf), and the printed path is always where the bytes actually
landed. '-' streams a single file's content to stdout instead.

The box is found machine-wide exactly as 'byre deliver' finds one: --box
picks explicitly, otherwise the box whose workdir contains the current
directory, otherwise the only running box owned by you, otherwise a picker.
Boxes started by other users are hidden unless --skip-uid-check.`,
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) < 1:
				return usageError("usage: byre grab <box-path> [<host-path> | -]")
			case len(args) > 2:
				return usageError(fmt.Sprintf("byre grab: unexpected argument %q", args[2]))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			hostPath := "."
			if len(args) == 2 {
				hostPath = args[1]
			}
			return a.grab(s, dir, opts, args[0], hostPath)
		},
	}
	c.Flags().StringVar(&opts.Box, "box", "", "grab from this box (unique id or project prefix)")
	c.Flags().BoolVar(&opts.SkipUIDCheck, "skip-uid-check", false, "include (and permit) boxes owned by other users")
	return c
}

func worktreeCmd(a app, dir string, s commands.Streams) *cobra.Command {
	var path string
	var selfEdit bool
	c := &cobra.Command{
		Use:   "worktree <name>",
		Short: "Create a git worktree and start a parallel session in it.",
		Long: `Create a linked git worktree for branch <name> and run 'byre develop' in
it — a parallel agent that inherits this project's config, volumes, and
image. Location: --path, or the configured worktree_base ("sibling" = a
sibling dir <repo>-<name>, or a directory to put worktrees under); with
neither set, byre refuses rather than guessing.`,
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) < 1:
				return usageError("usage: byre worktree <name> [--path <dir>] [--self-edit]")
			case len(args) > 1:
				return usageError(fmt.Sprintf("byre worktree: unexpected argument %q", args[1]))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.worktree(s, dir, args[0], path, selfEdit)
		},
	}
	c.Flags().StringVar(&path, "path", "", "create the worktree at an explicit path")
	c.Flags().BoolVar(&selfEdit, "self-edit", false, "forward 'develop --self-edit' for the new session")
	return c
}

func mcpCmd(dir string, s commands.Streams) *cobra.Command {
	mcp := &cobra.Command{
		Use:   "mcp",
		Short: "Manage this project's MCP server declarations ([[mcp]] config blocks).",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return usageError("usage: byre mcp add|remove|list")
		},
	}
	var addGlobal, rmGlobal bool
	var env, egress, headers []string
	var bearer string
	add := &cobra.Command{
		Use:   "add <name> (<url> | -- <command>...)",
		Short: "Declare an MCP server in the project config (or --global defaults).",
		Long: `Write an [[mcp]] declaration into this project's host-side config
(add-or-update by name; a matching "!name" closure is re-opened). One arg
that starts http(s):// is a remote server; anything else is a local stdio
command — put it after -- so its own flags aren't parsed as byre's.
Applies on the next develop.

Everything after -- is the argv itself, starting with the executable:

  byre mcp add qa -- npx some-server --stdio    →  command = ["npx", "some-server", "--stdio"]

(the config's key is NAMED command, but the word "command" is never part
of the argv you type).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return usageError("usage: byre mcp add <name> (<url> | -- <command>...)")
			}
			return commands.MCPAdd(s, dir, addGlobal, args[0], args[1:], env, egress, headers, bearer)
		},
	}
	add.Flags().BoolVar(&addGlobal, "global", false, "write your global defaults (~/.byre/default.config) instead")
	add.Flags().StringArrayVar(&env, "env", nil, "env var NAME the server consumes (repeatable; values ride env_from_host/[env], never this declaration)")
	add.Flags().StringArrayVar(&egress, "egress", nil, "extra host[:port] the server needs (repeatable; a remote url's own host is implied)")
	add.Flags().StringArrayVar(&headers, "header", nil, `HTTP header for a remote server, "Name: value" (repeatable; ${VAR} in the value expands from the box env at launch)`)
	add.Flags().StringVar(&bearer, "bearer", "", `env var NAME holding a bearer token — sugar for --header "Authorization: Bearer ${NAME}"`)
	remove := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a declared MCP server (closure-smart).",
		Long: `Remove a server from this project's effective set: deletes the layer's own
[[mcp]] block, and/or writes the "!name" closure when a lower layer or an
enabled skill still declares the name. Applies on the next develop.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageError("usage: byre mcp remove <name>")
			}
			return commands.MCPRemove(s, dir, rmGlobal, args[0])
		},
	}
	remove.Flags().BoolVar(&rmGlobal, "global", false, "edit your global defaults (~/.byre/default.config) instead")
	mcp.AddCommand(
		add,
		remove,
		&cobra.Command{
			Use:   "list",
			Short: "Show the effective MCP set (config + skills, attributed) and its delivery.",
			Args:  noArgsU,
			RunE: func(cmd *cobra.Command, args []string) error {
				return commands.MCPList(s, dir)
			},
		},
	)
	return mcp
}

func claudeSkillCmd(dir string, s commands.Streams) *cobra.Command {
	cs := &cobra.Command{
		Use:   "claude-skill",
		Short: "Manage this project's Claude Skill declarations ([[claude_skills]] config blocks).",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return usageError("usage: byre claude-skill add|remove|list")
		},
	}
	var addGlobal, rmGlobal bool
	var name string
	add := &cobra.Command{
		Use:   "add <dir>",
		Short: "Declare a Claude Skill (a directory with a SKILL.md) in the project config (or --global defaults).",
		Long: `Validate <dir> as a Claude Skill and write a [[claude_skills]] declaration
into this project's host-side config (add-or-update by name; a matching
"!name" closure is re-opened). The name comes from the SKILL.md frontmatter
unless --name overrides it. The directory bakes into the image and the
claude session receives the skill bare (as /name). Applies on the next
develop.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageError("usage: byre claude-skill add <dir>")
			}
			return commands.ClaudeSkillAdd(s, dir, addGlobal, name, args[0])
		},
	}
	add.Flags().BoolVar(&addGlobal, "global", false, "write your global defaults (~/.byre/default.config) instead")
	add.Flags().StringVar(&name, "name", "", "declared name (default: the SKILL.md frontmatter name; the two must match)")
	remove := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a declared Claude Skill (closure-smart).",
		Long: `Remove a Claude Skill from this project's effective set: deletes the
layer's own [[claude_skills]] block, and/or writes the "!name" closure when
a lower layer or an enabled skill still declares the name. Applies on the
next develop.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageError("usage: byre claude-skill remove <name>")
			}
			return commands.ClaudeSkillRemove(s, dir, rmGlobal, args[0])
		},
	}
	remove.Flags().BoolVar(&rmGlobal, "global", false, "edit your global defaults (~/.byre/default.config) instead")
	cs.AddCommand(
		add,
		remove,
		&cobra.Command{
			Use:   "list",
			Short: "Show the effective Claude Skill set (config + skills, attributed) and its delivery.",
			Args:  noArgsU,
			RunE: func(cmd *cobra.Command, args []string) error {
				return commands.ClaudeSkillList(s, dir)
			},
		},
	)
	return cs
}

func contextCmd(dir string, s commands.Streams) *cobra.Command {
	ctx := &cobra.Command{
		Use:   "context",
		Short: "Manage standing agent instructions ([[context]] config blocks).",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return usageError("usage: byre context add|remove|list")
		},
	}
	var addGlobal, rmGlobal bool
	var text, file string
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or update standing instructions in the project config (or --global defaults).",
		Long: `Write a [[context]] declaration into this project's host-side config
(add-or-update by name; a matching "!name" closure is re-opened). The prose
joins the baked agent context after any enabled skills' snippets and is
injected into the agent's session at launch, whatever agent the box runs. With neither --text nor --file, your $EDITOR opens on the
current text (the git-commit shape). Applies on the next develop.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageError("usage: byre context add <name> [--text <prose>|--file <host-path>]")
			}
			return commands.ContextAdd(s, dir, addGlobal, args[0], text, file)
		},
	}
	add.Flags().BoolVar(&addGlobal, "global", false, "write your global defaults (~/.byre/default.config) instead")
	add.Flags().StringVar(&text, "text", "", "inline prose (skips the $EDITOR flow)")
	add.Flags().StringVar(&file, "file", "", "host file to read at bake (~/… or absolute; machine-local, won't ride a preset)")
	remove := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove standing instructions (closure-smart).",
		Long: `Remove a [[context]] declaration from this project's effective set:
deletes the layer's own block, and/or writes the "!name" closure when a
lower layer still declares the name. Applies on the next develop.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageError("usage: byre context remove <name>")
			}
			return commands.ContextRemove(s, dir, rmGlobal, args[0])
		},
	}
	remove.Flags().BoolVar(&rmGlobal, "global", false, "edit your global defaults (~/.byre/default.config) instead")
	ctx.AddCommand(
		add,
		remove,
		&cobra.Command{
			Use:   "list",
			Short: "Show the resolved standing instructions (name + source).",
			Args:  noArgsU,
			RunE: func(cmd *cobra.Command, args []string) error {
				return commands.ContextList(s, dir)
			},
		},
	)
	return ctx
}

// packageCmd builds one package-noun subtree (skill or template): the verbs
// are identical by construction -- one registrar, so a flag or wording fix
// lands on both nouns -- while genuinely kind-specific text (inspect's
// grants, validate's resolve-check) and kind-only verbs arrive as
// parameters.
func packageCmd(s commands.Streams, noun string, kind packages.Kind, usage, inspectShort, validateShort string, extra ...*cobra.Command) *cobra.Command {
	root := &cobra.Command{
		Use:   noun,
		Short: "Manage " + noun + " packages (list, inspect, fork, init, validate).",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return usageError("usage: byre " + noun + " " + usage)
		},
	}
	root.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List " + noun + " packages in the catalog.",
			Args:  noArgsU,
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.PackageList(s, kind) },
		},
		&cobra.Command{
			Use:   "inspect <id|uri>",
			Short: inspectShort,
			Args:  exactArgsU(1, noun+" inspect <id|uri>"),
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.PackageInspect(s, kind, args[0]) },
		},
		installCmd(s, noun, func(st commands.Streams, uri, digest string, yes bool) error {
			return commands.PackageInstall(st, kind, uri, digest, yes)
		}),
		uninstallCmd(s, noun, func(st commands.Streams, id string, yes bool) error {
			return commands.PackageUninstall(st, kind, id, yes)
		}),
		&cobra.Command{
			Use:   "pack <name>",
			Short: "Emit the distribution manifest for a local " + noun + ".",
			Args:  exactArgsU(1, noun+" pack <name>"),
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.PackagePack(s, kind, args[0]) },
		},
		&cobra.Command{
			Use:   "fork <id> <new-id>",
			Short: "Fork an immutable " + noun + " into a local editable package.",
			Args:  exactArgsU(2, noun+" fork <id> <new-id>"),
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.PackageFork(s, kind, args[0], args[1]) },
		},
		&cobra.Command{
			Use:   "init <name>",
			Short: "Scaffold a new local " + noun + " package.",
			Args:  exactArgsU(1, noun+" init <name>"),
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.PackageInit(s, kind, args[0]) },
		},
		&cobra.Command{
			Use:   "validate [name]",
			Short: validateShort,
			Args:  maxArgsU(1, noun+" validate [name]"),
			RunE: func(cmd *cobra.Command, args []string) error {
				name := ""
				if len(args) == 1 {
					name = args[0]
				}
				return commands.PackageValidate(s, kind, name)
			},
		},
	)
	root.AddCommand(extra...)
	return root
}

func skillCmd(s commands.Streams) *cobra.Command {
	return packageCmd(s, "skill", packages.KindSkill,
		"list|inspect|install|uninstall|fork|init|validate|pack|archive-legacy",
		"Show skill package metadata and grants (URIs fetch without installing).",
		"Two-stage parse and resolve-check a skill (or all).",
		&cobra.Command{
			Use:   "archive-legacy",
			Short: "Move LEGACY materialized dirs to skills.legacy/ / templates.legacy/.",
			Args:  noArgsU,
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.SkillArchiveLegacy(s) },
		},
	)
}

func templateCmd(s commands.Streams) *cobra.Command {
	return packageCmd(s, "template", packages.KindTemplate,
		"list|inspect|install|uninstall|fork|init|validate|pack",
		"Show template package metadata (URIs fetch without installing).",
		"Two-stage parse a template (or all).",
	)
}

// presetCmd: a preset is a saved answer to onboarding's
// questions -- a config proposal from anywhere -- reviewed and applied as the
// project's byre.config. Not a package: no identity, no install.
func presetCmd(dir string, s commands.Streams) *cobra.Command {
	preset := &cobra.Command{
		Use:   "preset",
		Short: "Review and apply a config preset (byre.preset, a path, or an https URI).",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return usageError("usage: byre preset apply|inspect [<uri>|<path>]")
		},
	}
	optArg := func(args []string) string {
		if len(args) == 1 {
			return args[0]
		}
		return ""
	}
	preset.AddCommand(
		&cobra.Command{
			Use:   "apply [<uri>|<path>]",
			Short: "Chauffeur missing installs, review the composed box, write byre.config.",
			Args:  maxArgsU(1, "preset apply [<uri>|<path>]"),
			RunE: func(cmd *cobra.Command, args []string) error {
				return commands.PresetApply(s, dir, optArg(args))
			},
		},
		&cobra.Command{
			Use:   "inspect [<uri>|<path>]",
			Short: "The apply review without the write (read-only).",
			Args:  maxArgsU(1, "preset inspect [<uri>|<path>]"),
			RunE: func(cmd *cobra.Command, args []string) error {
				return commands.PresetInspect(s, dir, optArg(args))
			},
		},
	)
	return preset
}

// installCmd / uninstallCmd build the shared install/uninstall verbs for both
// package nouns, with identical flags: --digest pins bytes, --yes is the
// non-TTY consent for state-changing steps.
func installCmd(s commands.Streams, noun string, fn func(commands.Streams, string, string, bool) error) *cobra.Command {
	var digest string
	var yes bool
	c := &cobra.Command{
		Use:   "install <manifest-uri>",
		Short: "Fetch, verify, and snapshot a " + noun + " package (grants nothing until enabled).",
		Args:  exactArgsU(1, noun+" install <manifest-uri>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fn(s, args[0], digest, yes)
		},
	}
	c.Flags().StringVar(&digest, "digest", "", "expected package digest (sha256:...); mismatch fails the install")
	c.Flags().BoolVar(&yes, "yes", false, "confirm replacement/activation without a prompt (required in a pipe)")
	return c
}

func uninstallCmd(s commands.Streams, noun string, fn func(commands.Streams, string, bool) error) *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "uninstall <id>",
		Short: "Remove an installed " + noun + " package (referencing boxes are listed first).",
		Args:  exactArgsU(1, noun+" uninstall <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fn(s, args[0], yes)
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "confirm without a prompt (required in a pipe)")
	return c
}

func layerCmd(s commands.Streams) *cobra.Command {
	layer := &cobra.Command{
		Use:   "layer",
		Short: "Manage named config layers (new, list, validate).",
		Long: `Named layers are user-authored config files at ~/.byre/layers/<name>/
layer.config, chained into a project's cascade via 'extends' in its
byre.config (or in another layer). They carry the full config vocabulary
except 'template', and are resolved live at every develop. Plain files,
not packages: distribution is sending someone the file.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return usageError("usage: byre layer new|list|validate")
		},
	}
	layer.AddCommand(
		&cobra.Command{
			Use:   "new <name>",
			Short: "Scaffold a named layer.",
			Args:  exactArgsU(1, "layer new <name>"),
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.LayerNew(s, args[0]) },
		},
		&cobra.Command{
			Use:   "list",
			Short: "List named layers, flagging broken ones.",
			Args:  noArgsU,
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.LayerList(s) },
		},
		&cobra.Command{
			Use:   "validate [name]",
			Short: "Parse a layer and walk its extends chain (or all).",
			Args:  maxArgsU(1, "layer validate [name]"),
			RunE: func(cmd *cobra.Command, args []string) error {
				name := ""
				if len(args) == 1 {
					name = args[0]
				}
				return commands.LayerValidate(s, name)
			},
		},
	)
	return layer
}

func resetCmd(a app, dir string, s commands.Streams) *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "reset",
		Short: "Wipe this project's named volumes.",
		Long: `Permanently delete ALL of this project's named volumes (agent credentials,
caches — not the image). Prompts first; refuses while a session is running.`,
		Args: noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.reset(s, dir, force)
		},
	}
	c.Flags().BoolVarP(&force, "force", "y", false, "skip the confirmation prompt")
	return c
}

func rebuildCmd(a app, dir string, s commands.Streams) *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild the image with the cache disabled.",
		Long: `Regenerate the build context and rebuild this project's image with
--no-cache, picking up new upstream tool/package versions. Volumes are
untouched; the next 'byre develop' runs the fresh image.`,
		Args: noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.rebuild(s, dir)
		},
	}
}

func rehomeCmd(a app, dir string, s commands.Streams) *cobra.Command {
	return &cobra.Command{
		Use:   "rehome [<old-id>]",
		Short: "Re-point this directory's identity after a move.",
		Long: `After moving/renaming the project directory (which changes its path-derived
id), migrate the previous id's volumes onto the new identity. <old-id> is the
previous project id. Run 'byre rehome' bare to list likely candidates —
stored projects whose recorded path no longer exists, most recently used
first — instead of spelunking in ~/.byre/projects/.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return usageError("usage: byre rehome [<old-id>]")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return a.rehomeCandidates(s, dir)
			}
			return a.rehome(s, dir, args[0])
		},
	}
}

func forgetCmd(a app, dir string, s commands.Streams) *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "forget",
		Short: "Remove all byre host-side state for this directory.",
		Long: `Completely remove byre's host-side state for this directory: named volumes,
the image, and ~/.byre/projects/<id>/ (config, applied marker, build
context). Your project tree is left alone. Prompts first.`,
		Args: noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.forget(s, dir, force)
		},
	}
	c.Flags().BoolVarP(&force, "force", "y", false, "skip the confirmation prompt")
	return c
}

func versionCmd(a app, s commands.Streams) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the byre version.",
		Long: `Print the byre version ('byre --version' works too). Release binaries
report their tag; other builds report what Go recorded in the binary's
build info — a module or pseudo-version, or (devel) when nothing was.`,
		Args: noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.version(s)
		},
	}
}

func printVersion(s commands.Streams) error {
	_, err := fmt.Fprintln(s.Out, "byre "+byreversion.String())
	return err
}

// exitPanic is the status a byre CRASH exits with. It exists because Go's
// runtime exits a panic with 2, and 2 is byre's usage-error code -- so an
// unhandled panic is indistinguishable from "you typed the flags wrong" to
// the scripts DELIVER.md promises trustworthy exit codes to. byre already
// takes trouble in the other direction (fatal propagates an ExitError bare so
// a container's own status is not misreported as a byre bug); this is the
// same care pointed at the inverse case. 1 (byre error), 2 (usage) and 3
// (ExitRefused) are taken, so this is sysexits' EX_SOFTWARE.
//
// Covers panics on the MAIN goroutine only -- a recover cannot reach another
// goroutine's panic, which still ends the process at 2. That is a smaller
// hole than the one this closes, not an argument against closing it.
const exitPanic = 70

// panicReport writes the crash report. It HIDES NOTHING: the stack the
// recover captured is re-printed verbatim, because a panic is a bug and the
// trace is the bug report. All this adds is a line saying whose fault it is.
// Separate from the exit so it can be tested without ending the process.
func panicReport(w io.Writer, r any, stack []byte) {
	fmt.Fprintf(w, "\nbyre: CRASHED — this is a bug in byre, not something you did.\nPlease report it with everything below: https://github.com/pjlsergeant/byre/issues\n\npanic: %v\n\n", r)
	w.Write(stack)
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			panicReport(os.Stderr, r, debug.Stack())
			os.Exit(exitPanic)
		}
	}()
	dir, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	rerr := run(realApp, os.Args[1:], dir, commands.StdStreams())
	// Before the exit mapping (fatal/os.Exit skip defers): drop the bundled
	// packages' temp extractions. A forced kill leaves residue for the next
	// invocation's reap instead.
	packages.CleanupHostDirs()
	if rerr != nil {
		var uerr usageError
		if errors.As(rerr, &uerr) {
			fmt.Fprintln(os.Stderr, string(uerr))
			os.Exit(2)
		}
		fatal(rerr)
	}
}

// run parses argv (everything after the program name) and dispatches via the
// cobra tree. All parse failures come back as usageError; anything else is
// the command's own error, exit-mapped by main.
func run(a app, args []string, dir string, s commands.Streams) error {
	if len(args) > 0 && args[0] == "--version" {
		// Alias, not a second code path: the `version` command does the work,
		// so both spellings share help, operand checking, and dispatch.
		args = append([]string{"version"}, args[1:]...)
	}
	root := newRootCmd(a, dir, s)
	root.SetArgs(args)
	return root.Execute()
}

// fatal reports err and exits. An ExitError carries a process-level exit code
// that isn't a byre failure (the agent/container's own exit status, or a
// deliberate refusal like "session already running") — it's propagated
// silently via os.Exit, with no "byre: ..." banner, so scripts see the real
// code without it being misreported as a byre bug. Anything else is an actual
// byre error: print it and exit 1 (2 is reserved for usage errors, checked
// before this is ever called).
func fatal(err error) {
	var exitErr commands.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.Code)
	}
	// Error text can quote hostile file bytes — a layer someone sent you, a
	// cloned repo's preset, an unknown TOML key with a control character in
	// its name — so this one boundary escapes everything printed here. byre's
	// own messages carry no control characters (newlines survive: the escape
	// is per-line), so for them this is a no-op.
	fmt.Fprintf(os.Stderr, "byre: %s\n", commands.EscapeMultiline(err.Error()))
	os.Exit(1)
}
