package commands

// Gated end-to-end coverage for `byre run`, the headless-run verb: the real
// production launch path (Run -> developCommandWith -> the engine), the
// launcher's headless arm, the byre-headless chassis, and the host's output
// model, against a live engine. Run with BYRE_DOCKER_TESTS=1 (see
// integration_test.go):
//
//	byre-inttest ./internal/commands/ -run 'TestIntegrationRun' -v -count=1 -timeout 20m
//
// No agent and no credentials: the agent is a LOCAL test skill,
// headless-echo, whose headless command copies the task to the result
// descriptor, prints one marker line on its own stdout (the live stream the
// chassis relays to the box's stderr), and exits as the task's first word
// says. One project and one image serve every case: the first case
// configures the never-configured project on the way in (--template/--agent),
// and every later run reuses the build.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

// headlessEchoManifest is the test agent skill. Its interactive command is a
// shell; its headless command is the payload below, shipped by the skill.
// The firstrun hook reports whether the box handed hooks a terminal.
const headlessEchoManifest = `description = "byre's integration-test agent: echoes the task back as the answer."

[build]
files = { "byre-echo-headless" = "/usr/local/bin/byre-echo-headless", "tty-probe.sh" = "/etc/byre/firstrun.d/headless-echo-tty-probe" }
dockerfile = ["RUN chmod +x /usr/local/bin/byre-echo-headless"]

[agent]
command = "bash -l"
headless = "byre-echo-headless"
`

// headlessEchoPayload honours the headless contract (internal/gen/headless.sh):
// the task from BYRE_RUN_TASK_FILE, the answer to BYRE_RUN_RESULT_FILE,
// backend_exit=N to BYRE_RUN_STATUS_FILE before an 11. The task's first word
// picks the exit: exit0/exit10/exit11 are contract statuses, exit99 is outside
// the contract (the chassis calls it 14), sleep outlasts a --timeout, empty
// exits 0 with no answer (also 14).
const headlessEchoPayload = `#!/usr/bin/env bash
set -u
marker=""
read -r marker _ <"$BYRE_RUN_TASK_FILE" || true
echo "headless-echo-marker: $marker"
if [ "$marker" = empty ]; then exit 0; fi
cat "$BYRE_RUN_TASK_FILE" >"$BYRE_RUN_RESULT_FILE"
case $marker in
exit0) exit 0 ;;
exit10) exit 10 ;;
exit11)
  echo backend_exit=42 >>"$BYRE_RUN_STATUS_FILE"
  exit 11
  ;;
exit99) exit 99 ;;
sleep)
  sleep 30
  exit 0
  ;;
*)
  echo "headless-echo: unknown marker" >&2
  exit 99
  ;;
esac
`

// headlessEchoHook is a firstrun hook: one line saying which of its three
// standard descriptors is a terminal. Under a headless launch the launcher
// has moved hook stdout to stderr, so the line reaches byre's stderr.
const headlessEchoHook = `tty0=no; [ -t 0 ] && tty0=yes
tty1=no; [ -t 1 ] && tty1=yes
tty2=no; [ -t 2 ] && tty2=yes
printf 'headless-echo-hook: tty0=%s tty1=%s tty2=%s headless=%s\n' "$tty0" "$tty1" "$tty2" "${BYRE_HEADLESS:-}"
`

// hookNoTTY is the hook line a box with no terminal anywhere prints: no -t
// was allocated, so nothing a hook could prompt on exists.
const hookNoTTY = "headless-echo-hook: tty0=no tty1=no tty2=no headless=1"

// runDataKeys is the --data document's field order -- the contract
// (RunDataVersion 1).
var runDataKeys = []string{"version", "run_id", "outcome", "authority", "exit_code", "agent_exit_code", "phase", "agent", "launch_record", "started_at", "finished_at", "message", "exit_report"}

// runEnv is the one project every case runs in.
type runEnv struct {
	r     *runner.Runner
	p     project.Paths
	proj  string
	tasks string // task files live outside the project
}

func (e *runEnv) task(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(e.tasks, name)
	mustWriteFile(t, p, []byte(body), 0o644)
	return p
}

// run drives the production Run in-process: stdout a buffer (not a terminal,
// so the answer is copied raw), stdin empty.
func (e *runEnv) run(t *testing.T, o RunOptions) (code int, stdout []byte, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	s := Streams{Out: &out, Err: &errb, In: strings.NewReader("")}
	err := Run(s, e.proj, o)
	code = runExitCode(err)
	t.Logf("byre run %+v -> exit %d (err %v)\n--- stderr ---\n%s", o, code, err, errb.String())
	return code, out.Bytes(), errb.String()
}

