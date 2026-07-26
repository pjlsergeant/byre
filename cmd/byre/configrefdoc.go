package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pjlsergeant/byre/internal/build"
	"github.com/pjlsergeant/byre/internal/commands"
	"github.com/pjlsergeant/byre/internal/hostopen"
)

// configRefDocCmd is hidden plumbing for the repo: it renders the box-side
// config reference from the site's configuration-reference page, so the
// copy baked into every agent image is derived from the published one
// instead of hand-synced (the commands-page precedent, in the other
// direction). A test pins the checked-in derived file to this output.
func configRefDocCmd(s commands.Streams) *cobra.Command {
	return &cobra.Command{
		Use:    "config-reference-doc",
		Short:  "Render the baked config reference from the site page.",
		Hidden: true,
		Args:   noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The repo tree is agent-writable under self-host develop, so
			// even this maintainer tool reads fd-judged and bounded.
			src, err := hostopen.ReadFileBounded("site/content/docs/configuration-reference.md", true, 4<<20)
			if err != nil {
				return fmt.Errorf("run from the repo root: %w", err)
			}
			out, err := build.StripSiteReference(src)
			if err != nil {
				return err
			}
			fmt.Fprint(s.Out, string(out))
			return nil
		},
	}
}
