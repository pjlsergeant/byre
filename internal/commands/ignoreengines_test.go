package commands

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

// The totals commands (backup, reset, forget, rehome) refuse over an engine
// they could not query, because every one of them makes a claim about ALL of
// them -- "completely removed", "completely still", "migrated". PRINCIPLES.md
// P1 says such a refusal must hand the user the switch that takes the risk
// themselves, and must then say what went unchecked. Both halves are pinned
// here, per command, over an engine whose every query fails: if the flag were
// not honoured, the run would refuse instead of succeeding.

// totalsCommand is one verb under the P1 rule, driven over an installed docker
// and an installed-but-unreachable podman.
type totalsCommand struct {
	name string
	// run drives the verb with the given engine set and ignore flags. ignored
	// engines are handed in too: the filter is the verb's, and a test that
	// pre-filtered would pin nothing.
	run func(s Streams, p project.Paths, docker, podman engineRunner, ignore IgnoreEngines) error
}

func totalsCommands() []totalsCommand {
	return []totalsCommand{
		{"reset", func(s Streams, p project.Paths, docker, podman engineRunner, ig IgnoreEngines) error {
			return reset(s, p, engines(docker, podman), true, ig)
		}},
		{"forget", func(s Streams, p project.Paths, docker, podman engineRunner, ig IgnoreEngines) error {
			return forget(s, p, engines(docker, podman), true, ig)
		}},
		{"rehome", func(s Streams, p project.Paths, docker, podman engineRunner, ig IgnoreEngines) error {
			return rehome(s, p, "oldid", engines(docker, podman), 1000, 1000, ig)
		}},
	}
}

// backup's own pair of this is
// TestBackupRefusesAnUnreachableEngineNamingTheIgnoreFlag and
// TestBackupIgnoredEngineIsNeverQueriedAndEverySurfaceSaysSo.
func TestTotalsCommandsRefusalNamesTheSwitchAndHonoursIt(t *testing.T) {
	for _, tc := range totalsCommands() {
		t.Run(tc.name+" refuses naming the switch", func(t *testing.T) {
			p, _ := testPaths(t)
			docker := &fakeRunner{vols: map[string]bool{volumeName(p.ID, ".claude"): true}}
			podman := downEngine(runner.Podman)
			s, _, _ := testStreams("", false)
			err := tc.run(s, p, docker, podman, IgnoreEngines{})
			if err == nil {
				t.Fatalf("%s must refuse over an engine it could not query", tc.name)
			}
			for _, want := range []string{"podman isn't reachable", "start podman", "--ignore-podman"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s: the refusal must carry %q: %v", tc.name, want, err)
				}
			}
			if len(docker.removed) != 0 {
				t.Errorf("%s: a refusal must remove nothing: %v", tc.name, docker.removed)
			}
		})
		t.Run(tc.name+" honours the switch", func(t *testing.T) {
			p, _ := testPaths(t)
			docker := &fakeRunner{vols: map[string]bool{volumeName(p.ID, ".claude"): true}}
			podman := downEngine(runner.Podman)
			s, _, errb := testStreams("", false)
			if err := tc.run(s, p, docker, podman, IgnoreEngines{Podman: true}); err != nil {
				t.Fatalf("%s with --ignore-podman: %v", tc.name, err)
			}
			// Not queried AT ALL: this engine fails every query it is asked.
			if podman.liveCalls != 0 {
				t.Errorf("%s: the ignored engine was queried %d times", tc.name, podman.liveCalls)
			}
			if !strings.Contains(errb.String(), "podman ignored (--ignore-podman)") {
				t.Errorf("%s: the output must say what was skipped:\n%s", tc.name, errb.String())
			}
		})
	}
}

// Each verb's consequence is its own: what the user is not told is what they
// cannot find later. forget's is the sharpest -- the store goes anyway, so an
// unremoved volume there has nothing naming it afterwards.
func TestIgnoredEngineNotesNameTheConsequencePerVerb(t *testing.T) {
	for _, tc := range []struct {
		name string
		want []string
		run  func(s Streams, p project.Paths, docker, podman engineRunner) error
	}{
		{"reset", []string{"were NOT removed"}, func(s Streams, p project.Paths, docker, podman engineRunner) error {
			return reset(s, p, engines(docker, podman), true, IgnoreEngines{Podman: true})
		}},
		{"forget", []string{"were NOT removed", "orphans", "podman volume ls --filter name=byre-"}, func(s Streams, p project.Paths, docker, podman engineRunner) error {
			return forget(s, p, engines(docker, podman), true, IgnoreEngines{Podman: true})
		}},
		{"rehome", []string{"were not migrated"}, func(s Streams, p project.Paths, docker, podman engineRunner) error {
			return rehome(s, p, "oldid", engines(docker, podman), 1000, 1000, IgnoreEngines{Podman: true})
		}},
		{"backup", []string{"are not in this backup", "could not be ruled out"}, func(s Streams, p project.Paths, docker, podman engineRunner) error {
			rv := combine(merged(config.Config{Volumes: []config.Volume{
				{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
			}}), skills.Resolved{})
			b, _ := backupHarnessAt(t, p, rv, docker.(*fakeRunner), s, BackupOptions{Ignore: IgnoreEngines{Podman: true}})
			b.others = []engineRunner{podman}
			return b.run()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, proj := testPaths(t)
			writeStoreConfig(t, proj, "")
			docker := &fakeRunner{vols: map[string]bool{volumeName(p.ID, ".claude"): true}}
			podman := downEngine(runner.Podman)
			s, _, errb := testStreams("", false)
			if err := tc.run(s, p, docker, podman); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(errb.String(), want) {
					t.Errorf("%s: the ignored-engine note must say %q:\n%s", tc.name, want, errb.String())
				}
			}
		})
	}
}

