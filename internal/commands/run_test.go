package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/testtools"
)

// The `byre run` tests drive runWith with a launch that is develop's real
// engine-facing core (developWith) over the fake engine, after the same
// resolution Run's production launch would do. Nothing here needs an engine.

// headlessAgentManifest is a local agent skill that declares a headless
// command; plainAgentManifest is one that does not.
const (
	headlessAgentManifest = "[agent]\ncommand = \"agenty --go\"\nheadless = \"agenty-run --json\"\n"
	plainAgentManifest    = "[agent]\ncommand = \"plain --go\"\n"
)

// runProject is a configured project whose agent declares a headless
// command, with BYRE_TASK cleared so the host's environment cannot leak in.
func runProject(t *testing.T, cfg string) (project.Paths, string) {
	t.Helper()
	p, proj := testPaths(t)
	t.Setenv("BYRE_TASK", "")
	writeLocalSkill(t, p.Home, "agenty", headlessAgentManifest)
	writeLocalSkill(t, p.Home, "plainagent", plainAgentManifest)
	if cfg != "" {
		mustWriteFile(t, filepath.Join(p.Dir, config.ProjectConfigName), []byte(cfg), 0o644)
	}
	return p, proj
}

// fakeLaunch is Run's launch over the fake engine: the resolution develop
// would take (the --agent override included), then developWith.
func fakeLaunch(t *testing.T, f *fakeRunner, p project.Paths, proj string, o RunOptions) runLaunch {
	return func(ds Streams, h *headlessOptions) error {
		rv, err := resolveWithAgent(p, proj, nil, o.Agent)
		if err != nil {
			return err
		}
		return developWith(f, ds, p, rv, false, o.CredMode, h)
	}
}

// runStreams is a run's stdio over buffers. stdin is never a terminal in
// these tests unless asked; outTTY marks stdout as one.
func runStreams(in string, tty, outTTY bool) (Streams, *bytes.Buffer, *bytes.Buffer) {
	s, out, errBuf := testStreams(in, tty)
	s.OutTTY = outTTY
	return s, out, errBuf
}

// lastLine is the final line of a stream.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

func runExitCode(err error) int {
	var ee ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	if err != nil {
		return 1
	}
	return 0
}

func argvEnv(argv []string, key string) (string, bool) {
	val, found := "", false
	for i, a := range argv {
		if i > 0 && argv[i-1] == "-e" && strings.HasPrefix(a, key+"=") {
			val, found = strings.TrimPrefix(a, key+"="), true
		}
	}
	return val, found
}

func sha(b string) string {
	s := sha256.Sum256([]byte(b))
	return hex.EncodeToString(s[:])
}

const configuredAgenty = "agent = \"agenty\"\n"

// --task beats BYRE_TASK, with one note; `-` reads stdin to EOF; BYRE_TASK
// alone is the task; over the bound or blank is a refusal at exit 1, before
// any engine call.
func TestRunTaskPrecedence(t *testing.T) {
	t.Run("flag beats env with a note", func(t *testing.T) {
		p, proj := runProject(t, configuredAgenty)
		t.Setenv("BYRE_TASK", "from the env")
		task := filepath.Join(proj, "task.md")
		mustWriteFile(t, task, []byte("from the file"), 0o644)
		f := &fakeRunner{}
		o := RunOptions{TaskFlag: task}
		s, _, stderr := runStreams("", false, false)
		if err := runWith(s, proj, o, fakeLaunch(t, f, p, proj, o)); err != nil {
			t.Fatalf("run: %v\n%s", err, stderr)
		}
		if got, _ := argvEnv(f.creates[0], "BYRE_RUN_TASK_SHA256"); got != sha("from the file") {
			t.Errorf("the flag's task must win: sha %q", got)
		}
		if strings.Count(stderr.String(), "BYRE_TASK in the environment is ignored") != 1 {
			t.Errorf("want one precedence note:\n%s", stderr)
		}
	})
	t.Run("stdin", func(t *testing.T) {
		p, proj := runProject(t, configuredAgenty)
		f := &fakeRunner{}
		o := RunOptions{TaskFlag: "-"}
		s, _, stderr := runStreams("piped task\n", false, false)
		if err := runWith(s, proj, o, fakeLaunch(t, f, p, proj, o)); err != nil {
			t.Fatalf("run: %v\n%s", err, stderr)
		}
		if got, _ := argvEnv(f.creates[0], "BYRE_RUN_TASK_SHA256"); got != sha("piped task\n") {
			t.Errorf("stdin task sha %q", got)
		}
		b, err := os.ReadFile(filepath.Join(proj, runDirName, "task.md"))
		if err != nil || string(b) != "piped task\n" {
			t.Errorf("staged task = %q (%v)", b, err)
		}
	})
	t.Run("env alone", func(t *testing.T) {
		p, proj := runProject(t, configuredAgenty)
		t.Setenv("BYRE_TASK", "env task")
		f := &fakeRunner{}
		o := RunOptions{}
		s, _, stderr := runStreams("", false, false)
		if err := runWith(s, proj, o, fakeLaunch(t, f, p, proj, o)); err != nil {
			t.Fatalf("run: %v\n%s", err, stderr)
		}
		if got, _ := argvEnv(f.creates[0], "BYRE_RUN_TASK_SHA256"); got != sha("env task") {
			t.Errorf("env task sha %q", got)
		}
		if strings.Contains(stderr.String(), "is ignored") {
			t.Errorf("no note without a flag:\n%s", stderr)
		}
	})
	for _, tc := range []struct {
		name, in, want string
	}{
		{"over the bound", strings.Repeat("x", MaxTaskBytes+1), "over the 1048576-byte limit"},
		{"blank", " \n\t\n", "the task is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, proj := runProject(t, configuredAgenty)
			f := &fakeRunner{}
			o := RunOptions{TaskFlag: "-"}
			s, _, stderr := runStreams(tc.in, false, false)
			err := runWith(s, proj, o, fakeLaunch(t, f, p, proj, o))
			if runExitCode(err) != 1 || !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("want a refusal at 1 naming %q, got %v:\n%s", tc.want, err, stderr)
			}
			if len(f.creates) != 0 || len(f.ops) != 0 {
				t.Errorf("a task refusal precedes every engine call: ops %v", f.ops)
			}
		})
	}
	t.Run("over the bound from a file", func(t *testing.T) {
		p, proj := runProject(t, configuredAgenty)
		task := filepath.Join(t.TempDir(), "big.md")
		mustWriteFile(t, task, bytes.Repeat([]byte("x"), MaxTaskBytes+1), 0o644)
		o := RunOptions{TaskFlag: task}
		s, _, stderr := runStreams("", false, false)
		err := runWith(s, proj, o, fakeLaunch(t, &fakeRunner{}, p, proj, o))
		if runExitCode(err) != 1 || !strings.Contains(stderr.String(), "over the 1048576-byte limit") {
			t.Errorf("want the bound refusal, got %v:\n%s", err, stderr)
		}
	})
}

