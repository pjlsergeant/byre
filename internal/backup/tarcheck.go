package backup

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// The nested-tar contract. Every volume payload a backup carries is a plain
// tar of the volume root, and restore validates it header by header through
// archive/tar BEFORE any project state is written, then pours a stream the
// validator REBUILDS entry by entry -- never the file's own bytes. The
// restored tree is the source tree under one stated transformation and
// nothing else: ownership replaced by the box identity (the pour's chown),
// setuid/setgid/sticky zeroed, FIFOs and devices absent; every other mode
// bit, every mtime to the nanosecond, every size and every link target kept.
//
// The rules are spelled once, here, as code; ADR 0059's prose is the
// commentary. Each refusal names the entry so the user can see what a
// hand-made or damaged archive asked for, and uses %q so a name carrying a
// newline or an escape cannot break the one line the summary prints.

// MaxEntries bounds one payload: a design bound, not an engine fact.
const MaxEntries = 1_000_000

// modeTypeBits is the Unix file-type field of a mode word, and modeSocket the
// value naming a socket. A socket has no type flag of its own in any tar
// standard; archive/tar reads the type out of these bits (its c_ISSOCK), so
// they are the only spelling a hand-made archive has for one. GNU tar omits a
// socket at the source, so a socket here is not output byre wrote. Every
// OTHER file-type value in these bits is ignored: the type flag is the
// authority on an entry's kind, and the rebuilt header carries the permission
// bits alone.
const (
	modeTypeBits = 0o170000
	modeSocket   = 0o140000
)

// Link is one symlink the review lists: the entry's name and the target it
// carries verbatim. byre never resolves the target; it is agent-authored
// content and the review says so.
type Link struct {
	Name, Target string
}

// Dropped is one FIFO, character device or block device the pour leaves out,
// by name, with the kind for the summary.
type Dropped struct {
	Name, Kind string
}

// Report is what one validation pass learned about a payload.
type Report struct {
	// Entries counts the entries the validator accepted or dropped (regular
	// files, directories, symlinks, hardlinks, FIFOs, devices), excluding
	// the root header and excluding both kinds of PAX header.
	Entries int64
	// Absolute lists symlinks whose target begins with "/".
	Absolute []Link
	// Traversing lists symlinks whose relative target, joined lexically with
	// the link's own directory, leaves the volume root.
	Traversing []Link
	// Dropped lists the FIFOs and devices the pour omits.
	Dropped []Dropped
}

// kind is what a path has been seen as so far in one payload.
type kind uint8

const (
	kindUnseen      kind = iota
	kindImplicitDir      // created by an earlier entry's implicit parents
	kindDir
	kindFile
	kindSymlink
	kindHardlink
	kindDropped // a FIFO or device: occupies the name, nothing may sit under it
)

func (k kind) String() string {
	switch k {
	case kindImplicitDir, kindDir:
		return "a directory"
	case kindFile:
		return "a regular file"
	case kindSymlink:
		return "a symlink"
	case kindHardlink:
		return "a hardlink"
	case kindDropped:
		return "a FIFO or device (not carried)"
	}
	return "unseen"
}

// Validate reads one payload to its end, applying the contract, and returns
// the report. Nothing is written.
func Validate(r io.Reader) (Report, error) {
	return walk(r, nil)
}

// Rebuild is Validate that also writes the rebuilt PAX stream to w: the
// pour's input. Global headers, vendor records and dropped entries are not
// carried; ownership fields are zero; special mode bits are zero.
func Rebuild(r io.Reader, w io.Writer) (Report, error) {
	tw := tar.NewWriter(w)
	rep, err := walk(r, tw)
	if err != nil {
		return rep, err
	}
	if err := tw.Close(); err != nil {
		return rep, err
	}
	return rep, nil
}

