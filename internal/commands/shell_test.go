package commands

import (
	"errors"
	"strings"
	"testing"
)

func TestShellNoSessionAnywhere(t *testing.T) {
	_, proj := testPaths(t)
	err := shell(discardStreams(), proj, []sessionRunner{&fakeRunner{}, &fakeRunner{}})
	if err == nil || !strings.Contains(err.Error(), "byre develop") {
		t.Fatalf("expected 'no session' error pointing at develop, got %v", err)
	}
}

func TestShellNoEnginesInstalled(t *testing.T) {
	_, proj := testPaths(t)
	if err := shell(discardStreams(), proj, nil); err == nil {
		t.Fatal("expected an error with no engines installed")
	}
}

func TestShellQueryErrorNotMaskedAsNothingRunning(t *testing.T) {
	_, proj := testPaths(t)
	broken := &fakeRunner{liveErr: errors.New("daemon down")}
	err := shell(discardStreams(), proj, []sessionRunner{broken})
	if err == nil || !strings.Contains(err.Error(), "daemon down") {
		t.Fatalf("a broken engine must surface, not read as 'nothing running': %v", err)
	}
}

func TestShellExecsAsContainerDevUser(t *testing.T) {
	p, proj := testPaths(t)
	holder := &fakeRunner{
		live: liveWorkdir(p, "abc123def456"),
		env:  map[string]string{"BYRE_UID": "1234", "BYRE_GID": "5678"},
	}
	// The session lives in the SECOND engine; the first is installed but idle —
	// shell must keep probing rather than stop at the first engine.
	if err := shell(discardStreams(), proj, []sessionRunner{&fakeRunner{}, holder}); err != nil {
		t.Fatal(err)
	}
	want := "abc123def456 1234:5678 /workspace bash -l"
	if len(holder.execs) != 1 || holder.execs[0] != want {
		t.Fatalf("exec = %v, want %q", holder.execs, want)
	}
}

func TestShellFailsClosedWithoutContainerUID(t *testing.T) {
	p, proj := testPaths(t)
	holder := &fakeRunner{
		live: liveWorkdir(p, "abc123def456"),
		env:  map[string]string{}, // no BYRE_UID/BYRE_GID in the container env
	}
	err := shell(discardStreams(), proj, []sessionRunner{holder})
	if err == nil || !strings.Contains(err.Error(), "BYRE_UID") {
		t.Fatalf("expected fail-closed on missing container identity, got %v", err)
	}
	if len(holder.execs) != 0 {
		t.Fatal("must not exec without a valid dev identity")
	}
}

func TestShellNotesMultipleMatches(t *testing.T) {
	p, proj := testPaths(t)
	holder := &fakeRunner{
		live: liveWorkdir(p, "abc123def456", "0123456789ab"),
		env:  map[string]string{"BYRE_UID": "1000", "BYRE_GID": "1000"},
	}
	s, _, errBuf := testStreams("", false)
	if err := shell(s, proj, []sessionRunner{holder}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "2 containers match") {
		t.Errorf("expected a multiple-match note on stderr, got %q", errBuf.String())
	}
}
