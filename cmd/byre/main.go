// Command byre runs an AI coding agent in a throwaway, project-scoped container.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"byre/internal/commands"
)

// app is the set of command implementations run dispatches to. A struct (not
// direct calls) so tests can pin the flag->function wiring with recorders
// instead of executing real commands.
type app struct {
	dockerfile  func(s commands.Streams, dir string) error
	dockerrun   func(s commands.Streams, dir string) error
	develop     func(s commands.Streams, dir, tmpl, agent string, selfEdit bool) error
	config      func(s commands.Streams, dir string, global bool) error
	status      func(s commands.Streams, dir string, selfEdit bool) error
	reset       func(s commands.Streams, dir string, force bool) error
	forget      func(s commands.Streams, dir string, force bool) error
	shell       func(s commands.Streams, dir string) error
	worktree    func(s commands.Streams, dir, name, path string, selfEdit bool) error
	skillUpdate func(s commands.Streams) error
	rebuild     func(s commands.Streams, dir string) error
	rehome      func(s commands.Streams, dir, oldID string) error
}

var realApp = app{
	dockerfile:  commands.Dockerfile,
	dockerrun:   commands.DockerRun,
	develop:     commands.Develop,
	config:      commands.Config,
	status:      commands.Status,
	reset:       commands.Reset,
	forget:      commands.Forget,
	shell:       commands.Shell,
	worktree:    commands.Worktree,
	skillUpdate: commands.SkillUpdate,
	rebuild:     commands.Rebuild,
	rehome:      commands.Rehome,
}

// command is one byre subcommand: its one-line summary for the command list,
// its full help for `byre <name> --help`, and its parse+dispatch function.
// The top-level usage is GENERATED from this table, so a command (or flag
// documented in its help) can't be forgotten there.
type command struct {
	name    string
	summary string
	help    string
	run     func(a app, s commands.Streams, dir string, rest []string) error
}

