package gen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/testtools"
)

// headlessCase is one run of the REAL embedded byre-headless against a stub
// headless command passed as the positional test seam.
type headlessCase struct {
	stub    string   // the stub's bash body
	env     []string // extra environment, after the scrub and the defaults
	task    string   // task.md content (default "the task\n")
	bound   time.Duration
	prepare func(t *testing.T, ws, runDir string) // after staging, before the run
	during  func(t *testing.T, cmd *exec.Cmd)     // after start, before wait
	stdin   *os.File
	// pathFirst, when set, is a dir under the case's workspace put FIRST on
	// the chassis's inherited PATH.
	pathFirst string
}

type headlessResult struct {
	code   int
	stdout string // the chassis's fd 1 (the launcher moved it to stderr; never written)
	stderr string
	fd9    string // the box's real stdout
	runDir string
	ws     string
}

// headlessEnv scrubs the BYRE_RUN_* contract and BYRE_HEADLESS from the
// environment the suite inherits (it runs inside a byre box, which may one
// day be a headless run itself) and adds the case's defaults.
func headlessEnv(extra ...string) []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "BYRE_RUN_") || strings.HasPrefix(e, "BYRE_HEADLESS=") {
			continue
		}
		env = append(env, e)
	}
	return append(env, extra...)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func runHeadless(t *testing.T, c headlessCase) headlessResult {
	t.Helper()
	testtools.NeedTool(t, "bash", "setsid", "sha256sum", "tee", "mktemp", "awk")
	ws := t.TempDir()
	runDir := filepath.Join(ws, "run")
	if err := os.Mkdir(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	task := c.task
	if task == "" {
		task = "the task\n"
	}
	if err := os.WriteFile(filepath.Join(runDir, "task.md"), []byte(task), 0o644); err != nil {
		t.Fatal(err)
	}
	chassis := filepath.Join(ws, "byre-headless")
	if err := os.WriteFile(chassis, HeadlessScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(ws, "stub")
	if err := os.WriteFile(stub, []byte("#!/usr/bin/env bash\n"+c.stub+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if c.prepare != nil {
		c.prepare(t, ws, runDir)
	}
	fd9, err := os.Create(filepath.Join(ws, "fd9"))
	if err != nil {
		t.Fatal(err)
	}
	defer fd9.Close()

	bound := c.bound
	if bound == 0 {
		bound = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", chassis, stub)
	cmd.WaitDelay = 2 * time.Second
	env := []string{
		"BYRE_RUN_DIR=" + runDir,
		"BYRE_RUN_TASK_FILE=" + filepath.Join(runDir, "task.md"),
		"BYRE_RUN_TASK_SHA256=" + sha256Hex([]byte(task)),
		"BYRE_RUN_ID=0123abcd",
		"BYRE_RUN_AGENT=stubagent",
		"STUB_WS=" + ws,
	}
	if c.pathFirst != "" {
		env = append(env, "PATH="+filepath.Join(ws, c.pathFirst)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	cmd.Env = headlessEnv(append(env, c.env...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if c.stdin != nil {
		cmd.Stdin = c.stdin
	}
	// ExtraFiles[i] is fd 3+i; the nil entries start closed, as they are
	// when the launcher execs the chassis.
	cmd.ExtraFiles = []*os.File{nil, nil, nil, nil, nil, nil, fd9}
	if err := cmd.Start(); err != nil {
		t.Fatalf("chassis did not start: %v", err)
	}
	if c.during != nil {
		c.during(t, cmd)
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		t.Fatalf("chassis did not finish within %v (blocked?)\nstderr:\n%s", bound, stderr.String())
	}
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("chassis did not run: %v", err)
		}
		code = ee.ExitCode()
	}
	got, err := os.ReadFile(filepath.Join(ws, "fd9"))
	if err != nil {
		t.Fatal(err)
	}
	return headlessResult{code: code, stdout: stdout.String(), stderr: stderr.String(), fd9: string(got), runDir: runDir, ws: ws}
}

// resultJSON decodes the result dir's result.json, failing on invalid JSON:
// the strict escaper is what keeps it parseable.
func (r headlessResult) resultJSON(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.runDir, "result.json"))
	if err != nil {
		t.Fatalf("reading result.json: %v", err)
	}
	return decodeResultJSON(t, b)
}

func decodeResultJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("result.json is not JSON: %v\n%s", err, b)
	}
	return m
}

// jsonInt reads an integer field; ok is false for null.
func jsonInt(t *testing.T, m map[string]any, key string) (int, bool) {
	t.Helper()
	v, present := m[key]
	if !present {
		t.Fatalf("result.json has no %q: %v", key, m)
	}
	if v == nil {
		return 0, false
	}
	f, isNum := v.(float64)
	if !isNum {
		t.Fatalf("result.json %q = %v, want a number or null", key, v)
	}
	return int(f), true
}

// The exit code is a CLOSED set: the stub's status maps to byre's verdict,
// and anything outside the contract -- a raw 12 or 16 included -- is the
// script failing (14), never passed up.
func TestHeadlessExitMap(t *testing.T) {
	const answer = `printf 'the answer' >"$BYRE_RUN_RESULT_FILE"; `
	backendExit := func(n string) string {
		return `echo backend_exit=` + n + ` >>"$BYRE_RUN_STATUS_FILE"; exit 11`
	}
	cases := []struct {
		name        string
		stub        string
		code        int
		status      string
		backendExit int // -1 = null
		scriptExit  int // -1 = null
	}{
		{"success", answer + "exit 0", 0, "success", -1, -1},
		{"zero without a result", "exit 0", 14, "script_failed", -1, 0},
		{"agent reported failure", "exit 10", 10, "agent_error", -1, -1},
		{"agent exit 10", backendExit("10"), 11, "agent_exit", 10, -1},
		{"agent exit 13", backendExit("13"), 11, "agent_exit", 13, -1},
		{"agent exit 124", backendExit("124"), 11, "agent_exit", 124, -1},
		{"agent exit 137", backendExit("137"), 11, "agent_exit", 137, -1},
		{"agent exit without backend_exit", "exit 11", 11, "agent_exit", -1, -1},
		{"agent exit with a malformed backend_exit", backendExit("1234"), 11, "agent_exit", -1, -1},
		{"credential preflight", "exit 13", 13, "no_credentials", -1, -1},
		{"backend absent", "exit 15", 15, "backend_unavailable", -1, -1},
		{"script exit 1", "exit 1", 14, "script_failed", -1, 1},
		{"script exit 124", "exit 124", 14, "script_failed", -1, 124},
		{"script exit 137", "exit 137", 14, "script_failed", -1, 137},
		{"raw 12 is not a timeout", "exit 12", 14, "script_failed", -1, 12},
		{"raw 16 is not a result-dir refusal", "exit 16", 14, "script_failed", -1, 16},
		{"killed by a signal", "kill -KILL $$", 14, "script_failed", -1, 137},
		{"set -e death", "set -e; false; exit 0", 14, "script_failed", -1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := runHeadless(t, headlessCase{stub: tc.stub})
			if r.code != tc.code {
				t.Fatalf("exit %d, want %d\nstderr:\n%s", r.code, tc.code, r.stderr)
			}
			m := r.resultJSON(t)
			if m["status"] != tc.status {
				t.Errorf("result.json status = %v, want %s", m["status"], tc.status)
			}
			if got, _ := jsonInt(t, m, "exit_code"); got != tc.code {
				t.Errorf("result.json exit_code = %d, want %d", got, tc.code)
			}
			for key, want := range map[string]int{"backend_exit_code": tc.backendExit, "script_exit_code": tc.scriptExit} {
				got, ok := jsonInt(t, m, key)
				switch {
				case want < 0 && ok:
					t.Errorf("result.json %s = %d, want null", key, got)
				case want >= 0 && (!ok || got != want):
					t.Errorf("result.json %s = %d (set=%v), want %d", key, got, ok, want)
				}
			}
			if !strings.Contains(r.stderr, "exit "+strconv.Itoa(tc.code)) {
				t.Errorf("no summary line naming exit %d on stderr:\n%s", tc.code, r.stderr)
			}
		})
	}
}

// A headless command that is not there is the backend-absent verdict, with
// result.json saying so -- the run still publishes its outcome.
func TestHeadlessAbsentCommandIs15(t *testing.T) {
	r := runHeadless(t, headlessCase{
		stub:    "exit 0",
		prepare: func(t *testing.T, ws, _ string) { mustRemove(t, filepath.Join(ws, "stub")) },
	})
	if r.code != 15 {
		t.Fatalf("exit %d, want 15\nstderr:\n%s", r.code, r.stderr)
	}
	if m := r.resultJSON(t); m["status"] != "backend_unavailable" {
		t.Errorf("result.json status = %v, want backend_unavailable", m["status"])
	}
}

func mustRemove(t *testing.T, p string) {
	t.Helper()
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
}

// The chassis's own timer: past BYRE_RUN_TIMEOUT the verdict is 12 and the
// headless command's whole process group is gone, a background grandchild
// included.
//
// The TERM-ignoring arm waits out the 15 s KILL follow-up, so both arms run
// in parallel with the rest of the package.
func TestHeadlessTimeoutKillsTheGroup(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, prelude string }{
		{"obeys TERM", ""},
		{"ignores TERM", "trap '' TERM\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := runHeadless(t, headlessCase{
				bound: 40 * time.Second,
				env:   []string{"BYRE_RUN_TIMEOUT=1s"},
				stub: tc.prelude + `echo $$ >"$STUB_WS/pgid"
sleep 60 &
while :; do wait; done`,
			})
			if r.code != 12 {
				t.Fatalf("exit %d, want 12\nstderr:\n%s", r.code, r.stderr)
			}
			m := r.resultJSON(t)
			if m["status"] != "timeout" || m["timeout"] != "1s" {
				t.Errorf("result.json status/timeout = %v/%v, want timeout/1s", m["status"], m["timeout"])
			}
			b, err := os.ReadFile(filepath.Join(r.ws, "pgid"))
			if err != nil {
				t.Fatal(err)
			}
			pgid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil {
				t.Fatal(err)
			}
			if live := liveInGroup(t, pgid); len(live) > 0 {
				t.Errorf("the headless command's group %d survived the timeout: pids %v", pgid, live)
			}
		})
	}
}

