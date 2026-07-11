package commands

import (
	"bytes"
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