var cmdTable = []command{
	{
		name:    "develop",
		summary: "Set up and run the project container in the foreground.",
		help: `Usage: byre develop [--template <name>] [--agent <name>] [--self-edit]

Set up (generate + build the image) and run the project container in the
foreground. First run onboards the project (creates its host-side config).

  --template <name>  template for a NEW project's config (first run only; "none" to skip)
  --agent <name>     agent for a NEW project's config (first run only; "none" to skip)
  --self-edit        mount this project's host-side store read-write so the
                     agent can edit its own byre.config — a deliberate grant,
                     applied on the next develop`,
		run: func(a app, s commands.Streams, dir string, rest []string) error {
			var tmpl, agent string
			selfEdit := false
			for i := 0; i < len(rest); i++ {
				switch {
				case rest[i] == "--template":
					i++
					if i >= len(rest) {
						return usageError("byre develop: --template needs a value")
					}
					tmpl = rest[i]
				case rest[i] == "--agent":
					i++
					if i >= len(rest) {
						return usageError("byre develop: --agent needs a value")
					}
					agent = rest[i]
				case rest[i] == "--self-edit":
					selfEdit = true
				default:
					return usageError(fmt.Sprintf("byre develop: unknown argument %q", rest[i]))
				}
			}
			return a.develop(s, dir, tmpl, agent, selfEdit)
		},
	},
	{
		name:    "config",
		summary: "Edit this project's config interactively.",
		help: `Usage: byre config [--global]

Open the interactive editor for this project's host-side config
(~/.byre/projects/<id>/byre.config). Raw fields are shown, not edited.

  --global  edit your global defaults (~/.byre/default.config) instead`,
		run: func(a app, s commands.Streams, dir string, rest []string) error {
			global := false
			for _, arg := range rest {
				switch arg {
				case "--global":
					global = true
				default:
					return usageError(fmt.Sprintf("byre config: unknown argument %q", arg))
				}
			}
			return a.config(s, dir, global)
		},
	},
	{
		name:    "dockerfile",
		summary: "Print the generated Dockerfile for this directory.",
		help: `Usage: byre dockerfile

Print the Dockerfile byre would build for this directory (or the hand-written
one, when the config opts out of generation). Side-effect-free.`,
		run: func(a app, s commands.Streams, dir string, rest []string) error {
			if err := noArgs("dockerfile", rest); err != nil {
				return err
			}
			return a.dockerfile(s, dir)
		},
	},
	{
		name:    "dockerrun",
		summary: "Print the docker/podman run command byre would use.",
		help: `Usage: byre dockerrun

Print the exact docker/podman run invocation byre would use for this project —
the run-time counterpart to 'byre dockerfile'. Side-effect-free.`,
		run: func(a app, s commands.Streams, dir string, rest []string) error {
			if err := noArgs("dockerrun", rest); err != nil {
				return err
			}
			return a.dockerrun(s, dir)
		},
	},
	{
		name:    "status",
		summary: "Show resolved config, mounts, skills, container state.",
		help: `Usage: byre status [--self-edit]

Show the resolved view of this project: agent, engine, mounts, ports, volumes,
skill grants, and whether a session is running.

  --self-edit  also show the grant 'develop --self-edit' would add`,
		run: func(a app, s commands.Streams, dir string, rest []string) error {
			selfEdit := false
			for _, arg := range rest {
				switch arg {
				case "--self-edit":
					selfEdit = true
				default:
					return usageError(fmt.Sprintf("byre status: unknown argument %q", arg))
				}
			}
			return a.status(s, dir, selfEdit)
		},
	},
	{
		name:    "shell",
		summary: "Open a shell (as the dev user) in the running session.",
		help: `Usage: byre shell

Open an interactive shell in this project's running container, as the dev
user — for agent logins, running tests, poking around. Needs a session
started by 'byre develop'.`,
		run: func(a app, s commands.Streams, dir string, rest []string) error {
			if err := noArgs("shell", rest); err != nil {
				return err
			}
			return a.shell(s, dir)
		},
	},
	{
		name:    "worktree",
		summary: "Create a git worktree and start a parallel session in it.",
		help: `Usage: byre worktree <name> [--path <dir>] [--self-edit]

Create a linked git worktree for branch <name> (default location: a sibling
dir <repo>-<name>, from the configured worktree_base) and run 'byre develop'
in it — a parallel agent that inherits this repo's config, volumes, and image.

  --path <dir>  create the worktree at an explicit path instead
  --self-edit   forward 'develop --self-edit' for the new session`,
		run: func(a app, s commands.Streams, dir string, rest []string) error {
			var name, path string
			selfEdit := false
			for i := 0; i < len(rest); i++ {
				switch {
				case rest[i] == "--path":
					i++
					if i >= len(rest) {
						return usageError("byre worktree: --path needs a value")
					}
					path = rest[i]
				case rest[i] == "--self-edit":
					selfEdit = true
				case strings.HasPrefix(rest[i], "-"):
					return usageError(fmt.Sprintf("byre worktree: unknown flag %q", rest[i]))
				case name == "":
					name = rest[i]
				default:
					return usageError(fmt.Sprintf("byre worktree: unexpected argument %q", rest[i]))
				}
			}
			if name == "" {
				return usageError("usage: byre worktree <name> [--path <dir>] [--self-edit]")
			}
			return a.worktree(s, dir, name, path, selfEdit)
		},
	},
	{
		name:    "skill",
		summary: "skill update: re-materialize byre's built-in skills and templates.",
		help: `Usage: byre skill update

Re-materialize byre's built-in skills and templates into ~/.byre, picking up
shipped updates (a locally-modified copy is backed up under skills.bak/ or
templates.bak/). Follow with 'byre rebuild' to apply skill changes to the
image.`,
		run: func(a app, s commands.Streams, dir string, rest []string) error {
			if len(rest) != 1 || rest[0] != "update" {
				return usageError("usage: byre skill update   (re-materialize byre's built-in skills and templates)")
			}
			return a.skillUpdate(s)
		},
	},
	{
		name:    "reset",
		summary: "Wipe this project's named volumes.",
		help: `Usage: byre reset [--force|-y]

Permanently delete ALL of this project's named volumes (agent credentials,
caches — not the image). Prompts first; refuses while a session is running.

  --force, -y  skip the confirmation prompt`,
		run: func(a app, s commands.Streams, dir string, rest []string) error {
			force, err := parseForce("reset", rest)
			if err != nil {
				return err
			}
			return a.reset(s, dir, force)
		},
	},
	{
		name:    "rebuild",
		summary: "Rebuild the image with the cache disabled.",
		help: `Usage: byre rebuild

Regenerate the build context and rebuild this project's image with
--no-cache, picking up new upstream tool/package versions. Volumes are
untouched; the next 'byre develop' runs the fresh image.`,
		run: func(a app, s commands.Streams, dir string, rest []string) error {
			if err := noArgs("rebuild", rest); err != nil {
				return err
			}
			return a.rebuild(s, dir)
		},
	},
	{
		name:    "rehome",
		summary: "Re-point this directory's identity after a move.",
		help: `Usage: byre rehome <old-id>

After moving/renaming the project directory (which changes its path-derived
id), migrate the previous id's volumes onto the new identity. <old-id> is the
previous project id, from ~/.byre/projects/.`,
		run: func(a app, s commands.Streams, dir string, rest []string) error {
			if len(rest) != 1 {
				return usageError("usage: byre rehome <old-id>   (the previous project id, from ~/.byre/projects/)")
			}
			return a.rehome(s, dir, rest[0])
		},
	},
	{
		name:    "forget",
		summary: "Remove all byre host-side state for this directory.",
		help: `Usage: byre forget [--force|-y]

Completely remove byre's host-side state for this directory: named volumes,
the image, and ~/.byre/projects/<id>/ (config, adoption record, build
context). Your project tree is left alone. Prompts first.

  --force, -y  skip the confirmation prompt`,
		run: func(a app, s commands.Streams, dir string, rest []string) error {
			force, err := parseForce("forget", rest)
			if err != nil {
				return err
			}
			return a.forget(s, dir, force)
		},
	},
}

