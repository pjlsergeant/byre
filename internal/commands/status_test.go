package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/skills"
)

func TestRenderStatusFull(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{
		Agent:     "claude",
		Engine:    "docker",
		Canonical: "/home/me/proj",
		Skills:    []string{"moarcode"},
		Binds: []config.Mount{
			{Host: "/data", Target: "/data", Mode: "ro"},
			{Host: "/media", Target: "/media", Mode: "rw", Disabled: true},
		},
		Ports: []config.Port{
			{Container: 8080, Host: 8080},
			{Container: 3000}, // blank host = mirror the container port
		},
		Volumes: []config.Volume{
			{Name: "creds", Role: "state"},
			{Name: "node_modules", Role: "cache"},
		},
		RunArgs:   []string{"--cap-add=SYS_PTRACE"},
		Container: "abcdef0123456789",
	})
	out := b.String()

	assertRow(t, out, "Agent", "claude")
	assertRow(t, out, "Engine", "docker")
	assertRow(t, out, "Project", "/home/me/proj -> /workspace  (rw)")
	assertRow(t, out, "Network", "open")
	assertRow(t, out, "Ports", "127.0.0.1:8080 -> 8080  (host -> container)")
	assertRow(t, out, "Ports", "127.0.0.1:3000 -> 3000  (host -> container)")
	assertRow(t, out, "Host mounts", "/data -> /data  (ro)")
	assertRow(t, out, "Host mounts", "/media -> /media  (rw, disabled)")
	assertRow(t, out, "Skills", "moarcode")
	assertRow(t, out, "State vols", "creds")
	assertRow(t, out, "Cache vols", "node_modules")
	assertRow(t, out, "Raw run args", "--cap-add=SYS_PTRACE   (passed through; not introspected)")
	assertRow(t, out, "Container", "running (abcdef012345)") // short id
}

func TestRenderStatusGrantsAndRawBuild(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{
		Engine:    "docker",
		Canonical: "/p",
		Skills:    []string{"shem"},
		Grants: []skills.Grant{{
			Skill:  "shem",
			Mounts: []config.Mount{{Host: "/var/run/x.sock", Target: "/run/x.sock", Mode: "rw"}},
			Caps:   []string{"SYS_PTRACE"},
		}},
		BuildRaw: []string{"RUN echo hi"},
	})
	out := b.String()
	assertRow(t, out, "Skill grants", "shem: mounts /var/run/x.sock -> /run/x.sock (rw); +cap SYS_PTRACE")
	assertRow(t, out, "Raw build", "RUN echo hi")
	assertRow(t, out, "Raw build", "(raw build lines above are passed through; not introspected)")
}

// statusRows parses renderStatus's "Label:       value" rows into
// label -> values, folding continuation rows (blank label) into the most
// recent label — so an assertion can prove a value sits on the row it
// belongs to, not merely somewhere in the output.
func statusRows(out string) map[string][]string {
	rows := map[string][]string{}
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, " ") { // blank label: continuation row
			rows[cur] = append(rows[cur], strings.TrimLeft(line, " "))
			continue
		}
		head, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue // free-form text, not a labeled row
		}
		cur = head
		rows[cur] = append(rows[cur], strings.TrimLeft(rest, " "))
	}
	return rows
}

// assertRow requires the labeled row (or one of its continuation rows) to
// carry exactly want as its value — column padding stays presentation, the
// row's complete content is the contract.
func assertRow(t *testing.T, out, label, want string) {
	t.Helper()
	vals := statusRows(out)[label]
	for _, v := range vals {
		if v == want {
			return
		}
	}
	t.Errorf("status row %q: %q missing value %q\nfull output:\n%s", label, vals, want, out)
}

func TestRenderStatusEmptyAndNoEngine(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{
		Engine:    "auto",
		Canonical: "/p",
		EngineErr: "no container engine found on PATH",
	})
	out := b.String()
	assertRow(t, out, "Agent", "(none)")
	assertRow(t, out, "Host mounts", "none")
	assertRow(t, out, "Container", "unknown (no engine)")
}

// An orphaned box (running, byre client dead) must say so and give both
// routes out — reach it (byre shell) or stop it (the engine command). A
// plain running session keeps the plain line.
func TestRenderStatusOrphanedContainer(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{
		Engine:    "docker",
		Canonical: "/p",
		Container: "deadbeefcafe4567",
		Orphaned:  true,
	})
	out := b.String()
	for _, want := range []string{"orphaned", "byre shell", "docker stop deadbeefcafe"} {
		if !strings.Contains(out, want) {
			t.Errorf("orphaned container line missing %q: %s", want, out)
		}
	}
	b.Reset()
	renderStatus(&b, statusInfo{Engine: "docker", Canonical: "/p", Container: "deadbeefcafe4567"})
	if strings.Contains(b.String(), "orphaned") {
		t.Errorf("plain running session must not read as orphaned: %s", b.String())
	}
}

func TestRenderStatusRootlessPodman(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{Engine: "podman", Canonical: "/p", Rootless: true})
	out := b.String()
	if !strings.Contains(out, "rootless") || !strings.Contains(out, "UNSUPPORTED") {
		t.Errorf("rootless Podman not flagged on the Engine row: %s", out)
	}
}

