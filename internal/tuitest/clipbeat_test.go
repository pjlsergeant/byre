package tuitest

// The LIVE paste beat, headless: a fake wl-paste on a controlled PATH plays
// the pasteboard (the one place the TUI tier fakes a host capability — the
// product binary is untouched; ADR 0038 planned exactly this second wave).
// These pin the importFromPaste disambiguation end to end — the 2026-07-10
// field bug's whole decision tree, previously unit-only:
//
//   paste mirrors the pasteboard → a real Cmd-V: full clipboard read;
//   paste is an existing absolute path → a drag: deliver the FILE;
//   anything else → literal pasted text.
//
// Every test ends in the loud no-engine error (PATH resolves no docker), so
// the assertion is which BRANCH spoke, plus a nonzero exit.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeWaylandClipboard writes a wl-paste shim whose answers come from files
// the test controls, and returns Opts wiring it in: WAYLAND_DISPLAY set,
// PATH = the shim dir alone (no engines, no real clipboard tools), DISPLAY
// unset. The shim needs nothing from PATH itself (/bin/cat is absolute).
//
// Linux only: the shim plays the LINUX read backend — on darwin,
// hostClipboardReader rides osascript and never consults wl-paste, so
// these tests skip there (the macOS pasteboard keeps DELIVER.md's
// macOS-verified posture; a fake-osascript sibling is possible if that
// posture ever needs CI teeth).
func fakeWaylandClipboard(t *testing.T, pasteboardText string) Opts {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the wl-paste shim fakes the Linux clipboard backend; darwin reads via osascript")
	}
	clipDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(clipDir, "types"), []byte("text/plain\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clipDir, "text"), []byte(pasteboardText), 0o644); err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	shim := `#!/bin/sh
case "$1" in
  --list-types) exec /bin/cat "$FAKE_CLIP_DIR/types" ;;
  --type) case "$2" in
    text/plain*) exec /bin/cat "$FAKE_CLIP_DIR/text" ;;
  esac ;;
esac
exit 1
`
	if err := os.WriteFile(filepath.Join(shimDir, "wl-paste"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	_, env := storeEnv(t)
	env["PATH"] = shimDir
	env["WAYLAND_DISPLAY"] = "byre-test-fake"
	env["FAKE_CLIP_DIR"] = clipDir
	env["BYRE_DELIVER_DEBUG"] = "1" // DEBUG branch: dump paste-classification detail
	return Opts{Env: env, Unset: []string{"DISPLAY"}}
}

func TestIntegrationTUILiveBeatDragDeliversFile(t *testing.T) {
	Require(t)
	drag := filepath.Join(t.TempDir(), "dragged.txt")
	if err := os.WriteFile(drag, []byte("dragged content"), 0o644); err != nil {
		t.Fatal(err)
	}
	// DEBUG branch: record what the test created, to compare against the
	// byre-debug line reporting what the child process received + stat'd.
	if st, err := os.Stat(drag); err != nil {
		t.Logf("DEBUG created drag=%q len=%d stat-ERR=%v", drag, len(drag), err)
	} else {
		t.Logf("DEBUG created drag=%q len=%d mode=%v", drag, len(drag), st.Mode())
	}
	s := Start(t, fakeWaylandClipboard(t, "something else entirely"), Binary(t), "deliver")

	s.WaitFor("text on the clipboard") // the LIVE beat, seeing the fake pasteboard
	s.Paste(drag)
	s.WaitFor("delivering the dragged file")
	s.WaitFor("no container engine")
	if st := s.WaitForExit(); st == 0 {
		t.Fatalf("engine-less delivery should exit nonzero\n%s", s.CaptureNow())
	}
	if final := s.CaptureNow(); strings.Contains(final, "reading the clipboard") {
		t.Fatalf("drag took the clipboard branch:\n%s", final)
	}
}

func TestIntegrationTUILiveBeatMirrorReadsClipboard(t *testing.T) {
	Require(t)
	s := Start(t, fakeWaylandClipboard(t, "mirrored words"), Binary(t), "deliver")

	s.WaitFor("text on the clipboard")
	s.Paste("mirrored words") // a real Cmd-V: streamed text mirrors the pasteboard
	s.WaitFor("reading the clipboard")
	s.WaitFor("no container engine")
	if st := s.WaitForExit(); st == 0 {
		t.Fatalf("engine-less delivery should exit nonzero\n%s", s.CaptureNow())
	}
	if final := s.CaptureNow(); strings.Contains(final, "dragged") {
		t.Fatalf("mirror took the drag branch:\n%s", final)
	}
}

func TestIntegrationTUILiveBeatProseStaysText(t *testing.T) {
	Require(t)
	s := Start(t, fakeWaylandClipboard(t, "something else entirely"), Binary(t), "deliver")

	s.WaitFor("text on the clipboard")
	s.Paste("prose that names no file")
	s.WaitFor("paste received")
	s.WaitFor("no container engine")
	if st := s.WaitForExit(); st == 0 {
		t.Fatalf("engine-less delivery should exit nonzero\n%s", s.CaptureNow())
	}
	final := s.CaptureNow()
	if strings.Contains(final, "dragged") || strings.Contains(final, "reading the clipboard") {
		t.Fatalf("prose escaped the literal-text branch:\n%s", final)
	}
}

func TestIntegrationTUILiveBeatCtrlVReadsClipboard(t *testing.T) {
	Require(t)
	s := Start(t, fakeWaylandClipboard(t, "board content"), Binary(t), "deliver")

	s.WaitFor("text on the clipboard")
	s.Keys("C-v") // the app-level gesture: read the pasteboard out-of-band
	s.WaitFor("reading the clipboard")
	s.WaitFor("no container engine")
	if st := s.WaitForExit(); st == 0 {
		t.Fatalf("engine-less delivery should exit nonzero\n%s", s.CaptureNow())
	}
}