// walk is the one pass both Validate and Rebuild run. tw nil = validate only.
// One pass, so a payload is never judged by one reading and poured from
// another: Rebuild emits each entry as it is accepted.
func walk(r io.Reader, tw *tar.Writer) (Report, error) {
	var rep Report
	tr := tar.NewReader(r)
	seen := map[string]kind{}
	rootSeen := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return rep, nil
		}
		if err != nil {
			return rep, fmt.Errorf("reading the payload: %w", err)
		}
		// A global PAX header is the one meta entry archive/tar returns
		// rather than consuming (local 'x' headers are folded into the next
		// entry's fields). Its records are applied to nothing -- verified
		// against Go 1.27.1: the reader returns immediately on 'g' and its
		// paxHdrs do not reach the following entry -- it is not counted, and
		// it is not replayed. It is checked FIRST because mergePAX leaves
		// its records on the header, GNU.sparse.* included.
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if isSparse(hdr) {
			return rep, fmt.Errorf("entry %q is a sparse file; sparse entries are not carried", hdr.Name)
		}
		if hdr.Mode&modeTypeBits == modeSocket {
			return rep, fmt.Errorf("entry %q is a socket; sockets are not carried", hdr.Name)
		}
		// archive/tar sets the data section of a header-only type to zero
		// bytes and skips no padding, so a nonzero size there makes the next
		// 512 bytes of CONTENT read as a header: the same bytes mean
		// different trees to different tars. Refused with the rule named,
		// rather than left to surface as "invalid tar header" later.
		if headerOnlyType(hdr.Typeflag) && hdr.Size != 0 {
			return rep, fmt.Errorf("entry %q is type %q and declares %d bytes of content; that type carries none", hdr.Name, string(hdr.Typeflag), hdr.Size)
		}
		name, isRoot, err := normalizeName(hdr.Name)
		if err != nil {
			return rep, fmt.Errorf("entry %q: %w", hdr.Name, err)
		}
		if isRoot {
			if hdr.Typeflag != tar.TypeDir {
				return rep, fmt.Errorf("entry %q names the volume root but is not a directory", hdr.Name)
			}
			if rootSeen {
				return rep, fmt.Errorf("entry %q: the volume root appears twice", hdr.Name)
			}
			rootSeen = true
			if tw != nil {
				if err := writeRebuilt(tw, hdr, "./", ""); err != nil {
					return rep, err
				}
			}
			continue
		}
		rep.Entries++
		if rep.Entries > MaxEntries {
			return rep, fmt.Errorf("the payload holds more than %d entries", MaxEntries)
		}
		// Every ancestor that appeared earlier must be a directory; one that
		// did not appear is created by tar and is a directory from now on.
		for _, anc := range ancestors(name) {
			switch seen[anc] {
			case kindUnseen:
				seen[anc] = kindImplicitDir
			case kindImplicitDir, kindDir:
			default:
				return rep, fmt.Errorf("entry %q sits under %q, which an earlier entry made %s", hdr.Name, anc, seen[anc])
			}
		}
		switch seen[name] {
		case kindUnseen:
		case kindImplicitDir:
			// An earlier entry's parents made this a directory; only a
			// directory entry may now name it (its mode and mtime land).
			if hdr.Typeflag != tar.TypeDir {
				return rep, fmt.Errorf("entry %q names a path an earlier entry's parents already made a directory", hdr.Name)
			}
		default:
			return rep, fmt.Errorf("entry %q appears twice", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeReg:
			seen[name] = kindFile
			if tw != nil {
				if err := writeRebuilt(tw, hdr, name, ""); err != nil {
					return rep, err
				}
			}
			if err := copyContent(tw, tr, hdr); err != nil {
				return rep, err
			}
		case tar.TypeDir:
			seen[name] = kindDir
			if tw != nil {
				// The rebuilt directory name keeps the trailing slash every
				// tar writes, so the pour's tar reads a directory whatever
				// its own type-flag leniency.
				if err := writeRebuilt(tw, hdr, name+"/", ""); err != nil {
					return rep, err
				}
			}
		case tar.TypeSymlink:
			seen[name] = kindSymlink
			classifyLink(&rep, name, hdr.Linkname)
			if tw != nil {
				// Verbatim: the contract constrains Name only, and byre
				// never resolves a link target.
				if err := writeRebuilt(tw, hdr, name, hdr.Linkname); err != nil {
					return rep, err
				}
			}
		case tar.TypeLink:
			// The target rides the Name grammar, and the refusal NAMES the
			// rule that fired: a dozen rules can reject one string, and a
			// link-name refusal that said only "not a valid name" would leave
			// the user guessing which.
			target, tRoot, terr := normalizeName(hdr.Linkname)
			if terr != nil {
				return rep, fmt.Errorf("entry %q: hardlink target %q: %w", hdr.Name, hdr.Linkname, terr)
			}
			if tRoot {
				return rep, fmt.Errorf("entry %q: hardlink target %q names the volume root, which is not a regular file", hdr.Name, hdr.Linkname)
			}
			if seen[target] != kindFile {
				return rep, fmt.Errorf("entry %q: hardlink target %q is not an earlier regular file of this payload", hdr.Name, hdr.Linkname)
			}
			seen[name] = kindHardlink
			if tw != nil {
				if err := writeRebuilt(tw, hdr, name, target); err != nil {
					return rep, err
				}
			}
		case tar.TypeFifo:
			rep.Dropped = append(rep.Dropped, Dropped{Name: name, Kind: "FIFO"})
			seen[name] = kindDropped
		case tar.TypeChar:
			rep.Dropped = append(rep.Dropped, Dropped{Name: name, Kind: "character device"})
			seen[name] = kindDropped
		case tar.TypeBlock:
			rep.Dropped = append(rep.Dropped, Dropped{Name: name, Kind: "block device"})
			seen[name] = kindDropped
		default:
			// Reached by every type flag archive/tar HANDS BACK. Two vendor
			// types never arrive here because the reader consumes them inside
			// Next and substitutes their content into the following header:
			// TypeGNULongName ('L') and TypeGNULongLink ('K'). A payload
			// carrying them is therefore accepted, with the substituted name
			// and link name judged by exactly the grammar and the ancestor
			// rules above, and the rebuilt stream spelling them as PAX records.
			// Refusing them would mean re-implementing tar's framing to see
			// blocks the library hides -- the one refusal ADR 0059 records as
			// unimplementable.
			return rep, fmt.Errorf("entry %q has type flag %q, which this format does not carry", hdr.Name, string(hdr.Typeflag))
		}
	}
}

