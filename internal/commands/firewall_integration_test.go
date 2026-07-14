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
	"fmt"
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
// builds its real image. Returns the image tag, the resolved env to pass the
// netns helper, and the identity the image was built with (keep-id under
// rootless Podman — the boxes and helpers below must run in the same mode a
// real session would).
func buildFirewallImage(t *testing.T, r *runner.Runner) (string, map[string]string, runner.Identity) {
	t.Helper()
	p, _ := testPaths(t)
	if err := builtins.EnsureStore(p.Home); err != nil {
		t.Fatal(err)
	}
	cat, err := builtins.LoadCatalogRaw(p.Home)
	if err != nil {
		t.Fatal(err)
	}
	// The config `egress` key carries the grants: the firewall's own doors
	// are OFFERED, not open (ADR 0020), so a bare firewall config has an
	// empty allowlist and the allow probe would test a deliberate lockdown.
	// 1.1.1.1 is the IP-pinned allow the egress test asserts; github.com
	// exercises the helper's hostname-resolution path (reachability
	// deliberately unasserted — see the probe comment there).
	cfg := config.Config{Skills: []string{"firewall"}, Egress: []string{"1.1.1.1", "github.com"}}
	res, err := skills.Resolve(cfg, cat)
	if err != nil {
		t.Fatalf("resolve firewall skill: %v", err)
	}
	ident := testIdentity(t, r)
	image := imageTag(p.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, cfg, res, image, false, ident); err != nil {
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
	return image, env, ident
}

// uniqueName returns a recognizable, collision-free container name, so
// concurrent or aborted suite runs sharing a daemon can't delete each other's
// live containers (the runner package's smokeName precedent).
func uniqueName(kind string) string {
	return fmt.Sprintf("byre-inttest-%s-%d-%d", kind, os.Getpid(), time.Now().UnixNano()%1_000_000)
}

// boxUserns / helperUserns are the keep-id flags a real session would carry
// (empty slices on rootful engines): the box runs under the keep-id mapping
// (runParams), and any helper joining its namespaces joins the box's own
// userns (runner.NetnsInit) — an identical sibling mapping has no NET_ADMIN
// over the box's netns.
func boxUserns(ident runner.Identity) []string {
	if u := ident.Userns(); u != "" {
		return []string{"--userns=" + u}
	}
	return nil
}

func helperUserns(ident runner.Identity, box string) []string {
	if ident.KeepID {
		return []string{"--userns=container:" + box}
	}
	return nil
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
	image, env, ident := buildFirewallImage(t, r)
	name := uniqueName("fw-egress")
	t.Cleanup(func() { _ = exec.Command(string(r.Engine()), "rm", "-f", name).Run() })

	// The box runs these probes AFTER the launcher's gate opens (the launcher
	// execs "$@" only past the gate). Assertions are pinned to what is
	// DETERMINISTIC under the snapshot model:
	//   ALLOW — 1.1.1.1:443 is granted and dialed BY IP (anycast, fixed):
	//     immune to the failure CI run 29323436590 exposed, where Azure's
	//     forwarding DNS gave the helper 140.82.116.3 for github.com and the
	//     box .4 seconds later, so the (correct) snapshot rule dropped the
	//     probe. Hostname reachability under rotating DNS is the firewall's
	//     documented restart-to-re-resolve limitation, not a test target.
	//   DENY — 9.9.9.9:443 has no grant and must drop; by IP, so no DNS
	//     ambiguity (firewall.sh's own deny self-probe skips granted IPs).
	//   DNS — github.com must RESOLVE in-box: pins the scoped :53 allows.
	//     github.com also rides the egress list (buildFirewallImage), so the
	//     helper's hostname-resolution path stays exercised; only the
	//     rotation-racy connect is not asserted.
	// The DBG lines are diagnosis, not assertion — kept because this test's
	// failures tend to be environment-shaped and a bare FAIL can't be split
	// into "rule didn't match" vs "DNS dead in the box".
	probe := `
echo "DBG nameservers:" $(awk '/^nameserver/{print $2}' /etc/resolv.conf)
echo "DBG github.com ->" $(getent ahosts github.com | awk '{print $1}' | sort -u)
if [ -n "$(getent ahosts github.com)" ]; then echo DNS_OK; else echo DNS_FAIL; fi
if timeout 6 bash -c 'exec 3<>/dev/tcp/1.1.1.1/443' 2>/dev/null; then echo ALLOW_OK; else echo ALLOW_FAIL; fi
if timeout 6 bash -c 'exec 3<>/dev/tcp/9.9.9.9/443' 2>/dev/null; then echo DENY_LEAK; else echo DENY_OK; fi`

	// Start the box detached: the launcher comes up and parks at the gate.
	startArgs := append([]string{"run", "-d", "--name", name}, boxUserns(ident)...)
	start := exec.Command(string(r.Engine()), append(startArgs, image, "bash", "-c", probe)...)
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start box: %v\n%s", err, out)
	}
	waitRunning(t, r, name)

	// Apply the rules from outside, in the box's netns — this also opens the
	// gate (firewall.sh listens on the gate port once rules verify), letting
	// the launcher proceed to the probes. Run with the same argv NetnsInit
	// assembles, but CAPTURE the helper's output: firewall.sh's warnings
	// (per-host resolution, the allow self-probe result) are the difference
	// between "rule didn't match" and "DNS dead in the netns" when this fails
	// on an environment we can't shell into. NetnsInit itself stays covered
	// by the argv unit pins and the restart test.
	helperArgs := []string{"run", "--rm", "-u", "0:0",
		"--net", "container:" + name, "--cap-add", "NET_ADMIN",
		"--entrypoint", "/usr/local/bin/byre-firewall"}
	helperArgs = append(helperArgs, helperUserns(ident, name)...)
	for k, v := range env {
		helperArgs = append(helperArgs, "-e", k+"="+v)
	}
	helperArgs = append(helperArgs, image)
	helperOut, herr := exec.Command(string(r.Engine()), helperArgs...).CombinedOutput()
	t.Logf("netns helper output:\n%s", helperOut)
	if herr != nil {
		t.Fatalf("netns init failed: %v", herr)
	}
	// Snapshot the rules the helper actually installed, while the box (and
	// so its netns) is still alive — same diagnosis-on-failure rationale.
	dumpArgs := append([]string{"run", "--rm", "-u", "0:0",
		"--net", "container:" + name, "--cap-add", "NET_ADMIN",
		"--entrypoint", "iptables"}, helperUserns(ident, name)...)
	if rules, derr := exec.Command(string(r.Engine()),
		append(dumpArgs, image, "-S", "OUTPUT")...).CombinedOutput(); derr == nil {
		t.Logf("netns OUTPUT rules:\n%s", rules)
	} else {
		t.Logf("netns rules dump failed: %v\n%s", derr, rules)
	}

	dockerWait(t, r, name)
	logs := containerLogs(t, r, name)
	if !strings.Contains(logs, "ALLOW_OK") {
		t.Errorf("allowlisted 1.1.1.1:443 should be reachable; logs:\n%s", logs)
	}
	if !strings.Contains(logs, "DENY_OK") || strings.Contains(logs, "DENY_LEAK") {
		t.Errorf("non-allowlisted 9.9.9.9:443 should be DROPPED; logs:\n%s", logs)
	}
	if !strings.Contains(logs, "DNS_OK") {
		t.Errorf("in-box DNS should work through the scoped :53 allows; logs:\n%s", logs)
	}
}

