package main

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestCommandsPagePinsSiteFile is P10's tripwire: the checked-in site page
// must be exactly what the command tree renders. When it fails, regenerate:
//
//	go run ./cmd/byre commands-page > site/content/docs/commands.md
func TestCommandsPagePinsSiteFile(t *testing.T) {
	s, out := testStreams()
	root := newRootCmd(recorderApp(map[string]string{}), "/proj", s)
	root.SetArgs([]string{"commands-page"})
	if err := root.Execute(); err != nil {
		t.Fatalf("commands-page: %v", err)
	}
	want, err := os.ReadFile("../../site/content/docs/commands.md")
	if err != nil {
		t.Fatalf("reading site page: %v", err)
	}
	if got := out.String(); got != string(want) {
		t.Errorf("site/content/docs/commands.md is stale.\nRegenerate: go run ./cmd/byre commands-page > site/content/docs/commands.md\n--- rendered ---\n%s", got)
	}
}

// The rendered page must cover every visible command — no silent drops
// beyond the deliberate completion-children fold.
func TestCommandsPageCoversTree(t *testing.T) {
	s, out := testStreams()
	root := newRootCmd(recorderApp(map[string]string{}), "/proj", s)
	root.SetArgs([]string{"commands-page"})
	if err := root.Execute(); err != nil {
		t.Fatalf("commands-page: %v", err)
	}
	page := out.String()
	var walk func(cmds []*cobra.Command)
	walk = func(cmds []*cobra.Command) {
		for _, c := range cmds {
			if c.Hidden || c.Name() == "help" {
				continue
			}
			if !strings.Contains(page, "| `"+strings.ReplaceAll(c.CommandPath(), "|", `\|`)) {
				t.Errorf("command %q missing from rendered page", c.CommandPath())
			}
			if c.Name() == "completion" { // per-shell children fold into the parent row
				continue
			}
			walk(c.Commands())
		}
	}
	walk(root.Commands())
}
