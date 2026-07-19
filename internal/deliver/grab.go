package deliver

import (
	"fmt"
	"io"
	"path"
	"strings"
)

// grab.go is `byre grab <box-path> [<host-path>]`: deliver's mirror. The same
// machine-scoped discovery picks a box; a small POSIX-sh script classifies the
// box path; content streams OUT over `exec` (ExecOutput) into an atomic
// no-clobber claim on the host (grabhost.go). ADR 0040 carries the rationale.
//
// The trust polarity is reversed from deliver: everything that arrives from
// the box — existence, kind, enumeration, names, content — is agent-
// controlled, and the host destination may itself sit inside the agent-
// writable project tree. So the box side stays dumb (classify, enumerate,
// cat; every variable piece as argv, never spliced into script text) and ALL
// judgment lives in the host-side writes.

// classifyScript reports what the box path is: $1 is the absolute box path.
// Output: "f" for a regular file (symlinks followed — the user named the
// path, and the whole box filesystem is the agent's domain anyway), or
// "d <physical-path>" for a directory. The physical path (pwd -P) is what
// enumeration walks: find(1) does not follow a symlink given as its argument,
// so a symlinked directory would otherwise enumerate empty. Anything else is
// a loud exit 4.
const classifyScript = `
set -eu
if [ -d "$1" ]; then cd -- "$1"; printf 'd %s' "$(pwd -P)"
elif [ -f "$1" ]; then printf 'f'
elif [ -e "$1" ]; then echo "$1 is not a regular file or directory in the box" >&2; exit 4
else echo "no such path in the box: $1" >&2; exit 4
fi
`

// catScript streams one box file: $1 is the absolute file path. The -f check
// is politeness, not protection (the box can always swap the path after it —
// the host-side claim is what protects the host); its real job is a clear
// message when the agent's suggested path went stale.
const catScript = `
set -eu
if [ ! -f "$1" ]; then echo "$1 is not a file in the box" >&2; exit 4; fi
exec cat -- "$1"
`

// enumerateScript walks a box directory: $1 is the physical directory path.
// Output is NUL-framed records, "tag NUL path NUL": directories first (find
// emits parents before children), then regular files, then everything else
// (tag o — symlinks, sockets, FIFOs; the host skips them the way deliver
// skips their inbound cousins). Each pass survives the others failing (an
// unreadable subtree, say): whatever enumerated still grabs, and the nonzero
// exit tells the host the walk was incomplete.
const enumerateScript = `
set -u
code=0
find "$1" -type d -exec sh -c 'for p; do printf "d\0%s\0" "$p"; done' byre-grab {} + || code=1
find "$1" -type f -exec sh -c 'for p; do printf "f\0%s\0" "$p"; done' byre-grab {} + || code=1
find "$1" ! -type d ! -type f -exec sh -c 'for p; do printf "o\0%s\0" "$p"; done' byre-grab {} + || code=1
exit $code
`

// RunGrab grabs one box path onto the host and returns the landed host paths
// (top-level: one, or none when streaming to stdout). hostPath "-" streams a
// file's content to cfg.Out instead of landing it.
func RunGrab(cfg Config, opts Options, boxPath, hostPath string) ([]string, error) {
	sess, err := selectSession(cfg, opts)
	if err != nil {
		return nil, err
	}
	abs := boxAbs(boxPath)
	// pickArg, not ProjectID — same worktree-naming rule as RunSources.
	fmt.Fprintf(cfg.Err, "byre: grabbing %s from %s (%s, %s)%s\n",
		abs, pickArg(sess), sess.EngineName, shortID(sess.ID), foreignNote(sess))

	out, err := sess.Engine.ExecInput(sess.ID, sess.UID, sess.GID, strings.NewReader(""),
		"sh", "-c", classifyScript, "byre-grab", abs)
	if err != nil {
		return nil, fmt.Errorf("grabbing %s: %w", abs, err)
	}
	switch {
	case out == "f":
		if hostPath == "-" {
			return nil, grabFileToStdout(cfg, sess, abs)
		}
		return grabFile(cfg, sess, abs, hostPath)
	case strings.HasPrefix(out, "d "):
		if hostPath == "-" {
			return nil, fmt.Errorf("%s is a directory — '-' streams a single file; give the grab a destination path", abs)
		}
		return grabDir(cfg, sess, abs, out[2:], hostPath)
	default:
		return nil, fmt.Errorf("grabbing %s: unexpected reply from the box (%q)", abs, out)
	}
}