// TestIntegrationFirewallFailsClosed: with NO helper ever run, the launch gate
// must time out and the box must exit WITHOUT running its command — never
// launch open. A short gate timeout keeps the test quick.
func TestIntegrationFirewallFailsClosed(t *testing.T) {
	r := requireEngineRunner(t)
	image, _, ident := buildFirewallImage(t, r)
	name := uniqueName("fw-closed")
	t.Cleanup(func() { _ = exec.Command(string(r.Engine()), "rm", "-f", name).Run() })

	// If the gate ever opened, this marker would appear. It must not.
	startArgs := append([]string{"run", "-d", "--name", name,
		"-e", "BYRE_LAUNCH_GATE_TIMEOUT=4"}, boxUserns(ident)...)
	start := exec.Command(string(r.Engine()), append(startArgs, image, "bash", "-c", "echo GATE_OPENED_LEAK")...)
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
	image, env, ident := buildFirewallImage(t, r)
	name := uniqueName("fw-restart")
	t.Cleanup(func() { _ = exec.Command(string(r.Engine()), "rm", "-f", name).Run() })

	// The marker prints on every successful pass through the gate; the sleep
	// keeps the box running so the restart hits a live container, the way a
	// real session would be hit.
	//
	// The gate timeout governs BOTH launches (env is fixed at create): it is
	// the second launch's refusal wait, but also the first launch's budget to
	// survive until the helper listens. Too tight and the first launch loses
	// that race under a loaded engine (seen at 4s with the runner package's
	// engine tests running in parallel): launcher fails closed before the
	// helper listens, the helper's nc idles its full 60s, and the test dies
	// confusingly late. 30s (the production default) keeps the race
	// unlosable; the second phase pays it once as the refusal wait.
	startArgs := append([]string{"run", "-d", "--name", name,
		"-e", "BYRE_LAUNCH_GATE_TIMEOUT=30"}, boxUserns(ident)...)
	start := exec.Command(string(r.Engine()), append(startArgs, image, "bash", "-c", "echo BOX_RAN; sleep 300")...)
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start box: %v\n%s", err, out)
	}
	waitRunning(t, r, name)
	if err := r.NetnsInit(image, name, "/usr/local/bin/byre-firewall", env, ident.KeepID); err != nil {
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

// buildFirewallOpenImage mirrors buildFirewallImage for the open-denylist
// sibling: a firewall-open project whose config closes example.com. The
// closure rides the `egress` key as a `!` marker and must survive to
// EgressClosed via the real merge (Merge extracts markers), the same road
// develop's cascade takes.
func buildFirewallOpenImage(t *testing.T, r *runner.Runner) (string, map[string]string, runner.Identity) {
	t.Helper()
	p, _ := testPaths(t)
	if err := builtins.EnsureStore(p.Home); err != nil {
		t.Fatal(err)
	}
	cat, err := builtins.LoadCatalogRaw(p.Home)
	if err != nil {
		t.Fatal(err)
	}
	// Two closures: !9.9.9.9 is the IP-pinned drop the egress test asserts
	// (rotation-immune — the same lesson as the firewall test's 1.1.1.1
	// allow, but inverted: a hostname closure that re-resolves differently
	// at probe time MISSES, reading as a leak); !example.com keeps the
	// helper's hostname-resolution path exercised, reachability unasserted.
	cfg := config.Merge(config.Config{}, config.Config{Skills: []string{"firewall-open"}, Egress: []string{"!9.9.9.9", "!example.com"}})
	res, err := skills.Resolve(cfg, cat)
	if err != nil {
		t.Fatalf("resolve firewall-open skill: %v", err)
	}
	ident := testIdentity(t, r)
	image := imageTag(p.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, cfg, res, image, false, ident); err != nil {
		t.Fatalf("firewall-open image failed to build: %v", err)
	}
	env := res.Env()
	if env == nil {
		env = map[string]string{}
	}
	// Mirror develop's netns env: the helper enforces exactly the closures.
	env["BYRE_EGRESS_DENY"] = strings.Join(cfg.EgressClosed, " ")
	return image, env, ident
}

// TestIntegrationFirewallOpenEgress: the open-denylist inverse of the core
// firewall check — an arbitrary host is reachable WITHOUT any grant (the
// network is open), while the closed host is dropped. The probes only run
// because the gate opened, so gating rides along.
func TestIntegrationFirewallOpenEgress(t *testing.T) {
	r := requireEngineRunner(t)
	image, env, ident := buildFirewallOpenImage(t, r)
	name := uniqueName("fwo-egress")
	t.Cleanup(func() { _ = exec.Command(string(r.Engine()), "rm", "-f", name).Run() })

	// github.com has NO egress grant in this config — reachable means open.
	// The hostname is safe HERE (unlike the firewall test's allow probe):
	// under an ACCEPT policy any IP it resolves to connects, so rotation
	// can't race the assertion. The asserted drop is 9.9.9.9 by IP — a
	// hostname deny probe would leak (not just flake) when the box resolves
	// past the snapshot, per CI run 29323436590's lesson.
	probe := `
if timeout 6 bash -c 'exec 3<>/dev/tcp/github.com/443' 2>/dev/null; then echo OPEN_OK; else echo OPEN_FAIL; fi
if timeout 6 bash -c 'exec 3<>/dev/tcp/9.9.9.9/443' 2>/dev/null; then echo DENY_LEAK; else echo DENY_OK; fi`

	startArgs := append([]string{"run", "-d", "--name", name}, boxUserns(ident)...)
	start := exec.Command(string(r.Engine()), append(startArgs, image, "bash", "-c", probe)...)
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start box: %v\n%s", err, out)
	}
	waitRunning(t, r, name)
	if err := r.NetnsInit(image, name, "/usr/local/bin/byre-firewall-open", env, ident.KeepID); err != nil {
		t.Fatalf("netns init failed: %v", err)
	}

	dockerWait(t, r, name)
	logs := containerLogs(t, r, name)
	if !strings.Contains(logs, "OPEN_OK") {
		t.Errorf("ungranted github.com should be reachable on an open network; logs:\n%s", logs)
	}
	if !strings.Contains(logs, "DENY_OK") || strings.Contains(logs, "DENY_LEAK") {
		t.Errorf("closed 9.9.9.9:443 should be DROPPED; logs:\n%s", logs)
	}
}

