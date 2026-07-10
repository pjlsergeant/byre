package commands

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// liveWorkdir marks a session live for the project's worktree label — the
// label develop's fast path and run-race re-check query.
func liveWorkdir(p project.Paths, ids ...string) map[string][]string {
	return map[string][]string{workdirKey + "=" + p.WorktreeID: ids}
}

// exitError produces a real *exec.ExitError with the given exit code, so tests
// exercise develop's status mapping against the type docker's CLI failure
// actually returns.
func exitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	if err == nil {
		t.Fatalf("expected sh to exit %d", code)
	}
	return err
}

func TestDevelopRefusesWhenSessionLive(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{live: liveWorkdir(p, "abcdef0123456789")}
	s, _, stderr := testStreams("", false)
	err := develop(f, s, p, combine(config.Config{}, skills.Resolved{}), false)
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != ExitRefused {
		t.Fatalf("expected ExitError{%d}, got %v", ExitRefused, err)
	}
	if len(f.builds) != 0 || len(f.runs) != 0 {
		t.Fatalf("must not build or run when a session is live: builds=%v runs=%v", f.builds, f.runs)
	}
	// No walls go up on a refusal: the exposure lines describe a run that
	// isn't happening.
	if strings.Contains(stderr.String(), "byre: exposure:") {
		t.Errorf("exposure lines must not print when develop refuses: %s", stderr.String())
	}
}

func TestDevelopBuildsSeedsThenRuns(t *testing.T) {
	p, _ := testPaths(t)
	seedSrc := t.TempDir()
	cfg := config.Config{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude", Seed: &config.Seed{Host: seedSrc}},
	}}
	f := &fakeRunner{}
	if err := develop(f, discardStreams(), p, combine(cfg, skills.Resolved{}), false); err != nil {
		t.Fatal(err)
	}
	image := imageTag(p.ID, os.Getuid(), os.Getgid())
	if len(f.builds) != 1 || f.builds[0] != image {
		t.Fatalf("expected one cached build of %s, got %v", image, f.builds)
	}
	if len(f.seeded) != 1 || f.seeded[0] != volumeName(p.ID, ".claude") {
		t.Fatalf("expected the state volume seeded, got %v", f.seeded)
	}
	if len(f.runs) != 1 {
		t.Fatalf("expected one run, got %v", f.runs)
	}
	// Build, then seed, then run — seeding uses the image just built, and the
	// interactive run must come after setup completes.
	ops := strings.Join(f.ops, " | ")
	bi, si, ri := strings.Index(ops, "build"), strings.Index(ops, "seed"), strings.Index(ops, "run")
	if !(bi >= 0 && bi < si && si < ri) {
		t.Fatalf("expected build < seed < run, got ops %v", f.ops)
	}
	// The run argv is the assembled `run ...` for this project's image.
	argv := strings.Join(f.runs[0], " ")
	if !strings.HasPrefix(argv, "run ") || !strings.Contains(argv, image) {
		t.Fatalf("run argv doesn't run the built image: %s", argv)
	}
	if !strings.Contains(argv, "--name byre-"+p.WorktreeID) {
		t.Fatalf("run argv missing the session container name: %s", argv)
	}
}

// Every real session opens by showing the walls going up: the exposure lines
// print right before the run, and their counts must match what runParams
// actually does (disabled mounts don't bind; config egress joins the union).
func TestDevelopOpensWithExposureLines(t *testing.T) {
	p, _ := testPaths(t)
	cfg := config.Config{
		Mounts: []config.Mount{
			{Host: "/h/notes", Target: "/notes", Mode: "ro"},
			{Host: "/h/data", Target: "/data", Mode: "rw", Disabled: true},
		},
		Ports: []config.Port{{Container: 8080}},
		Env:   map[string]string{"FOO": "1", "BAR": "2"},
		// "example.com" normalizes to example.com:443; "github.com" restates
		// the skill's door in another spelling — one enforced host, not two.
		Egress: []string{"example.com", "github.com"},
	}
	var fw skills.Skill
	fw.Name = "firewall"
	fw.File.Runtime.NetworkPosture = "deny-by-default"
	fw.File.Runtime.Egress = []string{"github.com:443", "proxy.golang.org:443"}
	fw.File.Runtime.Env = map[string]string{"FOO": "skill"} // restates a config key: one var
	f := &fakeRunner{}
	s, _, stderr := testStreams("", false)
	if err := develop(f, s, p, combine(cfg, skills.Resolved{Skills: []skills.Skill{fw}}), false); err != nil {
		t.Fatal(err)
	}
	out := stderr.String()
	if !strings.Contains(out, "byre: exposure: /workspace rw · 1 host mount (+1 disabled) · 1 port · 2 env vars\n") {
		t.Errorf("expected the exposure line, got: %s", out)
	}
	if !strings.Contains(out, "byre: network deny-by-default · egress 3 hosts\n") {
		t.Errorf("expected the network line (2 skill hosts + 1 config host, dup spelling deduped), got: %s", out)
	}
}

