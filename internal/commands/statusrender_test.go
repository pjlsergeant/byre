package commands

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

// A value too long for the budget wraps INSIDE the funnel, hanging to the
// value column. Terminal wrapping put continuations at column zero, where
// they read as new rows -- the bug this replaces.
func TestStatusRowsWrapToTheValueColumn(t *testing.T) {
	var b strings.Builder
	writeStatusRows(&b, []statusRow{
		{Label: "Host env", Value: "GIT_AUTHOR_NAME <- git:user.name, GIT_AUTHOR_EMAIL <- git:user.email, NGROK_AUTHTOKEN <- env:NGROK_AUTHTOKEN"},
	}, 60)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("value never wrapped at width 60:\n%s", b.String())
	}
	for _, l := range lines {
		if len(l) > 60 {
			t.Errorf("line overruns the budget (%d cols): %q", len(l), l)
		}
	}
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, strings.Repeat(" ", statusLabelMin+1)) {
			t.Errorf("continuation line is not indented to the value column: %q", l)
		}
	}
	// The value survives the wrap intact: joining the pieces reproduces it.
	joined := lines[0][statusLabelMin+1:]
	for _, l := range lines[1:] {
		joined += " " + strings.TrimLeft(l, " ")
	}
	if want := "GIT_AUTHOR_NAME <- git:user.name, GIT_AUTHOR_EMAIL <- git:user.email, NGROK_AUTHTOKEN <- env:NGROK_AUTHTOKEN"; joined != want {
		t.Errorf("wrapping changed the value:\n got %q\nwant %q", joined, want)
	}
}

// The break lands at the row grammar's own separators, not in the middle of
// a path or an id.
func TestWrapValueBreaksAtSeparators(t *testing.T) {
	got := wrapValue("alpha-one, beta-two, gamma-three, delta-four", 24)
	for _, l := range got {
		if strings.HasSuffix(l, "-") || strings.Contains(l, "alph ") {
			t.Errorf("broke through a token: %q", got)
		}
	}
	for _, l := range got[:len(got)-1] {
		if !strings.HasSuffix(l, ",") {
			t.Errorf("line %q did not end at a separator: %v", l, got)
		}
	}
}

// A single token longer than the budget is left whole and allowed to
// overhang: a path cut in half is a lie about the path.
func TestWrapValueKeepsAnOverlongTokenWhole(t *testing.T) {
	long := "/home/me/some/extremely/deeply/nested/project/directory/name"
	got := wrapValue("mounts "+long+" (ro)", 20)
	found := false
	for _, l := range got {
		if strings.Contains(l, long) {
			found = true
		}
	}
	if !found {
		t.Errorf("the over-long path was broken up: %q", got)
	}
}

// The label column is as wide as the widest label the page actually uses, so
// a long label moves the column instead of pushing its own value out of line.
func TestStatusLabelColumnFitsTheLongestLabel(t *testing.T) {
	var b strings.Builder
	writeStatusRows(&b, []statusRow{
		{Label: "Agent", Value: "claude"},
		{Label: "Claude Skills", Value: "tdd-loop"},
	}, noWrapWidth)
	rows := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	col := strings.Index(rows[0], "claude")
	if col != strings.Index(rows[1], "tdd-loop") {
		t.Errorf("values do not share a column:\n%s", b.String())
	}
	if col != len("Claude Skills:")+1 {
		t.Errorf("column %d does not fit the longest label:\n%s", col, b.String())
	}
}

// The skills provenance column is sized from the widest id on the page. A
// fixed width was the bug: one long id pushed its own provenance out and
// mangled every row's alignment.
func TestPkgColumnSizesToTheLongestID(t *testing.T) {
	rows := []statusRow{}
	for i, line := range []string{
		pkgColumn("byre/claude", "bundled v1.3.1", len("pjlsergeant/claude-skills-pocock")+2),
		pkgColumn("pjlsergeant/claude-skills-pocock", "installed 1.0.0", len("pjlsergeant/claude-skills-pocock")+2),
	} {
		label := "Skills"
		if i > 0 {
			label = ""
		}
		rows = append(rows, statusRow{Label: label, Value: line})
	}
	var b strings.Builder
	writeStatusRows(&b, rows, noWrapWidth)
	out := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if strings.Index(out[0], "bundled") != strings.Index(out[1], "installed") {
		t.Errorf("provenance column is not aligned:\n%s", b.String())
	}
}

