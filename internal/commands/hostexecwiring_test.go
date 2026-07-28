package commands

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/project"
)

// The arms in this file are COMMAND-level: they drive the real wiring rather
// than the resolver, so a regression that empties a root set or swallows a
// *hostexec.ShadowError somewhere in between goes red here even while
// hostexec's own tests stay green.

// stubBin writes an executable at dir/name that fails every invocation, so a
// test that reaches an engine call fails fast instead of being answered by a
// lying stub. Returns its path.
func stubBin(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakePATH points PATH at dirs for this test and drops the process pin set on
// the way in and on the way out. hostexec pins per PROCESS -- right for a
// single-shot CLI, wrong for a test binary, where a `docker` pinned by an
// earlier test would answer past the PATH set here.
func fakePATH(t *testing.T, dirs ...string) {
	t.Helper()
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
	hostexec.ResetPins()
	t.Cleanup(hostexec.ResetPins)
}

// shadowedAndSafe sets up the case both reviewers named as missing: ONE engine
// planted in a box-writable tree, the OTHER a normal system install. Returns
// the tree, the planted docker's path, and the root set naming the tree.
func shadowedAndSafe(t *testing.T) (tree, planted string, roots hostexec.Roots) {
	t.Helper()
	tree = t.TempDir()
	safe := t.TempDir()
	planted = stubBin(t, tree, "docker")
	stubBin(t, safe, "podman")
	fakePATH(t, tree, safe)
	return tree, planted, hostexec.NewRoots(tree)
}

// A declined engine is NOT an absent one. installedEngines must hand back the
// safe engine AND report the declined one, because every caller that
// enumerates engines makes a statement about them.
func TestInstalledEnginesReportsDeclinedSeparately(t *testing.T) {
	tree, planted, roots := shadowedAndSafe(t)

	out, declined := installedEngines(roots)
	if len(out) != 1 || out[0].Engine() != "podman" {
		t.Fatalf("engines = %v, want podman only (docker sits in a box-writable dir)", out)
	}
	if len(declined) != 1 {
		t.Fatalf("declined = %v, want the shadowed docker", declined)
	}
	if declined[0].Engine != "docker" {
		t.Errorf("declined engine = %q, want docker", declined[0].Engine)
	}
	var shadow *hostexec.ShadowError
	if !errors.As(declined[0].Err, &shadow) {
		t.Fatalf("declined err = %v, want the *ShadowError to survive collection", declined[0].Err)
	}
	if shadow.Path != planted || shadow.Root != tree {
		t.Errorf("shadow = %+v, want path=%s root=%s", shadow, planted, tree)
	}
}

// hostexec is not the only thing that declines a binary: Go's own LookPath
// refuses a RELATIVE PATH entry with exec.ErrDot, and that refusal is part of
// what hostexec's contract leans on ("the absolute case is this package's").
// It must reach the callers as declined, not as absent -- typing the
// collection to one refusal made every other refusal read as absence again.
func TestInstalledEnginesDeclinesRelativePathEntry(t *testing.T) {
	dir := t.TempDir()
	stubBin(t, dir, "docker")
	t.Chdir(dir)
	fakePATH(t, ".") // a relative entry: Go answers with ErrDot, never ErrNotFound

	out, declined := installedEngines(hostexec.NewRoots(t.TempDir()))
	if len(out) != 0 {
		t.Fatalf("engines = %v, want none — a relative PATH entry is not runnable", out)
	}
	if len(declined) != 1 || declined[0].Engine != "docker" {
		t.Fatalf("declined = %v, want the docker Go refused", declined)
	}
	if !errors.Is(declined[0].Err, exec.ErrDot) {
		t.Errorf("declined err = %v, want exec.ErrDot", declined[0].Err)
	}
	// The disclosure has to name the ENGINE: exec's own error names a path and
	// not what byre was looking for.
	var out2 bytes.Buffer
	noteDeclinedEngines(&out2, declined, "consequence.")
	for _, want := range []string{"docker", "relative", "consequence."} {
		if !strings.Contains(out2.String(), want) {
			t.Errorf("disclosure missing %q: %s", want, out2.String())
		}
	}
	// ...and the same refusal must fail the totals commands, not drop out.
	if _, err := lifecycleEngines(hostexec.NewRoots(t.TempDir())); err == nil ||
		!strings.Contains(err.Error(), "speaks in totals") {
		t.Errorf("lifecycleEngines err = %v, want the totals refusal", err)
	}
}

// The consequence the reviewers traced: develop's cross-engine check must see
// a declined engine as UNCHECKABLE. Silently short by one, a live docker
// session sits invisible while `engine = podman` starts a second agent on the
// same tree.
func TestRefuseCrossEngineSessionSkipsAndDisclosesDeclined(t *testing.T) {
	paths, _ := testPaths(t)
	var out bytes.Buffer
	declined := []declinedEngine{{
		Engine: "docker",
		Err:    &hostexec.ShadowError{Name: "docker", Path: "/proj/.bin/docker", Root: "/proj"},
	}}

	skipped, err := refuseCrossEngineSession(&out, nil, declined, "podman", paths)
	if err != nil {
		t.Fatalf("a declined engine must not fail develop: %v", err)
	}
	// Returned as skipped: the engine record must stay UNRESOLVED, or the next
	// session stops re-checking an engine byre never looked at.
	if len(skipped) != 1 || skipped[0] != "docker" {
		t.Errorf("skipped = %v, want [docker]", skipped)
	}
	got := out.String()
	for _, want := range []string{"declines to run docker", "/proj/.bin/docker", "can't be ruled out"} {
		if !strings.Contains(got, want) {
			t.Errorf("disclosure missing %q: %s", want, got)
		}
	}
}

// The lifecycle commands speak in totals ("completely removed", "migrated").
// An engine byre never reached fails the enumeration rather than dropping out
// of it -- otherwise forget deletes the store while real docker volumes,
// images and credentials stay behind, under the word "completely".
func TestLifecycleEnginesRefusesOverADeclinedEngine(t *testing.T) {
	_, planted, roots := shadowedAndSafe(t)

	rs, err := lifecycleEngines(roots)
	if rs != nil {
		t.Errorf("a partial engine set must not be handed to a totals command: %v", rs)
	}
	if !errors.As(err, new(*hostexec.ShadowError)) {
		t.Fatalf("err = %v, want the *hostexec.ShadowError to survive the wrapping", err)
	}
	for _, want := range []string{"speaks in totals", planted} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q: %s", want, err)
		}
	}
}