// The task file's route: under a root the box writes, an interior component
// cannot carry the read outside it; a symlinked leaf is refused on either
// route; a path outside every writable root is read with the leaf guard.
// Containment tier: the refusal (exit 1, nothing launched) is the contract.
func TestRunTaskFileContained(t *testing.T) {
	outside := t.TempDir()
	mustWriteFile(t, filepath.Join(outside, "task.md"), []byte("secret outside"), 0o644)

	cases := []struct {
		name   string
		setup  func(t *testing.T, proj string) string // returns the --task path
		wantOK bool
		want   string // the task bytes when read
	}{
		{"interior symlink out of the work tree", func(t *testing.T, proj string) string {
			if err := os.Symlink(outside, filepath.Join(proj, "inner")); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(proj, "inner", "task.md")
		}, false, ""},
		{"interior symlink inside the work tree", func(t *testing.T, proj string) string {
			mustMkdirAll(t, filepath.Join(proj, "real"), 0o755)
			mustWriteFile(t, filepath.Join(proj, "real", "task.md"), []byte("in tree"), 0o644)
			if err := os.Symlink("real", filepath.Join(proj, "inner")); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(proj, "inner", "task.md")
		}, true, "in tree"},
		{"symlinked leaf in the work tree", func(t *testing.T, proj string) string {
			mustWriteFile(t, filepath.Join(proj, "real.md"), []byte("in tree"), 0o644)
			if err := os.Symlink(filepath.Join(proj, "real.md"), filepath.Join(proj, "task.md")); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(proj, "task.md")
		}, false, ""},
		{"external path", func(t *testing.T, proj string) string {
			return filepath.Join(outside, "task.md")
		}, true, "secret outside"},
		{"symlinked leaf outside", func(t *testing.T, proj string) string {
			link := filepath.Join(t.TempDir(), "task.md")
			if err := os.Symlink(filepath.Join(outside, "task.md"), link); err != nil {
				t.Fatal(err)
			}
			return link
		}, false, ""},
		{"external route through the work tree resolving out", func(t *testing.T, proj string) string {
			mustMkdirAll(t, filepath.Join(proj, "sub"), 0o755)
			if err := os.Symlink(outside, filepath.Join(proj, "sub", "inner")); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(t.TempDir(), "alias")
			if err := os.Symlink(filepath.Join(proj, "sub"), alias); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(alias, "inner", "task.md")
		}, false, ""},
		{"alias into the work tree", func(t *testing.T, proj string) string {
			mustWriteFile(t, filepath.Join(proj, "t.md"), []byte("via alias"), 0o644)
			alias := filepath.Join(t.TempDir(), "alias")
			if err := os.Symlink(proj, alias); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(alias, "t.md")
		}, true, "via alias"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, proj := runProject(t, configuredAgenty)
			o := RunOptions{TaskFlag: tc.setup(t, proj)}
			called := false
			var gotSHA string
			s, _, stderr := runStreams("", false, false)
			err := runWith(s, proj, o, func(ds Streams, h *headlessOptions) error {
				called = true
				gotSHA = h.TaskSHA256
				return nil
			})
			if tc.wantOK {
				if err != nil || !called || gotSHA != sha(tc.want) {
					t.Errorf("want the task read (%q): err %v, launched %v\n%s", tc.want, err, called, stderr)
				}
				return
			}
			if runExitCode(err) != 1 || called {
				t.Errorf("want a refusal at 1 with nothing launched: err %v, launched %v\n%s", err, called, stderr)
			}
		})
	}
}