// boxAbs resolves a box path the way the box's shell would: absolute paths
// stand, relative ones anchor at /workspace (the box's working directory).
// Slash-form throughout — box paths never see the host's separator.
func boxAbs(p string) string {
	p = path.Clean(p)
	if path.IsAbs(p) {
		return p
	}
	return path.Join("/workspace", p)
}

// grabFileToStdout is hostPath "-": the content IS stdout, nothing lands.
func grabFileToStdout(cfg Config, sess Session, abs string) error {
	cw := &countWriter{w: cfg.Out}
	if err := sess.Engine.ExecOutput(sess.ID, sess.UID, sess.GID, cw,
		"sh", "-c", catScript, "byre-grab", abs); err != nil {
		return fmt.Errorf("grabbing %s: %w", abs, err)
	}
	fmt.Fprintf(cfg.Err, "byre: grabbed %s (%s)\n", abs, sizeString(cw.n))
	return nil
}

// grabFile lands one box file at the resolved destination.
func grabFile(cfg Config, sess Session, abs, hostPath string) ([]string, error) {
	dest, err := resolveDest(hostPath, path.Base(abs))
	if err != nil {
		return nil, err
	}
	defer dest.Close()
	landed, size, err := dest.claimFile(cfg, "", func(w io.Writer) error {
		return sess.Engine.ExecOutput(sess.ID, sess.UID, sess.GID, w,
			"sh", "-c", catScript, "byre-grab", abs)
	})
	if err != nil {
		return nil, fmt.Errorf("grabbing %s: %w", abs, err)
	}
	fmt.Fprintln(cfg.Out, landed)
	fmt.Fprintf(cfg.Err, "byre: grabbed %s → %s (%s)\n", abs, landed, sizeString(size))
	return []string{landed}, nil
}

