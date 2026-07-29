package builtins

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/skills"
)

// TestDockerHostSkillResolves pins the shipped docker-host skill: parse,
// sock_groups + containment, socket mount, empty egress, env.d compose hook,
// apt-repo dockerfile lines, and context snippet.
func TestDockerHostSkillResolves(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Skills: []string{"docker-host"}}, cat)
	if err != nil {
		t.Fatalf("docker-host resolve: %v", err)
	}
	// Mount + sock_groups + containment.
	sgs := res.SockGroups()
	if len(sgs) != 1 || sgs[0].Path != "/var/run/docker.sock" {
		t.Fatalf("sock_groups: %+v", sgs)
	}
	cs := res.Containments()
	if len(cs) != 1 || !strings.Contains(cs[0].Text, "containment hole") {
		t.Fatalf("containment: %+v", cs)
	}
	ms := res.Mounts()
	if len(ms) != 1 || ms[0].Target != "/var/run/docker.sock" || ms[0].Mode != "rw" {
		t.Fatalf("mounts: %+v", ms)
	}
	// egress = [] -- zero doors.
	if len(res.Egress()) != 0 {
		t.Fatalf("egress should be empty: %v", res.Egress())
	}
	// Build block rendered through gen: a GOLDEN, not substring greps against
	// the skill's own text. This pins the apt-repo RUN's line ordering and `\`
	// continuations AND the COPY placement of the env.d hook relative to the
	// RUN -- the drift a substring check is blind to.
	var block skills.BuildBlock
	for _, b := range res.BuildBlocks() {
		if b.Name == "byre/docker-host" {
			block = b
		}
	}
	gb := gen.SkillBlock{Name: block.Name, Apt: block.Apt}
	for _, sf := range block.Files {
		if gb.Files == nil {
			gb.Files = map[string]string{}
		}
		gb.Files["skills/byre/docker-host/"+sf.Rel] = sf.Dest
	}
	gb.Dockerfile = block.Dockerfile
	full := gen.Dockerfile(gen.Input{Base: "debian:bookworm", Skills: []gen.SkillBlock{gb}})
	// The declarative apt (curl + ca-certificates, used by the repo RUN below)
	// emits in the hoisted section ahead of the block (ADR 0042).
	const wantApt = `# skill: byre/docker-host
RUN apt-get update \
 && apt-get install -y --no-install-recommends 'ca-certificates' 'curl' \
 && rm -rf /var/lib/apt/lists/*
`
	const wantSection = `# skill: byre/docker-host
COPY "skills/byre/docker-host/env.sh" "/etc/byre/env.d/50-docker-host.sh"
RUN . /etc/os-release \
 && install -m 0755 -d /etc/apt/keyrings \
 && curl -fsSL "https://download.docker.com/linux/${ID}/gpg" -o /etc/apt/keyrings/docker.asc \
 && chmod a+r /etc/apt/keyrings/docker.asc \
 && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/${ID} ${VERSION_CODENAME} stable" > /etc/apt/sources.list.d/docker.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends docker-ce-cli docker-compose-plugin docker-buildx-plugin \
 && rm -rf /var/lib/apt/lists/*
`
	ai := strings.Index(full, wantApt)
	si := strings.Index(full, wantSection)
	if ai < 0 || si < 0 || ai > si {
		start := strings.Index(full, "# skill: byre/docker-host")
		got := full
		if start >= 0 {
			got = full[start:]
		}
		t.Errorf("docker-host generated output drifted from golden (apt=%d block=%d; apt must precede the block).\n--- want apt ---\n%s--- want block ---\n%s\n--- got ---\n%s", ai, si, wantApt, wantSection, got)
	}
	// Agent context against the accident class.
	ctx := res.Context()
	if !strings.Contains(ctx, "host state that outlives this box") {
		t.Errorf("context missing the host-daemon warning:\n%s", ctx)
	}
	if !strings.Contains(ctx, "docker system prune") {
		t.Errorf("context missing the prune prohibition:\n%s", ctx)
	}
	if !strings.Contains(ctx, "COMPOSE_PROJECT_NAME") {
		t.Errorf("context missing COMPOSE_PROJECT_NAME:\n%s", ctx)
	}
	if !strings.Contains(ctx, "foreign") && !strings.Contains(ctx, "byre-machine") {
		t.Errorf("context missing foreign-volume guidance:\n%s", ctx)
	}
}

// cleanHookEnv is the environment an env.d hook is sourced under in these
// tests: PATH (the hook's helpers must resolve) plus exactly what the case
// hands it. Inherited agent credentials are absent by construction -- a
// CLAUDE_CODE_OAUTH_TOKEN from the box running the suite would otherwise sit
// in both baseline and final and quietly widen what "unchanged" means.
func cleanHookEnv(extra ...string) []string {
	return append([]string{"PATH=" + os.Getenv("PATH")}, extra...)
}

