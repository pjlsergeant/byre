package commands

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

const (
	unreachableErr = "Cannot connect to the Docker daemon at unix:///var/run/docker.sock"
	hardErr        = "dial unix: connect: permission denied"
	testUID        = 1000
)

// ownEnv is the dev identity of a box the test caller (testUID) owns.
var ownEnv = map[string]string{"BYRE_UID": "1000", "BYRE_GID": "1000"}

// statusPaths is a project (or a worktree of one) whose engine record names
// podman -- a record that says nothing about docker, so a box found there is
// found by asking, not by the record.
func statusPaths(t *testing.T, worktree bool) project.Paths {
	t.Helper()
	p, _ := testPaths(t)
	if worktree {
		p.IsWorktree = true
		p.WorkDir = t.TempDir()
		p.WorktreeID = p.ID + "-wt1"
	}
	if err := os.WriteFile(engineRecordPath(p), []byte("podman\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func containerRow(t *testing.T, info statusInfo) string {
	t.Helper()
	var b strings.Builder
	renderStatusTest(&b, info)
	return strings.Join(statusRows(b.String())["Container"], " ")
}

func worktreesRows(t *testing.T, info statusInfo) string {
	t.Helper()
	var b strings.Builder
	renderStatusTest(&b, info)
	return strings.Join(statusRows(b.String())["Worktrees"], " ")
}

// The field-QA bug (2026-10-03): a box running under docker, the config then
// flipped to podman. shell and deliver walked straight into the box while
// status said "not running", because status asked only the configured
// engine. ADR 0004: status must always be able to find the session -- on the
// engine it actually runs on, with its launch record as the page's subject,
// whatever the engine record says.
func TestStatusFindsTheBoxOnTheEngineItLaunchedUnder(t *testing.T) {
	for _, worktree := range []bool{true, false} {
		name := "main tree"
		if worktree {
			name = "worktree"
		}
		t.Run(name, func(t *testing.T) {
			p := statusPaths(t, worktree)
			rec := sampleLaunchRecord()
			rec.Engine = string(runner.Docker)
			hash, err := writeLaunchRecord(p, rec)
			if err != nil {
				t.Fatal(err)
			}
			podman := &fakeRunner{engine: runner.Podman}
			docker := &fakeRunner{
				engine: runner.Docker,
				env:    ownEnv, live: map[string][]string{workdirLabel(p): {"dockerbox0123"}, projectLabel(p): {"dockerbox0123"}},
				labelsByID: map[string]map[string]string{"dockerbox0123": {launchKey: hash, workdirKey: p.WorktreeID}},
			}
			info := statusInfo{Engine: "podman", Canonical: p.WorkDir}
			statusSession(discardStreams().Err, &info, p, podman, runner.Engine(info.Engine), false, []sessionRunner{docker}, nil, testUID)

			if info.Container != "dockerbox0123" || info.SessionEngine != "docker" {
				t.Fatalf("the docker box must be found: container=%q engine=%q", info.Container, info.SessionEngine)
			}
			if info.LaunchState != launchRecordOK || info.Launch == nil || info.Launch.Engine != "docker" {
				t.Fatalf("the box's own launch record must be the subject: state=%v launch=%+v", info.LaunchState, info.Launch)
			}
			if len(info.SiblingSessions) != 0 {
				t.Errorf("the box must not be listed as its own sibling: %v", info.SiblingSessions)
			}
			if got := containerRow(t, info); !strings.Contains(got, "running (") {
				t.Errorf("Container row must report the running box, got %q", got)
			}
		})
	}
}

// The plain case is unchanged: a box on the configured engine is running.
// The one extra query per status (asking the other installed engine) is the
// accepted cost of never missing a box.
func TestStatusPlainCaseStillReportsRunning(t *testing.T) {
	p := statusPaths(t, false)
	docker := &fakeRunner{env: ownEnv, live: map[string][]string{workdirLabel(p): {"dockerbox0123"}}}
	podman := &fakeRunner{engine: runner.Podman}
	info := statusInfo{Engine: "docker"}
	statusSession(discardStreams().Err, &info, p, docker, runner.Engine(info.Engine), false, []sessionRunner{podman}, nil, testUID)
	if info.Container != "dockerbox0123" || info.ContainerQueryErr != "" || info.SiblingQueryErr != "" {
		t.Fatalf("plain case: %+v", info)
	}
}

// Item 1, implicated: an unreachable engine this worktree's engine record
// names (last or unresolved) may hold its box, so it is disclosed and, with
// no box found, the state is unknown on the page AND in --data, never
// "not running"/"stopped".
func TestStatusImplicatedUnreachableEngineIsUnknown(t *testing.T) {
	for _, record := range []string{"docker\n", "podman unresolved=docker\n"} {
		p := statusPaths(t, true)
		if err := os.WriteFile(engineRecordPath(p), []byte(record), 0o600); err != nil {
			t.Fatal(err)
		}
		docker := &fakeRunner{engine: runner.Docker, liveErr: errors.New(unreachableErr)}
		s, _, stderr := testStreams("", false)
		info := statusInfo{Engine: "podman"}
		statusSession(s.Err, &info, p, &fakeRunner{engine: runner.Podman}, runner.Engine(info.Engine), false, []sessionRunner{docker}, nil, testUID)
		if got := stderr.String(); !strings.Contains(got, "docker isn't reachable") {
			t.Errorf("record %q: the implicated engine must be disclosed: %q", record, got)
		}
		if got := containerRow(t, info); !strings.Contains(got, "unknown") || strings.Contains(got, "not running") {
			t.Errorf("record %q: Container row must be unknown, got %q", record, got)
		}
		if st := statusDataContainerOf(info).State; st != "unknown" {
			t.Errorf("record %q: --data state = %q, want unknown", record, st)
		}
	}
}

// Item 1, steady state (ADR 0004's ambient-noise ruling): an unreachable
// engine the record does not name -- the installed-but-stopped podman beside
// docker on a Mac, or any engine when there is no record -- is skipped
// silently. No note, "not running", --data "stopped", and siblings are not
// marked unknown on its account, with or without a box running.
func TestStatusUnimplicatedUnreachableEngineIsQuiet(t *testing.T) {
	for _, record := range []bool{true, false} {
		p := statusPaths(t, true)
		if record {
			if err := os.WriteFile(engineRecordPath(p), []byte("docker\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Remove(engineRecordPath(p)); err != nil {
			t.Fatal(err)
		}
		podman := &fakeRunner{engine: runner.Podman, liveErr: errors.New("Cannot connect to Podman. Please verify your connection")}
		s, _, stderr := testStreams("", false)
		info := statusInfo{Engine: "docker"}
		statusSession(s.Err, &info, p, &fakeRunner{}, runner.Engine(info.Engine), false, []sessionRunner{podman}, nil, testUID)
		if got := stderr.String(); got != "" {
			t.Errorf("record=%v: no note for an unimplicated stopped engine: %q", record, got)
		}
		if got := containerRow(t, info); got != "not running" {
			t.Errorf("record=%v: Container row = %q, want not running", record, got)
		}
		if st := statusDataContainerOf(info).State; st != "stopped" {
			t.Errorf("record=%v: --data state = %q, want stopped", record, st)
		}
		if info.SiblingQueryErr != "" {
			t.Errorf("record=%v: siblings must not go unknown: %q", record, info.SiblingQueryErr)
		}

		// A running docker box with a sibling: still no sibling caveat.
		docker := &fakeRunner{
			env: ownEnv, live: map[string][]string{workdirLabel(p): {"dockerbox0123"}, projectLabel(p): {"dockerbox0123", "otherbox4567"}},
			labelsByID: map[string]map[string]string{"otherbox4567": {workdirKey: p.ID}},
		}
		running := statusInfo{Engine: "docker"}
		statusSession(discardStreams().Err, &running, p, docker, runner.Engine(running.Engine), false, []sessionRunner{podman}, nil, testUID)
		if running.Container != "dockerbox0123" || running.SiblingQueryErr != "" {
			t.Errorf("record=%v: running box: container=%q siblingErr=%q", record, running.Container, running.SiblingQueryErr)
		}
		if got := worktreesRows(t, running); strings.Contains(got, "unknown") || strings.Contains(got, "more may be live") {
			t.Errorf("record=%v: Worktrees row must not be qualified: %q", record, got)
		}
	}
}

// Item 2: siblings are gathered from every engine that answered -- a sibling
// left on the old engine after THIS worktree moved stays visible -- and a box
// found on one engine does not clear another engine's failure: a partial
// list and the failure are both on the page.
func TestStatusSiblingsComeFromEveryEngine(t *testing.T) {
	t.Run("a sibling left on the old engine", func(t *testing.T) {
		p := statusPaths(t, true)
		podman := &fakeRunner{engine: runner.Podman, env: ownEnv, live: map[string][]string{
			workdirLabel(p): {"podmanbox0123"}, projectLabel(p): {"podmanbox0123"}}}
		docker := &fakeRunner{
			engine: runner.Docker,
			env:    ownEnv, live: map[string][]string{projectLabel(p): {"mainbox456789"}},
			labelsByID: map[string]map[string]string{"mainbox456789": {workdirKey: p.ID}},
		}
		info := statusInfo{Engine: "podman"}
		statusSession(discardStreams().Err, &info, p, podman, runner.Engine(info.Engine), false, []sessionRunner{docker}, nil, testUID)
		if info.Container != "podmanbox0123" {
			t.Fatalf("own box not found: %q", info.Container)
		}
		if len(info.SiblingSessions) != 1 || !strings.Contains(info.SiblingSessions[0], p.ID) {
			t.Errorf("the sibling on docker must be listed: %v", info.SiblingSessions)
		}
		// Tagged with its engine, and outside the shared-volumes claim: a
		// box on docker mounts docker's volumes, not these.
		got := worktreesRows(t, info)
		if !strings.Contains(got, p.ID+" (mainbox45678, on docker)") || strings.Contains(got, "share these volumes") {
			t.Errorf("Worktrees row must tag the docker sibling and make no sharing claim for it: %q", got)
		}
		if d := statusDataContainerOf(info); len(d.Siblings) != 1 || !strings.Contains(d.Siblings[0], "on docker") {
			t.Errorf("--data siblings carry the same tagged name: %+v", d.Siblings)
		}
	})
	t.Run("mixed engines narrow the sharing claim", func(t *testing.T) {
		p := statusPaths(t, true)
		podman := &fakeRunner{engine: runner.Podman, env: ownEnv, live: map[string][]string{
			workdirLabel(p): {"podmanbox0123"}, projectLabel(p): {"podmanbox0123", "samebox456789"}},
			labelsByID: map[string]map[string]string{"samebox456789": {workdirKey: "wt-same"}}}
		docker := &fakeRunner{
			live:       map[string][]string{projectLabel(p): {"mainbox456789"}},
			labelsByID: map[string]map[string]string{"mainbox456789": {workdirKey: p.ID}},
		}
		info := statusInfo{Engine: "podman"}
		statusSession(discardStreams().Err, &info, p, podman, runner.Podman, false, []sessionRunner{docker}, nil, testUID)
		got := worktreesRows(t, info)
		if !strings.Contains(got, "wt-same (samebox45678)") || !strings.Contains(got, "except those on another engine") {
			t.Errorf("same-engine sibling untagged, claim narrowed: %q", got)
		}
	})
	t.Run("same engine keeps the row byte-identical", func(t *testing.T) {
		info := statusInfo{Engine: "docker", Canonical: "/p", Container: "abcdef0123456789",
			SiblingSessions: []string{"proj-wt1 (beef0123)"}}
		if got := worktreesRows(t, info); got != "1 other session(s) live: proj-wt1 (beef0123)  (share these volumes)" {
			t.Errorf("same-engine row changed: %q", got)
		}
	})
	t.Run("a found box keeps another engine's failure", func(t *testing.T) {
		p := statusPaths(t, true)
		podman := &fakeRunner{engine: runner.Podman, env: ownEnv, live: map[string][]string{
			workdirLabel(p): {"podmanbox0123"}, projectLabel(p): {"podmanbox0123", "otherbox4567"}}}
		docker := &fakeRunner{engine: runner.Docker, liveErr: errors.New(hardErr)}
		info := statusInfo{Engine: "podman"}
		statusSession(discardStreams().Err, &info, p, podman, runner.Engine(info.Engine), false, []sessionRunner{docker}, nil, testUID)
		if info.Container != "podmanbox0123" {
			t.Fatalf("own box not found: %q", info.Container)
		}
		if !strings.Contains(info.SiblingQueryErr, "docker") || !strings.Contains(info.SiblingQueryErr, "permission denied") {
			t.Fatalf("docker's failure must reach the sibling side: %q", info.SiblingQueryErr)
		}
		got := worktreesRows(t, info)
		if !strings.Contains(got, "otherbox") || !strings.Contains(got, "permission denied") {
			t.Errorf("the page must show the partial list AND the failure, got %q", got)
		}
		d := statusDataContainerOf(info)
		if len(d.Siblings) != 1 || d.SiblingsError == "" {
			t.Errorf("--data must carry both: %+v", d)
		}
	})
}

// One worktree's session on two engines at once is named, not silently
// reduced to one -- and the one described is the one `byre shell` enters:
// shell walks docker before podman (deliver's name sort agrees), whatever
// the configured engine is.
func TestStatusNamesASessionOnTwoEngines(t *testing.T) {
	p := statusPaths(t, true)
	podman := &fakeRunner{engine: runner.Podman, env: ownEnv, live: map[string][]string{workdirLabel(p): {"podmanbox0123"}}}
	docker := &fakeRunner{engine: runner.Docker, env: ownEnv, live: map[string][]string{workdirLabel(p): {"dockerbox0123"}}}
	s, _, stderr := testStreams("", false)
	info := statusInfo{Engine: "podman"}
	statusSession(s.Err, &info, p, podman, runner.Engine(info.Engine), false, []sessionRunner{docker}, nil, testUID)
	if info.SessionEngine != "docker" || info.Container != "dockerbox0123" {
		t.Errorf("the page must describe the box shell enters (docker): engine=%q container=%q", info.SessionEngine, info.Container)
	}
	got := stderr.String()
	if !strings.Contains(got, "both docker") || !strings.Contains(got, "podmanbox012") || !strings.Contains(got, "describe the docker one") {
		t.Errorf("both engines must be named, with the described one: %q", got)
	}
}

// The box described is the caller's, by the predicate shell and deliver
// share (deliver.JudgeBox): another user's box on a shared rootful daemon is
// not this session, and is counted rather than described; a rootless podman
// keep-id box carrying the generic in-container uid IS the caller's.
func TestStatusJudgesOwnershipAsShellDoes(t *testing.T) {
	p := statusPaths(t, true)
	if err := os.Remove(engineRecordPath(p)); err != nil {
		t.Fatal(err)
	}
	foreign := map[string]string{"BYRE_UID": "1001", "BYRE_GID": "1001"}
	docker := &fakeRunner{env: foreign, live: map[string][]string{workdirLabel(p): {"theirbox0123"}}}
	s, _, stderr := testStreams("", false)
	info := statusInfo{Engine: "docker"}
	statusSession(s.Err, &info, p, docker, runner.Docker, false, nil, nil, testUID)
	if info.Container != "" || containerRow(t, info) != "not running" {
		t.Errorf("another user's box is not this session: %+v", info)
	}
	if got := stderr.String(); !strings.Contains(got, "another user's identity") {
		t.Errorf("the hidden box must be counted: %q", got)
	}
	if boxRunningOn(p, docker, runner.Docker, nil, nil, testUID) {
		t.Error("the editor probe must not treat another user's box as running")
	}

	generic := map[string]string{"BYRE_UID": "1000", "BYRE_GID": "1000"}
	podman := &fakeRunner{engine: runner.Podman, rootless: true, env: generic, live: map[string][]string{workdirLabel(p): {"keepidbox0123"}}}
	info = statusInfo{Engine: "podman"}
	statusSession(discardStreams().Err, &info, p, podman, runner.Podman, false, nil, nil, 501)
	if info.Container != "keepidbox0123" {
		t.Errorf("a rootless podman box is the caller's whatever BYRE_UID says: %+v", info)
	}
}

// A box byre could hide but not settle is not "not running": hidden while
// the rootless probe failed (shell's undetermined-mode case -- on rootless
// podman the uid check does not apply, so it is likely the caller's keep-id
// box), or carrying no readable identity (shell refuses to enter it as a
// running session). That engine has not answered "not here": unknown on the
// page and in --data, the editor probe false, and the note keeps shell's
// uncertain wording rather than "another user's identity".
func TestStatusUnsettledBoxIsUnknown(t *testing.T) {
	cases := []struct {
		name   string
		podman *fakeRunner
		note   string
	}{
		{"undetermined rootless mode", &fakeRunner{engine: runner.Podman, rootlessErr: errors.New("info query boom"),
			env: map[string]string{"BYRE_UID": "1000", "BYRE_GID": "1000"}}, "couldn't be determined"},
		{"unreadable identity", &fakeRunner{engine: runner.Podman, env: map[string]string{"BYRE_UID": "nope"}}, "no readable dev identity"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := statusPaths(t, true)
			c.podman.live = map[string][]string{workdirLabel(p): {"keepidbox0123"}}
			s, _, stderr := testStreams("", false)
			info := statusInfo{Engine: "podman"}
			statusSession(s.Err, &info, p, c.podman, runner.Podman, false, nil, nil, 501)
			if info.Container != "" || !strings.Contains(containerRow(t, info), "unknown") {
				t.Errorf("Container row must be unknown: %q", containerRow(t, info))
			}
			if st := statusDataContainerOf(info).State; st != "unknown" {
				t.Errorf("--data state = %q, want unknown", st)
			}
			got := stderr.String()
			if !strings.Contains(got, c.note) || strings.Contains(got, "another user's identity") {
				t.Errorf("note wrong: %q", got)
			}
			if boxRunningOn(p, c.podman, runner.Podman, nil, nil, 501) {
				t.Error("the editor probe degrades to false")
			}
		})
	}
}

// An unsettled box makes THIS worktree's session unknown, but its engine
// still answered the project-wide query: the sibling list from it is
// complete and must not be qualified as partial.
func TestStatusUnsettledBoxDoesNotQualifySiblings(t *testing.T) {
	unsettled := func(p project.Paths, fam []string, labels map[string]map[string]string) *fakeRunner {
		return &fakeRunner{engine: runner.Podman, rootlessErr: errors.New("info query boom"),
			env:        map[string]string{"BYRE_UID": "1000", "BYRE_GID": "1000"},
			live:       map[string][]string{workdirLabel(p): {"keepidbox0123"}, projectLabel(p): fam},
			labelsByID: labels}
	}
	t.Run("caller box on one engine, unsettled box on the other", func(t *testing.T) {
		p := statusPaths(t, true)
		docker := &fakeRunner{env: map[string]string{"BYRE_UID": "501", "BYRE_GID": "20"}, live: map[string][]string{workdirLabel(p): {"dockerbox0123"}, projectLabel(p): {"dockerbox0123"}}}
		podman := unsettled(p, []string{"keepidbox0123"}, nil)
		info := statusInfo{Engine: "docker"}
		statusSession(discardStreams().Err, &info, p, docker, runner.Docker, false, []sessionRunner{podman}, nil, 501)
		if info.Container != "dockerbox0123" || info.SiblingQueryErr != "" {
			t.Fatalf("container=%q siblingErr=%q", info.Container, info.SiblingQueryErr)
		}
		if got := worktreesRows(t, info); got != "" {
			t.Errorf("no Worktrees row when no sibling runs: %q", got)
		}
	})
	t.Run("one engine, unsettled box and a real sibling", func(t *testing.T) {
		p := statusPaths(t, true)
		podman := unsettled(p, []string{"keepidbox0123", "otherbox4567"},
			map[string]map[string]string{"otherbox4567": {workdirKey: p.ID}})
		info := statusInfo{Engine: "podman"}
		statusSession(discardStreams().Err, &info, p, podman, runner.Podman, false, nil, nil, 501)
		if !strings.Contains(containerRow(t, info), "unknown") {
			t.Errorf("Container row must be unknown: %q", containerRow(t, info))
		}
		got := worktreesRows(t, info)
		if !strings.Contains(got, p.ID+" (otherbox4567)") || strings.Contains(got, "unknown") || strings.Contains(got, "more may be live") {
			t.Errorf("the sibling is listed, unqualified: %q", got)
		}
	})
}

// The editor's live-box probe rides the same lookup: a box on the engine the
// config no longer names still means "changes apply at next launch".
func TestBoxRunningForEditLooksAcrossEngines(t *testing.T) {
	p := statusPaths(t, true)
	docker := &fakeRunner{env: ownEnv, live: map[string][]string{workdirLabel(p): {"dockerbox0123"}}}
	if !boxRunningOn(p, &fakeRunner{engine: runner.Podman}, runner.Podman, []sessionRunner{docker}, nil, testUID) {
		t.Error("a box on the non-configured engine is running")
	}
	if boxRunningOn(p, &fakeRunner{engine: runner.Podman}, runner.Podman, []sessionRunner{&fakeRunner{}}, nil, testUID) {
		t.Error("nothing running anywhere is not running")
	}
}

// The boundary of the absent-configured-engine rule: "not running" needs an
// engine that answered. With the configured engine not installed and no
// other engine to ask, nothing was asked, so the row stays unknown.
func TestStatusAbsentConfiguredEngineAndNothingAsked(t *testing.T) {
	p := statusPaths(t, false)
	if err := os.Remove(engineRecordPath(p)); err != nil {
		t.Fatal(err)
	}
	info := statusInfo{Engine: "podman", EngineErr: "podman: not installed"}
	statusSession(discardStreams().Err, &info, p, nil, runner.Podman, true, nil, nil, testUID)
	if got := containerRow(t, info); got != "unknown (no engine)" {
		t.Errorf("Container row = %q, want unknown (no engine)", got)
	}
}

// Item 4: a failure from an engine other than the configured one carries the
// engine's name; the configured engine's own failure is already pinned to the
// Engine row and stays bare.
func TestStatusNamesTheEngineThatDidNotAnswer(t *testing.T) {
	p := statusPaths(t, true)
	info := statusInfo{Engine: "podman"}
	statusSession(discardStreams().Err, &info, p, &fakeRunner{engine: runner.Podman}, runner.Engine(info.Engine), false,
		[]sessionRunner{&fakeRunner{engine: runner.Docker, liveErr: errors.New(hardErr)}}, nil, testUID)
	if !strings.HasPrefix(info.ContainerQueryErr, "docker: ") {
		t.Errorf("another engine's failure must be named: %q", info.ContainerQueryErr)
	}
	info = statusInfo{Engine: "podman"}
	statusSession(discardStreams().Err, &info, p, &fakeRunner{engine: runner.Podman, liveErr: errors.New(hardErr)}, runner.Engine(info.Engine), false,
		[]sessionRunner{&fakeRunner{engine: runner.Docker}}, nil, testUID)
	if info.ContainerQueryErr != hardErr {
		t.Errorf("the configured engine's failure stays bare: %q", info.ContainerQueryErr)
	}
}

// Item 5, the precedence when the configured engine is unresolved: a box
// found elsewhere is running; another engine's hard error is the error shown;
// an engine that is simply not installed beside engines that all answered
// empty is not running; and a configured engine byre REFUSED (it may hold a
// box byre won't look at) stays unknown.
func TestStatusUnresolvedConfiguredEnginePrecedence(t *testing.T) {
	p := statusPaths(t, false)
	// No record: a record naming the absent engine is item 3's case
	// (TestStatusNamedEngineNoLongerInstalled), not this precedence.
	if err := os.Remove(engineRecordPath(p)); err != nil {
		t.Fatal(err)
	}
	run := func(absent bool, docker *fakeRunner) statusInfo {
		info := statusInfo{Engine: "podman", EngineErr: "podman: not installed"}
		statusSession(discardStreams().Err, &info, p, nil, runner.Engine(info.Engine), absent, []sessionRunner{docker}, nil, testUID)
		return info
	}

	found := run(true, &fakeRunner{env: ownEnv, live: map[string][]string{workdirLabel(p): {"dockerbox0123"}}})
	if got := containerRow(t, found); !strings.Contains(got, "running (") {
		t.Errorf("found box: %q", got)
	}
	if st := statusDataContainerOf(found).State; st != "running" {
		t.Errorf("found box --data: %q", st)
	}

	failed := run(true, &fakeRunner{liveErr: errors.New(hardErr)})
	if got := containerRow(t, failed); !strings.Contains(got, "docker: "+hardErr) {
		t.Errorf("docker's hard error must be the one shown: %q", got)
	}
	if d := statusDataContainerOf(failed); d.State != "unknown" || !strings.Contains(d.Error, "docker") {
		t.Errorf("--data must carry docker's error: %+v", d)
	}

	empty := run(true, &fakeRunner{})
	if got := containerRow(t, empty); got != "not running" {
		t.Errorf("an absent configured engine beside an empty answer is not running: %q", got)
	}
	if st := statusDataContainerOf(empty).State; st != "stopped" {
		t.Errorf("--data: %q", st)
	}

	refused := run(false, &fakeRunner{})
	if got := containerRow(t, refused); !strings.Contains(got, "unknown") {
		t.Errorf("a refused configured engine may hold the box: %q", got)
	}
}

// Item 6: a declined other engine is disclosed by the shared declined note
// (never as "no longer installed" -- that is develop's record wording) and
// leaves the state unknown, since byre could not look there.
func TestStatusDeclinedEngineIsDisclosedAndUnknown(t *testing.T) {
	p := statusPaths(t, false)
	if err := os.WriteFile(engineRecordPath(p), []byte("docker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _, stderr := testStreams("", false)
	info := statusInfo{Engine: "podman"}
	declined := []declinedEngine{{Engine: "docker", Err: errors.New("resolved inside the project tree")}}
	statusSession(s.Err, &info, p, &fakeRunner{engine: runner.Podman}, runner.Engine(info.Engine), false, nil, declined, testUID)
	got := stderr.String()
	if !strings.Contains(got, "byre can't look there") || strings.Contains(got, "no longer installed") {
		t.Errorf("declined disclosure wrong: %q", got)
	}
	if !strings.Contains(info.ContainerQueryErr, "docker") {
		t.Errorf("a declined engine leaves the state unknown, named: %q", info.ContainerQueryErr)
	}
}

// The orphan row's stop command names the engine the box is ON: telling the
// user to `podman stop` a docker box is a command that cannot work.
func TestRenderStatusOrphanStopNamesTheSessionEngine(t *testing.T) {
	got := containerRow(t, statusInfo{Engine: "podman", SessionEngine: "docker", Canonical: "/p",
		Container: "deadbeefcafe4567", Orphaned: true})
	if !strings.Contains(got, "docker stop deadbeefcafe") {
		t.Errorf("orphan stop must name the session's engine, got %q", got)
	}
}

// Round-2 items 1 and 2 through the real path: the full declined set, the
// configured engine's refusal included, goes into statusSession. That
// refusal stays out of the stderr note (the Engine row states it) but is a
// failure: the sibling list gathered from docker is qualified as partial,
// the Container row falls to "unknown (no engine)", and --data carries the
// siblings and their error on that branch too.
func TestStatusConfiguredRefusalQualifiesSiblings(t *testing.T) {
	p := statusPaths(t, true)
	docker := &fakeRunner{
		env: ownEnv, live: map[string][]string{projectLabel(p): {"otherbox4567"}},
		labelsByID: map[string]map[string]string{"otherbox4567": {workdirKey: p.ID}},
	}
	declined := []declinedEngine{{Engine: "podman", Err: errors.New("podman resolved inside the project tree")}}
	s, _, stderr := testStreams("", false)
	info := statusInfo{Engine: "podman", EngineErr: "podman resolved inside the project tree"}
	statusSession(s.Err, &info, p, nil, runner.Podman, false, []sessionRunner{docker}, declined, testUID)

	if got := stderr.String(); strings.Contains(got, "podman resolved") {
		t.Errorf("the configured engine's refusal belongs to the Engine row, not stderr: %q", got)
	}
	if !strings.Contains(info.SiblingQueryErr, "podman resolved") {
		t.Fatalf("the refusal must qualify the sibling list: %q", info.SiblingQueryErr)
	}
	if got := containerRow(t, info); got != "unknown (no engine)" {
		t.Errorf("Container row = %q", got)
	}
	if got := worktreesRows(t, info); !strings.Contains(got, "otherbox") || !strings.Contains(got, "more may be live") {
		t.Errorf("Worktrees must show the partial list and the qualifier: %q", got)
	}
	d := statusDataContainerOf(info)
	if d.State != "unknown" || len(d.Siblings) != 1 || d.SiblingsError == "" {
		t.Errorf("--data must carry siblings and their error on the engine-error branch: %+v", d)
	}
}

// Round-2 item 3: an engine the record names that is neither installed nor
// declined cannot be asked, which is the named-unreachable case by another
// route -- one note, and unknown rather than "not running".
func TestStatusNamedEngineNoLongerInstalled(t *testing.T) {
	p := statusPaths(t, true)
	if err := os.WriteFile(engineRecordPath(p), []byte("docker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _, stderr := testStreams("", false)
	info := statusInfo{Engine: "podman"}
	statusSession(s.Err, &info, p, &fakeRunner{engine: runner.Podman}, runner.Podman, false, nil, nil, testUID)
	if got := stderr.String(); strings.Count(got, "docker is no longer installed") != 1 {
		t.Errorf("one note naming docker: %q", got)
	}
	if got := containerRow(t, info); !strings.Contains(got, "unknown") || !strings.Contains(got, "docker") {
		t.Errorf("Container row must be unknown, naming docker: %q", got)
	}
	if st := statusDataContainerOf(info).State; st != "unknown" {
		t.Errorf("--data state = %q", st)
	}
}
