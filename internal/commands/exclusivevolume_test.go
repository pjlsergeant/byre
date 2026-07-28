package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

// exclusiveConfig is a project declaring one single-writer volume, plus the
// engine name that volume gets. Both come from the same declaration, so a test
// cannot assert against a name byre would not actually mount.
func exclusiveConfig(p project.Paths) (config.Config, string) {
	v := config.Volume{Name: "ledger", Role: "state", Target: "/var/lib/ledger", Sharing: "exclusive"}
	return config.Config{Volumes: []config.Volume{v}}, scopedVolumeName(p.ID, os.Getuid(), v)
}

// siblingHolding writes a launch record for another worktree that mounted the
// named engine volume, and returns the labels its container carries.
func siblingHolding(t *testing.T, p project.Paths, engineVolume string) map[string]string {
	t.Helper()
	rec := sampleLaunchRecord()
	rec.Workdir = "/home/pete/byre/wt"
	rec.Volumes = []launchVolume{{Name: engineVolume, Target: "/var/lib/ledger", Decl: "ledger", Role: "state", Scope: "project", Sharing: "exclusive"}}
	hash, err := writeLaunchRecord(p, rec)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{launchKey: hash, workdirKey: "byre-wt-abc123"}
}

// The headline: a live sibling holding the volume refuses the launch, in the
// session-already-live family, naming the volume and the worktree holding it
// -- and nothing is built or created.
func TestDevelopRefusesASiblingHoldingAnExclusiveVolume(t *testing.T) {
	p, _ := testPaths(t)
	cfg, vol := exclusiveConfig(p)
	f := &fakeRunner{
		live:       map[string][]string{projectLabel(p): {"sibling-box"}},
		labelsByID: map[string]map[string]string{"sibling-box": siblingHolding(t, p, vol)},
	}
	s, _, errBuf := testStreams("", false)

	err := develop(f, s, p, combine(cfg, skills.Resolved{}), false)

	var exit ExitError
	if !errors.As(err, &exit) || exit.Code != ExitRefused {
		t.Fatalf("err = %v, want ExitError{%d}", err, ExitRefused)
	}
	got := errBuf.String()
	for _, want := range []string{`volume "ledger" declares sharing = "exclusive"`, "byre-wt-abc123", "/var/lib/ledger", "stop the other session"} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal must name the rule, the volume and the holder; missing %q in:\n%s", want, got)
		}
	}
	if len(f.creates) != 0 || len(f.builds) != 0 {
		t.Errorf("a refusal must leave the store and the engine untouched: creates=%v builds=%v", f.creates, f.builds)
	}
}

// The gate: with no exclusive declaration the check does not run at all, so a
// sibling byre cannot describe is not a reason to refuse. Every config byre
// ships today is this case.
func TestExclusiveVolumeCheckIsSkippedWithoutADeclaration(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{
		live:       map[string][]string{projectLabel(p): {"sibling-box"}},
		labelsByID: map[string]map[string]string{"sibling-box": {workdirKey: "byre-wt-abc123"}}, // no launch record at all
	}
	s, _, _ := testStreams("", false)
	shared := config.Config{Volumes: []config.Volume{{Name: "ledger", Role: "state", Target: "/var/lib/ledger"}}}
	if err := develop(f, s, p, combine(shared, skills.Resolved{}), false); err != nil {
		t.Fatalf("a shared volume beside an unknowable sibling must launch: %v", err)
	}
}

// A sibling whose launch record does not mention the volume is not holding it,
// and the launch proceeds. Without this the rule would be "any sibling
// refuses", which is a different (and wrong) rule.
func TestExclusiveVolumeAllowsASiblingHoldingSomethingElse(t *testing.T) {
	p, _ := testPaths(t)
	cfg, _ := exclusiveConfig(p)
	other := siblingHolding(t, p, "byre-some-other-project-cache")
	f := &fakeRunner{
		live:       map[string][]string{projectLabel(p): {"sibling-box"}},
		labelsByID: map[string]map[string]string{"sibling-box": other},
	}
	s, _, _ := testStreams("", false)
	if err := develop(f, s, p, combine(cfg, skills.Resolved{}), false); err != nil {
		t.Fatalf("a sibling holding a different volume must not block the launch: %v", err)
	}
}

