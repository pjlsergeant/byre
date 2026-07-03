// Command byre runs an AI coding agent in a throwaway, project-scoped container.
package main

import (
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

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	switch cmd := os.Args[1]; cmd {
	case "-h", "--help", "help":
		fmt.Println(usage)
	case "dockerfile":
		noArgs(cmd)
		if err := commands.Dockerfile(os.Stdout, cwd()); err != nil {
			fatal(err)
		}
	case "dockerrun":
		noArgs(cmd)
		if err := commands.DockerRun(os.Stdout, cwd()); err != nil {
			fatal(err)
		}
	case "develop":
		var tmpl, agent string
		selfEdit := false
		args := os.Args[2:]
		for i := 0; i < len(args); i++ {
			switch {
			case args[i] == "--template":
				i++
				if i >= len(args) {
					fmt.Fprintln(os.Stderr, "byre develop: --template needs a value")
					os.Exit(2)
				}
				tmpl = args[i]
			case args[i] == "--agent":
				i++
				if i >= len(args) {
					fmt.Fprintln(os.Stderr, "byre develop: --agent needs a value")
					os.Exit(2)
				}
				agent = args[i]
			case args[i] == "--self-edit":
				selfEdit = true
			default:
				fmt.Fprintf(os.Stderr, "byre develop: unknown argument %q\n", args[i])
				os.Exit(2)
			}
		}
		if err := commands.Develop(cwd(), tmpl, agent, selfEdit); err != nil {
			fatal(err)
		}
	case "config":
		global := false
		for _, a := range os.Args[2:] {
			switch a {
			case "--global":
				global = true
			default:
				fmt.Fprintf(os.Stderr, "byre config: unknown argument %q\n", a)
				os.Exit(2)
			}
		}
		if err := commands.Config(cwd(), global); err != nil {
			fatal(err)
		}
	case "status":
		selfEdit := false
		for _, a := range os.Args[2:] {
			switch a {
			case "--self-edit":
				selfEdit = true
			default:
				fmt.Fprintf(os.Stderr, "byre status: unknown argument %q\n", a)
				os.Exit(2)
			}
		}
		if err := commands.Status(os.Stdout, cwd(), selfEdit); err != nil {
			fatal(err)
		}
	case "reset":
		force := false
		for _, a := range os.Args[2:] {
			switch a {
			case "--force", "-y":
				force = true
			default:
				fmt.Fprintf(os.Stderr, "byre reset: unknown argument %q\n", a)
				os.Exit(2)
			}
		}
		if err := commands.Reset(os.Stdout, os.Stdin, cwd(), force); err != nil {
			fatal(err)
		}
	case "forget":
		force := false
		for _, a := range os.Args[2:] {
			switch a {
			case "--force", "-y":
				force = true
			default:
				fmt.Fprintf(os.Stderr, "byre forget: unknown argument %q\n", a)
				os.Exit(2)
			}
		}
		if err := commands.Forget(os.Stdout, os.Stdin, cwd(), force); err != nil {
			fatal(err)
		}
	case "shell":
		noArgs(cmd)
		if err := commands.Shell(os.Stdout, cwd()); err != nil {
			fatal(err)
		}
	case "worktree":
		var name, path string
		selfEdit := false
		args := os.Args[2:]
		for i := 0; i < len(args); i++ {
			switch {
			case args[i] == "--path":
				i++
				if i >= len(args) {
					fmt.Fprintln(os.Stderr, "byre worktree: --path needs a value")
					os.Exit(2)
				}
				path = args[i]
			case args[i] == "--self-edit":
				selfEdit = true
			case strings.HasPrefix(args[i], "-"):
				fmt.Fprintf(os.Stderr, "byre worktree: unknown flag %q\n", args[i])
				os.Exit(2)
			case name == "":
				name = args[i]
			default:
				fmt.Fprintf(os.Stderr, "byre worktree: unexpected argument %q\n", args[i])
				os.Exit(2)
			}
		}
		if name == "" {
			fmt.Fprintln(os.Stderr, "usage: byre worktree <name> [--path <dir>] [--self-edit]")
			os.Exit(2)
		}
		if err := commands.Worktree(cwd(), name, path, selfEdit); err != nil {
			fatal(err)
		}
	case "skill":
		if len(os.Args) != 3 || os.Args[2] != "update" {
			fmt.Fprintln(os.Stderr, "usage: byre skill update   (re-materialize byre's built-in skills)")
			os.Exit(2)
		}
		if err := commands.SkillUpdate(os.Stdout, cwd()); err != nil {
			fatal(err)
		}
	case "rebuild":
		noArgs(cmd)
		if err := commands.Rebuild(os.Stdout, cwd()); err != nil {
			fatal(err)
		}
	case "rehome":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: byre rehome <old-id>   (the previous project id, from ~/.byre/projects/)")
			os.Exit(2)
		}
		if err := commands.Rehome(os.Stdout, cwd(), os.Args[2]); err != nil {
			fatal(err)
		}
	default:
		fmt.Fprintf(os.Stderr, "byre: unknown command %q\n\n%s\n", cmd, usage)
		os.Exit(2)
	}
}

// noArgs rejects unexpected operands after a subcommand.
func noArgs(cmd string) {
	if len(os.Args) > 2 {
		fmt.Fprintf(os.Stderr, "byre %s: unexpected arguments %v\n", cmd, os.Args[2:])
		os.Exit(2)
	}
}

func cwd() string {
	dir, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	return dir
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "byre: %v\n", err)
	os.Exit(1)
}
