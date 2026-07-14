package skills

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

func mkSkill(name string, mcps ...config.MCP) Skill {
	return Skill{Name: name, File: File{MCPs: mcps}}
}

func TestMCPSetUnionAndAttribution(t *testing.T) {
	cfg := config.Config{MCPs: []config.MCP{{Name: "github", Command: []string{"gh-mcp"}}}}
	r := Resolved{Skills: []Skill{mkSkill("pete/tools", config.MCP{Name: "linear", URL: "https://mcp.linear.app/mcp"})}}
	set, err := MCPSet(cfg, r)
	if err != nil {
		t.Fatalf("MCPSet: %v", err)
	}
	if len(set) != 2 {
		t.Fatalf("set = %+v", set)
	}
	if set[0].Skill != MCPFromConfig || set[0].MCP.Name != "github" {
		t.Fatalf("config attribution: %+v", set[0])
	}
	if set[1].Skill != "pete/tools" || set[1].MCP.Name != "linear" {
		t.Fatalf("skill attribution: %+v", set[1])
	}
}

func TestMCPSetDuplicateHardReject(t *testing.T) {
	cfg := config.Config{MCPs: []config.MCP{{Name: "github", Command: []string{"a"}}}}
	r := Resolved{Skills: []Skill{mkSkill("pete/tools", config.MCP{Name: "github", Command: []string{"b"}})}}
	_, err := MCPSet(cfg, r)
	if err == nil || !strings.Contains(err.Error(), "declared by both the config and skill \"pete/tools\"") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "!github") {
		t.Fatalf("remedy must name the closure: %v", err)
	}

	// skill + skill collide too.
	r2 := Resolved{Skills: []Skill{
		mkSkill("a/x", config.MCP{Name: "github", Command: []string{"a"}}),
		mkSkill("b/y", config.MCP{Name: "github", Command: []string{"b"}}),
	}}
	if _, err := MCPSet(config.Config{}, r2); err == nil || !strings.Contains(err.Error(), `skill "a/x" and skill "b/y"`) {
		t.Fatalf("skill+skill: %v", err)
	}
}

func TestMCPSetClosureSubtractsAfterSkillUnion(t *testing.T) {
	// The whole point of keeping closures through the merge: a config-layer
	// "!statsig" reaches a SKILL-declared server.
	cfg := config.Config{MCPClosed: []string{"telemetry"}}
	r := Resolved{Skills: []Skill{mkSkill("pete/tools",
		config.MCP{Name: "telemetry", Command: []string{"t"}},
		config.MCP{Name: "github", Command: []string{"g"}},
	)}}
	set, err := MCPSet(cfg, r)
	if err != nil {
		t.Fatalf("MCPSet: %v", err)
	}
	if len(set) != 1 || set[0].MCP.Name != "github" {
		t.Fatalf("closure must subtract the skill's server: %+v", set)
	}

	// A closure matching nothing is inert, never an error.
	set2, err := MCPSet(config.Config{MCPClosed: []string{"ghost"}}, r)
	if err != nil || len(set2) != 2 {
		t.Fatalf("inert closure: set=%+v err=%v", set2, err)
	}

	// The closure ALSO dissolves a would-be duplicate: with the name closed,
	// neither claimant survives to conflict... but the reject happens on the
	// union, before subtraction — pin that a closed name still hard-rejects
	// when two ACTIVE sources claim it, so the error isn't maskable.
	cfg3 := config.Config{
		MCPs:      []config.MCP{{Name: "github", Command: []string{"a"}}},
		MCPClosed: []string{"github"},
	}
	r3 := Resolved{Skills: []Skill{mkSkill("pete/tools", config.MCP{Name: "github", Command: []string{"b"}})}}
	if _, err := MCPSet(cfg3, r3); err == nil {
		t.Fatalf("duplicate must reject even when closed (the conflict is real; closing hides delivery, not the collision)")
	}
}

func TestMCPEgressDerivation(t *testing.T) {
	set := []MCPDecl{
		{Skill: MCPFromConfig, MCP: config.MCP{Name: "linear", URL: "https://mcp.linear.app/mcp", Egress: []string{"auth.linear.app"}}},
		{Skill: "pete/tools", MCP: config.MCP{Name: "github", Command: []string{"gh-mcp"}}},
		{Skill: MCPFromConfig, MCP: config.MCP{Name: "alt", URL: "http://mcp.example.com:8080/mcp"}},
	}
	got := MCPEgress(set)
	want := []EgressAllow{
		{Skill: "mcp:linear", Host: "mcp.linear.app", Port: 443},
		{Skill: "mcp:linear", Host: "auth.linear.app", Port: 443},
		{Skill: "mcp:alt", Host: "mcp.example.com", Port: 8080},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestResolveValidatesSkillMCPs(t *testing.T) {
	home := testHome(t)
	writeSkill(t, home, "toolkit", `
[[mcp]]
name = "github"
command = ["gh-mcp", "stdio"]
env = ["GITHUB_TOKEN"]
`, nil)
	res, err := Resolve(config.Config{Skills: []string{"toolkit"}}, catFor(t, home))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(res.Skills) != 1 || len(res.Skills[0].File.MCPs) != 1 || res.Skills[0].File.MCPs[0].Name != "github" {
		t.Fatalf("skill MCP lost: %+v", res.Skills)
	}
	// An MCP-only skill is a real contributor, never a stub.
	if IsStub(res.Skills[0].File) {
		t.Fatalf("MCP-only skill misclassified as stub")
	}

	writeSkill(t, home, "badshape", `
[[mcp]]
name = "Bad_Name"
command = ["x"]
`, nil)
	if _, err := Resolve(config.Config{Skills: []string{"badshape"}}, catFor(t, home)); err == nil ||
		!strings.Contains(err.Error(), `skill "badshape"`) || !strings.Contains(err.Error(), "mcp name") {
		t.Fatalf("bad shape: %v", err)
	}

	writeSkill(t, home, "twice", `
[[mcp]]
name = "github"
command = ["a"]

[[mcp]]
name = "github"
command = ["b"]
`, nil)
	if _, err := Resolve(config.Config{Skills: []string{"twice"}}, catFor(t, home)); err == nil ||
		!strings.Contains(err.Error(), "declared twice") {
		t.Fatalf("intra-skill duplicate: %v", err)
	}
}

func TestResolveValidatesAgentMCPAdapter(t *testing.T) {
	home := testHome(t)
	writeSkill(t, home, "agenty", `
[runtime]

[agent]
command = "agenty --go"
mcp = "inject"
`, nil)
	if _, err := Resolve(config.Config{Skills: []string{"agenty"}}, catFor(t, home)); err != nil {
		t.Fatalf("inject adapter must validate: %v", err)
	}
	writeSkill(t, home, "typo", `
[agent]
command = "typo --go"
mcp = "injekt"
`, nil)
	if _, err := Resolve(config.Config{Skills: []string{"typo"}}, catFor(t, home)); err == nil ||
		!strings.Contains(err.Error(), `[agent] mcp "injekt" invalid`) {
		t.Fatalf("adapter typo must refuse: %v", err)
	}
}
