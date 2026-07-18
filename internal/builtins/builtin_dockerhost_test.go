package builtins

import (
	"os"
	"os/exec"
	"path/filepath"
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
	gb := gen.SkillBlock{Name: block.Name, Apt: block.Apt, NpmGlobal: block.NpmGlobal}
	for _, sf := range block.Files {
		if gb.Files == nil {
			gb.Files = map[string]string{}
		}
		gb.Files["skills/byre/docker-host/"+sf.Rel] = sf.Dest
	}
	gb.Dockerfile = block.Dockerfile
	full := gen.Dockerfile(gen.Input{Base: "debian:bookworm", Skills: []gen.SkillBlock{gb}})
	const wantSection = `# skill: byre/docker-host
RUN apt-get update \
 && apt-get install -y --no-install-recommends 'ca-certificates' 'curl' \
 && rm -rf /var/lib/apt/lists/*
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
	if !strings.Contains(full, wantSection) {
		start := strings.Index(full, "# skill: byre/docker-host")
		got := full
		if start >= 0 {
			got = full[start:]
		}
		t.Errorf("docker-host generated block drifted from golden.\n--- want ---\n%s\n--- got ---\n%s", wantSection, got)
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
	if !strings.Contains(ctx, "prune") {
		t.Errorf("context missing prune guidance:\n%s", ctx)
	}
	if !strings.Contains(ctx, "foreign") && !strings.Contains(ctx, "byre-machine") {
		t.Errorf("context missing foreign-volume guidance:\n%s", ctx)
	}
}

// TestDockerHostComposeEnvHook pins the env.d script: defaults
// COMPOSE_PROJECT_NAME from BYRE_WORKTREE and respects an existing override.
func TestDockerHostComposeEnvHook(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "docker-host"), "env.sh")
	// Default from BYRE_WORKTREE.
	cmd := exec.Command("sh", "-c", `. "`+hook+`" && printf '%s' "$COMPOSE_PROJECT_NAME"`)
	cmd.Env = append(os.Environ(), "BYRE_WORKTREE=wt-abc", "BYRE_PROJECT=proj")
	// Clear any inherited COMPOSE_PROJECT_NAME.
	var cleaned []string
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "COMPOSE_PROJECT_NAME=") {
			continue
		}
		cleaned = append(cleaned, e)
	}
	cmd.Env = cleaned
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "byre-wt-abc" {
		t.Errorf("COMPOSE_PROJECT_NAME = %q, want byre-wt-abc", out)
	}
	// User override respected.
	cmd2 := exec.Command("sh", "-c", `. "`+hook+`" && printf '%s' "$COMPOSE_PROJECT_NAME"`)
	cmd2.Env = append(cleaned, "BYRE_WORKTREE=wt-abc", "COMPOSE_PROJECT_NAME=custom")
	out2, err := cmd2.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out2) != "custom" {
		t.Errorf("override lost: %q", out2)
	}
	// Distinct worktrees -> distinct names (the D-M2 race).
	cmd3 := exec.Command("sh", "-c", `. "`+hook+`" && printf '%s' "$COMPOSE_PROJECT_NAME"`)
	cmd3.Env = append(cleaned, "BYRE_WORKTREE=wt-other")
	out3, _ := cmd3.Output()
	if string(out3) == string(out) {
		t.Errorf("worktrees must not share COMPOSE_PROJECT_NAME: both %q", out)
	}
}
