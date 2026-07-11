package deliver

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The transport is one `exec -i` per delivered file, running a small POSIX-sh
// script inside the box as the dev identity. The scripts take every variable
// piece as argv ($1..) — names are NEVER spliced into script text — and print
// the landed path as their only stdout, which the host captures.
//
// Write protocol (decisions D8): stream stdin to a dotfile temp created under
// `set -C` (noclobber = O_CREAT|O_EXCL, refusing to write through any
// pre-existing name, planted symlinks included), then claim the final name
// with ln — link(2) fails EEXIST atomically, so uniquify (`report.pdf` →
// `report-2.pdf`) has no window in which two writers pick the same name and
// no rename ever clobbers. A died stream leaves at worst an orphaned dotfile.
//
// /inbox integrity (D5-D7): the inbox must be a real directory. It is baked
// dev-owned under root-owned / (the agent cannot replace it), so the check is
// belt-and-braces; a box whose image predates the bake gets the rebuild error
// (exit 3) rather than a root-exec backfill (reversed in review — ADR 0008).
const inboxCheck = `
if [ -L /inbox ] || { [ -e /inbox ] && [ ! -d /inbox ]; }; then
  echo "/inbox exists but is not a plain directory - refusing to deliver" >&2
  exit 3
fi
if [ ! -d /inbox ]; then
  echo "this box has no /inbox (image predates it); rebuild with 'byre develop'" >&2
  exit 3
fi
`

// fileScript delivers one file: $1 dest dir, $2 stem, $3 ext, $4 "mk" to
// mkdir -p the dest dir first (interior of a claimed directory tree only).
const fileScript = `
set -eu
` + inboxCheck + `
d=$1 stem=$2 ext=$3
if [ "${4:-}" = mk ]; then mkdir -p "$d"; fi
if [ ! -d "$d" ]; then echo "$d is not a directory" >&2; exit 1; fi
set -C
tmp= i=0
while [ $i -le 100 ]; do
  t="$d/.byre-tmp-$$-$i"
  if { : > "$t"; } 2>/dev/null; then tmp=$t; break; fi
  i=$((i+1))
done
if [ -z "$tmp" ]; then echo "cannot create a temp file in $d" >&2; exit 1; fi
trap 'rm -f "$tmp"' EXIT
cat >> "$tmp"
n="$stem$ext" k=1
while :; do
  if ln "$tmp" "$d/$n" 2>/dev/null; then printf '%s\n' "$d/$n"; exit 0; fi
  k=$((k+1))
  if [ $k -gt 9999 ]; then echo "could not claim a name for $stem$ext in $d" >&2; exit 1; fi
  n="$stem-$k$ext"
done
`

// dirScript claims a top-level directory name in /inbox: $1 stem, $2 ext.
// mkdir is the directory analogue of ln — it fails EEXIST atomically.
const dirScript = `
set -eu
` + inboxCheck + `
stem=$1 ext=$2
n="$stem$ext" k=1
while :; do
  if mkdir "/inbox/$n" 2>/dev/null; then printf '%s\n' "/inbox/$n"; exit 0; fi
  k=$((k+1))
  if [ $k -gt 9999 ]; then echo "could not claim a directory name for $stem$ext" >&2; exit 1; fi
  n="$stem-$k$ext"
done
`

// mkdirScript creates an interior directory of a claimed tree: $1 dir.
// Plain mkdir -p — interior structure is ours by construction (see the
// consciously-accepted race note in decisions D10).
const mkdirScript = `
set -eu
` + inboxCheck + `
mkdir -p "$1"
`

// deliverPath delivers one source argument and returns the landed top-level
// in-box path ("" when the source was skipped, e.g. a FIFO).
func deliverPath(cfg Config, sess Session, src string) (string, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return "", fmt.Errorf("delivering %s: %w", src, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Stat(src)
		if err != nil {
			return "", fmt.Errorf("delivering %s: broken symlink: %w", src, err)
		}
		if target.IsDir() {
			// Directory symlinks are skipped everywhere (also kills cycles).
			fmt.Fprintf(cfg.Err, "byre: skipping %s (symlink to a directory)\n", src)
			return "", nil
		}
		info = target // a file symlink is followed to its content
	}
	switch {
	case info.Mode().IsRegular():
		return deliverFile(sess, src, filepath.Base(src), "/inbox", false)
	case info.IsDir():
		return deliverDir(cfg, sess, src)
	default:
		fmt.Fprintf(cfg.Err, "byre: skipping %s (not a regular file or directory)\n", src)
		return "", nil
	}
}

// deliverFile streams one local file (or reader-backed capture) into destDir
// under name, returning the landed path the box reported (uniquify happens
// in-box, so the reported name is the truth).
func deliverFile(sess Session, src, name, destDir string, interior bool) (string, error) {
	f, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("delivering %s: %w", src, err)
	}
	defer f.Close()
	return deliverStream(sess, f, name, destDir, interior)
}

