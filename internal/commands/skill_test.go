package commands

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Bundled inspect shows a computed display digest (ADR 0029 deferral closed):
// same line shape as installed rows, stable across invocations.
func TestInspectBundledShowsDisplayDigest(t *testing.T) {
	installHome(t)
	s, out, _ := testStreams("", false)
	if err := SkillInspect(s, "claude"); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`Digest:\s+sha256:([0-9a-f]{64})\n`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no display digest in bundled inspect output:\n%s", out.String())
	}
	s2, out2, _ := testStreams("", false)
	if err := SkillInspect(s2, "claude"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2.String(), m[1]) {
		t.Errorf("display digest must be stable across inspects; first %s, second:\n%s", m[1], out2.String())
	}
}

// Fork's tree copy is judged at the descriptor: a symlink is the user's own
// arrangement of their store, so it is followed and its target's bytes are
// materialized as a regular file, while a FIFO fails loudly instead of
// hanging the copy.
func TestCopyDirFollowsLinksRefusesIrregulars(t *testing.T) {
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte("linked bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "skill.toml"), []byte("# ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(src, "extra")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "dst")
	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir must follow a user symlink: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "extra"))
	if err != nil || string(b) != "linked bytes" {
		t.Fatalf("fork should materialize the link target's bytes, got %q, %v", b, err)
	}
	if fi, err := os.Lstat(filepath.Join(dst, "extra")); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("materialized copy must be a regular file, got %v, %v", fi, err)
	}

	src2 := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(src2, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	dst2 := filepath.Join(t.TempDir(), "dst")
	done := make(chan error, 1)
	go func() { done <- copyDir(src2, dst2) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want FIFO refusal")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copyDir blocked on a FIFO — the exact hang fork must never have")
	}
}

// A failed fork must leave NOTHING at the destination: copying into the
// final name left a partial tree that poisoned retries with "already
// exists" and could carry the source's identity under the fork's path
// (external review, 2026-07-19). The fork stages beside the destination
// and publishes with one rename.
func TestForkFailureLeavesNoDestination(t *testing.T) {
	home := installHome(t)
	srcDir := filepath.Join(home, "skills", "pete", "tool")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "skill.toml"), []byte("description = \"local\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(srcDir, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	s, _, _ := testStreams("", false)
	if err := SkillFork(s, "pete/tool", "me/fork"); err == nil {
		t.Fatal("fork of a FIFO-bearing source must fail")
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "me", "fork")); !os.IsNotExist(err) {
		t.Fatalf("failed fork left a destination behind: %v", err)
	}
	ents, _ := os.ReadDir(filepath.Join(home, "skills", "me"))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".fork-stage-") {
			t.Fatalf("failed fork left staging residue %s", e.Name())
		}
	}
}

// The staged fork still publishes: destination exists afterwards with the
// fork's own identity in the rewritten primary.
func TestForkPublishesStagedTree(t *testing.T) {
	home := installHome(t)
	srcDir := filepath.Join(home, "skills", "pete", "tool")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "skill.toml"), []byte("description = \"local\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _, _ := testStreams("", false)
	if err := SkillFork(s, "pete/tool", "me/fork"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, "skills", "me", "fork", "skill.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `id = "me/fork"`) || !strings.Contains(string(b), "Forked from pete/tool") {
		t.Fatalf("published primary must carry the fork identity, got:\n%s", b)
	}
}