// Staging runs after Create and before the box starts; a failed Create never
// stages; a stage failure removes the container and the outcome is refused.
func TestRunStagesAfterCreate(t *testing.T) {
	t.Run("after create, before start", func(t *testing.T) {
		p, proj := runProject(t, configuredAgenty)
		t.Setenv("BYRE_TASK", "do it")
		var atStart []byte
		f := &fakeRunner{}
		f.runHook = func() { atStart, _ = os.ReadFile(filepath.Join(proj, runDirName, "task.md")) }
		o := RunOptions{}
		s, _, stderr := runStreams("", false, false)
		if err := runWith(s, proj, o, fakeLaunch(t, f, p, proj, o)); err != nil {
			t.Fatalf("run: %v\n%s", err, stderr)
		}
		if string(atStart) != "do it" {
			t.Errorf("the task must be staged before the box starts, saw %q", atStart)
		}
		if gi, err := os.ReadFile(filepath.Join(proj, runDirName, ".gitignore")); err != nil || string(gi) != "*\n" {
			t.Errorf(".gitignore = %q (%v)", gi, err)
		}
	})
	t.Run("create failure never stages", func(t *testing.T) {
		p, proj := runProject(t, configuredAgenty)
		t.Setenv("BYRE_TASK", "do it")
		f := &fakeRunner{createErr: errors.New("name in use")}
		o := RunOptions{Data: true}
		s, out, _ := runStreams("", false, false)
		err := runWith(s, proj, o, fakeLaunch(t, f, p, proj, o))
		if err == nil {
			t.Fatal("a failed create must fail the run")
		}
		if _, serr := os.Lstat(filepath.Join(proj, runDirName)); !errors.Is(serr, os.ErrNotExist) {
			t.Errorf("nothing may be staged without a created box: %v", serr)
		}
		if d := decodeRunData(t, out.Bytes()); d.Outcome != outcomeByreFailed || d.Phase != "prelaunch" {
			t.Errorf("outcome %q phase %q, want byre_failed at prelaunch", d.Outcome, d.Phase)
		}
	})
	victim := func(t *testing.T) (string, string) {
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, "keep"), []byte("victim"), 0o644)
		return dir, filepath.Join(dir, "keep")
	}
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, at string) (check func(t *testing.T))
	}{
		{"symlinked result dir", func(t *testing.T, at string) func(t *testing.T) {
			dir, keep := victim(t)
			if err := os.Symlink(dir, at); err != nil {
				t.Fatal(err)
			}
			return func(t *testing.T) {
				if b, err := os.ReadFile(keep); err != nil || string(b) != "victim" {
					t.Errorf("the symlink's target was touched: %q %v", b, err)
				}
				if ents, _ := os.ReadDir(dir); len(ents) != 1 {
					t.Errorf("the symlink's target gained or lost entries: %v", ents)
				}
			}
		}},
		{"file at the result dir", func(t *testing.T, at string) func(t *testing.T) {
			mustWriteFile(t, at, []byte("a file"), 0o644)
			return func(t *testing.T) {
				if b, err := os.ReadFile(at); err != nil || string(b) != "a file" {
					t.Errorf("the file was touched: %q %v", b, err)
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, proj := runProject(t, configuredAgenty)
			t.Setenv("BYRE_TASK", "do it")
			check := tc.plant(t, filepath.Join(proj, runDirName))
			f := &fakeRunner{}
			o := RunOptions{Data: true}
			s, out, stderr := runStreams("", false, false)
			err := runWith(s, proj, o, fakeLaunch(t, f, p, proj, o))
			if runExitCode(err) != 1 {
				t.Fatalf("want a refusal at 1, got %v\n%s", err, stderr)
			}
			if !slices.Contains(f.rmContainers, containerName(p)) || len(f.starts) != 0 {
				t.Errorf("a stage failure removes the created box and never starts it: rm %v starts %v", f.rmContainers, f.starts)
			}
			if d := decodeRunData(t, out.Bytes()); d.Outcome != outcomeRefused || d.LaunchRecord != "" {
				t.Errorf("outcome %q launch_record %q, want refused and no record (the box never ran)", d.Outcome, d.LaunchRecord)
			}
			if left := launchRecordFiles(t, p); len(left) != 0 {
				t.Errorf("a stage failure keeps no launch record: %v", left)
			}
			check(t)
		})
	}
	t.Run("populated dir emptied, symlinked task.md removed not followed", func(t *testing.T) {
		p, proj := runProject(t, configuredAgenty)
		t.Setenv("BYRE_TASK", "fresh")
		rd := filepath.Join(proj, runDirName)
		mustMkdirAll(t, filepath.Join(rd, "nested", "deep"), 0o755)
		mustWriteFile(t, filepath.Join(rd, "result.md"), []byte("old"), 0o644)
		dir, keep := victim(t)
		if err := os.Symlink(keep, filepath.Join(rd, "task.md")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(dir, filepath.Join(rd, "dirlink")); err != nil {
			t.Fatal(err)
		}
		f := &fakeRunner{}
		o := RunOptions{}
		s, _, stderr := runStreams("", false, false)
		if err := runWith(s, proj, o, fakeLaunch(t, f, p, proj, o)); err != nil {
			t.Fatalf("run: %v\n%s", err, stderr)
		}
		ents, err := os.ReadDir(rd)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		if !slices.Equal(names, []string{".gitignore", "outcome.json", "task.md"}) {
			t.Errorf("the result dir must hold only byre's files, got %v", names)
		}
		if li, err := os.Lstat(filepath.Join(rd, "task.md")); err != nil || !li.Mode().IsRegular() {
			t.Errorf("task.md must be a fresh regular file: %v %v", li, err)
		}
		if b, err := os.ReadFile(keep); err != nil || string(b) != "victim" {
			t.Errorf("a symlink's target was followed: %q %v", b, err)
		}
		if ents, _ := os.ReadDir(dir); len(ents) != 1 {
			t.Errorf("a symlinked dir's target was emptied: %v", ents)
		}
	})
}

// A task file that changes while byre waits for the setup lock refuses the
// run, whether or not the route changed: the bytes hashed before the lock
// are the only ones a run may stage.
func TestRunRefusesTaskChangedUnderLock(t *testing.T) {
	p, proj := runProject(t, configuredAgenty)
	task := filepath.Join(proj, "task.md")
	mustWriteFile(t, task, []byte("the task I meant"), 0o644)
	f := &fakeRunner{}
	f.boxCreateHook = func() { mustWriteFile(t, task, []byte("a different task"), 0o644) }
	o := RunOptions{TaskFlag: task, Data: true}
	s, out, stderr := runStreams("", false, false)
	err := runWith(s, proj, o, fakeLaunch(t, f, p, proj, o))
	if runExitCode(err) != 1 || !strings.Contains(stderr.String(), "the file changed while byre waited for the setup lock") {
		t.Fatalf("want the changed-task refusal at 1, got %v\n%s", err, stderr)
	}
	if d := decodeRunData(t, out.Bytes()); d.Outcome != outcomeRefused {
		t.Errorf("outcome %q, want refused", d.Outcome)
	}
	if len(f.starts) != 0 {
		t.Errorf("the box must not start: %v", f.starts)
	}
	if _, serr := os.Lstat(filepath.Join(proj, runDirName, "task.md")); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("nothing may be staged: %v", serr)
	}
}

// An unconfigured project needs both axis flags: run cannot ask, and the
// non-TTY onboarding remedy (run on a TTY) is not one run's caller can take.
func TestRunRefusesUnconfiguredWithoutFlags(t *testing.T) {
	for _, o := range []RunOptions{{}, {Agent: "agenty"}, {Template: "none"}} {
		_, proj := runProject(t, "")
		t.Setenv("BYRE_TASK", "do it")
		called := false
		s, _, stderr := runStreams("", false, false)
		err := runWith(s, proj, o, func(Streams, *headlessOptions) error { called = true; return nil })
		if runExitCode(err) != 1 || called || !strings.Contains(stderr.String(), "this project is not configured here: pass --template <t> and --agent <a>") {
			t.Errorf("%+v: want the unconfigured refusal at 1, got %v (launched %v)\n%s", o, err, called, stderr)
		}
	}
	_, proj := runProject(t, "")
	t.Setenv("BYRE_TASK", "do it")
	called := false
	s, _, stderr := runStreams("", false, false)
	if err := runWith(s, proj, RunOptions{Template: "none", Agent: "agenty"}, func(Streams, *headlessOptions) error { called = true; return nil }); err != nil || !called {
		t.Errorf("both flags configure on the way in: err %v launched %v\n%s", err, called, stderr)
	}
}

// An agent with no headless command is refused, naming the agents that have
// one.
func TestRunRefusesAgentWithoutHeadless(t *testing.T) {
	_, proj := runProject(t, "agent = \"plainagent\"\n")
	t.Setenv("BYRE_TASK", "do it")
	called := false
	s, _, stderr := runStreams("", false, false)
	err := runWith(s, proj, RunOptions{}, func(Streams, *headlessOptions) error { called = true; return nil })
	if runExitCode(err) != 1 || called {
		t.Fatalf("want a refusal at 1, got %v (launched %v)", err, called)
	}
	if !strings.Contains(stderr.String(), "declares no headless command; agents that do:") || !strings.Contains(stderr.String(), "agenty") {
		t.Errorf("the refusal must name the rule and the agents that qualify:\n%s", stderr)
	}
}

// An agentless resolution has nothing to run headlessly -- from the config
// or from -a none.
func TestRunRefusesAgentless(t *testing.T) {
	for _, tc := range []struct {
		cfg   string
		agent string
	}{{"", ""}, {configuredAgenty, "none"}} {
		cfg := tc.cfg
		if cfg == "" {
			cfg = "template = \"none\"\n"
		}
		_, proj := runProject(t, cfg)
		t.Setenv("BYRE_TASK", "do it")
		called := false
		s, _, stderr := runStreams("", false, false)
		err := runWith(s, proj, RunOptions{Agent: tc.agent}, func(Streams, *headlessOptions) error { called = true; return nil })
		if runExitCode(err) != 1 || called || !strings.Contains(stderr.String(), "no agent selected") {
			t.Errorf("agent %q: want the agentless refusal at 1, got %v (launched %v)\n%s", tc.agent, err, called, stderr)
		}
	}
}

// Whatever the caller's terminal: no -t, no stdin for the box, the chassis as
// the command, and the run's own env with only the knobs that were asked for.
func TestRunForcesNonInteractive(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    RunOptions
	}{
		{"plain", RunOptions{}},
		{"timeout and quiet", RunOptions{Timeout: "30m", Quiet: true, Model: "big"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, proj := runProject(t, configuredAgenty)
			t.Setenv("BYRE_TASK", "do it")
			f := &fakeRunner{}
			s, _, stderr := runStreams("", true, true)
			if err := runWith(s, proj, tc.o, fakeLaunch(t, f, p, proj, tc.o)); err != nil {
				t.Fatalf("run: %v\n%s", err, stderr)
			}
			argv := f.creates[0]
			if slices.Contains(argv, "-t") || !slices.Contains(argv, "-i") {
				t.Errorf("want -i and no -t: %q", argv)
			}
			if argv[len(argv)-1] != gen.HeadlessPath {
				t.Errorf("the chassis must be the command: %q", argv)
			}
			if len(f.startIOs) != 1 || f.startIOs[0].stdin != nil {
				t.Errorf("the box gets no stdin: %+v", f.startIOs)
			}
			want := map[string]string{
				"BYRE_RUN_TASK_FILE":   "/workspace/.byre-run/task.md",
				"BYRE_RUN_TASK_SHA256": sha("do it"),
				"BYRE_RUN_DIR":         "/workspace/.byre-run",
				"BYRE_RUN_MODEL":       tc.o.Model,
				"BYRE_RUN_AGENT":       "agenty",
			}
			for k, v := range want {
				if got, ok := argvEnv(argv, k); !ok || got != v {
					t.Errorf("%s = %q (present %v), want %q", k, got, ok, v)
				}
			}
			if id, ok := argvEnv(argv, "BYRE_RUN_ID"); !ok || len(id) != 16 {
				t.Errorf("BYRE_RUN_ID = %q", id)
			}
			// Both keys are always on the argv, empty without their flag
			// (the chassis reads empty as unset).
			if got, ok := argvEnv(argv, "BYRE_RUN_TIMEOUT"); !ok || got != tc.o.Timeout {
				t.Errorf("BYRE_RUN_TIMEOUT = %q (present %v), want %q", got, ok, tc.o.Timeout)
			}
			wantQuiet := ""
			if tc.o.Quiet {
				wantQuiet = "1"
			}
			if got, ok := argvEnv(argv, "BYRE_RUN_QUIET"); !ok || got != wantQuiet {
				t.Errorf("BYRE_RUN_QUIET = %q (present %v), want %q", got, ok, wantQuiet)
			}
		})
	}
}

