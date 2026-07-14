package commands

// Gated firewall integration: real docker/podman, run host-side with
//
//	BYRE_DOCKER_TESTS=1 go test ./internal/commands/ -run IntegrationFirewall -v
//
// These are the checks a unit test can't vouch for: that the built firewall
// image + the netns-init helper actually (a) apply egress rules that let an
// allowlisted host through and drop everything else, (b) gate the box's launch
// on those rules landing, and (c) FAIL CLOSED — the box never runs the command
// if the helper never signals. Needs outbound network (debian + apt on a cold
// cache; github/example.com reachable from the host).
//
// They drive the mechanism directly (build the image, start the box, call
// NetnsInit against it) rather than the full develop orchestration — the
// nonce-label plumbing and goroutine are covered by the unit suite; what needs
// a live engine is the iptables/gate behavior inside a real netns.

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

// buildFirewallImage prepares the store, resolves a firewall-only project, and
// builds its real image. Returns the image tag and the resolved env to pass
// the netns helper.
func buildFirewallImage(t *testing.T, r *runner.Runner) (string, map[string]string) {
	t.Helper()
	p, _ := testPaths(t)
	if err := builtins.EnsureStore(p.Home); err != nil {
		t.Fatal(err)
	}
	cat, err := builtins.LoadCatalogRaw(p.Home)
	if err != nil {
		t.Fatal(err)
	}
	// github.com rides the config `egress` key: the firewall's own doors are
	// OFFERED, not open (ADR 0020), so a bare firewall config has an empty
	// allowlist and the allow probe below would test a deliberate lockdown.
	cfg := config.Config{Skills: []string{"firewall"}, Egress: []string{"github.com"}}
	res, err := skills.Resolve(cfg, cat)
	if err != nil {
		t.Fatalf("resolve firewall skill: %v", err)
	}
	image := imageTag(p.ID, os.Getuid(), os.Getgid())
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, cfg, res, image, false); err != nil {
		t.Fatalf("firewall image failed to build: %v", err)
	}
	// Mirror develop's netns env exactly: BYRE_EGRESS is the full allowlist
	// union — skill-declared egress plus the config `egress` key — via the
	// same resolvedEgress develop uses. Without it the box comes up fully
	// locked and the allow probe below would wrongly fail.
	env := res.Env()
	if env == nil {
		env = map[string]string{}
	}
	env["BYRE_EGRESS"] = strings.Join(resolvedEgress(combine(cfg, res)), " ")
	return image, env
}

// dockerWait blocks until the named container exits and returns its exit code.
func dockerWait(t *testing.T, r *runner.Runner, name string) int {
	t.Helper()
	out, err := exec.Command(string(r.Engine()), "wait", name).CombinedOutput()
	if err != nil {
		t.Fatalf("wait %s: %v\n%s", name, err, out)
	}
	code := strings.TrimSpace(string(out))
	switch code {
	case "0":
		return 0
	default:
		return 1 // any non-zero; the tests only care zero vs not
	}
}

// TestIntegrationFirewallEgress is the core check: with the helper applied,
// an allowlisted host is reachable and a non-allowlisted one is dropped — and
// the box only got to run its probes because the launch gate opened, which it
// only does after the rules verify.
func TestIntegrationFirewallEgress(t *testing.T) {
	r := requireEngineRunner(t)
	image, env := buildFirewallImage(t, r)
	name := "byre-inttest-fw-egress"
	_ = exec.Command(string(r.Engine()), "rm", "-f", name).Run() // clear any leftover
	t.Cleanup(func() { _ = exec.Command(string(r.Engine()), "rm", "-f", name).Run() })

	// The box runs these probes AFTER the launcher's gate opens (the launcher
	// execs "$@" only past the gate). github.com is granted via the config
	// egress key above; example.com is not. bash /dev/tcp needs no curl in
	// the image.
	probe := `
if timeout 6 bash -c 'exec 3<>/dev/tcp/github.com/443' 2>/dev/null; then echo ALLOW_OK; else echo ALLOW_FAIL; fi
if timeout 6 bash -c 'exec 3<>/dev/tcp/example.com/443' 2>/dev/null; then echo DENY_LEAK; else echo DENY_OK; fi`

	// Start the box detached: the launcher comes up and parks at the gate.
	start := exec.Command(string(r.Engine()), "run", "-d", "--name", name, image, "bash", "-c", probe)
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start box: %v\n%s", err, out)
	}
	waitRunning(t, r, name)

	// Apply the rules from outside, in the box's netns — this also opens the
	// gate (firewall.sh listens on the gate port once rules verify), letting
	// the launcher proceed to the probes.
	if err := r.NetnsInit(image, name, "/usr/local/bin/byre-firewall", env); err != nil {
		t.Fatalf("netns init failed: %v", err)
	}

	dockerWait(t, r, name)
	logs := containerLogs(t, r, name)
	if !strings.Contains(logs, "ALLOW_OK") {
		t.Errorf("allowlisted host github.com should be reachable; logs:\n%s", logs)
	}
	if !strings.Contains(logs, "DENY_OK") || strings.Contains(logs, "DENY_LEAK") {
		t.Errorf("non-allowlisted host example.com should be DROPPED; logs:\n%s", logs)
	}
}

