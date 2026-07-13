// Command byre runs an AI coding agent in a throwaway, project-scoped container.
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pjlsergeant/byre/internal/commands"
	"github.com/pjlsergeant/byre/internal/deliver"
	byreversion "github.com/pjlsergeant/byre/internal/version"
)

// version is stamped by release builds for backwards-compatible ldflags
// (-X main.version=vX.Y.Z). Prefer -X github.com/pjlsergeant/byre/internal/version.Version
// going forward; both are honored.
var version string

// app is the set of command implementations the CLI dispatches to. A struct
// (not direct calls) so tests can pin the flag->function wiring with
// recorders instead of executing real commands.
type app struct {
	dockerfile    func(s commands.Streams, dir string) error
	dockerrun     func(s commands.Streams, dir string) error
	ejectfirewall func(s commands.Streams, dir string) error
	develop       func(s commands.Streams, dir, tmpl, agent string, sharedAuth *bool, selfEdit bool) error
	config        func(s commands.Streams, dir string, global bool) error
	status        func(s commands.Streams, dir string, selfEdit bool) error
	reset         func(s commands.Streams, dir string, force bool) error
	forget        func(s commands.Streams, dir string, force bool) error
	shell         func(s commands.Streams, dir string) error
	deliver       func(s commands.Streams, dir string, opts deliver.Options, paths []string) error
	installApp    func(s commands.Streams, box string) error
	worktree      func(s commands.Streams, dir, name, path string, selfEdit bool) error
	skillUpdate   func(s commands.Streams) error
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
	installApp:       commands.InstallApp,
	worktree:         commands.Worktree,
	skillUpdate:      commands.SkillUpdate,
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

// newRootCmd builds the byre command tree wired to a's implementations.
// Built fresh per invocation: flag state lives in the closures, and tests
// exercise the real tree with recorder apps.
func newRootCmd(a app, dir string, s commands.Streams) *cobra.Command {
	root := &cobra.Command{
		Use:   "byre",
		Short: "Run an AI coding agent in a throwaway, project-scoped container.",
		Long: `byre — run an AI coding agent in a throwaway, project-scoped container.

Run byre in the project directory you want to develop.`,
		// byre owns error printing and the exit-code contract (usage = 2,
		// byre failure = 1, agent/refusal codes passed through): cobra must
		// neither print errors nor dump usage after them.
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
		worktreeCmd(a, dir, s),
		skillCmd(a, s),
		templateCmd(s),
		resetCmd(a, dir, s),
		rebuildCmd(a, dir, s),
		rehomeCmd(a, dir, s),
		forgetCmd(a, dir, s),
		versionCmd(a, s),
		completionCmd(s),
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
		Short: "Set up and run the project container in the foreground.",
		Long: `Set up (generate + build the image) and run the project container in the
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
	c := &cobra.Command{
		Use:   "config",
		Short: "Edit this project's config interactively.",
		Long: `Open the interactive editor for this project's host-side config
(~/.byre/projects/<id>/byre.config). Raw fields are shown, not edited.`,
		Args: noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.config(s, dir, global)
		},
	}
	c.Flags().BoolVar(&global, "global", false, "edit your global defaults (~/.byre/default.config) instead")
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
		Short: "Print the firewall sidecar as a standalone script.",
		Long: `Print, as a shell script, the firewall sidecar byre runs for this project —
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
		Short: "Show resolved config, mounts, skills, container state.",
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
	return &cobra.Command{
		Use:   "shell",
		Short: "Open a shell (as the dev user) in the running session.",
		Long: `Open an interactive shell in this project's running container, as the dev
user — for agent logins, running tests, poking around. Needs a session
started by 'byre develop'.`,
		Args: noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.shell(s, dir)
		},
	}
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

After a delivery the landed paths also go to your clipboard (pbcopy /
wl-copy / xclip, or OSC 52 through SSH), ready to paste; --no-clip skips
that, and when no clipboard path exists byre says so — the printed path is
always the contract.

'byre deliver --install-app' installs the DELIVER APP instead: a generated
"Byre Deliver" drag target (macOS: a Dock/Finder droplet plus a right-click
"Deliver to Byre" Quick Action; Linux: a .desktop launcher). Drop files on
it, or open it plain to deliver the clipboard; outcomes arrive as
notifications. Re-run it after moving byre; --box bakes a fixed target in.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if installApp {
				// Changed(), not the parsed values: --no-clip=false is still
				// a supplied flag the exclusivity promise rejects.
				for _, f := range []string{"name", "skip-uid-check", "no-clip"} {
					if cmd.Flags().Changed(f) {
						return usageError("byre deliver --install-app: takes only an optional --box")
					}
				}
				if len(args) > 0 {
					return usageError("byre deliver --install-app: takes only an optional --box")
				}
				return a.installApp(s, opts.Box)
			}
			if len(args) > 1 {
				for _, p := range args {
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

func skillCmd(a app, s commands.Streams) *cobra.Command {
	skill := &cobra.Command{
		Use:   "skill",
		Short: "Manage skill packages (list, inspect, fork, init, validate, update).",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return usageError("usage: byre skill list|inspect|fork|init|validate|update|archive-legacy")
		},
	}
	skill.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List skill packages in the catalog.",
			Args:  noArgsU,
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.SkillList(s) },
		},
		&cobra.Command{
			Use:   "inspect <id>",
			Short: "Show skill package metadata and grants.",
			Args:  cobra.ExactArgs(1),
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.SkillInspect(s, args[0]) },
		},
		&cobra.Command{
			Use:   "fork <id> <new-id>",
			Short: "Fork an immutable skill into a local editable package.",
			Args:  cobra.ExactArgs(2),
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.SkillFork(s, args[0], args[1]) },
		},
		&cobra.Command{
			Use:   "init <name>",
			Short: "Scaffold a new local skill package.",
			Args:  cobra.ExactArgs(1),
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.SkillInit(s, args[0]) },
		},
		&cobra.Command{
			Use:   "validate [name]",
			Short: "Two-stage parse and resolve-check a skill (or all).",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				name := ""
				if len(args) == 1 {
					name = args[0]
				}
				return commands.SkillValidate(s, name)
			},
		},
		&cobra.Command{
			Use:   "update",
			Short: "Explain that bundled packages update with byre itself (D11 stub).",
			Args:  noArgsU,
			RunE:  func(cmd *cobra.Command, args []string) error { return a.skillUpdate(s) },
		},
		&cobra.Command{
			Use:   "archive-legacy",
			Short: "Move LEGACY materialized dirs to skills.legacy/ / templates.legacy/.",
			Args:  noArgsU,
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.SkillArchiveLegacy(s) },
		},
	)
	return skill
}

