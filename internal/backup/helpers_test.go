package backup

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// Payload builders. Every refusal in the nested-tar contract is built from a
// real stream: archive/tar's own writer where it can spell the shape, and a
// hand-assembled 512-byte header where it cannot (a socket's mode bits, a
// contiguous-file or unknown type flag, a GNU sparse header). Both are here
// so no test re-derives the checksum rule.

// entry is one header (plus content for a regular file) in a built payload.
type entry struct {
	hdr  tar.Header
	body string
	// raw, when set, is a hand-assembled header block written instead of
	// anything archive/tar would produce.
	raw []byte
}

// buildPayload spells a nested payload. Entries with a raw block are written
// through the tar writer's own stream after flushing it, so a hand-made
// header and a generated one can sit in one archive.
func buildPayload(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		if e.raw != nil {
			if err := tw.Flush(); err != nil {
				t.Fatal(err)
			}
			if _, err := buf.Write(e.raw); err != nil {
				t.Fatal(err)
			}
			continue
		}
		h := e.hdr
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if h.Format == 0 {
			h.Format = tar.FormatPAX
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("writing %q: %v", h.Name, err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// rootEntry is GNU tar's "./" volume-root header.
func rootEntry(mode int64, mtime time.Time) entry {
	return entry{hdr: tar.Header{Typeflag: tar.TypeDir, Name: "./", Mode: mode, ModTime: mtime}}
}

func fileEntry(name, body string, mode int64) entry {
	return entry{hdr: tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode, ModTime: time.Unix(1700000000, 0)}, body: body}
}

func dirEntry(name string, mode int64) entry {
	return entry{hdr: tar.Header{Typeflag: tar.TypeDir, Name: name, Mode: mode, ModTime: time.Unix(1700000000, 0)}}
}

func symEntry(name, target string) entry {
	return entry{hdr: tar.Header{Typeflag: tar.TypeSymlink, Name: name, Linkname: target, Mode: 0o777, ModTime: time.Unix(1700000000, 0)}}
}

func linkEntry(name, target string) entry {
	return entry{hdr: tar.Header{Typeflag: tar.TypeLink, Name: name, Linkname: target, Mode: 0o644, ModTime: time.Unix(1700000000, 0)}}
}

// rawEntry hand-assembles one 512-byte tar header with a correct checksum,
// for the shapes archive/tar's writer refuses to produce. gnu selects the
// GNU magic/version, which the reader requires before it will read an old
// GNU sparse map at all.
func rawEntry(name string, typeflag byte, size int64, link string, mode int64, gnu bool) entry {
	var blk [512]byte
	copy(blk[0:100], name)
	copy(blk[100:108], fmt.Sprintf("%07o\x00", mode))
	copy(blk[108:116], fmt.Sprintf("%07o\x00", 0))
	copy(blk[116:124], fmt.Sprintf("%07o\x00", 0))
	copy(blk[124:136], fmt.Sprintf("%011o\x00", size))
	copy(blk[136:148], fmt.Sprintf("%011o\x00", 0))
	blk[156] = typeflag
	copy(blk[157:257], link)
	if gnu {
		copy(blk[257:263], "ustar ")
		copy(blk[263:265], " \x00")
	} else {
		copy(blk[257:263], "ustar\x00")
		copy(blk[263:265], "00")
	}
	for i := 148; i < 156; i++ {
		blk[i] = ' '
	}
	sum := 0
	for _, c := range blk {
		sum += int(c)
	}
	copy(blk[148:156], fmt.Sprintf("%06o\x00 ", sum))
	out := make([]byte, 512)
	copy(out, blk[:])
	return entry{raw: out}
}

// manyHeaders streams n zero-length regular entries lazily, so the
// MaxEntries ceiling is exercised without half a gigabyte in memory. The
// header block is built once and only its name and checksum are rewritten,
// so a million entries cost a million checksum sums and no allocation.
type manyHeaders struct {
	n, i  int
	blk   [512]byte
	buf   []byte
	ready bool
	done  bool
}

func (m *manyHeaders) Read(p []byte) (int, error) {
	for len(m.buf) == 0 {
		switch {
		case m.i < m.n:
			if !m.ready {
				copy(m.blk[:], rawEntry("f00000000", tar.TypeReg, 0, "", 0o644, false).raw)
				m.ready = true
			}
			// Nine fixed-width digits, so every name is the same length and
			// the block's layout never shifts.
			v := m.i
			for d := 8; d >= 0; d-- {
				m.blk[1+d] = byte('0' + v%10)
				v /= 10
			}
			for j := 148; j < 156; j++ {
				m.blk[j] = ' '
			}
			sum := 0
			for _, c := range m.blk {
				sum += int(c)
			}
			copy(m.blk[148:156], fmt.Sprintf("%06o\x00 ", sum))
			m.buf = m.blk[:]
			m.i++
		case !m.done:
			m.buf = make([]byte, 1024)
			m.done = true
		default:
			return 0, io.EOF
		}
	}
	n := copy(p, m.buf)
	m.buf = m.buf[n:]
	return n, nil
}

// readTar lists a rebuilt stream's headers in order, with each regular
// entry's content, so a test can assert what the pour will replay.
type rebuiltEntry struct {
	hdr  tar.Header
	body string
}

func readTar(t *testing.T, raw []byte) []rebuiltEntry {
	t.Helper()
	var out []rebuiltEntry
	tr := tar.NewReader(bytes.NewReader(raw))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("reading the rebuilt stream: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("reading %q: %v", h.Name, err)
		}
		out = append(out, rebuiltEntry{hdr: *h, body: string(body)})
	}
}

// wantRule asserts that the rule a test is about is the rule that fired: a
// stable identifying fragment plus the offending value. A dozen rules can
// refuse one payload, so a test that asserted only "an error" would stay
// green on the wrong refusal.
func wantRule(t *testing.T, err error, fragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error; wanted the rule naming %q", fragments)
	}
	for _, f := range fragments {
		if !strings.Contains(err.Error(), f) {
			t.Fatalf("error %q does not name %q", err, f)
		}
	}
}
