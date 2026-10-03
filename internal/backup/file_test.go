package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/hostopen"
)

const testPrefix = "byre-demo-abc123-"

func testConfig() []byte {
	return []byte("base = \"debian:bookworm\"\n\n[env_from_host]\nTZ = \"host:TZ\"\n")
}

// volFixture is one carried volume as a test states it: a logical name and a
// nested payload built through the contract's own builders.
type volFixture struct {
	name string
	raw  []byte
}

func testVolumes(t *testing.T) []volFixture {
	t.Helper()
	return []volFixture{
		{name: ".claude", raw: buildPayload(t,
			rootEntry(0o755, time.Unix(1700000000, 123)),
			dirEntry("projects", 0o755),
			fileEntry("projects/memory.md", "remember this", 0o644),
			symEntry("out", "../elsewhere"),
		)},
		{name: ".grok", raw: buildPayload(t, rootEntry(0o700, time.Unix(1700000005, 0)))},
	}
}

func indexFor(t *testing.T, cfg []byte, vols []volFixture) Index {
	t.Helper()
	ix := Index{
		Format:         Format,
		ByreVersion:    "v1.12.0",
		MinByreVersion: MinByreVersion,
		Folder:         "demo",
		Engine:         "docker",
		Config:         ConfigRow{Bytes: int64(len(cfg)), SHA256: DigestBytes(cfg), Credentials: CredNone},
		References:     References{Base: "debian:bookworm"},
	}
	for _, v := range vols {
		rep, err := Validate(bytes.NewReader(v.raw))
		if err != nil {
			t.Fatalf("fixture payload %q does not pass the contract: %v", v.name, err)
		}
		ix.Volumes = append(ix.Volumes, Volume{
			Name:    v.name,
			Bytes:   int64(len(v.raw)),
			SHA256:  DigestBytes(v.raw),
			Entries: rep.Entries,
		})
	}
	return ix
}

func payloadsFor(vols []volFixture) []Payload {
	var out []Payload
	for _, v := range vols {
		raw := v.raw
		out = append(out, Payload{Name: v.name, Size: int64(len(raw)), Open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(raw)), nil
		}})
	}
	return out
}

// outerMember is one member of a FORGED outer archive: the refusal tests need
// shapes Write will not produce.
type outerMember struct {
	name string
	body []byte
}

func canonicalMembers(ix Index, cfg []byte, vols []volFixture) []outerMember {
	out := []outerMember{{IndexName, ix.Render()}, {ConfigName, cfg}}
	for _, v := range vols {
		out = append(out, outerMember{MemberName(v.name), v.raw})
	}
	return out
}