// A skill's [runtime].env cannot set a BYRE_RUN_* key over the run's own --
// including the knobs the run was not asked for, which stay empty.
func TestRunEnvBeatsSkillEnv(t *testing.T) {
	p, proj := runProject(t, configuredAgenty)
	writeLocalSkill(t, p.Home, "agenty", headlessAgentManifest+"\n[runtime.env]\nBYRE_RUN_MODEL = \"skill-chosen\"\nBYRE_RUN_TIMEOUT = \"1s\"\nBYRE_RUN_QUIET = \"0\"\n")
	t.Setenv("BYRE_TASK", "do it")
	f := &fakeRunner{}
	o := RunOptions{Model: "mine"}
	s, _, stderr := runStreams("", false, false)
	if err := runWith(s, proj, o, fakeLaunch(t, f, p, proj, o)); err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if got, _ := argvEnv(f.creates[0], "BYRE_RUN_MODEL"); got != "mine" {
		t.Errorf("BYRE_RUN_MODEL = %q, want the run's own value", got)
	}
	for _, k := range []string{"BYRE_RUN_TIMEOUT", "BYRE_RUN_QUIET"} {
		if got, ok := argvEnv(f.creates[0], k); !ok || got != "" {
			t.Errorf("%s = %q (present %v), want it empty without its flag", k, got, ok)
		}
	}
}

