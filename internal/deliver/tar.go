package deliver

import (
	"archive/tar"
	"fmt"
	"io"
	"strings"
)

// Tar mode (ADR 0037): the remote half of ssh delivery. The archive arrives
// on stdin and its entries feed straight into the existing per-file
// exec-stream transport — no temp files, no staging, nothing on this host's
// disk. Top-level names claim atomically exactly as local delivery claims
// them (uniquify, never overwrite); interior paths ride their claimed root.
//
// The archive normally comes from byre's own packer, but it is still INPUT:
// entries are confined to /inbox by construction (components are cleaned,
// `..` refuses, absolute names lose their leading slash), as an accident
// guard with the same posture as the rest of deliver — the agent cannot
// invoke deliver, so there is no adversary to confine.

// RunTar delivers a tar archive into the selected box and returns the landed
// top-level in-box paths (printed to cfg.Out as they are claimed — stdout is
// the contract the local side reads back over ssh).
func RunTar(cfg Config, opts Options, archive io.Reader) ([]string, error) {
	sess, err := selectSession(cfg, opts)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(cfg.Err, "byre: delivering to %s (%s, %s)%s\n",
		sess.ProjectID, sess.EngineName, shortID(sess.ID), foreignNote(sess))
	u := &tarUnpack{cfg: cfg, sess: sess, claimed: map[string]string{}}
	err = u.run(archive)
	shipClipboard(cfg, opts, u.landed)
	return u.landed, err
}

// tarUnpack carries one archive's delivery state: which top-level tar names
// have claimed which in-box roots, what landed, and the failure counts.
type tarUnpack struct {
	cfg     Config
	sess    Session
	claimed map[string]string // top-level tar name -> claimed in-box root
	landed  []string
	files   int
	okFiles int
	failed  int
	bytes   int64
}

func (u *tarUnpack) run(archive io.Reader) error {
	tr := tar.NewReader(archive)
	entries := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// A truncated or corrupt stream: what landed stays landed, the
			// error carries the truth (a died ssh mid-stream ends up here).
			return fmt.Errorf("reading the archive: %w", err)
		}
		entries++
		u.entry(hdr, tr)
	}
	if entries == 0 {
		return fmt.Errorf("the archive contained no entries")
	}
	if u.failed > 0 {
		fmt.Fprintf(u.cfg.Err, "byre: delivered %d of %d files, %s; %d %s failed\n",
			u.okFiles, u.files, sizeString(u.bytes), u.failed, plural(u.failed, "entry", "entries"))
		return fmt.Errorf("%d %s failed", u.failed, plural(u.failed, "entry", "entries"))
	}
	fmt.Fprintf(u.cfg.Err, "byre: delivered %d %s, %s\n",
		u.files, plural(u.files, "file", "files"), sizeString(u.bytes))
	return nil
}

// entry delivers one archive member. Failures warn and count — the walk
// continues, successes stay (deliverDir's partial semantics).
func (u *tarUnpack) entry(hdr *tar.Header, content io.Reader) {
	top, rest, renamed, ok := splitEntryName(hdr.Name)
	if !ok {
		fmt.Fprintf(u.cfg.Err, "byre: skipping archive entry %q (unusable name)\n", hdr.Name)
		u.failed++
		return
	}
	if renamed {
		clean := top
		if rest != "" {
			clean += "/" + rest
		}
		fmt.Fprintf(u.cfg.Err, "byre: renamed %q (control characters) → %q\n", hdr.Name, clean)
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		if rest == "" {
			if _, err := u.root(top); err != nil {
				fmt.Fprintf(u.cfg.Err, "byre: %v\n", err)
				u.failed++
			}
			return
		}
		root, err := u.root(top)
		if err == nil {
			_, err = u.sess.Engine.ExecInput(u.sess.ID, u.sess.UID, u.sess.GID,
				strings.NewReader(""), "sh", "-c", mkdirScript, "byre-deliver", root+"/"+rest)
		}
		if err != nil {
			fmt.Fprintf(u.cfg.Err, "byre: creating %s: %v\n", rest, err)
			u.failed++
		}
	case tar.TypeReg:
		u.files++
		if rest == "" {
			// A top-level file claims its own name; the landed path is a
			// top-level result.
			got, err := deliverStream(u.cfg, u.sess, content, top, "/inbox", false)
			if err != nil {
				fmt.Fprintf(u.cfg.Err, "byre: %v\n", err)
				u.failed++
				return
			}
			u.claim(got)
			u.okFiles++
			u.bytes += hdr.Size
			return
		}
		root, err := u.root(top)
		if err != nil {
			fmt.Fprintf(u.cfg.Err, "byre: %v\n", err)
			u.failed++
			return
		}
		destDir := root
		if d := dirOf(rest); d != "" {
			destDir += "/" + d
		}
		if _, err := deliverStream(u.cfg, u.sess, content, baseOf(rest), destDir, true); err != nil {
			fmt.Fprintf(u.cfg.Err, "byre: %v\n", err)
			u.failed++
			return
		}
		u.okFiles++
		u.bytes += hdr.Size
	default:
		// The packer never emits these; an alien archive's links/devices are
		// skipped the way local delivery skips them.
		fmt.Fprintf(u.cfg.Err, "byre: skipping archive entry %s (not a regular file or directory)\n", hdr.Name)
	}
}

// root returns top's claimed in-box root, claiming it on first need (an
// archive without explicit directory entries still lands correctly).
func (u *tarUnpack) root(top string) (string, error) {
	if r, ok := u.claimed[top]; ok {
		return r, nil
	}
	stem, ext, sanitized := splitName(top)
	if sanitized {
		fmt.Fprintf(u.cfg.Err, "byre: renamed %q → %q\n", top, stem+ext)
	}
	out, err := u.sess.Engine.ExecInput(u.sess.ID, u.sess.UID, u.sess.GID,
		strings.NewReader(""), "sh", "-c", dirScript, "byre-deliver", stem, ext)
	if err != nil {
		return "", fmt.Errorf("delivering %s/: %w", top, err)
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", fmt.Errorf("delivering %s/: the box reported no landed path", top)
	}
	u.claimed[top] = root
	u.claim(root)
	return root, nil
}

// claim records a landed top-level path and prints it — as it lands, in
// archive order, so the ssh reader on the other end sees paths stream in.
func (u *tarUnpack) claim(path string) {
	u.landed = append(u.landed, path)
	fmt.Fprintln(u.cfg.Out, path)
}

// splitEntryName normalizes an archive member name into a top-level
// component and an interior remainder, both slash-form and sanitized of
// control characters (renamed reports that sanitization changed something —
// the caller says so, never renames silently). Absolute names lose their
// leading slash (confinement to /inbox is unconditional); any `.`/`..`/empty
// component refuses the entry — ok=false, the caller counts it failed.
func splitEntryName(name string) (top, rest string, renamed, ok bool) {
	n := strings.TrimPrefix(name, "/")
	parts := strings.Split(n, "/")
	// A trailing slash (directory entries) yields one empty tail component;
	// drop it before validating.
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return "", "", false, false
	}
	for i, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", "", false, false
		}
		var ch bool
		parts[i], ch = sanitizeBase(p)
		renamed = renamed || ch
	}
	return parts[0], strings.Join(parts[1:], "/"), renamed, true
}

// baseOf is the final component of a slash-form relative path.
func baseOf(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[i+1:]
	}
	return rel
}