// copyContent moves a regular entry's bytes: into the rebuilt stream when
// there is one, into the void when validating. Either way the content is
// consumed, so the next header is read from the right place and a truncated
// payload shows as a short entry rather than as a bad header.
func copyContent(tw *tar.Writer, tr io.Reader, hdr *tar.Header) error {
	var dst io.Writer = io.Discard
	if tw != nil {
		dst = tw
	}
	n, err := io.Copy(dst, tr)
	if err != nil {
		return fmt.Errorf("entry %q: %w", hdr.Name, err)
	}
	if n != hdr.Size {
		return fmt.Errorf("entry %q: %d of %d bytes present", hdr.Name, n, hdr.Size)
	}
	return nil
}

// writeRebuilt emits one rebuilt header, naming the entry if the writer
// refuses it (an unencodable link target, say) so the refusal reads like
// every other one here.
func writeRebuilt(tw *tar.Writer, hdr *tar.Header, name, linkname string) error {
	if err := tw.WriteHeader(rebuilt(hdr, name, linkname)); err != nil {
		return fmt.Errorf("entry %q cannot be rebuilt: %w", hdr.Name, err)
	}
	return nil
}

// rebuilt is the header the pour receives: name, type, link name, mode with
// the special bits zeroed, mtime to the nanosecond, size; ownership zero,
// names empty, no access or change times, PAX asked for so long names and
// nanosecond mtimes survive (Go's writer spells an entry that needs neither
// as plain USTAR, which is what FormatPAX means to it). The pour's
// --no-same-owner makes the zero ownership moot, and the chown that follows
// is the ownership step.
func rebuilt(hdr *tar.Header, name, linkname string) *tar.Header {
	size := hdr.Size
	if hdr.Typeflag != tar.TypeReg {
		size = 0
	}
	return &tar.Header{
		Typeflag: hdr.Typeflag,
		Name:     name,
		Linkname: linkname,
		Size:     size,
		Mode:     hdr.Mode & 0o777,
		ModTime:  hdr.ModTime,
		Format:   tar.FormatPAX,
	}
}

