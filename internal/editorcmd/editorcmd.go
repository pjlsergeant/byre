// Package editorcmd is the one place byre launches the user's $EDITOR --
// the config UI's ^e handoffs and the `byre context add` prose flow share
// it. The editor value runs through the shell (`sh -c '<editor> "$@"'`, the
// git approach), so quoted executable paths and flag-carrying values like
//
//	EDITOR='"/Applications/Visual Studio Code.app/.../code" --wait'
//
// work as the user wrote them -- a whitespace split cannot parse those.
package editorcmd

import (
	"os"
	"os/exec"
	"strings"
)

// Resolve returns the editor to launch: $EDITOR, or vi.
func Resolve() string {
	if e := strings.TrimSpace(os.Getenv("EDITOR")); e != "" {
		return e
	}
	return "vi"
}

// Command builds the editor invocation for path, wired to the terminal.
func Command(editor, path string) *exec.Cmd {
	// $0 is the editor string itself, so sh error messages name it.
	cmd := exec.Command("sh", "-c", editor+` "$@"`, editor, path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd
}
