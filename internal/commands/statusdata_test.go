package commands

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

// decodeStatusData renders --data and decodes it generically -- generically
// on purpose: a test that unmarshals into statusData would agree with any
// rename the product made, which is the one thing a wire-shape test must not
// do.
func decodeStatusData(t *testing.T, s statusInfo) map[string]any {
	t.Helper()
	var b bytes.Buffer
	if err := writeStatusData(&b, s); err != nil {
		t.Fatalf("writeStatusData: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("--data is not valid JSON: %v\n%s", err, b.String())
	}
	return got
}

// --data carries a version. It is versioned-but-not-frozen for now: nothing
// consumes it, byre does not advertise it as scriptable, and the field is
// what lets that change without breaking a reader that arrives later.
func TestStatusDataCarriesItsVersion(t *testing.T) {
	got := decodeStatusData(t, fullStatusInfo())
	if got["version"] != float64(StatusDataVersion) {
		t.Errorf("version = %v, want %d", got["version"], StatusDataVersion)
	}
}

// The exit report's rule holds on every surface: env KEYS cross, values
// never do.
func TestStatusDataCarriesEnvKeysNeverValues(t *testing.T) {
	var b bytes.Buffer
	if err := writeStatusData(&b, statusInfo{
		HostEnv: []hostEnvResult{{
			Key: "NGROK_AUTHTOKEN", Source: "env:NGROK_AUTHTOKEN",
			Value: "s3cr3t-token-value", State: hostEnvDelivered,
		}},
		EnvKeys: []string{"TOKEN_NAME"},
	}); err != nil {
		t.Fatalf("writeStatusData: %v", err)
	}
	out := b.String()
	if strings.Contains(out, "s3cr3t-token-value") {
		t.Errorf("--data leaked a host env VALUE:\n%s", out)
	}
	for _, want := range []string{"NGROK_AUTHTOKEN", "TOKEN_NAME", "delivered"} {
		if !strings.Contains(out, want) {
			t.Errorf("--data dropped %q:\n%s", want, out)
		}
	}
}

// --data is the same information --full renders: every grant, mount, volume,
// skill, port and reserved key the page shows has a field here.
func TestStatusDataCoversTheFullPage(t *testing.T) {
	got := decodeStatusData(t, fullStatusInfo())
	for _, key := range []string{
		"project_id", "workdir", "worktree_of", "agent", "template", "extends",
		"preset_note", "self_edit", "engine", "network", "ports", "binds",
		"volumes", "skills", "skill_grants", "reserved_env", "mcp_servers",
		"mcp_closed", "mcp_delivery", "claude_skills", "claude_skills_closed",
		"claude_skills_delivery", "instructions", "instructions_delivery",
		"host_env", "env_keys", "run_args", "build_raw", "containments",
		"managed_path_shadows", "container",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("--data has no %q field", key)
		}
	}
	// The rows the page never folds are structured facts here, not prose:
	// a reader must be able to see a hole without parsing a sentence.
	net, _ := got["network"].(map[string]any)
	if net["warranted"] != false {
		t.Errorf("network.warranted = %v; a raw build line plus an unrecognized reserved key must withdraw the claim", net["warranted"])
	}
	res, _ := got["reserved_env"].([]any)
	if len(res) != 1 {
		t.Fatalf("reserved_env = %v, want one entry", res)
	}
	entry, _ := res[0].(map[string]any)
	if entry["known"] != false {
		t.Errorf("BYRE_SCRATCH must not be reported as a known chassis knob: %v", entry)
	}
	if note, _ := entry["note"].(string); !strings.Contains(note, "not a control this byre recognizes") {
		t.Errorf("reserved_env note overclaims: %q", note)
	}
}

// A found engine that will not answer leaves the box's state UNKNOWN. --data
// must not collapse that to "stopped": the lifecycle commands refuse in this
// state, and a confident negative here would contradict them.
func TestStatusDataKeepsAnUnknownContainerStateUnknown(t *testing.T) {
	got := decodeStatusData(t, statusInfo{Engine: "docker", ContainerQueryErr: "daemon timeout"})
	c, _ := got["container"].(map[string]any)
	if c["state"] != "unknown" {
		t.Errorf("container.state = %v, want unknown", c["state"])
	}
	if c["error"] != "daemon timeout" {
		t.Errorf("container.error = %v, want the engine's reason", c["error"])
	}
}

// The self-edit grant is a bind like any other here: a reader asking "what
// can this box write?" must not have to know it is spelled differently.
func TestStatusDataListsTheSelfEditGrantAsABind(t *testing.T) {
	got := decodeStatusData(t, statusInfo{
		SelfEdit: "/home/me/.byre",
		Binds:    []config.Mount{{Host: "/data", Target: "/data"}},
	})
	binds, _ := got["binds"].([]any)
	if len(binds) != 2 {
		t.Fatalf("binds = %v, want the project mount and the self-edit store", binds)
	}
	last, _ := binds[1].(map[string]any)
	if last["host"] != "/home/me/.byre" || last["mode"] != "rw" {
		t.Errorf("self-edit bind wrong: %v", last)
	}
	// A mode-less mount defaults to ro, the same way the runtime binds it.
	first, _ := binds[0].(map[string]any)
	if first["mode"] != "ro" {
		t.Errorf("a mode-less mount must report the ro default, got %v", first["mode"])
	}
}

// MCP declarations carry their consumed env NAMES and which of them this box
// actually provides -- the page's "(provided) / (NOT provided)" verdict, as
// data rather than as a sentence.
func TestStatusDataMarksProvidedMCPEnv(t *testing.T) {
	got := decodeStatusData(t, statusInfo{
		Agent: "byre/claude", AgentMCP: "inject",
		MCPs: []skills.MCPDecl{{Skill: skills.MCPFromConfig, MCP: config.MCP{
			Name: "github", Command: []string{"gh-mcp"}, Env: []string{"GITHUB_TOKEN", "GH_HOST"},
		}}},
		EnvProvided: map[string]bool{"GITHUB_TOKEN": true},
	})
	servers, _ := got["mcp_servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("mcp_servers = %v", servers)
	}
	m, _ := servers[0].(map[string]any)
	if consumed, _ := m["consumed_env"].([]any); len(consumed) != 2 {
		t.Errorf("consumed_env = %v, want both names", m["consumed_env"])
	}
	provided, _ := m["provided_env"].([]any)
	if len(provided) != 1 || provided[0] != "GITHUB_TOKEN" {
		t.Errorf("provided_env = %v, want only the key this box supplies", m["provided_env"])
	}
	if m["source"] != "config" {
		t.Errorf("source = %v, want config", m["source"])
	}
}
