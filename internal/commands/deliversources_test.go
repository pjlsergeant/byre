package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/deliver"
)

func stubStdinPipe(t *testing.T, piped bool) {
	t.Helper()
	orig := stdinIsPiped
	t.Cleanup(func() { stdinIsPiped = orig })
	stdinIsPiped = func() bool { return piped }
}

func TestSourcesPathsWin(t *testing.T) {
	s, _, _ := testStreams("", true)
	sources, err := deliverSources(s, deliver.Options{}, []string{"/a", "/b"}, nil)
	if err != nil || len(sources) != 2 || sources[0].Path != "/a" {
		t.Fatalf("sources = %+v err = %v", sources, err)
	}
}

func TestSourcesDashIsStdin(t *testing.T) {
	s, _, _ := testStreams("payload", false)
	sources, err := deliverSources(s, deliver.Options{Name: "shot.png"}, []string{"-"}, nil)
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources = %+v err = %v", sources, err)
	}
	if sources[0].Reader == nil || sources[0].Name != "shot.png" || sources[0].Kind != "stdin" {
		t.Fatalf("source = %+v", sources[0])
	}
}

func TestSourcesStdinDefaultNameIsStamped(t *testing.T) {
	s, _, _ := testStreams("x", false)
	sources, err := deliverSources(s, deliver.Options{}, []string{"-"}, nil)
	if err != nil || len(sources) != 1 || !strings.HasPrefix(sources[0].Name, "stdin-") {
		t.Fatalf("sources = %+v err = %v", sources, err)
	}
}

func TestSourcesNoArgsPipedStdinStreams(t *testing.T) {
	stubStdinPipe(t, true)
	s, _, _ := testStreams("piped", false) // no TTY
	sources, err := deliverSources(s, deliver.Options{}, nil, nil)
	if err != nil || len(sources) != 1 || sources[0].Kind != "stdin" {
		t.Fatalf("sources = %+v err = %v", sources, err)
	}
}

func TestSourcesNoArgsDetachedReadsClipboard(t *testing.T) {
	stubStdinPipe(t, false)
	cb := backend([]string{"text/plain"}, map[string][]byte{"text/plain": []byte("clip")})
	s, _, _ := testStreams("", false) // no TTY, not piped: graphical/detached
	sources, err := deliverSources(s, deliver.Options{}, nil, &cb)
	if err != nil || len(sources) != 1 || sources[0].Kind != "clipboard text" {
		t.Fatalf("sources = %+v err = %v", sources, err)
	}
}

func TestSourcesNoArgsNothingAnywhereErrors(t *testing.T) {
	stubStdinPipe(t, false)
	s, _, _ := testStreams("", false)
	_, err := deliverSources(s, deliver.Options{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "nothing to deliver") {
		t.Fatalf("err = %v", err)
	}
}

func TestSourcesTTYBeatNeedsRealTerminal(t *testing.T) {
	// s.TTY set but stdin isn't an *os.File: the beat must fail loudly, not
	// pretend. (The real path is exercised interactively; the loop logic is
	// covered in beat_test.go.)
	var in bytes.Buffer
	s := Streams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, In: &in, TTY: true}
	_, err := deliverSources(s, deliver.Options{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportFromPasteDragDeliversTheFile(t *testing.T) {
	// Field-found: dragging a file onto the beat pasted its PATH, and the
	// old discard-and-read-clipboard logic delivered STALE clipboard content
	// (byre's own previous output, at worst). A dragged path now delivers
	// the file itself.
	real := filepath.Join(t.TempDir(), "authors.txt")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cb := backend([]string{"text/plain"}, map[string][]byte{"text/plain": []byte("stale old clipboard")})
	s, _, errw := testStreams("", true)
	sources, err := importFromPaste(s, &cb, []byte(real+" "), "stamp")
	if err != nil || len(sources) != 1 || sources[0].Path != real {
		t.Fatalf("sources = %+v err = %v", sources, err)
	}
	if !strings.Contains(errw.String(), "paste received") || !strings.Contains(errw.String(), "dragged file") {
		t.Fatalf("no immediate feedback: %q", errw.String())
	}
}

func TestImportFromPasteDragWithEscapedSpaces(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "My File.txt")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cb := backend([]string{"text/plain"}, map[string][]byte{"text/plain": []byte("unrelated")})
	s, _, _ := testStreams("", true)
	escaped := strings.ReplaceAll(real, " ", `\ `)
	sources, err := importFromPaste(s, &cb, []byte(escaped), "stamp")
	if err != nil || len(sources) != 1 || sources[0].Path != real {
		t.Fatalf("sources = %+v err = %v", sources, err)
	}
}

func TestImportFromPasteMirroringClipboardDoesFullRead(t *testing.T) {
	// A real Cmd-V: streamed text equals the pasteboard text — the full
	// priority read runs, so a Finder copy's file refs beat its text rep.
	real := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cb := backend(
		[]string{typeFileRefs, "text/plain"},
		map[string][]byte{typeFileRefs: []byte(real + "\n"), "text/plain": []byte(real)},
	)
	s, _, _ := testStreams("", true)
	sources, err := importFromPaste(s, &cb, []byte(real+"\n"), "stamp")
	if err != nil || len(sources) != 1 || sources[0].Path != real {
		t.Fatalf("file refs should win on a mirrored paste: %+v err = %v", sources, err)
	}
}

func TestImportFromPastePlainTextStaysText(t *testing.T) {
	cb := backend([]string{"text/plain"}, map[string][]byte{"text/plain": []byte("something else")})
	s, _, _ := testStreams("", true)
	sources, err := importFromPaste(s, &cb, []byte("hello world, not a path"), "stamp")
	if err != nil || len(sources) != 1 || sources[0].Kind != "pasted text" ||
		string(sources[0].Data) != "hello world, not a path" {
		t.Fatalf("sources = %+v err = %v", sources, err)
	}
}

func TestDraggedPathsRelativeNeverMatches(t *testing.T) {
	// Pasted prose naming a relative file must stay text.
	if got := draggedPaths("README.md"); got != nil {
		t.Fatalf("relative path must not be treated as a drag: %v", got)
	}
}

func TestDraggedPathsMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := draggedPaths(a + " " + b)
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("draggedPaths = %v", got)
	}
	// One real + one bogus → not a drag.
	if got := draggedPaths(a + " /no/such/thing"); got != nil {
		t.Fatalf("mixed existence must not be a drag: %v", got)
	}
}
