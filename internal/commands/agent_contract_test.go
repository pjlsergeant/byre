package commands

// Agent-contract tier: gated on BYRE_AGENT_TESTS=1 (its own gate, separate
// from BYRE_DOCKER_TESTS — these builds run live agent INSTALLERS over the
// network, so they are slower and flakier than the engine suite and ride
// their own schedule: .github/workflows/agents.yml, every two days + push).
//
// The other half of every byre<->agent contract. The unit suite pins byre's
// half by driving the real hook/wrapper scripts against STUB binaries; these
// tests pin the AGENTS' half — the assumptions recorded version-stamped in
// docs/AGENT-CREDENTIAL-MECHANICS.md that a routine agent release can
// silently invalidate (boxes install agents unpinned: npm latest, curl
// installers). Each TestAgentContract* test builds the agent's real box via
// the generator and probes only what needs NO credentials; login/rotation
// mechanics stay field gates. Every test logs the installed agent version
// first, so even green runs leave a version trail to correlate drift against.
//
// One resident that is NOT part of the canary: TestOpencodeSharedAuthLiveGate
// (bottom of this file) drives a real login — it shares the tier's gate and
// helpers but is named outside the TestAgentContract prefix so agents.yml's
// -run patterns can never schedule it; it exists to be run ad hoc.
//
// Cache caveat: builds run WITH the engine's layer cache, so a warm daemon
// (local, the inttest VM) reuses the agent binary from the last build — a
// local re-run answers "do the contracts hold for the version I already
// have", not "did upstream drift today". The scheduled canary runs on
// ephemeral GitHub runners, which are cold by construction; that path is
// the drift detector.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

func requireAgentRunner(t *testing.T) *runner.Runner {
	t.Helper()
	if os.Getenv("BYRE_AGENT_TESTS") != "1" {
		t.Skip("set BYRE_AGENT_TESTS=1 to run agent-contract tests (live agent installs)")
	}
	setting := os.Getenv("BYRE_TEST_ENGINE")
	if setting == "" {
		setting = "auto"
	}
	eng, err := runner.Detect(setting, nil)
	if err != nil {
		t.Fatalf("BYRE_AGENT_TESTS=1 but no engine (BYRE_TEST_ENGINE=%q): %v", setting, err)
	}
	t.Logf("engine: %s", eng)
	return runner.New(eng)
}