// TestIntegrationFirewallFailsClosed: with NO helper ever run, the launch gate
// must time out and the box must exit WITHOUT running its command — never
// launch open. A short gate timeout keeps the test quick.
func TestIntegrationFirewallFailsClosed(t *testing.T) {
	r := requireEngineRunner(t)
	image, _ := buildFirewallImage(t, r)
	name := "byre-inttest-fw-closed"
	_ = exec.Command(string(r.Engine()), "rm", "-f", name).Run()
	t.Cleanup(func() { _ = exec.Command(string(r.Engine()), "rm", "-f", name).Run() })

	// If the gate ever opened, this marker would appear. It must not.
	start := exec.Command(string(r.Engine()), "run", "-d", "--name", name,
		"-e", "BYRE_LAUNCH_GATE_TIMEOUT=4",
		image, "bash", "-c", "echo GATE_OPENED_LEAK")
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start box: %v\n%s", err, out)
	}

	// No NetnsInit: nobody opens the gate. The launcher must give up and exit
	// non-zero within the timeout window.
	if code := dockerWait(t, r, name); code == 0 {
		t.Errorf("box must exit non-zero when the gate never opens (fail closed)")
	}
	logs := containerLogs(t, r, name)
	if strings.Contains(logs, "GATE_OPENED_LEAK") {
		t.Errorf("the command ran despite no firewall — the launch was NOT gated:\n%s", logs)
	}
	if !strings.Contains(logs, "refusing to launch") {
		t.Errorf("expected the launcher's fail-closed message; logs:\n%s", logs)
	}
}

// TestIntegrationFirewallRestartFailsClosed: a box that launched legitimately
// and is then restarted (user's `docker restart`, daemon restart) comes up
// with a FRESH netns — the rules are gone and nothing re-runs the helper. The
// launcher must park at the gate again and time out: the box's command runs
// exactly once, never a second time without a wall. This is the stale-state
// half of fail-closed (launcher.sh: "times out again rather than trusting
// stale state"); FailsClosed above covers the never-launched half.
func TestIntegrationFirewallRestartFailsClosed(t *testing.T) {
	r := requireEngineRunner(t)
	image, env := buildFirewallImage(t, r)
	name := "byre-inttest-fw-restart"
	_ = exec.Command(string(r.Engine()), "rm", "-f", name).Run()
	t.Cleanup(func() { _ = exec.Command(string(r.Engine()), "rm", "-f", name).Run() })

	// The marker prints on every successful pass through the gate; the sleep
	// keeps the box running so the restart hits a live container, the way a
	// real session would be hit.
	start := exec.Command(string(r.Engine()), "run", "-d", "--name", name,
		"-e", "BYRE_LAUNCH_GATE_TIMEOUT=4",
		image, "bash", "-c", "echo BOX_RAN; sleep 300")
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start box: %v\n%s", err, out)
	}
	waitRunning(t, r, name)
	if err := r.NetnsInit(image, name, "/usr/local/bin/byre-firewall", env); err != nil {
		t.Fatalf("netns init failed: %v", err)
	}
	waitForLog(t, r, name, "BOX_RAN") // first launch made it through the gate

	// Restart: SIGKILL quickly (-t 1; PID-1 bash won't die to TERM), fresh
	// netns, and nobody runs the helper again.
	if out, err := exec.Command(string(r.Engine()), "restart", "-t", "1", name).CombinedOutput(); err != nil {
		t.Fatalf("restart box: %v\n%s", err, out)
	}

	// The relaunched box must time out at the gate and exit non-zero.
	if code := dockerWait(t, r, name); code == 0 {
		t.Errorf("restarted box must exit non-zero when nothing re-opens the gate (fail closed)")
	}
	logs := containerLogs(t, r, name)
	if got := strings.Count(logs, "BOX_RAN"); got != 1 {
		t.Errorf("box command must run exactly once (the gated first launch), ran %d times:\n%s", got, logs)
	}
	if !strings.Contains(logs, "refusing to launch") {
		t.Errorf("expected the launcher's fail-closed message on the restarted run; logs:\n%s", logs)
	}
}

// waitForLog polls the container's logs until the marker appears, or fails
// after a short deadline.
func waitForLog(t *testing.T, r *runner.Runner, name, marker string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(containerLogs(t, r, name), marker) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("container %s never logged %q", name, marker)
}

// waitRunning polls until the named container reports running (the netns must
// exist before the helper can join it), or fails after a short deadline.
func waitRunning(t *testing.T, r *runner.Runner, name string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := exec.Command(string(r.Engine()), "inspect", "-f", "{{.State.Running}}", name).CombinedOutput()
		if strings.TrimSpace(string(out)) == "true" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("container %s never reported running", name)
}

// containerLogs returns the combined stdout+stderr of a (finished) container.
func containerLogs(t *testing.T, r *runner.Runner, name string) string {
	t.Helper()
	out, err := exec.Command(string(r.Engine()), "logs", name).CombinedOutput()
	if err != nil {
		t.Fatalf("logs %s: %v\n%s", name, err, out)
	}
	return string(out)
}
