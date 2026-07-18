package configui

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// The MCP screen: add/edit/delete with config-owned validation, the effective
// rows (local, inherited-with-override, skill-with-closure), and assemble
// round-trip. Egress-pattern parity — plus the one MCP-specific power: a
// skill-declared server is closable from this file (`!name` reaches it).
func TestMCPItemAddEditValidation(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fMCP

	// The editor is Kind-first: the picker is control 0 (focus starts there)
	// and the single Endpoint input's meaning follows it — the url-XOR-command
	// rule is structural, no both-set state exists to reject.
	m = m.startItem(-1)
	if !m.itemHasMode || !m.itemModeFirst || m.itemModeLabel != "Kind" || !m.onModePicker() {
		t.Fatalf("Kind picker must lead the MCP editor: hasMode=%v first=%v label=%q onPicker=%v",
			m.itemHasMode, m.itemModeFirst, m.itemModeLabel, m.onModePicker())
	}
	if len(m.inputs) != 5 {
		t.Fatalf("form should be 5 inputs + picker, got %d", len(m.inputs))
	}

	// Reject: no endpoint at all (local kind, empty command).
	m.inputs[0].SetValue("github")
	if m2 := m.commitItem(); m2.itemErr == "" || len(m2.mcps) != 0 {
		t.Fatalf("empty endpoint should be rejected: err=%q mcps=%v", m2.itemErr, m2.mcps)
	}
	// Accept a local declaration with env + egress; the name auto-lowercases.
	m.inputs[0].SetValue("GitHub")
	m.inputs[1].SetValue("gh-mcp stdio")
	m.inputs[2].SetValue("GITHUB_TOKEN GH_HOST")
	m.inputs[3].SetValue("api.github.com")
	m = m.commitItem()
	if m.itemErr != "" || len(m.mcps) != 1 {
		t.Fatalf("local add failed: err=%q mcps=%v", m.itemErr, m.mcps)
	}
	got := m.mcps[0]
	if got.Name != "github" || got.Command[0] != "gh-mcp" || len(got.Env) != 2 || got.Egress[0] != "api.github.com" {
		t.Fatalf("declaration shape wrong (name must auto-lowercase): %+v", got)
	}

	// Duplicate name in this layer: caught by the assembled ValidateLayer.
	m = m.startItem(-1)
	m.inputs[0].SetValue("github")
	m.inputs[1].SetValue("other")
	if m2 := m.commitItem(); m2.itemErr == "" || len(m2.mcps) != 1 {
		t.Fatalf("in-layer duplicate should be rejected: err=%q", m2.itemErr)
	}

	// Edit in place: flip the Kind to remote; the endpoint becomes a url.
	m = m.startItem(0)
	if m.itemMode != 0 {
		t.Fatalf("editing a local declaration must open with Kind=local")
	}
	m.itemMode = 1
	m.inputs[1].SetValue("https://mcp.github.example/mcp")
	m = m.commitItem()
	if m.itemErr != "" || !m.mcps[0].Remote() {
		t.Fatalf("edit to remote failed: err=%q %+v", m.itemErr, m.mcps)
	}
	// Re-opening a remote declaration restores Kind + url in the endpoint.
	m = m.startItem(0)
	if m.itemMode != 1 || m.inputs[1].Value() != "https://mcp.github.example/mcp" {
		t.Fatalf("remote edit must reopen as Kind=remote with the url: mode=%d val=%q", m.itemMode, m.inputs[1].Value())
	}
	m = m.commitItem()

	// Assemble round-trips the working state into the config.
	if out := m.assemble(); len(out.MCPs) != 1 || out.MCPs[0].URL == "" {
		t.Fatalf("assemble lost the declaration: %+v", out.MCPs)
	}
	m.deleteItem(fMCP, 0)
	if out := m.assemble(); out.MCPs != nil {
		t.Fatalf("empty set must assemble nil: %+v", out.MCPs)
	}
}

func TestMCPRowsEffectiveView(t *testing.T) {
	inh := Inherited{
		HasLower: true,
		Default: config.Config{MCPs: []config.MCP{
			{Name: "inherited", Command: []string{"srv"}},
			{Name: "shadowed", Command: []string{"old"}},
		}},
		Skills: map[string]SkillRuntime{
			"pete/tools": {MCPs: []config.MCP{
				{Name: "from-skill", Command: []string{"sk"}},
				{Name: "closed-skill", Command: []string{"sk2"}},
			}},
		},
	}
	cfg := config.Config{
		Skills: []string{"pete/tools"},
		MCPs: []config.MCP{
			{Name: "own", Command: []string{"mine"}},
			{Name: "shadowed", Command: []string{"new"}},
			{Name: "!closed-skill"},
			{Name: "!ghost"},
		},
	}
	m := newModel("t", "/tmp/x", cfg, nil, nil, []string{"pete/tools"}, nil, inh, nil, TargetProject)
	m.listField = fMCP
	rows := m.fieldRows(fMCP)

	find := func(kind rowKind, substr string) *listRow {
		for i := range rows {
			if rows[i].kind == kind && strings.Contains(rows[i].text, substr) {
				return &rows[i]
			}
		}
		return nil
	}
	if r := find(rowInherited, "inherited"); r == nil || r.source != "default" || r.ident != "inherited" {
		t.Fatalf("inherited row wrong: %+v (rows: %+v)", r, rows)
	}
	if r := find(rowOverride, "shadowed — local: new"); r == nil {
		t.Fatalf("replace-by-name must render as override: %+v", rows)
	}
	if r := find(rowLocal, "own"); r == nil {
		t.Fatalf("local row missing: %+v", rows)
	}
	if r := find(rowSkill, "from-skill"); r == nil || r.ident != "from-skill" {
		t.Fatalf("skill row must be closable (ident set): %+v", rows)
	}
	if r := find(rowRemoved, "closed-skill"); r == nil || r.idx < 0 {
		t.Fatalf("skill server closed by this file must show removed with Restore: %+v", rows)
	}
	if r := find(rowStaleMarker, "ghost"); r == nil {
		t.Fatalf("marker matching nothing must read stale: %+v", rows)
	}

	// The closable skill row offers exactly "Remove in this project", and
	// applying it writes the closure marker into this layer.
	sk := find(rowSkill, "from-skill")
	choices := m.rowChoices(fMCP, *sk)
	if len(choices) != 1 || choices[0].act != actRemoveHere {
		t.Fatalf("skill MCP row choices: %+v", choices)
	}
	m.removeHere(*sk)
	if out := m.assemble(); !hasMCPName(out.MCPs, "!from-skill") {
		t.Fatalf("removeHere must write the closure: %+v", out.MCPs)
	}

	// A non-MCP skill row still has no menu (parity guard).
	if got := m.rowChoices(fEgress, listRow{kind: rowSkill, ident: "x"}); got != nil {
		t.Fatalf("egress skill rows must stay menu-less: %+v", got)
	}
}

