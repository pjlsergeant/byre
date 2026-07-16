// Package tuitest drives the shipped byre binary (or any argv) inside a
// private tmux server and asserts on captured pane text — the pty-boundary
// tier of the test pyramid (design: the TUI-harness ADR; conventions for
// humans and the future QA agent: docs/BYRE-DEVELOPMENT.md).
//
// The harness deliberately does nothing an agent or a human can't do with
// plain tmux: the same verbs (send-keys, capture-pane, paste-buffer), no
// in-process hooks. WaitFor/WaitForExit are conveniences over a
// capture-pane poll loop and pane_dead_status.
//
// Gating: tests call Require(t). BYRE_TUI_TESTS=1 unset → skip. Gate set
// with tmux missing → FAIL (a configuration error — in CI a silent skip
// would delete the tier unnoticed), except locally where CI is unset the
// failure message names the install.
package tuitest

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Require gates a TUI test: skip without BYRE_TUI_TESTS=1, fail loudly when
// the gate is set but tmux is absent (never a silent skip — an install
// regression must not quietly delete the tier).
func Require(t *testing.T) {
	t.Helper()
	if os.Getenv("BYRE_TUI_TESTS") != "1" {
		t.Skip("set BYRE_TUI_TESTS=1 to run TUI tests (needs tmux)")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatal("BYRE_TUI_TESTS=1 but no tmux on PATH — install tmux (the gate set without the tool is a configuration error, not a skip)")
	}
}

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// Binary builds ./cmd/byre once per test binary and returns the path. The
// build is plain `go build` — the race detector instruments the TEST binary,
// never this child.
func Binary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "byre-tuitest-bin-")
		if err != nil {
			binErr = err
			return
		}
		binPath = filepath.Join(dir, "byre")
		root, err := repoRoot()
		if err != nil {
			binErr = err
			return
		}
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/byre")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			binErr = fmt.Errorf("building byre: %v\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return binPath
}

// repoRoot walks up from the working directory to the go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above the test's working directory")
		}
		dir = parent
	}
}

// Opts shapes a session. The child's environment is the test process's plus
// Env overrides minus Unset — headless-ness and store isolation are things a
// test ENFORCES here (BYRE_HOME, DISPLAY, PATH), never assumes.
type Opts struct {
	Cols, Rows int               // pane geometry; 0 → 100x30
	Env        map[string]string // set in the child (e.g. BYRE_HOME, PATH)
	Unset      []string          // removed from the child (e.g. DISPLAY)
}

// Session is one live pane in a private tmux server. The pane outlives its
// process (remain-on-exit), so the final screen and the exit status stay
// observable however the process ends.
type Session struct {
	t          *testing.T
	socket     string
	statusFile string
}

// Epoch is the pre-action screen, captured by Keys/Type/Paste. WaitForAfter
// uses it for transition semantics: a wanted string already present before
// the action fails the wait immediately (a stale match, not evidence).
type Epoch struct{ before string }

const (
	pollEvery   = 50 * time.Millisecond
	waitDefault = 15 * time.Second
)

var sessionSeq atomic.Int64

// Start launches argv in a fresh private tmux server and returns the session.
// Cleanup kills the server. The server reads no user config (-f /dev/null),
// the status bar is off, and the pane remains after exit.
func Start(t *testing.T, o Opts, argv ...string) *Session {
	t.Helper()
	if o.Cols == 0 {
		o.Cols = 100
	}
	if o.Rows == 0 {
		o.Rows = 30
	}
	// The sequence number keeps repeated Starts within one test (a second
	// byre run against the same boxes) on distinct servers.
	sum := sha256.Sum256([]byte(t.Name()))
	s := &Session{t: t, socket: fmt.Sprintf("byre-tui-%x-%d", sum[:5], sessionSeq.Add(1))}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", s.socket, "kill-server").Run() })

	// A placeholder session first, so remain-on-exit is set before the real
	// process can possibly exit; then the real argv replaces it.
	s.tmux("-f", "/dev/null", "new-session", "-d", "-s", "main",
		"-x", fmt.Sprint(o.Cols), "-y", fmt.Sprint(o.Rows), "sleep 600")
	s.tmux("set-option", "-g", "remain-on-exit", "on")
	s.tmux("set-option", "-g", "status", "off")

	// /usr/bin/env carries the overrides and unsets; tmux hands the command
	// to a shell, so every word is single-quoted.
	cmd := []string{"/usr/bin/env"}
	for _, k := range o.Unset {
		cmd = append(cmd, "-u", k)
	}
	if _, ok := o.Env["TERM"]; !ok {
		cmd = append(cmd, "TERM=xterm-256color")
	}
	for k, v := range o.Env {
		cmd = append(cmd, k+"="+v)
	}
	cmd = append(cmd, argv...)
	// The wrapper records the exact exit status itself: tmux's
	// #{pane_dead_status} proved version-sensitive (ubuntu's 3.4 reported 0
	// where the VM's 3.5a reported the real status — caught by CI on the
	// first push), and the harness gates tests on this value.
	s.statusFile = filepath.Join(t.TempDir(), "exit-status")
	s.tmux("respawn-pane", "-k", "-t", "main",
		quoteJoin(cmd)+"; echo $? > '"+s.statusFile+"'")
	return s
}