// USR1 is the timer's signal, but the agent shares the chassis's uid and can
// send it: an early USR1 -- with no timer at all, or before the deadline --
// leaves the headless command's own verdict standing, never a forged 12. The
// real timer still times the run out, whatever the duration's spelling.
//
// The deadline is exact, not whole seconds: the "late" forger signals 1.2 s
// into a 1.9 s timeout, past any whole-second floor of it, and must still be
// ignored. It exits at once after signalling, so 0.7 s separate its verdict
// from the real timer's.
func TestHeadlessForgedUSR1IsNotATimeout(t *testing.T) {
	t.Parallel()
	// The stub's parent is the chassis: setsid(1) execs in place.
	const forger = `kill -USR1 $PPID
printf 'own answer' >"$BYRE_RUN_RESULT_FILE"`
	const lateForger = "sleep 1.2\n" + forger
	for _, tc := range []struct {
		name, stub string
		env        []string
	}{
		{"no timer", forger, nil},
		{"before the deadline", forger, []string{"BYRE_RUN_TIMEOUT=30s"}},
		{"before a whole-second deadline", forger, []string{"BYRE_RUN_TIMEOUT=5"}},
		{"before a fractional deadline", forger, []string{"BYRE_RUN_TIMEOUT=1.9s"}},
		{"late, inside the deadline's final second", lateForger, []string{"BYRE_RUN_TIMEOUT=1.9s"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := runHeadless(t, headlessCase{stub: tc.stub, env: tc.env})
			if r.code != 0 || r.fd9 != "own answer" {
				t.Fatalf("exit %d fd9 %q, want 0 and the stub's answer (a forged USR1 decided the verdict)\nstderr:\n%s", r.code, r.fd9, r.stderr)
			}
		})
	}
	for _, spec := range []string{"1", "1.5", "0.02m"} {
		t.Run("real timer "+spec, func(t *testing.T) {
			t.Parallel()
			r := runHeadless(t, headlessCase{
				env:  []string{"BYRE_RUN_TIMEOUT=" + spec},
				stub: `sleep 60`,
			})
			if r.code != 12 {
				t.Fatalf("exit %d, want 12\nstderr:\n%s", r.code, r.stderr)
			}
		})
	}
}