// ...and an engine that is genuinely NOT INSTALLED still just drops out: the
// distinction is the whole point of runner.NotInstalledError.
func TestLifecycleEnginesSkipsAnAbsentEngine(t *testing.T) {
	safe := t.TempDir()
	stubBin(t, safe, "podman")
	fakePATH(t, safe)

	rs, err := lifecycleEngines(hostexec.NewRoots(t.TempDir()))
	if err != nil {
		t.Fatalf("an absent docker must not fail the enumeration: %v", err)
	}
	if len(rs) != 1 || rs[0].Engine() != "podman" {
		t.Fatalf("engines = %v, want podman only", rs)
	}
}

// develop's ENGINE is the hard refusal: nothing safe can proceed, so the
// command ends by name. Drives Develop end-to-end so an empty root set here
// (the wiring regression) goes red.
func TestDevelopRefusesShadowedEngine(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()
	planted := stubBin(t, proj, "docker")
	fakePATH(t, proj)

	err := Develop(discardStreams(), proj, "", "", nil, false)
	var shadow *hostexec.ShadowError
	if !errors.As(err, &shadow) {
		t.Fatalf("err = %v, want *hostexec.ShadowError", err)
	}
	if shadow.Name != "docker" || shadow.Path != planted {
		t.Errorf("refusal = %+v, want the planted docker at %s", shadow, planted)
	}
}