// TestIntegrationFirewallOpenUnresolvableFailsClosed: an unresolvable closure
// is FATAL for the open sibling (grilled 2026-07-14) — under deny-by-default
// an unresolved host stays safely blocked, but here it would stay silently
// reachable while status claims it blocked. The helper must die and the box
// must never run its command.
func TestIntegrationFirewallOpenUnresolvableFailsClosed(t *testing.T) {
	r := requireEngineRunner(t)
	image, env, ident := buildFirewallOpenImage(t, r)
	env["BYRE_EGRESS_DENY"] = "definitely-not-a-real-host.invalid"
	name := uniqueName("fwo-badhost")
	t.Cleanup(func() { _ = exec.Command(string(r.Engine()), "rm", "-f", name).Run() })

	startArgs := append([]string{"run", "-d", "--name", name,
		"-e", "BYRE_LAUNCH_GATE_TIMEOUT=4"}, boxUserns(ident)...)
	start := exec.Command(string(r.Engine()), append(startArgs, image, "bash", "-c", "echo GATE_OPENED_LEAK")...)
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start box: %v\n%s", err, out)
	}
	waitRunning(t, r, name)
	if err := r.NetnsInit(image, name, "/usr/local/bin/byre-firewall-open", env, ident.KeepID); err == nil {
		t.Errorf("helper must fail on an unresolvable closure")
	}
	if code := dockerWait(t, r, name); code == 0 {
		t.Errorf("box must exit non-zero when the helper dies (fail closed)")
	}
	if logs := containerLogs(t, r, name); strings.Contains(logs, "GATE_OPENED_LEAK") {
		t.Errorf("the command ran despite the dead helper:\n%s", logs)
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

// TestIntegrationFirewallV6GuardFailsClosed: a netns with real (non-loopback)
// IPv6 interfaces whose ip6tables is broken must NOT launch — skipping would
// leave the entire v6 side policy-ACCEPT under a deny-by-default claim
// (firewall-open's review guard, ported to firewall.sh 2026-07-14). The box
// rides an IPv6-enabled network so its netns has v6 interfaces; the helper
// has its ip6tables removed before exec'ing the real script.
func TestIntegrationFirewallV6GuardFailsClosed(t *testing.T) {
	r := requireEngineRunner(t)
	image, env, ident := buildFirewallImage(t, r)

	netName := uniqueName("fw-v6net")
	if out, err := exec.Command(string(r.Engine()), "network", "create",
		"--ipv6", "--subnet", "fd07:b14e::/64", netName).CombinedOutput(); err != nil {
		t.Skipf("engine cannot create an IPv6 network here: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command(string(r.Engine()), "network", "rm", netName).Run() })

	name := uniqueName("fw-v6guard")
	t.Cleanup(func() { _ = exec.Command(string(r.Engine()), "rm", "-f", name).Run() })
	startArgs := append([]string{"run", "-d", "--name", name,
		"--network", netName,
		"-e", "BYRE_LAUNCH_GATE_TIMEOUT=4"}, boxUserns(ident)...)
	start := exec.Command(string(r.Engine()), append(startArgs, image, "bash", "-c", "echo GATE_OPENED_LEAK")...)
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start box: %v\n%s", err, out)
	}
	waitRunning(t, r, name)

	// The helper, with ip6tables removed from its own filesystem (CoW —
	// the shared image is untouched) before the real script runs.
	helperArgs := []string{"run", "--rm", "-u", "0:0",
		"--net", "container:" + name, "--cap-add", "NET_ADMIN",
		"--entrypoint", "bash"}
	helperArgs = append(helperArgs, helperUserns(ident, name)...)
	for k, v := range env {
		helperArgs = append(helperArgs, "-e", k+"="+v)
	}
	helperArgs = append(helperArgs, image,
		"-c", "rm -f /usr/sbin/ip6tables && exec /usr/local/bin/byre-firewall")
	out, err := exec.Command(string(r.Engine()), helperArgs...).CombinedOutput()
	t.Logf("helper output:\n%s", out)
	if err == nil {
		t.Errorf("helper must die when ip6tables is unavailable in a v6-capable netns")
	}
	if !strings.Contains(string(out), "IPv6 side would stay OPEN") {
		t.Errorf("expected the v6 guard's fatal message; got:\n%s", out)
	}
	if code := dockerWait(t, r, name); code == 0 {
		t.Errorf("box must exit non-zero when the helper dies (fail closed)")
	}
	if logs := containerLogs(t, r, name); strings.Contains(logs, "GATE_OPENED_LEAK") {
		t.Errorf("the command ran despite the dead helper:\n%s", logs)
	}
}
