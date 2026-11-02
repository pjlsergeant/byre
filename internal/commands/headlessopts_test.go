package commands

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/lock"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// The stage callback runs after Create succeeded and BEFORE the setup lock
// is released: the created container is the ownership token, and the lock is
// what keeps reset/forget from tearing the store down under the staging. The
// proof is the fake's op order plus a lock attempt from inside the callback,
// which must find the lock held. It also receives the under-lock re-read, not
// the rv develop entered with.
func TestHeadlessStageRunsAfterCreateUnderLock(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{}
	rv := combine(merged(config.Config{}), skills.Resolved{})
	rv.reread = func() (resolved, error) {
		return combine(merged(config.Config{Agent: "fresh-read"}), skills.Resolved{}), nil
	}
	var stagedAgent string
	lockHeld := false
	h := &headlessOptions{Stage: func(sp project.Paths, srv resolved) error {
		f.mu.Lock()
		f.ops = append(f.ops, "stage")
		f.mu.Unlock()
		stagedAgent = srv.cfg.Agent
		l, ok, err := lock.TryAcquire(sp.LockFile)
		if err != nil {
			t.Errorf("lock probe: %v", err)
			return nil
		}
		if ok {
			_ = l.Release()
			return nil
		}
		lockHeld = true
		return nil
	}}
	if err := developWith(f, discardStreams(), p, rv, false, CredentialAsk, h); err != nil {
		t.Fatal(err)
	}
	ci, si, ai := slices.Index(f.ops, "createbox"), slices.Index(f.ops, "stage"), slices.Index(f.ops, "start")
	if ci < 0 || si != ci+1 || ai < si {
		t.Errorf("want createbox, then stage at once, then start; ops = %v", f.ops)
	}
	if !lockHeld {
		t.Error("the stage callback must run while develop still holds the setup lock")
	}
	if stagedAgent != "fresh-read" {
		t.Errorf("stage must receive the under-lock re-read, got agent %q", stagedAgent)
	}
}

// A Create that fails never stages: there is no ownership token to stage
// under, and the result says no box of this launch existed.
func TestHeadlessCreateFailureNeverStages(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{createErr: errors.New("name in use")}
	staged := false
	var res developResult
	h := &headlessOptions{Stage: func(project.Paths, resolved) error { staged = true; return nil }, Result: &res}
	if err := developWith(f, discardStreams(), p, combine(merged(config.Config{}), skills.Resolved{}), false, CredentialAsk, h); err == nil {
		t.Fatal("a failed create must fail the launch")
	}
	if staged {
		t.Error("stage ran after a failed Create")
	}
	if res.Phase != phasePrelaunch || res.ContainerStatus != -1 {
		t.Errorf("result = %+v, want prelaunch with status -1", res)
	}
}

// A stage failure removes the just-created container, never attaches, and
// hands the callback's own error back for its caller to map -- develop adds no
// wrapping text of its own around it.
func TestHeadlessStageFailureRemovesContainer(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{}
	var res developResult
	stageErr := ExitError{Code: 1}
	h := &headlessOptions{Stage: func(project.Paths, resolved) error { return stageErr }, Result: &res}
	s, _, stderr := testStreams("", false)
	err := developWith(f, s, p, combine(merged(config.Config{}), skills.Resolved{}), false, CredentialAsk, h)
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("want the stage's ExitError{1} back, got %v", err)
	}
	if err.Error() != stageErr.Error() {
		t.Errorf("develop must not add text to a stage error, got %q", err.Error())
	}
	if !slices.Contains(f.rmContainers, containerName(p)) {
		t.Errorf("the created container must be removed after a stage failure, removed = %v", f.rmContainers)
	}
	if len(f.starts) != 0 {
		t.Errorf("must not attach after a stage failure: %v", f.starts)
	}
	if res.Phase != phasePrelaunch {
		t.Errorf("phase = %v, want prelaunch (the box was removed before it ran)", res.Phase)
	}
	if strings.Contains(stderr.String(), "byre: exposure:") {
		t.Errorf("a launch that never started claims no walls: %s", stderr.String())
	}
}

// Headless attach: byre-owned stdout, NO stdin, the command last on the argv,
// -i kept and no -t when the streams are not a terminal.
func TestHeadlessAttachUsesOwnedIO(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{attachOut: "the answer\n"}
	var out bytes.Buffer
	h := &headlessOptions{Command: []string{"byre-headless"}, AttachStdout: &out}
	if err := developWith(f, discardStreams(), p, combine(merged(config.Config{}), skills.Resolved{}), false, CredentialAsk, h); err != nil {
		t.Fatal(err)
	}
	if len(f.startIOs) != 1 {
		t.Fatalf("want one StartAttachIO call, got %d (starts %v)", len(f.startIOs), f.starts)
	}
	call := f.startIOs[0]
	if call.container != containerName(p) || call.stdin != nil || call.stdout != &out {
		t.Errorf("StartAttachIO(%q, %v, %v): want the session container, nil stdin, the given stdout", call.container, call.stdin, call.stdout)
	}
	if out.String() != "the answer\n" {
		t.Errorf("box stdout must reach the given writer, got %q", out.String())
	}
	argv := f.creates[0]
	// The image, then the command: nothing may follow it.
	if argv[len(argv)-1] != "byre-headless" || !strings.HasPrefix(argv[len(argv)-2], "byre-") {
		t.Errorf("the command must be last on the argv, right after the image: %q", argv)
	}
	if !slices.Contains(argv, "-i") || slices.Contains(argv, "-t") {
		t.Errorf("want -i and no -t on a non-TTY launch: %q", argv)
	}
}

