package runner

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/testtools"
)

// capBuffer's contract has two halves and the second is the load-bearing one:
// it keeps at most max bytes, and it ALWAYS reports a full write. A child
// process writing past the cap must never block on its stderr pipe -- it just
// stops being recorded. A short write here would stall an engine CLI mid-run
// with no diagnosis, which is why this is pinned rather than assumed.
func TestCapBufferNeverShortWrites(t *testing.T) {
	c := &capBuffer{max: 8}
	for _, chunk := range []string{"abcdefgh", "ijkl", strings.Repeat("z", 4096)} {
		n, err := c.Write([]byte(chunk))
		if err != nil {
			t.Fatalf("Write(%d bytes) errored: %v", len(chunk), err)
		}
		if n != len(chunk) {
			t.Fatalf("Write returned %d for %d bytes -- a short write blocks the child", n, len(chunk))
		}
	}
	if got := c.String(); got != "abcdefgh" {
		t.Fatalf("kept %q, want the first 8 bytes only", got)
	}
}

func TestCapBufferKeepsThePrefixAcrossAStraddlingWrite(t *testing.T) {
	// The interesting case is one Write that crosses the cap: the prefix is
	// kept, the tail is dropped, and nothing beyond max is ever stored.
	c := &capBuffer{max: 5}
	c.Write([]byte("abc"))
	c.Write([]byte("de-DROPPED"))
	if got := c.String(); got != "abcde" {
		t.Fatalf("kept %q, want %q", got, "abcde")
	}
}

func TestCapBufferIsAnIOWriter(t *testing.T) {
	// It is handed to exec.Cmd as Stderr, so the interface is the contract.
	var w io.Writer = &capBuffer{max: 4}
	if _, err := io.Copy(w, bytes.NewReader([]byte("hello"))); err != nil {
		t.Fatal(err)
	}
	if got := w.(*capBuffer).String(); got != "hell" {
		t.Fatalf("kept %q, want %q", got, "hell")
	}
}

// Past the cap, what was dropped is COUNTED. The kept text is a prefix, and a
// caller that hands those lines on (backup prints a helper's tar lines, where
// the `socket ignored` lines are the socket list) can only say the list is cut
// off if the buffer knows that it is.
func TestCapBufferCountsWhatItDropped(t *testing.T) {
	c := &capBuffer{max: 64 << 10}
	if c.truncated() || c.dropped != 0 {
		t.Fatal("an untouched buffer must not report truncation")
	}
	c.Write([]byte(strings.Repeat("a", 64<<10)))
	if c.truncated() {
		t.Fatalf("a write that exactly fills the cap dropped nothing, got dropped=%d", c.dropped)
	}
	// One write straddling the cap, then one wholly past it: both tails count.
	c2 := &capBuffer{max: 64 << 10}
	c2.Write([]byte(strings.Repeat("a", (64<<10)+10)))
	c2.Write([]byte(strings.Repeat("b", 90)))
	if !c2.truncated() || c2.dropped != 100 {
		t.Fatalf("truncated=%v dropped=%d, want true and 100", c2.truncated(), c2.dropped)
	}
	got := c2.capped()
	if !strings.HasSuffix(got, "[byre: 100 more bytes of stderr not shown; output exceeded 64 KiB]") {
		t.Fatalf("capped() must end on the marker naming the count and the cap, got %q", got[len(got)-120:])
	}
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("the marker is ONE line after the kept text, got %d newlines", strings.Count(got, "\n"))
	}
}

// A buffer that held says nothing extra: the marker is a statement about THIS
// child's output, not decoration on every captured stderr.
func TestCapBufferCappedIsSilentWhenNothingWasDropped(t *testing.T) {
	c := &capBuffer{max: 64 << 10}
	c.Write([]byte("  tar: ./sock: socket ignored\n"))
	if got := c.capped(); got != "tar: ./sock: socket ignored" {
		t.Fatalf("capped() = %q, want the trimmed text and no marker", got)
	}
}

// pipeExec is RunHelper's seam, and the stderr it RETURNS is what backup prints
// as tar's own voice. A child that outruns the cap must come back with the
// marker among those lines, so a partial list never reads as a whole one.
func TestPipeExecReturnsTheTruncationMarker(t *testing.T) {
	for _, tool := range []string{"sh", "yes", "head"} {
		testtools.NeedTool(t, tool)
	}
	var out bytes.Buffer
	// 128 KiB of stderr: twice the cap, so the second half is the dropped
	// count the marker has to name.
	stderr, err := pipeExec(nil, &out, "sh", "-c",
		`yes "tar: ./sock: socket ignored" | head -c $((128 * 1024)) >&2`)
	if err != nil {
		t.Fatalf("the child exited 0; pipeExec must not report a failure: %v", err)
	}
	if !strings.Contains(stderr, "socket ignored") {
		t.Fatal("pipeExec must still return what the child said")
	}
	lines := strings.Split(stderr, "\n")
	// 128 KiB written, 64 KiB kept: the marker names the exact tail it dropped.
	if last := lines[len(lines)-1]; last != "[byre: 65536 more bytes of stderr not shown; output exceeded 64 KiB]" {
		t.Fatalf("the last line must be the truncation marker naming the dropped count, got %q", last)
	}
	if n := len(stderr); n > (64<<10)+200 {
		t.Fatalf("returned %d bytes: the cap must still bound what comes back", n)
	}
}