// This worktree's own box is not a sibling: the session-already-live report
// upstream says more useful things, and this rule must not shadow it.
func TestExclusiveVolumeIgnoresThisWorktreesOwnBox(t *testing.T) {
	p, _ := testPaths(t)
	cfg, vol := exclusiveConfig(p)
	mine := siblingHolding(t, p, vol)
	mine[workdirKey] = p.WorktreeID
	f := &fakeRunner{
		// Only the PROJECT label lists it: the own-worktree fast path queries
		// the workdir label, so this box reaches the exclusive check.
		live:       map[string][]string{projectLabel(p): {"my-box"}},
		labelsByID: map[string]map[string]string{"my-box": mine},
	}
	s, _, _ := testStreams("", false)
	if err := develop(f, s, p, combine(cfg, skills.Resolved{}), false); err != nil {
		t.Fatalf("this worktree's own box must not refuse itself over its own volume: %v", err)
	}
}

// Every uncertainty refuses, because the two outcomes are not symmetric: a
// wrong refusal costs a command, a wrong launch costs the volume's contents.
// Each arm must name WHICH uncertainty fired -- the remedies differ.
func TestExclusiveVolumeRefusesWhatItCannotProve(t *testing.T) {
	cases := map[string]struct {
		// build returns the configured engine, its peers, and the engines
		// byre found but will not run.
		build func(t *testing.T, p project.Paths) (*fakeRunner, []sessionRunner, []declinedEngine)
		want  string
	}{
		"sibling with no launch record": {
			build: func(t *testing.T, p project.Paths) (*fakeRunner, []sessionRunner, []declinedEngine) {
				return &fakeRunner{
					live:       map[string][]string{projectLabel(p): {"old-box"}},
					labelsByID: map[string]map[string]string{"old-box": {workdirKey: "byre-wt-abc123"}},
				}, nil, nil
			},
			want: "carries no launch record",
		},
		"sibling whose record is gone from the store": {
			build: func(t *testing.T, p project.Paths) (*fakeRunner, []sessionRunner, []declinedEngine) {
				return &fakeRunner{
					live:       map[string][]string{projectLabel(p): {"box"}},
					labelsByID: map[string]map[string]string{"box": {workdirKey: "byre-wt-abc123", launchKey: strings.Repeat("a", 64)}},
				}, nil, nil
			},
			want: "no longer in the store",
		},
		// The adversarial arm: under --self-edit the box owns the store, so
		// "byre cannot read it" must never resolve to "it is not holding the
		// volume" -- that would be a second writer for the asking.
		"sibling whose record does not match its address": {
			build: func(t *testing.T, p project.Paths) (*fakeRunner, []sessionRunner, []declinedEngine) {
				_, vol := exclusiveConfig(p)
				labels := siblingHolding(t, p, vol)
				path := filepath.Join(launchesDir(p), labels[launchKey]+".toml")
				if err := hostopen.PublishFile(path, "record = 1\n", 0o600); err != nil {
					t.Fatal(err)
				}
				return &fakeRunner{
					live:       map[string][]string{projectLabel(p): {"box"}},
					labelsByID: map[string]map[string]string{"box": labels},
				}, nil, nil
			},
			want: "does not match its own address",
		},
		"peer engine that cannot be listed": {
			build: func(t *testing.T, p project.Paths) (*fakeRunner, []sessionRunner, []declinedEngine) {
				peer := &fakeRunner{engine: runner.Podman, liveErr: errors.New("Cannot connect to the Podman socket")}
				return &fakeRunner{}, []sessionRunner{peer}, nil
			},
			want: "could not list this project's boxes on podman",
		},
		"sibling whose labels cannot be read": {
			build: func(t *testing.T, p project.Paths) (*fakeRunner, []sessionRunner, []declinedEngine) {
				return &fakeRunner{
					live:      map[string][]string{projectLabel(p): {"box"}},
					labelsErr: errors.New("no such object"),
				}, nil, nil
			},
			want: "could not read the labels",
		},
		"engine byre will not run": {
			build: func(t *testing.T, p project.Paths) (*fakeRunner, []sessionRunner, []declinedEngine) {
				return &fakeRunner{}, nil, []declinedEngine{{Engine: "podman", Err: errors.New("resolved out of a box-writable directory")}}
			},
			want: "byre will not run podman",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p, _ := testPaths(t)
			f, peers, declined := tc.build(t, p)
			s, _, errBuf := testStreams("", false)
			cfg, _ := exclusiveConfig(p)
			rv := combine(cfg, skills.Resolved{})
			rv.otherEngines = peers
			rv.declinedEngines = declined

			err := develop(f, s, p, rv, false)

			var exit ExitError
			if !errors.As(err, &exit) || exit.Code != ExitRefused {
				t.Fatalf("err = %v, want ExitError{%d}", err, ExitRefused)
			}
			got := errBuf.String()
			if !strings.Contains(got, `sharing = "exclusive"`) || !strings.Contains(got, tc.want) {
				t.Errorf("refusal must name the rule AND the uncertainty %q, got:\n%s", tc.want, got)
			}
			if len(f.creates) != 0 {
				t.Errorf("a refusal must create nothing: %v", f.creates)
			}
		})
	}
}

