package deliver

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- flow tests (fake engine) ---

func grabBox() *fakeEngine {
	eng := box("docker", "aaa")
	eng.boxfs = map[string]string{"/workspace/out/report.pdf": "PDFBYTES"}
	eng.boxdirs = []string{"/workspace", "/workspace/out"}
	return eng
}

func TestGrabFileIntoDirectory(t *testing.T) {
	eng := grabBox()
	cfg, out, errw := testConfig(eng)
	dest := t.TempDir()
	landed, err := RunGrab(cfg, Options{}, "out/report.pdf", dest)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dest, "report.pdf")
	if len(landed) != 1 || landed[0] != want {
		t.Fatalf("landed = %v, want %s", landed, want)
	}
	if got := out.String(); got != want+"\n" {
		t.Fatalf("stdout = %q", got)
	}
	if b, err := os.ReadFile(want); err != nil || string(b) != "PDFBYTES" {
		t.Fatalf("content = %q, %v", b, err)
	}
	// The target line names the box; the summary names size and landing.
	if !strings.Contains(errw.String(), "grabbing /workspace/out/report.pdf from proj-aaa") {
		t.Fatalf("stderr = %q", errw.String())
	}
	if !strings.Contains(errw.String(), "grabbed /workspace/out/report.pdf → "+want) {
		t.Fatalf("stderr = %q", errw.String())
	}
}

func TestGrabFileExplicitName(t *testing.T) {
	eng := grabBox()
	cfg, out, _ := testConfig(eng)
	dest := filepath.Join(t.TempDir(), "renamed.pdf")
	if _, err := RunGrab(cfg, Options{}, "/workspace/out/report.pdf", dest); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != dest+"\n" {
		t.Fatalf("stdout = %q", got)
	}
	if b, err := os.ReadFile(dest); err != nil || string(b) != "PDFBYTES" {
		t.Fatalf("content = %q, %v", b, err)
	}
}

// TestGrabNeverClobbers pins the no-clobber rule (ADR 0040): an existing host
// file — even one the user named exactly — keeps its content; the grab lands
// uniquified and SAYS so.
func TestGrabNeverClobbers(t *testing.T) {
	eng := grabBox()
	cfg, out, errw := testConfig(eng)
	dir := t.TempDir()
	dest := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(dest, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	landed, err := RunGrab(cfg, Options{}, "out/report.pdf", dest)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "report-2.pdf")
	if len(landed) != 1 || landed[0] != want || out.String() != want+"\n" {
		t.Fatalf("landed = %v stdout = %q", landed, out.String())
	}
	if b, _ := os.ReadFile(dest); string(b) != "precious" {
		t.Fatalf("existing file clobbered: %q", b)
	}
	if b, _ := os.ReadFile(want); string(b) != "PDFBYTES" {
		t.Fatalf("landed content = %q", b)
	}
	if !strings.Contains(errw.String(), "report.pdf existed — landed as report-2.pdf") {
		t.Fatalf("stderr = %q", errw.String())
	}
}

func TestGrabFileToStdout(t *testing.T) {
	eng := grabBox()
	cfg, out, errw := testConfig(eng)
	landed, err := RunGrab(cfg, Options{}, "out/report.pdf", "-")
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 0 {
		t.Fatalf("landed = %v, want none for '-'", landed)
	}
	if out.String() != "PDFBYTES" {
		t.Fatalf("stdout = %q, want the raw content", out.String())
	}
	if !strings.Contains(errw.String(), "grabbed /workspace/out/report.pdf (8 bytes)") {
		t.Fatalf("stderr = %q", errw.String())
	}
}