// ...and the exit report's git is the DEGRADE: develop discloses once, before
// the session, and carries no git for the rest of the run. Drives Develop so
// a disclosure that stopped being printed (or a root set that stopped
// containing the work tree) goes red.
func TestDevelopDisclosesShadowedGitAndContinues(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()
	engineDir := t.TempDir()
	planted := stubBin(t, proj, "git")
	stubBin(t, engineDir, "docker")
	fakePATH(t, proj, engineDir)

	s, _, errBuf := testStreams("", false)
	// The engine stub fails every call, so develop gets past resolution and
	// then stops at its first real engine query. What matters is that the
	// disclosure was printed BEFORE that, and that the declined git did not
	// end the run itself.
	err := Develop(s, proj, "", "", nil, false)

	// CONTINUED, not refused: printing the line and then returning the git
	// refusal would satisfy the text asserts below while gating a session on
	// the thing the session-end report reads.
	var shadow *hostexec.ShadowError
	if errors.As(err, &shadow) && shadow.Name == "git" {
		t.Fatalf("develop must degrade past a declined git, not refuse on it: %v", err)
	}

	got := errBuf.String()
	for _, want := range []string{"declines to run git", planted, "skipped for this session"} {
		if !strings.Contains(got, want) {
			t.Errorf("git disclosure missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "declines to run docker") {
		t.Errorf("the engine resolved from outside the tree and must not be declined:\n%s", got)
	}
}

// The rule behind that disclosure, at its own level: a declined git yields no
// git AND a line; an absent one yields no git and SILENCE (a host with no git
// is not news); a resolvable one yields the path and nothing to say.
func TestHostGitForSessionDispositions(t *testing.T) {
	safe := t.TempDir()
	realGit := stubBin(t, safe, "git")
	tree := t.TempDir()
	fakePATH(t, safe)

	exe, disclosure := hostGitForSession(hostexec.NewRoots(tree))
	if exe != realGit || disclosure != "" {
		t.Errorf("resolvable git = (%q, %q), want (%q, \"\")", exe, disclosure, realGit)
	}

	hostexec.ResetPins()
	exe, disclosure = hostGitForSession(hostexec.NewRoots(safe))
	if exe != "" {
		t.Errorf("a declined git must yield no path, got %q", exe)
	}
	for _, want := range []string{"declines to run git", realGit, safe} {
		if !strings.Contains(disclosure, want) {
			t.Errorf("disclosure missing %q: %s", want, disclosure)
		}
	}

	hostexec.ResetPins()
	fakePATH(t, t.TempDir()) // nothing on PATH at all
	if exe, disclosure = hostGitForSession(hostexec.NewRoots(tree)); exe != "" || disclosure != "" {
		t.Errorf("absent git = (%q, %q), want (\"\", \"\") — silence, not a disclosure", exe, disclosure)
	}
}

// A declined clipboard helper reads as TOOL UNAVAILABLE, not as a hard
// failure: these capabilities degrade per axis (ADR 0021), and a refusal that
// broke `byre deliver` outright would be a gate where a degrade belongs.
func TestClipboardTreatsDeclinedToolAsUnavailable(t *testing.T) {
	orig := clipLookPath
	t.Cleanup(func() { clipLookPath = orig })
	clipLookPath = func(name string, _ hostexec.Roots) (string, error) {
		return "", &hostexec.ShadowError{Name: name, Path: "/proj/" + name, Root: "/proj"}
	}
	roots := hostexec.NewRoots("/proj")

	// A terminal is still a write path: OSC 52 needs no host tool.
	var term bytes.Buffer
	if c := clipboardWriter("darwin", env(nil), &term, roots); c == nil || c.Name != "OSC 52" {
		t.Errorf("writer = %+v, want the OSC 52 fallback", c)
	}
	// No terminal and no runnable tool: no write path, which deliver already
	// renders as "clipboard unavailable".
	if c := clipboardWriter("darwin", env(nil), nil, roots); c != nil {
		t.Errorf("writer = %+v, want nil", c)
	}
	// The read side has no fallback at all and simply offers no backend.
	if b := linuxBackend(env(map[string]string{"DISPLAY": ":0"}), roots); b != nil {
		t.Errorf("reader = %+v, want nil", b)
	}
}

// The root set must survive a project.Resolve failure. Resolve reads worktree
// metadata the box writes, so an agent can ARRANGE the failure -- and deliver
// spawns clipboard/picker/notifier helpers before any later resolution error
// surfaces. Narrower is fine; empty is a hole the box opened for itself.
func TestBoxWritableRootsForSurvivesAResolveFailure(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()
	// A .git FILE pointing at a <common>/worktrees/<name> that isn't there:
	// worktree-SHAPED, so detectWorktree refuses to fall back to standalone
	// and hard-errors on the unreadable commondir. Exactly the metadata a box
	// can write into its own tree.
	pointer := "gitdir: " + filepath.Join(proj, "gone", "worktrees", "wt") + "\n"
	if err := os.WriteFile(filepath.Join(proj, ".git"), []byte(pointer), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Resolve(proj); err == nil {
		t.Skip("this fixture no longer makes Resolve fail; the fallback needs another shape")
	}

	roots := boxWritableRootsFor(proj)
	planted := stubBin(t, proj, "zenity")
	r := hostexec.NewResolver(func(string) (string, error) { return planted, nil })
	if _, err := r.Look("zenity", roots); !errors.As(err, new(*hostexec.ShadowError)) {
		t.Fatalf("err = %v, want *hostexec.ShadowError — a Resolve failure must not empty the root set", err)
	}
}
