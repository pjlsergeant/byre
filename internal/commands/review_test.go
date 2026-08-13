package commands

import (
	"encoding/base64"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/skills"
)

func grantTexts(lines []grantLine) string {
	var out []string
	for _, l := range lines {
		out = append(out, l.Text)
	}
	return strings.Join(out, "\n")
}

func TestGrantSummaryMarksDisabledMounts(t *testing.T) {
	got := grantTexts(grantSummary(config.Config{Mounts: []config.Mount{
		{Host: "/a", Target: "/a", Mode: "rw"},
		{Host: "/b", Target: "/b", Mode: "rw", Disabled: true},
	}}))
	if !strings.Contains(got, "/a->/a(rw)") {
		t.Errorf("active mount missing: %q", got)
	}
	// Applying a disabled mount plants an entry one flip away from a grant:
	// the reviewer must see it, marked, not have it hidden.
	if !strings.Contains(got, "/b->/b(rw, disabled)") {
		t.Errorf("disabled mount should be shown marked: %q", got)
	}
}

// A preset's [env] table reaches every box process and bakes into the
// image -- consented-to-but-not-authored content gets a summary line, not
// one unremarkable TOML line in a skimmable body (the review's preset-
// vector finding). Keys, sorted; an empty table adds nothing.
func TestGrantSummaryListsEnvKeys(t *testing.T) {
	got := grantTexts(grantSummary(config.Config{Env: map[string]string{"ZED": "1", "AAA": "2"}}))
	if !strings.Contains(got, "sets env in every box process") || !strings.Contains(got, "AAA, ZED") {
		t.Errorf("[env] keys must appear in the grant summary, sorted: %q", got)
	}
	if got := grantTexts(grantSummary(config.Config{})); strings.Contains(got, "sets env") {
		t.Errorf("no [env], no line: %q", got)
	}
}

// A credential row belongs in the summary — the reviewer must see THAT the
// row is there — but its payload does not: an age blob is hundreds of
// base64 characters, and one of them would push the rest of the consent gate
// off the screen. The scheme stays visible; the ciphertext elides.
func TestGrantSummaryElidesCredentialCiphertext(t *testing.T) {
	blob := strings.Repeat("QUJD", 200) // a plausible base64 wall
	got := grantTexts(grantSummary(config.Config{EnvFromHost: map[string]string{
		"STRIPE_KEY": config.EncryptedScheme + blob,
		"EDITOR":     "env:EDITOR",
	}}))
	if !strings.Contains(got, "STRIPE_KEY <- "+config.EncryptedScheme+"[…]") {
		t.Errorf("the scheme must stay visible: %q", got)
	}
	if strings.Contains(got, blob[:64]) {
		t.Errorf("the payload must never render: %q", got)
	}
	// Ordinary sources are short and read as written.
	if !strings.Contains(got, "EDITOR <- env:EDITOR") {
		t.Errorf("a plain passthrough is unchanged: %q", got)
	}
}

// The summary's charter (nothing smuggled unseen) covers every Grant class
// it owns: machine-scoped volumes — the shared-credential shape, and the only
// grant that crosses project scope — plus ports. Egress is the caller's per
// grantSummary's own doc (its live/inert status needs the resolved posture).
func TestGrantSummaryFlagsMachineVolumesAndPorts(t *testing.T) {
	lines := grantSummary(config.Config{
		Volumes: []config.Volume{
			{Name: "claude-identity", Role: "state", Target: "/x", Scope: "machine"},
			{Name: "cache", Role: "cache", Target: "/c"}, // per-project: quiet
		},
		Ports: []config.Port{{Container: 3000}, {Container: 8080, Host: 80, Interface: "0.0.0.0"}, {Container: 9999, Remove: true}},
	})
	got := grantTexts(lines)
	if !strings.Contains(got, `machine-scoped volume "claude-identity"`) || !strings.Contains(got, "every project on this machine") {
		t.Errorf("machine-scoped volume must be flagged loudly: %q", got)
	}
	if strings.Contains(got, `"cache"`) {
		t.Errorf("per-project volumes are the sandbox model, not a grant: %q", got)
	}
	var cross bool
	for _, l := range lines {
		if strings.Contains(l.Text, "claude-identity") && l.CrossProject {
			cross = true
		}
	}
	if !cross {
		t.Error("the machine-volume line must carry the cross-project emphasis flag")
	}
	if !strings.Contains(got, "binds host ports: 127.0.0.1:3000->3000, 0.0.0.0:80->8080") {
		t.Errorf("ports must be summarized (removal markers skipped): %q", got)
	}
	if strings.Contains(got, "9999") {
		t.Errorf("a removal marker grants nothing: %q", got)
	}
}