func TestDevelopRunRaceReportsRefusal(t *testing.T) {
	p, _ := testPaths(t)
	// Nothing live at the fast path, run fails, and the re-check finds the
	// winner's container: a concurrent develop won the container-name race.
	f := &fakeRunner{
		runErr:     exitError(t, 125),
		liveSecond: liveWorkdir(p, "cafebabe0000"),
	}
	err := develop(f, discardStreams(), p, combine(config.Config{}, skills.Resolved{}), false)
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != ExitRefused {
		t.Fatalf("expected ExitError{%d} after losing the run race, got %v", ExitRefused, err)
	}
}

func TestDevelopAgentExitCodePassesThrough(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{runErr: exitError(t, 7)}
	err := develop(f, discardStreams(), p, combine(config.Config{}, skills.Resolved{}), false)
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 7 {
		t.Fatalf("expected the agent's own exit 7 passed through, got %v", err)
	}
}

func TestDevelopEngineFailureStaysByreError(t *testing.T) {
	p, _ := testPaths(t)
	// Docker reserves 125-127 for engine-level failures; with no session live at
	// the re-check, that must surface as a byre error, not the agent's status.
	f := &fakeRunner{runErr: exitError(t, 126)}
	err := develop(f, discardStreams(), p, combine(config.Config{}, skills.Resolved{}), false)
	var exitErr ExitError
	if err == nil || errors.As(err, &exitErr) {
		t.Fatalf("engine failure must stay an ordinary error, got %v", err)
	}
}

func TestDevelopSelfEditNotesAndMount(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{}
	s, _, stderr := testStreams("", false)
	if err := develop(f, s, p, combine(config.Config{}, skills.Resolved{}), true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "self-edit is on") {
		t.Errorf("expected the self-edit warning on stderr: %s", stderr.String())
	}
	if argv := strings.Join(f.runs[0], " "); !strings.Contains(argv, "target="+selfEditTarget) {
		t.Errorf("run argv missing the self-edit mount: %s", argv)
	}
	// Config untouched during the session: no diff noise at exit.
	if strings.Contains(stderr.String(), "the project store changed") {
		t.Errorf("no config diff expected for an unchanged session: %s", stderr.String())
	}
}