// liveInGroup lists the processes in group pgid that are not zombies. A
// zombie is dead: outside a container the killed grandchild is reparented to
// a reaper the test does not control, which may not have collected it yet.
func liveInGroup(t *testing.T, pgid int) []int {
	t.Helper()
	stats, err := filepath.Glob("/proc/[0-9]*/stat")
	if err != nil {
		t.Fatal(err)
	}
	var live []int
	for _, p := range stats {
		b, err := os.ReadFile(p)
		if err != nil {
			continue // exited while we looked
		}
		// pid (comm) state ppid pgrp ...; comm may hold spaces and parens.
		s := string(b)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+1:])
		if len(f) < 3 || f[0] == "Z" || f[2] != strconv.Itoa(pgid) {
			continue
		}
		pid, _ := strconv.Atoi(strings.Fields(s)[0])
		live = append(live, pid)
	}
	return live
}

// childNamesProbe lists the process names of the chassis's children (the
// stub's parent) into $STUB_WS/children, then exits 0 with an answer after
// a sleep that would outlive any accidental default bound.
const childNamesProbe = `for d in /proc/[0-9]*; do
  [ "$(awk '/^PPid:/{print $2}' "$d/status" 2>/dev/null)" = "$PPID" ] || continue
  [ "${d#/proc/}" = "$$" ] && continue
  awk '/^Name:/{print $2}' "$d/status" 2>/dev/null
done >"$STUB_WS/children"
sleep 2
printf done >"$BYRE_RUN_RESULT_FILE"`