// Without headless options develop attaches as it always has: byre's own
// stdio through StartAttach, and the image's default command.
func TestDevelopWithoutHeadlessUsesStartAttach(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{}
	if err := developWith(f, discardStreams(), p, combine(merged(config.Config{}), skills.Resolved{}), false, CredentialAsk, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.startIOs) != 0 || len(f.starts) != 1 {
		t.Errorf("want StartAttach only: starts %v, startIOs %d", f.starts, len(f.startIOs))
	}
	if slices.Contains(f.creates[0], "byre-headless") {
		t.Errorf("no command without headless options: %q", f.creates[0])
	}
}

// The BYRE_RUN_* set is re-asserted after skill env: a skill's [runtime].env
// that sets one is overridden. A raw run_args `-e` still follows it on the
// argv, so last-wins is the engine's, as ADR 0006 has it.
func TestHeadlessEnvBeatsSkillEnvAndLosesToRunArgs(t *testing.T) {
	p, _ := testPaths(t)
	var sk skills.Skill
	sk.Name = "knobs"
	sk.File.Runtime.Env = map[string]string{"BYRE_RUN_TASK_FILE": "/elsewhere"}
	cfg := config.Config{RunArgs: []string{"-e", "BYRE_RUN_TASK_FILE="}}
	f := &fakeRunner{}
	h := &headlessOptions{Env: map[string]string{"BYRE_RUN_TASK_FILE": "/workspace/.byre-run/task.md"}}
	if err := developWith(f, discardStreams(), p, combine(merged(cfg), skills.Resolved{Skills: []skills.Skill{sk}}), false, CredentialAsk, h); err != nil {
		t.Fatal(err)
	}
	argv := f.creates[0]
	ours := slices.Index(argv, "BYRE_RUN_TASK_FILE=/workspace/.byre-run/task.md")
	raw := slices.Index(argv, "BYRE_RUN_TASK_FILE=")
	if ours < 1 || argv[ours-1] != "-e" {
		t.Fatalf("the run's own value must be on the argv as -e: %q", argv)
	}
	if slices.Contains(argv, "BYRE_RUN_TASK_FILE=/elsewhere") {
		t.Errorf("a skill's [runtime].env must not set a BYRE_RUN_ key over the run's: %q", argv)
	}
	if raw < ours {
		t.Errorf("raw run_args must follow the run's env (last-wins): ours at %d, raw at %d in %q", ours, raw, argv)
	}
}

// A headless launch writes its run facts into the record it creates the box
// with; the task's hash, never the task. A develop launch writes none.
func TestHeadlessLaunchRecordCarriesRun(t *testing.T) {
	p, _ := testPaths(t)
	var res developResult
	sum := strings.Repeat("cd", 32)
	h := &headlessOptions{RunID: "r-1", TaskSHA256: sum, Result: &res}
	f := &fakeRunner{}
	if err := developWith(f, discardStreams(), p, combine(merged(config.Config{}), skills.Resolved{}), false, CredentialAsk, h); err != nil {
		t.Fatal(err)
	}
	rec, st := readLaunchRecord(p, map[string]string{launchKey: res.LaunchHash})
	if st != launchRecordOK {
		t.Fatalf("record state %v", st)
	}
	if !rec.Headless || rec.RunID != "r-1" || rec.TaskSHA256 != sum || rec.RunTimeout == nil || *rec.RunTimeout != "" {
		t.Errorf("headless record = headless %v run_id %q task %q timeout %v", rec.Headless, rec.RunID, rec.TaskSHA256, rec.RunTimeout)
	}

	var plain developResult
	p2, _ := testPaths(t)
	if err := developWith(&fakeRunner{}, discardStreams(), p2, combine(merged(config.Config{}), skills.Resolved{}), false, CredentialAsk, &headlessOptions{Result: &plain}); err != nil {
		t.Fatal(err)
	}
	if err := developWith(&fakeRunner{}, discardStreams(), p2, combine(merged(config.Config{}), skills.Resolved{}), false, CredentialAsk, nil); err != nil {
		t.Fatal(err)
	}
	b, err := hostopen.ReadDirNoFollow(launchesDir(p2))
	if err != nil {
		t.Fatal(err)
	}
	developRecords := 0
	for _, e := range b {
		name := e.Name()
		if strings.TrimSuffix(name, ".toml") == plain.LaunchHash {
			continue
		}
		r, st := readLaunchRecord(p2, map[string]string{launchKey: strings.TrimSuffix(name, ".toml")})
		if st != launchRecordOK {
			continue
		}
		developRecords++
		if r.Headless || r.RunID != "" || r.TaskSHA256 != "" || r.RunTimeout != nil {
			t.Errorf("a develop record carries no run facts: %+v", r)
		}
	}
	if developRecords == 0 {
		t.Fatal("test setup: no develop record found")
	}
}

