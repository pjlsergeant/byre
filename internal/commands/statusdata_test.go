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

// --data mirrors the page's compat-warning rows (version 3): kind, layer,
// path and text cross; a clean cascade omits the field entirely.
func TestStatusDataCarriesCompatWarnings(t *testing.T) {
	got := decodeStatusData(t, statusInfo{CompatWarnings: []config.Warning{{
		Kind: config.WarnSharedAuthTopLevel, Layer: "default",
		Path: "/h/default.config",
		Text: "legacy top-level shared_auth — the next save moves it under [defaults]",
	}}})
	warns, ok := got["warnings"].([]any)
	if !ok || len(warns) != 1 {
		t.Fatalf("warnings = %v", got["warnings"])
	}
	w := warns[0].(map[string]any)
	if w["kind"] != config.WarnSharedAuthTopLevel || w["layer"] != "default" {
		t.Errorf("warning = %v", w)
	}
	if s, _ := w["text"].(string); !strings.Contains(s, "moves it under [defaults]") {
		t.Errorf("warning text must carry the remedy: %v", w["text"])
	}

	if clean := decodeStatusData(t, statusInfo{}); clean["warnings"] != nil {
		t.Errorf("a clean cascade must omit warnings, got %v", clean["warnings"])
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
	byKey := map[string]map[string]any{}
	for _, e := range res {
		m, _ := e.(map[string]any)
		key, _ := m["key"].(string)
		byKey[key] = m
	}
	// A key byre reads and a key it does not must be distinguishable as
	// DATA, not only in the prose: both degrade, only one is a byre control.
	scratch, ok := byKey["BYRE_SCRATCH"]
	if !ok {
		t.Fatalf("reserved_env = %v, want the unrecognized key", res)
	}
	if scratch["known"] != false {
		t.Errorf("BYRE_SCRATCH must not be reported as a known chassis knob: %v", scratch)
	}
	if note, _ := scratch["note"].(string); !strings.Contains(note, "not a control this byre recognizes") {
		t.Errorf("reserved_env note overclaims: %q", note)
	}
	if knob, ok := byKey["BYRE_EGRESS"]; !ok || knob["known"] != true {
		t.Errorf("a chassis knob must be reported as known: %v", byKey["BYRE_EGRESS"])
	}
}

// The page and --data read ONE predicate for whether byre stands behind the
// Network row, so they cannot disagree about whether the wall is byre's. The
// open default is the case that caught it: the row prints "open" flat with
// raw run_args present, and --data must not call that unwarranted.
func TestStatusDataNetworkWarrantAgreesWithThePage(t *testing.T) {
	for _, tc := range []struct {
		name      string
		info      statusInfo
		wantRow   string // a fragment the Network row must carry
		warranted bool
	}{
		{
			name:      "open network, raw run_args -- nothing to withdraw",
			info:      statusInfo{ProjectRunArgs: true, RunArgs: []string{"--privileged"}},
			wantRow:   "open",
			warranted: true,
		},
		{
			name:      "declared posture, raw run_args -- withdrawn",
			info:      statusInfo{NetPosture: "deny-by-default", NetPostureSkill: "firewall", ProjectRunArgs: true},
			wantRow:   "not guaranteed",
			warranted: false,
		},
		{
			name:      "declared posture, nothing displacing it",
			info:      statusInfo{NetPosture: "deny-by-default", NetPostureSkill: "firewall"},
			wantRow:   "skill: firewall",
			warranted: true,
		},
		{
			name:      "skills unresolved -- unknowable",
			info:      statusInfo{NetPosture: "deny-by-default", SkillErr: "broken"},
			wantRow:   "unknown",
			warranted: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if row := networkLine(tc.info); !strings.Contains(row, tc.wantRow) {
				t.Fatalf("Network row = %q, want it to carry %q", row, tc.wantRow)
			}
			got := decodeStatusData(t, tc.info)
			net, _ := got["network"].(map[string]any)
			if net["warranted"] != tc.warranted {
				t.Errorf("network.warranted = %v, want %v -- the page says %q",
					net["warranted"], tc.warranted, networkLine(tc.info))
			}
		})
	}
}

// A passthrough a layer switched off is not a channel this box has: the page
// omits it, so --data omits it too. Emitting it would make --data a superset
// of --full rather than the same content, and invite a reader to count a
// disabled passthrough as one that exists.
func TestStatusDataOmitsDisabledHostEnvTheSameWayThePageDoes(t *testing.T) {
	info := statusInfo{HostEnv: []hostEnvResult{
		{Key: "TERM", Source: "env:TERM", Value: "xterm", State: hostEnvDelivered},
		{Key: "TZ", Source: "tz:", State: hostEnvDisabled},
	}}
	var page strings.Builder
	renderStatus(&page, info, tierFull, noWrapWidth)
	if strings.Contains(page.String(), "TZ") {
		t.Fatalf("the page's own rule changed -- it now shows a disabled entry:\n%s", page.String())
	}
	got := decodeStatusData(t, info)
	entries, _ := got["host_env"].([]any)
	if len(entries) != 1 {
		t.Fatalf("host_env = %v, want only the entry the page shows", entries)
	}
	if e, _ := entries[0].(map[string]any); e["key"] != "TERM" {
		t.Errorf("host_env kept the wrong entry: %v", entries[0])
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

// The page marks an exclusive volume in its row and the delta can name a
// sharing change, so a document without the key would be the one place the
// two tiers describe different boxes -- which is the rule this projection is
// held to. Always written, like scope: absent-means-shared is a rule a reader
// should not have to know.
func TestStatusDataCarriesVolumeSharing(t *testing.T) {
	got := decodeStatusData(t, statusInfo{
		Engine: "docker",
		Volumes: []config.Volume{
			{Name: "ledger", Role: "state", Target: "/var/lib/ledger", Sharing: "exclusive"},
			{Name: "deps", Role: "cache", Target: "/workspace/node_modules"},
		},
	})
	vols, _ := got["volumes"].([]any)
	if len(vols) != 2 {
		t.Fatalf("volumes = %v", vols)
	}
	sharing := map[string]any{}
	for _, v := range vols {
		m, _ := v.(map[string]any)
		name, _ := m["name"].(string)
		s, ok := m["sharing"]
		if !ok {
			t.Fatalf("volume %q has no sharing key: %v", name, m)
		}
		sharing[name] = s
	}
	if sharing["ledger"] != "exclusive" {
		t.Errorf("the single-writer declaration did not reach --data: %v", sharing)
	}
	if sharing["deps"] != "shared" {
		t.Errorf("the default must be spelled out, not omitted: %v", sharing)
	}
}