// A flag naming an engine this machine does not have changed nothing, and byre
// says so rather than letting the user believe something was skipped.
func TestIgnoreFlagForAnAbsentEngineIsANoOpWithANote(t *testing.T) {
	p, _ := testPaths(t)
	docker := &fakeRunner{vols: map[string]bool{volumeName(p.ID, ".claude"): true}}
	s, _, errb := testStreams("", false)
	if err := reset(s, p, engines(docker), true, IgnoreEngines{Podman: true}); err != nil {
		t.Fatal(err)
	}
	if len(docker.removed) != 1 {
		t.Errorf("the installed engine must still be reset: %v", docker.removed)
	}
	if !strings.Contains(errb.String(), "podman is not installed; --ignore-podman changed nothing") {
		t.Errorf("a no-op flag must say so:\n%s", errb.String())
	}
}

// Ignoring every installed engine leaves no total to speak in, so it is a
// refusal rather than a cheerful "no volumes to reset".
func TestIgnoringEveryInstalledEngineIsRefused(t *testing.T) {
	p, _ := testPaths(t)
	docker := &fakeRunner{vols: map[string]bool{volumeName(p.ID, ".claude"): true}}
	s, _, _ := testStreams("", false)
	err := reset(s, p, engines(docker), true, IgnoreEngines{Docker: true})
	if err == nil || !strings.Contains(err.Error(), "every installed engine is ignored") {
		t.Fatalf("want the nothing-to-check refusal, got %v", err)
	}
	if len(docker.removed) != 0 {
		t.Errorf("nothing may be removed: %v", docker.removed)
	}
}

// The switch is for an engine byre could not REACH, never for one it declined
// to run (ADR 0047): a shadowed binary is a different claim -- byre will not
// execute that file -- and no flag talks byre into it.
func TestIgnoreFlagDoesNotExcuseADeclinedBinary(t *testing.T) {
	tree, planted, _ := shadowedAndSafe(t)
	t.Setenv("BYRE_HOME", t.TempDir())
	p, err := project.Resolve(tree)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	s, _, _ := testStreams("", false)
	rerr := Reset(s, tree, true, IgnoreEngines{Docker: true})
	if rerr == nil || !strings.Contains(rerr.Error(), "speaks in totals") {
		t.Fatalf("a declined binary must refuse whatever the flags say, got %v", rerr)
	}
	if !strings.Contains(rerr.Error(), planted) {
		t.Errorf("the refusal must name the binary byre declined: %v", rerr)
	}
}

// splitEngines is the one owner of the filter: it never drops an engine no flag
// named, and it reports a flag that matched nothing separately from one that
// did -- the two have different notes and must not be conflated.
func TestSplitEnginesReportsIgnoredAndAbsentSeparately(t *testing.T) {
	docker := &fakeRunner{}
	sp := splitEngines(engines(docker), IgnoreEngines{Podman: true})
	if len(sp.query) != 1 || sp.query[0].Engine() != runner.Docker {
		t.Fatalf("query = %v, want docker alone", sp.query)
	}
	if len(sp.ignored) != 0 {
		t.Errorf("ignored = %v, want none: podman was never installed", sp.ignored)
	}
	if len(sp.notInstalled) != 1 || sp.notInstalled[0] != "podman" {
		t.Errorf("notInstalled = %v, want [podman]", sp.notInstalled)
	}
	sp = splitEngines(engines(docker, &fakeRunner{engine: runner.Podman}), IgnoreEngines{Podman: true})
	if len(sp.query) != 1 || sp.query[0].Engine() != runner.Docker {
		t.Fatalf("query = %v, want docker alone", sp.query)
	}
	if len(sp.ignored) != 1 || sp.ignored[0] != "podman" || len(sp.notInstalled) != 0 {
		t.Errorf("ignored = %v, notInstalled = %v, want [podman] and none", sp.ignored, sp.notInstalled)
	}
}
