package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pjlsergeant/byre/internal/commands"
	"github.com/pjlsergeant/byre/internal/deliver"
)

// commandsPageCmd is hidden plumbing for the site: it renders the
// /docs/commands/ page from the live command tree, so the published table
// is derived from the binary instead of hand-synced. A golden test pins
// the checked-in site file to this output — a new command cannot ship
// without its line.
func commandsPageCmd(s commands.Streams) *cobra.Command {
	return &cobra.Command{
		Use:    "commands-page",
		Short:  "Render the site's commands page from the command tree.",
		Hidden: true,
		Args:   noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			page, err := renderCommandsPage(cmd.Root())
			if err != nil {
				return err
			}
			fmt.Fprint(s.Out, page)
			return nil
		},
	}
}

// commandsPageAreas assigns every visible top-level command to a page
// section. The curation is deliberate (the tree itself carries no
// grouping); renderCommandsPage errors on a visible command missing from
// the map — and on a mapped name that no longer exists — so a new or
// renamed command still cannot ship without a home on the page.
var commandsPageAreas = []struct {
	title string
	names []string
}{
	{"Daily driving", []string{"develop", "shell", "worktree", "deliver", "grab"}},
	{"Inspection", []string{"status", "dockerfile", "dockerrun", "ejectfirewall", "version"}},
	{"Configuration", []string{"config", "preset", "layer", "mcp", "claude-skill", "context"}},
	{"Skills & templates", []string{"skill", "template"}},
	{"Lifecycle & recovery", []string{"reset", "rebuild", "rehome", "forget"}},
	{"Shell integration", []string{"completion"}},
}

func renderCommandsPage(root *cobra.Command) (string, error) {
	byName := map[string]*cobra.Command{}
	for _, c := range root.Commands() {
		if c.Hidden || c.Name() == "help" {
			continue
		}
		byName[c.Name()] = c
	}

	var b strings.Builder
	b.WriteString(`---
title: Commands
weight: 40
description: every byre command, generated from the binary
---

<!-- GENERATED FILE — do not edit. Rendered from the cobra command tree:
     go run ./cmd/byre commands-page > site/content/docs/commands.md
     TestCommandsPagePinsSiteFile pins this file to that output. -->

Every command, one line each, straight from the binary. Flags and detail:
` + "`byre <command> --help`" + ` -- and
[completions](/docs/how-do-i/workflow/#get-tab-completion-for-byre-commands) cover
every command and flag.
`)

	seen := map[string]bool{}
	for _, area := range commandsPageAreas {
		b.WriteString("\n## " + area.title + "\n\n| Command | What it does |\n|---|---|\n")
		for _, n := range area.names {
			c, ok := byName[n]
			if !ok {
				return "", fmt.Errorf("commands-page area %q lists unknown command %q — fix commandsPageAreas (cmd/byre/commandspage.go)", area.title, n)
			}
			seen[n] = true
			b.WriteString(commandRow(c))
			// The per-shell completion children are four copies of the
			// same sentence; the parent row covers them.
			if c.Name() != "completion" {
				writeCommandRows(&b, c.Commands())
			}
		}
	}
	for n := range byName {
		if !seen[n] {
			return "", fmt.Errorf("command %q has no commands-page area — add it to commandsPageAreas (cmd/byre/commandspage.go)", n)
		}
	}
	b.WriteString(exitCodeSection())
	return b.String(), nil
}

// exitCodeSection renders the exit-code contract with the numbers taken from
// the constants the binary actually exits with, so a code cannot be changed
// in one place and published from another. The contract is CLI reference, and
// this is the CLI reference page; DELIVER.md states deliver's half for the
// people reading the design record.
func exitCodeSection() string {
	return fmt.Sprintf("\n## Exit codes\n\n"+
		"Scriptable, and the same for every command unless a row says otherwise.\n\n"+
		"| Code | Meaning |\n|---|---|\n"+
		"| `0` | Success. For `byre deliver` and `byre grab`, that means bytes landed. |\n"+
		"| `1` | byre failed, with the reason on stderr. Also every nothing-was-delivered outcome -- a cancelled picker, an empty paste, an ambiguous box set with no terminal. |\n"+
		"| `2` | You typed it wrong: an unknown flag, a bad argument count. |\n"+
		"| `%d` | `byre develop` refused to start because a session is already live in this directory. `reset` and `forget` decline the same situation with `1` -- a deliberate asymmetry, since only `develop` has a code to spare. |\n"+
		"| `%d` | `byre deliver --boxes` reached part of the pool but not all of it. |\n"+
		"| `%d` | byre crashed. That is a bug in byre; the report is on stderr and we would like to see it. |\n\n"+
		"`byre develop` adds one rule on top, deliberately: once the box has\n"+
		"actually run, **whatever status the agent's own process exits with, `0`\n"+
		"through `127`, is passed straight through** -- no byre banner -- so a\n"+
		"script sees what your agent did rather than what byre made of it. Past\n"+
		"`127` the box died on a signal (`128+n`), and byre calls that its own\n"+
		"failure with the signal decoded, because nothing in a box's normal life\n"+
		"ends that way. The overlap is real and cannot be designed away: a\n"+
		"`develop` exiting `1` is byre's own failure if it never got the box\n"+
		"running and the agent's status if it did -- the stderr message is what\n"+
		"tells them apart.\n",
		commands.ExitRefused, deliver.ExitPartialPool, exitPanic)
}

func writeCommandRows(b *strings.Builder, cmds []*cobra.Command) {
	for _, c := range cmds {
		if c.Hidden || c.Name() == "help" {
			continue
		}
		b.WriteString(commandRow(c))
		if c.Name() == "completion" {
			continue
		}
		writeCommandRows(b, c.Commands())
	}
}

// commandRow renders one command's table line (also the unit the coverage
// test asserts on, so the two can't diverge).
func commandRow(c *cobra.Command) string {
	use := strings.TrimSuffix(c.UseLine(), " [flags]")
	// Table cells: a raw | splits the cell, even inside a code span.
	use = strings.ReplaceAll(use, "|", `\|`)
	short := strings.ReplaceAll(c.Short, "|", `\|`)
	return fmt.Sprintf("| `%s` | %s |\n", use, short)
}