// No BYRE_RUN_TIMEOUT means no bound at all: no timer process exists while
// the headless command runs, and a sleeping one is not killed. The same
// probe with a timeout set sees the timer -- the control that proves the
// probe can see one.
func TestHeadlessNoTimeoutMeansNoTimer(t *testing.T) {
	children := func(t *testing.T, env ...string) []string {
		r := runHeadless(t, headlessCase{stub: childNamesProbe, env: env})
		if r.code != 0 {
			t.Fatalf("exit %d, want 0 (a sleeping headless command must not be killed)\nstderr:\n%s", r.code, r.stderr)
		}
		b, err := os.ReadFile(filepath.Join(r.ws, "children"))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Fields(string(b))
	}
	without := children(t)
	for _, name := range without {
		if name != "tee" {
			t.Errorf("without BYRE_RUN_TIMEOUT the chassis has a %q child beside its relays: %v", name, without)
		}
	}
	for _, zero := range []string{"0", "0s", "0.0m"} {
		for _, name := range children(t, "BYRE_RUN_TIMEOUT="+zero) {
			if name != "tee" {
				t.Errorf("BYRE_RUN_TIMEOUT=%s must mean no timer, but the chassis has a %q child", zero, name)
			}
		}
	}
	with := children(t, "BYRE_RUN_TIMEOUT=60")
	if len(with) <= len(without) {
		t.Errorf("control: with BYRE_RUN_TIMEOUT the probe saw no timer (%v vs %v), so it cannot see one", with, without)
	}
}

// result.json is written once as `started` before the headless command runs,
// then replaced by the final outcome.
func TestHeadlessResultJSONStartedThenFinal(t *testing.T) {
	r := runHeadless(t, headlessCase{
		env:  []string{"BYRE_RUN_MODEL=some-model", "BYRE_RUN_TIMEOUT=5m"},
		stub: `cat "$BYRE_RUN_DIR/result.json" >"$BYRE_RUN_RESULT_FILE"`,
	})
	if r.code != 0 {
		t.Fatalf("exit %d, want 0\nstderr:\n%s", r.code, r.stderr)
	}
	started := decodeResultJSON(t, []byte(r.fd9))
	if started["status"] != "started" {
		t.Errorf("result.json during the run: status = %v, want started", started["status"])
	}
	if _, ok := jsonInt(t, started, "exit_code"); ok {
		t.Errorf("result.json during the run carries an exit code: %v", started)
	}
	final := r.resultJSON(t)
	if final["status"] != "success" || final["finished_at"] == nil {
		t.Errorf("final result.json: status %v finished_at %v, want success and a time", final["status"], final["finished_at"])
	}
	for key, want := range map[string]any{
		"schema": float64(1), "run_id": "0123abcd", "agent": "stubagent", "model": "some-model",
		"timeout": "5m", "task_sha256": sha256Hex([]byte("the task\n")), "result_file": "result.md",
	} {
		if final[key] != want {
			t.Errorf("final result.json %s = %v, want %v", key, final[key], want)
		}
	}
}

