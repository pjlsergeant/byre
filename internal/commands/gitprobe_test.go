package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A hostile repo can mint output faster than any wall-clock bound stops it;
// the probe must cap stdout, not buffer it (codex residual-hunt, third
// round of the FIFO-class closure).
func TestGitProbeCapsOutput(t *testing.T) {
	dir := t.TempDir()
	stub := `#!/bin/sh
while :; do echo xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx; done
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := gitProbe(filepath.Join(dir, "git"), "config", "--get", "user.email")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unbounded emitter: err = %v, want output-cap error", err)
	}
	// The cap must fire from the READ side — long before the 5s wall clock.
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("cap took %v — the read waited for the timeout instead of capping", d)
	}
}

// No host git to run -- absent, or hostexec declining one resolved out of a
// directory the box writes -- fails the probe immediately rather than
// falling back to whatever PATH answers.
func TestGitProbeWithoutHostGit(t *testing.T) {
	_, err := gitProbe("", "config", "--get", "user.email")
	if !errors.Is(err, errNoHostGit) {
		t.Fatalf("err = %v, want errNoHostGit", err)
	}
}

func TestGitProbeNormalAnswer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\necho fine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := gitProbe(filepath.Join(dir, "git"), "anything")
	if err != nil || strings.TrimSpace(string(out)) != "fine" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