func templateCmd(s commands.Streams) *cobra.Command {
	tmpl := &cobra.Command{
		Use:   "template",
		Short: "Manage template packages (list, inspect, fork, init, validate).",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return usageError("usage: byre template list|inspect|fork|init|validate")
		},
	}
	tmpl.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List template packages in the catalog.",
			Args:  noArgsU,
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.TemplateList(s) },
		},
		&cobra.Command{
			Use:   "inspect <id>",
			Short: "Show template package metadata.",
			Args:  cobra.ExactArgs(1),
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.TemplateInspect(s, args[0]) },
		},
		&cobra.Command{
			Use:   "fork <id> <new-id>",
			Short: "Fork an immutable template into a local editable package.",
			Args:  cobra.ExactArgs(2),
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.TemplateFork(s, args[0], args[1]) },
		},
		&cobra.Command{
			Use:   "init <name>",
			Short: "Scaffold a new local template package.",
			Args:  cobra.ExactArgs(1),
			RunE:  func(cmd *cobra.Command, args []string) error { return commands.TemplateInit(s, args[0]) },
		},
		&cobra.Command{
			Use:   "validate [name]",
			Short: "Two-stage parse a template (or all).",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				name := ""
				if len(args) == 1 {
					name = args[0]
				}
				return commands.TemplateValidate(s, name)
			},
		},
	)
	return tmpl
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
the image, and ~/.byre/projects/<id>/ (config, adoption record, build
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

// versionString resolves what `byre version` prints, in priority order: the
// release-stamped tag (main.version or internal/version.Version), then the
// version Go recorded in build info, then "(devel)" with a short VCS
// revision when available.
func versionString(stamped string, bi *debug.BuildInfo) string {
	if stamped != "" {
		return stamped
	}
	if byreversion.Version != "" {
		return byreversion.Version
	}
	if bi == nil {
		return "(devel)"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			n := 12
			if len(s.Value) < n {
				n = len(s.Value)
			}
			return "(devel) " + s.Value[:n]
		}
	}
	return "(devel)"
}

func printVersion(s commands.Streams) error {
	// Propagate legacy main.version stamp into the shared package once.
	if version != "" && byreversion.Version == "" {
		byreversion.Version = version
	}
	bi, _ := debug.ReadBuildInfo()
	_, err := fmt.Fprintln(s.Out, "byre "+versionString(version, bi))
	return err
}

func main() {
	dir, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	if err := run(realApp, os.Args[1:], dir, commands.StdStreams()); err != nil {
		var uerr usageError
		if errors.As(err, &uerr) {
			fmt.Fprintln(os.Stderr, string(uerr))
			os.Exit(2)
		}
		fatal(err)
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
	fmt.Fprintf(os.Stderr, "byre: %v\n", err)
	os.Exit(1)
}
