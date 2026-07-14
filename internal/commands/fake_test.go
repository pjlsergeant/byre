package commands

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

// fakeRunner is the one configurable engineRunner fake for commands tests. The
// zero value is a quiet docker engine: nothing live, no volumes, no images,
// every operation succeeds. Tests set the state fields and read the recorded
// calls; ops additionally records the mutating calls in order, for tests that
// assert sequencing (e.g. build before run).
type fakeRunner struct {
	// mu guards the recorded slices/counters: during develop's netns window the
	// foreground Run (main goroutine) and the netns-init poll (RunningContainers
	// ByLabel + NetnsInit, background goroutine) touch this fake concurrently.
	// The real runner shares no such mutable state; this is test-only.
	mu sync.Mutex

	engine      runner.Engine // "" means docker
	rootless    bool
	rootlessErr error
	keepID      bool // SupportsKeepIDMapping answer (rootless-Podman keep-id path)
	keepIDErr   error

	// sessions
	live          map[string][]string // label -> running container ids
	liveSecond    map[string][]string // consulted from the 2nd query on (lock re-check races)
	liveErr       error
	liveCalls     int
	allContainers map[string][]string // label -> ANY-state ids (ContainersByLabel; pre-start markers)
	allErr        error
	rmContainers  []string          // ContainerRemove calls
	failRmCont    map[string]bool   // container ids whose removal fails (started meanwhile)
	env           map[string]string // ContainerEnv of any id
	envErr        error
	execEnv       map[string]string // env map passed to the last Exec
	labels        map[string]string // ContainerLabels of any id
	labelsErr     error
	execInputs    []string // ExecInput: "id uid:gid args <-stdin"
	execInputErr  error
	creates       [][]string // Create argvs
	createErr     error
	starts        []string // StartAttach: container names
	runErr        error    // StartAttach result
	runHook       func()   // called inside StartAttach: "while the session is live"
	execErr       error
	execs         []string // "id uid:gid workdir cmd..."
	netnsErr      error
	netnsInits    []string // NetnsInit: "container entrypoint"
	netMode       string   // NetworkMode result; "" means "bridge" (private netns)
	netModeErr    error
	stops         []string // Stop: container ids
	stopErr       error
	// sock_groups probe (ProbeSockGroup): default gid 0 success; probeErr fails.
	probeGID   int
	probeErr   error
	probes     []string // "image host target"
	desktop    bool
	desktopErr error

	// volumes
	vols        map[string]bool // existing named volumes
	created     []string
	removed     []string
	seeded      []string          // SeedVolume: name
	seedIdents  []runner.Identity // SeedVolume: the identity each seed ran with
	literals    []string          // SeedLiteral: name:dest=content
	fileSeed    []string          // SeedFiles: name:f1,f2
	migrated    []string          // MigrateVolume: src->dst
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

func (f *fakeRunner) SupportsKeepIDMapping() (bool, error) { return f.keepID, f.keepIDErr }

func (f *fakeRunner) RunningContainersByLabel(label string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (f *fakeRunner) ContainerLabels(id string) (map[string]string, error) {
	return f.labels, f.labelsErr
}

// ExecInput records the call and answers like deliver's in-box scripts do:
// the landed path (dest dir + stem + ext from the script argv), so wiring
// tests see a plausible transport without re-implementing uniquify.
func (f *fakeRunner) ExecInput(id string, uid, gid int, stdin io.Reader, command ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := io.ReadAll(stdin)
	f.execInputs = append(f.execInputs, fmt.Sprintf("%s %d:%d %s <-%s", id, uid, gid, strings.Join(command[3:], " "), b))
	if f.execInputErr != nil {
		return "", f.execInputErr
	}
	if len(command) >= 7 { // sh -c script tag dir stem ext ...
		return command[4] + "/" + command[5] + command[6] + "\n", nil
	}
	if len(command) >= 6 { // dirScript: sh -c script tag stem ext
		return "/inbox/" + command[4] + command[5] + "\n", nil
	}
	return "", nil
}

func (f *fakeRunner) NetworkMode(container string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.netModeErr != nil {
		return "", f.netModeErr
	}
	if f.netMode == "" {
		return "bridge", nil // a private namespace, the normal case
	}
	return f.netMode, nil
}

func (f *fakeRunner) Stop(container string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, container)
	f.ops = append(f.ops, "stop "+container)
	return f.stopErr
}

func (f *fakeRunner) NetnsInit(image, container, entrypoint string, env map[string]string, joinUserns bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := container + " " + entrypoint
	if joinUserns {
		rec += " userns"
	}
	f.netnsInits = append(f.netnsInits, rec)
	f.ops = append(f.ops, "netnsinit "+entrypoint)
	return f.netnsErr
}

func (f *fakeRunner) ProbeSockGroup(image, hostPath, targetPath, userns string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := image + " " + hostPath + " " + targetPath
	if userns != "" {
		rec += " userns=" + userns
	}
	f.probes = append(f.probes, rec)
	f.ops = append(f.ops, "probesock "+targetPath)
	if f.probeErr != nil {
		return 0, f.probeErr
	}
	return f.probeGID, nil
}

func (f *fakeRunner) IsDockerDesktop() (bool, error) {
	return f.desktop, f.desktopErr
}

// ContainersByLabel answers with the any-state extras only (fake simplicity:
// tests exercising the marker path set allContainers; the running-session
// aborts happen at RunningContainersByLabel before this is consulted).
func (f *fakeRunner) ContainersByLabel(label string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.allErr != nil {
		return nil, f.allErr
	}
	return f.allContainers[label], nil
}

func (f *fakeRunner) ContainerRemove(container string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failRmCont[container] {
		return fmt.Errorf("rm %s: container is running", container)
	}
	f.rmContainers = append(f.rmContainers, container)
	f.ops = append(f.ops, "rmcontainer "+container)
	return nil
}

func (f *fakeRunner) Create(args []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, args)
	f.ops = append(f.ops, "createbox")
	return f.createErr
}