// Egress is summarized with its honest posture status, and never hidden even
// when the cascade can't be expanded.
func TestEgressGrantLineStatus(t *testing.T) {
	if got := grantTexts(egressGrantLine([]string{"a.com", "b.com:8443"}, "restricted", "firewall", true)); !strings.Contains(got, "live — skill \"firewall\" sets posture \"restricted\"") {
		t.Errorf("posture-live phrasing: %q", got)
	}
	if got := grantTexts(egressGrantLine([]string{"a.com"}, "", "", true)); !strings.Contains(got, "inert now") {
		t.Errorf("no-posture phrasing: %q", got)
	}
	// open-denylist leaves the network open: allowlist entries are inert
	// there, and the review must not dress them up as live grants (ADR 0030).
	if got := grantTexts(egressGrantLine([]string{"a.com"}, config.PostureOpenDenylist, "firewall-open", true)); !strings.Contains(got, "inert") || strings.Contains(got, "live — skill") {
		t.Errorf("open-denylist phrasing must read inert: %q", got)
	}
	if got := grantTexts(egressGrantLine([]string{"a.com"}, "", "", false)); !strings.Contains(got, "under a restrictive network posture") {
		t.Errorf("unknown-posture fallback phrasing: %q", got)
	}
	if lines := egressGrantLine(nil, "p", "s", true); lines != nil {
		t.Errorf("no entries — no line: %v", lines)
	}
}

func TestSkillGrantSummaryContainmentTopSorted(t *testing.T) {
	var sf skills.File
	sf.Runtime.Containment = "docker-host opens a containment hole -- skim docs"
	sf.Runtime.Mounts = []config.Mount{{Host: "/var/run/docker.sock", Target: "/var/run/docker.sock", Mode: "rw"}}
	sf.Runtime.SockGroups = []string{"/var/run/docker.sock"}
	sf.Volumes = []config.Volume{{Name: "id", Role: "state", Target: "/x", Scope: "machine"}}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "docker-host", File: sf}}}
	lines := skillGrantSummary(res)
	if len(lines) < 2 {
		t.Fatalf("expected containment + other grants: %+v", lines)
	}
	if !lines[0].Containment || !strings.Contains(lines[0].Text, "containment hole") {
		t.Fatalf("containment must be first: %+v", lines[0])
	}
	// After full sort, containment still tops cross-project.
	mixed := append([]grantLine{{Text: "plain"}, {Text: "machine", CrossProject: true}}, lines...)
	sorted := sortGrantLines(mixed)
	if !sorted[0].Containment {
		t.Fatalf("sortGrantLines containment first: %+v", sorted)
	}
	if !sorted[1].CrossProject {
		t.Fatalf("cross-project second: %+v", sorted)
	}
}

// A proposed mount or volume over a byre-managed path is a containment
// disclosure, not a storage row (ADR 0052): the review that asks for consent
// carries it at the same weight as a skill's declared hole, or the user
// answers before ever seeing it.
func TestShadowGrantLinesRideContainmentWeight(t *testing.T) {
	cfg := config.Config{
		Volumes: []config.Volume{{Name: "gate", Role: "state", Target: gen.ByreDir}},
		Mounts:  []config.Mount{{Host: "~/ok", Target: "/opt/data", Mode: "ro"}},
	}
	lines := shadowGrantLines(cfg, skills.Resolved{})
	if len(lines) != 1 {
		t.Fatalf("expected exactly the shadowing entry: %+v", lines)
	}
	if !lines[0].Containment || !strings.Contains(lines[0].Text, gen.ByreDir) {
		t.Fatalf("the shadow line must name the target at containment weight: %+v", lines[0])
	}
	if sorted := sortGrantLines(append([]grantLine{{Text: "plain"}}, lines...)); !sorted[0].Containment {
		t.Fatalf("containment sorts first in the review: %+v", sorted)
	}
	if l := shadowGrantLines(config.Config{Mounts: cfg.Mounts}, skills.Resolved{}); len(l) != 0 {
		t.Errorf("a harmless proposal discloses nothing: %+v", l)
	}
}

