package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// MaxTaskBytes bounds a headless run's task, whichever source it came from.
// The chassis's hash check reads one byte past it, so an oversized file in
// the box can never match the host's hash either.
const MaxTaskBytes = 1 << 20

// RunDataVersion is the shape of the `byre run --data` document. Bump it when
// a consumer would have to change, and note the change in CHANGES.md.
//
// 1 (2026-11-02): the first shape -- version, run_id, outcome, authority,
// exit_code, agent_exit_code, phase, agent, launch_record, started_at,
// finished_at, message, exit_report.
const RunDataVersion = 1

// runDirName is the result dir, directly under the work tree.
const runDirName = ".byre-run"

// The outcome classes the --data document and the summary line name. usage
// never reaches Run: cmd/byre rejects it before dispatch.
const (
	outcomeRan        = "ran"
	outcomeRefused    = "refused"
	outcomeBoxExited  = "box_exited"
	outcomeByreFailed = "byre_failed"
	outcomeCrashed    = "crashed"
)

// authorityVerified / authorityRawRunArgs: with raw run_args in force the box
// may have been handed any mount or a different PID 1, and byre does not
// parse the block (ADR 0006), so every outcome says so.
const (
	authorityVerified   = "verified"
	authorityRawRunArgs = "unverified: raw run_args"
)

// exitCrash is the status main exits a recovered panic with; the crashed
// outcome reports the same number.
const exitCrash = 70

// RunOptions is `byre run`'s parsed command line. cmd/byre has already
// rejected the flag-shape errors (no task source, `--task -` with
// `--credentials stdin`, a zero or malformed --timeout), so Run trusts them.
type RunOptions struct {
	// TaskFlag is --task: "" when not given, "-" for stdin, else a path.
	TaskFlag string
	// Data prints the versioned outcome document on stdout instead of the
	// agent's answer.
	Data bool
	// Quiet keeps the agent's live stream in the result dir and drops
	// byre's banner-class notices.
	Quiet bool
	// Agent is --agent: the run's agent on a configured project, the
	// project's agent on a new one (with Template).
	Agent    string
	Template string
	// Model is handed to the headless script as BYRE_RUN_MODEL ("" = the
	// agent's default).
	Model string
	// Timeout is a validated, non-zero coreutils duration, or "" for none.
	Timeout  string
	CredMode CredentialMode
}