func gzTar(t *testing.T, members ...outerMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(gzTarPlain(t, members...)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// gzTarPlain is the uncompressed tar a forged fixture needs when it has to
// prepend its own blocks before compressing.
func gzTarPlain(t *testing.T, members ...outerMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, m := range members {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: m.name, Size: int64(len(m.body)), Mode: 0o600, Format: tar.FormatPAX}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(m.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readBackup(t *testing.T, raw []byte) (*File, *Staging, error) {
	t.Helper()
	id, err := hostopen.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	st := newTestStaging(t, t.TempDir(), id)
	f, err := Read(bytes.NewReader(raw), st, testPrefix)
	return f, st, err
}

func TestWriteThenReadRoundTrips(t *testing.T) {
	cfg := testConfig()
	vols := testVolumes(t)
	ix := indexFor(t, cfg, vols)
	out := filepath.Join(t.TempDir(), "demo-2026-10-02.byre-backup.tar.gz")

	if err := Write(out, testPrefix, ix, cfg, payloadsFor(vols), nil); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(out)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("published mode = %o, want 0600", fi.Mode().Perm())
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// A plain gzip tar: no envelope of byre's own around it.
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		t.Fatalf("published file does not start with the gzip magic: % x", raw[:2])
	}
	var names []string
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	want := []string{IndexName, ConfigName, "volumes/.claude.tar", "volumes/.grok.tar"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("members = %v, want %v", names, want)
	}

	f, st, err := readBackup(t, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.Config, cfg) {
		t.Fatalf("config round trip: got %q want %q", f.Config, cfg)
	}
	if f.CredentialState != CredNone {
		t.Fatalf("CredentialState = %q, want %q", f.CredentialState, CredNone)
	}
	if len(f.Volumes) != 2 {
		t.Fatalf("staged %d volumes, want 2", len(f.Volumes))
	}
	for i, v := range f.Volumes {
		if v.Name != vols[i].name {
			t.Fatalf("volume %d is %q, want %q", i, v.Name, vols[i].name)
		}
		got, err := io.ReadAll(mustOpen(t, st, v.Staged))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, vols[i].raw) {
			t.Fatalf("volume %q staged %d bytes, want the payload's %d", v.Name, len(got), len(vols[i].raw))
		}
		if v.Report.Entries != ix.Volumes[i].Entries {
			t.Fatalf("volume %q report says %d entries, index says %d", v.Name, v.Report.Entries, ix.Volumes[i].Entries)
		}
	}
	// The report is derived from the payload, so the traversing symlink is
	// listed whatever the index says about it.
	if len(f.Volumes[0].Report.Traversing) != 1 {
		t.Fatalf("Traversing = %#v, want the one traversing target", f.Volumes[0].Report.Traversing)
	}

	// The pour replays the rebuilt stream, not the file's bytes.
	var poured bytes.Buffer
	rep, err := f.RebuildTo(f.Volumes[0], st, &poured)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries != f.Volumes[0].Report.Entries {
		t.Fatalf("RebuildTo reported %d entries, verification reported %d", rep.Entries, f.Volumes[0].Report.Entries)
	}
	if _, err := Validate(bytes.NewReader(poured.Bytes())); err != nil {
		t.Fatalf("the rebuilt stream does not pass the contract: %v", err)
	}
}

func mustOpen(t *testing.T, st *Staging, name string) io.Reader {
	t.Helper()
	f, err := st.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestReadConfigOnlyBackup(t *testing.T) {
	cfg := testConfig()
	ix := indexFor(t, cfg, nil)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := Write(out, testPrefix, ix, cfg, nil, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	f, _, err := readBackup(t, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Volumes) != 0 || !bytes.Equal(f.Config, cfg) {
		t.Fatalf("a project with no volumes should back up its config alone: %#v", f)
	}
}

func TestReadRefusals(t *testing.T) {
	cfg := testConfig()
	vols := testVolumes(t)
	ix := indexFor(t, cfg, vols)
	canonical := canonicalMembers(ix, cfg, vols)

	flip := func(b []byte) []byte {
		out := append([]byte(nil), b...)
		out[len(out)/2] ^= 0xff
		return out
	}

	cases := []struct {
		name      string
		build     func(t *testing.T) []byte
		fragments []string
	}{
		{
			"config digest mismatch",
			func(t *testing.T) []byte {
				m := append([]outerMember(nil), canonical...)
				m[1] = outerMember{ConfigName, flip(cfg)}
				return gzTar(t, m...)
			},
			[]string{`"byre.config"`, "has sha256", ix.Config.SHA256},
		},
		{
			"volume digest mismatch",
			func(t *testing.T) []byte {
				m := append([]outerMember(nil), canonical...)
				m[2] = outerMember{MemberName(".claude"), flip(vols[0].raw)}
				return gzTar(t, m...)
			},
			[]string{`"volumes/.claude.tar"`, "has sha256", ix.Volumes[0].SHA256},
		},
		{
			"members out of order",
			func(t *testing.T) []byte {
				return gzTar(t, canonical[0], canonical[2], canonical[1], canonical[3])
			},
			[]string{"expected member", `"byre.config"`, `"volumes/.claude.tar"`},
		},
		{
			"an extra member",
			func(t *testing.T) []byte {
				m := append([]outerMember(nil), canonical...)
				m = append(m, outerMember{"volumes/.extra.tar", vols[1].raw})
				return gzTar(t, m...)
			},
			[]string{"unexpected extra member", `"volumes/.extra.tar"`},
		},
		{
			"a missing member",
			func(t *testing.T) []byte {
				return gzTar(t, canonical[0], canonical[1], canonical[2])
			},
			[]string{"ends before member", `"volumes/.grok.tar"`},
		},
		{
			"a duplicate member",
			func(t *testing.T) []byte {
				return gzTar(t, canonical[0], canonical[1], canonical[2], canonical[2], canonical[3])
			},
			[]string{"expected member", `"volumes/.grok.tar"`, `"volumes/.claude.tar"`},
		},
		{
			"a member name that is not the row's",
			func(t *testing.T) []byte {
				m := append([]outerMember(nil), canonical...)
				m[2] = outerMember{"volumes/.cladue.tar", vols[0].raw}
				return gzTar(t, m...)
			},
			[]string{"expected member", `"volumes/.claude.tar"`, `"volumes/.cladue.tar"`},
		},
		{
			"a member that is not a regular file",
			func(t *testing.T) []byte {
				var buf bytes.Buffer
				zw := gzip.NewWriter(&buf)
				tw := tar.NewWriter(zw)
				if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: IndexName, Linkname: "/etc/passwd", Mode: 0o777, Format: tar.FormatPAX}); err != nil {
					t.Fatal(err)
				}
				tw.Close()
				zw.Close()
				return buf.Bytes()
			},
			[]string{`"backup.toml"`, "not a regular file"},
		},
		{
			"trailing data after the gzip member",
			func(t *testing.T) []byte {
				return append(gzTar(t, canonical...), []byte("and one more thing")...)
			},
			[]string{"data follows the gzip member"},
		},
		{
			"a second gzip member",
			func(t *testing.T) []byte {
				return append(gzTar(t, canonical...), gzTar(t, canonical...)...)
			},
			[]string{"data follows the gzip member"},
		},
		{
			"an entry count the payload does not have",
			func(t *testing.T) []byte {
				bad := ix
				bad.Volumes = append([]Volume(nil), ix.Volumes...)
				bad.Volumes[0].Entries = 99
				m := append([]outerMember(nil), canonical...)
				m[0] = outerMember{IndexName, bad.Render()}
				return gzTar(t, m...)
			},
			[]string{`".claude"`, "holds 3 entries but the index says 99"},
		},
		{
			"a forged index naming a volume the archive does not carry",
			func(t *testing.T) []byte {
				bad := ix
				bad.Volumes = append([]Volume(nil), ix.Volumes...)
				bad.Volumes[0].Name = ".codex"
				m := append([]outerMember(nil), canonical...)
				m[0] = outerMember{IndexName, bad.Render()}
				return gzTar(t, m...)
			},
			[]string{"expected member", `"volumes/.codex.tar"`},
		},
		{
			"not a gzip file at all",
			func(t *testing.T) []byte { return []byte("plain text, not a backup") },
			[]string{"not a gzip file"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := readBackup(t, c.build(t))
			wantRule(t, err, c.fragments...)
		})
	}
}

// The decompressed budget has to fire BEFORE the member-name comparison, or a
// few kilobytes of archive could ask the reader for an unbounded PAX header.
func TestReadRefusesOverTheDecompressedBudget(t *testing.T) {
	cfg := testConfig()
	vols := testVolumes(t)
	ix := indexFor(t, cfg, vols[:1])
	huge := "volumes/" + strings.Repeat("n", 300<<10) + ".tar"
	raw := gzTar(t,
		outerMember{IndexName, ix.Render()},
		outerMember{ConfigName, cfg},
		outerMember{huge, vols[0].raw},
	)
	_, _, err := readBackup(t, raw)
	wantRule(t, err, "over the declared totals")
}

// And the pre-index phase has a bound of its own: an index member larger than
// a config can be is refused by its header, before it is read.
func TestReadRefusesAnOversizeIndexMember(t *testing.T) {
	raw := gzTar(t, outerMember{IndexName, bytes.Repeat([]byte("#"), (1<<20)+1)})
	_, _, err := readBackup(t, raw)
	wantRule(t, err, `"backup.toml"`, "1048577 bytes", "limit 1048576")
}

// Free space is checked against the DECLARED totals before a payload is
// staged, so the refusal names the shortfall rather than arriving after the
// disk is full.
func TestReadRefusesWhenStagingCannotHoldTheDeclaredTotals(t *testing.T) {
	cfg := testConfig()
	vols := testVolumes(t)
	ix := indexFor(t, cfg, vols)
	ix.Volumes[0].Bytes = 1 << 55
	raw := gzTar(t, outerMember{IndexName, ix.Render()}, outerMember{ConfigName, cfg})

	id, err := hostopen.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	st := newTestStaging(t, t.TempDir(), id)
	_, err = Read(bytes.NewReader(raw), st, testPrefix)
	wantRule(t, err, "short by", st.Path())

	entries, err := os.ReadDir(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging holds %d files; the budget must refuse before staging", len(entries))
	}
}

func TestReadRefusesAPayloadThatFailsTheContract(t *testing.T) {
	cfg := testConfig()
	bad := buildPayload(t, fileEntry("../escape", "x", 0o644))
	vols := []volFixture{{name: ".claude", raw: bad}}
	ix := Index{
		Format:         Format,
		ByreVersion:    "v1.12.0",
		MinByreVersion: MinByreVersion,
		Folder:         "demo",
		Engine:         "docker",
		Config:         ConfigRow{Bytes: int64(len(cfg)), SHA256: DigestBytes(cfg), Credentials: CredNone},
		Volumes:        []Volume{{Name: ".claude", Bytes: int64(len(bad)), SHA256: DigestBytes(bad), Entries: 1}},
		References:     References{Base: "debian:bookworm"},
	}
	_, _, err := readBackup(t, gzTar(t, canonicalMembers(ix, cfg, vols)...))
	wantRule(t, err, `volume ".claude"`, `".." path component`)
}

// "one whose index misstates the credential state shows the derived state,
// not the index's" -- a refusal here would be the wrong answer.
func TestReadShowsTheDerivedCredentialStateOverTheIndex(t *testing.T) {
	cfg := testConfig()
	ix := indexFor(t, cfg, nil)
	ix.Config.Credentials = CredRows
	raw := gzTar(t, outerMember{IndexName, ix.Render()}, outerMember{ConfigName, cfg})
	f, _, err := readBackup(t, raw)
	if err != nil {
		t.Fatalf("a misstated credential state is not a refusal: %v", err)
	}
	if f.CredentialState != CredNone {
		t.Fatalf("CredentialState = %q, want the derived %q", f.CredentialState, CredNone)
	}
	if f.Index.Config.Credentials != CredRows {
		t.Fatal("the index's own claim should still be readable, as what the source said")
	}
}

func TestWriteRefusesBeforePublishing(t *testing.T) {
	cfg := testConfig()
	vols := testVolumes(t)

	cases := []struct {
		name      string
		mutate    func(ix *Index, p []Payload) []Payload
		fragments []string
	}{
		{
			"a payload shorter than its row",
			func(ix *Index, p []Payload) []Payload {
				p[0].Open = func() (io.ReadCloser, error) {
					return io.NopCloser(bytes.NewReader(vols[0].raw[:10])), nil
				}
				return p
			},
			[]string{`".claude"`, "streamed 10 bytes but the index says"},
		},
		{
			"a payload whose digest is not its row's",
			func(ix *Index, p []Payload) []Payload {
				other := append([]byte(nil), vols[0].raw...)
				other[100] ^= 0xff
				p[0].Open = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(other)), nil }
				return p
			},
			[]string{`".claude"`, "has sha256"},
		},
		{
			"a payload list that does not match the rows",
			func(ix *Index, p []Payload) []Payload { return p[:1] },
			[]string{"1 payloads for 2 index volume rows"},
		},
		{
			"a payload out of the index's order",
			func(ix *Index, p []Payload) []Payload { return []Payload{p[1], p[0]} },
			[]string{"payload 0 is volume", `".grok"`, `".claude"`},
		},
		{
			"a config the index does not describe",
			func(ix *Index, p []Payload) []Payload { ix.Config.Bytes = 3; return p },
			[]string{"byre.config", "but the index says 3"},
		},
		{
			"a config digest the index does not state",
			func(ix *Index, p []Payload) []Payload { ix.Config.SHA256 = shaA; return p },
			[]string{"byre.config", "has sha256", shaA},
		},
		{
			"an index that fails its own bounds",
			func(ix *Index, p []Payload) []Payload { ix.Volumes[0].Name = ".."; return p },
			[]string{IndexName, `".."`, "is not a name"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ix := indexFor(t, cfg, vols)
			out := filepath.Join(t.TempDir(), "b.tar.gz")
			err := Write(out, testPrefix, ix, cfg, c.mutate(&ix, payloadsFor(vols)), nil)
			wantRule(t, err, c.fragments...)
			if _, statErr := os.Lstat(out); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("something was published at the output path: %v", statErr)
			}
		})
	}
}