// MCP wiring is disclosed at preset-apply review with its carried reach
// spelled out per entry — skill contributions included and attributed, so a
// preset can't enable a skill whose wiring goes unseen at confirm time.
func TestMCPGrantLinesCarriedReach(t *testing.T) {
	decls := []skills.MCPDecl{
		{Skill: skills.MCPFromConfig, MCP: config.MCP{Name: "linear", URL: "https://mcp.linear.app/mcp", Egress: []string{"auth.linear.app"}, Env: []string{"LINEAR_API_KEY"}}},
		{Skill: "pete/tools", MCP: config.MCP{Name: "github", Command: []string{"gh-mcp", "stdio"}, Egress: []string{"api.github.com"}}},
	}
	got := grantTexts(mcpGrantLines(decls, nil))
	if !strings.Contains(got, "wires MCP server linear (config): remote https://mcp.linear.app/mcp (implies egress to mcp.linear.app:443); declared egress auth.linear.app; consumes env LINEAR_API_KEY") {
		t.Errorf("remote line wrong: %q", got)
	}
	if !strings.Contains(got, `wires MCP server github (skill "pete/tools"): local process gh-mcp stdio; declared egress api.github.com`) {
		t.Errorf("skill-attributed local line wrong: %q", got)
	}
	// A cross-source conflict is disclosed, not hidden (develop refuses it).
	if got := grantTexts(mcpGrantLines(nil, errAlwaysMCP{})); !strings.Contains(got, "mcp declarations conflict (develop will refuse)") {
		t.Errorf("conflict disclosure missing: %q", got)
	}
	// Fallback paths review the config layer only; closure markers grant
	// nothing and are skipped.
	fallback := configMCPDecls([]config.MCP{
		{Name: "!closed"},
		{Name: "kept", Command: []string{"srv"}},
	})
	if len(fallback) != 1 || fallback[0].MCP.Name != "kept" || fallback[0].Skill != skills.MCPFromConfig {
		t.Errorf("configMCPDecls = %+v", fallback)
	}
}

type errAlwaysMCP struct{}

func (errAlwaysMCP) Error() string { return "mcp github: declared by both the config and skill \"x\"" }

// reviewCredFile spells one physical config file's credential-relevant content, so a
// test states the two SIDES of an apply rather than the TOML that carries them.
func reviewCredFile(block string, rows map[string]string, env map[string]string) []byte {
	var b strings.Builder
	if block != "" {
		b.WriteString(block)
	}
	b.WriteString("[" + config.EnvFromHostTable + "]\n")
	for _, k := range slices.Sorted(maps.Keys(rows)) {
		fmt.Fprintf(&b, "%s = %q\n", k, rows[k])
	}
	b.WriteString("[env]\n")
	for _, k := range slices.Sorted(maps.Keys(env)) {
		fmt.Fprintf(&b, "%s = %q\n", k, env[k])
	}
	return []byte(b.String())
}

func credBlock(identity, recipient string) string {
	return fmt.Sprintf("[credentials]\nidentity = %q\nrecipient = %q\n\n", identity, recipient)
}

// Two identities the tests can tell apart. The block is only ever COMPARED
// here, so the bytes need to be well-formed, not usable.
var (
	credIdentityA  = base64.StdEncoding.EncodeToString([]byte("wrapped-identity-A"))
	credIdentityB  = base64.StdEncoding.EncodeToString([]byte("wrapped-identity-B"))
	credRecipientA = "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"
	credRecipientB = "age1lggyhqrw2nlhcxprm67z43rta597azn8gknawjehu9d9dl0jq3yqqvfafg"
)

func credRow(payload string) string { return config.EncryptedScheme + payload }

// The whole point of the annotation: a credential value that CHANGED between
// the version in the store and the version being applied. Neither side is
// readable -- the ciphertext elides, and byre cannot tell a rotation the user
// performed from a blob swapped in by whatever wrote the repo -- so the line
// asks the one question that separates them.
func TestCredentialReviewFlagsAChangedValue(t *testing.T) {
	blob := strings.Repeat("QUJD", 200)
	before := reviewCredFile("", map[string]string{"STRIPE_KEY": credRow(blob), "EDITOR": "env:EDITOR"}, nil)
	after := reviewCredFile("", map[string]string{"STRIPE_KEY": credRow("Zm9v"), "EDITOR": "env:EDITOR"}, nil)
	lines := credentialReviewLines(before, after)
	got := grantTexts(lines)
	if !strings.Contains(got, "STRIPE_KEY: credential value changed") || !strings.Contains(got, "didn't rotate this credential, reject") {
		t.Fatalf("the value-change rule did not fire: %q", got)
	}
	if strings.Contains(got, blob[:64]) {
		t.Fatalf("the payload rendered: %q", got)
	}
	if !strings.Contains(got, config.EncryptedScheme+"[…] -> "+config.EncryptedScheme+"[…]") {
		t.Fatalf("both sides must render elided: %q", got)
	}
	// An unrelated passthrough is not this annotation's business.
	if strings.Contains(got, "EDITOR") {
		t.Fatalf("a non-credential row was annotated: %q", got)
	}
	for _, l := range lines {
		if !l.Credential {
			t.Fatalf("line %q does not carry the credential weight", l.Text)
		}
	}
}