// The strict escaper: agent_version is agent text, and quotes, backslashes
// and control characters must not break the document; UTF-8 passes through.
func TestHeadlessResultJSONEscapesAgentText(t *testing.T) {
	const version = "v1 \"quoted\" back\\slash \x01\x1f\x7f tab\t é ✓"
	r := runHeadless(t, headlessCase{
		stub: `printf 'agent_version=v1 "quoted" back\\slash \001\037\177 tab\t é ✓\n' >>"$BYRE_RUN_STATUS_FILE"
printf ok >"$BYRE_RUN_RESULT_FILE"`,
	})
	if r.code != 0 {
		t.Fatalf("exit %d, want 0\nstderr:\n%s", r.code, r.stderr)
	}
	if got := r.resultJSON(t)["agent_version"]; got != version {
		t.Errorf("agent_version = %q, want %q", got, version)
	}
}

// Exit 16: the inputs the host staged are refused before anything runs --
// the stub never starts.
func TestHeadlessRefusesInvalidInputs(t *testing.T) {
	ran := "touch \"$STUB_WS/ran\"; printf x >\"$BYRE_RUN_RESULT_FILE\""
	cases := []struct {
		name     string
		env      []string
		prepare  func(t *testing.T, ws, runDir string)
		fragment string
	}{
		{"symlinked result dir", nil, func(t *testing.T, ws, runDir string) {
			real := filepath.Join(ws, "real")
			if err := os.Rename(runDir, real); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, runDir); err != nil {
				t.Fatal(err)
			}
		}, "symlink"},
		{"symlinked task", nil, func(t *testing.T, ws, runDir string) {
			other := filepath.Join(ws, "other.md")
			if err := os.Rename(filepath.Join(runDir, "task.md"), other); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(runDir, "task.md")); err != nil {
				t.Fatal(err)
			}
		}, "symlink"},
		{"task rewritten after staging", nil, func(t *testing.T, _, runDir string) {
			if err := os.WriteFile(filepath.Join(runDir, "task.md"), []byte("something else\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "sha256"},
		{"task outside the dir", []string{"BYRE_RUN_TASK_FILE=/etc/hostname"}, nil, "BYRE_RUN_TASK_FILE"},
		{"relative dir", []string{"BYRE_RUN_DIR=run"}, nil, "BYRE_RUN_DIR"},
		{"malformed digest", []string{"BYRE_RUN_TASK_SHA256=abc"}, nil, "BYRE_RUN_TASK_SHA256"},
		{"malformed timeout", []string{"BYRE_RUN_TIMEOUT=soon"}, nil, "BYRE_RUN_TIMEOUT"},
		{"sub-second timeout", []string{"BYRE_RUN_TIMEOUT=0.5"}, nil, "smallest timeout, 1s"},
		{"sub-second timeout with a suffix", []string{"BYRE_RUN_TIMEOUT=0.000001s"}, nil, "smallest timeout, 1s"},
		{"timeout whose product overflows", []string{"BYRE_RUN_TIMEOUT=999999999999999999d"}, nil, "largest timeout, 3650d"},
		{"timeout over the cap", []string{"BYRE_RUN_TIMEOUT=3651d"}, nil, "largest timeout, 3650d"},
	}
	for _, name := range []string{"transcript.jsonl", "stderr.log", "result.md", "status", "result.json"} {
		cases = append(cases,
			struct {
				name     string
				env      []string
				prepare  func(t *testing.T, ws, runDir string)
				fragment string
			}{"planted file at " + name, nil, func(t *testing.T, _, runDir string) {
				if err := os.WriteFile(filepath.Join(runDir, name), []byte("victim"), 0o644); err != nil {
					t.Fatal(err)
				}
			}, name},
			struct {
				name     string
				env      []string
				prepare  func(t *testing.T, ws, runDir string)
				fragment string
			}{"dangling symlink at " + name, nil, func(t *testing.T, ws, runDir string) {
				if err := os.Symlink(filepath.Join(ws, "nowhere"), filepath.Join(runDir, name)); err != nil {
					t.Fatal(err)
				}
			}, name},
			struct {
				name     string
				env      []string
				prepare  func(t *testing.T, ws, runDir string)
				fragment string
			}{"FIFO at " + name, nil, func(t *testing.T, _, runDir string) {
				if err := syscall.Mkfifo(filepath.Join(runDir, name), 0o644); err != nil {
					t.Fatalf("mkfifo: %v", err)
				}
			}, name},
		)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := runHeadless(t, headlessCase{stub: ran, env: tc.env, prepare: tc.prepare, bound: 10 * time.Second})
			if r.code != 16 {
				t.Fatalf("exit %d, want 16\nstderr:\n%s", r.code, r.stderr)
			}
			if !strings.Contains(r.stderr, tc.fragment) {
				t.Errorf("the refusal does not name %q:\n%s", tc.fragment, r.stderr)
			}
			if _, err := os.Stat(filepath.Join(r.ws, "ran")); err == nil {
				t.Error("the headless command ran despite the refusal")
			}
			if r.fd9 != "" {
				t.Errorf("a refused run wrote %q to the real stdout", r.fd9)
			}
			if strings.HasPrefix(tc.name, "planted file at ") {
				victim := filepath.Join(r.runDir, strings.TrimPrefix(tc.name, "planted file at "))
				if b, _ := os.ReadFile(victim); string(b) != "victim" {
					t.Errorf("the planted file was written through: %q", b)
				}
			}
		})
	}
}

