package commands

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
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

// Width is measured in terminal CELLS. A value byre did not author can carry
// CJK or emoji, and byre's own 🛑 containment marker is two cells wide -- so
// counting runes let exactly the rows that matter most overrun the budget and
// get wrapped by the TERMINAL at column zero, the bug this funnel replaced.
func TestStatusRowsMeasureWideCharactersAsTwoCells(t *testing.T) {
	if displayLen("🛑") != 2 {
		t.Errorf("the containment marker measures %d cells, want 2", displayLen("🛑"))
	}
	if displayLen("日本語") != 6 {
		t.Errorf("CJK measures %d cells, want 6", displayLen("日本語"))
	}

	// A value of nothing but wide runes: every emitted line must fit the
	// budget in CELLS, which a rune count would let it exceed twofold.
	const budget = 40
	wide := strings.TrimSuffix(strings.Repeat("日本語のパス, ", 12), ", ")
	for _, l := range wrapValue(wide, budget) {
		if displayLen(l) > budget {
			t.Errorf("wrapped line is %d cells, budget %d: %q", displayLen(l), budget, l)
		}
	}

	// And through the funnel, where the label column shares the budget.
	var b strings.Builder
	writeStatusRows(&b, []statusRow{{Label: "Containment", Value: "🛑 " + wide}}, 60)
	for _, l := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if displayLen(l) > 60 {
			t.Errorf("rendered line is %d cells, budget 60: %q", displayLen(l), l)
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

// statusRowCounts folds continuation rows into the label above them, so a
// count answers "how many things does this label report" rather than how many
// lines it took to print them.
func statusRowCounts(rows []statusRow) map[string]int {
	counts := map[string]int{}
	cur := ""
	for _, r := range rows {
		if r.Label != "" {
			cur = r.Label
		}
		counts[cur]++
	}
	return counts
}

// The completeness rule, counted rather than merely present: the default tier
// may truncate values and fold mechanism notes, but a row that EXISTS is
// never elided. Comparing label SETS was too weak -- dropping nine of ten
// Skills rows kept a set-based check green -- so this compares counts, with
// the intentional aggregations named one by one.
func TestDefaultTierElidesNoRow(t *testing.T) {
	info := fullStatusInfo()
	// A fixture with one row per label would make this test vacuous.
	if len(info.Skills) < 2 || len(info.Binds) < 2 || len(info.Ports) < 2 ||
		len(info.MCPs) < 2 || len(info.ClaudeSkills) < 2 || len(info.Contexts) < 2 ||
		len(info.BuildRaw) < 2 || len(info.Grants) < 2 || len(info.Volumes) < 2 {
		t.Fatal("the fixture must carry several rows per label or this proves nothing")
	}
	full := statusRowCounts(statusRowsOf(info, tierFull))
	def := statusRowCounts(statusRowsOf(info, tierDefault))

	// The aggregations the default tier is ALLOWED to make, each with what
	// it collapses to and why. Every other label must match the full page
	// row for row.
	allowed := map[string]int{
		// N opaque Dockerfile lines + the not-introspected note -> one
		// counted row that keeps the caveat.
		"Raw build": 1,
		// N blocks + the delivery arrow -> one row naming and counting them.
		"Instructions": 1,
		// The delivery arrow FOLDS onto the rows it qualifies, so the block
		// loses the arrow's line and keeps one row per server / per skill.
		"MCP servers":   len(info.MCPs),
		"Claude Skills": len(info.ClaudeSkills),
	}
	// DigestHint is the one default-only ADDITION: a continuation row under
	// Skills naming the digest cut. Derived from the product's own predicate
	// so the allowance holds whether or not the fixture carries digests.
	if anyDigestDropped(info.Cat, info.Skills) {
		allowed["Skills"] = full["Skills"] + 1
	}
	for label, n := range full {
		want, ok := allowed[label]
		if !ok {
			want = n
		}
		if got := def[label]; got != want {
			t.Errorf("default tier: %q has %d rows, want %d (full page has %d)", label, got, want, n)
		}
	}
	for label := range def {
		if _, ok := full[label]; !ok {
			t.Errorf("the default tier invented a %q row the full page does not have", label)
		}
	}
}

// Dropping an installed package's acquisition digest is a truncation like
// every other, so it names itself. Without the pointer the short provenance
// is indistinguishable from a package that never carried a digest -- and
// only a package that HAS one gets the pointer, or it advertises detail that
// does not exist.
func TestDefaultTierNamesTheDroppedDigest(t *testing.T) {
	home := installHome(t)
	uri, digest := publishSkill(t, "pete/tool", "1.0.0", "")
	if err := PackageInstall(discardStreams(), packages.KindSkill, uri, "sha256:"+digest, false); err != nil {
		t.Fatal(err)
	}
	cat, err := packages.LoadCatalog(home, nil, "v0.2.0", "0.2.0",
		packages.Stage2Hooks{Skill: skills.ValidatePrimaryBytes, Template: config.ValidateTemplateBytes})
	if err != nil {
		t.Fatal(err)
	}
	info := statusInfo{Cat: cat, Skills: []string{"pete/tool"}}

	var full, def strings.Builder
	renderStatus(&full, info, tierFull, noWrapWidth)
	renderStatus(&def, info, tierDefault, noWrapWidth)
	if !strings.Contains(full.String(), "(sha256:") {
		t.Fatalf("the full tier must show the acquisition digest:\n%s", full.String())
	}
	if strings.Contains(def.String(), "(sha256:") {
		t.Errorf("the default tier must drop the digest:\n%s", def.String())
	}
	if !strings.Contains(def.String(), DigestHint) {
		t.Errorf("the default tier dropped the digest without saying so:\n%s", def.String())
	}
	if strings.Contains(full.String(), DigestHint) {
		t.Errorf("the full tier truncates nothing and must not point at itself:\n%s", full.String())
	}

	// A page with no digest to drop carries no pointer.
	var bundledOnly strings.Builder
	renderStatus(&bundledOnly, statusInfo{Cat: cat, Skills: []string{"claude"}}, tierDefault, noWrapWidth)
	if strings.Contains(bundledOnly.String(), DigestHint) {
		t.Errorf("a page with no dropped digest must not advertise one:\n%s", bundledOnly.String())
	}
}

// The narrow-terminal policy is a CLAMP, not a substitution: byre lays out at
// its floor and lets the terminal wrap the overhang, rather than laying out
// at 80 on a 30-column terminal -- which would guarantee the column-zero
// continuations the funnel exists to end.
func TestStatusWidthClampsRatherThanSubstitutes(t *testing.T) {
	for _, tc := range []struct{ cols, want int }{
		{1, statusMinWidth},
		{30, statusMinWidth},
		{47, statusMinWidth},
		{48, 48},
		{100, 100},
		{160, 160},
		{400, statusMaxWidth},
	} {
		if got := clampStatusWidth(tc.cols); got != tc.want {
			t.Errorf("clampStatusWidth(%d) = %d, want %d", tc.cols, got, tc.want)
		}
	}
	if clampStatusWidth(30) == statusFallbackWidth {
		t.Error("a narrow terminal must not be given the 80-column fallback layout")
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
		ID:          "proj-abc123",
		Agent:       "byre/claude",
		Template:    "byre/go",
		Chain:       []string{"base"},
		Engine:      "docker",
		Canonical:   "/home/me/proj",
		WorktreeOf:  "/home/me/main",
		PresetNote:  "the repo's byre.preset differs from the version you applied; `byre preset apply` to review the changes",
		PresetShort: "byre.preset differs from what you applied  (`byre preset apply`)",
		Skills:      []string{"byre/claude", "pjlsergeant/devlog", "pete/tools"},
		Binds: []config.Mount{
			{Host: "/data", Target: "/data", Mode: "ro"},
			{Host: "/media", Target: "/media", Mode: "rw", Disabled: true},
		},
		Ports:           []config.Port{{Container: 8080, Host: 8080}, {Container: 3000}},
		Volumes:         []config.Volume{{Name: "creds", Role: "state"}, {Name: "shared", Scope: "machine"}},
		SelfEdit:        "/home/me/.byre",
		NetPosture:      "deny-by-default",
		NetPostureSkill: "firewall",
		Egress: []skills.EgressAllow{
			{Skill: "firewall", Host: "api.anthropic.com", Port: 443},
			{Skill: skills.EgressFromConfig, Host: "github.com", Port: 443},
		},
		EgressClosed: []string{"evil.example", "worse.example"},
		Grants: []skills.Grant{
			{Skill: "dockerhost", Caps: []string{"SYS_PTRACE"}},
			{Skill: "pete/tools", Mounts: []config.Mount{{Host: "/opt/t", Target: "/opt/t", Mode: "ro"}}},
		},
		Containments: []skills.ContainmentDecl{
			{Skill: "dockerhost", Text: "the box can reach the host engine"},
			{Skill: "pete/tools", Text: "the box can reach your ssh agent"},
		},
		ManagedShadows: []ManagedPathShadow{{Target: "/etc/byre", Source: "config"}},
		SkillReservedEnv: []skills.ReservedEnvSet{
			{Skill: "pjlsergeant/devlog", Key: "BYRE_SCRATCH"},
			{Skill: "firewall", Key: "BYRE_EGRESS"},
		},
		MCPs: []skills.MCPDecl{
			{Skill: skills.MCPFromConfig, MCP: config.MCP{Name: "github", Command: []string{"gh-mcp"}}},
			{Skill: "pete/tools", MCP: config.MCP{Name: "linear", URL: "https://mcp.linear.app/mcp"}},
		},
		MCPClosed: []string{"old-thing", "older-thing"},
		AgentMCP:  "inject",
		ClaudeSkills: []skills.ClaudeSkillDecl{
			{Skill: skills.ClaudeSkillsFromConfig, CS: config.ClaudeSkill{Name: "tdd-loop", Path: "~/cs/tdd"}},
			{Skill: "pete/tools", CS: config.ClaudeSkill{Name: "review-loop", From: "cs/review"}},
		},
		ClaudeSkillsClosed: []string{"legacy", "older-legacy"},
		AgentClaudeSkills:  "inject",
		Contexts: []config.ContextDecl{
			{Name: "house-rules", Text: "Run the linter.\nNever force-push.\n"},
			{Name: "conventions", File: "~/notes/conv.md"},
		},
		AgentContext: "inject",
		HostEnv: []hostEnvResult{
			{Key: "GIT_AUTHOR_NAME", Source: "git:user.name", Value: "me", State: hostEnvDelivered},
			{Key: "TZ", Source: "tz:", State: hostEnvDisabled},
		},
		EnvKeys:         []string{"TOKEN_NAME"},
		RunArgs:         []string{"--cap-add=SYS_PTRACE"},
		BuildRaw:        []string{"RUN echo hi", "RUN echo there"},
		Container:       "abcdef0123456789",
		SiblingSessions: []string{"wt-1 (0123456789ab)"},
	}
}
