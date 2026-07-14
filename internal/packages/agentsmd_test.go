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

// A pre-existing AGENTS.md byre never wrote (agents conventionally create
// one) is preserved as AGENTS.md.bak on takeover -- once: the .bak keeps
// the original, later foreign copies are edits to a byre-owned file. A
// PAST byre version (same title line, different body) is just replaced.
func TestEnsureAgentsMDPreservesForeignFile(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "AGENTS.md")
	bak := path + ".bak"

	if err := os.WriteFile(path, []byte("# my own notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := EnsureStore(home, nil, "test", &out); err != nil {
		t.Fatalf("EnsureStore: %v", err)
	}
	if got, _ := os.ReadFile(bak); string(got) != "# my own notes\n" {
		t.Fatalf("foreign file not preserved as .bak; got %q", got)
	}
	if got, _ := os.ReadFile(path); string(got) != agentsMD {
		t.Fatalf("guide not landed after takeover")
	}
	if !strings.Contains(out.String(), "preserved") {
		t.Fatalf("takeover should say so; got: %q", out.String())
	}

	// Second foreign copy: .bak keeps the ORIGINAL; the new copy is
	// preserved too, under a unique-ified name.
	if err := os.WriteFile(path, []byte("more notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureStore(home, nil, "test", nil); err != nil {
		t.Fatalf("EnsureStore (second foreign): %v", err)
	}
	if got, _ := os.ReadFile(bak); string(got) != "# my own notes\n" {
		t.Fatalf(".bak clobbered by a later foreign copy; got %q", got)
	}
	baks, _ := filepath.Glob(path + ".bak-*")
	if len(baks) != 1 {
		t.Fatalf("expected one unique-ified backup, got %v", baks)
	}
	if got, _ := os.ReadFile(baks[0]); string(got) != "more notes\n" {
		t.Fatalf("second foreign copy not preserved; got %q", got)
	}

	// A stale byre copy (title line intact) is replaced without a .bak.
	os.Remove(bak)
	if err := os.WriteFile(path, []byte(agentsMDTitle+"\nold byre words\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureStore(home, nil, "test", nil); err != nil {
		t.Fatalf("EnsureStore (stale byre copy): %v", err)
	}
	if _, err := os.Lstat(bak); !os.IsNotExist(err) {
		t.Fatalf("stale byre copy must not spawn a .bak")
	}
	if got, _ := os.ReadFile(path); string(got) != agentsMD {
		t.Fatalf("stale byre copy not refreshed")
	}
}

// Preservation is a precondition of the takeover: when the foreign file
// cannot be moved aside, EnsureStore fails and the file stays untouched.
func TestEnsureAgentsMDAbortsWhenPreservationFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	home := t.TempDir()
	path := filepath.Join(home, "AGENTS.md")
	if err := os.WriteFile(path, []byte("# my own notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-create the store dirs so the failure below is the guide's, not
	// the MkdirAll loop's.
	for _, sub := range []string{"skills", "templates", "bundled"} {
		if err := os.MkdirAll(filepath.Join(home, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Read-only store root: the preserving rename (and any write) must fail.
	if err := os.Chmod(home, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(home, 0o755) })

	if err := EnsureStore(home, nil, "test", nil); err == nil {
		t.Fatalf("EnsureStore should fail when it cannot preserve a foreign AGENTS.md")
	}
	if got, _ := os.ReadFile(path); string(got) != "# my own notes\n" {
		t.Fatalf("foreign AGENTS.md touched despite failed preservation: %q", got)
	}
}

// A symlink at AGENTS.md is replaced as a link -- byre must never write
// through it into the target (a notes file, a dotfiles repo, ...).
func TestEnsureAgentsMDNeverWritesThroughSymlink(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "elsewhere.md")
	if err := os.WriteFile(target, []byte("linked notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "AGENTS.md")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := EnsureStore(home, nil, "test", nil); err != nil {
		t.Fatalf("EnsureStore: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "linked notes\n" {
		t.Fatalf("symlink target was written through: %q", got)
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("AGENTS.md should now be a regular file, got mode %v (err %v)", fi.Mode(), err)
	}
	if got, _ := os.ReadFile(path); string(got) != agentsMD {
		t.Fatalf("guide not landed over the symlink")
	}
}

// The guide's load-bearing claims stay pinned to the mechanisms they
// describe: if one of these strings vanishes from the doc, either the
// guidance or the feature moved and the other must follow.
func TestAgentsMDPinsItsClaims(t *testing.T) {
	for _, want := range []string{
		// Ownership contract, first paragraph.
		"byre generates this file and rewrites it",
		// The consent-document rule -- including the full grant model:
		// `agent` and `template` widen the box too, not just the listed
		// grant keys (grok review 2026-07-14).
		"projects/<id>/byre.config",
		"byre preset apply",
		"enables it implicitly",
		"the template (it pulls in a whole config layer)",
		"skills = [...]",
		"or naming it as the layer's",
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
