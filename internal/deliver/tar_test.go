package deliver

import (
	"archive/tar"
	"bytes"
	"strings"
	"testing"
)

// tarEntry is one member of a test archive; Dir marks a directory entry.
type tarEntry struct {
	Name    string
	Content string
	Dir     bool
	Type    byte // overrides the Reg/Dir typeflag when nonzero
}

func mktar(t *testing.T, entries ...tarEntry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.Name, Mode: 0o644, Size: int64(len(e.Content)), Typeflag: tar.TypeReg}
		if e.Dir {
			hdr.Typeflag = tar.TypeDir
			hdr.Mode = 0o755
		}
		if e.Type != 0 {
			hdr.Typeflag = e.Type
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.Content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestTarTopLevelFiles(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, out, errw := testConfig(eng)
	archive := mktar(t,
		tarEntry{Name: "report.pdf", Content: "pdf-bytes"},
		tarEntry{Name: "notes.md", Content: "md"},
	)
	landed, err := RunTar(cfg, Options{}, archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 2 || landed[0] != "/inbox/report.pdf" || landed[1] != "/inbox/notes.md" {
		t.Fatalf("landed = %v", landed)
	}
	if out.String() != "/inbox/report.pdf\n/inbox/notes.md\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	if len(eng.streams) != 2 || eng.streams[0] != "aaa report.pdf<-pdf-bytes" {
		t.Fatalf("streams = %v", eng.streams)
	}
	if !strings.Contains(errw.String(), "delivered 2 files") {
		t.Fatalf("no summary: %q", errw.String())
	}
}

func TestTarDirectoryTree(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, out, _ := testConfig(eng)
	archive := mktar(t,
		tarEntry{Name: "bug/", Dir: true},
		tarEntry{Name: "bug/notes.txt", Content: "n"},
		tarEntry{Name: "bug/sub/", Dir: true},
		tarEntry{Name: "bug/sub/x.txt", Content: "x"},
	)
	landed, err := RunTar(cfg, Options{}, archive)
	if err != nil {
		t.Fatal(err)
	}
	// One top-level path: the claimed root. Interior files stream under it.
	if len(landed) != 1 || landed[0] != "/inbox/bug" {
		t.Fatalf("landed = %v", landed)
	}
	if out.String() != "/inbox/bug\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	want := []string{"aaa bug/notes.txt<-n", "aaa bug/sub/x.txt<-x"}
	if len(eng.streams) != 2 || eng.streams[0] != want[0] || eng.streams[1] != want[1] {
		t.Fatalf("streams = %v, want %v", eng.streams, want)
	}
}

func TestTarUniquifiesAgainstInbox(t *testing.T) {
	eng := box("docker", "aaa")
	eng.inbox = map[string]bool{"report.pdf": true}
	cfg, _, _ := testConfig(eng)
	landed, err := RunTar(cfg, Options{}, mktar(t, tarEntry{Name: "report.pdf", Content: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 || landed[0] != "/inbox/report-2.pdf" {
		t.Fatalf("landed = %v", landed)
	}
}

func TestTarClaimsRootOnDemand(t *testing.T) {
	// No explicit directory entries — GNU tar --exclude and hand-built
	// archives both produce these; the root claims at first interior need.
	eng := box("docker", "aaa")
	cfg, out, _ := testConfig(eng)
	landed, err := RunTar(cfg, Options{}, mktar(t, tarEntry{Name: "bug/deep/notes.txt", Content: "n"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 || landed[0] != "/inbox/bug" {
		t.Fatalf("landed = %v", landed)
	}
	if out.String() != "/inbox/bug\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	if len(eng.streams) != 1 || eng.streams[0] != "aaa bug/deep/notes.txt<-n" {
		t.Fatalf("streams = %v", eng.streams)
	}
}

func TestTarRefusesDotDot(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng)
	archive := mktar(t,
		tarEntry{Name: "../evil.sh", Content: "boom"},
		tarEntry{Name: "fine.txt", Content: "ok"},
	)
	landed, err := RunTar(cfg, Options{}, archive)
	if err == nil || !strings.Contains(err.Error(), "1 entry failed") {
		t.Fatalf("err = %v", err)
	}
	if len(landed) != 1 || landed[0] != "/inbox/fine.txt" {
		t.Fatalf("landed = %v", landed)
	}
	for _, s := range eng.streams {
		if strings.Contains(s, "evil") {
			t.Fatalf("dot-dot entry streamed: %v", eng.streams)
		}
	}
	if !strings.Contains(errw.String(), "unusable name") {
		t.Fatalf("no refusal note: %q", errw.String())
	}
}

func TestTarConfinesAbsoluteNames(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, _ := testConfig(eng)
	landed, err := RunTar(cfg, Options{}, mktar(t, tarEntry{Name: "/etc/passwd", Content: "root"}))
	if err != nil {
		t.Fatal(err)
	}
	// The leading slash drops: it lands as /inbox/etc/passwd, never /etc.
	if len(landed) != 1 || landed[0] != "/inbox/etc" {
		t.Fatalf("landed = %v", landed)
	}
	if len(eng.streams) != 1 || eng.streams[0] != "aaa etc/passwd<-root" {
		t.Fatalf("streams = %v", eng.streams)
	}
}

func TestTarSkipsAlienTypes(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng)
	archive := mktar(t,
		tarEntry{Name: "link", Type: tar.TypeSymlink},
		tarEntry{Name: "fine.txt", Content: "ok"},
	)
	landed, err := RunTar(cfg, Options{}, archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 || landed[0] != "/inbox/fine.txt" {
		t.Fatalf("landed = %v", landed)
	}
	if !strings.Contains(errw.String(), "not a regular file or directory") {
		t.Fatalf("no skip note: %q", errw.String())
	}
}

func TestTarEmptyArchiveErrors(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, _ := testConfig(eng)
	if _, err := RunTar(cfg, Options{}, mktar(t)); err == nil || !strings.Contains(err.Error(), "no entries") {
		t.Fatalf("err = %v", err)
	}
}

func TestTarInteriorFailureCountsAndContinues(t *testing.T) {
	eng := box("docker", "aaa")
	eng.failMkdir = true
	cfg, out, errw := testConfig(eng)
	archive := mktar(t,
		tarEntry{Name: "bug/", Dir: true},
		tarEntry{Name: "bug/sub/", Dir: true}, // interior mkdir fails
		tarEntry{Name: "solo.txt", Content: "s"},
	)
	landed, err := RunTar(cfg, Options{}, archive)
	if err == nil || !strings.Contains(err.Error(), "1 entry failed") {
		t.Fatalf("err = %v", err)
	}
	// The claimed root still printed (the path stays useful); the later
	// top-level file still delivered.
	if len(landed) != 2 || landed[0] != "/inbox/bug" || landed[1] != "/inbox/solo.txt" {
		t.Fatalf("landed = %v", landed)
	}
	if out.String() != "/inbox/bug\n/inbox/solo.txt\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(errw.String(), "1 entry failed") {
		t.Fatalf("no failure summary: %q", errw.String())
	}
}

func TestTarTruncatedStream(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, _ := testConfig(eng)
	whole := mktar(t, tarEntry{Name: "a.txt", Content: "aaa"}, tarEntry{Name: "b.txt", Content: "bbb"})
	cut := whole.Bytes()[:1200] // a.txt complete (1024), b.txt's header clipped mid-block
	landed, err := RunTar(cfg, Options{}, bytes.NewReader(cut))
	if err == nil || !strings.Contains(err.Error(), "reading the archive") {
		t.Fatalf("err = %v", err)
	}
	// What landed before the cut stays landed.
	if len(landed) != 1 || landed[0] != "/inbox/a.txt" {
		t.Fatalf("landed = %v", landed)
	}
}

func TestTarControlCharacterNames(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng)
	landed, err := RunTar(cfg, Options{}, mktar(t, tarEntry{Name: "bad\nname.txt", Content: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 || landed[0] != "/inbox/bad_name.txt" {
		t.Fatalf("landed = %v", landed)
	}
	if !strings.Contains(errw.String(), "renamed") {
		t.Fatalf("silent rename: %q", errw.String())
	}
}