func (f *fakeRunner) StartAttach(container string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, container)
	f.ops = append(f.ops, "start")
	if f.runHook != nil {
		f.runHook()
	}
	return f.runErr
}

func (f *fakeRunner) Exec(id string, uid, gid int, workdir string, env map[string]string, tty bool, command ...string) error {
	f.execs = append(f.execs, fmt.Sprintf("%s %d:%d %s %s", id, uid, gid, workdir, strings.Join(command, " ")))
	f.execEnv = env
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

func (f *fakeRunner) SeedVolume(name, hostPath, image string, id runner.Identity) error {
	if f.failSeed {
		return io.EOF
	}
	f.seeded = append(f.seeded, name)
	f.seedIdents = append(f.seedIdents, id)
	f.ops = append(f.ops, "seed "+name)
	return nil
}

func (f *fakeRunner) SeedLiteral(volName, destPath, content, image string, id runner.Identity) error {
	if f.failSeed {
		return io.EOF
	}
	f.literals = append(f.literals, volName+":"+destPath+"="+content)
	f.ops = append(f.ops, "seedliteral "+volName)
	return nil
}

func (f *fakeRunner) SeedFiles(volName, srcDir string, files []string, image string, id runner.Identity) error {
	if f.failSeed {
		return io.EOF
	}
	f.fileSeed = append(f.fileSeed, volName+":"+strings.Join(files, ","))
	f.ops = append(f.ops, "seedfiles "+volName)
	return nil
}

func (f *fakeRunner) MigrateVolume(src, dst, image string, id runner.Identity) error {
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

// engines wraps fakes as the []engineRunner slice the lifecycle commands
// (reset, forget, rehome) take — one entry per installed engine.
func engines(rs ...engineRunner) []engineRunner { return rs }

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
