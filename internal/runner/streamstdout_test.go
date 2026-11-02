package runner

import (
	"bytes"
	"strings"
	"testing"
)

// WithStreamStdout moves the streamed engine calls' stdout (the build, the
// seeds) to the given writer, so a caller whose stdout is a product keeps it
// clean; the copy keeps the engine and binary.
func TestWithStreamStdoutRoutesStreamedCalls(t *testing.T) {
	var buf bytes.Buffer
	base := New(Docker, "/bin/sh")
	r := base.WithStreamStdout(&buf)
	if err := r.stream("sh", "-c", "echo built"); err != nil {
		t.Fatal(err)
	}
	if err := r.streamIn(strings.NewReader("seeded\n"), "cat"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "built\nseeded\n" {
		t.Errorf("streamed stdout = %q", buf.String())
	}
	if base.engine != r.engine || base.exe != r.exe {
		t.Errorf("the copy must keep the engine and binary: %+v", r)
	}
}
