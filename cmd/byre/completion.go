package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pjlsergeant/byre/internal/commands"
)

// zshSiteFunctionDirs are system fpath locations --install will use when one
// exists and is writable — Homebrew's site-functions (both Mac architectures)
// and linuxbrew's. No standard *user* autoload dir exists for zsh, so when
// none of these works the fallback is ~/.zfunc plus a printed rc line: byre
// never edits shell rc files. Package var so tests can point it at temp dirs.
var zshSiteFunctionDirs = []string{
	"/opt/homebrew/share/zsh/site-functions",
	"/usr/local/share/zsh/site-functions",
	"/home/linuxbrew/.linuxbrew/share/zsh/site-functions",
}

// completionCmd is byre's own `completion` command — cobra's stock one is
// disabled because it has no --install. Printing to stdout stays the
// composable contract; --install is the deliver-app doctrine applied to a
// completion script: a generated artifact at a printed path, idempotent
// regeneration, a same-named file byre didn't write is refused.
func completionCmd(s commands.Streams) *cobra.Command {
	c := &cobra.Command{
		Use:   "completion <shell>",
		Short: "Generate a shell completion script (--install puts it in place).",
		Long: `Print a completion script for bash, zsh, fish, or powershell.

--install (all shells but powershell) instead writes the script where your
shell will find it and prints the path. byre never edits your shell's rc
files: when a line is needed there, it prints the line for you to add.
Re-run --install to regenerate (e.g. after a byre upgrade adds commands);
uninstall by deleting the printed path.`,
		// Anything that isn't a known shell is a usage error (exit 2), not
		// cobra's default show-help-exit-0 for a bare parent command.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return usageError("usage: byre completion bash|zsh|fish|powershell [--install]")
		},
	}
	c.AddCommand(
		shellCompletionCmd(s, "bash", "Generate the bash completion script.",
			func(root *cobra.Command, w io.Writer) error { return root.GenBashCompletionV2(w, true) },
			installBash),
		shellCompletionCmd(s, "zsh", "Generate the zsh completion script.",
			func(root *cobra.Command, w io.Writer) error { return root.GenZshCompletion(w) },
			installZsh),
		shellCompletionCmd(s, "fish", "Generate the fish completion script.",
			func(root *cobra.Command, w io.Writer) error { return root.GenFishCompletion(w, true) },
			installFish),
		shellCompletionCmd(s, "powershell", "Generate the powershell completion script.",
			func(root *cobra.Command, w io.Writer) error { return root.GenPowerShellCompletionWithDesc(w) },
			nil), // no --install: the powershell profile IS an rc file
	)
	return c
}

// shellCompletionCmd builds one shell's subcommand. install == nil means the
// shell has no autoload location byre is willing to write to, so no --install
// flag is registered and the script only prints.
func shellCompletionCmd(s commands.Streams, shell, short string,
	gen func(root *cobra.Command, w io.Writer) error,
	install func(script []byte, s commands.Streams) error) *cobra.Command {
	var doInstall bool
	c := &cobra.Command{
		Use:   shell,
		Short: short,
		Args:  noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !doInstall {
				return gen(cmd.Root(), s.Out)
			}
			var buf bytes.Buffer
			if err := gen(cmd.Root(), &buf); err != nil {
				return err
			}
			return install(buf.Bytes(), s)
		},
	}
	if install != nil {
		c.Flags().BoolVar(&doInstall, "install", false, "write the script where "+shell+" will find it, and print the path")
	}
	return c
}

// foreignFileError is a refusal to overwrite a file byre didn't write — a
// real refusal the user must resolve, distinct from a not-writable location
// the zsh cascade may fall past.
type foreignFileError struct{ path string }

func (e foreignFileError) Error() string {
	return fmt.Sprintf("refusing to overwrite %s — it exists and byre didn't write it; move it aside and re-run", e.path)
}

// writeCompletion writes the generated script at target, creating parents.
// Every cobra-generated completion script carries a "completion for byre"
// header line — a same-named file without it is someone else's, refused.
func writeCompletion(target string, script []byte) error {
	if existing, err := os.ReadFile(target); err == nil {
		if !bytes.Contains(existing, []byte("completion for byre")) {
			return foreignFileError{target}
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, script, 0o644)
}

func installFish(script []byte, s commands.Streams) error {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(home, ".config")
	}
	target := filepath.Join(dir, "fish", "completions", "byre.fish")
	if err := writeCompletion(target, script); err != nil {
		return err
	}
	fmt.Fprintln(s.Out, target)
	fmt.Fprintln(s.Err, "byre: fish loads it automatically — restart your shell")
	return nil
}

func installBash(script []byte, s commands.Streams) error {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(home, ".local", "share")
	}
	target := filepath.Join(dir, "bash-completion", "completions", "byre")
	if err := writeCompletion(target, script); err != nil {
		return err
	}
	fmt.Fprintln(s.Out, target)
	fmt.Fprintln(s.Err, "byre: loaded by the bash-completion package (macOS's stock bash 3.2 can't) — restart your shell")
	return nil
}

func installZsh(script []byte, s commands.Streams) error {
	for _, dir := range zshSiteFunctionDirs {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		target := filepath.Join(dir, "_byre")
		err := writeCompletion(target, script)
		if err == nil {
			fmt.Fprintln(s.Out, target)
			fmt.Fprintln(s.Err, "byre: already on your fpath — restart your shell")
			return nil
		}
		var ferr foreignFileError
		if errors.As(err, &ferr) {
			// The operative fpath dir holds someone else's _byre; silently
			// shadowing it from ~/.zfunc would be worse than stopping.
			return err
		}
		// Not writable (e.g. root-owned site-functions): fall past it.
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	target := filepath.Join(home, ".zfunc", "_byre")
	if err := writeCompletion(target, script); err != nil {
		return err
	}
	fmt.Fprintln(s.Out, target)
	fmt.Fprintln(s.Err, "byre: add this to ~/.zshrc before compinit, then restart your shell:\n  fpath+=(~/.zfunc)")
	return nil
}