// headerOnlyType reports the type flags archive/tar gives no data section.
// It mirrors the reader's own isHeaderOnlyType (Go 1.27.1); TypeGNUSparse is
// absent from both and is refused earlier anyway.
func headerOnlyType(flag byte) bool {
	switch flag {
	case tar.TypeLink, tar.TypeSymlink, tar.TypeChar, tar.TypeBlock, tar.TypeDir, tar.TypeFifo:
		return true
	}
	return false
}

// normalizeName applies the Name grammar: a leading "./" dropped, a leading
// "/" refused, "..", "." and empty components refused, a trailing "/"
// removed. isRoot is true for GNU tar's "./" root header (and a bare ".").
func normalizeName(raw string) (name string, isRoot bool, err error) {
	n := raw
	if n == "." || n == "./" {
		return "", true, nil
	}
	n = strings.TrimPrefix(n, "./")
	if strings.HasPrefix(n, "/") {
		return "", false, errors.New("absolute name")
	}
	n = strings.TrimSuffix(n, "/")
	if n == "" {
		return "", false, errors.New("empty name")
	}
	for _, c := range strings.Split(n, "/") {
		switch c {
		case "":
			return "", false, errors.New("empty path component")
		case ".":
			return "", false, errors.New("\".\" path component")
		case "..":
			return "", false, errors.New("\"..\" path component")
		}
	}
	return n, false, nil
}

// ancestors lists the proper ancestors of a normalized name, nearest the
// root first.
func ancestors(name string) []string {
	var out []string
	for i, c := range name {
		if c == '/' {
			out = append(out, name[:i])
		}
	}
	return out
}

// classifyLink records a symlink in the report's lists when its target is
// absolute or lexically leaves the volume root. Lexical only: nothing is
// resolved, and a target that stays inside the root is not listed at all.
func classifyLink(rep *Report, name, target string) {
	if strings.HasPrefix(target, "/") {
		rep.Absolute = append(rep.Absolute, Link{Name: name, Target: target})
		return
	}
	joined := path.Clean(path.Join(path.Dir(name), target))
	if joined == ".." || strings.HasPrefix(joined, "../") {
		rep.Traversing = append(rep.Traversing, Link{Name: name, Target: target})
	}
}

// isSparse reports a GNU or PAX sparse entry. archive/tar expands holes on
// read, so the pour would materialise the full size; agent state holds
// none, and the contract refuses rather than guess at the expansion. Both
// spellings are covered: the old GNU 'S' type flag, and the GNU.sparse.*
// PAX records (0.0 is normalised to 0.1 by the reader, and 1.0 names itself
// in GNU.sparse.major), which mergePAX leaves on the header.
func isSparse(hdr *tar.Header) bool {
	if hdr.Typeflag == tar.TypeGNUSparse {
		return true
	}
	for k := range hdr.PAXRecords {
		if strings.HasPrefix(k, "GNU.sparse.") {
			return true
		}
	}
	return false
}

// EmptyArchive is a valid empty tar stream: two 512-byte zero blocks. The
// preflight feeds it to the pour command, because GNU tar exits 2 on zero
// bytes ("does not look like a tar archive") and 0 on this.
func EmptyArchive() []byte { return make([]byte, 1024) }
