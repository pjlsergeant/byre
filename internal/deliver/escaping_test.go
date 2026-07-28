package deliver

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/testtools"
)

// The arm for the delivery transport's reporting: every line it prints names
// something the user or the filesystem authored -- a source path, a walked
// entry, an engine message -- and the transport emits no ANSI of its own, so
// the contract is exact: no raw ESC reaches the terminal. Scoped to this
// surface on purpose; remote.go's send meter writes CSI deliberately.
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