// tmux runs one tmux command against the private server, failing the test on
// error — a harness-infrastructure failure is never a mysterious timeout.
func (s *Session) tmux(args ...string) string {
	s.t.Helper()
	out, err := exec.Command("tmux", append([]string{"-L", s.socket}, args...)...).CombinedOutput()
	if err != nil {
		s.t.Fatalf("tmux %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// quoteJoin single-quotes each word for the shell tmux hands the command to.
func quoteJoin(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

// Keys sends key tokens (tmux send-keys names: "Down", "Enter", "C-s", …)
// and returns the pre-action screen as the transition epoch.
func (s *Session) Keys(keys ...string) Epoch {
	s.t.Helper()
	e := Epoch{before: s.CaptureNow()}
	s.tmux(append([]string{"send-keys", "-t", "main"}, keys...)...)
	return e
}

// Type sends literal text (send-keys -l), returning the epoch like Keys.
func (s *Session) Type(text string) Epoch {
	s.t.Helper()
	e := Epoch{before: s.CaptureNow()}
	s.tmux("send-keys", "-t", "main", "-l", text)
	return e
}

// Paste performs a real bracketed paste through tmux's own paste machinery
// (set-buffer + paste-buffer -p) — the negotiation a terminal actually does,
// distinct from raw escape injection (use Type with ESC sequences for parser
// edge cases).
func (s *Session) Paste(text string) Epoch {
	s.t.Helper()
	e := Epoch{before: s.CaptureNow()}
	s.tmux("set-buffer", "--", text)
	s.tmux("paste-buffer", "-p", "-t", "main")
	return e
}

// CaptureNow returns the pane text as it is this instant — a diagnostic
// dump, never a layout oracle.
func (s *Session) CaptureNow() string {
	s.t.Helper()
	return s.tmux("capture-pane", "-p", "-t", "main")
}

// dead reports whether the pane's process has exited, and its status (from
// the wrapper's status file — see Start).
func (s *Session) dead() (bool, int) {
	s.t.Helper()
	b, err := os.ReadFile(s.statusFile)
	trimmed := strings.TrimSpace(string(b))
	if err != nil || trimmed == "" {
		// Absent, or caught between the shell's truncate and its write:
		// still running as far as the harness is concerned.
		return false, 0
	}
	status := 0
	if _, err := fmt.Sscanf(trimmed, "%d", &status); err != nil {
		s.t.Fatalf("unparseable exit status %q in %s", b, s.statusFile)
	}
	return true, status
}

// WaitFor polls until the pane contains substr. A dead process without the
// match fails immediately with the final screen and exit status — never a
// blind timeout. Returns the matching screen.
func (s *Session) WaitFor(substr string) string {
	s.t.Helper()
	deadline := time.Now().Add(waitDefault)
	for {
		screen := s.CaptureNow()
		if strings.Contains(screen, substr) {
			return screen
		}
		if dead, status := s.dead(); dead {
			s.t.Fatalf("process exited (status %d) without %q on screen:\n%s", status, substr, screen)
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("timeout waiting for %q; final screen:\n%s", substr, screen)
		}
		time.Sleep(pollEvery)
	}
}

// WaitForAfter is WaitFor with transition semantics: if substr was already
// on screen when the epoch was taken, the match would be stale, so the test
// fails immediately — wait for its absence first, or assert a more specific
// string.
func (s *Session) WaitForAfter(e Epoch, substr string) string {
	s.t.Helper()
	if strings.Contains(e.before, substr) {
		s.t.Fatalf("%q was already on screen before the action — a wait for it can't prove the action worked; assert a more specific string or wait for its absence first. Pre-action screen:\n%s", substr, e.before)
	}
	return s.WaitFor(substr)
}

// WaitForExit polls until the process exits and returns its status; the pane
// (and its final screen) survives for further assertions.
func (s *Session) WaitForExit() int {
	s.t.Helper()
	deadline := time.Now().Add(waitDefault)
	for {
		if dead, status := s.dead(); dead {
			return status
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("timeout waiting for the process to exit; screen:\n%s", s.CaptureNow())
		}
		time.Sleep(pollEvery)
	}
}
