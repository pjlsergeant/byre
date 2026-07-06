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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

// buildFirewallImage materializes the built-in skills, resolves a
// firewall-only project, and builds its real image. Returns the image tag and
// the resolved env to pass the netns helper.
func buildFirewallImage(t *testing.T, r *runner.Runner) (string, map[string]string) {
	t.Helper()
	p, _ := testPaths(t)
	skillsDir := filepath.Join(p.Home, "skills")
	if err := builtins.MaterializeSkills(skillsDir); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Skills: []string{"firewall"}}
	res, err := skills.Resolve(cfg, skillsDir)
	if err != nil {
		t.Fatalf("resolve firewall skill: %v", err)
	}
	image := imageTag(p.ID, os.Getuid(), os.Getgid())
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, cfg, res, image, false); err != nil {
		t.Fatalf("firewall image failed to build: %v", err)
	}
	// Mirror develop's netns env: the helper builds its allowlist from
	// BYRE_EGRESS (the skill union) — without it the box comes up fully
	// locked and the allow probe below would wrongly fail.
	env := res.Env()
	if env == nil {
		env = map[string]string{}
	}
	env["BYRE_EGRESS"] = strings.Join(res.Egress(), " ")
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
	// execs "$@" only past the gate). github.com is on the default allowlist;
	// example.com is not. bash /dev/tcp needs no curl in the image.
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