// A delivery verdict that says delivery WORKS folds into the rows it
// qualifies; one that has stopped asserting keeps its own row at every tier.
// That is the whole rule behind the fold: the default tier folds mechanism,
// never a withdrawal.
func TestDefaultTierFoldsOnlyAWorkingDeliveryVerdict(t *testing.T) {
	decl := []skills.MCPDecl{{Skill: skills.MCPFromConfig, MCP: config.MCP{Name: "github", Command: []string{"gh-mcp"}}}}

	var works strings.Builder
	renderStatus(&works, statusInfo{Agent: "byre/claude", AgentMCP: "inject", MCPs: decl}, tierDefault, noWrapWidth)
	assertRow(t, works.String(), "MCP servers", "github — local: gh-mcp  (config)  — delivered")
	if strings.Contains(works.String(), "the agent session receives") {
		t.Errorf("a working verdict must not keep its own arrow row:\n%s", works.String())
	}

	// No adapter: the verdict is a claim byre has withdrawn, so it keeps the
	// row and the remedy even in the short page.
	var degraded strings.Builder
	renderStatus(&degraded, statusInfo{Agent: "byre/gemini", MCPs: decl}, tierDefault, noWrapWidth)
	out := degraded.String()
	if !strings.Contains(out, "NOT delivered: agent skill byre/gemini has no MCP adapter") {
		t.Errorf("a withdrawn verdict must keep its own row at the default tier:\n%s", out)
	}
	if strings.Contains(out, "— delivered") {
		t.Errorf("a withdrawn verdict must not be folded into the row as delivery:\n%s", out)
	}
}

// Template and Skills share one id column, so they line up with each other
// as they always have -- sizing each set on its own fixed the overflow and
// broke that.
func TestPackageRowsShareOneIDColumn(t *testing.T) {
	// The template is IN the set the width is computed from, so a template
	// id wider than every skill still moves the shared column.
	skillsOnly := []string{"byre/claude"}
	withTemplate := append(append([]string{}, skillsOnly...), "some/very-long-template-id")
	if pkgIDWidth(nil, withTemplate, tierDefault) <= pkgIDWidth(nil, skillsOnly, tierDefault) {
		t.Error("the template does not participate in the shared id column")
	}
	if got, want := pkgIDWidth(nil, withTemplate, tierDefault), len("some/very-long-template-id")+2; got != want {
		t.Errorf("id column = %d, want %d (the widest id plus a gap)", got, want)
	}
}

// Output that is not going to a terminal wraps at the fixed fallback, so a
// redirected `byre status` is byte-identical wherever it is produced -- which
// is what lets the README and the docs site pin a sample of it.
func TestStatusWidthFallsBackOffATerminal(t *testing.T) {
	if got := statusWidth(&strings.Builder{}); got != statusFallbackWidth {
		t.Errorf("statusWidth off a terminal = %d, want %d", got, statusFallbackWidth)
	}
}

// The completeness rule: the default tier may truncate values and fold
// mechanism notes, but a row that EXISTS is never elided. Every label the
// full page prints must be on the default page too.
func TestDefaultTierElidesNoRow(t *testing.T) {
	info := fullStatusInfo()
	labels := func(tier statusTier) map[string]bool {
		out := map[string]bool{}
		for _, r := range statusRowsOf(info, tier) {
			if r.Label != "" {
				out[r.Label] = true
			}
		}
		return out
	}
	def := labels(tierDefault)
	for label := range labels(tierFull) {
		if !def[label] {
			t.Errorf("the default tier elided the %q row entirely", label)
		}
	}
}

