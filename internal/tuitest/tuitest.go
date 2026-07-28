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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/hostopen"
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
//
// The build dir must outlive the single test that triggered it (sync.Once,
// shared by every test in the process), so t.TempDir can't own it and no one
// removes it at exit. Instead the dir name carries this process's pid and
// each call reaps siblings whose owner is gone — otherwise ~18 MB per gated
// run accumulates until the tmpfs fills and the suite fails on link errors.
func Binary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		reapStaleBinDirs()
		dir, err := hostopen.PlainMkdirTemp("", fmt.Sprintf("byre-tuitest-bin-%d-", os.Getpid()), hostopen.TestHarness)
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
			return
		}
		binErr = recordProductSources(root)
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return binPath
}

// productSourceFields are the per-package `go list` fields naming files the
// compiler and linker consume. Restricting this to GoFiles plus EmbedFiles
// would leave real build inputs — cgo, assembly, headers, prebuilt objects —
// out of the key, so the list is the full input set rather than the subset
// this repo happens to use today.
var productSourceFields = []string{
	"GoFiles", "CgoFiles", "SFiles", "CFiles", "CXXFiles", "MFiles", "FFiles",
	"HFiles", "SwigFiles", "SwigCXXFiles", "SysoFiles", "EmbedFiles",
}

// recordProductSources puts the built binary's own inputs into Go's test
// cache key for this package, so a product edit re-runs the tier instead of
// replaying a cached pass.
//
// Nothing else does it. The binary is built in a SUBPROCESS, so the import
// graph carries no edge to it, and the blank imports in productdeps_test.go
// are not enough: `go test` keys a cached result on the test binary's
// CONTENT, and the linker drops blank-imported code nothing references, so
// editing a configui screen leaves the test binary byte-identical and the
// whole pty tier reports "(cached)". What `go test` re-checks instead is
// what the test PROCESS touched inside the module root, which is also the
// only edge that can reach cmd/byre at all — package main, unimportable.
//
// Two kinds of input, and the difference is the whole design (cmd/go's
// hashOpen is the reference):
//
//   - A file contributes its SIZE and MTIME, not its bytes. Every edit to a
//     listed file therefore invalidates.
//   - A directory contributes its ENTRY LIST — each child's name, size,
//     mode and mtime. So opening the directories too is what catches a file
//     being ADDED or REMOVED, which no per-file record can see: on a cache
//     hit nothing runs, so `go list` is never consulted and the recorded
//     set is last run's. The sharp case is a new file under an existing
//     //go:embed root (a new bundled skill tree, zero .go edits) — bundled
//     into the binary, invisible to every source file's mtime.
//
// Hence: read every listed file, and open every directory from each file's
// own up to its package's, which covers embed trees at any depth. A brand
// new PACKAGE needs no special handling — it joins the build only when an
// existing file imports it, and that import edit is itself a file change.
//
// The module root is deliberately NOT walked to: its entry list carries
// .git, whose mtime moves on every commit, and the tier would then re-run
// for reasons that have nothing to do with the product. Files outside the
// module root (stdlib, module cache) are skipped because `go test` ignores
// them, and stat-ing them on every later invocation is pure cost.
func recordProductSources(root string) error {
	var tmpl strings.Builder
	tmpl.WriteString("{{$d := .Dir}}")
	for _, field := range productSourceFields {
		// pkg dir, tab, file path -- the pkg dir bounds the walk upward.
		fmt.Fprintf(&tmpl, "{{range .%s}}{{$d}}\t{{$d}}/{{.}}\n{{end}}", field)
	}
	cmd := exec.Command("go", "list", "-deps", "-f", tmpl.String(), "./cmd/byre")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("listing the product's sources for the test-cache key: %v", err)
	}

	prefix := root + string(filepath.Separator)
	dirs := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		pkgDir, file, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasPrefix(file, prefix) {
			continue
		}
		// A listed source that cannot be read after a successful build is a
		// broken invariant, not a degrade case: carrying on would leave the
		// key quietly weaker than it claims to be.
		if _, err := hostopen.PlainReadFile(file, hostopen.TestHarness); err != nil {
			return fmt.Errorf("reading %s for the test-cache key: %w", file, err)
		}
		for d := filepath.Dir(file); strings.HasPrefix(d, prefix); d = filepath.Dir(d) {
			dirs[d] = true
			if d == pkgDir {
				break
			}
		}
	}
	for d := range dirs {
		if _, err := hostopen.PlainReadDir(d, hostopen.TestHarness); err != nil {
			return fmt.Errorf("listing %s for the test-cache key: %w", d, err)
		}
	}
	return nil
}

