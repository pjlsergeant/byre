package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
			func(root *cobra.Command, w io.Writer, desc bool) error { return root.GenBashCompletionV2(w, desc) },
			installBash),
		shellCompletionCmd(s, "zsh", "Generate the zsh completion script.",
			func(root *cobra.Command, w io.Writer, desc bool) error {
				if desc {
					return root.GenZshCompletion(w)
				}
				return root.GenZshCompletionNoDesc(w)
			},
			installZsh),
		shellCompletionCmd(s, "fish", "Generate the fish completion script.",
			func(root *cobra.Command, w io.Writer, desc bool) error { return root.GenFishCompletion(w, desc) },
			installFish),
		shellCompletionCmd(s, "powershell", "Generate the powershell completion script.",
			func(root *cobra.Command, w io.Writer, desc bool) error {
				if desc {
					return root.GenPowerShellCompletionWithDesc(w)
				}
				return root.GenPowerShellCompletion(w)
			},
			nil), // no --install: the powershell profile IS an rc file
	)
	return c
}

// shellCompletionCmd builds one shell's subcommand. install == nil means the
// shell has no autoload location byre is willing to write to, so no --install
// flag is registered and the script only prints.
func shellCompletionCmd(s commands.Streams, shell, short string,
	gen func(root *cobra.Command, w io.Writer, desc bool) error,
	install func(script []byte, s commands.Streams) error) *cobra.Command {
	var doInstall, noDesc bool
	c := &cobra.Command{
		Use:   shell,
		Short: short,
		Args:  noArgsU,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !doInstall {
				return gen(cmd.Root(), s.Out, !noDesc)
			}
			var buf bytes.Buffer
			if err := gen(cmd.Root(), &buf, !noDesc); err != nil {
				return err
			}
			return install(buf.Bytes(), s)
		},
	}
	c.Flags().BoolVar(&noDesc, "no-descriptions", false, "disable completion descriptions")
	if install != nil {
		c.Flags().BoolVar(&doInstall, "install", false, "write the script where "+shell+" will find it, and print the path")
	}
	return c
}

// completionMarker tags every script --install writes; it is the ownership
// test on re-install. The generated script's own header ("# zsh completion
// for byre") is NOT trusted for this — a hand-written completion could
// legitimately contain that phrase (zsh's, for one, must start with the
// same '#compdef byre' line any zsh completion for byre would).
const completionMarker = "# installed by 'byre completion --install' — re-running it overwrites this file"

// writeCompletion writes the generated script at target, creating parents.
// A pre-existing target must carry byre's install marker; a file byre can't
// prove it wrote — unreadable ones included — is refused, never truncated.
func writeCompletion(target string, script []byte) error {
	existing, err := os.ReadFile(target)
	switch {
	case err == nil:
		if !bytes.Contains(existing, []byte(completionMarker)) {
			return fmt.Errorf("refusing to overwrite %s — it exists and byre didn't write it; move it aside and re-run", target)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("refusing to overwrite %s — can't verify byre wrote it: %w", target, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if len(script) > 0 && script[len(script)-1] != '\n' {
		script = append(script, '\n')
	}
	script = append(script, []byte(completionMarker+"\n")...)
	return os.WriteFile(target, script, 0o644)
}

// xdgDir resolves an XDG base directory: the env var when set AND absolute
// (the spec says relative values must be ignored), else home+fallback.
func xdgDir(envVar string, fallback ...string) (string, error) {
	if dir := os.Getenv(envVar); dir != "" && filepath.IsAbs(dir) {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, fallback...)...), nil
}

func installFish(script []byte, s commands.Streams) error {
	dir, err := xdgDir("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return err
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
	dir, err := xdgDir("XDG_DATA_HOME", ".local", "share")
	if err != nil {
		return err
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
		_, statErr := os.Lstat(target)
		err := writeCompletion(target, script)
		if err == nil {
			fmt.Fprintln(s.Out, target)
			fmt.Fprintln(s.Err, "byre: already on your fpath — restart your shell")
			return nil
		}
		if statErr == nil {
			// A _byre already lives in this fpath dir and couldn't be
			// replaced — foreign file, or a byre-owned copy that failed to
			// update. Falling through would leave THIS stale/foreign copy
			// shadowing whatever we wrote further down the cascade: stop.
			return err
		}
		// Nothing here yet and the dir isn't writable (e.g. root-owned
		// site-functions): fall past it.
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