// EITHER side, which is what stops the classifier being dodged: replacing a
// credential row with an ordinary scheme (or with an [env] literal, which
// takes the key out of env_from_host entirely) changes the same delivered
// value, and a rule that only looked at the NEW side would wave both through.
func TestCredentialReviewFlagsEitherSide(t *testing.T) {
	cred := credRow("Zm9v")
	cases := []struct {
		name          string
		before, after []byte
	}{
		{"credential replaced by a passthrough",
			reviewCredFile("", map[string]string{"STRIPE_KEY": cred}, nil),
			reviewCredFile("", map[string]string{"STRIPE_KEY": "env:STRIPE_KEY"}, nil)},
		{"credential replaced by an [env] literal",
			reviewCredFile("", map[string]string{"STRIPE_KEY": cred}, nil),
			reviewCredFile("", nil, map[string]string{"STRIPE_KEY": "sk-live-attacker"})},
		{"credential disabled with the empty override",
			reviewCredFile("", map[string]string{"STRIPE_KEY": cred}, nil),
			reviewCredFile("", map[string]string{"STRIPE_KEY": ""}, nil)},
		{"passthrough replaced by a credential",
			reviewCredFile("", map[string]string{"STRIPE_KEY": "env:STRIPE_KEY"}, nil),
			reviewCredFile("", map[string]string{"STRIPE_KEY": cred}, nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := grantTexts(credentialReviewLines(tc.before, tc.after))
			if !strings.Contains(got, "STRIPE_KEY: credential value changed") || !strings.Contains(got, "didn't rotate this credential, reject") {
				t.Fatalf("the value-change rule did not fire: %q", got)
			}
			if strings.Contains(got, "sk-live-attacker") {
				t.Fatalf("an [env] literal's VALUE rendered: %q", got)
			}
		})
	}
}

// A row that appears or vanishes is worth saying — the reader should know a
// credential arrived or left — but it is not the loud one: "if you didn't
// rotate this" is a question about a key that was already here.
func TestCredentialReviewNamesAppearedAndVanishedRows(t *testing.T) {
	cred := credRow("Zm9v")
	appeared := grantTexts(credentialReviewLines(reviewCredFile("", nil, nil), reviewCredFile("", map[string]string{"STRIPE_KEY": cred}, nil)))
	if !strings.Contains(appeared, "STRIPE_KEY: credential row appeared") {
		t.Fatalf("an appearing row is not named: %q", appeared)
	}
	if strings.Contains(appeared, "rotate") {
		t.Fatalf("a new row must not carry the value-change wording: %q", appeared)
	}
	vanished := grantTexts(credentialReviewLines(reviewCredFile("", map[string]string{"STRIPE_KEY": cred}, nil), reviewCredFile("", nil, nil)))
	if !strings.Contains(vanished, "STRIPE_KEY: credential row vanished") {
		t.Fatalf("a vanishing row is not named: %q", vanished)
	}
	if strings.Contains(vanished, "rotate") {
		t.Fatalf("a removed row must not carry the value-change wording: %q", vanished)
	}
}

