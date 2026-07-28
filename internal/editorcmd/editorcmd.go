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

	"github.com/pjlsergeant/byre/internal/hostexec"
)

// Resolve returns the editor to launch: $EDITOR, or vi.
func Resolve() string {
	if e := strings.TrimSpace(os.Getenv("EDITOR")); e != "" {
		return e
	}
	return "vi"
}

// Command builds the editor invocation for path, wired to the terminal, or
// reports why byre will not launch it.
//
// roots governs the one binary BYRE picks here: the `sh` it execs. The editor
// VALUE is the user's, an opaque shell fragment byre passes to that shell
// verbatim -- byre does not parse it, resolve it, or judge where it points.
// A $EDITOR aimed at something in the project tree is the user's own
// arrangement (P1), and inspecting it would be the path-nannying byre parked;
// running byre's own shell out of a directory the box writes is not.
func Command(editor, path string, roots hostexec.Roots) (*exec.Cmd, error) {
	sh, err := hostexec.Look("sh", roots)
	if err != nil {
		return nil, err
	}
	// $0 is the editor string itself, so sh error messages name it.
	cmd := exec.Command(sh, "-c", editor+` "$@"`, editor, path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd, nil
}