// The chassis's 10-16 pass through as the process status with no banner: the
// run returns the ExitError main exits with silently.
func TestRunPassesChassisExitThrough(t *testing.T) {
	p, proj := runProject(t, configuredAgenty)
	t.Setenv("BYRE_TASK", "do it")
	f := &fakeRunner{runErr: exitError(t, 12)}
	s, _, stderr := runStreams("", false, false)
	err := runWith(s, proj, RunOptions{}, fakeLaunch(t, f, p, proj, RunOptions{}))
	var ee ExitError
	if !errors.As(err, &ee) || ee.Code != 12 {
		t.Fatalf("want ExitError{12}, got %v", err)
	}
	if !strings.HasSuffix(lastLine(stderr.String()), ": ran, exit 12 (verified)") {
		t.Errorf("summary: %q", lastLine(stderr.String()))
	}
}

// A plain error from the launch prints in main's register BEFORE the summary
// line, and main gets a bare exit code, so the summary line stays last.
func TestRunPlainErrorPrintsBeforeSummary(t *testing.T) {
	_, proj := runProject(t, configuredAgenty)
	t.Setenv("BYRE_TASK", "do it")
	s, _, stderr := runStreams("", false, false)
	err := runWith(s, proj, RunOptions{}, func(Streams, *headlessOptions) error {
		return errors.New("credential delivery failed")
	})
	var ee ExitError
	if !errors.As(err, &ee) || ee.Code != 1 {
		t.Fatalf("want ExitError{1}, got %v", err)
	}
	got := stderr.String()
	banner := strings.Index(got, "byre: credential delivery failed\n")
	summary := strings.Index(got, ": byre_failed, exit 1 (")
	if banner < 0 || summary < 0 || banner > summary {
		t.Errorf("want the error line before the summary line:\n%s", got)
	}
	if !strings.HasSuffix(lastLine(got), ": byre_failed, exit 1 (verified)") {
		t.Errorf("the summary line must be last: %q", lastLine(got))
	}
}

// The outcome classes, read from develop's typed result.
func TestRunOutcomeClassification(t *testing.T) {
	plain := errors.New("credential delivery failed")
	cases := []struct {
		name      string
		err       error
		res       developResult
		class     string
		code      int
		agentExit *int
	}{
		{"clean", nil, developResult{Phase: phaseAttached, ContainerStatus: 0}, outcomeRan, 0, intPtr(0)},
		{"3 before create", ExitError{Code: 3}, developResult{Phase: phasePrelaunch, ContainerStatus: -1}, outcomeRefused, 3, nil},
		{"run's own 1 before create", ExitError{Code: 1}, developResult{Phase: phasePrelaunch, ContainerStatus: -1}, outcomeRefused, 1, nil},
		{"3 after attach, box live", ExitError{Code: 3}, developResult{Phase: phaseAttached, Live: true, ContainerStatus: -1}, outcomeRefused, 3, nil},
		{"11 after create", ExitError{Code: 11}, developResult{Phase: phaseAttached, ContainerStatus: 11}, outcomeRan, 11, intPtr(11)},
		{"1 after create", ExitError{Code: 1}, developResult{Phase: phaseAttached, ContainerStatus: 1}, outcomeBoxExited, 1, intPtr(1)},
		{"3 after attach, box gone", ExitError{Code: 3}, developResult{Phase: phaseAttached, ContainerStatus: 3}, outcomeBoxExited, 3, intPtr(3)},
		{"137 decoded", errors.New("the box was killed (SIGKILL)"), developResult{Phase: phaseAttached, ContainerStatus: 137}, outcomeByreFailed, 1, intPtr(137)},
		{"credential error after create", plain, developResult{Phase: phaseAttached, ContainerStatus: 0}, outcomeByreFailed, 1, intPtr(0)},
		{"plain error before create", plain, developResult{Phase: phasePrelaunch, ContainerStatus: -1}, outcomeByreFailed, 1, nil},
	}
	for _, tc := range cases {
		got := classifyRun(tc.err, tc.res, "")
		if got.class != tc.class || got.exitCode != tc.code || !equalIntPtr(got.agentExit, tc.agentExit) {
			t.Errorf("%s: got %s exit %d agent %v, want %s exit %d agent %v", tc.name, got.class, got.exitCode, derefInt(got.agentExit), tc.class, tc.code, derefInt(tc.agentExit))
		}
	}

	// authority degrades with raw run_args on every class, ran included.
	p, proj := runProject(t, configuredAgenty+"run_args = [\"--pids-limit\", \"512\"]\n")
	t.Setenv("BYRE_TASK", "do it")
	s, out, stderr := runStreams("", false, false)
	o := RunOptions{Data: true}
	if err := runWith(s, proj, o, fakeLaunch(t, &fakeRunner{}, p, proj, o)); err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if d := decodeRunData(t, out.Bytes()); d.Outcome != outcomeRan || d.Authority != "unverified: raw run_args" {
		t.Errorf("outcome %q authority %q", d.Outcome, d.Authority)
	}
	if !strings.HasSuffix(lastLine(stderr.String()), "(unverified: raw run_args)") {
		t.Errorf("summary: %q", lastLine(stderr.String()))
	}
}

func equalIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func derefInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// The box's stdout is the answer: escaped per line on a terminal, byte for
// byte when redirected, and nothing else on run's stdout either way.
func TestRunStdoutIsTheAnswer(t *testing.T) {
	answer := "first\x1b[2J line\nclean\npartial\x07tail"
	for _, tc := range []struct {
		name   string
		outTTY bool
		want   string
	}{
		{"terminal", true, "first line\nclean\npartialtail"},
		{"redirected", false, answer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, proj := runProject(t, configuredAgenty)
			t.Setenv("BYRE_TASK", "do it")
			f := &fakeRunner{attachOut: answer}
			s, out, stderr := runStreams("", false, tc.outTTY)
			if err := runWith(s, proj, RunOptions{}, fakeLaunch(t, f, p, proj, RunOptions{})); err != nil {
				t.Fatalf("run: %v\n%s", err, stderr)
			}
			if out.String() != tc.want {
				t.Errorf("stdout = %q, want %q", out.String(), tc.want)
			}
			if !strings.Contains(stderr.String(), "byre: exposure:") {
				t.Errorf("byre's own notices belong on stderr:\n%s", stderr)
			}
		})
	}
	// A line longer than the escaper's hold is written in pieces, escaped.
	var b bytes.Buffer
	le := &lineEscaper{w: &b}
	long := strings.Repeat("é", maxEscapedLine)
	_, _ = le.Write([]byte(long + "\x1b"))
	le.flush()
	if b.String() != long {
		t.Errorf("a long line must survive in pieces with runes intact (len %d vs %d)", b.Len(), len(long))
	}
}

type decodedRunData struct {
	Version       int      `json:"version"`
	RunID         string   `json:"run_id"`
	Outcome       string   `json:"outcome"`
	Authority     string   `json:"authority"`
	ExitCode      int      `json:"exit_code"`
	AgentExitCode *int     `json:"agent_exit_code"`
	Phase         string   `json:"phase"`
	Agent         string   `json:"agent"`
	LaunchRecord  string   `json:"launch_record"`
	StartedAt     string   `json:"started_at"`
	FinishedAt    string   `json:"finished_at"`
	Message       string   `json:"message"`
	ExitReport    []string `json:"exit_report"`
}

// decodeRunData requires stdout to be exactly one JSON document.
func decodeRunData(t *testing.T, b []byte) decodedRunData {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var d decodedRunData
	if err := dec.Decode(&d); err != nil {
		t.Fatalf("stdout is not a run document: %v\n%s", err, b)
	}
	if dec.More() {
		t.Fatalf("stdout carries more than one document:\n%s", b)
	}
	return d
}

// pinRun fixes the clock and the run id for the byte-pinned contract.
func pinRun(t *testing.T) {
	t.Helper()
	clock, id := runClock, newRunID
	t.Cleanup(func() { runClock, newRunID = clock, id })
	at := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	runClock = func() time.Time { return at }
	newRunID = func() (string, error) { return "0123456789abcdef", nil }
}

// CONTRACT: the --data document, byte for byte. RunDataVersion's changelog and
// CHANGES.md move with any change here. The box's stdout is discarded, so the
// answer the fake box prints never reaches it.
func TestRunDataDocument(t *testing.T) {
	_, proj := runProject(t, configuredAgenty)
	t.Setenv("BYRE_TASK", "do it")
	pinRun(t)
	s, out, stderr := runStreams("", false, false)
	err := runWith(s, proj, RunOptions{Data: true}, func(ds Streams, h *headlessOptions) error {
		_, _ = h.AttachStdout.Write([]byte("the answer\n"))
		*h.Result = developResult{
			Phase:           phaseAttached,
			ContainerStatus: 11,
			LaunchHash:      strings.Repeat("ab", 32),
			ExitReport:      []string{"⚠ changed", "   .git/hooks/pre-commit\x1b"},
			RawRunArgs:      true,
		}
		return ExitError{Code: 11}
	})
	if runExitCode(err) != 11 {
		t.Fatalf("want ExitError{11}, got %v\n%s", err, stderr)
	}
	want := `{
  "version": 1,
  "run_id": "0123456789abcdef",
  "outcome": "ran",
  "authority": "unverified: raw run_args",
  "exit_code": 11,
  "agent_exit_code": 11,
  "phase": "attached",
  "agent": "agenty",
  "launch_record": "abababababababababababababababababababababababababababababababab",
  "started_at": "2026-11-02T09:30:00Z",
  "finished_at": "2026-11-02T09:30:00Z",
  "message": "the agent process failed",
  "exit_report": [
    "⚠ changed",
    "   .git/hooks/pre-commit\u001b"
  ]
}
`
	if out.String() != want {
		t.Errorf("--data document:\n%s\nwant:\n%s", out.String(), want)
	}
	if RunDataVersion != 1 {
		t.Errorf("RunDataVersion moved to %d: re-pin this contract and note it in CHANGES.md", RunDataVersion)
	}
	// A refusal before launch still prints the document, prelaunch, with
	// the refusal as its message.
	_, proj2 := runProject(t, "agent = \"plainagent\"\n")
	t.Setenv("BYRE_TASK", "do it")
	s2, out2, _ := runStreams("", false, false)
	_ = runWith(s2, proj2, RunOptions{Data: true}, func(Streams, *headlessOptions) error { return nil })
	d := decodeRunData(t, out2.Bytes())
	if d.Outcome != outcomeRefused || d.Phase != "prelaunch" || d.ExitCode != 1 || d.Agent != "plainagent" || d.AgentExitCode != nil || !strings.Contains(d.Message, "declares no headless command") || d.ExitReport == nil {
		t.Errorf("refusal document: %+v", d)
	}
}

