package gen

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runLauncher drives the real embedded launcher under bash with the gate file
// override, asking it to exec `true` so a successful launch exits 0 instead of
// starting a login shell. HOME is the launcher's own export (harmless in a
// test process); the gate env overrides are the script's test seams.
func runLauncher(t *testing.T, gateFile, timeout string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "launcher.sh")
	if err := os.WriteFile(script, LauncherScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script, "true")
	cmd.Env = append(os.Environ(),
		"BYRE_LAUNCH_GATE_FILE="+gateFile,
		"BYRE_LAUNCH_GATE_TIMEOUT="+timeout,
		// Isolate from the box running the suite: without these the launcher
		// executes the REAL /etc/byre hook dirs, and a hook that prompts
		// (e.g. a login hook on a box whose credential died) hangs the test.
		"BYRE_FIRSTRUN_DIR="+filepath.Join(dir, "no-firstrun"),
		"BYRE_ENVD_DIR="+filepath.Join(dir, "no-envd"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("launcher did not run: %v (%s)", err, out)
	return -1, ""
}

func TestLauncherGateAbsentProceeds(t *testing.T) {
	// No gate file: the launcher must exec the command untouched.
	code, out := runLauncher(t, filepath.Join(t.TempDir(), "no-such-gate"), "1")
	if code != 0 {
		t.Fatalf("launcher with no gate file must proceed; exit %d: %s", code, out)
	}
}

func TestLauncherGateTimesOutClosed(t *testing.T) {
	// Gate file present, nobody ever listens: the launcher must refuse to
	// launch (fail closed) with a legible message, not proceed open.
	dir := t.TempDir()
	gate := filepath.Join(dir, "launch-gate")
	// A port from the dynamic range with no listener.
	if err := os.WriteFile(gate, []byte("59999"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runLauncher(t, gate, "1")
	if code == 0 {
		t.Fatalf("launcher must fail closed when the gate never opens: %s", out)
	}
	if !strings.Contains(out, "refusing to launch") {
		t.Errorf("failure message should say it refused to launch: %s", out)
	}
}

func TestLauncherGateOpensOnListener(t *testing.T) {
	// A listener on the gate port stands in for the netns-init helper: the
	// launcher must connect, unblock, and exec the command.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	dir := t.TempDir()
	gate := filepath.Join(dir, "launch-gate")
	if err := os.WriteFile(gate, []byte(fmt.Sprintf("%d", port)), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runLauncher(t, gate, "10")
	if code != 0 {
		t.Fatalf("launcher must proceed once the gate listener is up; exit %d: %s", code, out)
	}
}

func TestLauncherGateMalformedPortFailsClosed(t *testing.T) {
	// A gate file with no digits yields no usable port: the only safe reading
	// of a present-but-broken gate is "the wall was requested"; fail closed.
	dir := t.TempDir()
	gate := filepath.Join(dir, "launch-gate")
	if err := os.WriteFile(gate, []byte("bogus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _ := runLauncher(t, gate, "1")
	if code == 0 {
		t.Fatal("launcher must fail closed on a malformed gate file")
	}
}

// runLauncherEnvd drives the real launcher with an env.d override dir and a
// command that asserts on the resulting environment.
func runLauncherEnvd(t *testing.T, envdDir string, cmd ...string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "launcher.sh")
	if err := os.WriteFile(script, LauncherScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("bash", append([]string{script}, cmd...)...)
	c.Env = append(os.Environ(),
		"BYRE_LAUNCH_GATE_FILE="+filepath.Join(dir, "no-such-gate"),
		"BYRE_ENVD_DIR="+envdDir,
		// Isolate from the box running the suite (see runLauncher).
		"BYRE_FIRSTRUN_DIR="+filepath.Join(dir, "no-firstrun"),
	)
	out, err := c.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("launcher did not run: %v (%s)", err, out)
	return -1, ""
}

// An env.d hook's exports must land in the exec'd agent process — the whole
// point of the mechanism (a firstrun hook runs in its own process and can't).
func TestLauncherEnvdExportsReachAgent(t *testing.T) {
	envd := t.TempDir()
	hook := "export BYRE_ENVD_PROOF=yes\n"
	if err := os.WriteFile(filepath.Join(envd, "10-proof.sh"), []byte(hook), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runLauncherEnvd(t, envd, "sh", "-c", `test "$BYRE_ENVD_PROOF" = yes`)
	if code != 0 {
		t.Fatalf("env.d export did not reach the agent process (exit %d): %s", code, out)
	}
}

// A hook whose commands fail (reading an absent token file, say) must never
// block the launch — errexit is suspended around the source.
func TestLauncherEnvdBrokenHookStillLaunches(t *testing.T) {
	envd := t.TempDir()
	hook := "false\nexport AFTER_FAILURE=set\nno-such-command-zzz\n"
	if err := os.WriteFile(filepath.Join(envd, "10-broken.sh"), []byte(hook), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runLauncherEnvd(t, envd, "sh", "-c", `test "$AFTER_FAILURE" = set`)
	if code != 0 {
		t.Fatalf("a failing env.d hook must not block the launch (exit %d): %s", code, out)
	}
}

// No env.d dir at all: the launcher proceeds untouched.
func TestLauncherEnvdAbsentProceeds(t *testing.T) {
	code, out := runLauncherEnvd(t, filepath.Join(t.TempDir(), "nope"), "true")
	if code != 0 {
		t.Fatalf("missing env.d dir must be a no-op (exit %d): %s", code, out)
	}
}

// The /etc/profile.d shim sources env.d so a LOGIN SHELL (e.g. `byre shell`)
// gets the same env.d-provided environment the launcher gives the agent. This
// is what closes the D-M2 shell-path gap: COMPOSE_PROJECT_NAME etc. reach a
// hand-typed shell, not just the agent.
func TestProfileEnvShimSourcesEnvd(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "byre-env.sh")
	if err := os.WriteFile(shim, ProfileEnvScript(), 0o644); err != nil {
		t.Fatal(err)
	}
	envd := t.TempDir()
	if err := os.WriteFile(filepath.Join(envd, "50-proof.sh"), []byte("export BYRE_SHIM_PROOF=yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Source the shim the way /etc/profile does, then assert the export landed.
	c := exec.Command("bash", "-c", `. "$1"; test "$BYRE_SHIM_PROOF" = yes`, "bash", shim)
	c.Env = append(os.Environ(), "BYRE_ENVD_DIR="+envd)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("profile.d shim did not source env.d into the shell: %v (%s)", err, out)
	}

	// No env.d dir -> a login shell must still start cleanly (no error).
	c2 := exec.Command("bash", "-c", `. "$1"; true`, "bash", shim)
	c2.Env = append(os.Environ(), "BYRE_ENVD_DIR="+filepath.Join(dir, "nope"))
	if out, err := c2.CombinedOutput(); err != nil {
		t.Fatalf("profile.d shim must be a no-op with no env.d dir: %v (%s)", err, out)
	}
}

// The shim restores the image's ENV PATH after Debian's /etc/profile resets
// it (QA pass-2: a go-template box had no `go` in `byre shell` or the
// agent=none foreground shell). Additive merge: missing entries only, image
// order preserved, prepended; already-present entries are not duplicated.
func TestProfileEnvShimRestoresImagePath(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "byre-env.sh")
	if err := os.WriteFile(shim, ProfileEnvScript(), 0o644); err != nil {
		t.Fatal(err)
	}
	imgPath := filepath.Join(dir, "image-path")
	// Trailing newline as gen's `printf '%s\n' "$PATH"` writes it.
	if err := os.WriteFile(imgPath, []byte("/usr/local/go/bin:/go/bin:/usr/bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reset := "/usr/local/bin:/usr/bin:/bin" // Debian /etc/profile's user PATH
	run := func(script string) (string, error) {
		c := exec.Command("bash", "-c", script, "bash", shim)
		c.Env = []string{
			"PATH=" + reset,
			"BYRE_IMAGE_PATH_FILE=" + imgPath,
			"BYRE_ENVD_DIR=" + filepath.Join(dir, "nope"),
		}
		out, err := c.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	got, err := run(`. "$1"; printf '%s' "$PATH"`)
	if err != nil {
		t.Fatalf("shim failed: %v (%s)", err, got)
	}
	if want := "/usr/local/go/bin:/go/bin:" + reset; got != want {
		t.Fatalf("PATH after shim = %q, want %q", got, want)
	}
	// Idempotent: a nested login shell sourcing it again adds nothing.
	got, err = run(`. "$1"; . "$1"; printf '%s' "$PATH"`)
	if err != nil {
		t.Fatalf("second source failed: %v (%s)", err, got)
	}
	if want := "/usr/local/go/bin:/go/bin:" + reset; got != want {
		t.Fatalf("PATH after double source = %q, want %q", got, want)
	}
	// Missing capture file (image built by an older byre): PATH untouched,
	// shell starts cleanly.
	c := exec.Command("bash", "-c", `. "$1"; printf '%s' "$PATH"`, "bash", shim)
	c.Env = []string{
		"PATH=" + reset,
		"BYRE_IMAGE_PATH_FILE=" + filepath.Join(dir, "nope-file"),
		"BYRE_ENVD_DIR=" + filepath.Join(dir, "nope"),
	}
	out, err := c.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != reset {
		t.Fatalf("missing capture file must leave PATH alone: %v (%s)", err, out)
	}
}

// The worktree populate step: byre creates the worktree --no-checkout on the
// host (agent-safe) and the launcher runs the checkout in the box. Driven via
// the BYRE_WORKSPACE_DIR seam against a real --no-checkout worktree.
func TestLauncherPopulatesPendingWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	git("-c", "user.email=a@b.c", "-c", "user.name=x", "commit", "-q", "--allow-empty", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("-c", "user.email=a@b.c", "-c", "user.name=x", "add", "-A")
	git("-c", "user.email=a@b.c", "-c", "user.name=x", "commit", "-q", "-m", "add file")

	wt := filepath.Join(root, "wt")
	git("worktree", "add", "--no-checkout", "-b", "feat", wt)
	if _, err := os.Stat(filepath.Join(wt, "file.txt")); err == nil {
		t.Fatal("precondition: --no-checkout worktree should be empty")
	}
	gitdir, err := exec.Command("git", "-C", wt, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(strings.TrimSpace(string(gitdir)), "byre-needs-checkout")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := runLauncherInWorktree(t, wt)
	if code != 0 {
		t.Fatalf("launcher exit %d\n%s", code, out)
	}
	// The launcher checked the tree out, in the box's stead.
	if b, err := os.ReadFile(filepath.Join(wt, "file.txt")); err != nil || string(b) != "payload\n" {
		t.Fatalf("worktree not populated: %q err=%v\n%s", b, err, out)
	}
	// Marker cleared on success (so a normal re-launch is a no-op).
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker not cleared after a successful populate")
	}
}

// No marker: a normal box start must not touch the working tree.
func TestLauncherLeavesNonPendingWorktreeAlone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	sentinel := filepath.Join(dir, "untouched")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runLauncherInWorktree(t, dir); code != 0 {
		t.Fatalf("launcher exit %d\n%s", code, out)
	}
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != "keep" {
		t.Fatalf("launcher disturbed a non-pending tree: %q err=%v", b, err)
	}
}

// runLauncherInWorktree drives the real launcher with BYRE_WORKSPACE_DIR set to
// a git worktree, asking it to exec `true`.
func runLauncherInWorktree(t *testing.T, ws string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "launcher.sh")
	if err := os.WriteFile(script, LauncherScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("bash", script, "true")
	c.Env = append(os.Environ(),
		"BYRE_WORKSPACE_DIR="+ws,
		"BYRE_LAUNCH_GATE_FILE="+filepath.Join(dir, "no-such-gate"),
		"BYRE_FIRSTRUN_DIR="+filepath.Join(dir, "no-firstrun"),
		"BYRE_ENVD_DIR="+filepath.Join(dir, "no-envd"),
	)
	out, err := c.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("launcher did not run: %v (%s)", err, out)
	return -1, ""
}

// A box without git (the bare `none` template) can't run the checkout — the
// launcher must say so loudly and KEEP the marker (resumable once git is
// added), not skip silently into an empty tree.
func TestLauncherWorktreeNoGitIsLoudAndResumable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	g := func(a ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	g("-c", "user.email=a@b.c", "-c", "user.name=x", "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(root, "wt")
	g("worktree", "add", "--no-checkout", "-b", "feat", wt)
	gitdir, err := exec.Command("git", "-C", wt, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(strings.TrimSpace(string(gitdir)), "byre-needs-checkout")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// A PATH with the coreutils the launcher needs but NO git.
	toolbin := filepath.Join(root, "bin")
	if err := os.MkdirAll(toolbin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"sed", "head", "rm", "cat", "grep", "mkdir", "dirname", "sleep", "tr", "true"} {
		if p, err := exec.LookPath(tool); err == nil {
			_ = os.Symlink(p, filepath.Join(toolbin, tool))
		}
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "launcher.sh")
	if err := os.WriteFile(script, LauncherScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("bash", script, "true")
	c.Env = []string{
		"PATH=" + toolbin,
		"BYRE_WORKSPACE_DIR=" + wt,
		"BYRE_LAUNCH_GATE_FILE=" + filepath.Join(dir, "no-gate"),
		"BYRE_FIRSTRUN_DIR=" + filepath.Join(dir, "no-firstrun"),
		"BYRE_ENVD_DIR=" + filepath.Join(dir, "no-envd"),
		"HOME=" + dir,
	}
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("launcher should still launch without git: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "box has no git") {
		t.Fatalf("expected a loud no-git message, got:\n%s", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker must survive so the populate is resumable once git is added: %v", err)
	}
}

// A linked worktree with an empty tree and NO marker (a marker a concurrent
// box deleted, or a checkout that never happened) must surface loudly, not
// launch silently into emptiness (codex + grok review).
func TestLauncherWarnsUnpopulatedWorktreeWithoutMarker(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", repo, "-c", "user.email=a@b.c", "-c", "user.name=x", "commit", "-q", "--allow-empty", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	wt := filepath.Join(root, "wt")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "--no-checkout", "-b", "feat", wt).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	// No marker written — the suppressed/never-created case.
	code, out := runLauncherInWorktree(t, wt)
	if code != 0 {
		t.Fatalf("launcher should still launch: %d\n%s", code, out)
	}
	if !strings.Contains(out, "looks unpopulated") {
		t.Fatalf("expected an unpopulated-worktree warning, got:\n%s", out)
	}
}
