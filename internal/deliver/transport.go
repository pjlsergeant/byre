package deliver

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/hostopen"
)

// The transport is one `exec -i` per delivered file, running a small POSIX-sh
// script inside the box as the dev identity. The scripts take every variable
// piece as argv ($1..) — names are NEVER spliced into script text — and print
// the landed path as their only stdout, which the host captures.
//
// Write protocol (ADR 0021): stream stdin to a dotfile temp created under
// `set -C` (noclobber = O_CREAT|O_EXCL, refusing to write through any
// pre-existing name, planted symlinks included), then claim the final name
// with ln — link(2) fails EEXIST atomically, so uniquify (`report.pdf` →
// `report-2.pdf`) has no window in which two writers pick the same name and
// no rename ever clobbers. A died stream leaves at worst an orphaned dotfile.
//
// /inbox integrity: the inbox must be a real directory. It is baked
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
// The claim loop lives in fileClaim so tests can run it under a real sh
// against a temp dir (inboxCheck hardcodes /inbox).
const fileScript = `
set -eu
` + inboxCheck + fileClaim

const fileClaim = `
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
// Plain mkdir -p — interior structure is ours by construction; an agent
// racing symlinks into the fresh tree is a consciously accepted race
// (ADR 0021).
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
		// The USER named a symlink — following it is their explicit choice.
		// Route dirs (skipped, as everywhere — also kills cycles) and reject
		// non-files up front; the actual open (deliverTopFile, follow=true) is
		// still nonblocking + fd-stat'd so a symlink to a FIFO can't hang.
		target, err := os.Stat(src)
		if err != nil {
			return "", fmt.Errorf("delivering %s: broken symlink: %w", src, err)
		}
		if target.IsDir() {
			fmt.Fprintf(cfg.Err, "byre: skipping %s (symlink to a directory)\n", src)
			return "", nil
		}
		return deliverTopFile(cfg, sess, src, true)
	}
	switch {
	case info.Mode().IsRegular():
		// A regular file the user named: open race-safely (no-follow +
		// nonblocking), so a swap to an escaping symlink (exfil) or a FIFO
		// (hang) in the window after this Lstat can't change what is
		// delivered — the same protection interior entries get.
		return deliverTopFile(cfg, sess, src, false)
	case info.IsDir():
		return deliverDir(cfg, sess, src)
	default:
		fmt.Fprintf(cfg.Err, "byre: skipping %s (not a regular file or directory)\n", src)
		return "", nil
	}
}

// deliverTopFile opens a top-level source and streams it. follow follows a
// symlink the user named directly (their choice); otherwise O_NOFOLLOW so a
// path that was a regular file at Lstat is never followed if swapped to a
// symlink afterward. hostopen's descriptor judgment means a FIFO (named,
// swapped, or symlinked-to) returns immediately and is skipped rather than
// blocking — a non-regular here is a SKIP, not a failure, so the sentinel
// is branched on rather than propagated.
func deliverTopFile(cfg Config, sess Session, src string, follow bool) (string, error) {
	f, _, err := hostopen.OpenRegular(src, follow)
	if errors.Is(err, hostopen.ErrNotRegular) {
		fmt.Fprintf(cfg.Err, "byre: skipping %s (not a regular file)\n", src)
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("delivering %s: %w", src, err)
	}
	defer f.Close()
	return deliverStream(cfg, sess, f, filepath.Base(src), "/inbox", false)
}

// deliverStream is deliverFile's engine: content from any reader.
func deliverStream(cfg Config, sess Session, content io.Reader, name, destDir string, interior bool) (string, error) {
	stem, ext, sanitized := splitName(name)
	if sanitized {
		// The landing name was rewritten (control chars, or a path-shaped
		// --name forced to a basename) — say so, never rename silently.
		fmt.Fprintf(cfg.Err, "byre: renamed %q → %q\n", name, stem+ext)
	}
	args := []string{"sh", "-c", fileScript, "byre-deliver", destDir, stem, ext}
	if interior {
		args = append(args, "mk")
	}
	out, err := sess.Engine.ExecInput(sess.ID, sess.UID, sess.GID, content, args...)
	if err != nil {
		return "", fmt.Errorf("delivering %s: %w", name, err)
	}
	// Trim ONLY the protocol line-framing, never arbitrary whitespace: a
	// filename may legitimately end in a space, and the printed path must be
	// the path that actually landed (TrimSpace would report /inbox/report for
	// a file that landed as "/inbox/report ").
	landed := strings.TrimRight(out, "\r\n")
	if landed == "" {
		return "", fmt.Errorf("delivering %s: the box reported no landed path", name)
	}
	return landed, nil
}

// deliverDir claims /inbox/<dirname> and streams the tree into it, preserving
// structure. Per-source-entry failures don't stop the walk: successes stay,
// the summary and returned error carry the count.
func deliverDir(cfg Config, sess Session, src string) (string, error) {
	base := filepath.Base(src)
	stem, ext, sanitized := splitName(base)
	if sanitized {
		fmt.Fprintf(cfg.Err, "byre: renamed %q → %q\n", base, stem+ext)
	}
	out, err := sess.Engine.ExecInput(sess.ID, sess.UID, sess.GID, strings.NewReader(""),
		"sh", "-c", dirScript, "byre-deliver", stem, ext)
	if err != nil {
		return "", fmt.Errorf("delivering %s/: %w", src, err)
	}
	root := strings.TrimRight(out, "\r\n") // line-framing only — see deliverStream
	if root == "" {
		return "", fmt.Errorf("delivering %s/: the box reported no landed path", src)
	}

	// Interior files are opened AND enumerated THROUGH this root, never by a
	// re-walked pathname. os.Root resolves each component with openat, so a
	// symlink the agent planted inside the tree (it has /workspace rw) cannot
	// pull a file from OUTSIDE the delivered directory into the box, and an
	// entry cannot be swapped for an escaping symlink between the walk's
	// enumeration and the open (the check/open race). The root itself anchors
	// no-follow: deliverPath classified src as a real directory, so a symlink
	// here means the source was swapped mid-delivery — refuse rather than
	// anchor the walk in a tree the user never named (with the delivered
	// source agent-writable, plain OpenRoot would be a host-exfiltration
	// primitive). And because the walk rides hostRoot.FS(), the delivered
	// names and their contents always come from the SAME directory even if
	// the pathname is swapped mid-walk. This mirrors internal/build's
	// copyPath. A top-level symlink the USER named is a different case — that
	// is their explicit choice and is followed by deliverPath, not here.
	hostRoot, err := hostopen.OpenDirRootNoFollow(src)
	if err != nil {
		return root, fmt.Errorf("delivering %s/: %w", src, err)
	}
	defer hostRoot.Close()

	files, okFiles, failed := 0, 0, 0
	var bytes int64
	walkErr := fs.WalkDir(hostRoot.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		p := filepath.Join(src, filepath.FromSlash(rel)) // display only — opens ride hostRoot
		if err != nil {
			fmt.Fprintf(cfg.Err, "byre: %s: %v\n", p, err)
			failed++
			return nil
		}
		if rel == "." {
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
		// deliverContained opens rel through hostRoot (escape-safe) and streams
		// it. isLink distinguishes the failure semantics: for a symlink entry,
		// an open that fails or resolves to a non-regular target is a benign
		// SKIP (it escaped the tree, or points at a dir/FIFO — same as a
		// symlink-to-dir); for a real regular-file entry, the same open failing
		// (unreadable, or swapped to an escaping symlink after the walk) is a
		// genuine delivery FAILURE the user must hear about.
		deliverContained := func(isLink bool) {
			// hostopen's rooted open is the same escape-safe walk with the
			// FIFO/device judgment done at the descriptor (a FIFO — an
			// interior symlink to one, or a regular file swapped to one
			// after the walk — returns immediately instead of blocking);
			// the sentinel keeps the skip-vs-fail split per entry kind.
			f, st, oerr := hostopen.OpenRegularIn(hostRoot, rel)
			if errors.Is(oerr, hostopen.ErrNotRegular) {
				if isLink {
					fmt.Fprintf(cfg.Err, "byre: skipping %s (symlink to something other than a file)\n", p)
					return
				}
				fmt.Fprintf(cfg.Err, "byre: delivering %s: not a regular file\n", p)
				files++
				failed++
				return
			}
			if oerr != nil {
				if isLink {
					fmt.Fprintf(cfg.Err, "byre: skipping %s (symlink outside the delivered directory, or broken)\n", p)
					return
				}
				fmt.Fprintf(cfg.Err, "byre: delivering %s: %v\n", p, oerr)
				files++
				failed++
				return
			}
			defer f.Close()
			files++
			if _, ferr := deliverStream(cfg, sess, f, filepath.Base(dest), destDir, true); ferr != nil {
				fmt.Fprintf(cfg.Err, "byre: %v\n", ferr)
				failed++
				return
			}
			okFiles++
			bytes += st.Size()
		}
		switch {
		case d.IsDir():
			if _, derr := sess.Engine.ExecInput(sess.ID, sess.UID, sess.GID, strings.NewReader(""),
				"sh", "-c", mkdirScript, "byre-deliver", root+"/"+dest); derr != nil {
				fmt.Fprintf(cfg.Err, "byre: creating %s: %v\n", dest, derr)
				failed++
				return filepath.SkipDir
			}
		case d.Type().IsRegular():
			deliverContained(false)
		case d.Type()&os.ModeSymlink != 0:
			// Followed only if it stays inside the delivered tree (a relative
			// interior link); an escaping or non-regular target is skipped.
			// Note os.Root re-roots ABSOLUTE symlink targets, so an
			// absolute-but-in-tree link is skipped too — a conscious tradeoff
			// (matches internal/build's "no nested symlinks" spirit) for
			// swap-safe containment.
			deliverContained(true)
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
		// `failed` counts ENTRIES (files and interior dirs both), so a
		// dirs-only failure can't hide behind an "N of N files" line.
		fmt.Fprintf(cfg.Err, "byre: delivered %s — %d of %d files, %s; %d %s failed\n",
			root, okFiles, files, sizeString(bytes), failed, plural(failed, "entry", "entries"))
		return root, fmt.Errorf("delivering %s/: %d entries failed", src, failed)
	}
	fmt.Fprintf(cfg.Err, "byre: delivered %s — %d files, %s\n", root, files, sizeString(bytes))
	return root, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
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

// splitName splits a landing name into (stem, ext) for uniquify ("report",
// ".pdf"). It FORCES a basename first — a caller-supplied name (--name, a
// clipboard capture) can never escape the destination dir with `/` or `..`
// (the container has no /inbox-escape hatch until an explicit --to lands) —
// and sanitizes control characters (including newlines: the printed path and
// the porcelain grammar are line-framed by THIS rule, not by escaping).
func splitName(name string) (stem, ext string, sanitized bool) {
	base := filepath.Base(name)
	if base == "." || base == "/" || base == ".." || base == string(filepath.Separator) {
		base = "unnamed"
	}
	changed := base != name
	base, sc := sanitizeBase(base)
	ext = filepath.Ext(base)
	stem = strings.TrimSuffix(base, ext)
	if stem == "" { // dotfiles: Ext(".bashrc") is the whole name
		stem, ext = base, ""
	}
	return stem, ext, changed || sc
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
