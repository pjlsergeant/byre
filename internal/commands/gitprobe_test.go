package commands

import (
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
	t.Setenv("PATH", dir)
	start := time.Now()
	_, err := gitProbe("config", "--get", "user.email")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unbounded emitter: err = %v, want output-cap error", err)
	}
	// The cap must fire from the READ side — long before the 5s wall clock.
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("cap took %v — the read waited for the timeout instead of capping", d)
	}
}

func TestGitProbeNormalAnswer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\necho fine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	out, err := gitProbe("anything")
	if err != nil || strings.TrimSpace(string(out)) != "fine" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