func (e *runEnv) runDir() string { return filepath.Join(e.proj, runDirName) }

func (e *runEnv) runDirNames(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir(e.runDir())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, en := range ents {
		names = append(names, en.Name())
	}
	slices.Sort(names)
	return names
}

// summary is the stable fragment of byre's last stderr line for a run that
// reached the chassis.
func summary(code int, authority string) string {
	return ": " + outcomeRan + ", exit " + strconv.Itoa(code) + " (" + authority + ")"
}

// parseRunData checks the --data document's shape byte-for-byte where it is
// a contract -- one JSON object, its keys in contract order, a single
// trailing newline, nothing after -- and returns it decoded.
func parseRunData(t *testing.T, b []byte) runData {
	t.Helper()
	if len(b) == 0 || b[0] != '{' || !bytes.HasSuffix(b, []byte("}\n")) {
		t.Fatalf("--data stdout is not exactly one JSON object and a newline:\n%q", b)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("--data: want an object, got %v (%v)", tok, err)
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k.(string))
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		t.Fatalf("--data: stdout carries more than one document:\n%s", b)
	}
	if !slices.Equal(keys, runDataKeys) {
		t.Fatalf("--data keys = %v, want the contract order %v", keys, runDataKeys)
	}
	var d runData
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// TestIntegrationRun proves `byre run` end to end on a live engine. Each
// subtest names the rule it asserts; they share one project and one image,
// so they run in order and the first (which configures the project) is fatal.
func TestIntegrationRun(t *testing.T) {
	r := requireEngineRunner(t)
	ident := testIdentity(t, r)
	p, proj := testPaths(t)
	t.Setenv("BYRE_TASK", "")
	e := &runEnv{r: r, p: p, proj: proj, tasks: t.TempDir()}

	// Run detects its engine from the config: pin it to the one this suite
	// was given, so the cleanup below talks to the engine the run used.
	mustWriteFile(t, filepath.Join(p.Home, "default.config"), []byte("engine = \""+string(r.Engine())+"\"\n"), 0o644)

	skillDir := filepath.Join(p.Home, "skills", "headless-echo")
	mustMkdirAll(t, skillDir, 0o755)
	mustWriteFile(t, filepath.Join(skillDir, "skill.toml"), []byte(headlessEchoManifest), 0o644)
	mustWriteFile(t, filepath.Join(skillDir, "byre-echo-headless"), []byte(headlessEchoPayload), 0o755)
	mustWriteFile(t, filepath.Join(skillDir, "tty-probe.sh"), []byte(headlessEchoHook), 0o644)

	t.Cleanup(func() {
		_ = exec.Command(string(r.Engine()), "rm", "-f", containerName(p)).Run()
		if vols, err := r.VolumesByPrefix(volumePrefix(p.ID)); err == nil {
			for _, v := range vols {
				_ = r.VolumeRemove(v)
			}
		}
		_ = r.ImageRemove(imageTag(p.ID, ident.UID, ident.GID))
	})

	cfgPath := filepath.Join(p.Dir, config.ProjectConfigName)

	// Section 4 step 2(b): a never-configured checkout is configured by the
	// two axis flags on the way in, and the run goes ahead. This is also the
	// one image build every later case reuses.
	if _, err := os.Stat(cfgPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("precondition: the project must start unconfigured (%v)", err)
	}
	first := "exit0 configured on the way in\n"
	code, out, stderr := e.run(t, RunOptions{TaskFlag: e.task(t, "first.md", first), Template: "none", Agent: "headless-echo"})
	if code != 0 || string(out) != first {
		t.Fatalf("unconfigured + --template none --agent headless-echo: want exit 0 with the task as the answer, got %d stdout %q", code, out)
	}
	cfg, err := config.ParseFile(cfgPath, true)
	if err != nil {
		t.Fatalf("onboarding wrote no readable config: %v", err)
	}
	if cfg.Agent != "headless-echo" {
		t.Fatalf("the written config's agent = %q, want headless-echo", cfg.Agent)
	}
	if !strings.Contains(stderr, summary(0, authorityVerified)) {
		t.Fatalf("summary line missing %q", summary(0, authorityVerified))
	}

	// The exit code is the chassis's verdict on the headless command's
	// status; the answer (the task, echoed) is the only stdout; the live
	// stream (the marker line) is on stderr.
	t.Run("exit codes, stdout is the answer", func(t *testing.T) {
		for _, c := range []struct {
			marker string
			want   int
			answer bool
		}{
			{"exit0", 0, true},
			{"exit10", 10, true},
			{"exit11", 11, true},
			{"exit99", 14, true}, // outside the contract's exit set
			{"empty", 14, false}, // 0 without an answer
		} {
			body := c.marker + " the task text, verbatim\nsecond line\n"
			code, out, stderr := e.run(t, RunOptions{TaskFlag: e.task(t, c.marker+".md", body)})
			if code != c.want {
				t.Errorf("%s: exit %d, want %d", c.marker, code, c.want)
			}
			wantOut := ""
			if c.answer {
				wantOut = body
			}
			if string(out) != wantOut {
				t.Errorf("%s: stdout must be the answer and nothing else: got %q, want %q", c.marker, out, wantOut)
			}
			if !strings.Contains(stderr, "headless-echo-marker: "+c.marker) {
				t.Errorf("%s: the headless command's live stream (the marker line) is not on stderr", c.marker)
			}
			if !strings.Contains(stderr, hookNoTTY) {
				t.Errorf("%s: the firstrun hook must see no terminal under a headless launch (want %q)", c.marker, hookNoTTY)
			}
			if !strings.Contains(stderr, summary(c.want, authorityVerified)) {
				t.Errorf("%s: summary line missing %q", c.marker, summary(c.want, authorityVerified))
			}
			if c.marker == "exit11" {
				// Read by the TEST only: byre itself never reads the dir.
				b, err := os.ReadFile(filepath.Join(e.runDir(), "result.json"))
				if err != nil {
					t.Fatalf("exit11: result.json: %v", err)
				}
				var rj struct {
					Status      string `json:"status"`
					ExitCode    int    `json:"exit_code"`
					BackendExit *int   `json:"backend_exit_code"`
				}
				if err := json.Unmarshal(b, &rj); err != nil {
					t.Fatalf("exit11: result.json: %v\n%s", err, b)
				}
				if rj.Status != "agent_exit" || rj.ExitCode != 11 || rj.BackendExit == nil || *rj.BackendExit != 42 {
					t.Errorf("exit11: result.json must carry backend_exit_code 42 from the status descriptor: %s", b)
				}
			}
		}
	})

	// --timeout is the chassis's own timer: the sleeping task is cut short
	// and the run exits 12, not the headless command's status.
	t.Run("timeout exits 12", func(t *testing.T) {
		start := time.Now()
		code, _, stderr := e.run(t, RunOptions{TaskFlag: e.task(t, "sleep.md", "sleep through the timeout\n"), Timeout: "2s"})
		elapsed := time.Since(start)
		t.Logf("timeout run took %s", elapsed)
		if code != 12 {
			t.Fatalf("--timeout 2s on a sleeping task: exit %d, want 12", code)
		}
		if !strings.Contains(stderr, summary(12, authorityVerified)) {
			t.Errorf("summary line missing %q", summary(12, authorityVerified))
		}
		if elapsed > 25*time.Second {
			t.Errorf("--timeout 2s took %s: the 30 s sleep was not cut short", elapsed)
		}
	})

	// --data: stdout is exactly one versioned JSON document (the answer is
	// discarded), and the in-tree copy holds the same bytes.
	t.Run("data document", func(t *testing.T) {
		for _, c := range []struct {
			marker string
			want   int
		}{{"exit0", 0}, {"exit10", 10}} {
			code, out, _ := e.run(t, RunOptions{TaskFlag: e.task(t, "data-"+c.marker+".md", c.marker+" answer that must not reach stdout\n"), Data: true})
			if code != c.want {
				t.Errorf("--data %s: exit %d, want %d", c.marker, code, c.want)
			}
			if bytes.Contains(out, []byte("must not reach stdout")) {
				t.Errorf("--data %s: the answer leaked onto stdout:\n%s", c.marker, out)
			}
			d := parseRunData(t, out)
			if d.Version != RunDataVersion || d.Outcome != outcomeRan || d.Authority != authorityVerified ||
				d.ExitCode != c.want || d.AgentExitCode == nil || *d.AgentExitCode != c.want ||
				d.Phase != "attached" || d.Agent != "headless-echo" || len(d.LaunchRecord) != 64 {
				t.Errorf("--data %s: document does not say ran/verified/exit %d/attached/headless-echo with a launch record:\n%s", c.marker, c.want, out)
			}
			inTree, err := os.ReadFile(filepath.Join(e.runDir(), "outcome.json"))
			if err != nil || !bytes.Equal(inTree, out) {
				t.Errorf("--data %s: .byre-run/outcome.json must hold the same bytes as stdout (%v):\n%s", c.marker, err, inTree)
			}
		}
	})

	// The result dir holds exactly byre's and the chassis's files, and every
	// run starts from an empty one: a stray file goes, and a symlink planted
	// under byre's own name is unlinked, never followed.
	t.Run("result dir contents and emptying", func(t *testing.T) {
		body := "exit0 the dir listing run\n"
		if code, _, _ := e.run(t, RunOptions{TaskFlag: e.task(t, "dir1.md", body)}); code != 0 {
			t.Fatalf("exit %d, want 0", code)
		}
		want := []string{".gitignore", "outcome.json", "result.json", "result.md", "status", "stderr.log", "task.md", "transcript.jsonl"}
		if got := e.runDirNames(t); !slices.Equal(got, want) {
			t.Fatalf(".byre-run holds %v, want exactly %v", got, want)
		}
		if b, _ := os.ReadFile(filepath.Join(e.runDir(), ".gitignore")); string(b) != "*\n" {
			t.Errorf(".byre-run/.gitignore = %q, want %q", b, "*\n")
		}
		if b, _ := os.ReadFile(filepath.Join(e.runDir(), "task.md")); string(b) != body {
			t.Errorf(".byre-run/task.md = %q, want the task", b)
		}
		if b, _ := os.ReadFile(filepath.Join(e.runDir(), "transcript.jsonl")); !strings.Contains(string(b), "headless-echo-marker: exit0") {
			t.Errorf("transcript.jsonl must record the headless command's stdout: %q", b)
		}

		victim := filepath.Join(t.TempDir(), "victim")
		mustWriteFile(t, victim, []byte("untouched\n"), 0o644)
		mustWriteFile(t, filepath.Join(e.runDir(), "stray"), []byte("left by the agent\n"), 0o644)
		if err := os.Remove(filepath.Join(e.runDir(), "task.md")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(e.runDir(), "task.md")); err != nil {
			t.Fatal(err)
		}

		body2 := "exit0 the second run\n"
		code, out, _ := e.run(t, RunOptions{TaskFlag: e.task(t, "dir2.md", body2)})
		if code != 0 || string(out) != body2 {
			t.Fatalf("second run: exit %d stdout %q, want 0 and the task", code, out)
		}
		if b, err := os.ReadFile(victim); err != nil || string(b) != "untouched\n" {
			t.Errorf("the planted task.md symlink was followed: victim = %q (%v)", b, err)
		}
		if got := e.runDirNames(t); !slices.Equal(got, want) {
			t.Errorf("after the second run .byre-run holds %v, want exactly %v (the stray emptied)", got, want)
		}
		li, err := os.Lstat(filepath.Join(e.runDir(), "task.md"))
		if err != nil || !li.Mode().IsRegular() {
			t.Errorf("task.md must be a fresh regular file, not the planted symlink: %v %v", li, err)
		}
		if b, _ := os.ReadFile(filepath.Join(e.runDir(), "task.md")); string(b) != body2 {
			t.Errorf("task.md = %q, want the second task", b)
		}
	})

	// The real binary, from here: a pty and a never-closing pipe are what a
	// caller's stdio actually is, and only the process's own stdio proves it.
	bin := filepath.Join(t.TempDir(), "byre")
	build := exec.Command("go", "build", "-o", bin, "./cmd/byre")
	build.Dir = filepath.Join("..", "..")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building byre: %v\n%s", err, b)
	}

	// From a terminal: no -t is allocated (the hook sees no terminal on any
	// descriptor, so it has nothing to prompt on), and the answer is escaped
	// for the terminal.
	t.Run("from a pty", func(t *testing.T) {
		body := "exit0 ring\abell\n"
		taskPath := e.task(t, "pty.md", body)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		var cmd *exec.Cmd
		if py, err := exec.LookPath("python3"); err == nil {
			cmd = exec.CommandContext(ctx, py, "-I", "-c", ptyDriver, bin, "run", "--task", taskPath)
		} else if sc, err := exec.LookPath("script"); err == nil {
			cmd = exec.CommandContext(ctx, sc, "-q", "-e", "-c", shellQuoteJoin([]string{bin, "run", "--task", taskPath}), "/dev/null")
		} else {
			t.Skip("neither python3 nor script(1) on this runner: the pty case cannot be driven (the escape path stays covered in-process by TestRunStdoutIsTheAnswer)")
		}
		cmd.Dir = e.proj
		cmd.Env = append(os.Environ(), "TERM=dumb")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		raw, err := cmd.Output()
		t.Logf("pty transcript (%v):\n%q\ndriver stderr: %s", err, raw, stderr.String())
		if ctx.Err() != nil {
			t.Fatal("byre run under a pty did not finish within 5 minutes")
		}
		if code := exitCodeOf(err); code != 0 {
			t.Fatalf("byre run under a pty: exit %d, want 0", code)
		}
		if bytes.IndexByte(raw, '\a') >= 0 {
			t.Errorf("a raw control character reached the terminal: the answer was not escaped")
		}
		if !bytes.Contains(raw, []byte("exit0 ringbell")) {
			t.Errorf("the escaped answer %q is not on the terminal", "exit0 ringbell")
		}
		if !bytes.Contains(raw, []byte(hookNoTTY)) {
			t.Errorf("from a pty the box must still get no terminal (no -t): want %q", hookNoTTY)
		}
		if !bytes.Contains(raw, []byte(summary(0, authorityVerified))) {
			t.Errorf("summary line missing %q", summary(0, authorityVerified))
		}
	})

	// Section 12: stdin a pipe whose writer never closes, the task from a
	// file. The box's stdin is /dev/null whatever byre's is, so the run
	// finishes and exits as mapped.
	t.Run("never-closing stdin pipe", func(t *testing.T) {
		pr, pw, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer pw.Close()
		defer pr.Close()
		body := "exit10 with stdin held open\n"
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "run", "--task", e.task(t, "pipe.md", body))
		cmd.Dir = e.proj
		cmd.Stdin = pr
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err = cmd.Run()
		t.Logf("pipe run (%v)\n--- stderr ---\n%s", err, stderr.String())
		if ctx.Err() != nil {
			t.Fatal("byre run hung on a never-closing stdin pipe")
		}
		if code := exitCodeOf(err); code != 10 {
			t.Fatalf("exit %d, want 10 (the task's mapped status)", code)
		}
		if stdout.String() != body {
			t.Errorf("stdout into a pipe must be the answer, raw: got %q", stdout.String())
		}
	})

	// Raw run_args can hand the box a different PID 1, and byre does not
	// parse the block: the run still reports its exit, but never as
	// verified. /bin/true replaces the launcher, so the chassis never runs.
	t.Run("raw run_args are unverified", func(t *testing.T) {
		orig, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.WriteFile(cfgPath, orig, 0o644) })
		// Prepended, so the key is top-level whatever tables the file holds.
		mustWriteFile(t, cfgPath, append([]byte("run_args = [\"--entrypoint\", \"/bin/true\"]\n"), orig...), 0o644)
		code, out, stderr := e.run(t, RunOptions{TaskFlag: e.task(t, "rawargs.md", "exit10 never seen by a headless command\n"), Data: true})
		if code != 0 {
			t.Fatalf("run_args --entrypoint /bin/true: exit %d, want 0", code)
		}
		d := parseRunData(t, out)
		if d.Outcome != outcomeRan || d.Authority != authorityRawRunArgs || d.ExitCode != 0 {
			t.Errorf("want ran / %q / exit 0:\n%s", authorityRawRunArgs, out)
		}
		if !strings.Contains(stderr, summary(0, authorityRawRunArgs)) {
			t.Errorf("summary line missing %q", summary(0, authorityRawRunArgs))
		}
		if _, err := os.Lstat(filepath.Join(e.runDir(), "result.json")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the chassis must not have run under --entrypoint /bin/true (result.json: %v)", err)
		}
	})
}

// ptyDriver runs argv[1:] on a fresh pty that is its controlling terminal
// (stdin, stdout and stderr all on it), copies everything the terminal shows
// to stdout, and exits with the child's status.
const ptyDriver = `
import fcntl, os, pty, select, subprocess, sys, termios
m, s = pty.openpty()
def ctty():
    os.setsid()
    fcntl.ioctl(0, termios.TIOCSCTTY, 0)
p = subprocess.Popen(sys.argv[1:], stdin=s, stdout=s, stderr=s, preexec_fn=ctty)
os.close(s)
out = bytearray()
while True:
    r, _, _ = select.select([m], [], [], 0.5)
    if r:
        try:
            b = os.read(m, 65536)
        except OSError:
            break
        if not b:
            break
        out += b
    elif p.poll() is not None:
        break
rc = p.wait()
sys.stdout.buffer.write(out)
sys.stdout.flush()
sys.exit(rc if rc >= 0 else 128 - rc)
`

// exitCodeOf is a finished command's exit status: 0 for nil, the status for
// an exit, -1 for anything else (it never ran, or was killed).
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