// sourceEnvHook sources an env.d hook the way the launcher and /etc/profile.d
// do, and reports EVERYTHING observable it left behind: stdout, stderr, and
// the exported environment. hook == "" runs the same shell with the same env
// and sources nothing -- the BASELINE, so a caller can subtract it and see the
// delta in both directions (a var the hook set, and one it deliberately
// unset). ADR 0028's contract is that the delta is all there is.
func sourceEnvHook(t *testing.T, hook string, env []string) (stdout, stderr string, exported map[string]string) {
	t.Helper()
	envOut := filepath.Join(t.TempDir(), "env.out")
	script := `env -0 >"$2"`
	if hook != "" {
		script = `. "$1"; ` + script
	}
	cmd := exec.Command("bash", "-c", script, "bash", hook, envOut)
	cmd.Env = env
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("sourcing %q failed: %v (stdout=%q stderr=%q)", hook, err, out.String(), errOut.String())
	}
	b, err := os.ReadFile(envOut)
	if err != nil {
		t.Fatalf("hook terminated the sourcing shell before it could report its env: %v", err)
	}
	exported = map[string]string{}
	for _, kv := range strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00") {
		k, v, ok := strings.Cut(kv, "=")
		// `_` is bash's own last-argument variable (here: env's path), set by
		// the shell for every command and identical across both runs; it is
		// noise in the delta, never a hook's doing.
		if !ok || k == "_" {
			continue
		}
		exported[k] = v
	}
	return out.String(), errOut.String(), exported
}

// envDelta is what changed between a baseline environment and one produced by
// sourcing a hook: keys added or altered, and keys the hook removed.
func envDelta(baseline, final map[string]string) (changed map[string]string, removed []string) {
	changed = map[string]string{}
	for k, v := range final {
		if bv, ok := baseline[k]; !ok || bv != v {
			changed[k] = v
		}
	}
	for k := range baseline {
		if _, ok := final[k]; !ok {
			removed = append(removed, k)
		}
	}
	sort.Strings(removed)
	return changed, removed
}

// TestDockerHostComposeEnvHookIsPure pins the env.d script on both axes.
// BEHAVIOR: COMPOSE_PROJECT_NAME defaults from BYRE_WORKTREE, respects an
// existing override, and differs per worktree. PURITY (ADR 0028): the
// exported environment is the hook's ONLY lasting effect -- no output on
// stdout or stderr, and the env delta is exactly the one variable. The
// contract's filesystem clause holds vacuously here: this hook declares no
// filesystem inputs at all (it reads only BYRE_WORKTREE), so there is nothing
// for it to leave modified.
//
// Partial arm, per the ADR: this exercises the hook's known branches, it does
// not prove the absence of every possible external effect.
func TestDockerHostComposeEnvHookIsPure(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "docker-host"), "env.sh")

	// Default from BYRE_WORKTREE, measured against a baseline of the same shell.
	env := cleanHookEnv("BYRE_WORKTREE=wt-abc", "BYRE_PROJECT=proj")
	_, _, baseline := sourceEnvHook(t, "", env)
	stdout, stderr, final := sourceEnvHook(t, hook, env)
	if stdout != "" || stderr != "" {
		t.Errorf("env.d hook must be silent: stdout=%q stderr=%q", stdout, stderr)
	}
	if final["COMPOSE_PROJECT_NAME"] != "byre-wt-abc" {
		t.Errorf("COMPOSE_PROJECT_NAME = %q, want byre-wt-abc", final["COMPOSE_PROJECT_NAME"])
	}
	changed, removed := envDelta(baseline, final)
	delete(changed, "COMPOSE_PROJECT_NAME")
	if len(changed) != 0 || len(removed) != 0 {
		t.Errorf("hook left more than its export behind: changed=%v removed=%v", changed, removed)
	}

	// User override respected -- and then the hook changes nothing at all.
	env2 := cleanHookEnv("BYRE_WORKTREE=wt-abc", "COMPOSE_PROJECT_NAME=custom")
	_, _, baseline2 := sourceEnvHook(t, "", env2)
	stdout, stderr, final2 := sourceEnvHook(t, hook, env2)
	if stdout != "" || stderr != "" {
		t.Errorf("override path must be silent: stdout=%q stderr=%q", stdout, stderr)
	}
	if final2["COMPOSE_PROJECT_NAME"] != "custom" {
		t.Errorf("override lost: %q", final2["COMPOSE_PROJECT_NAME"])
	}
	if changed2, removed2 := envDelta(baseline2, final2); len(changed2) != 0 || len(removed2) != 0 {
		t.Errorf("hook must be a no-op with an override set: changed=%v removed=%v", changed2, removed2)
	}

	// No BYRE_WORKTREE at all: nothing exported, nothing said.
	env3 := cleanHookEnv()
	_, _, baseline3 := sourceEnvHook(t, "", env3)
	stdout, stderr, final3 := sourceEnvHook(t, hook, env3)
	if stdout != "" || stderr != "" {
		t.Errorf("no-worktree path must be silent: stdout=%q stderr=%q", stdout, stderr)
	}
	if changed3, removed3 := envDelta(baseline3, final3); len(changed3) != 0 || len(removed3) != 0 {
		t.Errorf("without BYRE_WORKTREE the hook must do nothing: changed=%v removed=%v", changed3, removed3)
	}

	// Distinct worktrees -> distinct names (the D-M2 race).
	_, _, other := sourceEnvHook(t, hook, cleanHookEnv("BYRE_WORKTREE=wt-other"))
	if other["COMPOSE_PROJECT_NAME"] == final["COMPOSE_PROJECT_NAME"] {
		t.Errorf("worktrees must not share COMPOSE_PROJECT_NAME: both %q", other["COMPOSE_PROJECT_NAME"])
	}
}