// After the exclusive opens the chassis never opens a pathname in the dir:
// a FIFO the headless command plants at status, result.md and result.json
// (each unlinked first) blocks nothing, and the held descriptors still carry
// what was written to them.
func TestHeadlessFIFOsPlantedDuringTheRunNeverBlock(t *testing.T) {
	r := runHeadless(t, headlessCase{
		bound: 10 * time.Second,
		stub: `printf 'the answer' >"$BYRE_RUN_RESULT_FILE"
echo backend_exit=7 >>"$BYRE_RUN_STATUS_FILE"
for n in status result.md result.json; do
  rm -f "$BYRE_RUN_DIR/$n" && mkfifo "$BYRE_RUN_DIR/$n" || exit 99
done
exit 11`,
	})
	if r.code != 11 {
		t.Fatalf("exit %d, want 11\nstderr:\n%s", r.code, r.stderr)
	}
	if r.fd9 != "the answer" {
		t.Errorf("fd 9 = %q, want the answer from the held descriptor", r.fd9)
	}
	m := r.resultJSON(t)
	if got, ok := jsonInt(t, m, "backend_exit_code"); !ok || got != 7 {
		t.Errorf("backend_exit_code = %d (set=%v), want 7 from the held status descriptor", got, ok)
	}
}

// A directory planted at result.json wins the squat: the chassis says so on
// stderr and the exit code is unchanged.
func TestHeadlessDirectoryAtResultJSONIsReported(t *testing.T) {
	r := runHeadless(t, headlessCase{
		stub: `rm -f "$BYRE_RUN_DIR/result.json" && mkdir "$BYRE_RUN_DIR/result.json" || exit 99
printf 'the answer' >"$BYRE_RUN_RESULT_FILE"`,
	})
	if r.code != 0 {
		t.Fatalf("exit %d, want 0 (the squat must not change the verdict)\nstderr:\n%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "could not publish the final result.json") {
		t.Errorf("the squatted result.json was not reported:\n%s", r.stderr)
	}
	if r.fd9 != "the answer" {
		t.Errorf("fd 9 = %q, want the answer", r.fd9)
	}
}

// Nothing reads the box's stdin: a pipe that never closes, with bytes in it,
// neither hangs the run nor reaches the headless command.
func TestHeadlessNeverReadsStdin(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	if _, err := pw.WriteString("SECRET-ON-STDIN\n"); err != nil {
		t.Fatal(err)
	}
	r := runHeadless(t, headlessCase{
		stdin: pr,
		bound: 10 * time.Second,
		stub:  `got=$(cat); printf 'stdin=[%s]' "$got" >"$BYRE_RUN_RESULT_FILE"`,
	})
	if r.code != 0 {
		t.Fatalf("exit %d, want 0\nstderr:\n%s", r.code, r.stderr)
	}
	if r.fd9 != "stdin=[]" {
		t.Errorf("the headless command read %q, want an empty stdin", r.fd9)
	}
}

// TERM and INT to the chassis reach the headless command's group. INT is
// the case that needs the chassis's care: bash starts every background child
// with INT ignored.
func TestHeadlessForwardsSignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			r := runHeadless(t, headlessCase{
				bound: 15 * time.Second,
				stub: `trap 'printf got-signal >"$BYRE_RUN_RESULT_FILE"; exit 10' TERM INT
touch "$STUB_WS/ready"
while :; do sleep 0.1; done`,
				during: func(t *testing.T, cmd *exec.Cmd) {
					ready := filepath.Join(filepath.Dir(cmd.Args[1]), "ready")
					deadline := time.Now().Add(10 * time.Second)
					for {
						if _, err := os.Stat(ready); err == nil {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("the stub never became ready")
						}
						time.Sleep(20 * time.Millisecond)
					}
					if err := cmd.Process.Signal(sig); err != nil {
						t.Fatal(err)
					}
				},
			})
			if r.code != 10 || r.fd9 != "got-signal" {
				t.Fatalf("exit %d fd9 %q, want 10 and the stub's trap output (the signal was not forwarded)\nstderr:\n%s", r.code, r.fd9, r.stderr)
			}
		})
	}
}

