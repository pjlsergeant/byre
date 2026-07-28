package deliver

import (
	"archive/tar"
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/testtools"
)

// The arm for delivery's reporting: every line it prints names something the
// user, the filesystem, an ARCHIVE or a BOX authored -- a source path, a walked
// entry, a tar member name, an enumerated box path, an engine message -- and
// none of these printers emits ANSI of its own, so the contract is exact: no
// raw ESC reaches the terminal, and one report is one line. Scoped per surface
// on purpose, never package-wide: remote.go's send meter writes CR-anchored
// progress and an explicit erase, deliberately, and is exempt by design.
const (
	// escCSI erases the reported line and rewinds a row: the shape that lets a
	// filename rewrite the delivery summary above it.
	escCSI = "\x1b[2K\x1b[A"
	// escOSC is an OSC 52 clipboard write: reporting as an exfiltration verb.
	escOSC = "\x1b]52;c;cGF5bG9hZA==\a"
	// escFrame is the other half of the funnel's promise: a name that carries
	// its own line break, and a CR that would rewrite the line in place.
	escFrame = "\nbyre: delivered everything, honest\r"
)

// deliverPayloadTree delivers one directory and one top-level FIFO whose names
// all carry payload, and returns everything the transport reported. Two runs
// differing only in the payload report the same LINES about the same entries,
// so their line counts are comparable -- which is how the framing promise gets
// asserted without hardcoding a count.
func deliverPayloadTree(t *testing.T, payload string) string {
	t.Helper()
	cfg, _, errw := testConfig(box("docker", "aaa"))
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj"+payload)
	mustMkdir(t, proj)
	mustWrite(t, filepath.Join(proj, "a.txt"), "A")
	mustSymlink(t, "../outside", filepath.Join(proj, "escape"+payload))
	if err := mkfifo(filepath.Join(proj, "pipe")); err != nil {
		testtools.Unavailable(t, "mkfifo", err)
	}
	// A top-level non-regular source: deliverPath's own skip line.
	fifo := filepath.Join(dir, "top"+payload+".pipe")
	if err := mkfifo(fifo); err != nil {
		testtools.Unavailable(t, "mkfifo", err)
	}
	if _, err := RunSources(cfg, Options{}, PathSources([]string{proj, fifo})); err != nil {
		t.Fatalf("delivery failed: %v", err)
	}
	return errw.String()
}

func TestTransportReportsEscapeSourceNames(t *testing.T) {
	// The names carry the payload, so every line built from a display path
	// carries it too. The framing half of the payload imitates a byre report
	// line: a prefix check would wave that through, a line count cannot.
	out := deliverPayloadTree(t, escCSI+escFrame+escOSC)

	if i := strings.IndexByte(out, 0x1b); i >= 0 {
		t.Errorf("the transport printed a raw ESC at byte %d: %q", i, out)
	}
	// The baseline carries control characters too -- the rename notices fire on
	// exactly that -- but none of them frames a line, so the two runs report
	// the same lines about the same entries and only the framing can differ.
	clean := deliverPayloadTree(t, "\x01\x02")
	if got, want := strings.Count(out, "\n"), strings.Count(clean, "\n"); got != want {
		t.Errorf("the transport printed %d lines, want %d -- a name forged one:\n%s", got, want, out)
	}
	// The strip is not a censor: the reports still name what they are about.
	for _, want := range []string{"proj", "skipping", "top", ".pipe"} {
		if !strings.Contains(out, want) {
			t.Errorf("the transport dropped %q from its reporting: %q", want, out)
		}
	}
}

