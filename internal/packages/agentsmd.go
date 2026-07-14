package packages

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// agentsMD is the byre-owned guide landed at ~/.byre/AGENTS.md for coding
// agents (and humans) operating on the store host-side. Ownership contract
// (stated in its first paragraph): byre regenerates it, edits are
// overwritten. Content is version-independent so the file only rewrites
// when the binary's copy actually changes.
//
//go:embed agents.md
var agentsMD string

// agentsMDTitle -- the guide's first line -- is how byre recognizes its own
// past writes across versions: a file that does not start with it is
// user-placed and gets preserved, not clobbered, on takeover. Keep the
// title line in agents.md stable forever.
var agentsMDTitle = agentsMD[:strings.IndexByte(agentsMD, '\n')+1]

// ensureAgentsMD lands the byre-owned agent guide at the store root,
// rewriting it whenever the on-disk copy differs from the binary's --
// self-healing by design: this is the file that tells host-side agents
// what not to touch, so a stale or edited copy is the worst one to keep.
//
// Two hazards of "byre owns this path" are handled here, not assumed away:
// a pre-existing file byre never wrote (agents conventionally create
// AGENTS.md) is moved aside to AGENTS.md.bak once, never destroyed; and the
// write is stage+rename, which replaces a symlink at the path itself
// rather than following it into some unrelated target file.
func ensureAgentsMD(home string, out io.Writer) error {
	path := filepath.Join(home, "AGENTS.md")
	if fi, err := os.Lstat(path); err == nil && fi.Mode().IsRegular() {
		cur, rerr := os.ReadFile(path)
		if rerr == nil && bytes.Equal(cur, []byte(agentsMD)) {
			return nil
		}
		// Past byre writes all start with the stable title line; anything
		// else (or unreadable) is user-placed -- preserve it, as a
		// PRECONDITION of the takeover: if the preservation cannot
		// complete, fail without touching the file. Rename, not copy, so
		// an unreadable file is still saved whole; the backup name
		// unique-ifies when AGENTS.md.bak is already occupied.
		if rerr != nil || !bytes.HasPrefix(cur, []byte(agentsMDTitle)) {
			bak, berr := reserveBakName(path)
			if berr == nil {
				if berr = os.Rename(path, bak); berr != nil {
					// Don't leave the empty reserved placeholder behind.
					_ = os.Remove(bak)
				}
			}
			if berr != nil {
				return fmt.Errorf("agents guide: cannot preserve the existing AGENTS.md (not byre's): %w", berr)
			}
			if out != nil {
				fmt.Fprintf(out, "byre: ~/.byre/AGENTS.md existed but is not byre's -- preserved it as %s\n", filepath.Base(bak))
			}
		}
	}
	tmp, err := os.CreateTemp(home, ".agents-md-*")
	if err != nil {
		return fmt.Errorf("agents guide: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(agentsMD); err != nil {
		tmp.Close()
		return fmt.Errorf("agents guide: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("agents guide: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("agents guide: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("agents guide: %w", err)
	}
	if out != nil {
		fmt.Fprintln(out, "byre: wrote ~/.byre/AGENTS.md (byre-owned agent guide)")
	}
	return nil
}

// reserveBakName reserves a destination for preserving a foreign AGENTS.md:
// plain .bak when free, otherwise a unique .bak-* file. Both branches
// exclusively CREATE the destination (the caller's rename replaces only the
// placeholder we own), so an existing backup -- byre's own earlier takeover
// or the user's, even one landing concurrently -- is never clobbered.
func reserveBakName(path string) (string, error) {
	bak := path + ".bak"
	f, err := os.OpenFile(bak, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		f.Close()
		return bak, nil
	}
	if !os.IsExist(err) {
		return "", err
	}
	f, err = os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".bak-*")
	if err != nil {
		return "", err
	}
	f.Close()
	return f.Name(), nil
}