// Stream routing: the headless command's stdout is the transcript and its
// stderr the stderr log, both relayed to the box's stderr unless quiet; fd 9
// carries the answer and only the answer; the chassis's own fd 1 carries
// nothing.
func TestHeadlessStreamRouting(t *testing.T) {
	const stub = `echo STDOUT-EVENT
echo STDERR-LINE >&2
{ echo LEAK >&9; } 2>/dev/null
printf 'THE-ANSWER' >"$BYRE_RUN_RESULT_FILE"`
	for _, quiet := range []bool{false, true} {
		name := "loud"
		var env []string
		if quiet {
			name = "quiet"
			env = []string{"BYRE_RUN_QUIET=1"}
		}
		t.Run(name, func(t *testing.T) {
			r := runHeadless(t, headlessCase{stub: stub, env: env})
			if r.code != 0 {
				t.Fatalf("exit %d, want 0\nstderr:\n%s", r.code, r.stderr)
			}
			if r.fd9 != "THE-ANSWER" {
				t.Errorf("fd 9 = %q, want exactly the answer", r.fd9)
			}
			if r.stdout != "" {
				t.Errorf("the chassis wrote its own stdout: %q", r.stdout)
			}
			for file, want := range map[string]string{"transcript.jsonl": "STDOUT-EVENT\n", "stderr.log": "STDERR-LINE\n"} {
				b, err := os.ReadFile(filepath.Join(r.runDir, file))
				if err != nil {
					t.Fatal(err)
				}
				if string(b) != want {
					t.Errorf("%s = %q, want %q", file, b, want)
				}
			}
			for _, line := range []string{"STDOUT-EVENT", "STDERR-LINE"} {
				relayed := strings.Contains(r.stderr, line)
				if relayed == quiet {
					t.Errorf("%s on the box's stderr = %v, want %v\nstderr:\n%s", line, relayed, !quiet, r.stderr)
				}
			}
			if !strings.Contains(r.stderr, "exit 0") {
				t.Errorf("the summary line is missing even when quiet:\n%s", r.stderr)
			}
		})
	}
}

// --quiet never hides a refusal: a failed quiet run prints the tail of the
// headless command's stderr, where its remedy lives. A successful quiet run
// prints none of it, and a loud run shows the remedy once -- the live relay
// -- not again at the end.
func TestHeadlessQuietFailureShowsTheStderrTail(t *testing.T) {
	const remedy = "REMEDY-give-the-box-a-credential"
	for _, tc := range []struct {
		name  string
		quiet bool
		exit  string
		code  int
		times int
	}{
		{"quiet failure", true, "exit 13", 13, 1},
		{"quiet success", true, `printf answer >"$BYRE_RUN_RESULT_FILE"`, 0, 0},
		{"quiet reported failure", true, `printf why >"$BYRE_RUN_RESULT_FILE"; exit 10`, 10, 0},
		{"loud failure", false, "exit 13", 13, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var env []string
			if tc.quiet {
				env = []string{"BYRE_RUN_QUIET=1"}
			}
			r := runHeadless(t, headlessCase{env: env, stub: "echo " + remedy + " >&2\n" + tc.exit})
			if r.code != tc.code {
				t.Fatalf("exit %d, want %d\nstderr:\n%s", r.code, tc.code, r.stderr)
			}
			if n := strings.Count(r.stderr, remedy); n != tc.times {
				t.Errorf("the remedy appears %d times on the box's stderr, want %d\nstderr:\n%s", n, tc.times, r.stderr)
			}
		})
	}
}