// The [credentials] block is invisible to every other surface here -- Parse
// drops it deliberately, so it reaches no Config and no grant line -- and it is
// the strongest move a preset can make: whatever identity it lands is what
// opens the file's rows and what every later `set` encrypts to.
func TestCredentialReviewFlagsBlockChanges(t *testing.T) {
	mine := credBlock(credIdentityA, credRecipientA)
	theirs := credBlock(credIdentityB, credRecipientB)
	rows := map[string]string{"STRIPE_KEY": credRow("Zm9v")}

	replaced := grantTexts(credentialReviewLines(reviewCredFile(mine, rows, nil), reviewCredFile(theirs, rows, nil)))
	if !strings.Contains(replaced, "replaces the file's credentials identity") || !strings.Contains(replaced, "if you didn't do this, reject") {
		t.Fatalf("the block-replacement rule did not fire: %q", replaced)
	}
	// The values did not move; only the identity did. That must not be
	// reported as a value change too.
	if strings.Contains(replaced, "credential value changed") {
		t.Fatalf("an unchanged row was reported as changed: %q", replaced)
	}
	// A recipient swap under the SAME identity is the same move: values set
	// afterward encrypt to whatever the recipient names.
	recip := grantTexts(credentialReviewLines(reviewCredFile(credBlock(credIdentityA, credRecipientA), rows, nil), reviewCredFile(credBlock(credIdentityA, credRecipientB), rows, nil)))
	if !strings.Contains(recip, "replaces the file's credentials identity") {
		t.Fatalf("a recipient swap did not fire: %q", recip)
	}

	arrived := grantTexts(credentialReviewLines(reviewCredFile("", nil, nil), reviewCredFile(theirs, rows, nil)))
	if !strings.Contains(arrived, "brings its own credentials identity") || !strings.Contains(arrived, "if you didn't do this, reject") {
		t.Fatalf("an arriving block is not named: %q", arrived)
	}
	removed := grantTexts(credentialReviewLines(reviewCredFile(mine, rows, nil), reviewCredFile("", rows, nil)))
	if !strings.Contains(removed, "removes the file's credentials identity") {
		t.Fatalf("a removed block is not named: %q", removed)
	}
}

// Silence is the contract on the other side: this annotation rides the ⚠ list,
// and a line that fires when nothing moved trains the reader to skip the one
// place byre says a credential changed.
func TestCredentialReviewIsSilentWithoutAChange(t *testing.T) {
	same := reviewCredFile(credBlock(credIdentityA, credRecipientA),
		map[string]string{"STRIPE_KEY": credRow("Zm9v"), "EDITOR": "env:EDITOR"},
		map[string]string{"TERM": "xterm"})
	if lines := credentialReviewLines(same, same); len(lines) != 0 {
		t.Fatalf("an unchanged file was annotated: %q", grantTexts(lines))
	}
	// And a change to something that is not a credential stays the ordinary
	// review's business.
	other := reviewCredFile(credBlock(credIdentityA, credRecipientA),
		map[string]string{"STRIPE_KEY": credRow("Zm9v"), "EDITOR": "env:VISUAL"},
		map[string]string{"TERM": "xterm"})
	if lines := credentialReviewLines(same, other); len(lines) != 0 {
		t.Fatalf("a non-credential change was annotated: %q", grantTexts(lines))
	}
}

// A side byre cannot read is not a side with no credentials: saying nothing
// would read as "nothing moved", which is the one thing this annotation exists
// to stop.
func TestCredentialReviewDegradesOnAnUnreadableSide(t *testing.T) {
	good := reviewCredFile("", map[string]string{"STRIPE_KEY": credRow("Zm9v")}, nil)
	bad := []byte("[credentials]\nidentity = 7\n")
	got := grantTexts(credentialReviewLines(good, bad))
	if !strings.Contains(got, "could not compare this file's credentials") || !strings.Contains(got, "NOT shown") {
		t.Fatalf("an unreadable side was passed over in silence: %q", got)
	}
}

// The lines have to REACH the gate: the review is the whole reason they exist,
// and they cannot come off the resolved proposal the rest of the summary is
// built from (the block never reaches a Config, and a diff needs both sides).
func TestPresetReviewCarriesTheCredentialAnnotation(t *testing.T) {
	paths, _ := testPaths(t)
	store := reviewCredFile(credBlock(credIdentityA, credRecipientA), map[string]string{"STRIPE_KEY": credRow("Zm9v")}, nil)
	content := reviewCredFile(credBlock(credIdentityB, credRecipientB), map[string]string{"STRIPE_KEY": credRow("YmFy")}, nil)
	s, _, errBuf := testStreams("", true)
	renderPresetReview(s, paths, config.Config{}, content, nil, "Apply", store, true)
	out := errBuf.String()
	if !strings.Contains(out, "STRIPE_KEY: credential value changed") {
		t.Fatalf("the value-change line never reached the gate:\n%s", out)
	}
	if !strings.Contains(out, "replaces the file's credentials identity") {
		t.Fatalf("the block line never reached the gate:\n%s", out)
	}
	// Same weight as a containment hole and a machine volume: on a TTY it is
	// emphasized, or it sits grey among the rows a reader skims.
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "credential value changed") && !strings.Contains(l, "\x1b[1;33m") {
			t.Fatalf("the credential line is not emphasized: %q", l)
		}
	}
}