// usageText renders the top-level help from the command table.
func usageText() string {
	var b strings.Builder
	b.WriteString("byre — run an AI coding agent in a throwaway, project-scoped container.\n")
	b.WriteString("\nUsage: byre <command> [args]\n\nCommands:\n")
	for _, c := range cmdTable {
		fmt.Fprintf(&b, "  %-11s %s\n", c.name, c.summary)
	}
	b.WriteString("\nRun byre in the project directory you want to develop.\n")
	b.WriteString("Use 'byre <command> --help' for details on a command.")
	return b.String()
}

// usageError is a command-line parse failure: main prints it to stderr and
// exits 2, distinct from a byre failure (1) and an agent/refusal code.
type usageError string

func (e usageError) Error() string { return string(e) }

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
// command table. All parse failures come back as usageError; anything else is
// the command's own error, exit-mapped by main.
func run(a app, args []string, dir string, s commands.Streams) error {
	if len(args) < 1 {
		return usageError(usageText())
	}
	name, rest := args[0], args[1:]
	if name == "-h" || name == "--help" || name == "help" {
		fmt.Fprintln(s.Out, usageText())
		return nil
	}
	for _, c := range cmdTable {
		if c.name != name {
			continue
		}
		// -h/--help anywhere in the subcommand's args prints its help. Checked
		// before parsing, so 'byre worktree --help' is help, not an unknown flag.
		for _, arg := range rest {
			if arg == "-h" || arg == "--help" {
				fmt.Fprintln(s.Out, c.help)
				return nil
			}
		}
		return c.run(a, s, dir, rest)
	}
	return usageError(fmt.Sprintf("byre: unknown command %q\n\n%s", name, usageText()))
}

// noArgs rejects unexpected operands after a subcommand.
func noArgs(cmd string, rest []string) error {
	if len(rest) > 0 {
		return usageError(fmt.Sprintf("byre %s: unexpected arguments %v", cmd, rest))
	}
	return nil
}

// parseForce parses the shared --force/-y flag of the destructive commands.
func parseForce(cmd string, rest []string) (bool, error) {
	force := false
	for _, arg := range rest {
		switch arg {
		case "--force", "-y":
			force = true
		default:
			return false, usageError(fmt.Sprintf("byre %s: unknown argument %q", cmd, arg))
		}
	}
	return force, nil
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
