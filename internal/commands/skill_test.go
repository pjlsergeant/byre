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

// Fork's tree copy follows pack's rule: packages carry files, not links. A
// symlink in the source must not have its target's bytes materialized into
// the fork (a cloned skill could link into credentials), and a FIFO must
// fail loudly instead of hanging the copy.
func TestCopyDirRefusesLinksAndIrregulars(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("hunter2"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "skill.toml"), []byte("# ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(src, "creds")); err != nil {
		t.Fatal(err)
	}
	err := copyDir(src, filepath.Join(t.TempDir(), "dst"))
	if err == nil || !strings.Contains(err.Error(), "not links") {
		t.Fatalf("want symlink refusal, got %v", err)
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
