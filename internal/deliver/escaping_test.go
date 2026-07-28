package deliver

import (
	"path/filepath"
	"strings"
	"testing"
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
)

func TestTransportReportsEscapeSourceNames(t *testing.T) {
	// The tree's own name carries the payload, so every per-entry line built
	// from the display path (`p`) carries it too.
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng)
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj"+escCSI)
	mustMkdir(t, proj)
	mustWrite(t, filepath.Join(proj, "a.txt"), "A")
	mustSymlink(t, "../outside", filepath.Join(proj, "escape"+escOSC))
	if err := mkfifo(filepath.Join(proj, "pipe")); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	// A top-level non-regular source: deliverPath's own skip line.
	fifo := filepath.Join(dir, "top"+escOSC+".pipe")
	if err := mkfifo(fifo); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	if _, err := RunSources(cfg, Options{}, PathSources([]string{proj, fifo})); err != nil {
		t.Fatalf("delivery failed: %v", err)
	}
	out := errw.String()

	if i := strings.IndexByte(out, 0x1b); i >= 0 {
		t.Errorf("the transport printed a raw ESC at byte %d: %q", i, out)
	}
	// The strip is not a censor: the reports still name what they are about.
	for _, want := range []string{"proj", "skipping", "top", ".pipe"} {
		if !strings.Contains(out, want) {
			t.Errorf("the transport dropped %q from its reporting: %q", want, out)
		}
	}
}
