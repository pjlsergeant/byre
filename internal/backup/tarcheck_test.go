package backup

import (
	"archive/tar"
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

// paxExtendedEntry hand-assembles a local PAX extended header ('x') carrying
// records archive/tar's WRITER refuses to emit: it drops every GNU.sparse.*
// record a caller hands it, so a PAX sparse entry has no other spelling.
func paxExtendedEntry(records ...string) entry {
	var body bytes.Buffer
	for _, rec := range records {
		// A PAX record's length field counts itself, so it is solved rather
		// than measured: n = len(" k=v\n") + len(decimal n).
		base := len(rec) + 2 // one space, one newline
		n := base + 1
		for len(fmt.Sprint(n))+base != n {
			n++
		}
		fmt.Fprintf(&body, "%d %s\n", n, rec)
	}
	hdr := rawEntry("PaxHeaders/x", tar.TypeXHeader, int64(body.Len()), "", 0o644, false)
	blocks := append([]byte(nil), hdr.raw...)
	padded := body.Bytes()
	for len(padded)%512 != 0 {
		padded = append(padded, 0)
	}
	return entry{raw: append(blocks, padded...)}
}

func TestNestedTarRefusesNameGrammar(t *testing.T) {
	cases := []struct {
		name      string
		entries   []entry
		fragments []string
	}{
		{"leading slash", []entry{fileEntry("/etc/shadow", "x", 0o644)}, []string{`"/etc/shadow"`, "absolute name"}},
		{"dotdot component", []entry{fileEntry("a/../b", "x", 0o644)}, []string{`"a/../b"`, `".." path component`}},
		{"dot component", []entry{fileEntry("a/./b", "x", 0o644)}, []string{`"a/./b"`, `"." path component`}},
		{"empty component", []entry{fileEntry("a//b", "x", 0o644)}, []string{`"a//b"`, "empty path component"}},
		{"duplicate plain name", []entry{fileEntry("f", "1", 0o644), fileEntry("f", "2", 0o644)}, []string{`"f"`, "appears twice"}},
		{"duplicate across trailing slash", []entry{dirEntry("d", 0o755), dirEntry("d/", 0o755)}, []string{`"d/"`, "appears twice"}},
		{"root twice", []entry{rootEntry(0o755, time.Unix(1, 0)), rootEntry(0o755, time.Unix(1, 0))}, []string{"volume root appears twice"}},
		{"root is not a directory", []entry{fileEntry(".", "x", 0o644)}, []string{"names the volume root but is not a directory"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Validate(bytes.NewReader(buildPayload(t, c.entries...)))
			wantRule(t, err, c.fragments...)
		})
	}
}

func TestNestedTarRefusesEntriesUnderNonDirectories(t *testing.T) {
	cases := []struct {
		name      string
		entries   []entry
		fragments []string
	}{
		{
			"under an earlier symlink",
			[]entry{symEntry("l", "/tmp"), fileEntry("l/x", "x", 0o644)},
			[]string{`"l/x"`, `under "l"`, "a symlink"},
		},
		{
			"under an earlier regular file",
			[]entry{fileEntry("f", "x", 0o644), fileEntry("f/y", "x", 0o644)},
			[]string{`"f/y"`, `under "f"`, "a regular file"},
		},
		{
			"under an earlier hardlink",
			[]entry{fileEntry("f", "x", 0o644), linkEntry("h", "f"), fileEntry("h/y", "x", 0o644)},
			[]string{`"h/y"`, `under "h"`, "a hardlink"},
		},
		{
			"under a dropped FIFO",
			[]entry{{hdr: tar.Header{Typeflag: tar.TypeFifo, Name: "p", Mode: 0o644}}, fileEntry("p/x", "x", 0o644)},
			[]string{`"p/x"`, `under "p"`, "not carried"},
		},
		{
			"symlink at a path implicit parents made a directory",
			[]entry{fileEntry("d/x", "x", 0o644), symEntry("d", "/tmp")},
			[]string{`"d"`, "already made a directory"},
		},
		{
			"file at a path implicit parents made a directory",
			[]entry{fileEntry("d/x", "x", 0o644), fileEntry("d", "y", 0o644)},
			[]string{`"d"`, "already made a directory"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Validate(bytes.NewReader(buildPayload(t, c.entries...)))
			wantRule(t, err, c.fragments...)
		})
	}
}

func TestNestedTarRefusesBadHardlinks(t *testing.T) {
	cases := []struct {
		name      string
		entries   []entry
		fragments []string
	}{
		{"missing target", []entry{linkEntry("h", "nope")}, []string{`"h"`, `"nope"`, "not an earlier regular file"}},
		{"non-regular target", []entry{dirEntry("d", 0o755), linkEntry("h", "d")}, []string{`"h"`, `"d"`, "not an earlier regular file"}},
		{"later target", []entry{linkEntry("h", "f"), fileEntry("f", "x", 0o644)}, []string{`"h"`, `"f"`, "not an earlier regular file"}},
		{"target with a bad name", []entry{linkEntry("h", "../outside")}, []string{`"h"`, `"../outside"`, `".." path component`}},
		{"target with an absolute name", []entry{linkEntry("h", "/etc/shadow")}, []string{`"h"`, `"/etc/shadow"`, "absolute name"}},
		{"target is the root", []entry{rootEntry(0o755, time.Unix(1, 0)), linkEntry("h", "./")}, []string{`"h"`, "names the volume root"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Validate(bytes.NewReader(buildPayload(t, c.entries...)))
			wantRule(t, err, c.fragments...)
		})
	}
}

func TestNestedTarRefusesKindsItDoesNotCarry(t *testing.T) {
	cases := []struct {
		name      string
		entries   []entry
		fragments []string
	}{
		{
			"old GNU sparse",
			[]entry{rawEntry("sp", tar.TypeGNUSparse, 0, "", 0o644, true)},
			[]string{`"sp"`, "sparse file"},
		},
		{
			"PAX GNU.sparse records",
			[]entry{
				paxExtendedEntry("GNU.sparse.numblocks=1", "GNU.sparse.map=0,4", "GNU.sparse.size=8"),
				rawEntry("sp", tar.TypeReg, 4, "", 0o644, false),
			},
			[]string{`"sp"`, "sparse file"},
		},
		{
			"socket",
			// No tar standard gives a socket a type flag; archive/tar reads
			// the kind out of the mode's file-type bits, which is the only
			// spelling a hand-made archive has.
			[]entry{rawEntry("s", tar.TypeReg, 0, "", 0o140644, false)},
			[]string{`"s"`, "is a socket"},
		},
		{
			"contiguous file",
			[]entry{rawEntry("c", tar.TypeCont, 0, "", 0o644, false)},
			[]string{`"c"`, `type flag "7"`},
		},
		{
			"unknown type flag",
			[]entry{rawEntry("u", 'Q', 0, "", 0o644, false)},
			[]string{`"u"`, `type flag "Q"`},
		},
		{
			"header-only type with content",
			[]entry{rawEntry("d/", tar.TypeDir, 512, "", 0o755, false)},
			[]string{`"d/"`, "512 bytes of content", "carries none"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Validate(bytes.NewReader(buildPayload(t, c.entries...)))
			wantRule(t, err, c.fragments...)
		})
	}
}

func TestNestedTarRefusesOverTheEntryCeiling(t *testing.T) {
	_, err := Validate(&manyHeaders{n: MaxEntries + 1})
	wantRule(t, err, fmt.Sprintf("more than %d entries", MaxEntries))
}

func TestNestedTarAcceptsTheEntryCeiling(t *testing.T) {
	rep, err := Validate(&manyHeaders{n: MaxEntries})
	if err != nil {
		t.Fatalf("MaxEntries entries refused: %v", err)
	}
	if rep.Entries != MaxEntries {
		t.Fatalf("Entries = %d, want %d", rep.Entries, MaxEntries)
	}
}

func TestNestedTarRefusesATruncatedPayload(t *testing.T) {
	raw := buildPayload(t, fileEntry("f", string(bytes.Repeat([]byte("x"), 900)), 0o644))
	_, err := Validate(bytes.NewReader(raw[:len(raw)-1200]))
	wantRule(t, err, `"f"`, "unexpected EOF")
}

// The accepted half of the contract, proven by what the REBUILT stream holds:
// the pour replays this, not the file's own bytes.
func TestRebuildReplaysTheContractedTree(t *testing.T) {
	rootTime := time.Unix(1700000001, 222333444)
	fileTime := time.Unix(1700000002, 555666777)
	dirTime := time.Unix(1700000003, 0)
	long := "deep/" + string(bytes.Repeat([]byte("n"), 160)) + "/memory.json"

	raw := buildPayload(t,
		rootEntry(0o1755, rootTime),
		entry{hdr: tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": "from the source"}, Format: tar.FormatPAX}},
		entry{hdr: tar.Header{Typeflag: tar.TypeDir, Name: "./d", Mode: 0o2755, ModTime: dirTime}},
		entry{hdr: tar.Header{Typeflag: tar.TypeDir, Name: "other/", Mode: 0o755, ModTime: dirTime}},
		entry{hdr: tar.Header{Typeflag: tar.TypeReg, Name: "./d/f", Mode: 0o4640, ModTime: fileTime, Uid: 4242, Gid: 4343, Uname: "someone", Gname: "somegroup"}, body: "hello"},
		entry{hdr: tar.Header{Typeflag: tar.TypeReg, Name: long, Mode: 0o644, ModTime: fileTime}, body: "{}"},
		symEntry("abs", "/etc/hosts"),
		symEntry("out", "../../elsewhere"),
		symEntry("in", "d/f"),
		linkEntry("hard", "./d/f"),
		entry{hdr: tar.Header{Typeflag: tar.TypeFifo, Name: "pipe", Mode: 0o644, ModTime: fileTime}},
		entry{hdr: tar.Header{Typeflag: tar.TypeChar, Name: "chr", Mode: 0o666, ModTime: fileTime, Devmajor: 1, Devminor: 3}},
		entry{hdr: tar.Header{Typeflag: tar.TypeBlock, Name: "blk", Mode: 0o660, ModTime: fileTime, Devmajor: 8, Devminor: 0}},
	)

	var out bytes.Buffer
	rep, err := Rebuild(bytes.NewReader(raw), &out)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// The global header is accepted, not counted; the root is not counted;
	// the three specials are dropped and still counted.
	// Eight entries reach the rebuilt stream, three specials are dropped;
	// the root header and the global PAX header are neither.
	if rep.Entries != 11 {
		t.Fatalf("Entries = %d, want 11 (root and the global header excluded, the three specials included)", rep.Entries)
	}
	if len(rep.Absolute) != 1 || rep.Absolute[0] != (Link{Name: "abs", Target: "/etc/hosts"}) {
		t.Fatalf("Absolute = %#v", rep.Absolute)
	}
	if len(rep.Traversing) != 1 || rep.Traversing[0] != (Link{Name: "out", Target: "../../elsewhere"}) {
		t.Fatalf("Traversing = %#v", rep.Traversing)
	}
	wantDropped := []Dropped{{"pipe", "FIFO"}, {"chr", "character device"}, {"blk", "block device"}}
	if fmt.Sprint(rep.Dropped) != fmt.Sprint(wantDropped) {
		t.Fatalf("Dropped = %#v, want %#v", rep.Dropped, wantDropped)
	}

	got := readTar(t, out.Bytes())
	type want struct {
		name, link, body string
		typeflag         byte
		mode             int64
		mtime            time.Time
	}
	wants := []want{
		{name: "./", typeflag: tar.TypeDir, mode: 0o755, mtime: rootTime},
		{name: "d/", typeflag: tar.TypeDir, mode: 0o755, mtime: dirTime},
		{name: "other/", typeflag: tar.TypeDir, mode: 0o755, mtime: dirTime},
		{name: "d/f", typeflag: tar.TypeReg, mode: 0o640, mtime: fileTime, body: "hello"},
		{name: long, typeflag: tar.TypeReg, mode: 0o644, mtime: fileTime, body: "{}"},
		{name: "abs", typeflag: tar.TypeSymlink, link: "/etc/hosts", mode: 0o777, mtime: time.Unix(1700000000, 0)},
		{name: "out", typeflag: tar.TypeSymlink, link: "../../elsewhere", mode: 0o777, mtime: time.Unix(1700000000, 0)},
		{name: "in", typeflag: tar.TypeSymlink, link: "d/f", mode: 0o777, mtime: time.Unix(1700000000, 0)},
		{name: "hard", typeflag: tar.TypeLink, link: "d/f", mode: 0o644, mtime: time.Unix(1700000000, 0)},
	}
	if len(got) != len(wants) {
		var names []string
		for _, g := range got {
			names = append(names, g.hdr.Name)
		}
		t.Fatalf("rebuilt %d entries %v, want %d", len(got), names, len(wants))
	}
	for i, w := range wants {
		g := got[i]
		if g.hdr.Name != w.name {
			t.Errorf("entry %d name = %q, want %q", i, g.hdr.Name, w.name)
		}
		if g.hdr.Typeflag != w.typeflag {
			t.Errorf("%q type = %q, want %q", w.name, string(g.hdr.Typeflag), string(w.typeflag))
		}
		if g.hdr.Linkname != w.link {
			t.Errorf("%q linkname = %q, want %q", w.name, g.hdr.Linkname, w.link)
		}
		if g.hdr.Mode != w.mode {
			t.Errorf("%q mode = %o, want %o (special bits zeroed, the rest kept)", w.name, g.hdr.Mode, w.mode)
		}
		if !g.hdr.ModTime.Equal(w.mtime) {
			t.Errorf("%q mtime = %v (%d ns), want %v", w.name, g.hdr.ModTime, g.hdr.ModTime.UnixNano(), w.mtime)
		}
		if g.body != w.body {
			t.Errorf("%q body = %q, want %q", w.name, g.body, w.body)
		}
		if g.hdr.Uid != 0 || g.hdr.Gid != 0 || g.hdr.Uname != "" || g.hdr.Gname != "" {
			t.Errorf("%q carries ownership %d:%d %q:%q; the pour's chown is the ownership step", w.name, g.hdr.Uid, g.hdr.Gid, g.hdr.Uname, g.hdr.Gname)
		}
		if len(g.hdr.PAXRecords) > 0 {
			if _, ok := g.hdr.PAXRecords["comment"]; ok {
				t.Errorf("%q carries the source's global PAX record", w.name)
			}
		}
	}
}

func TestRebuildReplaysAnEmptyRootOnlyPayload(t *testing.T) {
	rootTime := time.Unix(1700000009, 987654321)
	raw := buildPayload(t, rootEntry(0o700, rootTime))
	var out bytes.Buffer
	rep, err := Rebuild(bytes.NewReader(raw), &out)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if rep.Entries != 0 {
		t.Fatalf("Entries = %d, want 0 for a payload holding only the root header", rep.Entries)
	}
	got := readTar(t, out.Bytes())
	if len(got) != 1 || got[0].hdr.Name != "./" || got[0].hdr.Typeflag != tar.TypeDir {
		t.Fatalf("rebuilt %#v, want one \"./\" directory header", got)
	}
	if got[0].hdr.Mode != 0o700 || !got[0].hdr.ModTime.Equal(rootTime) {
		t.Fatalf("root mode %o mtime %v, want 0700 and %v", got[0].hdr.Mode, got[0].hdr.ModTime, rootTime)
	}
}

func TestRebuildAcceptsADirectoryClaimingAnImpliedPath(t *testing.T) {
	dirTime := time.Unix(1700000777, 0)
	raw := buildPayload(t,
		fileEntry("d/f", "x", 0o644),
		entry{hdr: tar.Header{Typeflag: tar.TypeDir, Name: "d", Mode: 0o750, ModTime: dirTime}},
	)
	var out bytes.Buffer
	rep, err := Rebuild(bytes.NewReader(raw), &out)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if rep.Entries != 2 {
		t.Fatalf("Entries = %d, want 2", rep.Entries)
	}
	got := readTar(t, out.Bytes())
	if len(got) != 2 || got[1].hdr.Name != "d/" {
		t.Fatalf("rebuilt %#v, want the file then \"d/\"", got)
	}
	if !got[1].hdr.ModTime.Equal(dirTime) {
		t.Fatalf("revisited directory mtime = %v, want the archived %v", got[1].hdr.ModTime, dirTime)
	}
}

func TestRebuildOutputPassesTheContractAgain(t *testing.T) {
	raw := buildPayload(t,
		rootEntry(0o755, time.Unix(1700000000, 1)),
		dirEntry("d", 0o755),
		fileEntry("d/f", "hello", 0o644),
		symEntry("l", "d/f"),
		linkEntry("h", "d/f"),
		entry{hdr: tar.Header{Typeflag: tar.TypeFifo, Name: "p", Mode: 0o644}},
	)
	var once bytes.Buffer
	first, err := Rebuild(bytes.NewReader(raw), &once)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	var twice bytes.Buffer
	second, err := Rebuild(bytes.NewReader(once.Bytes()), &twice)
	if err != nil {
		t.Fatalf("rebuilding the rebuilt stream: %v", err)
	}
	// The dropped FIFO is gone from the rebuilt stream, so the second pass
	// counts one fewer; everything else is a fixed point.
	if first.Entries != 5 || second.Entries != 4 {
		t.Fatalf("Entries = %d then %d, want 5 then 4", first.Entries, second.Entries)
	}
	if !bytes.Equal(once.Bytes(), twice.Bytes()) {
		t.Fatal("the rebuilt stream is not a fixed point of Rebuild")
	}
}

// gnuLongNameEntry is GNU tar's 'L' header: the real name as the content of a
// vendor entry, which archive/tar CONSUMES inside Next and substitutes into
// the following header. byre never sees the 'L' block, so the contract's
// "every other type flag refuses" cannot reach it; what it can do, and does,
// is judge the substituted name by the same grammar as any other.
func gnuLongNameEntry(name string, typeflag byte) entry {
	body := append([]byte(name), 0)
	hdr := rawEntry("././@LongLink", typeflag, int64(len(body)), "", 0o644, true)
	for len(body)%512 != 0 {
		body = append(body, 0)
	}
	return entry{raw: append(append([]byte(nil), hdr.raw...), body...)}
}

func TestNestedTarJudgesAGNULongNameByTheSameGrammar(t *testing.T) {
	long := "deep/" + strings.Repeat("n", 200) + "/memory.json"
	raw := buildPayload(t,
		gnuLongNameEntry(long, 'L'),
		rawEntry("truncated-name", tar.TypeReg, 0, "", 0o644, true),
	)
	var out bytes.Buffer
	rep, err := Rebuild(bytes.NewReader(raw), &out)
	if err != nil {
		t.Fatalf("a GNU long name is accepted with its substituted name: %v", err)
	}
	if rep.Entries != 1 {
		t.Fatalf("Entries = %d, want 1 (the 'L' block is not an entry)", rep.Entries)
	}
	got := readTar(t, out.Bytes())
	if len(got) != 1 || got[0].hdr.Name != long {
		t.Fatalf("rebuilt %#v, want the substituted long name", got)
	}

	// And the grammar is what judges it: a long name that escapes refuses,
	// exactly as the same name in a short header would.
	bad := buildPayload(t,
		gnuLongNameEntry("../"+strings.Repeat("e", 200), 'L'),
		rawEntry("short", tar.TypeReg, 0, "", 0o644, true),
	)
	_, err = Validate(bytes.NewReader(bad))
	wantRule(t, err, `".." path component`)
}

func TestNestedTarJudgesAGNULongLinkByTheSameGrammar(t *testing.T) {
	// A hardlink whose target rides a 'K' header: the substituted link name
	// goes through the Name grammar and the earlier-regular-file rule.
	bad := buildPayload(t,
		fileEntry("f", "x", 0o644),
		gnuLongLinkEntry("../"+strings.Repeat("e", 200)),
		rawEntry("h", tar.TypeLink, 0, "ignored", 0o644, true),
	)
	_, err := Validate(bytes.NewReader(bad))
	wantRule(t, err, `"h"`, `".." path component`)
}

func gnuLongLinkEntry(target string) entry { return gnuLongNameEntry(target, 'K') }

// A global PAX header's records are applied to nothing -- not to the next
// entry's path, not to its mtime, and not as a sparse declaration. The
// fixture carries all three of those operative records so acceptance cannot
// be mistaken for "the records were inert anyway".
func TestNestedTarIgnoresOperativeGlobalPAXRecords(t *testing.T) {
	fileTime := time.Unix(1700000002, 0)
	raw := buildPayload(t,
		entry{hdr: tar.Header{
			Typeflag: tar.TypeXGlobalHeader,
			Name:     "pax_global_header",
			PAXRecords: map[string]string{
				"path":             "../escape",
				"mtime":            "1",
				"GNU.sparse.major": "1",
				"comment":          "from the source",
			},
			Format: tar.FormatPAX,
		}},
		entry{hdr: tar.Header{Typeflag: tar.TypeReg, Name: "f", Mode: 0o644, ModTime: fileTime}, body: "kept"},
	)
	var out bytes.Buffer
	rep, err := Rebuild(bytes.NewReader(raw), &out)
	if err != nil {
		t.Fatalf("a global PAX header is accepted: %v", err)
	}
	if rep.Entries != 1 {
		t.Fatalf("Entries = %d, want 1 (a global header is not counted)", rep.Entries)
	}
	got := readTar(t, out.Bytes())
	if len(got) != 1 {
		t.Fatalf("rebuilt %d entries, want 1", len(got))
	}
	if got[0].hdr.Name != "f" {
		t.Fatalf("name = %q; the global header's path record was applied", got[0].hdr.Name)
	}
	if !got[0].hdr.ModTime.Equal(fileTime) {
		t.Fatalf("mtime = %v; the global header's mtime record was applied", got[0].hdr.ModTime)
	}
	if _, ok := got[0].hdr.PAXRecords["comment"]; ok {
		t.Fatal("the global header's records reached the rebuilt stream")
	}
	if _, ok := got[0].hdr.PAXRecords["GNU.sparse.major"]; ok {
		t.Fatal("the global header's sparse record reached the rebuilt stream")
	}
}