func TestMCPSigTracksChanges(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{MCPs: []config.MCP{{Name: "a", Command: []string{"srv"}}}}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	if m.dirty() {
		t.Fatal("fresh model must not be dirty")
	}
	m.mcps[0].Command = []string{"changed"}
	if !m.dirty() {
		t.Fatal("an MCP edit must flip the dirty signature")
	}
}

// The argv text form must be REVERSIBLE: opening a declaration whose command
// carries spaced/quoted args and committing it unchanged must not corrupt
// the argv (codex review round 4 — join/Fields split "hello world" apart).
func TestMCPArgvRoundTrip(t *testing.T) {
	cases := [][]string{
		{"server", "--label", "hello world"},
		{"srv", `say "hi"`, ""},
		{"plain", "args", "only"},
		{"srv", `trailing backslash \`},
		{"srv", `double \\ back`, `\" tricky`},
		{`C:\bare\backslash`, "unquoted"},
	}
	for _, argv := range cases {
		got, err := splitArgv(joinArgv(argv))
		if err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
		if strings.Join(got, "\x00") != strings.Join(argv, "\x00") {
			t.Errorf("round trip lost data: %v -> %q -> %v", argv, joinArgv(argv), got)
		}
	}
	if _, err := splitArgv(`bad "unterminated`); err == nil {
		t.Error("unterminated quote must error")
	}

	// The regression as the user hits it: open the existing item, commit
	// with no edits, argv unchanged.
	m := newModel("t", "/tmp/x", config.Config{MCPs: []config.MCP{
		{Name: "spaced", Command: []string{"server", "--label", "hello world"}},
	}}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fMCP
	m = m.startItem(0)
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("no-op commit errored: %s", m.itemErr)
	}
	if got := m.mcps[0].Command; len(got) != 3 || got[2] != "hello world" {
		t.Fatalf("no-op open-and-commit corrupted argv: %v", got)
	}
}

// Headers ride the argv codec (one quoted "Name: value" token each): the
// form accepts them for remote kind, validation refuses them on local, and
// a no-op open-and-commit round-trips multiple headers unchanged.
func TestMCPHeadersInForm(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{MCPs: []config.MCP{{
		Name: "proxied", URL: "https://mcp.internal.example/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${TOK}", "X-Api-Key": "${KEY}"},
	}}}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fMCP

	// No-op open-and-commit keeps both headers.
	m = m.startItem(0)
	m = m.commitItem()
	if m.itemErr != "" || len(m.mcps[0].Headers) != 2 || m.mcps[0].Headers["Authorization"] != "Bearer ${TOK}" {
		t.Fatalf("headers round trip: err=%q %+v", m.itemErr, m.mcps[0].Headers)
	}
	// Edit a header value through the input.
	m = m.startItem(0)
	m.inputs[4].SetValue(`"Authorization: Bearer ${OTHER}"`)
	m = m.commitItem()
	if m.itemErr != "" || m.mcps[0].Headers["Authorization"] != "Bearer ${OTHER}" || len(m.mcps[0].Headers) != 1 {
		t.Fatalf("header edit: err=%q %+v", m.itemErr, m.mcps[0].Headers)
	}
	// Headers on a local declaration refuse (config owns the rule).
	m = m.startItem(-1)
	m.inputs[0].SetValue("loc")
	m.inputs[1].SetValue("srv")
	m.inputs[4].SetValue(`"X: y"`)
	if m2 := m.commitItem(); m2.itemErr == "" || !strings.Contains(m2.itemErr, "remote (url) servers") {
		t.Fatalf("local headers must refuse: %q", m2.itemErr)
	}
	// A malformed header token errors cleanly.
	m.inputs[1].SetValue("")
	m.itemMode = 1
	m.inputs[1].SetValue("https://h.example/mcp")
	m.inputs[4].SetValue(`"no-colon"`)
	if m2 := m.commitItem(); m2.itemErr == "" || !strings.Contains(m2.itemErr, "Name: value") {
		t.Fatalf("malformed header: %q", m2.itemErr)
	}
}