// reapStaleBinDirs removes byre-tuitest-bin-<pid>-* dirs whose owning process
// is gone. Best-effort: a dir that won't parse or won't remove is skipped
// (legacy pid-less dirs from before this scheme age out by hand), and a live
// sibling — a concurrent package's test binary — is left alone. Runs before
// this process creates its own dir, so a dir carrying OUR pid is a previous
// pid incarnation's and is removed unconditionally — the liveness probe would
// misread it as alive. Death is proven only by "no such process" (ESRCH /
// ErrProcessDone); any other probe result (e.g. EPERM: alive, different
// user) keeps the dir.
func reapStaleBinDirs() {
	entries, err := hostopen.PlainReadDir(os.TempDir(), hostopen.TestHarness)
	if err != nil {
		return
	}
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), "byre-tuitest-bin-")
		if !ok || !e.IsDir() {
			continue
		}
		pidStr, _, ok := strings.Cut(rest, "-")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid <= 0 {
			continue
		}
		if pid != os.Getpid() {
			proc, ferr := os.FindProcess(pid)
			if ferr != nil {
				continue
			}
			serr := proc.Signal(syscall.Signal(0))
			if !errors.Is(serr, os.ErrProcessDone) && !errors.Is(serr, syscall.ESRCH) {
				continue // running, or not provably dead — keep
			}
		}
		hostopen.PlainRemoveAll(filepath.Join(os.TempDir(), e.Name()), hostopen.TestHarness)
	}
}

// repoRoot walks up from the working directory to the go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := hostopen.PlainStat(filepath.Join(dir, "go.mod"), hostopen.TestHarness); err == nil {
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
	Dir        string            // child working directory (a cd in the pane's shell — BSD env has no -C)
	RecordTo   string            // demo recording: attach an asciinema spectator, cast written here (see demo.go)
}

// Session is one live pane in a private tmux server. The pane outlives its
// process (remain-on-exit), so the final screen and the exit status stay
// observable however the process ends.
type Session struct {
	t          *testing.T
	socket     string
	statusFile string
	rec        *exec.Cmd // the asciinema spectator, when recording (demo.go)
	castPath   string
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
	// The dead-pane banner would be one extra line of output: on a full pane
	// it scrolls the top line away (found live: `byre status` + banner = 31
	// rows), corrupting final-screen assertions and recorded demo frames.
	// Best-effort — an ancient tmux without the option just keeps the banner.
	_ = exec.Command("tmux", "-L", s.socket, "set-option", "-g", "remain-on-exit-format", "").Run()

	// The spectator must be attached before the real process can paint its
	// first frame — the placeholder session exists exactly so this ordering
	// is possible.
	if o.RecordTo != "" {
		s.startRecorder(o.RecordTo, o.Cols, o.Rows)
	}

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
	run := quoteJoin(cmd)
	if o.Dir != "" {
		// The cd stays INSIDE the status-recording wrapper: a failed cd
		// writes its own exit status instead of leaving WaitForExit to a
		// timeout with an empty file.
		run = "cd " + quoteJoin([]string{o.Dir}) + " && " + run
	}
	s.tmux("respawn-pane", "-k", "-t", "main",
		run+"; echo $? > "+quoteJoin([]string{s.statusFile}))
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
	b, err := hostopen.PlainReadFile(s.statusFile, hostopen.TestHarness)
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

// scanOutcome is scanUntil's verdict.
type scanOutcome int

const (
	scanFound   scanOutcome = iota
	scanDied                // process exited and the settle window closed without a match
	scanTimeout             // deadline passed with the process still alive
)

// scanUntil is WaitFor's loop with its probes injected — capture reads the
// pane, dead reads the exit-status file, sleep paces the polls. Injection
// exists because the one subtle branch is untestable against real tmux: the
// status file can land BEFORE tmux renders the process's final pty writes,
// so on observed death the scan RE-captures for settleFor before ruling the
// text absent (the CI integration flake, 2026-07-19 and 2026-07-25:
// deliver's engine error painted right as the process exited, and ruling on
// the pre-death capture misreported it). The settle only rescues bytes
// written BEFORE exit and rendered late — tmux drops pty writes from
// survivors of a dead pane (verified live), which is fine: a single process
// can't write after exiting. Only the genuine-failure path pays the wait.
func scanUntil(capture func() string, dead func() (bool, int), substr string,
	deadline time.Time, settleFor time.Duration, sleep func(time.Duration)) (string, scanOutcome, int) {
	for {
		screen := capture()
		if strings.Contains(screen, substr) {
			return screen, scanFound, 0
		}
		if isDead, status := dead(); isDead {
			settle := time.Now().Add(settleFor)
			for {
				screen = capture()
				if strings.Contains(screen, substr) {
					return screen, scanFound, 0
				}
				if time.Now().After(settle) {
					return screen, scanDied, status
				}
				sleep(pollEvery)
			}
		}
		if time.Now().After(deadline) {
			return screen, scanTimeout, 0
		}
		sleep(pollEvery)
	}
}

// WaitFor polls until the pane contains substr. A dead process without the
// match fails with the final screen and exit status — never a blind
// timeout. Returns the matching screen.
func (s *Session) WaitFor(substr string) string {
	s.t.Helper()
	screen, outcome, status := scanUntil(s.CaptureNow, s.dead, substr,
		time.Now().Add(waitDefault), 2*time.Second, time.Sleep)
	switch outcome {
	case scanDied:
		s.t.Fatalf("process exited (status %d) without %q on screen:\n%s", status, substr, screen)
	case scanTimeout:
		s.t.Fatalf("timeout waiting for %q; final screen:\n%s", substr, screen)
	}
	return screen
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