// buildAgentBox writes cfgTOML as the project config, resolves it, and builds
// the real image (network: the agent's installer runs in the build). Returns
// the image tag; removal is registered on t.Cleanup.
func buildAgentBox(t *testing.T, r *runner.Runner, cfgTOML string) (string, resolved, runner.Identity, project.Paths) {
	t.Helper()
	p, proj := testPaths(t)
	if err := os.WriteFile(filepath.Join(p.Dir, config.ProjectConfigName), []byte(cfgTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	rv, err := resolve(p, proj, nil)
	if err != nil {
		t.Fatal(err)
	}
	ident := testIdentity(t, r)
	image := imageTag(p.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, rv.cfg, rv.skills, image, false, ident); err != nil {
		t.Fatalf("agent box failed to build: %v", err)
	}
	return image, rv, ident, p
}

// agentProbe runs one command in the built image with the entrypoint
// bypassed (no firstrun hooks — probes must stay loginless) and returns its
// combined output. env entries are NAME=value.
func agentProbe(t *testing.T, r *runner.Runner, image string, env []string, cmd ...string) string {
	t.Helper()
	args := []string{"run", "--rm"}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, "--entrypoint", cmd[0], image)
	args = append(args, cmd[1:]...)
	out, err := exec.Command(string(r.Engine()), args...).CombinedOutput()
	if err != nil {
		t.Fatalf("probe %v failed: %v\n%s", cmd, err, out)
	}
	return string(out)
}

// TestAgentContractOpencode: the assumptions the opencode skill and ADR 0033
// adapter stake on the opencode binary.
//   - OPENCODE_CONFIG_CONTENT carries an `mcp` map that `opencode mcp list`
//     resolves (the injection seam; this probe is the live half of the
//     ADR 0033 "inject" vouch — the wip handoff's check 1).
//   - auth.json lives at the XDG data dir the shared-auth hook links
//     (`opencode debug paths`).
func TestAgentContractOpencode(t *testing.T) {
	r := requireAgentRunner(t)
	image, _, _, _ := buildAgentBox(t, r, `agent = "opencode"

[[mcp]]
name = "byre-probe"
command = ["echo", "hi"]
`)

	t.Logf("opencode version: %s", strings.TrimSpace(agentProbe(t, r, image, nil, "opencode", "--version")))

	// The raw injection seam. A local echo server is never spawned for a
	// LIST — the probe pins config resolution, not server health.
	out := agentProbe(t, r, image,
		[]string{`OPENCODE_CONFIG_CONTENT={"mcp":{"byre-env-probe":{"type":"local","command":["echo","hi"]}}}`},
		"opencode", "mcp", "list")
	if !strings.Contains(out, "byre-env-probe") {
		t.Fatalf("opencode no longer resolves mcp servers from OPENCODE_CONFIG_CONTENT — the ADR 0033 inject contract broke:\n%s", out)
	}

	// The SHIPPED adapter end to end: baked /etc/byre/mcp.json -> wrapper ->
	// opencode resolves the declared server (the inject vouch, live).
	out = agentProbe(t, r, image, nil, "byre-opencode-mcp-launch", "mcp", "list")
	if !strings.Contains(out, "byre-probe") {
		t.Fatalf("the byre-opencode-mcp-launch wrapper no longer delivers baked servers — the ADR 0033 inject vouch broke:\n%s", out)
	}

	// The credential path the shared-auth hook symlinks.
	paths := agentProbe(t, r, image, nil, "opencode", "debug", "paths")
	if !strings.Contains(paths, "/home/dev/.local/share/opencode") {
		t.Fatalf("opencode data dir moved off the XDG path the shared-auth hook links:\n%s", paths)
	}
}

// TestAgentContractCodex: the codex skill's MCP adapter is already vouched
// (`mcp = "inject"`); this pins its agent half — the shipped wrapper's
// `-c mcp_servers.*` overrides still register with the binary, end to end
// from a baked /etc/byre/mcp.json.
func TestAgentContractCodex(t *testing.T) {
	r := requireAgentRunner(t)
	image, _, _, _ := buildAgentBox(t, r, `agent = "codex"

[[mcp]]
name = "byre-probe"
command = ["echo", "hi"]
`)

	t.Logf("codex version: %s", strings.TrimSpace(agentProbe(t, r, image, nil, "codex", "--version")))

	out := agentProbe(t, r, image, nil, "byre-codex-mcp-launch", "mcp", "list")
	if !strings.Contains(out, "byre-probe") {
		t.Fatalf("codex no longer honors the wrapper's -c mcp_servers overrides — the ADR 0033 inject vouch broke:\n%s", out)
	}
}

// TestAgentContractGemini: the assumptions the gemini + gemini-shared-auth
// skills stake on the npm-installed bundle. No loginless CLI surface reaches
// the auth internals, so the seed/storage contract is pinned by PRESENCE
// probes over the installed source (the same tokens the 2026-07-16 source
// pass verified): if a release drops selectedType or renames the plaintext
// store, these greps go red and the mechanics doc needs a re-pass.
func TestAgentContractGemini(t *testing.T) {
	r := requireAgentRunner(t)
	image, _, _, _ := buildAgentBox(t, r, "agent = \"gemini\"\n")

	t.Logf("gemini version: %s", strings.TrimSpace(agentProbe(t, r, image, nil, "gemini", "--version")))

	// ripgrep: gemini's native search tool; the skill installs it so every
	// session doesn't warn and fall back to GrepTool.
	t.Logf("ripgrep: %s", strings.TrimSpace(strings.SplitN(agentProbe(t, r, image, nil, "rg", "--version"), "\n", 2)[0]))

	for _, token := range []string{
		"selectedType",              // settings key the shared-auth hook seeds
		"oauth_creds.json",          // the default plaintext OAuth store the hook links
		"clearCachedCredentialFile", // the dialog-only rm the seed exists to dodge
	} {
		out := agentProbe(t, r, image, nil, "sh", "-c",
			fmt.Sprintf(`grep -rlq %q "$(npm root -g)/@google/gemini-cli" && echo FOUND || echo MISSING`, token))
		if !strings.Contains(out, "FOUND") {
			t.Errorf("installed gemini bundle no longer carries %q — the shared-auth seed/link contract needs a re-pass (docs/AGENT-CREDENTIAL-MECHANICS.md, Gemini section)", token)
		}
	}
}

// TestAgentContractGrok: the grok-shared-auth v2 broker (ADR 0036) hangs off
// GROK_AUTH_PROVIDER_COMMAND, and the skill relocates state via GROK_HOME.
// Both are closed-source env seams — presence in the shipped binary is the
// only loginless probe there is.
func TestAgentContractGrok(t *testing.T) {
	r := requireAgentRunner(t)
	image, _, _, _ := buildAgentBox(t, r, "agent = \"grok\"\n")

	t.Logf("grok version: %s", strings.TrimSpace(agentProbe(t, r, image, nil, "grok", "--version")))

	for _, seam := range []string{"GROK_AUTH_PROVIDER_COMMAND", "GROK_HOME"} {
		out := agentProbe(t, r, image, nil, "sh", "-c",
			fmt.Sprintf(`grep -ac %s "$(readlink -f "$(command -v grok)")" || true`, seam))
		if strings.TrimSpace(out) == "0" {
			t.Errorf("grok binary no longer carries %s — the broker/relocation seam is gone (ADR 0036)", seam)
		}
	}
}

// TestOpencodeSharedAuthLiveGate: the two-box API-key FIELD GATE for
// opencode-shared-auth (the wip handoff's check 2) — deliberately NOT named
// TestAgentContract*: it drives a real login and writes a credential store,
// so it stays out of the loginless canary tier and agents.yml's -run
// patterns. Run it ad hoc (BYRE_AGENT_TESTS=1, fresh engine). Box A runs a REAL
// `opencode auth login` (the api-key prompt driven through a pty — a dummy
// key: the gate proves the sharing mechanism, not key validity) through the
// REAL launch path, so the firstrun hooks place the symlink first; box B,
// a different project on the same machine-scoped identity volume, must see
// the credential through its own opencode.
func TestOpencodeSharedAuthLiveGate(t *testing.T) {
	r := requireAgentRunner(t)

	// ANTHROPIC_API_KEY set: the opencode-login firstrun hook exits early
	// (env-key path) instead of opening its interactive picker under the
	// launcher — the test drives the login itself, as its own command.
	cfgTOML := `agent = "opencode"
skills = ["opencode-shared-auth"]

[env]
ANTHROPIC_API_KEY = "env-skip-login-hook"
`
	imageA, rvA, ident, pA := buildAgentBox(t, r, cfgTOML)
	imageB, rvB, _, pB := buildAgentBox(t, r, cfgTOML)
	if pA.ID == pB.ID {
		t.Fatalf("test projects collided on ID %q", pA.ID)
	}

	paramsA, err := runParams(pA, rvA, imageA, false, false, ident)
	if err != nil {
		t.Fatal(err)
	}
	paramsB, err := runParams(pB, rvB, imageB, false, false, ident)
	if err != nil {
		t.Fatal(err)
	}
	// Identify the machine-scoped identity volume both boxes share, and
	// start it clean so a stale credential can't satisfy the read.
	identityVol := ""
	for _, v := range paramsA.Volumes {
		if strings.Contains(v.Target, ".byre-identity") {
			identityVol = v.Name
		}
	}
	if identityVol == "" {
		t.Fatalf("no identity volume in run params: %+v", paramsA.Volumes)
	}
	// The machine-scoped identity volume has a FIXED name — on a developer
	// host it may hold real shared logins. This test only ever touches a
	// volume it OWNS, proven by a label (the grok-v1 never-clobber lesson):
	//   1. skip if the volume already exists (a real store, or anything else);
	//   2. create it ourselves, labeled — a failed create is a loud daemon
	//      problem, never treated as "absent";
	//   3. re-verify the label before proceeding and before removal, so even
	//      a lost create race (something claimed the name between 1 and 2)
	//      cannot end with this test deleting a volume it didn't make.
	// Residual (accepted): a concurrent byre session could store a REAL login
	// into our labeled volume mid-test; cleanup would remove it. That needs a
	// user logging in on the same daemon within the test's ~30s window of an
	// ad-hoc run — CI and the sacrificial VM have no such sessions.
	engine := string(r.Engine())
	// Per-INVOCATION nonce, not a constant label value: an engine's
	// create-if-absent can hand a second concurrent invocation the first
	// one's volume, and a shared value would make both believe they own it
	// (and both delete it). Only the invocation whose nonce is on the
	// volume owns it.
	nonce := fmt.Sprintf("gate-%d-%d", os.Getpid(), time.Now().UnixNano())
	ownedByTest := func() bool {
		out, err := exec.Command(engine, "volume", "inspect",
			"--format", `{{ index .Labels "byre-test" }}`, identityVol).Output()
		return err == nil && strings.TrimSpace(string(out)) == nonce
	}
	if exec.Command(engine, "volume", "inspect", identityVol).Run() == nil {
		t.Skipf("machine identity volume %q already exists — refusing to touch a possibly-real credential store", identityVol)
	}
	if out, err := exec.Command(engine, "volume", "create", "--label", "byre-test="+nonce, identityVol).CombinedOutput(); err != nil {
		// A failed create over a volume that now exists is a lost race —
		// leave it alone; a failed create with no volume at all is a loud
		// daemon problem.
		if exec.Command(engine, "volume", "inspect", identityVol).Run() == nil {
			t.Skipf("identity volume %q appeared concurrently — leaving it alone (create said: %s)", identityVol, out)
		}
		t.Fatalf("creating the test-owned identity volume: %v\n%s", err, out)
	}
	// Cleanup registers BEFORE the ownership check: if the create succeeded
	// but the inspect below fails transiently, the skip must not orphan the
	// fixed-name volume (a leak here becomes the store real sessions adopt).
	// The nonce check keeps removal safe when create merely returned a
	// concurrently created volume.
	t.Cleanup(func() {
		_ = exec.Command(engine, "rm", "-f", paramsA.Name).Run()
		_ = exec.Command(engine, "rm", "-f", paramsB.Name).Run()
		// Project-scoped volumes (.opencode state) are named after the temp
		// test projects — always this test's own; remove them all (the
		// sibling integration tests' cleanup contract). Only the FIXED-name
		// identity volume needs the nonce-ownership guard.
		for _, params := range []runner.RunParams{paramsA, paramsB} {
			for _, v := range params.Volumes {
				if v.Name != identityVol {
					_ = r.VolumeRemove(v.Name)
				}
			}
		}
		if ownedByTest() {
			_ = r.VolumeRemove(identityVol)
		}
	})
	if !ownedByTest() {
		t.Skipf("machine identity volume %q exists without this invocation's nonce — lost a create race; leaving it alone", identityVol)
	}

	key := fmt.Sprintf("sk-byre-gate-%d-%d", os.Getpid(), time.Now().UnixNano())
	// Box A: the pty-driven login, then prove the write landed THROUGH the
	// symlink in the shared volume (not a local fork).
	paramsA.Command = []string{"bash", "-c", fmt.Sprintf(`
set -e
( sleep 3; printf '%s\r' ) | TERM=xterm timeout 120 script -qec 'opencode auth login --provider mistral' /dev/null >/tmp/login.out 2>&1 || {
  echo "LOGIN FAILED"; cat /tmp/login.out; exit 1; }
cred=/home/dev/.local/share/opencode/auth.json
[ -L "$cred" ] || { echo "NOT A SYMLINK — login forked the credential"; ls -la "$cred"; exit 1; }
grep -q %s /home/dev/.byre-identity/opencode/auth.json || { echo "KEY NOT IN SHARED STORE"; exit 1; }
echo BOX_A_STORED_OK
`, key, key)}
	out, err := exec.Command(string(r.Engine()), runner.RunArgs(paramsA)...).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "BOX_A_STORED_OK") {
		t.Fatalf("box A login did not store through the shared symlink: %v\n%s", err, out)
	}

	// Box B: its own opencode reads the credential box A stored. auth list
	// prints the store path + provider entries; the file-level check pins
	// the same inode.
	paramsB.Command = []string{"bash", "-c", fmt.Sprintf(`
set -e
grep -q %s /home/dev/.byre-identity/opencode/auth.json || { echo "SHARED STORE EMPTY IN BOX B"; exit 1; }
opencode auth list 2>&1 | tee /tmp/list.out
grep -qi mistral /tmp/list.out && echo BOX_B_SEES_CRED
`, key)}
	out, err = exec.Command(string(r.Engine()), runner.RunArgs(paramsB)...).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "BOX_B_SEES_CRED") {
		t.Fatalf("box B does not see box A's credential: %v\n%s", err, out)
	}
}