func TestDevelopSelfEditShowsConfigDiffOnExit(t *testing.T) {
	p, _ := testPaths(t)
	cfgPath := filepath.Join(p.Dir, config.ProjectConfigName)
	if err := os.WriteFile(cfgPath, []byte("base = \"node:22\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{}
	// "During the session" the agent rewrites its own config.
	f.runHook = func() {
		os.WriteFile(cfgPath, []byte("base = \"node:22\"\nrun_args = [\"--privileged\"]\n"), 0o644)
	}
	s, _, stderr := testStreams("", false)
	if err := develop(f, s, p, combine(config.Config{}, skills.Resolved{}), true); err != nil {
		t.Fatal(err)
	}
	out := stderr.String()
	if !strings.Contains(out, "the project store changed") {
		t.Fatalf("expected the exit diff header, got: %s", out)
	}
	if !strings.Contains(out, `+ run_args = ["--privileged"]`) {
		t.Errorf("expected the added line in the diff, got: %s", out)
	}
	if strings.Contains(out, `- base`) {
		t.Errorf("unchanged line must not appear as removed: %s", out)
	}
	// Without --self-edit the config isn't the agent's to change; no snapshot,
	// no diff, even if the file moved underneath us.
	f2 := &fakeRunner{runHook: func() {
		os.WriteFile(cfgPath, []byte("base = \"debian:bookworm\"\n"), 0o644)
	}}
	s2, _, stderr2 := testStreams("", false)
	if err := develop(f2, s2, p, combine(config.Config{}, skills.Resolved{}), false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr2.String(), "the project store changed") {
		t.Errorf("no diff expected without --self-edit: %s", stderr2.String())
	}
}

func TestRebuildBuildsNoCache(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{}
	var out bytes.Buffer
	if err := rebuild(&out, f, p, config.Config{}, skills.Resolved{}); err != nil {
		t.Fatal(err)
	}
	image := imageTag(p.ID, os.Getuid(), os.Getgid())
	if len(f.builds) != 1 || f.builds[0] != image+" nocache" {
		t.Fatalf("expected one --no-cache build of %s, got %v", image, f.builds)
	}
}

// netnsSkill builds a Resolved with one skill declaring a netns_init hook, the
// way the firewall skill does.
func netnsSkill(path string) skills.Resolved {
	var sk skills.Skill
	sk.Name = "fw"
	sk.File.Runtime.NetnsInit = path
	return skills.Resolved{Skills: []skills.Skill{sk}}
}

// pinNonce pins the per-invocation byre.run nonce so a test can pre-key the
// fake's label queries, restoring the real generator afterwards.
func pinNonce(t *testing.T, v string) {
	t.Helper()
	orig := runNonce
	runNonce = func() string { return v }
	t.Cleanup(func() { runNonce = orig })
}

func TestDevelopRunsNetnsInitsOnceUp(t *testing.T) {
	p, _ := testPaths(t)
	pinNonce(t, "feedface")
	// liveSecond, not live: the develop fast path queries the workdir label
	// first and must see NOTHING (else it refuses as already-running). Our box
	// appears only from the 2nd query on — what the netns poll sees after the
	// run starts it. The poll keys on the byre.run NONCE label (the ownership
	// proof — the name and path-derived labels are plantable) and the hook
	// must target the container id resolved from it.
	id := "cafef00d1234"
	f := &fakeRunner{liveSecond: map[string][]string{"byre.run=feedface": {id}}}
	err := develop(f, discardStreams(), p, combine(config.Config{}, netnsSkill("/usr/local/bin/byre-firewall")), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.netnsInits) != 1 || f.netnsInits[0] != id+" /usr/local/bin/byre-firewall" {
		t.Fatalf("expected the netns hook applied to OUR container id, got %v", f.netnsInits)
	}
	// The nonce label must actually be on the run argv (asserted with the
	// identity labels, after run_args) or the poll could never match.
	if argv := strings.Join(f.runs[0], " "); !strings.Contains(argv, "--label byre.run=feedface") {
		t.Errorf("run argv missing the nonce label: %s", argv)
	}
}

func TestDevelopNetnsInitSkippedWhenBoxNeverRuns(t *testing.T) {
	p, _ := testPaths(t)
	pinNonce(t, "feedface")
	// Our container never appears under the nonce label (e.g. the run failed
	// instantly, or a squatter holds the name — it can't hold the nonce): the
	// poll must exit via the done channel, not hang or fire the hook.
	f := &fakeRunner{}
	err := develop(f, discardStreams(), p, combine(config.Config{}, netnsSkill("/usr/local/bin/byre-firewall")), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.netnsInits) != 0 {
		t.Fatalf("hook must not fire for a box that never came up: %v", f.netnsInits)
	}
}

func TestDevelopNetnsInitFailureWarnsFailClosed(t *testing.T) {
	p, _ := testPaths(t)
	pinNonce(t, "feedface")
	f := &fakeRunner{
		liveSecond: map[string][]string{"byre.run=feedface": {"cafef00d1234"}},
		netnsErr:   errors.New("iptables: boom"),
	}
	s, _, stderr := testStreams("", false)
	if err := develop(f, s, p, combine(config.Config{}, netnsSkill("/usr/local/bin/byre-firewall")), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "failing closed") {
		t.Errorf("hook failure must explain the fail-closed outcome: %s", stderr.String())
	}
}

func TestDevelopNetnsInitRefusesSharedNamespace(t *testing.T) {
	// A box in a shared network namespace (--network host or container:<other>
	// via run_args) is not byre's to firewall: the root+NET_ADMIN hook would
	// rewrite host (or foreign-container) network state. The hook must not
	// fire, and the skip must explain the fail-closed outcome.
	for _, mode := range []string{"host", "container:deadbeef", "ns:/proc/1/ns/net"} {
		t.Run(mode, func(t *testing.T) {
			p, _ := testPaths(t)
			pinNonce(t, "feedface")
			f := &fakeRunner{
				liveSecond: map[string][]string{"byre.run=feedface": {"cafef00d1234"}},
				netMode:    mode,
			}
			s, _, stderr := testStreams("", false)
			if err := develop(f, s, p, combine(config.Config{}, netnsSkill("/usr/local/bin/byre-firewall")), false); err != nil {
				t.Fatal(err)
			}
			if len(f.netnsInits) != 0 {
				t.Fatalf("hook must not fire into a shared (%s) namespace: %v", mode, f.netnsInits)
			}
			if !strings.Contains(stderr.String(), "not byre's to modify") || !strings.Contains(stderr.String(), "failing closed") {
				t.Errorf("skip must name the shared namespace and the fail-closed outcome: %s", stderr.String())
			}
			// The gate can't be trusted in a shared namespace (any listener on
			// the gate port opens it), so byre must stop the box itself.
			if len(f.stops) != 1 || f.stops[0] != "cafef00d1234" {
				t.Errorf("shared-namespace skip must stop the box: stops=%v", f.stops)
			}
		})
	}
}

func TestDevelopNetnsInitSkipsOnUnknownNetworkMode(t *testing.T) {
	// No proof of a private namespace, no hooks: an inspect failure skips the
	// hooks (the gate fails the launch closed) rather than firing blind.
	p, _ := testPaths(t)
	pinNonce(t, "feedface")
	f := &fakeRunner{
		liveSecond: map[string][]string{"byre.run=feedface": {"cafef00d1234"}},
		netModeErr: errors.New("inspect: boom"),
	}
	s, _, stderr := testStreams("", false)
	if err := develop(f, s, p, combine(config.Config{}, netnsSkill("/usr/local/bin/byre-firewall")), false); err != nil {
		t.Fatal(err)
	}
	if len(f.netnsInits) != 0 {
		t.Fatalf("hook must not fire without a known network mode: %v", f.netnsInits)
	}
	if !strings.Contains(stderr.String(), "failing closed") {
		t.Errorf("skip must explain the fail-closed outcome: %s", stderr.String())
	}
	if len(f.stops) != 1 {
		t.Errorf("unknown-mode skip must stop the box: stops=%v", f.stops)
	}
}

func TestDevelopWorktreeSaysImageBuildsFromMain(t *testing.T) {
	// Worktrees inherit the project image, so build inputs resolve from the
	// main worktree — every worktree session must say so, or a branch editing
	// a build input silently runs an image built from other content.
	p, _ := testPaths(t)
	p.IsWorktree = true
	p.WorkDir = t.TempDir()
	f := &fakeRunner{}
	s, _, stderr := testStreams("", false)
	if err := develop(f, s, p, combine(config.Config{}, skills.Resolved{}), false); err != nil {
		t.Fatal(err)
	}
	if msg := stderr.String(); !strings.Contains(msg, "main worktree") || !strings.Contains(msg, p.Canonical) {
		t.Errorf("worktree develop must state the image builds from the main worktree: %s", msg)
	}
}

func TestDevelopNoNetnsSkillNoHelper(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{liveSecond: liveWorkdir(p, "cafef00d1234")}
	if err := develop(f, discardStreams(), p, combine(config.Config{}, skills.Resolved{}), false); err != nil {
		t.Fatal(err)
	}
	if len(f.netnsInits) != 0 {
		t.Fatalf("no skill declares a hook; none must run: %v", f.netnsInits)
	}
	// And no nonce label is added when nothing consumes it.
	if argv := strings.Join(f.runs[0], " "); strings.Contains(argv, "byre.run=") {
		t.Errorf("nonce label must not appear without netns hooks: %s", argv)
	}
}

func TestDevelopNetnsNoNonceSkipsHooks(t *testing.T) {
	p, _ := testPaths(t)
	pinNonce(t, "") // randomness unavailable
	f := &fakeRunner{}
	s, _, stderr := testStreams("", false)
	if err := develop(f, s, p, combine(config.Config{}, netnsSkill("/usr/local/bin/byre-firewall")), false); err != nil {
		t.Fatal(err)
	}
	if len(f.netnsInits) != 0 {
		t.Fatalf("no nonce, no ownership proof — hooks must not run: %v", f.netnsInits)
	}
	if !strings.Contains(stderr.String(), "fail the launch closed") {
		t.Errorf("expected the fail-closed note, got: %s", stderr.String())
	}
}

// resolvedEgress unions skill egress with the config `egress` key (ADR 0019),
// normalized to host:port and deduped.
func TestResolvedEgressUnionsConfigKey(t *testing.T) {
	rv := resolved{
		cfg: config.Config{Egress: []string{"grafana.com", "api.anthropic.com"}},
		skills: skills.Resolved{Skills: []skills.Skill{
			func() skills.Skill {
				var sk skills.Skill
				sk.Name = "claude"
				sk.File.Runtime.Egress = []string{"api.anthropic.com"}
				return sk
			}(),
		}},
	}
	got := resolvedEgress(rv)
	want := []string{"api.anthropic.com:443", "grafana.com:443"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolvedEgress = %v, want %v", got, want)
	}
}