// grabDir claims a directory name at the destination and lands the box tree
// inside it. phys is the classify-resolved physical path enumeration walks.
// Per-entry failures don't stop the walk: successes stay, the claimed path
// still prints, the error (and exit code) carry completeness — deliverDir's
// partial semantics, reversed.
func grabDir(cfg Config, sess Session, abs, phys, hostPath string) ([]string, error) {
	dest, err := resolveDest(hostPath, path.Base(abs))
	if err != nil {
		return nil, err
	}
	defer dest.Close()
	tree, err := dest.claimDir(cfg)
	if err != nil {
		return nil, fmt.Errorf("grabbing %s/: %w", abs, err)
	}
	defer tree.Close()

	enum, enumErr := sess.Engine.ExecInput(sess.ID, sess.UID, sess.GID, strings.NewReader(""),
		"sh", "-c", enumerateScript, "byre-grab", phys)

	files, okFiles, failed := 0, 0, 0
	var bytes int64
	for _, rec := range parseRecords(enum) {
		rel, ok := relUnder(phys, rec.path)
		if !ok {
			// Enumeration output is agent input: a record naming a path outside
			// the grabbed directory is ignored loudly, never landed.
			fmt.Fprintf(cfg.Err, "byre: ignoring enumerated %q (outside %s)\n", rec.path, phys)
			failed++
			continue
		}
		if rel == "" {
			continue // the grabbed directory itself — already claimed
		}
		clean, renamed, ok := sanitizeGrabRel(rel)
		if !ok {
			fmt.Fprintf(cfg.Err, "byre: skipping %s/%s (unusable name)\n", phys, rel)
			failed++
			continue
		}
		if renamed {
			fmt.Fprintf(cfg.Err, "byre: renamed %q (control characters) → %q\n", rel, clean)
		}
		switch rec.tag {
		case 'd':
			if err := tree.mkdirAll(clean); err != nil {
				fmt.Fprintf(cfg.Err, "byre: creating %s: %v\n", clean, err)
				failed++
			}
		case 'f':
			files++
			boxFile := rec.path
			// Interior dirs exist from the directories pass; MkdirAll again
			// covers a directory born between the two passes.
			if d := dirOf(clean); d != "" {
				if err := tree.mkdirAll(d); err != nil {
					fmt.Fprintf(cfg.Err, "byre: creating %s: %v\n", d, err)
					failed++
					continue
				}
			}
			_, size, err := tree.claimInterior(cfg, clean, func(w io.Writer) error {
				return sess.Engine.ExecOutput(sess.ID, sess.UID, sess.GID, w,
					"sh", "-c", catScript, "byre-grab", boxFile)
			})
			if err != nil {
				fmt.Fprintf(cfg.Err, "byre: grabbing %s: %v\n", boxFile, err)
				failed++
				continue
			}
			okFiles++
			bytes += size
		case 'o':
			fmt.Fprintf(cfg.Err, "byre: skipping %s (not a regular file or directory)\n", rec.path)
		}
	}
	if enumErr != nil {
		fmt.Fprintf(cfg.Err, "byre: enumerating %s in the box failed partway (%v) — the grab may be incomplete\n", abs, firstLine(enumErr))
		failed++
	}
	landed := tree.path
	fmt.Fprintln(cfg.Out, landed)
	if failed > 0 {
		// `failed` counts ENTRIES (files, dirs, and the enumeration itself),
		// so a dirs-only failure can't hide behind an "N of N files" line.
		fmt.Fprintf(cfg.Err, "byre: grabbed %s — %d of %d files, %s; %d %s failed\n",
			landed, okFiles, files, sizeString(bytes), failed, plural(failed, "entry", "entries"))
		return []string{landed}, fmt.Errorf("grabbing %s/: %d entries failed", abs, failed)
	}
	fmt.Fprintf(cfg.Err, "byre: grabbed %s — %d %s, %s\n", landed, files, plural(files, "file", "files"), sizeString(bytes))
	return []string{landed}, nil
}

// record is one enumerated box entry: d(irectory), f(ile), or o(ther).
type record struct {
	tag  byte
	path string
}

// parseRecords decodes enumerateScript's NUL-framed output. NUL is the one
// byte a filename cannot contain, so the framing holds against any name the
// agent invents; a malformed tail (a died exec mid-record) is dropped.
func parseRecords(out string) []record {
	var recs []record
	fields := strings.Split(out, "\x00")
	// A well-formed stream is tag,path pairs with one trailing "" from the
	// final NUL; iterate pairwise and ignore an incomplete tail.
	for i := 0; i+1 < len(fields); i += 2 {
		tag := fields[i]
		if len(tag) != 1 {
			break // out of frame — nothing after this point is trustworthy
		}
		switch tag[0] {
		case 'd', 'f', 'o':
			recs = append(recs, record{tag: tag[0], path: fields[i+1]})
		default:
			return recs
		}
	}
	return recs
}

// relUnder returns p relative to root ("" for root itself) and whether p is
// under root at all. Pure string containment — the pathnames come from the
// box and are judged, never resolved, on the host.
func relUnder(root, p string) (string, bool) {
	if p == root {
		return "", true
	}
	if strings.HasPrefix(p, root+"/") {
		return p[len(root)+1:], true
	}
	return "", false
}

// sanitizeGrabRel validates and sanitizes a box-relative path for landing on
// the host: any ""/"."/".." component refuses the entry (ok=false) — those
// never arrive from a real find walk, only from hostile output — and control
// characters rewrite per component (renamed reports it, the caller says so).
func sanitizeGrabRel(rel string) (clean string, renamed, ok bool) {
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", false, false
		}
		var ch bool
		parts[i], ch = sanitizeBase(p)
		renamed = renamed || ch
	}
	return strings.Join(parts, "/"), renamed, true
}