// TestReportfFramesOneLine pins the funnel itself: the framing newline is the
// funnel's to add, so no argument can add another.
func TestReportfFramesOneLine(t *testing.T) {
	cfg, _, errw := testConfig(box("docker", "aaa"))
	reportf(cfg, "byre: delivering %s: %v", "a\nb"+escCSI, errors.New("refused\rbyre: delivered everything"+escOSC))
	out := errw.String()

	if n := strings.Count(out, "\n"); n != 1 {
		t.Errorf("reportf wrote %d lines, want 1: %q", n, out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("reportf must frame its line: %q", out)
	}
	if i := strings.IndexByte(out, 0x1b); i >= 0 {
		t.Errorf("reportf printed a raw ESC at byte %d: %q", i, out)
	}
	if !strings.Contains(out, "refused") {
		t.Errorf("reportf dropped the error text: %q", out)
	}
}

// unpackPayloadArchive delivers an archive whose member names carry payload and
// returns what the unpacker reported. The ARCHIVE authors these names -- the
// strongest case in the family, since the sender is whoever built the tar.
func unpackPayloadArchive(t *testing.T, payload string) string {
	t.Helper()
	cfg, _, errw := testConfig(box("docker", "aaa"))
	archive := mktar(t,
		tarEntry{Name: "report" + payload + ".pdf", Content: "pdf"},
		tarEntry{Name: "bug" + payload + "/notes.txt", Content: "n"},
		tarEntry{Name: "sock" + payload, Type: tar.TypeSymlink},
	)
	if _, err := RunTar(cfg, Options{}, archive); err != nil {
		t.Fatalf("unpack failed: %v", err)
	}
	return errw.String()
}

func TestTarReportsEscapeArchiveNames(t *testing.T) {
	out := unpackPayloadArchive(t, escCSI+escFrame+escOSC)

	if i := strings.IndexByte(out, 0x1b); i >= 0 {
		t.Errorf("the unpacker printed a raw ESC at byte %d: %q", i, out)
	}
	// Same baseline rule as the transport: control characters (so the rename
	// notices still fire) that frame nothing.
	clean := unpackPayloadArchive(t, "\x01\x02")
	if got, want := strings.Count(out, "\n"), strings.Count(clean, "\n"); got != want {
		t.Errorf("the unpacker printed %d lines, want %d -- a member name forged one:\n%s", got, want, out)
	}
	for _, want := range []string{"renamed", "skipping archive entry", "report", "sock"} {
		if !strings.Contains(out, want) {
			t.Errorf("the unpacker dropped %q from its reporting: %q", want, out)
		}
	}
}

// grabPayloadTree grabs a box directory whose entry names carry payload. The
// polarity is reversed here -- the BOX authors every name the host prints.
func grabPayloadTree(t *testing.T, payload string) string {
	t.Helper()
	eng := box("docker", "aaa")
	eng.boxdirs = []string{"/workspace", "/workspace/out"}
	eng.boxfs = map[string]string{"/workspace/out/report" + payload + ".pdf": "PDF"}
	eng.boxOther = []string{"/workspace/out/sock" + payload}
	cfg, _, errw := testConfig(eng)
	if _, err := RunGrab(cfg, Options{}, "out", t.TempDir()); err != nil {
		t.Fatalf("grab failed: %v", err)
	}
	return errw.String()
}

func TestGrabReportsEscapeBoxNames(t *testing.T) {
	out := grabPayloadTree(t, escCSI+escFrame+escOSC)

	if i := strings.IndexByte(out, 0x1b); i >= 0 {
		t.Errorf("grab printed a raw ESC at byte %d: %q", i, out)
	}
	clean := grabPayloadTree(t, "\x01\x02")
	if got, want := strings.Count(out, "\n"), strings.Count(clean, "\n"); got != want {
		t.Errorf("grab printed %d lines, want %d -- a box-authored name forged one:\n%s", got, want, out)
	}
	for _, want := range []string{"renamed", "skipping", "grabbed"} {
		if !strings.Contains(out, want) {
			t.Errorf("grab dropped %q from its reporting: %q", want, out)
		}
	}
}

// planPayloadPack plans a remote send over a tree whose entries carry payload:
// the planner reports through a caller-supplied warn writer, the one report
// path in this package that is not the session config.
func planPayloadPack(t *testing.T, payload string) string {
	t.Helper()
	dir := t.TempDir()
	sub := filepath.Join(dir, "bug"+payload)
	mustMkdir(t, sub)
	mustWrite(t, filepath.Join(sub, "notes.txt"), "n")
	if err := mkfifo(filepath.Join(sub, "pipe"+payload)); err != nil {
		testtools.Unavailable(t, "mkfifo", err)
	}
	var warn bytes.Buffer
	_, cleanup, err := planPack(&warn, PathSources([]string{sub}))
	defer cleanup()
	if err != nil {
		t.Fatalf("planning failed: %v", err)
	}
	return warn.String()
}

func TestRemotePlanWarningsEscapeSourceNames(t *testing.T) {
	out := planPayloadPack(t, escCSI+escFrame+escOSC)

	if i := strings.IndexByte(out, 0x1b); i >= 0 {
		t.Errorf("the planner printed a raw ESC at byte %d: %q", i, out)
	}
	clean := planPayloadPack(t, "\x01\x02")
	if got, want := strings.Count(out, "\n"), strings.Count(clean, "\n"); got != want {
		t.Errorf("the planner printed %d lines, want %d -- a name forged one:\n%s", got, want, out)
	}
	if !strings.Contains(out, "skipping") {
		t.Errorf("the planner dropped its skip note: %q", out)
	}
}
