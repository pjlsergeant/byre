package tuitest

import (
	"strings"
	"testing"
	"time"
)

// The fast-exit race, pinned deterministically at the logic level (real tmux
// timing can't be ordered from a test): the status file lands before tmux
// renders the process's final pty writes, so the first capture lacks the
// text, death is already observable, and the text appears only on a LATER
// capture. scanUntil must keep re-capturing through the settle window and
// find it — ruling on the pre-death capture misreported fast exits as "died
// without the message" (the CI integration flake, 2026-07-19 / 2026-07-25).
func TestScanUntilSettlesAfterDeath(t *testing.T) {
	deadline := time.Now().Add(time.Minute)

	// Text renders two captures after death is first observed.
	captures := []string{"", "", "late-painted text arrives"}
	i := 0
	capture := func() string {
		if i < len(captures)-1 {
			s := captures[i]
			i++
			return s
		}
		return captures[len(captures)-1]
	}
	dead := func() (bool, int) { return true, 1 }
	noSleep := func(time.Duration) {}

	screen, outcome, _ := scanUntil(capture, dead, "late-painted", deadline, time.Second, noSleep)
	if outcome != scanFound || !strings.Contains(screen, "late-painted") {
		t.Fatalf("settle must catch a late-rendered paint: outcome=%d screen=%q", outcome, screen)
	}

	// Genuinely absent text still reports the death (with its status), just
	// after the settle window — never a false match, never a blind timeout.
	empty := func() string { return "" }
	start := time.Now()
	_, outcome, status := scanUntil(empty, dead, "never-appears", deadline, 50*time.Millisecond, time.Sleep)
	if outcome != scanDied || status != 1 {
		t.Fatalf("absent text must report the death: outcome=%d status=%d", outcome, status)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the settle window must be bounded")
	}

	// A live process past the deadline is a timeout, not a death.
	alive := func() (bool, int) { return false, 0 }
	_, outcome, _ = scanUntil(empty, alive, "never-appears", time.Now().Add(-time.Second), time.Second, noSleep)
	if outcome != scanTimeout {
		t.Fatalf("expired deadline with a live process must time out: outcome=%d", outcome)
	}
}

// Integration smoke for the same race against real tmux: a fast exit right
// after painting must still match. Not a deterministic pin — tmux usually
// renders in time even without the settle — but it exercises the death
// branch end to end on a real pty (the deterministic pin is
// TestScanUntilSettlesAfterDeath; constructing lateness via a backgrounded
// child does NOT work — tmux drops pty writes from survivors of a dead
// pane, verified live 2026-07-26).
func TestIntegrationWaitForFastExitStillMatches(t *testing.T) {
	Require(t)
	s := Start(t, Opts{}, "/bin/sh", "-c", "echo final-breath-text; exit 3")
	screen := s.WaitFor("final-breath-text")
	if !strings.Contains(screen, "final-breath-text") {
		t.Fatalf("returned screen lost the text:\n%s", screen)
	}
	if st := s.WaitForExit(); st != 3 {
		t.Fatalf("exit = %d, want 3", st)
	}
}
