package runner

import (
	"bytes"
	"io"
	"strings"
	"testing"
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