// A claim degradation and a containment disclosure are never folded: they
// are what the page is for, and they are short.
func TestDefaultTierKeepsDegradationsAtFullStrength(t *testing.T) {
	info := fullStatusInfo()
	var full, def strings.Builder
	renderStatus(&full, info, tierFull, noWrapWidth)
	renderStatus(&def, info, tierDefault, noWrapWidth)
	for _, want := range []string{
		"🛑 HOLE",                     // a skill-declared containment hole
		"🛑 a mount or volume covers", // a byre-managed path shadowed
		"Reserved env",               // the skewed-claims row
		"not a control this byre recognizes",
	} {
		if !strings.Contains(def.String(), want) {
			t.Errorf("the default tier folded %q:\n%s", want, def.String())
		}
	}
}

// fullStatusInfo exercises every row the page can print, so the tier rules
// above are asserted against the whole page rather than a corner of it.
func fullStatusInfo() statusInfo {
	return statusInfo{
		ID:               "proj-abc123",
		Agent:            "byre/claude",
		Template:         "byre/go",
		Chain:            []string{"base"},
		Engine:           "docker",
		Canonical:        "/home/me/proj",
		WorktreeOf:       "/home/me/main",
		PresetNote:       "the repo's byre.preset differs from the version you applied; `byre preset apply` to review the changes",
		PresetShort:      "byre.preset differs from what you applied  (`byre preset apply`)",
		Skills:           []string{"byre/claude", "pjlsergeant/devlog"},
		Binds:            []config.Mount{{Host: "/data", Target: "/data", Mode: "ro"}},
		Ports:            []config.Port{{Container: 8080, Host: 8080}},
		Volumes:          []config.Volume{{Name: "creds", Role: "state"}, {Name: "shared", Scope: "machine"}},
		SelfEdit:         "/home/me/.byre",
		NetPosture:       "deny-by-default",
		NetPostureSkill:  "firewall",
		Egress:           []skills.EgressAllow{{Skill: "firewall", Host: "api.anthropic.com", Port: 443}},
		EgressClosed:     []string{"evil.example"},
		Grants:           []skills.Grant{{Skill: "dockerhost", Caps: []string{"SYS_PTRACE"}}},
		Containments:     []skills.ContainmentDecl{{Skill: "dockerhost", Text: "the box can reach the host engine"}},
		ManagedShadows:   []ManagedPathShadow{{Target: "/etc/byre", Source: "config"}},
		SkillReservedEnv: []skills.ReservedEnvSet{{Skill: "pjlsergeant/devlog", Key: "BYRE_SCRATCH"}},
		MCPs:             []skills.MCPDecl{{Skill: skills.MCPFromConfig, MCP: config.MCP{Name: "github", Command: []string{"gh-mcp"}}}},
		MCPClosed:        []string{"old-thing"},
		AgentMCP:         "inject",
		ClaudeSkills: []skills.ClaudeSkillDecl{
			{Skill: skills.ClaudeSkillsFromConfig, CS: config.ClaudeSkill{Name: "tdd-loop", Path: "~/cs/tdd"}},
		},
		ClaudeSkillsClosed: []string{"legacy"},
		AgentClaudeSkills:  "inject",
		Contexts:           []config.ContextDecl{{Name: "house-rules", Text: "Run the linter.\nNever force-push.\n"}},
		AgentContext:       "inject",
		HostEnv: []hostEnvResult{
			{Key: "GIT_AUTHOR_NAME", Source: "git:user.name", Value: "me", State: hostEnvDelivered},
		},
		EnvKeys:         []string{"TOKEN_NAME"},
		RunArgs:         []string{"--cap-add=SYS_PTRACE"},
		BuildRaw:        []string{"RUN echo hi"},
		Container:       "abcdef0123456789",
		SiblingSessions: []string{"wt-1 (0123456789ab)"},
	}
}
