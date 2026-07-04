// Command byre runs an AI coding agent in a throwaway, project-scoped container.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"byre/internal/commands"
)

const usage = `byre — run an AI coding agent in a throwaway, project-scoped container.

Usage: byre <command> [args]

Commands:
  develop      Set up and run the project container in the foreground.
               (--self-edit mounts this project's host-side store read-write so
               the agent can edit its own byre.config — a deliberate grant.)
  config       Edit this project's config interactively (--global edits
               your ~/.byre/default.config). Raw fields are shown, not edited.
  dockerfile   Print the generated Dockerfile for this directory.
  dockerrun    Print the docker/podman run command byre would use (no side effects).
  status       Show resolved config, mounts, skills, container state.
  shell        Open a shell (as the dev user) in this project's running session.
  worktree     Create a git worktree (<repo>-<name>, or --path <dir>) and start a
               session in it — a parallel agent that inherits this repo's setup.
  skill update Re-materialize byre's built-in skills (pick up shipped updates).
  reset        Wipe this project's named volumes.
  rebuild      Rebuild the image with the cache disabled.
  rehome       Re-point this directory's identity after a move.
  forget       Remove all of byre's host-side state for this directory
               (volumes, image, config, adoption record). Leaves your tree.

Run byre in the project directory you want to develop.`

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
	skillUpdate func(s commands.Streams, dir string) error
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

// run parses argv (everything after the program name) and dispatches to a. All
// parse failures come back as usageError; anything else is the command's own
// error, exit-mapped by main.
func run(a app, args []string, dir string, s commands.Streams) error {
	if len(args) < 1 {
		return usageError(usage)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "-h", "--help", "help":
		fmt.Fprintln(s.Out, usage)
		return nil
	case "dockerfile":
		if err := noArgs(cmd, rest); err != nil {
			return err
		}
		return a.dockerfile(s, dir)
	case "dockerrun":
		if err := noArgs(cmd, rest); err != nil {
			return err
		}
		return a.dockerrun(s, dir)
	case "develop":
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
	case "config":
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
	case "status":
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
	case "reset":
		force, err := parseForce(cmd, rest)
		if err != nil {
			return err
		}
		return a.reset(s, dir, force)
	case "forget":
		force, err := parseForce(cmd, rest)
		if err != nil {
			return err
		}
		return a.forget(s, dir, force)
	case "shell":
		if err := noArgs(cmd, rest); err != nil {
			return err
		}
		return a.shell(s, dir)
	case "worktree":
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
	case "skill":
		if len(rest) != 1 || rest[0] != "update" {
			return usageError("usage: byre skill update   (re-materialize byre's built-in skills)")
		}
		return a.skillUpdate(s, dir)
	case "rebuild":
		if err := noArgs(cmd, rest); err != nil {
			return err
		}
		return a.rebuild(s, dir)
	case "rehome":
		if len(rest) != 1 {
			return usageError("usage: byre rehome <old-id>   (the previous project id, from ~/.byre/projects/)")
		}
		return a.rehome(s, dir, rest[0])
	default:
		return usageError(fmt.Sprintf("byre: unknown command %q\n\n%s", cmd, usage))
	}
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