// runTimeoutRE is coreutils timeout(1)'s duration: an integer or decimal with
// an optional s/m/h/d suffix -- the grammar byre-codereview validates, a
// subset of what the chassis accepts.
var runTimeoutRE = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[smhd]?$`)
var runTimeoutZeroRE = regexp.MustCompile(`^0+(\.0+)?[smhd]?$`)

// CheckRunTimeout validates --timeout. A zero duration is no timeout at all,
// and a run has none unless asked, so 0 is refused with the remedy rather
// than read as "no timer".
func CheckRunTimeout(v string) error {
	if !runTimeoutRE.MatchString(v) {
		return fmt.Errorf("--timeout %q: not a duration -- use a number with an optional s/m/h/d suffix, e.g. 600, 30m, 1.5h", packages.EscapeTerminal(v))
	}
	if runTimeoutZeroRE.MatchString(v) {
		return fmt.Errorf("--timeout %s: a zero timeout is no timeout -- omit the flag (a run has no timeout unless you give one)", v)
	}
	return nil
}

// runLaunch starts the session; production is developCommandWith, tests
// drive developWith against a fake engine.
type runLaunch func(ds Streams, h *headlessOptions) error

// Seams for the --data contract test: the clock and the run id.
var (
	runClock = time.Now
	newRunID = hostopen.NewRunID
)

// Run implements `byre run`: one task, handed to the selected agent's headless
// command in a fresh box, with byre's own verdict as the exit code (and, under
// --data, as a versioned JSON document on stdout).
func Run(s Streams, projectDir string, o RunOptions) error {
	return runWith(s, projectDir, o, func(ds Streams, h *headlessOptions) error {
		return developCommandWith(ds, projectDir, o.Template, o.Agent, nil, false, o.CredMode, true, h)
	})
}

// runState is one invocation's account, filled as Run goes and read by the
// finishers.
type runState struct {
	s         Streams
	o         RunOptions
	runID     string
	started   time.Time
	agent     string
	workDir   string
	res       developResult
	staged    bool
	refusal   string // run's own refusal text, when it refused
	finalized bool
}

func runWith(s Streams, projectDir string, o RunOptions, launch runLaunch) (err error) {
	st := &runState{s: s, o: o, started: runClock(), res: developResult{ContainerStatus: -1}}
	id, err := newRunID()
	if err != nil {
		return err
	}
	st.runID = id
	// A crash after staging still owes the caller its outcome: the document
	// (when asked) and the summary line, best-effort, then the panic goes on
	// to main's recovery, which prints the report and exits 70.
	defer func() {
		if r := recover(); r != nil {
			if st.staged && !st.finalized {
				func() {
					defer func() { _ = recover() }()
					st.finish(outcomeOf(outcomeCrashed, exitCrash, knownStatus(st.res), fmt.Sprintf("byre crashed: %v", r)))
				}()
			}
			panic(r)
		}
	}()
	err = st.run(projectDir, launch)
	st.finish(classifyRun(err, st.res, st.refusal))
	return err
}

// refuse prints a refusal in byre's register and returns the code main exits
// with, no banner. The text is held for the outcome's message.
func (st *runState) refuse(format string, args ...any) error {
	var b strings.Builder
	dataf(&b, format, args...)
	msg := b.String()
	fmt.Fprintf(st.s.Err, "byre: %s\n", msg)
	st.refusal = msg
	return ExitError{Code: 1}
}

func (st *runState) run(projectDir string, launch runLaunch) error {
	s, o := st.s, st.o
	// The task source first: the flag beats byre's own environment, and
	// says so once when both were set.
	envTask, envSet := os.LookupEnv("BYRE_TASK")
	envSet = envSet && envTask != ""
	var task []byte
	taskPath := ""
	switch {
	case o.TaskFlag != "":
		if envSet {
			fmt.Fprintln(s.Err, "byre: --task given; BYRE_TASK in the environment is ignored for this run.")
		}
		if o.TaskFlag == "-" {
			// Reads to EOF by definition: a writer that never closes its end
			// holds the run here, before anything is built.
			b, err := io.ReadAll(io.LimitReader(s.In, MaxTaskBytes+1))
			if err != nil {
				return st.refuse("--task -: reading the task from stdin: %v", err)
			}
			task = b
		} else {
			p, err := config.ExpandTilde(o.TaskFlag)
			if err != nil {
				return st.refuse("--task %s: %v", o.TaskFlag, err)
			}
			// Relative to the directory byre was run in, like every path a
			// command line names.
			if taskPath, err = filepath.Abs(p); err != nil {
				return st.refuse("--task %s: %v", o.TaskFlag, err)
			}
		}
	case envSet:
		task = []byte(envTask)
	default:
		return st.refuse("no task: pass --task <file>, --task - (stdin), or set BYRE_TASK")
	}

	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	st.workDir = paths.WorkDir

	// The non-TTY onboarding arms' remedy ("run on a TTY") can never be
	// followed inside run, so an unconfigured project is judged here: both
	// axis flags configure it on the way in, anything less is refused with
	// run's own remedy.
	cfgPath := filepath.Join(paths.Dir, config.ProjectConfigName)
	configured, perr := hostopen.ExistsNoFollow(cfgPath)
	if perr != nil {
		return fmt.Errorf("cannot tell whether %s exists (%v) — fix the store's permissions, or run 'byre forget' to clear it", cfgPath, perr)
	}
	if !configured && (o.Template == "" || o.Agent == "") {
		return st.refuse(`this project is not configured here: pass --template <t> and --agent <a> (use "none" for no template), or configure it first with byre develop / byre preset apply`)
	}

	// The pre-lock read: which agent, and which roots the box can write (the
	// task file's route is judged against them). develop reads again under
	// the lock and Stage re-checks against that read.
	rv, err := resolveWithAgent(paths, projectDir, s.Err, o.Agent)
	if err != nil {
		return err
	}
	st.agent = rv.cfg.Agent
	if rerr := st.checkAgent(rv); rerr != nil {
		return rerr
	}

	var plan taskPlan
	if taskPath != "" {
		if plan, err = planTaskRead(taskPath, runWritableRoots(paths, rv)); err != nil {
			return st.refuse("--task: %v", err)
		}
		if task, err = readTask(taskPath, plan); err != nil {
			return st.refuse("--task: %v", err)
		}
	}
	if len(task) > MaxTaskBytes {
		return st.refuse("the task is over the %d-byte limit", MaxTaskBytes)
	}
	if strings.TrimSpace(string(task)) == "" {
		return st.refuse("the task is empty: give the agent something to do")
	}
	sum := sha256.Sum256(task)
	taskSHA := hex.EncodeToString(sum[:])

	runDir := workspaceTarget + "/" + runDirName
	env := map[string]string{
		"BYRE_RUN_TASK_FILE":   runDir + "/task.md",
		"BYRE_RUN_TASK_SHA256": taskSHA,
		"BYRE_RUN_DIR":         runDir,
		"BYRE_RUN_MODEL":       o.Model,
		"BYRE_RUN_AGENT":       st.agent,
		"BYRE_RUN_ID":          st.runID,
	}
	if o.Timeout != "" {
		env["BYRE_RUN_TIMEOUT"] = o.Timeout
	}
	if o.Quiet {
		env["BYRE_RUN_QUIET"] = "1"
	}

	// The box's stdout is the answer: discarded under --data (the document is
	// the only thing stdout ever carries then), escaped per line on a
	// terminal, byte for byte into a file or a pipe.
	var boxOut io.Writer = io.Discard
	sw := &stickyWriter{w: s.Out}
	var esc *lineEscaper
	switch {
	case o.Data:
	case s.OutTTY:
		esc = &lineEscaper{w: sw}
		boxOut = esc
	default:
		boxOut = sw
	}

	h := &headlessOptions{
		Command:      []string{gen.HeadlessPath},
		Env:          env,
		AttachStdout: boxOut,
		StreamStdout: s.Err,
		RunID:        st.runID,
		TaskSHA256:   taskSHA,
		RunTimeout:   o.Timeout,
		Quiet:        o.Quiet,
		Result:       &st.res,
	}
	h.Stage = func(sp project.Paths, lrv resolved) error {
		// The under-lock read is the box that was created: its agent must be
		// the one the run was told about, and its writable roots must not
		// have made the task's route one the pre-lock read did not judge.
		if lrv.cfg.Agent != st.agent {
			return st.refuse("the agent changed to %s while byre waited for the setup lock (this run resolved %s); nothing ran -- re-run", config.OrNone(lrv.cfg.Agent), config.OrNone(st.agent))
		}
		if rerr := st.checkAgent(lrv); rerr != nil {
			return rerr
		}
		if taskPath != "" {
			lplan, perr := planTaskRead(taskPath, runWritableRoots(sp, lrv))
			if perr != nil {
				return st.refuse("--task: %v", perr)
			}
			if lplan != plan {
				again, rerr := readTask(taskPath, lplan)
				if rerr != nil {
					return st.refuse("--task: %v", rerr)
				}
				if !bytes.Equal(again, task) {
					return st.refuse("--task %s: the file changed while byre waited for the setup lock; nothing ran -- re-run", taskPath)
				}
			}
		}
		if serr := stageRunDir(sp.WorkDir, task); serr != nil {
			return st.refuse("%v", serr)
		}
		st.staged = true
		return nil
	}

	ds := s
	ds.TTY = false
	// develop prints nothing on stdout; anything that tried would land in
	// front of the answer, so its stdout is byre's stderr here.
	ds.Out = s.Err
	err = launch(ds, h)
	if esc != nil {
		esc.flush()
	}
	if sw.err != nil {
		dataf(s.Err, "byre: run: writing the agent's answer to stdout failed: %v\n", sw.err)
	}
	return err
}

// checkAgent refuses an agentless resolution and an agent with no headless
// command.
func (st *runState) checkAgent(rv resolved) error {
	if rv.skills.Agent == nil {
		return st.refuse("no agent selected: set agent in byre config, or pass -a <skill>")
	}
	if rv.skills.AgentHeadless() == "" {
		others := headlessAgents(rv)
		if len(others) == 0 {
			return st.refuse("agent %s declares no headless command; no installed agent declares one", rv.cfg.Agent)
		}
		return st.refuse("agent %s declares no headless command; agents that do: %s", rv.cfg.Agent, strings.Join(others, ", "))
	}
	return nil
}

// headlessAgents lists the installed agent skills that declare a headless
// command, for the refusal's remedy.
func headlessAgents(rv resolved) []string {
	if rv.cat == nil {
		return nil
	}
	var out []string
	for _, n := range skills.ListAgentSkills(rv.cat) {
		if sk, err := skills.Load(rv.cat, n); err == nil && sk.File.Agent != nil && sk.File.Agent.Headless != "" {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// taskRoot is one directory the box can write, as spelled and as resolved.
type taskRoot struct{ spelled, canon string }

// runWritableRoots is boxWritableRoots plus every rw bind source of the
// resolved mounts: the directories a task path must not be routed through
// by a component the agent swapped.
func runWritableRoots(paths project.Paths, rv resolved) []taskRoot {
	dirs := []string{paths.WorkDir, paths.Canonical, paths.CommonGitDirHost, paths.Dir}
	for _, m := range rv.mounts {
		if m.Disabled || m.Mode != "rw" {
			continue
		}
		if host, err := expandHostPath(m.Host); err == nil {
			dirs = append(dirs, host)
		}
	}
	var out []taskRoot
	seen := map[taskRoot]bool{}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		d = filepath.Clean(d)
		c, err := project.Canonicalize(d)
		if err != nil {
			c = d
		}
		if d == string(filepath.Separator) || c == string(filepath.Separator) {
			continue
		}
		r := taskRoot{spelled: d, canon: c}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// taskPlan is how the task file is read: through an anchored root (root set)
// or, outside every writable root, by its path with the leaf unfollowed.
type taskPlan struct{ root, rel string }

// lexUnder returns p relative to root when p is lexically inside it.
func lexUnder(root, p string) (string, bool) {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// planTaskRead decides the task file's route. A path spelled under a
// writable root is read through that root, so an interior component the
// agent swapped cannot carry the read outside it. A path whose directory
// resolves into one (the user stands in the tree through a symlinked path)
// is read the same way, by its resolved route. A path that passes through a
// writable root and resolves outside it is refused: an agent-made symlink
// is the only way that route exists.
func planTaskRead(abs string, roots []taskRoot) (taskPlan, error) {
	for _, r := range roots {
		for _, base := range []string{r.spelled, r.canon} {
			if rel, ok := lexUnder(base, abs); ok {
				return taskPlan{root: r.canon, rel: rel}, nil
			}
		}
	}
	dir := filepath.Dir(abs)
	if cdir, err := project.Canonicalize(dir); err == nil {
		cabs := filepath.Join(cdir, filepath.Base(abs))
		for _, r := range roots {
			if rel, ok := lexUnder(r.canon, cabs); ok {
				return taskPlan{root: r.canon, rel: rel}, nil
			}
		}
	}
	for _, r := range roots {
		if hostopen.InTreeByIdentity(r.canon, dir) {
			return taskPlan{}, fmt.Errorf("%s passes through %s, which the box can write, and resolves outside it -- pass the resolved path", abs, r.canon)
		}
	}
	return taskPlan{}, nil
}

// readTask reads the task file by plan, bounded, refusing a symlinked leaf on
// both routes.
func readTask(abs string, plan taskPlan) ([]byte, error) {
	var f *os.File
	var fi os.FileInfo
	var err error
	if plan.root == "" {
		f, fi, err = hostopen.OpenRegular(abs, false)
	} else {
		var root *os.Root
		if root, err = hostopen.OpenDirRootNoFollow(plan.root); err != nil {
			return nil, fmt.Errorf("%s: %w", abs, err)
		}
		defer root.Close()
		if li, lerr := root.Lstat(plan.rel); lerr == nil && li.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s is a symlink -- pass the resolved path", abs)
		}
		if f, fi, err = hostopen.OpenRegularIn(root, plan.rel); err != nil && !errors.Is(err, os.ErrNotExist) {
			// The read rides a root the box can write: a symlink on the way
			// that leaves it, or names an absolute path, is refused there.
			return nil, fmt.Errorf("%s: %w (read inside %s, which the box can write -- pass the resolved path)", abs, err, plan.root)
		}
	}
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("%s is a symlink -- pass the resolved path", abs)
		}
		return nil, fmt.Errorf("%s: %w", abs, err)
	}
	defer f.Close()
	if fi.Size() > MaxTaskBytes {
		return nil, fmt.Errorf("%s: the task is over the %d-byte limit", abs, MaxTaskBytes)
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxTaskBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", abs, err)
	}
	if len(b) > MaxTaskBytes {
		return nil, fmt.Errorf("%s: the task is over the %d-byte limit", abs, MaxTaskBytes)
	}
	return b, nil
}

// stageRunDir makes <workDir>/.byre-run an empty directory holding byre's two
// files: a self-ignoring .gitignore and the task. It runs under the setup
// lock after Create, so no other launch of this worktree can reach it; the
// agent's own earlier output is what it clears. Every touch is anchored: the
// work tree without following its final component, the result dir the same
// way (ADR 0040's ancestor residual accepted), each entry removed through
// that root (a symlink is unlinked, never followed), each file published
// exclusively.
func stageRunDir(workDir string, task []byte) error {
	ws, err := hostopen.OpenDirRootNoFollow(workDir)
	if err != nil {
		return fmt.Errorf("staging %s: %w", runDirName, err)
	}
	defer ws.Close()
	dir := filepath.Join(workDir, runDirName)
	li, err := ws.Lstat(runDirName)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := ws.Mkdir(runDirName, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	case err != nil:
		return fmt.Errorf("staging %s: %w", dir, err)
	case li.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("%s is a symlink, not a directory -- move it aside; byre stages the run's files there", dir)
	case !li.IsDir():
		return fmt.Errorf("%s is not a directory -- move it aside; byre stages the run's files there", dir)
	}
	rd, err := hostopen.OpenDirRootNoFollow(dir)
	if err != nil {
		return fmt.Errorf("staging %s: %w", dir, err)
	}
	defer rd.Close()
	d, err := rd.Open(".")
	if err != nil {
		return fmt.Errorf("staging %s: %w", dir, err)
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return fmt.Errorf("emptying %s: %w", dir, err)
	}
	for _, n := range names {
		if err := rd.RemoveAll(n); err != nil {
			return fmt.Errorf("emptying %s: %w", dir, err)
		}
	}
	files := []struct {
		name string
		body []byte
	}{{".gitignore", []byte("*\n")}, {"task.md", task}}
	for _, f := range files {
		body := f.body
		if err := hostopen.PublishStreamExclusive(filepath.Join(dir, f.name), 0o644, func(w io.Writer) error {
			_, werr := w.Write(body)
			return werr
		}, nil); err != nil {
			return fmt.Errorf("staging %s: %w", filepath.Join(dir, f.name), err)
		}
	}
	return nil
}

// runOutcome is byre's verdict, before it is rendered.
type runOutcome struct {
	class     string
	exitCode  int
	agentExit *int
	message   string
}

func outcomeOf(class string, code int, agentExit *int, message string) runOutcome {
	return runOutcome{class: class, exitCode: code, agentExit: agentExit, message: message}
}

func intPtr(n int) *int { return &n }

// knownStatus is the box's raw status when the engine reported one.
func knownStatus(res developResult) *int {
	if res.ContainerStatus < 0 {
		return nil
	}
	return intPtr(res.ContainerStatus)
}

// chassisCodes are the statuses the headless contract owns: success and the
// chassis's 10-16 band.
func chassisCode(c int) bool { return c == 0 || (c >= 10 && c <= 16) }

// chassisMeaning is the contract's one-line reading of a chassis status.
var chassisMeaning = map[int]string{
	0:  "the agent ran and left an answer",
	10: "the agent reported failure",
	11: "the agent process failed",
	12: "--timeout expired",
	13: "no usable credential in the box",
	14: "the headless script failed, or exited 0 without a result",
	15: "the backend binary is absent, or no headless command is baked",
	16: "the result dir, the task hash, or the chassis files could not be set up",
}

// classifyRun reads develop's typed result into the outcome classes. The
// order is the contract: a refused attach first, then "did a box of this
// launch exist", then the code band.
func classifyRun(err error, res developResult, refusal string) runOutcome {
	if err == nil {
		agentExit := knownStatus(res)
		if agentExit == nil {
			agentExit = intPtr(0)
		}
		return outcomeOf(outcomeRan, 0, agentExit, chassisMeaning[0])
	}
	var ee ExitError
	if errors.As(err, &ee) {
		c := ee.Code
		switch {
		case c == ExitRefused && res.Phase == phaseAttached && res.Live:
			return outcomeOf(outcomeRefused, c, nil, "the attach failed with the box still running")
		case res.Phase < phaseCreated:
			msg := refusal
			if msg == "" {
				if c == ExitRefused {
					msg = "a session is already live for this worktree"
				} else {
					msg = fmt.Sprintf("byre declined to launch (exit %d)", c)
				}
			}
			return outcomeOf(outcomeRefused, c, nil, msg)
		case chassisCode(c):
			return outcomeOf(outcomeRan, c, intPtr(c), chassisMeaning[c])
		default:
			return outcomeOf(outcomeBoxExited, c, intPtr(c), fmt.Sprintf("the box exited %d, outside the headless contract (0, 10-16): it never reached the chassis, or what ran it died", c))
		}
	}
	var agentExit *int
	if res.Phase >= phaseCreated {
		agentExit = knownStatus(res)
	}
	return outcomeOf(outcomeByreFailed, 1, agentExit, strings.ReplaceAll(err.Error(), "\n", " "))
}

// runData is the --data document. Field order is the contract.
type runData struct {
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

// finish renders the outcome: the --data document on stdout, its in-tree
// copy for humans, and the summary line, always last on stderr.
func (st *runState) finish(out runOutcome) {
	st.finalized = true
	authority := authorityVerified
	if st.res.RawRunArgs {
		authority = authorityRawRunArgs
	}
	report := st.res.ExitReport
	if report == nil {
		report = []string{}
	}
	doc := runData{
		Version:       RunDataVersion,
		RunID:         st.runID,
		Outcome:       out.class,
		Authority:     authority,
		ExitCode:      out.exitCode,
		AgentExitCode: out.agentExit,
		Phase:         st.res.Phase.String(),
		Agent:         st.agent,
		LaunchRecord:  st.res.LaunchHash,
		StartedAt:     st.started.UTC().Format(time.RFC3339),
		FinishedAt:    runClock().UTC().Format(time.RFC3339),
		Message:       out.message,
		ExitReport:    report,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		dataf(st.s.Err, "byre: run: rendering the outcome: %v\n", err)
	} else {
		if st.o.Data {
			_, _ = st.s.Out.Write(buf.Bytes())
		}
		if st.staged {
			st.writeInTreeCopy(buf.Bytes())
		}
	}
	fmt.Fprintf(st.s.Err, "byre: run %s: %s, exit %d (%s)\n", st.runID, out.class, out.exitCode, authority)
}

// writeInTreeCopy publishes the document into the result dir for humans. The
// agent had the dir for the whole run, so a replaced dir or a name already
// standing there wins and the copy is skipped, said so: the authoritative
// outcome is the exit code and the --data document, never this file.
func (st *runState) writeInTreeCopy(doc []byte) {
	p := filepath.Join(st.workDir, runDirName, "outcome.json")
	if err := hostopen.PublishStreamExclusive(p, 0o644, func(w io.Writer) error {
		_, werr := w.Write(doc)
		return werr
	}, nil); err != nil {
		dataf(st.s.Err, "byre: run: outcome.json not written to the result dir (%v); the exit code and --data are the outcome\n", err)
	}
}

// stickyWriter keeps the box's stdout flowing whatever happens to byre's own:
// the first write error is held and every later write is dropped, so a
// closed reader never back-pressures the engine client into a hang.
type stickyWriter struct {
	w   io.Writer
	err error
}

func (s *stickyWriter) Write(p []byte) (int, error) {
	if s.err == nil {
		if _, err := s.w.Write(p); err != nil {
			s.err = err
		}
	}
	return len(p), nil
}

// maxEscapedLine bounds how much of one line the escaper holds before it
// writes the line out in pieces: the answer is agent text, and a line with no
// newline must not grow host memory without limit.
const maxEscapedLine = 64 << 10

// lineEscaper renders agent text for a terminal: each line through
// packages.EscapeTerminal, line breaks kept. A trailing partial line is
// escaped at flush.
type lineEscaper struct {
	w   io.Writer
	buf []byte
}

func (e *lineEscaper) Write(p []byte) (int, error) {
	e.buf = append(e.buf, p...)
	for {
		i := bytes.IndexByte(e.buf, '\n')
		if i < 0 {
			break
		}
		_, _ = io.WriteString(e.w, packages.EscapeTerminal(string(e.buf[:i]))+"\n")
		e.buf = append(e.buf[:0], e.buf[i+1:]...)
	}
	if len(e.buf) > maxEscapedLine {
		// Cut before a trailing incomplete rune so a multi-byte character is
		// not split across two escapes.
		cut := len(e.buf)
		for back := 1; back <= utf8.UTFMax && back <= len(e.buf); back++ {
			if utf8.RuneStart(e.buf[len(e.buf)-back]) {
				if !utf8.FullRune(e.buf[len(e.buf)-back:]) {
					cut = len(e.buf) - back
				}
				break
			}
		}
		_, _ = io.WriteString(e.w, packages.EscapeTerminal(string(e.buf[:cut])))
		e.buf = append(e.buf[:0], e.buf[cut:]...)
	}
	return len(p), nil
}

func (e *lineEscaper) flush() {
	if len(e.buf) > 0 {
		_, _ = io.WriteString(e.w, packages.EscapeTerminal(string(e.buf)))
		e.buf = e.buf[:0]
	}
}
