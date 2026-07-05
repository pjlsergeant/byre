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