// developResult population across the outcomes a caller must tell apart.
func TestHeadlessResultMatrix(t *testing.T) {
	cases := []struct {
		name       string
		fake       func(t *testing.T, p project.Paths) *fakeRunner
		cfg        config.Config
		wantPhase  launchPhase
		wantStatus int
		wantLive   bool
		wantRaw    bool
		wantErr    func(err error) bool
	}{
		{
			name:       "create fails",
			fake:       func(t *testing.T, p project.Paths) *fakeRunner { return &fakeRunner{createErr: errors.New("boom")} },
			wantPhase:  phasePrelaunch,
			wantStatus: -1,
			wantErr:    func(err error) bool { return err != nil },
		},
		{
			name:       "clean attach",
			fake:       func(t *testing.T, p project.Paths) *fakeRunner { return &fakeRunner{} },
			wantPhase:  phaseAttached,
			wantStatus: 0,
			wantErr:    func(err error) bool { return err == nil },
		},
		{
			name:       "box exits 11",
			fake:       func(t *testing.T, p project.Paths) *fakeRunner { return &fakeRunner{runErr: exitError(t, 11)} },
			wantPhase:  phaseAttached,
			wantStatus: 11,
			wantErr:    func(err error) bool { var e ExitError; return errors.As(err, &e) && e.Code == 11 },
		},
		{
			name:       "box killed, 137",
			fake:       func(t *testing.T, p project.Paths) *fakeRunner { return &fakeRunner{runErr: exitError(t, 137)} },
			wantPhase:  phaseAttached,
			wantStatus: 137,
			// develop still decodes 128+ into a plain error, as today.
			wantErr: func(err error) bool {
				var e ExitError
				return err != nil && !errors.As(err, &e) && strings.Contains(err.Error(), "SIGKILL")
			},
		},
		{
			name: "attach fails, box still running",
			fake: func(t *testing.T, p project.Paths) *fakeRunner {
				return &fakeRunner{runErr: errors.New("attach lost"), liveSecond: liveWorkdir(p, "cafe0001")}
			},
			wantPhase:  phaseAttached,
			wantStatus: -1,
			wantLive:   true,
			wantErr:    func(err error) bool { var e ExitError; return errors.As(err, &e) && e.Code == ExitRefused },
		},
		{
			name:       "raw run_args",
			fake:       func(t *testing.T, p project.Paths) *fakeRunner { return &fakeRunner{} },
			cfg:        config.Config{RunArgs: []string{"--pids-limit", "512"}},
			wantPhase:  phaseAttached,
			wantStatus: 0,
			wantRaw:    true,
			wantErr:    func(err error) bool { return err == nil },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := testPaths(t)
			var res developResult
			h := &headlessOptions{Result: &res}
			err := developWith(tc.fake(t, p), discardStreams(), p, combine(merged(tc.cfg), skills.Resolved{}), false, CredentialAsk, h)
			if !tc.wantErr(err) {
				t.Errorf("unexpected error shape: %v", err)
			}
			if res.Phase != tc.wantPhase || res.ContainerStatus != tc.wantStatus || res.Live != tc.wantLive || res.RawRunArgs != tc.wantRaw {
				t.Errorf("result = %+v, want phase %v status %d live %v raw %v", res, tc.wantPhase, tc.wantStatus, tc.wantLive, tc.wantRaw)
			}
			if tc.wantPhase >= phaseCreated && len(res.LaunchHash) != 64 {
				t.Errorf("a created box carries its launch record's address, got %q", res.LaunchHash)
			}
		})
	}
}

// The result slice is the printed report, line for line -- the hostile
// content escaped exactly as stderr got it.
func TestReportExitReturnsPrintedLines(t *testing.T) {
	empty := exitSnapshot{
		hooks: map[string]string{}, config: map[string]map[string]string{},
		env: map[string]map[string]string{}, hooksWalked: true, configListed: true,
		envListed: true, unreadable: map[string]bool{}, configFromListing: map[string]bool{},
	}
	after := empty
	after.hooks = map[string]string{".git/hooks/pre-commit\x1b[2J": "sig"}
	var b bytes.Buffer
	lines := reportExit(&b, empty, after)
	if len(lines) == 0 {
		t.Fatal("a changed hook must produce a report")
	}
	if got := strings.Join(lines, "\n") + "\n"; got != b.String() {
		t.Errorf("returned lines differ from printed output:\n--- returned ---\n%s--- printed ---\n%s", got, b.String())
	}
	var silent bytes.Buffer
	if lines := reportExit(&silent, empty, empty); lines != nil || silent.Len() != 0 {
		t.Errorf("a silent report returns nil and prints nothing, got %q / %q", lines, silent.String())
	}
}
