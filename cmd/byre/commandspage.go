package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pjlsergeant/byre/internal/commands"
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
			fmt.Fprint(s.Out, renderCommandsPage(cmd.Root()))
			return nil
		},
	}
}

func renderCommandsPage(root *cobra.Command) string {
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
[completions](/docs/how-do-i/#get-tab-completion-for-byre-commands) cover
every command and flag.

| Command | What it does |
|---|---|
`)
	writeCommandRows(&b, root.Commands())
	return b.String()
}

func writeCommandRows(b *strings.Builder, cmds []*cobra.Command) {
	for _, c := range cmds {
		if c.Hidden || c.Name() == "help" {
			continue
		}
		b.WriteString(commandRow(c))
		// The per-shell completion children are four copies of the same
		// sentence; the parent row covers them.
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
