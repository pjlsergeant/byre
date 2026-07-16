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
