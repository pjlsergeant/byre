package packages

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// EnsureStore lands the byre-owned AGENTS.md at the store root and keeps it
// current: absent -> written, edited -> restored, current -> untouched
// (no notice, so callers don't nag on every command).
func TestEnsureStoreLandsAgentsMD(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "AGENTS.md")

	var out bytes.Buffer
	if err := EnsureStore(home, nil, "test", &out); err != nil {
		t.Fatalf("EnsureStore: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("AGENTS.md not written: %v", err)
	}
	if string(got) != agentsMD {
		t.Fatalf("AGENTS.md content differs from the binary's copy")
	}
	if !strings.Contains(out.String(), "AGENTS.md") {
		t.Fatalf("first write should notice; got: %q", out.String())
	}

	// Current copy: untouched, and no notice.
	out.Reset()
	if err := EnsureStore(home, nil, "test", &out); err != nil {
		t.Fatalf("EnsureStore (repeat): %v", err)
	}
	if strings.Contains(out.String(), "AGENTS.md") {
		t.Fatalf("unchanged file must not re-notice; got: %q", out.String())
	}

	// Edited copy: restored (the stated byre-owned contract).
	if err := os.WriteFile(path, []byte("my notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := EnsureStore(home, nil, "test", &out); err != nil {
		t.Fatalf("EnsureStore (after edit): %v", err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != agentsMD {
		t.Fatalf("edited AGENTS.md was not restored")
	}
	if !strings.Contains(out.String(), "AGENTS.md") {
		t.Fatalf("restore should notice; got: %q", out.String())
	}
}

// The guide's load-bearing claims stay pinned to the mechanisms they
// describe: if one of these strings vanishes from the doc, either the
// guidance or the feature moved and the other must follow.
func TestAgentsMDPinsItsClaims(t *testing.T) {
	for _, want := range []string{
		// Ownership contract, first paragraph.
		"byre generates this file and rewrites it",
		// The consent-document rule.
		"projects/<id>/byre.config",
		"byre preset apply",
		// Immutability + the sanctioned escape.
		"NEVER edit anything here",
		"byre skill fork",
		// The version-control ruling: drawer-level git, never whole-store.
		"Do NOT `git init` this directory as a whole",
		"skips dot-directories",
		// Distribution.
		"[sources]",
		"--digest",
	} {
		if !strings.Contains(agentsMD, want) {
			t.Errorf("AGENTS.md lost its claim %q", want)
		}
	}
}