func TestWriteRefusesAnExistingOutputEntry(t *testing.T) {
	cfg := testConfig()
	vols := testVolumes(t)
	ix := indexFor(t, cfg, vols)

	t.Run("a file", func(t *testing.T) {
		dir := t.TempDir()
		out := filepath.Join(dir, "taken")
		if err := os.WriteFile(out, []byte("the user's own file"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := Write(out, testPrefix, ix, cfg, payloadsFor(vols), nil)
		if !errors.Is(err, fs.ErrExist) {
			t.Fatalf("err = %v, want fs.ErrExist", err)
		}
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "the user's own file" {
			t.Fatalf("the existing file was touched: %q", got)
		}
	})
	t.Run("a directory", func(t *testing.T) {
		dir := t.TempDir()
		out := filepath.Join(dir, "taken")
		if err := os.Mkdir(out, 0o755); err != nil {
			t.Fatal(err)
		}
		err := Write(out, testPrefix, ix, cfg, payloadsFor(vols), nil)
		if !errors.Is(err, fs.ErrExist) {
			t.Fatalf("err = %v, want fs.ErrExist", err)
		}
		if fi, err := os.Lstat(out); err != nil || !fi.IsDir() {
			t.Fatalf("the existing directory was replaced: %v %v", fi, err)
		}
	})
	t.Run("a symlink", func(t *testing.T) {
		dir := t.TempDir()
		out := filepath.Join(dir, "taken")
		if err := os.Symlink(filepath.Join(dir, "elsewhere"), out); err != nil {
			t.Fatal(err)
		}
		err := Write(out, testPrefix, ix, cfg, payloadsFor(vols), nil)
		if !errors.Is(err, fs.ErrExist) {
			t.Fatalf("err = %v, want fs.ErrExist", err)
		}
		fi, err := os.Lstat(out)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("the symlink was replaced: %v %v", fi, err)
		}
		if _, err := os.Lstat(filepath.Join(dir, "elsewhere")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatal("the publish followed the symlink and wrote its target")
		}
	})
}

func TestWriteRefusesAMissingOutputParent(t *testing.T) {
	cfg := testConfig()
	vols := testVolumes(t)
	ix := indexFor(t, cfg, vols)
	out := filepath.Join(t.TempDir(), "no-such-dir", "b.tar.gz")
	if err := Write(out, testPrefix, ix, cfg, payloadsFor(vols), nil); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
	if _, err := os.Lstat(filepath.Dir(out)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the missing parent was created")
	}
}

// A format-1 index may legally approach the config bound. The budget's second
// phase has to admit the index it just read, or a big-but-legal file would be
// refused for being what it says it is.
func TestReadAcceptsALargeButLegalIndex(t *testing.T) {
	cfg := testConfig()
	ix := indexFor(t, cfg, nil)
	ix.Folder = strings.Repeat("f", 900<<10)
	raw := ix.Render()
	if len(raw) < 900<<10 {
		t.Fatalf("fixture index is only %d bytes", len(raw))
	}
	f, _, err := readBackup(t, gzTar(t, outerMember{IndexName, raw}, outerMember{ConfigName, cfg}))
	if err != nil {
		t.Fatal(err)
	}
	if f.Index.Folder != ix.Folder {
		t.Fatal("the index did not round trip")
	}
}

// A stream of zero-length local PAX headers is the shape that defeats a soft
// budget: archive/tar consumes each one and loops, and every header block is
// read with io.ReadFull, which swallows a read error whenever the bytes it got
// filled the buffer. A few compressed kilobytes must not expand without end.
func TestReadRefusesAFloodOfEmptyPAXHeaders(t *testing.T) {
	var plain bytes.Buffer
	block := rawEntry("PaxHeaders/x", tar.TypeXHeader, 0, "", 0o644, false)
	for plain.Len() < (1<<20)+(256<<10) {
		plain.Write(block.raw)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(plain.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() > 64<<10 {
		t.Fatalf("fixture is %d compressed bytes; the point is that it is small", buf.Len())
	}
	_, _, err := readBackup(t, buf.Bytes())
	wantRule(t, err, "over the index bound")
}

// Many members with long names cost three header blocks each. Write and Read
// have to agree about that, or backup publishes files restore refuses.
func TestWriteThenReadManyLongNamedVolumes(t *testing.T) {
	cfg := testConfig()
	empty := buildPayload(t, rootEntry(0o755, time.Unix(1700000000, 0)))
	var vols []volFixture
	for i := 0; i < 200; i++ {
		name := strings.Repeat("v", 189) + string(rune('a'+i/26)) + string(rune('a'+i%26))
		vols = append(vols, volFixture{name: name, raw: empty})
	}
	ix := indexFor(t, cfg, vols)
	if err := CheckIndex(ix, testPrefix); err != nil {
		t.Fatalf("fixture index does not pass its own bounds: %v", err)
	}
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := Write(out, testPrefix, ix, cfg, payloadsFor(vols), nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	f, _, err := readBackup(t, raw)
	if err != nil {
		t.Fatalf("a file byre wrote was refused on read: %v", err)
	}
	if len(f.Volumes) != len(vols) {
		t.Fatalf("read %d volumes, wrote %d", len(f.Volumes), len(vols))
	}
}

func TestWriteRefusesAnIndexOverTheConfigBound(t *testing.T) {
	cfg := testConfig()
	ix := indexFor(t, cfg, nil)
	ix.Folder = strings.Repeat("f", (1<<20)+1)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	err := Write(out, testPrefix, ix, cfg, nil, nil)
	wantRule(t, err, IndexName, "renders to", "limit 1048576")
	if _, statErr := os.Lstat(out); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("something was published: %v", statErr)
	}
}

// countingReader is how much a reader actually pulled from the stream behind
// it: the budget's promise is a bound on exactly that.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

type endlessZeros struct{}

func (endlessZeros) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// The budget is a HARD bound, and it has to survive io.ReadFull, which is how
// archive/tar reads every header block and which discards a read's error
// whenever the bytes it got filled the buffer. A budget that merely reported
// the overrun would be ignored once per 512-byte block, so a few compressed
// kilobytes of repeated headers could expand without end.
func TestBudgetSurvivesReadFullAndBoundsTheSource(t *testing.T) {
	src := &countingReader{r: endlessZeros{}}
	b := &budget{r: src, label: "index bound", limit: 4096}
	buf := make([]byte, 512)
	var err error
	for i := 0; i < 1000; i++ {
		if _, err = io.ReadFull(b, buf); err != nil {
			break
		}
	}
	wantRule(t, err, "over the index bound", "limit 4096")
	if src.n > 4097 {
		t.Fatalf("the source gave up %d bytes under a limit of 4096", src.n)
	}
}

// A stream that lands exactly on the limit still reaches its own EOF: the
// bound must not turn a legal file's final zero-byte read into a refusal.
func TestBudgetAdmitsAStreamThatLandsOnTheLimit(t *testing.T) {
	b := &budget{r: bytes.NewReader(make([]byte, 4096)), label: "declared totals", limit: 4096}
	n, err := io.Copy(io.Discard, b)
	if err != nil {
		t.Fatalf("a stream exactly at the limit was refused: %v", err)
	}
	if n != 4096 {
		t.Fatalf("read %d bytes, want 4096", n)
	}
}

// The second-phase limit can be SMALLER than the first phase's: a tiny
// archive padded with meta headers arrives at the index having already spent
// more than the index then declares. That has to refuse, not slice a buffer
// with a negative bound.
func TestReadRefusesAnArchivePaddedBeforeItsIndex(t *testing.T) {
	cfg := testConfig()
	ix := indexFor(t, cfg, nil)
	var plain bytes.Buffer
	block := rawEntry("PaxHeaders/x", tar.TypeXHeader, 0, "", 0o644, false)
	for i := 0; i < 200; i++ {
		plain.Write(block.raw)
	}
	inner := gzTarPlain(t, outerMember{IndexName, ix.Render()}, outerMember{ConfigName, cfg})
	plain.Write(inner)

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(plain.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, err := readBackup(t, buf.Bytes())
	wantRule(t, err, "over the declared totals")
}

// A forged index may declare totals that would wrap the budget's sum. The
// limit saturates instead, so the refusal is the free-space one that names
// the shortfall -- never a panic.
func TestReadRefusesTotalsThatWouldWrapTheBudget(t *testing.T) {
	ix := Index{
		Format:         Format,
		ByreVersion:    "v1.12.0",
		MinByreVersion: MinByreVersion,
		Folder:         "demo",
		Engine:         "docker",
		Config:         ConfigRow{Bytes: 0, SHA256: DigestBytes(nil), Credentials: CredNone},
		Volumes:        []Volume{{Name: ".claude", Bytes: math.MaxInt64, SHA256: shaB, Entries: 0}},
		References:     References{Base: "debian:bookworm"},
	}
	if err := CheckIndex(ix, testPrefix); err != nil {
		t.Fatalf("the forged index is meant to pass CheckIndex: %v", err)
	}
	raw := gzTar(t, outerMember{IndexName, ix.Render()}, outerMember{ConfigName, nil})
	_, _, err := readBackup(t, raw)
	wantRule(t, err, "short by")
}

func TestReadBudgetSaturatesRatherThanWrapping(t *testing.T) {
	if got := readBudget(math.MaxInt64, math.MaxInt64, 1<<20); got <= 0 {
		t.Fatalf("readBudget = %d, want a positive saturated limit", got)
	}
	if got := readBudget(0, math.MaxInt64, 0); got != math.MaxInt64-1 {
		t.Fatalf("readBudget = %d, want MaxInt64-1", got)
	}
}

// ------------------------------------------------- the pour's second reading

// The pour's second pass is the only reading the helper's bytes come from, so
// it re-checks the staged payload against BOTH things Read established: the
// whole report, and the index row's digest. Staging is this invocation's
// private 0700 directory, so any difference is a failed pour -- these arms
// swap the staged bytes under byre to prove each check fires.
//
// The rebuilt stream is written as it goes, so a refusal here can come after
// the writer has seen bytes (a digest is only knowable at the end). The
// contract is that RebuildTo RETURNS an error, which restore treats as a
// failed pour and rolls back; what reached the writer is not asserted.
func TestRebuildToRefusesAStagedPayloadThatChanged(t *testing.T) {
	for _, tc := range []struct {
		name    string
		swapped []byte
		want    string
	}{
		{
			// Same entry count, same kinds, a different symlink TARGET: no
			// count moves, so only a deep comparison of the report sees it.
			name: "a symlink target changed",
			swapped: buildPayload(t,
				rootEntry(0o755, time.Unix(1700000000, 0)),
				fileEntry("memory.md", "remember this", 0o644),
				symEntry("out", "../somewhere-else"),
				entry{hdr: tar.Header{Typeflag: tar.TypeFifo, Name: "pipe", Mode: 0o644}},
			),
			want: "is not the payload byre verified",
		},
		{
			// Same count, same kinds, same targets: one content byte. Only the
			// digest can see this one.
			name: "a regular file's bytes changed",
			swapped: buildPayload(t,
				rootEntry(0o755, time.Unix(1700000000, 0)),
				fileEntry("memory.md", "remember THAT!", 0o644),
				symEntry("out", "../elsewhere"),
				entry{hdr: tar.Header{Typeflag: tar.TypeFifo, Name: "pipe", Mode: 0o644}},
			),
			want: "the staged payload changed under byre",
		},
		{
			// The FIFO the review listed as dropped is now a regular file the
			// pour would write. Entries counts dropped entries too, so the
			// count is unmoved.
			name: "a dropped FIFO became a regular file",
			swapped: buildPayload(t,
				rootEntry(0o755, time.Unix(1700000000, 0)),
				fileEntry("memory.md", "remember this", 0o644),
				symEntry("out", "../elsewhere"),
				fileEntry("pipe", "", 0o644),
			),
			want: "is not the payload byre verified",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			vols := []volFixture{{name: ".claude", raw: buildPayload(t,
				rootEntry(0o755, time.Unix(1700000000, 0)),
				fileEntry("memory.md", "remember this", 0o644),
				symEntry("out", "../elsewhere"),
				entry{hdr: tar.Header{Typeflag: tar.TypeFifo, Name: "pipe", Mode: 0o644}},
			)}}
			out := filepath.Join(t.TempDir(), "b.tar.gz")
			if err := Write(out, testPrefix, indexFor(t, cfg, vols), cfg, payloadsFor(vols), nil); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			f, st, err := readBackup(t, raw)
			if err != nil {
				t.Fatal(err)
			}
			// The entry count must be the one thing that did NOT move, or the
			// arm would pass on the old check too.
			rep, err := Validate(bytes.NewReader(tc.swapped))
			if err != nil {
				t.Fatal(err)
			}
			if rep.Entries != f.Volumes[0].Report.Entries {
				t.Fatalf("the swapped payload holds %d entries and the verified one %d; this arm must keep the count still",
					rep.Entries, f.Volumes[0].Report.Entries)
			}
			// Swapped under byre, through the staging directory's own path --
			// exactly what the second reading exists to catch.
			if err := os.WriteFile(filepath.Join(st.Path(), f.Volumes[0].Staged), tc.swapped, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = f.RebuildTo(f.Volumes[0], st, io.Discard)
			if err == nil {
				t.Fatal("RebuildTo poured a payload that changed after it was verified")
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), ".claude") {
				t.Fatalf("the refusal must name the rule and the volume, got %v", err)
			}
		})
	}
}