func TestGrabDirectoryToStdoutRefuses(t *testing.T) {
	eng := grabBox()
	cfg, _, _ := testConfig(eng)
	_, err := RunGrab(cfg, Options{}, "/workspace/out", "-")
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestGrabMissingBoxPath(t *testing.T) {
	eng := grabBox()
	cfg, _, _ := testConfig(eng)
	_, err := RunGrab(cfg, Options{}, "nope.txt", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no such path in the box: /workspace/nope.txt") {
		t.Fatalf("err = %v", err)
	}
}

func TestGrabDirectoryPreservesStructure(t *testing.T) {
	eng := box("docker", "aaa")
	eng.boxdirs = []string{"/workspace/proj", "/workspace/proj/sub"}
	eng.boxfs = map[string]string{
		"/workspace/proj/a.txt":     "A",
		"/workspace/proj/sub/b.txt": "B",
	}
	eng.boxOther = []string{"/workspace/proj/link"}
	cfg, out, errw := testConfig(eng)
	dest := t.TempDir()
	landed, err := RunGrab(cfg, Options{}, "proj", dest)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dest, "proj")
	if len(landed) != 1 || landed[0] != root || out.String() != root+"\n" {
		t.Fatalf("landed = %v stdout = %q", landed, out.String())
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(b) != "A" {
		t.Fatalf("a.txt = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "sub", "b.txt")); string(b) != "B" {
		t.Fatalf("sub/b.txt = %q", b)
	}
	if !strings.Contains(errw.String(), "skipping /workspace/proj/link (not a regular file or directory)") {
		t.Fatalf("stderr = %q", errw.String())
	}
	if !strings.Contains(errw.String(), "grabbed "+root+" — 2 files, 2 bytes") {
		t.Fatalf("stderr = %q", errw.String())
	}
}

// TestGrabDirectoryHostileEnumeration pins the containment judgment: records
// outside the grabbed root, traversal components, and control characters in
// names are ignored/refused/renamed — never landed outside the claimed tree.
func TestGrabDirectoryHostileEnumeration(t *testing.T) {
	eng := box("docker", "aaa")
	eng.boxdirs = []string{"/workspace/proj"}
	eng.boxfs = map[string]string{"/workspace/proj/ok.txt": "OK"}
	eng.enumOut = "d\x00/workspace/proj\x00" +
		"f\x00/etc/passwd\x00" + // outside the root entirely
		"f\x00/workspace/proj/../escape.txt\x00" + // traversal component
		"f\x00/workspace/proj/evil\nname\x00" + // control character
		"f\x00/workspace/proj/ok.txt\x00"
	eng.boxfs["/workspace/proj/evil\nname"] = "EVIL"
	cfg, _, errw := testConfig(eng)
	dest := t.TempDir()
	landed, err := RunGrab(cfg, Options{}, "proj", dest)
	if err == nil || !strings.Contains(err.Error(), "2 entries failed") {
		t.Fatalf("err = %v", err)
	}
	root := filepath.Join(dest, "proj")
	if len(landed) != 1 || landed[0] != root {
		t.Fatalf("landed = %v", landed)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "ok.txt")); string(b) != "OK" {
		t.Fatalf("ok.txt = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "evil_name")); string(b) != "EVIL" {
		t.Fatalf("sanitized name content = %q", b)
	}
	if !strings.Contains(errw.String(), `ignoring enumerated "/etc/passwd"`) {
		t.Fatalf("stderr = %q", errw.String())
	}
	if !strings.Contains(errw.String(), "unusable name") {
		t.Fatalf("stderr = %q", errw.String())
	}
	// Nothing may have landed outside the claimed root.
	entries, _ := os.ReadDir(dest)
	if len(entries) != 1 || entries[0].Name() != "proj" {
		t.Fatalf("destination gained extra entries: %v", entries)
	}
}

func TestGrabDirectoryPartialEnumeration(t *testing.T) {
	eng := box("docker", "aaa")
	eng.boxdirs = []string{"/workspace/proj"}
	eng.enumOut = "d\x00/workspace/proj\x00f\x00/workspace/proj/a.txt\x00"
	eng.enumErr = fmt.Errorf("exit status 1")
	eng.boxfs = map[string]string{"/workspace/proj/a.txt": "A"}
	cfg, _, errw := testConfig(eng)
	dest := t.TempDir()
	landed, err := RunGrab(cfg, Options{}, "proj", dest)
	if err == nil || !strings.Contains(err.Error(), "1 entries failed") {
		t.Fatalf("err = %v", err)
	}
	if len(landed) != 1 {
		t.Fatalf("landed = %v — partial results must stay", landed)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "proj", "a.txt")); string(b) != "A" {
		t.Fatalf("a.txt = %q", b)
	}
	if !strings.Contains(errw.String(), "may be incomplete") {
		t.Fatalf("stderr = %q", errw.String())
	}
}

// --- destination resolution ---

// TestResolveDestRefusesSymlinkedDir pins finding-1's fix (ADR 0040 / the
// hostopen rule): a destination directory that is a SYMLINK — the shape an
// agent swaps in to redirect the grab's anchor outside the named directory —
// is refused, never followed. Without the fix os.OpenRoot would anchor the
// grab in the symlink's target and land agent content there.
func TestResolveDestRefusesSymlinkedDir(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveDest(link, "x.txt"); err == nil {
		t.Fatal("a symlinked destination directory must be refused, not followed")
	}
}

func TestResolveDestMissingParent(t *testing.T) {
	_, err := resolveDest(filepath.Join(t.TempDir(), "no", "such", "out.txt"), "x")
	if err == nil || !strings.Contains(err.Error(), "no such file or directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveDestTrailingSlashMissing(t *testing.T) {
	_, err := resolveDest(filepath.Join(t.TempDir(), "nodir")+"/", "x")
	if err == nil || !strings.Contains(err.Error(), "no such directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveDestParentIsFile(t *testing.T) {
	d := t.TempDir()
	file := filepath.Join(d, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := resolveDest(filepath.Join(file, "out.txt"), "x")
	if err == nil {
		t.Fatal("expected an error for a file used as a directory")
	}
}

// --- host claim protocol ---

// TestClaimStreamRefusesSymlinkName pins the reversed write protocol's core
// property: a pre-existing symlink at the landing name (the agent can plant
// one when the destination is inside the project tree) is never followed —
// O_EXCL/link treat it as an existing name and the claim uniquifies past it.
func TestClaimStreamRefusesSymlinkName(t *testing.T) {
	d := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(d, "out.txt")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(d)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	claimed, size, err := claimStream(root, "", "out", ".txt", func(w io.Writer) error {
		_, werr := io.WriteString(w, "agent bytes")
		return werr
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed != "out-2.txt" || size != int64(len("agent bytes")) {
		t.Fatalf("claimed = %q size = %d", claimed, size)
	}
	if b, _ := os.ReadFile(outside); string(b) != "precious" {
		t.Fatalf("symlink target written through: %q", b)
	}
}

func TestClaimStreamCleansTempOnFailure(t *testing.T) {
	d := t.TempDir()
	root, err := os.OpenRoot(d)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	_, _, err = claimStream(root, "", "out", ".txt", func(w io.Writer) error {
		io.WriteString(w, "partial")
		return fmt.Errorf("stream died")
	})
	if err == nil || !strings.Contains(err.Error(), "stream died") {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(d)
	if len(entries) != 0 {
		t.Fatalf("temp or half-file left behind: %v", entries)
	}
}

// --- pure helpers ---

// decode drives recordSink to completion for the whole string in one Write,
// the equivalent of the old parseRecords (streaming behavior is pinned
// separately in TestRecordSinkStreamingAndCaps).
func decode(s string) []record {
	var sink recordSink
	sink.Write([]byte(s))
	return sink.recs
}

func TestParseRecords(t *testing.T) {
	recs := decode("d\x00/a\x00f\x00/a/b\x00o\x00/a/l\x00")
	want := []record{{'d', "/a"}, {'f', "/a/b"}, {'o', "/a/l"}}
	if len(recs) != len(want) {
		t.Fatalf("recs = %v", recs)
	}
	for i := range want {
		if recs[i] != want[i] {
			t.Fatalf("recs[%d] = %v, want %v", i, recs[i], want[i])
		}
	}
	// A malformed tail (died exec mid-record) drops cleanly.
	if got := decode("f\x00/a\x00garbage"); len(got) != 1 || got[0].path != "/a" {
		t.Fatalf("malformed tail: %v", got)
	}
	// An out-of-frame tag stops parsing — nothing after it is trusted.
	if got := decode("f\x00/a\x00zz\x00/b\x00f\x00/c\x00"); len(got) != 1 {
		t.Fatalf("out-of-frame: %v", got)
	}
	if decode("") != nil {
		t.Fatal("empty output must parse to no records")
	}
}

// TestRecordSinkStreamingAndCaps pins the streaming decode: records split
// across Write boundaries reassemble, the entry cap truncates without dropping
// bytes on the floor (Write always reports full consumption), and an unframed
// flood past the pending cap goes out-of-frame instead of growing forever.
func TestRecordSinkStreamingAndCaps(t *testing.T) {
	// A frame split mid-path across two writes must still decode.
	var s recordSink
	full := "f\x00/workspace/proj/file\x00"
	n1, _ := s.Write([]byte(full[:10]))
	n2, _ := s.Write([]byte(full[10:]))
	if n1 != 10 || n2 != len(full)-10 {
		t.Fatalf("Write must report full consumption: %d,%d", n1, n2)
	}
	if len(s.recs) != 1 || s.recs[0].path != "/workspace/proj/file" {
		t.Fatalf("split frame not reassembled: %v", s.recs)
	}

	// Past the entry cap: truncated set, Write still consumes fully (no block).
	var capped recordSink
	capped.recs = make([]record, maxGrabEntries) // pre-fill to the ceiling
	over := "f\x00/x\x00"
	if n, _ := capped.Write([]byte(over)); n != len(over) {
		t.Fatalf("over-cap Write must still consume all bytes: %d", n)
	}
	if !capped.truncated {
		t.Fatal("exceeding maxGrabEntries must set truncated")
	}

	// Past the cumulative byte budget (few but enormous paths): truncated too.
	var bytesCap recordSink
	bytesCap.stored = maxGrabBytes // already at the budget
	if n, _ := bytesCap.Write([]byte("f\x00/x\x00")); n != len("f\x00/x\x00") {
		t.Fatalf("over-byte-budget Write must still consume all bytes: %d", n)
	}
	if !bytesCap.truncated {
		t.Fatal("exceeding maxGrabBytes must set truncated")
	}

	// An unframed flood (no NUL) past the pending cap goes out-of-frame, not
	// unbounded — the buffer is dropped rather than grown forever.
	var flood recordSink
	blob := bytes.Repeat([]byte("a"), maxGrabPending+1)
	if n, _ := flood.Write(blob); n != len(blob) {
		t.Fatalf("flood Write must consume all bytes: %d", n)
	}
	if !flood.outOfFrame || flood.pending != nil {
		t.Fatalf("an over-long unframed run must go out-of-frame and drop the buffer")
	}
}

func TestRelUnder(t *testing.T) {
	for _, tc := range []struct {
		root, p, rel string
		ok           bool
	}{
		{"/a/b", "/a/b", "", true},
		{"/a/b", "/a/b/c/d", "c/d", true},
		{"/a/b", "/a/bc", "", false},
		{"/a/b", "/etc", "", false},
	} {
		rel, ok := relUnder(tc.root, tc.p)
		if rel != tc.rel || ok != tc.ok {
			t.Errorf("relUnder(%q, %q) = %q, %v", tc.root, tc.p, rel, ok)
		}
	}
}

func TestSanitizeGrabRel(t *testing.T) {
	if _, _, ok := sanitizeGrabRel("a/../b"); ok {
		t.Fatal("traversal must refuse")
	}
	if _, _, ok := sanitizeGrabRel("a//b"); ok {
		t.Fatal("empty component must refuse")
	}
	clean, renamed, ok := sanitizeGrabRel("a/evil\nname")
	if !ok || !renamed || clean != "a/evil_name" {
		t.Fatalf("sanitize = %q, %v, %v", clean, renamed, ok)
	}
}

func TestBoxAbs(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"out/report.pdf", "/workspace/out/report.pdf"},
		{"/inbox/x", "/inbox/x"},
		{"./a", "/workspace/a"},
		{"a/./b/../c", "/workspace/a/c"},
	} {
		if got := boxAbs(tc.in); got != tc.want {
			t.Errorf("boxAbs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- the production scripts under a real sh ---

func needSh(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
}

func runScript(t *testing.T, script string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{"-c", script, "byre-grab"}, args...)...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func TestClassifyScript(t *testing.T) {
	needSh(t)
	d := t.TempDir()
	file := filepath.Join(d, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(d, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if out, _, err := runScript(t, classifyScript, file); err != nil || out != "f" {
		t.Fatalf("file: out = %q err = %v", out, err)
	}
	if out, _, err := runScript(t, classifyScript, sub); err != nil || !strings.HasPrefix(out, "d ") {
		t.Fatalf("dir: out = %q err = %v", out, err)
	}
	// A symlinked directory classifies to its PHYSICAL path, so enumeration
	// (find does not follow argument symlinks) walks the real tree.
	link := filepath.Join(d, "link")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatal(err)
	}
	out, _, err := runScript(t, classifyScript, link)
	if err != nil || !strings.HasPrefix(out, "d ") {
		t.Fatalf("symlinked dir: out = %q err = %v", out, err)
	}
	phys := out[2:]
	if filepath.Base(phys) != "sub" {
		t.Fatalf("physical path = %q, want .../sub", phys)
	}
	if _, stderr, err := runScript(t, classifyScript, filepath.Join(d, "nope")); err == nil || !strings.Contains(stderr, "no such path in the box") {
		t.Fatalf("missing: err = %v stderr = %q", err, stderr)
	}
}

func TestCatScript(t *testing.T) {
	needSh(t)
	d := t.TempDir()
	file := filepath.Join(d, "f.bin")
	content := "binary\x00ish\ncontent"
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _, err := runScript(t, catScript, file); err != nil || out != content {
		t.Fatalf("out = %q err = %v", out, err)
	}
	if _, stderr, err := runScript(t, catScript, d); err == nil || !strings.Contains(stderr, "is not a file in the box") {
		t.Fatalf("dir: err = %v stderr = %q", err, stderr)
	}
}

func TestEnumerateScript(t *testing.T) {
	needSh(t)
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "sub", "b.txt"), []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(d, "a.txt"), filepath.Join(d, "l")); err != nil {
		t.Fatal(err)
	}
	out, _, err := runScript(t, enumerateScript, d)
	if err != nil {
		t.Fatalf("err = %v (out %q)", err, out)
	}
	recs := decode(out)
	got := map[string]byte{}
	for _, r := range recs {
		got[r.path] = r.tag
	}
	for p, tag := range map[string]byte{
		d:                             'd',
		filepath.Join(d, "sub"):       'd',
		filepath.Join(d, "a.txt"):     'f',
		filepath.Join(d, "sub/b.txt"): 'f',
		filepath.Join(d, "l"):         'o',
	} {
		if got[p] != tag {
			t.Errorf("%s enumerated as %q, want %q (records: %v)", p, got[p], tag, recs)
		}
	}
}
