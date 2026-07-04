package commands

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"

	"byre/internal/project"
	"byre/internal/runner"
)

// fakeRunner is the one configurable engineRunner fake for commands tests. The
// zero value is a quiet docker engine: nothing live, no volumes, no images,
// every operation succeeds. Tests set the state fields and read the recorded
// calls; ops additionally records the mutating calls in order, for tests that
// assert sequencing (e.g. build before run).
type fakeRunner struct {
	engine      runner.Engine // "" means docker
	rootless    bool
	rootlessErr error

	// sessions
	live       map[string][]string // label -> running container ids
	liveSecond map[string][]string // consulted from the 2nd query on (lock re-check races)
	liveErr    error
	liveCalls  int
	env        map[string]string // ContainerEnv of any id
	envErr     error
	runErr     error
	execErr    error
	runs       [][]string
	execs      []string // "id uid:gid workdir cmd..."

	// volumes
	vols        map[string]bool // existing named volumes
	created     []string
	removed     []string
	seeded      []string // SeedVolume: name
	literals    []string // SeedLiteral: name:dest=content
	fileSeed    []string // SeedFiles: name:f1,f2
	migrated    []string // MigrateVolume: src->dst
	failSeed    bool
	failMigrate string          // MigrateVolume dst to fail on
	failRemove  map[string]bool // volume names whose removal fails

	// images
	images   map[string]bool // tag -> exists
	rmImages []string
	builds   []string // tag, with " nocache" appended when noCache
	buildErr error

	ops []string
}

func (f *fakeRunner) Engine() runner.Engine {
	if f.engine == "" {
		return runner.Docker
	}
	return f.engine
}

func (f *fakeRunner) IsRootlessPodman() (bool, error) { return f.rootless, f.rootlessErr }

func (f *fakeRunner) RunningContainersByLabel(label string) ([]string, error) {
	f.liveCalls++
	if f.liveErr != nil {
		return nil, f.liveErr
	}
	if f.liveCalls >= 2 && f.liveSecond != nil {
		return f.liveSecond[label], nil
	}
	return f.live[label], nil
}

func (f *fakeRunner) ContainerEnv(id string) (map[string]string, error) { return f.env, f.envErr }

func (f *fakeRunner) Run(args []string) error {
	f.runs = append(f.runs, args)
	f.ops = append(f.ops, "run")
	return f.runErr
}

func (f *fakeRunner) Exec(id string, uid, gid int, workdir string, env map[string]string, tty bool, command ...string) error {
	f.execs = append(f.execs, fmt.Sprintf("%s %d:%d %s %s", id, uid, gid, workdir, strings.Join(command, " ")))
	f.ops = append(f.ops, "exec")
	return f.execErr
}

func (f *fakeRunner) VolumesByPrefix(prefix string) ([]string, error) {
	var out []string
	for v := range f.vols {
		if strings.HasPrefix(v, prefix) {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (f *fakeRunner) VolumeExists(name string) (bool, error) { return f.vols[name], nil }

func (f *fakeRunner) VolumeCreate(name string) error {
	f.created = append(f.created, name)
	f.ops = append(f.ops, "create "+name)
	if f.vols == nil {
		f.vols = map[string]bool{}
	}
	f.vols[name] = true
	return nil
}

func (f *fakeRunner) VolumeRemove(name string) error {
	if f.failRemove[name] {
		return fmt.Errorf("remove %s: boom", name)
	}
	f.removed = append(f.removed, name)
	f.ops = append(f.ops, "remove "+name)
	delete(f.vols, name)
	return nil
}

func (f *fakeRunner) SeedVolume(name, hostPath, image string, uid, gid int) error {
	if f.failSeed {
		return io.EOF
	}
	f.seeded = append(f.seeded, name)
	f.ops = append(f.ops, "seed "+name)
	return nil
}

func (f *fakeRunner) SeedLiteral(volName, destPath, content, image string, uid, gid int) error {
	if f.failSeed {
		return io.EOF
	}
	f.literals = append(f.literals, volName+":"+destPath+"="+content)
	f.ops = append(f.ops, "seedliteral "+volName)
	return nil
}

func (f *fakeRunner) SeedFiles(volName, srcDir string, files []string, image string, uid, gid int) error {
	if f.failSeed {
		return io.EOF
	}
	f.fileSeed = append(f.fileSeed, volName+":"+strings.Join(files, ","))
	f.ops = append(f.ops, "seedfiles "+volName)
	return nil
}

func (f *fakeRunner) MigrateVolume(src, dst, image string, uid, gid int) error {
	if dst == f.failMigrate {
		return fmt.Errorf("copy boom")
	}
	f.migrated = append(f.migrated, src+"->"+dst)
	f.ops = append(f.ops, "migrate "+src+"->"+dst)
	return nil
}

func (f *fakeRunner) ImageExists(tag string) (bool, error) { return f.images[tag], nil }

func (f *fakeRunner) ImageRemove(tag string) error {
	f.rmImages = append(f.rmImages, tag)
	f.ops = append(f.ops, "rmimage "+tag)
	return nil
}

func (f *fakeRunner) Build(tag, dockerfile, contextDir string, noCache bool, buildArgs []string) error {
	b := tag
	if noCache {
		b += " nocache"
	}
	f.builds = append(f.builds, b)
	f.ops = append(f.ops, "build "+tag)
	return f.buildErr
}

var _ engineRunner = (*fakeRunner)(nil)

// testStreams builds Streams over buffers: the returned buffers capture Out
// and Err, in feeds prompts, tty marks stdin as interactive.
func testStreams(in string, tty bool) (Streams, *bytes.Buffer, *bytes.Buffer) {
	var out, errBuf bytes.Buffer
	return Streams{Out: &out, Err: &errBuf, In: strings.NewReader(in), TTY: tty}, &out, &errBuf
}

// discardStreams is a non-TTY Streams that swallows all output — for tests
// that only care about behavior, not messages.
func discardStreams() Streams {
	return Streams{Out: io.Discard, Err: io.Discard, In: strings.NewReader("")}
}

// testPaths points BYRE_HOME at a temp dir and resolves + bootstraps a fresh
// temp project, returning its paths and the project dir.
func testPaths(t *testing.T) (project.Paths, string) {
	t.Helper()
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()
	p, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	return p, proj
}