func TestNetworkLine(t *testing.T) {
	cases := []struct {
		name string
		info statusInfo
		want string
	}{
		{"default open", statusInfo{}, "open"},
		{"clean posture", statusInfo{NetPosture: "deny-by-default", NetPostureSkill: "firewall"},
			"deny-by-default  (skill: firewall)"},
		{"project run_args degrade", statusInfo{NetPosture: "deny-by-default", NetPostureSkill: "firewall", ProjectRunArgs: true},
			"deny-by-default  (declared; raw run_args present — not guaranteed)"},
		{"raw build lines degrade", statusInfo{NetPosture: "deny-by-default", NetPostureSkill: "firewall", BuildRaw: []string{"RUN x"}},
			"deny-by-default  (declared; raw build lines present — not guaranteed)"},
		{"both degrade", statusInfo{NetPosture: "deny-by-default", NetPostureSkill: "firewall", ProjectRunArgs: true, BuildRaw: []string{"RUN x"}},
			"deny-by-default  (declared; raw run_args + raw build lines present — not guaranteed)"},
		// A mount/volume over a byre-managed path is disclosed in the
		// Containment register, not folded into this claim (ADR 0052).
		{"a managed-path shadow leaves the claim describing what byre built",
			statusInfo{NetPosture: "deny-by-default", NetPostureSkill: "firewall",
				ManagedShadows: []ManagedPathShadow{{Target: gen.ByreDir, Source: shadowFromConfig}}},
			"deny-by-default  (skill: firewall)"},
		{"unresolved skills", statusInfo{SkillErr: "boom", NetPosture: ""},
			"unknown  (skills unresolved)"},
		{"open-denylist composes the blocked count", statusInfo{NetPosture: "open-denylist", NetPostureSkill: "firewall-open",
			EgressClosed: []string{"statsig.anthropic.com", "telemetry.example.com:443"}},
			"open-denylist (open network, 2 hosts blocked)  (skill: firewall-open)"},
		{"open-denylist singular", statusInfo{NetPosture: "open-denylist", NetPostureSkill: "firewall-open",
			EgressClosed: []string{"statsig.anthropic.com"}},
			"open-denylist (open network, 1 host blocked)  (skill: firewall-open)"},
		{"open-denylist degrades like any posture", statusInfo{NetPosture: "open-denylist", NetPostureSkill: "firewall-open",
			EgressClosed: []string{"statsig.anthropic.com"}, ProjectRunArgs: true},
			"open-denylist (open network, 1 host blocked)  (declared; raw run_args present — not guaranteed)"},
	}
	for _, c := range cases {
		if got := networkLine(c.info); got != c.want {
			t.Errorf("%s: networkLine = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRenderStatusEgressSection(t *testing.T) {
	var buf strings.Builder
	renderStatus(&buf, statusInfo{
		Agent:           "claude",
		NetPosture:      "deny-by-default",
		NetPostureSkill: "firewall",
		Egress: []skills.EgressAllow{
			{Skill: "claude", Host: "api.anthropic.com", Port: 443},
			{Skill: "firewall", Host: "deb.debian.org", Port: 80},
			{Skill: "claude", Host: "api.anthropic.com", Port: 443}, // dup, must collapse
		},
	})
	out := buf.String()
	if !strings.Contains(out, "Egress:") {
		t.Fatalf("expected an Egress section when a posture is declared:\n%s", out)
	}
	if !strings.Contains(out, "api.anthropic.com:443  (claude)") {
		t.Errorf("egress entry not attributed to its skill:\n%s", out)
	}
	if !strings.Contains(out, "deb.debian.org:80  (firewall)") {
		t.Errorf("port-scoped base entry missing:\n%s", out)
	}
	if strings.Count(out, "api.anthropic.com:443") != 1 {
		t.Errorf("duplicate host:port must collapse to one row:\n%s", out)
	}
}

// Config [env] renders (keys only) and a skill-set reserved BYRE_* key is
// never silent: its own attributed row, plus a degradation of EVERY claim
// it can skew -- network for the gate/egress knobs, context and MCP
// delivery for theirs. Rendering the key somewhere is not enough; the
// affected claim lines themselves must stop asserting.
func TestRenderStatusEnvAndReservedOverrides(t *testing.T) {
	var buf strings.Builder
	renderStatus(&buf, statusInfo{
		Agent:            "claude",
		AgentMCP:         "inject",
		AgentContext:     "inject",
		NetPosture:       "deny-by-default",
		NetPostureSkill:  "firewall",
		EnvKeys:          []string{"CGO_ENABLED", "FOO"},
		MCPs:             []skills.MCPDecl{{Skill: "cfg", MCP: config.MCP{Name: "ctx7", URL: "https://x/mcp"}}},
		Contexts:         []config.ContextDecl{{Name: "ops", Text: "x"}},
		SkillReservedEnv: []skills.ReservedEnvSet{{Skill: "evil", Key: "BYRE_LAUNCH_GATE_FILE"}, {Skill: "evil", Key: "BYRE_CONTEXT_DIR"}, {Skill: "evil", Key: "BYRE_MCP_CONFIG"}},
	})
	out := buf.String()
	if !strings.Contains(out, "Env:") || !strings.Contains(out, "CGO_ENABLED, FOO") {
		t.Errorf("config [env] keys must render:\n%s", out)
	}
	if !strings.Contains(out, "evil sets BYRE_LAUNCH_GATE_FILE") {
		t.Errorf("reserved override must be attributed to its skill and key:\n%s", out)
	}
	if !strings.Contains(out, "a skill setting byre's network controls") {
		t.Errorf("network claim must degrade under a reserved gate/egress override:\n%s", out)
	}
	if !strings.Contains(out, "context") || !strings.Contains(out, "delivery not warranted") {
		t.Errorf("context delivery must stop asserting under BYRE_CONTEXT_DIR:\n%s", out)
	}
	if strings.Contains(out, "the agent session receives") {
		t.Errorf("MCP delivery must stop asserting under BYRE_MCP_CONFIG:\n%s", out)
	}
}

// The Host env row reads outcomes, not intentions: a source that resolved
// empty says NOT passed instead of posing as a delivered grant (the
// review's headline finding -- an empty host git identity rendered as
// `<- git:user.email` while nothing reached the box).
func TestRenderStatusHostEnvOutcomes(t *testing.T) {
	var buf strings.Builder
	renderStatus(&buf, statusInfo{
		Agent: "claude",
		HostEnv: []hostEnvResult{
			{Key: "GIT_AUTHOR_EMAIL", Source: "git:user.email", State: hostEnvEmpty},
			{Key: "TERM", Source: "env:TERM", Value: "xterm", State: hostEnvDelivered},
			{Key: "TZ", Source: "tz:", State: hostEnvDisabled},
			{Key: "GIT_AUTHOR_NAME", Source: "git:user.name", State: hostEnvOverridden},
		},
	})
	out := buf.String()
	if !strings.Contains(out, "GIT_AUTHOR_EMAIL <- git:user.email (NOT passed — source resolved empty)") {
		t.Errorf("empty source must render as not passed:\n%s", out)
	}
	if !strings.Contains(out, "TERM <- env:TERM") {
		t.Errorf("delivered source renders plainly:\n%s", out)
	}
	if strings.Contains(out, "TZ <-") {
		t.Errorf("disabled source must be omitted:\n%s", out)
	}
	if !strings.Contains(out, "GIT_AUTHOR_NAME (passthrough overridden by [env] GIT_AUTHOR_NAME)") {
		t.Errorf("override must be named:\n%s", out)
	}
}

// Byre's baked artifacts (mcp.json, the agent context file, the
// claude-skills dir) are emitted BEFORE the project block and are not
// tail-guarded, so a project files entry overwrites them in the image --
// every delivery claim reading from one must stop asserting (review
// finding: the registry's Files entry originally overstated coverage).
func TestRenderStatusArtifactShadowsDegradeDelivery(t *testing.T) {
	var buf strings.Builder
	renderStatus(&buf, statusInfo{
		Agent:             "claude",
		AgentMCP:          "inject",
		AgentContext:      "inject",
		AgentClaudeSkills: "inject",
		MCPs:              []skills.MCPDecl{{Skill: "cfg", MCP: config.MCP{Name: "ctx7", URL: "https://x/mcp"}}},
		Contexts:          []config.ContextDecl{{Name: "ops", Text: "x"}},
		ClaudeSkills:      []skills.ClaudeSkillDecl{{Skill: skills.ClaudeSkillsFromConfig, CS: config.ClaudeSkill{Name: "s", Path: "p"}}},
		ArtifactShadows: map[string]bool{
			gen.MCPConfigPath:                   true,
			"/etc/byre/" + gen.AgentContextName: true,
			gen.ClaudeSkillsPath:                true,
		},
	})
	out := buf.String()
	if strings.Count(out, "a project files entry overwrites") != 3 {
		t.Errorf("all three artifact-backed delivery lines must degrade:\n%s", out)
	}
	if strings.Contains(out, "the agent session receives") || strings.Contains(out, "injects the baked text") {
		t.Errorf("no delivery line may keep asserting over a shadowed artifact:\n%s", out)
	}
}

// artifactShadows matches the destination Docker actually writes: the
// file form, the dir form (dest dir + source basename), and anything
// landing inside the claude-skills dir.
func TestArtifactShadowsMatching(t *testing.T) {
	hits := artifactShadows(config.Config{Files: map[string]string{
		"mcp.json":  "/etc/byre/",                         // dir form -> /etc/byre/mcp.json
		"SKILL.md":  gen.ClaudeSkillsPath + "/x/SKILL.md", // inside the skills dir
		"README.md": "/workspace/doc/README.md",           // innocent
	}})
	if !hits[gen.MCPConfigPath] || !hits[gen.ClaudeSkillsPath] {
		t.Errorf("expected mcp.json (dir form) and claude-skills (subpath) hits: %v", hits)
	}
	if len(hits) != 2 {
		t.Errorf("innocent files must not hit: %v", hits)
	}
}

// Without reserved overrides the new rows stay absent and the delivery
// claims assert normally -- the hedges are override-gated, not ambient.
func TestRenderStatusNoReservedOverrideNoHedge(t *testing.T) {
	var buf strings.Builder
	renderStatus(&buf, statusInfo{
		Agent:           "claude",
		AgentContext:    "inject",
		NetPosture:      "deny-by-default",
		NetPostureSkill: "firewall",
		Contexts:        []config.ContextDecl{{Name: "ops", Text: "x"}},
	})
	out := buf.String()
	if strings.Contains(out, "Reserved env") || strings.Contains(out, "Env:") {
		t.Errorf("no reserved/env rows without content:\n%s", out)
	}
	if !strings.Contains(out, "the agent command injects the baked text") {
		t.Errorf("context delivery must assert normally with no override:\n%s", out)
	}
}

func TestRenderStatusNoEgressWithoutPosture(t *testing.T) {
	var buf strings.Builder
	// Agent skills declare egress even with no firewall; without a posture in
	// effect, status must NOT imply an allowlist is enforced.
	renderStatus(&buf, statusInfo{
		Agent:  "claude",
		Egress: []skills.EgressAllow{{Skill: "claude", Host: "api.anthropic.com", Port: 443}},
	})
	if strings.Contains(buf.String(), "Egress:") {
		t.Errorf("no Egress section when the network is open:\n%s", buf.String())
	}
}

func TestRenderStatusConfigEgressShownUnenforced(t *testing.T) {
	var buf strings.Builder
	// The user's own `egress` config entries are latent grants: with no
	// posture they still print, marked unenforced (ADR 0019) — while skill
	// egress stays suppressed as noise on an open network.
	renderStatus(&buf, statusInfo{
		Agent: "claude",
		Egress: []skills.EgressAllow{
			{Skill: "claude", Host: "api.anthropic.com", Port: 443},
			{Skill: "config", Host: "grafana.com", Port: 443},
		},
	})
	out := buf.String()
	if !strings.Contains(out, "grafana.com:443") || !strings.Contains(out, "unenforced") {
		t.Errorf("config egress should print unenforced without a posture:\n%s", out)
	}
	if strings.Contains(out, "api.anthropic.com") {
		t.Errorf("skill egress should stay suppressed without a posture:\n%s", out)
	}
	// With a posture, everything prints and nothing claims unenforced.
	buf.Reset()
	renderStatus(&buf, statusInfo{
		Agent:      "claude",
		NetPosture: "deny-by-default",
		Egress: []skills.EgressAllow{
			{Skill: "claude", Host: "api.anthropic.com", Port: 443},
			{Skill: "config", Host: "grafana.com", Port: 443},
		},
	})
	out = buf.String()
	if !strings.Contains(out, "api.anthropic.com:443") || strings.Contains(out, "unenforced") {
		t.Errorf("posture on: full list, no unenforced tag:\n%s", out)
	}
}

func TestRenderStatusClosures(t *testing.T) {
	t.Run("deny-by-default: skill entry shown closed-by, not vanished", func(t *testing.T) {
		var buf strings.Builder
		renderStatus(&buf, statusInfo{
			Agent:           "claude",
			NetPosture:      "deny-by-default",
			NetPostureSkill: "firewall",
			Egress: []skills.EgressAllow{
				{Skill: "claude", Host: "api.anthropic.com", Port: 443},
				{Skill: "claude", Host: "statsig.anthropic.com", Port: 443},
			},
			EgressClosed: []string{"statsig.anthropic.com"},
		})
		out := buf.String()
		if !strings.Contains(out, "statsig.anthropic.com:443  (claude — closed by config '!statsig.anthropic.com')") {
			t.Errorf("closed skill entry must print as closed-by, not vanish:\n%s", out)
		}
		if !strings.Contains(out, "api.anthropic.com:443  (claude)") {
			t.Errorf("unclosed entry must stay plain:\n%s", out)
		}
		if !strings.Contains(out, "Closed:") ||
			!strings.Contains(out, "statsig.anthropic.com (every port)  (config — removed from the allowlist)") {
			t.Errorf("closures must get their own attributed rows:\n%s", out)
		}
	})
	t.Run("open-denylist: allowlist suppressed, closures are the enforced list", func(t *testing.T) {
		var buf strings.Builder
		renderStatus(&buf, statusInfo{
			Agent:           "claude",
			NetPosture:      "open-denylist",
			NetPostureSkill: "firewall-open",
			Egress: []skills.EgressAllow{
				{Skill: "claude", Host: "api.anthropic.com", Port: 443},
				{Skill: "config", Host: "grafana.com", Port: 443},
			},
			EgressClosed: []string{"statsig.anthropic.com", "telemetry.example.com:443"},
		})
		out := buf.String()
		if strings.Contains(out, "api.anthropic.com") {
			t.Errorf("skill egress is meaningless noise on an open network:\n%s", out)
		}
		if !strings.Contains(out, "grafana.com:443  (config — unenforced, network open)") {
			t.Errorf("config egress prints unenforced under open-denylist:\n%s", out)
		}
		if !strings.Contains(out, "statsig.anthropic.com (every port)  (config — blocked; skill: firewall-open)") {
			t.Errorf("portless closure row wrong:\n%s", out)
		}
		if !strings.Contains(out, "telemetry.example.com:443  (config — blocked; skill: firewall-open)") {
			t.Errorf("ported closure row wrong:\n%s", out)
		}
	})
	t.Run("no posture: closures print inert, not invisible", func(t *testing.T) {
		var buf strings.Builder
		renderStatus(&buf, statusInfo{
			Agent:        "claude",
			EgressClosed: []string{"statsig.anthropic.com"},
		})
		out := buf.String()
		if !strings.Contains(out, "statsig.anthropic.com (every port)  (config — unenforced, network open)") {
			t.Errorf("closure with no posture must print inert:\n%s", out)
		}
	})
}

func TestConfigEgressAttributed(t *testing.T) {
	entries := configEgress([]string{"grafana.com", "internal:8443", "bad:99999"})
	if len(entries) != 2 {
		t.Fatalf("expected 2 valid entries (99999 dropped), got %+v", entries)
	}
	if entries[0].Host != "grafana.com" || entries[0].Port != 443 || entries[1].Port != 8443 {
		t.Errorf("entries parsed wrong: %+v", entries)
	}
	for _, e := range entries {
		if e.Skill != "config" {
			t.Errorf("egress entry not attributed to config: %+v", e)
		}
	}
}

func TestRenderStatusContainmentAndSockGroups(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{
		Engine:    "docker",
		Canonical: "/p",
		Skills:    []string{"docker-host"},
		Containments: []skills.ContainmentDecl{{
			Skill: "docker-host",
			Text:  "docker-host opens a containment hole -- skim docs/DOCKER-HOST.md",
		}},
		Grants: []skills.Grant{{
			Skill:      "docker-host",
			Mounts:     []config.Mount{{Host: "/var/run/docker.sock", Target: "/var/run/docker.sock", Mode: "rw"}},
			SockGroups: []string{"/var/run/docker.sock"},
		}},
	})
	out := b.String()
	assertRow(t, out, "Containment",
		"🛑 HOLE -- docker-host opens a containment hole -- skim docs/DOCKER-HOST.md  (skill: docker-host)")
	assertRow(t, out, "Skill grants",
		"docker-host: mounts /var/run/docker.sock -> /var/run/docker.sock (rw); "+
			"sock group access via /var/run/docker.sock (gid resolved at launch; wider than the named path)")
	// Network row must stay unqualified (warranty model: hole is separate).
	assertRow(t, out, "Network", "open")
}

func TestRenderStatusMultiContainment(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{
		Engine:    "docker",
		Canonical: "/p",
		Containments: []skills.ContainmentDecl{
			{Skill: "docker-host", Text: "hole A"},
			{Skill: "podman-host", Text: "hole B"},
		},
	})
	out := b.String()
	if !strings.Contains(out, "hole A") || !strings.Contains(out, "hole B") {
		t.Fatalf("multi-declarer not both shown:\n%s", out)
	}
	if !strings.Contains(out, "(skill: docker-host)") || !strings.Contains(out, "(skill: podman-host)") {
		t.Fatalf("both skills must be attributed:\n%s", out)
	}
}

// MCP rows are configuration reporting: what's wired, from where, consumed
// env verdicts, and the delivery line — never grant rows (carried egress
// rides the Egress section, attributed mcp:<name>).
func TestRenderStatusMCPRows(t *testing.T) {
	var buf strings.Builder
	renderStatus(&buf, statusInfo{
		Agent:    "byre/claude",
		AgentMCP: "inject",
		MCPs: []skills.MCPDecl{
			{Skill: skills.MCPFromConfig, MCP: config.MCP{Name: "github", Command: []string{"gh-mcp", "stdio"}, Env: []string{"GITHUB_TOKEN", "GH_HOST"}}},
			{Skill: "pete/tools", MCP: config.MCP{Name: "linear", URL: "https://mcp.linear.app/mcp"}},
		},
		EnvProvided: map[string]bool{"GITHUB_TOKEN": true},
	})
	out := buf.String()
	if !strings.Contains(out, "MCP servers:") {
		t.Fatalf("MCP section missing:\n%s", out)
	}
	if !strings.Contains(out, "github — local: gh-mcp stdio  (config; consumes GITHUB_TOKEN (provided), GH_HOST (NOT provided by this box))") {
		t.Errorf("local row wrong:\n%s", out)
	}
	if !strings.Contains(out, "linear — remote: https://mcp.linear.app/mcp  (skill pete/tools)") {
		t.Errorf("remote row wrong:\n%s", out)
	}
	if !strings.Contains(out, "the agent session receives: github, linear  (injected via /etc/byre/mcp.json)") {
		t.Errorf("inject delivery line wrong:\n%s", out)
	}
}

// A registrar-less agent degrades honestly: declared-but-NOT-delivered, with
// the baked path as the manual wiring point. No agent at all says where the
// file is too.
func TestRenderStatusMCPDeliveryDegrades(t *testing.T) {
	decl := []skills.MCPDecl{{Skill: skills.MCPFromConfig, MCP: config.MCP{Name: "github", Command: []string{"gh-mcp"}}}}
	var buf strings.Builder
	renderStatus(&buf, statusInfo{Agent: "byre/gemini", MCPs: decl})
	if out := buf.String(); !strings.Contains(out, "NOT delivered: agent skill byre/gemini has no MCP adapter") ||
		!strings.Contains(out, "/etc/byre/mcp.json") {
		t.Errorf("registrar-less degradation missing:\n%s", out)
	}
	buf.Reset()
	renderStatus(&buf, statusInfo{MCPs: decl})
	if out := buf.String(); !strings.Contains(out, "no agent selected") {
		t.Errorf("agentless line missing:\n%s", out)
	}
	// Unresolved skills: delivery is unknown, never asserted.
	buf.Reset()
	renderStatus(&buf, statusInfo{Agent: "byre/claude", MCPs: decl, SkillErr: "boom"})
	if out := buf.String(); !strings.Contains(out, "delivery unknown (skills unresolved)") {
		t.Errorf("unresolved delivery line missing:\n%s", out)
	}
}

// Endpoint-closed coupling renders ON the MCP row — but only where closures
// are enforced (an allowlist posture or open-denylist); on an open network
// the closure is inert and the row must not claim non-operation. Local
// servers with no declared egress get the outbound-unknown note under an
// allowlist posture.
func TestRenderStatusMCPEndpointClosedAndUnknownOutbound(t *testing.T) {
	mcps := []skills.MCPDecl{
		{Skill: skills.MCPFromConfig, MCP: config.MCP{Name: "linear", URL: "https://mcp.linear.app/mcp"}},
		{Skill: skills.MCPFromConfig, MCP: config.MCP{Name: "github", Command: []string{"gh-mcp"}}},
	}
	var buf strings.Builder
	renderStatus(&buf, statusInfo{
		Agent: "byre/claude", AgentMCP: "inject",
		NetPosture: "deny-by-default", NetPostureSkill: "firewall",
		EgressClosed: []string{"mcp.linear.app"},
		MCPs:         mcps,
	})
	out := buf.String()
	if !strings.Contains(out, "endpoint closed by config '!mcp.linear.app' — not operational") {
		t.Errorf("endpoint-closed note missing under allowlist posture:\n%s", out)
	}
	if !strings.Contains(out, "outbound unknown — under deny-by-default") {
		t.Errorf("local unknown-outbound note missing:\n%s", out)
	}

	// Open-denylist: the closure IS enforced (dropped), so the claim stands;
	// the unknown-outbound note does not (the network is open).
	buf.Reset()
	renderStatus(&buf, statusInfo{
		Agent: "byre/claude", AgentMCP: "inject",
		NetPosture: config.PostureOpenDenylist, NetPostureSkill: "firewall-open",
		EgressClosed: []string{"mcp.linear.app"},
		MCPs:         mcps,
	})
	out = buf.String()
	if !strings.Contains(out, "not operational") {
		t.Errorf("endpoint-closed note missing under open-denylist:\n%s", out)
	}
	if strings.Contains(out, "outbound unknown") {
		t.Errorf("unknown-outbound must not print on an open network:\n%s", out)
	}

	// Open network: closures are inert — no non-operational claim, no
	// unknown-outbound note.
	buf.Reset()
	renderStatus(&buf, statusInfo{
		Agent: "byre/claude", AgentMCP: "inject",
		EgressClosed: []string{"mcp.linear.app"},
		MCPs:         mcps,
	})
	out = buf.String()
	if strings.Contains(out, "not operational") || strings.Contains(out, "outbound unknown") {
		t.Errorf("inert closure must not claim non-operation on an open network:\n%s", out)
	}
}

// MCP `!name` closures render one row each, unconditionally — the declared
// set is byre's own construction, so the removal is always in effect.
func TestRenderStatusMCPClosedRows(t *testing.T) {
	var buf strings.Builder
	renderStatus(&buf, statusInfo{MCPClosed: []string{"telemetry"}})
	if out := buf.String(); !strings.Contains(out, "MCP closed:") ||
		!strings.Contains(out, "!telemetry  (config — removed from the declared set)") {
		t.Errorf("MCP closed row missing:\n%s", out)
	}
}

// Declared extra egress renders ON the MCP row, whatever the posture — on
// an open network the Egress section suppresses mcp:-attributed entries as
// noise, and without the row rendering the extras would be invisible teeth
// a later posture toggle arms.
func TestRenderStatusMCPExtrasAlwaysOnRow(t *testing.T) {
	var buf strings.Builder
	renderStatus(&buf, statusInfo{
		Agent: "byre/claude", AgentMCP: "inject",
		MCPs: []skills.MCPDecl{{Skill: skills.MCPFromConfig, MCP: config.MCP{
			Name: "linear", URL: "https://mcp.linear.app/mcp", Egress: []string{"auth.linear.app"},
		}}},
	})
	out := buf.String()
	if !strings.Contains(out, "+egress auth.linear.app") {
		t.Errorf("declared extras must render on the MCP row under an open network:\n%s", out)
	}
	if strings.Contains(out, "Egress:") {
		t.Errorf("the Egress section itself stays suppressed on an open network:\n%s", out)
	}
}

// Claude Skill rows are configuration reporting, the MCP posture: name,
// source spelling, attribution, one delivery verdict — never grant rows.
func TestRenderStatusClaudeSkillRows(t *testing.T) {
	var buf strings.Builder
	renderStatus(&buf, statusInfo{
		Agent:             "byre/claude",
		AgentClaudeSkills: "inject",
		ClaudeSkills: []skills.ClaudeSkillDecl{
			{Skill: skills.ClaudeSkillsFromConfig, CS: config.ClaudeSkill{Name: "tdd-loop", Path: "~/cs/tdd-loop"}},
			{Skill: "pete/tools", CS: config.ClaudeSkill{Name: "review-loop", From: "cs/review-loop"}, SrcDir: "/resolved"},
		},
		ClaudeSkillsClosed: []string{"legacy-thing"},
	})
	out := buf.String()
	if !strings.Contains(out, "Claude Skills:") {
		t.Fatalf("Claude Skills section missing:\n%s", out)
	}
	if !strings.Contains(out, "tdd-loop — ~/cs/tdd-loop  (config)") {
		t.Errorf("config row wrong:\n%s", out)
	}
	if !strings.Contains(out, "review-loop — cs/review-loop  (skill pete/tools)") {
		t.Errorf("skill row wrong:\n%s", out)
	}
	if !strings.Contains(out, "the agent session receives: /tdd-loop, /review-loop  (via /etc/byre/claude-skills") {
		t.Errorf("inject delivery line wrong:\n%s", out)
	}
	if !strings.Contains(out, "!legacy-thing  (config — removed from the declared set)") {
		t.Errorf("closure row missing:\n%s", out)
	}
}

// An adapter-less agent degrades honestly, with the baked path as the manual
// wiring point; no agent at all names the path too.
func TestRenderStatusClaudeSkillDeliveryDegrades(t *testing.T) {
	decl := []skills.ClaudeSkillDecl{{Skill: skills.ClaudeSkillsFromConfig, CS: config.ClaudeSkill{Name: "tdd-loop", Path: "/x"}}}
	var buf strings.Builder
	renderStatus(&buf, statusInfo{Agent: "byre/gemini", ClaudeSkills: decl})
	if out := buf.String(); !strings.Contains(out, "NOT delivered: agent skill byre/gemini has no claude-skills adapter") ||
		!strings.Contains(out, "/etc/byre/claude-skills") {
		t.Errorf("adapter-less degradation missing:\n%s", out)
	}
	buf.Reset()
	renderStatus(&buf, statusInfo{ClaudeSkills: decl})
	if out := buf.String(); !strings.Contains(out, "no agent selected") {
		t.Errorf("agentless line missing:\n%s", out)
	}
	buf.Reset()
	renderStatus(&buf, statusInfo{Agent: "byre/claude", AgentClaudeSkills: "inject", ClaudeSkills: decl, SkillErr: "boom"})
	if out := buf.String(); !strings.Contains(out, "delivery unknown (skills unresolved)") {
		t.Errorf("unresolved must not assert delivery:\n%s", out)
	}
}

// A mount or volume over a byre-managed path is detected whoever declared it
// (ADR 0052): byre has no runtime twin of the build-tail re-assertion that
// backs the trust `files` gets, so a skill's mount shadows exactly as hard as
// the project's own.
func TestManagedPathShadows(t *testing.T) {
	var fw skills.File
	fw.Runtime.NetnsInit = "/usr/local/bin/byre-firewall"
	res := skills.Resolved{Skills: []skills.Skill{{Name: "firewall", File: fw}}}

	// A volume over /etc/byre covers the managed directory (ancestor match); a
	// mount directly on the netns script covers it (equal match); a harmless
	// mount elsewhere doesn't. A DISABLED mount binds nothing, so it doesn't.
	cfg := config.Config{
		Volumes: []config.Volume{{Name: "v", Role: "state", Target: "/etc/byre"}},
		Mounts: []config.Mount{
			{Host: "~/x", Target: "/usr/local/bin/byre-firewall", Mode: "ro"},
			{Host: "~/y", Target: "/opt/data", Mode: "ro"},
			{Host: "~/z", Target: "/etc/byre/mcp.json", Mode: "rw", Disabled: true},
			// A bind INSIDE the managed directory replaces that one entry;
			// the launch gate is the sharp case (an empty gate fails open on
			// the next restart).
			{Host: "~/g", Target: gen.LaunchGatePath, Mode: "rw"},
		},
	}
	got := map[string]string{}
	for _, sh := range managedPathShadows(cfg, res) {
		got[sh.Target] = sh.Source
	}
	if got[gen.ByreDir] != shadowFromConfig {
		t.Errorf("a project volume over %s must be reported as a config shadow: %v", gen.ByreDir, got)
	}
	if got["/usr/local/bin/byre-firewall"] != shadowFromConfig {
		t.Errorf("a project mount on the netns script must be reported: %v", got)
	}
	if got[gen.LaunchGatePath] != shadowFromConfig {
		t.Errorf("a bind onto the launch gate itself must be reported: %v", got)
	}
	if _, ok := got["/opt/data"]; ok {
		t.Errorf("a harmless mount target must not be reported: %v", got)
	}
	if _, ok := got[gen.MCPConfigPath]; ok {
		t.Errorf("a disabled mount binds nothing and must not be reported: %v", got)
	}

	var b strings.Builder
	warnManagedPathShadows(&b, cfg, res)
	out := b.String()
	if !strings.Contains(out, "🛑") || !strings.Contains(out, "cannot re-assert over a runtime mount") ||
		!strings.Contains(out, gen.ByreDir) {
		t.Fatalf("expected a loud blanket shadow warning naming the target, got:\n%s", out)
	}
	if strings.Contains(out, "/opt/data") {
		t.Fatalf("a harmless mount target must not warn:\n%s", out)
	}

	// A SKILL's own mount/volume over a managed path is reported too, attributed
	// to the skill that declared it.
	var greedy skills.File
	greedy.Runtime.Mounts = []config.Mount{{Host: "~/g", Target: "/usr/local/bin", Mode: "rw"}}
	greedy.Volumes = []config.Volume{{Name: "gv", Role: "state", Target: gen.ClaudeSkillsPath}}
	resSkill := skills.Resolved{Skills: []skills.Skill{{Name: "greedy", File: greedy}}}
	skillHits := map[string]string{}
	for _, sh := range managedPathShadows(config.Config{}, resSkill) {
		skillHits[sh.Target] = sh.Source
	}
	if skillHits["/usr/local/bin"] != "skill greedy" {
		t.Errorf("a skill mount covering the launcher must be attributed to the skill: %v", skillHits)
	}
	if skillHits[gen.ClaudeSkillsPath] != "skill greedy" {
		t.Errorf("a skill volume over a baked artifact must be attributed to the skill: %v", skillHits)
	}

	// Every entry byre bakes under /etc/byre rides the directory, so no
	// individual artifact needs its own root: a volume there is one hit, not
	// one per artifact — and the dedup is by cleaned target.
	dup := config.Config{
		Mounts:  []config.Mount{{Host: "~/a", Target: "/etc/byre/", Mode: "rw"}},
		Volumes: []config.Volume{{Name: "v", Role: "state", Target: "/etc/./byre"}},
	}
	if h := managedPathShadows(dup, skills.Resolved{}); len(h) != 1 {
		t.Errorf("two spellings of one target must dedupe to one line, got %v", h)
	}

	// An UNCLEAN netns_init hook (absolute but not clean) must still match a mount
	// on its canonical form — both operands are cleaned before comparison.
	var fwU skills.File
	fwU.Runtime.NetnsInit = "/usr/local/lib/../bin/byre-firewall" // cleans to /usr/local/bin/byre-firewall
	resU := skills.Resolved{Skills: []skills.Skill{{Name: "firewall", File: fwU}}}
	cfgU := config.Config{Mounts: []config.Mount{{Host: "~/x", Target: "/usr/local/bin/byre-firewall", Mode: "ro"}}}
	if h := managedPathShadows(cfgU, resU); len(h) != 1 {
		t.Errorf("a mount on the canonical hook path must match an unclean hook, got %v", h)
	}

	// Nothing declared anywhere -> silence.
	if h := managedPathShadows(config.Config{}, skills.Resolved{}); len(h) != 0 {
		t.Errorf("no mounts/volumes -> no shadows, got %v", h)
	}
}

// The disclosure renders in status's Containment register beside the
// skill-declared holes, once per offending target, attributed.
func TestRenderStatusManagedShadowDisclosure(t *testing.T) {
	var b strings.Builder
	renderStatus(&b, statusInfo{
		Canonical: "/p", NetPosture: "deny-by-default", NetPostureSkill: "firewall",
		Containments: []skills.ContainmentDecl{{Skill: "docker-host", Text: "host daemon access"}},
		ManagedShadows: []ManagedPathShadow{
			{Target: gen.ByreDir, Source: shadowFromConfig},
			{Target: gen.LauncherPath, Source: "skill greedy"},
		},
	})
	rows := statusRows(b.String())
	got := strings.Join(rows["Containment"], "\n")
	if !strings.Contains(got, "host daemon access") {
		t.Errorf("the skill-declared hole must still render:\n%s", got)
	}
	for _, want := range []string{gen.ByreDir + " (" + shadowFromConfig + ")", gen.LauncherPath + " (skill greedy)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing attributed shadow line %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "cannot re-assert over a runtime mount") != 2 {
		t.Errorf("one disclosure line per offending target expected:\n%s", got)
	}
}

func TestWarnGuardCollisions(t *testing.T) {
	var fw skills.File
	fw.Runtime.NetnsInit = "/usr/local/bin/byre-firewall"
	res := skills.Resolved{Skills: []skills.Skill{{Name: "firewall", File: fw}}}

	// A project files clobber of the gate and the launcher — both warned.
	cfg := config.Config{Files: map[string]string{
		"empty":    gen.LaunchGatePath,
		"evil":     gen.LauncherPath,
		"harmless": "/opt/data.txt",
	}}
	var b strings.Builder
	warnGuardCollisions(&b, cfg, res)
	out := b.String()
	if !strings.Contains(out, gen.LaunchGatePath) || !strings.Contains(out, gen.LauncherPath) {
		t.Fatalf("expected warnings for gate and launcher, got:\n%s", out)
	}
	if strings.Contains(out, "/opt/data.txt") {
		t.Fatalf("harmless files dest should not warn:\n%s", out)
	}

	// No netns skill: the gate/firewall paths aren't guarded, but the launcher
	// always is — a launcher clobber still warns, a gate clobber does not.
	cfg2 := config.Config{Files: map[string]string{"g": gen.LaunchGatePath, "l": gen.LauncherPath}}
	var b2 strings.Builder
	warnGuardCollisions(&b2, cfg2, skills.Resolved{})
	out2 := b2.String()
	if !strings.Contains(out2, gen.LauncherPath) {
		t.Fatalf("launcher clobber should warn without a netns skill:\n%s", out2)
	}
	if strings.Contains(out2, gen.LaunchGatePath) {
		t.Fatalf("gate is not guarded without a netns skill; should not warn:\n%s", out2)
	}

	// Docker-equivalent dest forms that clobber a guarded path must still warn:
	// a "." segment, a "..", and a dir-form dest (trailing slash) that appends
	// the source basename. All resolve to /usr/local/bin/byre-launch or the gate.
	cfg3 := config.Config{Files: map[string]string{
		"a":               "/etc/byre/./launch-gate",
		"b":               "/usr/local/../local/bin/byre-launch",
		"sub/byre-launch": "/usr/local/bin/", // dir-form (slash): appends basename
	}}
	var b3 strings.Builder
	warnGuardCollisions(&b3, cfg3, res)
	out3 := b3.String()
	if !strings.Contains(out3, gen.LaunchGatePath) {
		t.Fatalf("'.'-segment gate clobber should warn:\n%s", out3)
	}
	if strings.Count(out3, gen.LauncherPath) != 1 {
		t.Fatalf("expected exactly one launcher warning (deduped) from the '..' and dir-form clobbers:\n%s", out3)
	}

	// Dir-form WITHOUT a trailing slash (Docker treats an existing image dir like
	// /usr/local/bin as a directory): source basename byre-launch lands at the
	// launcher path and must warn.
	var b4 strings.Builder
	warnGuardCollisions(&b4, config.Config{Files: map[string]string{"byre-launch": "/usr/local/bin"}}, res)
	if !strings.Contains(b4.String(), gen.LauncherPath) {
		t.Fatalf("no-slash dir-form clobber should warn:\n%s", b4.String())
	}
	// A non-matching basename into the same dir must NOT warn (no over-warning).
	var b5 strings.Builder
	warnGuardCollisions(&b5, config.Config{Files: map[string]string{"notes.txt": "/usr/local/bin"}}, res)
	if strings.Contains(b5.String(), gen.LauncherPath) {
		t.Fatalf("unrelated file into /usr/local/bin must not warn:\n%s", b5.String())
	}
}

// A found engine whose daemon won't answer is UNKNOWN, never "not running" —
// the lifecycle commands refuse in this state and status must not contradict
// them with a confident negative.
func TestStatusContainerUnknownWhenEngineQueryFails(t *testing.T) {
	var b strings.Builder
	renderStatus(&b, statusInfo{Engine: "docker", Canonical: "/p",
		ContainerQueryErr: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock"})
	rows := statusRows(b.String())
	if got := strings.Join(rows["Container"], " "); !strings.Contains(got, "unknown") || !strings.Contains(got, "Cannot connect") {
		t.Fatalf("Container row must be unknown with the engine error, got %q", got)
	}
	if strings.Contains(b.String(), "not running") {
		t.Fatal("a failed query must never render as 'not running'")
	}
}

func TestStatusSiblingQueryFailureIsReported(t *testing.T) {
	var b strings.Builder
	renderStatus(&b, statusInfo{Engine: "docker", Canonical: "/p",
		Container: "deadbeefcafe4567", SiblingQueryErr: "daemon timeout"})
	rows := statusRows(b.String())
	if got := strings.Join(rows["Worktrees"], " "); !strings.Contains(got, "unknown") || !strings.Contains(got, "daemon timeout") {
		t.Fatalf("Worktrees row must report the failed sibling query, got %q", got)
	}
}

// Standing instructions render as attributed wiring rows with the delivery
// verdict keyed off the context vouch (ADR 0046) — inject names the baked
// path; an adapter-less agent degrades honestly; no agent / unresolved
// skills degrade too. Status must never assert a delivery byre can't stand
// behind (PRINCIPLES.md §4).
func TestRenderStatusContextDelivery(t *testing.T) {
	decls := []config.ContextDecl{
		{Name: "house-rules", Text: "Run the linter.\nNever force-push.\n"},
		{Name: "conventions", File: "~/notes/conv.md"},
	}
	var buf strings.Builder
	renderStatus(&buf, statusInfo{Agent: "byre/claude", AgentContext: "inject", Contexts: decls})
	out := buf.String()
	if !strings.Contains(out, `house-rules  "Run the linter."  (+1 more lines)`) {
		t.Errorf("inline row wrong:\n%s", out)
	}
	if !strings.Contains(out, "conventions  (file: ~/notes/conv.md)") {
		t.Errorf("file row wrong:\n%s", out)
	}
	if !strings.Contains(out, "the agent command injects the baked text (/etc/byre/agent-context.md") {
		t.Errorf("inject verdict wrong:\n%s", out)
	}

	buf.Reset()
	renderStatus(&buf, statusInfo{Agent: "byre/some-agent", Contexts: decls})
	if out := buf.String(); !strings.Contains(out, "NOT delivered: agent skill byre/some-agent has no context adapter") ||
		!strings.Contains(out, "/etc/byre/agent-context.md") {
		t.Errorf("adapter-less degradation missing:\n%s", out)
	}

	buf.Reset()
	renderStatus(&buf, statusInfo{Contexts: decls})
	if out := buf.String(); !strings.Contains(out, "no agent selected") {
		t.Errorf("agent-less degradation missing:\n%s", out)
	}

	buf.Reset()
	renderStatus(&buf, statusInfo{Agent: "byre/claude", AgentContext: "inject", Contexts: decls, SkillErr: "broken skill"})
	if out := buf.String(); !strings.Contains(out, "delivery unknown (skills unresolved)") {
		t.Errorf("unresolved degradation missing:\n%s", out)
	}

	// No declarations = no Instructions rows, no verdict.
	buf.Reset()
	renderStatus(&buf, statusInfo{Agent: "byre/claude", AgentContext: "inject"})
	if out := buf.String(); strings.Contains(out, "Instructions") {
		t.Errorf("empty set must render nothing:\n%s", out)
	}
}

// The baked artifacts are emitted BEFORE the project block and are
// deliberately not tail-guarded (they are wiring, not the security guard's
// charge), so a `files` entry targeting one WINS -- the opposite direction
// from a guarded path, and the note must say so rather than reusing the
// guard's wording.
func TestWarnGuardCollisionsNotesArtifactShadows(t *testing.T) {
	var b strings.Builder
	warnGuardCollisions(&b, config.Config{Files: map[string]string{
		"mcp.json": "/etc/byre/",
	}}, skills.Resolved{})
	out := b.String()
	if !strings.Contains(out, gen.MCPConfigPath) || !strings.Contains(out, "your file wins") {
		t.Errorf("a files entry over a baked artifact must warn that YOUR file wins:\n%s", out)
	}
	var clean strings.Builder
	warnGuardCollisions(&clean, config.Config{Files: map[string]string{"notes.md": "/workspace/doc/notes.md"}}, skills.Resolved{})
	if clean.String() != "" {
		t.Errorf("an innocent files entry must warn nothing:\n%s", clean.String())
	}
}
