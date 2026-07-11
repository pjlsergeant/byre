package main

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/pjlsergeant/byre/internal/commands"
)

// completionCmd is byre's own `completion` command — cobra's stock one is
// disabled so the help can carry the recommended per-shell setup lines and
// so a bare/unknown invocation stays a usage error (exit 2), matching the
// rest of the CLI.
//
// The recommendation is the eval/source line, NOT a static file install: the
// script is regenerated at shell startup (~3ms — it's one exec of a static
// binary) so it always matches the installed byre, it needs no extra package
// (the bash script only uses bash-completion's _init_completion when
// present, with its own fallback), and byre writes nothing anywhere. A
// static-file `--install` shipped briefly in v0.1.5 and was walked back —
// see ADR 0022.
func completionCmd(s commands.Streams) *cobra.Command {
	c := &cobra.Command{
		Use:   "completion <shell>",
		Short: "Generate a shell completion script.",
		Long: `Print a completion script for bash, zsh, fish, or powershell, covering
every byre command and flag. Load it from your shell's startup file — it
regenerates in ~3ms and never goes stale across byre upgrades:

  bash        eval "$(byre completion bash)"            in ~/.bashrc
  zsh         source <(byre completion zsh)             in ~/.zshrc, after compinit
  fish        byre completion fish | source             in ~/.config/fish/config.fish
  powershell  byre completion powershell | Out-String | Invoke-Expression
                                                        in your $PROFILE`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return usageError("usage: byre completion bash|zsh|fish|powershell")
		},
	}
	c.AddCommand(
		shellCompletionCmd(s, "bash",
			`eval "$(byre completion bash)"`, "~/.bashrc",
			func(root *cobra.Command, w io.Writer, desc bool) error { return root.GenBashCompletionV2(w, desc) }),
		shellCompletionCmd(s, "zsh",
			"source <(byre completion zsh)", "~/.zshrc (after compinit)",
			func(root *cobra.Command, w io.Writer, desc bool) error {
				if desc {
					return root.GenZshCompletion(w)
				}
				return root.GenZshCompletionNoDesc(w)
			}),
		shellCompletionCmd(s, "fish",
			"byre completion fish | source", "~/.config/fish/config.fish",
			func(root *cobra.Command, w io.Writer, desc bool) error { return root.GenFishCompletion(w, desc) }),
		shellCompletionCmd(s, "powershell",
			"byre completion powershell | Out-String | Invoke-Expression", "your $PROFILE",
			func(root *cobra.Command, w io.Writer, desc bool) error {
				if desc {
					return root.GenPowerShellCompletionWithDesc(w)
				}
				return root.GenPowerShellCompletion(w)
			}),
	)
	return c
}

func shellCompletionCmd(s commands.Streams, shell, loadLine, loadWhere string,
	gen func(root *cobra.Command, w io.Writer, desc bool) error) *cobra.Command {
	var noDesc bool
	c := &cobra.Command{
		Use:   shell,
		Short: "Generate the " + shell + " completion script.",
		Long: "Print the " + shell + " completion script. To load it in every shell, add\n\n" +
			"  " + loadLine + "\n\nto " + loadWhere + ".",
		Args: noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			return gen(cmd.Root(), s.Out, !noDesc)
		},
	}
	c.Flags().BoolVar(&noDesc, "no-descriptions", false, "disable completion descriptions")
	return c
}