// --quiet drops byre's banner-class lines and passes BYRE_RUN_QUIET; it never
// drops a refusal or the summary line.
func TestRunQuiet(t *testing.T) {
	p, proj := runProject(t, configuredAgenty)
	t.Setenv("BYRE_TASK", "do it")
	f := &fakeRunner{}
	o := RunOptions{Quiet: true, Agent: "agenty"}
	s, _, stderr := runStreams("", false, false)
	if err := runWith(s, proj, o, fakeLaunch(t, f, p, proj, o)); err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	for _, banner := range []string{"byre: exposure:", "byre: network", "agent for this run"} {
		if strings.Contains(stderr.String(), banner) {
			t.Errorf("--quiet must drop %q:\n%s", banner, stderr)
		}
	}
	if !strings.Contains(lastLine(stderr.String()), ": ran, exit 0 (verified)") {
		t.Errorf("the summary line survives --quiet:\n%s", stderr)
	}
	if _, ok := argvEnv(f.creates[0], "BYRE_RUN_QUIET"); !ok {
		t.Error("BYRE_RUN_QUIET must reach the box")
	}

	// The same run, loud, prints them.
	f2 := &fakeRunner{}
	o.Quiet = false
	s2, _, stderr2 := runStreams("", false, false)
	if err := runWith(s2, proj, o, fakeLaunch(t, f2, p, proj, o)); err != nil {
		t.Fatalf("run: %v\n%s", err, stderr2)
	}
	for _, banner := range []string{"byre: exposure:", "byre: network", "agent for this run"} {
		if !strings.Contains(stderr2.String(), banner) {
			t.Errorf("a loud run prints %q:\n%s", banner, stderr2)
		}
	}

	// A refusal is never quiet.
	_, proj3 := runProject(t, "agent = \"plainagent\"\n")
	t.Setenv("BYRE_TASK", "do it")
	s3, _, stderr3 := runStreams("", false, false)
	_ = runWith(s3, proj3, RunOptions{Quiet: true}, func(Streams, *headlessOptions) error { return nil })
	if !strings.Contains(stderr3.String(), "declares no headless command") {
		t.Errorf("--quiet must not drop a refusal:\n%s", stderr3)
	}

	// The opening notes (worktree, preset) are banner-class too.
	mustWriteFile(t, filepath.Join(proj, PresetName), []byte("agent = \"agenty\"\n"), 0o644)
	var loud, quiet bytes.Buffer
	developNotices(&loud, proj, p, nil)
	developNotices(&quiet, proj, p, &headlessOptions{Quiet: true})
	if !strings.Contains(loud.String(), PresetName) || quiet.Len() != 0 {
		t.Errorf("preset note: loud %q, quiet %q", loud.String(), quiet.String())
	}
}

// The summary line is the last thing on stderr on every class.
func TestRunSummaryLineAlwaysPrints(t *testing.T) {
	cases := []struct {
		name   string
		cfg    string
		launch func(t *testing.T, p project.Paths, proj string) runLaunch
		want   string
	}{
		{"refused before launch", "agent = \"plainagent\"\n", nil, ": refused, exit 1 (verified)"},
		{"ran", configuredAgenty, func(t *testing.T, p project.Paths, proj string) runLaunch {
			return fakeLaunch(t, &fakeRunner{}, p, proj, RunOptions{})
		}, ": ran, exit 0 (verified)"},
		{"box exited", configuredAgenty, func(t *testing.T, p project.Paths, proj string) runLaunch {
			return fakeLaunch(t, &fakeRunner{runErr: exitError(t, 1)}, p, proj, RunOptions{})
		}, ": box_exited, exit 1 (verified)"},
		{"byre failed", configuredAgenty, func(t *testing.T, p project.Paths, proj string) runLaunch {
			return fakeLaunch(t, &fakeRunner{createErr: errors.New("boom")}, p, proj, RunOptions{})
		}, ": byre_failed, exit 1 (verified)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, proj := runProject(t, tc.cfg)
			t.Setenv("BYRE_TASK", "do it")
			pinRun(t)
			launch := func(Streams, *headlessOptions) error { return nil }
			if tc.launch != nil {
				launch = tc.launch(t, p, proj)
			}
			s, _, stderr := runStreams("", false, false)
			_ = runWith(s, proj, RunOptions{}, launch)
			if got := lastLine(stderr.String()); got != "byre: run 0123456789abcdef"+tc.want {
				t.Errorf("last stderr line = %q, want the summary ending %q", got, tc.want)
			}
		})
	}
}

// The in-tree copy of the outcome is the --data document's bytes; a name the
// agent planted in the result dir during the run wins, and the copy is
// skipped with a note -- never written through.
func TestRunInTreeCopySkippedOnPlant(t *testing.T) {
	p, proj := runProject(t, configuredAgenty)
	t.Setenv("BYRE_TASK", "do it")
	s, out, stderr := runStreams("", false, false)
	o := RunOptions{Data: true}
	if err := runWith(s, proj, o, fakeLaunch(t, &fakeRunner{}, p, proj, o)); err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if b, err := os.ReadFile(filepath.Join(proj, runDirName, "outcome.json")); err != nil || !bytes.Equal(b, out.Bytes()) {
		t.Errorf("the in-tree copy must be the document's bytes: %q (%v)", b, err)
	}

	outcome := filepath.Join(proj, runDirName, "outcome.json")
	_, keep := func() (string, string) {
		dir := t.TempDir()
		k := filepath.Join(dir, "keep")
		mustWriteFile(t, k, []byte("victim"), 0o644)
		return dir, k
	}()
	f := &fakeRunner{}
	f.runHook = func() {
		if err := os.Symlink(keep, outcome); err != nil {
			t.Error(err)
		}
	}
	s2, out2, stderr2 := runStreams("", false, false)
	if err := runWith(s2, proj, o, fakeLaunch(t, f, p, proj, o)); err != nil {
		t.Fatalf("run: %v\n%s", err, stderr2)
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "victim" {
		t.Errorf("the planted name's target was written: %q %v", b, err)
	}
	if li, err := os.Lstat(outcome); err != nil || li.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the planted name must stand: %v %v", li, err)
	}
	if d := decodeRunData(t, out2.Bytes()); d.Outcome != outcomeRan {
		t.Errorf("the document on stdout is unaffected: %+v", d)
	}
	if !strings.Contains(stderr2.String(), "outcome.json not written") {
		t.Errorf("a skipped copy is said so:\n%s", stderr2)
	}
}