// deliverStream is deliverFile's engine: content from any reader.
func deliverStream(sess Session, content io.Reader, name, destDir string, interior bool) (string, error) {
	stem, ext, _ := splitName(name)
	args := []string{"sh", "-c", fileScript, "byre-deliver", destDir, stem, ext}
	if interior {
		args = append(args, "mk")
	}
	out, err := sess.Engine.ExecInput(sess.ID, sess.UID, sess.GID, content, args...)
	if err != nil {
		return "", fmt.Errorf("delivering %s: %w", name, err)
	}
	landed := strings.TrimSpace(out)
	if landed == "" {
		return "", fmt.Errorf("delivering %s: the box reported no landed path", name)
	}
	return landed, nil
}

// deliverDir claims /inbox/<dirname> and streams the tree into it, preserving
// structure. Per-source-entry failures don't stop the walk: successes stay,
// the summary and returned error carry the count (decisions D9).
func deliverDir(cfg Config, sess Session, src string) (string, error) {
	stem, ext, _ := splitName(filepath.Base(src))
	out, err := sess.Engine.ExecInput(sess.ID, sess.UID, sess.GID, strings.NewReader(""),
		"sh", "-c", dirScript, "byre-deliver", stem, ext)
	if err != nil {
		return "", fmt.Errorf("delivering %s/: %w", src, err)
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", fmt.Errorf("delivering %s/: the box reported no landed path", src)
	}

	files, okFiles, failed := 0, 0, 0
	var bytes int64
	walkErr := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			fmt.Fprintf(cfg.Err, "byre: %s: %v\n", p, err)
			failed++
			return nil
		}
		if p == src {
			return nil
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			fmt.Fprintf(cfg.Err, "byre: %s: %v\n", p, rerr)
			failed++
			return nil
		}
		dest, sanitized := sanitizeRel(rel)
		if sanitized {
			fmt.Fprintf(cfg.Err, "byre: renamed %q (control characters) → %q\n", rel, dest)
		}
		destDir := root
		if d := dirOf(dest); d != "" {
			destDir = root + "/" + d
		}
		deliverOne := func(size int64) {
			files++
			if _, ferr := deliverFile(sess, p, filepath.Base(dest), destDir, true); ferr != nil {
				fmt.Fprintf(cfg.Err, "byre: %v\n", ferr)
				failed++
				return
			}
			okFiles++
			bytes += size
		}
		switch {
		case d.Type()&os.ModeSymlink != 0:
			st, serr := os.Stat(p)
			if serr != nil || st.IsDir() {
				fmt.Fprintf(cfg.Err, "byre: skipping %s (symlink to a directory, or broken)\n", p)
				return nil
			}
			deliverOne(st.Size())
		case d.IsDir():
			if _, derr := sess.Engine.ExecInput(sess.ID, sess.UID, sess.GID, strings.NewReader(""),
				"sh", "-c", mkdirScript, "byre-deliver", root+"/"+dest); derr != nil {
				fmt.Fprintf(cfg.Err, "byre: creating %s: %v\n", dest, derr)
				failed++
				return filepath.SkipDir
			}
		case d.Type().IsRegular():
			var size int64
			if st, serr := d.Info(); serr == nil {
				size = st.Size()
			}
			deliverOne(size)
		default:
			fmt.Fprintf(cfg.Err, "byre: skipping %s (not a regular file or directory)\n", p)
		}
		return nil
	})
	if walkErr != nil {
		return root, fmt.Errorf("delivering %s/: %w", src, walkErr)
	}
	if failed > 0 {
		// The path stays useful and still prints; the exit code and this
		// count carry the truth — the path alone never asserts completeness.
		fmt.Fprintf(cfg.Err, "byre: delivered %s — %d of %d files, %s\n", root, okFiles, files, sizeString(bytes))
		return root, fmt.Errorf("delivering %s/: %d entries failed", src, failed)
	}
	fmt.Fprintf(cfg.Err, "byre: delivered %s — %d files, %s\n", root, files, sizeString(bytes))
	return root, nil
}

// dirOf is filepath.Dir but "" (not ".") for a bare name, so interior dest
// dirs join cleanly.
func dirOf(rel string) string {
	d := filepath.Dir(rel)
	if d == "." {
		return ""
	}
	return d
}

// splitName splits a basename into (stem, ext) for uniquify ("report", ".pdf")
// and sanitizes control characters (including newlines — the printed path and
// the porcelain grammar are line-framed by THIS rule, not by escaping).
func splitName(name string) (stem, ext string, sanitized bool) {
	name, sanitized = sanitizeBase(name)
	ext = filepath.Ext(name)
	stem = strings.TrimSuffix(name, ext)
	if stem == "" { // dotfiles: Ext(".bashrc") is the whole name
		stem, ext = name, ""
	}
	return stem, ext, sanitized
}

// sanitizeBase replaces control characters in a basename with '_'.
func sanitizeBase(name string) (string, bool) {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, name)
	if clean == "" {
		clean = "unnamed"
	}
	return clean, clean != name
}

// sanitizeRel applies sanitizeBase per path component.
func sanitizeRel(rel string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	changed := false
	for i, p := range parts {
		c, ch := sanitizeBase(p)
		parts[i] = c
		changed = changed || ch
	}
	return strings.Join(parts, "/"), changed
}

// sizeString renders a byte count the way a human reads one.
func sizeString(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