// A process that leaves the headless command's group (its own setsid) while
// holding the stdout relay open survives the group KILL; the chassis's wait
// for its relays is bounded, so it still finishes with the verdict.
func TestHeadlessEscapedRelayHolderDoesNotHoldTheRun(t *testing.T) {
	var escaped string
	t.Cleanup(func() {
		if pid, err := strconv.Atoi(escaped); err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	r := runHeadless(t, headlessCase{
		bound: 20 * time.Second,
		stub: `setsid sleep 60 &
p=$!
echo $p >"$STUB_WS/escaped"
# Exit only once it has left the group, or the group KILL wins the race.
until [ "$(cut -d' ' -f6 /proc/$p/stat 2>/dev/null)" = "$p" ] &&
  [ "$(cut -d'(' -f2 /proc/$p/stat | cut -d')' -f1)" = sleep ]; do sleep 0.01; done
printf 'the answer' >"$BYRE_RUN_RESULT_FILE"`,
	})
	if b, err := os.ReadFile(filepath.Join(r.ws, "escaped")); err == nil {
		escaped = strings.TrimSpace(string(b))
	}
	if r.code != 0 || r.fd9 != "the answer" {
		t.Errorf("exit %d fd9 %q, want 0 and the answer\nstderr:\n%s", r.code, r.fd9, r.stderr)
	}
}

// fd 9 carries the answer whatever the exit code -- a reported failure still
// has a message worth reading -- and nothing when the answer is empty.
func TestHeadlessAnswerOnEveryExit(t *testing.T) {
	r := runHeadless(t, headlessCase{stub: `printf 'why it failed' >"$BYRE_RUN_RESULT_FILE"; exit 10`})
	if r.code != 10 || r.fd9 != "why it failed" {
		t.Errorf("exit %d fd9 %q, want 10 and the answer", r.code, r.fd9)
	}
	r = runHeadless(t, headlessCase{stub: `exit 13`})
	if r.code != 13 || r.fd9 != "" {
		t.Errorf("exit %d fd9 %q, want 13 and nothing on fd 9", r.code, r.fd9)
	}
}

// The chassis's own helpers never resolve through the inherited PATH, whose
// first entry is agent-writable in the box: a planted sha256sum, tee, setsid
// (and the rest) earlier on PATH never runs, and the verdict is still right.
// The headless command alone gets the inherited PATH back, so the agent's own
// binaries there still resolve.
func TestHeadlessHelpersIgnoreThePlantedPath(t *testing.T) {
	if !bytes.HasPrefix(HeadlessScript(), []byte("#!/bin/bash\n")) {
		t.Errorf("headless.sh must name its interpreter absolutely, not through PATH")
	}
	planted := []string{"sha256sum", "head", "tee", "setsid", "mktemp", "date", "cat", "sleep", "mv", "chmod", "rm", "kill", "env"}
	r := runHeadless(t, headlessCase{
		stub:      `printf '%s' "$PATH" >"$STUB_WS/cmd-path"; agent-tool`,
		pathFirst: "planted",
		prepare: func(t *testing.T, ws, runDir string) {
			plantDir := filepath.Join(ws, "planted")
			if err := os.Mkdir(plantDir, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, name := range planted {
				body := "#!/bin/sh\necho " + name + " >>\"$STUB_WS/planted-ran\"\nexit 99\n"
				if err := os.WriteFile(filepath.Join(plantDir, name), []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			agentTool := "#!/bin/sh\nprintf 'the answer' >\"$BYRE_RUN_RESULT_FILE\"\n"
			if err := os.WriteFile(filepath.Join(plantDir, "agent-tool"), []byte(agentTool), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	})
	if r.code != 0 || r.fd9 != "the answer" {
		t.Fatalf("exit %d, fd 9 %q; want 0 and the answer\nstderr:\n%s", r.code, r.fd9, r.stderr)
	}
	if m := r.resultJSON(t); m["status"] != "success" {
		t.Errorf("result.json status = %v, want success", m["status"])
	}
	if ran, err := os.ReadFile(filepath.Join(r.ws, "planted-ran")); err == nil {
		t.Errorf("the chassis ran planted helpers from PATH: %q", ran)
	}
	seen, err := os.ReadFile(filepath.Join(r.ws, "cmd-path"))
	if err != nil {
		t.Fatalf("the headless command did not record its PATH: %v", err)
	}
	if want := filepath.Join(r.ws, "planted") + string(os.PathListSeparator); !strings.HasPrefix(string(seen), want) {
		t.Errorf("the headless command's PATH = %q, want the inherited one (starting %q)", seen, want)
	}
}
