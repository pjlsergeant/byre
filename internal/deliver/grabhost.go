package deliver

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The host-side write protocol: ADR 0021's claim, reversed. Grab never
// overwrites a host file — content streams into a dotfile temp created
// O_CREAT|O_EXCL, then a hardlink claims the final name (link(2) fails EEXIST
// atomically), uniquifying `report.pdf` → `report-2.pdf` on collision; a
// directory claims its top-level name with mkdir by the same rule. A died
// stream leaves at worst an orphaned dotfile — never a half-file under a real
// name.
//
// Every operation rides an os.Root anchored at the destination directory.
// That matters because the destination may sit inside the agent-writable
// project tree: O_EXCL and link(2) never write through a pre-existing
// symlink, and the Root's openat walk means an interior path the agent
// redirects mid-grab cannot land content outside the directory the user
// named. The user's own naming is honored as their explicit choice — a
// symlink THEY name as the destination directory resolves once, at the
// anchoring open (deliver's rule for user-named symlinks, reversed).

// destination is a resolved grab target: an anchored destination directory
// and the basename to claim in it.
type destination struct {
	root *os.Root
	dir  string // the anchored directory as the user knows it (display)
	base string // requested landing basename
}

// resolveDest interprets hostPath the way cp does: an existing directory
// lands boxBase inside it; anything else names the landing basename itself,
// and its parent must already exist (no mkdir -p surprises from a typo).
func resolveDest(hostPath, boxBase string) (*destination, error) {
	var dir, base string
	st, err := os.Stat(hostPath)
	switch {
	case err == nil && st.IsDir():
		dir, base = hostPath, boxBase
	case err == nil:
		dir, base = filepath.Dir(hostPath), filepath.Base(hostPath)
	case errors.Is(err, fs.ErrNotExist):
		if strings.HasSuffix(hostPath, "/") {
			return nil, fmt.Errorf("destination %s: no such directory", hostPath)
		}
		parent := filepath.Dir(hostPath)
		pst, perr := os.Stat(parent)
		if perr != nil {
			return nil, fmt.Errorf("destination %s: %w", hostPath, perr)
		}
		if !pst.IsDir() {
			return nil, fmt.Errorf("destination %s: %s is not a directory", hostPath, parent)
		}
		dir, base = parent, filepath.Base(hostPath)
	default:
		return nil, fmt.Errorf("destination %s: %w", hostPath, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("destination %s: %w", hostPath, err)
	}
	return &destination{root: root, dir: dir, base: base}, nil
}

func (d *destination) Close() error { return d.root.Close() }

// claimFile claims d.base in the destination and streams fill's output into
// it, returning the landed host path and size.
func (d *destination) claimFile(cfg Config, relDir string, fill func(io.Writer) error) (string, int64, error) {
	stem, ext, sanitized := splitName(d.base)
	if sanitized {
		fmt.Fprintf(cfg.Err, "byre: renamed %q → %q\n", d.base, stem+ext)
	}
	claimed, size, err := claimStream(d.root, relDir, stem, ext, fill)
	if err != nil {
		return "", 0, err
	}
	if baseOf(claimed) != stem+ext {
		fmt.Fprintf(cfg.Err, "byre: %s existed — landed as %s\n", stem+ext, baseOf(claimed))
	}
	return filepath.Join(d.dir, filepath.FromSlash(claimed)), size, nil
}

// claimDir claims d.base as a directory (mkdir is the directory analogue of
// link — it fails EEXIST atomically) and returns the anchored tree interior
// entries land in.
func (d *destination) claimDir(cfg Config) (*tree, error) {
	stem, ext, sanitized := splitName(d.base)
	if sanitized {
		fmt.Fprintf(cfg.Err, "byre: renamed %q → %q\n", d.base, stem+ext)
	}
	n := stem + ext
	for k := 2; ; k++ {
		err := d.root.Mkdir(n, 0o755)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if k > 9999 {
			return nil, fmt.Errorf("could not claim a directory name for %s%s in %s", stem, ext, d.dir)
		}
		n = fmt.Sprintf("%s-%d%s", stem, k, ext)
	}
	if n != stem+ext {
		fmt.Fprintf(cfg.Err, "byre: %s existed — landed as %s\n", stem+ext, n)
	}
	sub, err := d.root.OpenRoot(n)
	if err != nil {
		return nil, err
	}
	return &tree{root: sub, path: filepath.Join(d.dir, n)}, nil
}

// tree is a claimed destination directory; interior writes ride its Root, so
// nothing the box enumerates can land outside it.
type tree struct {
	root *os.Root
	path string // the claimed directory as the user knows it (display)
}

func (t *tree) Close() error { return t.root.Close() }

func (t *tree) mkdirAll(rel string) error {
	return t.root.MkdirAll(filepath.FromSlash(rel), 0o755)
}

// claimInterior claims rel's basename inside rel's directory and streams
// fill's output into it. Interior collisions uniquify exactly like top-level
// ones (in a fresh tree a collision means a race — the claim still never
// clobbers).
func (t *tree) claimInterior(cfg Config, rel string, fill func(io.Writer) error) (string, int64, error) {
	stem, ext, sanitized := splitName(baseOf(rel))
	if sanitized {
		fmt.Fprintf(cfg.Err, "byre: renamed %q → %q\n", baseOf(rel), stem+ext)
	}
	claimed, size, err := claimStream(t.root, dirOf(rel), stem, ext, fill)
	if err != nil {
		return "", 0, err
	}
	return filepath.Join(t.path, filepath.FromSlash(claimed)), size, nil
}

// claimStream is the file-claim engine: stream to an O_EXCL dotfile temp in
// relDir, hardlink to claim the final name, uniquify on EEXIST. Returns the
// claimed root-relative path (slash-form) and the byte count.
func claimStream(root *os.Root, relDir, stem, ext string, fill func(io.Writer) error) (string, int64, error) {
	join := func(n string) string {
		if relDir == "" {
			return n
		}
		return relDir + "/" + n
	}
	var tmp *os.File
	var tmpName string
	for i := 0; i <= 100; i++ {
		n := join(fmt.Sprintf(".byre-tmp-%d-%d", os.Getpid(), i))
		f, err := root.OpenFile(filepath.FromSlash(n), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			tmp, tmpName = f, n
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", 0, err
		}
	}
	if tmp == nil {
		return "", 0, fmt.Errorf("cannot create a temp file in the destination")
	}
	defer root.Remove(filepath.FromSlash(tmpName))
	cw := &countWriter{w: tmp}
	ferr := fill(cw)
	cerr := tmp.Close()
	if ferr != nil {
		return "", 0, ferr
	}
	if cerr != nil {
		return "", 0, cerr
	}
	n := stem + ext
	for k := 2; ; k++ {
		err := root.Link(filepath.FromSlash(tmpName), filepath.FromSlash(join(n)))
		if err == nil {
			return join(n), cw.n, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", 0, err
		}
		if k > 9999 {
			return "", 0, fmt.Errorf("could not claim a name for %s%s", stem, ext)
		}
		n = fmt.Sprintf("%s-%d%s", stem, k, ext)
	}
}

// countWriter counts what passes through — grab's sizes are measured on the
// host, not trusted from the box.
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	m, err := c.w.Write(p)
	c.n += int64(m)
	return m, err
}