// The peer engines are consulted, not assumed: a sibling worktree may
// legitimately run on the other engine (ADR 0004 stops two boxes for one
// WORKTREE, never for one project), and the volume it holds is the same one.
func TestExclusiveVolumeSeesAHolderOnAnotherEngine(t *testing.T) {
	p, _ := testPaths(t)
	cfg, vol := exclusiveConfig(p)
	docker := &fakeRunner{}
	podman := &fakeRunner{
		engine:     runner.Podman,
		live:       map[string][]string{projectLabel(p): {"wt-box"}},
		labelsByID: map[string]map[string]string{"wt-box": siblingHolding(t, p, vol)},
	}
	s, _, errBuf := testStreams("", false)
	rv := combine(cfg, skills.Resolved{})
	rv.otherEngines = []sessionRunner{podman}

	err := develop(docker, s, p, rv, false)

	var exit ExitError
	if !errors.As(err, &exit) || exit.Code != ExitRefused {
		t.Fatalf("err = %v, want ExitError{%d}", err, ExitRefused)
	}
	if got := errBuf.String(); !strings.Contains(got, "podman stop wt-box") {
		t.Errorf("the refusal must name the engine holding the box: %s", got)
	}
	// The docker-only view sees nothing -- which is what makes the peer set
	// load-bearing rather than decorative.
	rv.otherEngines = nil
	s2, _, _ := testStreams("", false)
	if err := develop(docker, s2, p, rv, false); err != nil {
		t.Fatalf("test is not exercising the peer set: %v", err)
	}
}

// Where byre could not LOOK, it cannot say which of the declared volumes is
// at risk -- naming one would imply it had checked, so the uncertainty arms
// name them all.
func TestExclusiveVolumeUncertaintyNamesEveryDeclaration(t *testing.T) {
	p, _ := testPaths(t)
	cfg := config.Config{Volumes: []config.Volume{
		{Name: "ledger", Role: "state", Target: "/var/lib/ledger", Sharing: "exclusive"},
		{Name: "index", Role: "state", Target: "/var/lib/index", Sharing: "exclusive"},
	}}
	rv := combine(cfg, skills.Resolved{})
	rv.declinedEngines = []declinedEngine{{Engine: "podman", Err: errors.New("resolved out of a box-writable directory")}}
	s, _, errBuf := testStreams("", false)

	if err := develop(&fakeRunner{}, s, p, rv, false); err == nil {
		t.Fatal("want a refusal")
	}
	got := errBuf.String()
	if !strings.Contains(got, `volumes "index", "ledger" declare`) {
		t.Errorf("both declarations must be named, sorted:\n%s", got)
	}
}
