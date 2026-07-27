package hostopen

import (
	"errors"
	"io/fs"
	"os"
)

// probe.go answers "what, if anything, is sitting at this name?" without
// following a symlink standing at the final component.
//
// These are the cheapest calls byre makes and the easiest to get subtly
// wrong: os.Stat resolves the leaf, so a probe of a byre record that a box
// has replaced with a link reports on the LINK TARGET -- byre asks about its
// own file and is answered about /etc/passwd. Nothing byre probes here is
// ever legitimately a symlink, so the refusal belongs in the function rather
// than in a judgement re-made at each call site.
//
// Neither function opens anything, so neither can block and neither returns
// content: a wrong answer (from a swapped ANCESTOR, which no leaf rule can
// close) costs a wrong existence or mode, never a read. Every caller that
// goes on to ACT on the answer opens through this package's real functions,
// where the swap would be caught.
//
// NOT for the config family. A user symlinking ~/.byre/default.config out of
// a dotfiles repo is a supported arrangement, and whether to follow there is
// the caller's trust ruling, carried explicitly by config.ParseFile's follow
// argument.

// ExistsNoFollow reports whether anything at all sits at path -- a dangling
// symlink counts, because something is there. The three states are distinct
// on purpose: (true, nil) something is here, (false, nil) PROVABLY nothing is
// here, (false, err) could not tell (EACCES and friends). Callers that act on
// absence must check the error, not just the bool: "I could not look" is not
// "it is gone".
func ExistsNoFollow(path string) (bool, error) {
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// StatNoFollow describes the object at path itself, never what a symlink
// there points at. For callers that need the mode or the mtime rather than
// mere existence.
func StatNoFollow(path string) (fs.FileInfo, error) { return os.Lstat(path) }