// A crash after staging still prints the document and the summary, then the
// panic continues to main's recovery.
func TestRunCrashWritesSummary(t *testing.T) {
	p, proj := runProject(t, configuredAgenty)
	t.Setenv("BYRE_TASK", "do it")
	f := &fakeRunner{}
	f.runHook = func() { panic("boom") }
	o := RunOptions{Data: true}
	s, out, stderr := runStreams("", false, false)
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = runWith(s, proj, o, fakeLaunch(t, f, p, proj, o))
	}()
	if recovered != "boom" {
		t.Fatalf("the panic must continue to main's recovery, got %v", recovered)
	}
	if d := decodeRunData(t, out.Bytes()); d.Outcome != outcomeCrashed || d.ExitCode != 70 {
		t.Errorf("crash document: %+v", d)
	}
	if !strings.HasSuffix(lastLine(stderr.String()), ": crashed, exit 70 (verified)") {
		t.Errorf("summary: %q", lastLine(stderr.String()))
	}

	// Before staging there is nothing to report: the panic passes through.
	_, proj2 := runProject(t, configuredAgenty)
	t.Setenv("BYRE_TASK", "do it")
	s2, out2, stderr2 := runStreams("", false, false)
	func() {
		defer func() { recovered = recover() }()
		_ = runWith(s2, proj2, o, func(Streams, *headlessOptions) error { panic("early") })
	}()
	if recovered != "early" || out2.Len() != 0 || strings.Contains(stderr2.String(), "crashed") {
		t.Errorf("a pre-staging crash prints nothing of run's: %v %q %q", recovered, out2, stderr2)
	}
}

// --timeout's grammar: coreutils durations, zero refused with the remedy,
// and the 1s..3650d range the chassis's whole-second deadline holds.
func TestCheckRunTimeout(t *testing.T) {
	for _, v := range []string{"600", "30m", "1.5h", "2d", "10s", "1", "1s", "2s", "0.02m", "3650d", "315360000", "0000000000001s", "0.0000249d"} {
		if err := CheckRunTimeout(v); err != nil {
			t.Errorf("%q: %v", v, err)
		}
	}
	for v, want := range map[string]string{"0": "omit the flag", "0.0m": "omit the flag", "10x": "not a duration", "": "not a duration", "-5": "not a duration", "1.": "not a duration",
		"0.5": "the smallest --timeout is 1s", "0.9s": "the smallest --timeout is 1s", "0.000001s": "the smallest --timeout is 1s", "0.0000001d": "the smallest --timeout is 1s",
		"3651d": "the largest --timeout is 3650d", "315360001": "the largest --timeout is 3650d", "3650.00002d": "the largest --timeout is 3650d", "999999999999999999d": "the largest --timeout is 3650d", "1000000000": "the largest --timeout is 3650d"} {
		if err := CheckRunTimeout(v); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want %q, got %v", v, want, err)
		}
	}
}

// The host's --timeout check and the chassis's BYRE_RUN_TIMEOUT parse are
// two implementations of one range: each spelling is accepted by both or
// refused by both, with the same whole seconds. The chassis's centisecond
// deadline is pinned too: the fraction is scaled by the unit before it is
// floored, so 0.0000249d (2.15136 s) is 215 cs, not the 207 a fraction cut
// to six digits before scaling gives. The spellings are the host's grammar:
// the chassis's also takes a bare point (1., .5), which the host refuses.
func TestRunTimeoutMatchesTheChassis(t *testing.T) {
	testtools.NeedTool(t, "bash")
	src := string(gen.HeadlessScript())
	start := strings.Index(src, "\ntimeout_spec=${BYRE_RUN_TIMEOUT:-}\n")
	end := strings.Index(src, "\n# --- 2.")
	if start < 0 || end < start {
		t.Fatal("headless.sh has no BYRE_RUN_TIMEOUT parse block before section 2")
	}
	parse := "refuse() { echo refused; exit 16; }\nuptime_cs() { :; }\n" + src[start:end] +
		"\nprintf '%s %s\\n' \"$timeout_secs\" \"$timeout_cs\"\n"
	chassis := func(v string) (secs, cs int64, ok bool) {
		cmd := exec.Command("bash", "-c", parse)
		cmd.Env = []string{"BYRE_RUN_TIMEOUT=" + v, "PATH=" + os.Getenv("PATH")}
		out, err := cmd.Output()
		if strings.TrimSpace(string(out)) == "refused" {
			return 0, 0, false
		}
		if err != nil {
			t.Fatalf("%q: the parse block failed: %v (%q)", v, err, out)
		}
		f := strings.Fields(string(out))
		if len(f) != 2 {
			t.Fatalf("%q: the parse block printed %q", v, out)
		}
		secs, _ = strconv.ParseInt(f[0], 10, 64)
		cs, _ = strconv.ParseInt(f[1], 10, 64)
		return secs, cs, true
	}
	wantCS := map[string]int64{
		"0.0000249d": 215, "0.0000249999999d": 215, "0.00002314814814d": 199,
		"1.9s": 190, "1.999999999s": 199, "1.9999999999s": 199, "0.02m": 120,
		"1.5h": 540000, "1.0000000001d": 8640000, "3650d": 31536000000,
	}
	spellings := []string{"600", "30m", "1.5h", "2d", "10s", "1", "1s", "2s", "0.02m", "3650d", "315360000", "0000000000001s",
		"0.5", "0.9s", "0.000001s", "0.0000001d", "0.0000116d", "0.0000115740d", "0.000011574074d",
		"3651d", "315360001", "3650.00002d", "3650.0000000001d", "999999999999999999d", "1000000000", "10x", "-5"}
	for v := range wantCS {
		spellings = append(spellings, v)
	}
	for _, v := range spellings {
		secs, cs, accepted := chassis(v)
		if hostOK := CheckRunTimeout(v) == nil; hostOK != accepted {
			t.Errorf("%q: host accepts %v, chassis accepts %v", v, hostOK, accepted)
			continue
		}
		if !accepted {
			continue
		}
		if hostSecs, _ := runTimeoutSecs(v); hostSecs != secs {
			t.Errorf("%q: host %d s, chassis %d s", v, hostSecs, secs)
		}
		if want, ok := wantCS[v]; ok && cs != want {
			t.Errorf("%q: chassis timeout_cs = %d, want %d", v, cs, want)
		}
	}
}
