package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/testtools"
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

// The deadline must reach the whole process GROUP. CommandContext kills the
// direct child only, so a descendant holding the stdout pipe keeps the read
// blocked past the deadline that was supposed to end it -- exactly the wedge
// the bound exists to prevent.
func TestGitProbeKillsTheWholeGroup(t *testing.T) {
	dir := t.TempDir()
	// The stub prints, then exits the wait only when the backgrounded sleep
	// does; the sleep inherits stdout and would hold the pipe open for a minute.
	stub := `#!/bin/sh
echo x
sleep 60 &
wait
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := gitProbeBounded(150*time.Millisecond, filepath.Join(dir, "git"), "status")
	if err == nil {
		t.Fatal("a probe that outlives its deadline must be an error")
	}
	// Under gitProbeWaitDelay, not merely finite: a group that died closes the
	// pipes at once, so a probe that takes the full delay to return is one
	// WaitDelay rescued rather than the group kill.
	if el := time.Since(start); el >= gitProbeWaitDelay {
		t.Fatalf("the probe took %s to return: the group was not killed, WaitDelay was", el)
	}
}

// A descendant that leaves the group (setsid) and holds stdout is not
// reached by the group SIGKILL; boundPipe's delayed close of the read end
// is what unblocks the probe.
func TestGitProbeBoundsADescendantThatLeftTheGroup(t *testing.T) {
	testtools.NeedTool(t, "setsid")
	dir := t.TempDir()
	stub := `#!/bin/sh
echo x
setsid sh -c 'sleep 60'
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := gitProbeBounded(150*time.Millisecond, filepath.Join(dir, "git"), "status")
	if err == nil {
		t.Fatal("a probe whose descendant left the group and holds stdout must be an error")
	}
	if el := time.Since(start); el >= 2*gitProbeWaitDelay {
		t.Fatalf("the probe took %s to return: the pipe close did not bound the escaped descendant", el)
	}
}
