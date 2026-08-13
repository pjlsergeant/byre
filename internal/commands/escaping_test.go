package commands

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
	"github.com/pjlsergeant/byre/internal/testtools"
)

// The arm for the reporting surfaces that print byre's own words around
// strings byre did not author: status (its rows and its develop-time
// warnings), the exit report, the self-edit report, and skill inspect. None of
// them emits ANSI of its own, so the contract is exact -- no raw ESC survives
// into the output, whatever a config, a skill manifest or a walked filename
// carries. Deliberately per-surface and never package-wide: preset's TTY grant
// highlight and clipboard's OSC 52 write escapes on purpose.
const (
	// escCSI erases the reported line and rewinds a row: the shape that makes
	// a report claim something other than what byre wrote.
	escCSI = "\x1b[2K\x1b[A"
	// escOSC is an OSC 52 clipboard write: reporting as an exfiltration verb.
	escOSC = "\x1b]52;c;cGF5bG9hZA==\a"
)

// assertNoESC fails when a surface let a raw ESC through, naming the byte
// offset so the offending line is findable in a long report.
func assertNoESC(t *testing.T, surface, out string) {
	t.Helper()
	if i := strings.IndexByte(out, 0x1b); i >= 0 {
		t.Errorf("%s printed a raw ESC at byte %d: %q", surface, i, out)
	}
}

// assertKept fails when the strip ate the value along with the escape: these
// surfaces report data, and a censored row is as useless as a forged one.
func assertKept(t *testing.T, surface, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("%s dropped %q from its output: %q", surface, w, out)
		}
	}
}

// statusPayloads is every externally-sourced field of a status render carrying
// the same payload, so two renders differing only in the payload have exactly
// the same rows -- the framing assertion below compares their line counts.
func statusPayloads(p string) statusInfo {
	return statusInfo{
		ID:         "proj" + p,
		Agent:      "claude" + p,
		Template:   "acme/go" + p,
		Chain:      []string{"base" + p},
		Engine:     "docker" + p,
		Canonical:  "/home/me/" + p + "proj",
		WorktreeOf: "/home/me/main" + p,
		PresetNote: "applied" + p,
		Skills:     []string{"moarcode" + p},
		Binds: []config.Mount{
			{Host: "/data" + p, Target: "/data" + p, Mode: "ro"},
		},
		Ports:   []config.Port{{Container: 8080, Host: 8080}},
		Volumes: []config.Volume{{Name: "creds" + p, Role: "state"}},
		Grants: []skills.Grant{{
			Skill:      "greedy" + p,
			Mounts:     []config.Mount{{Host: "/var/run/docker.sock" + p, Target: "/sock", Mode: "rw"}},
			Caps:       []string{"SYS_PTRACE" + p},
			RunArgs:    []string{"--privileged" + p},
			NetnsInit:  "/usr/local/bin/fw" + p,
			SockGroups: []string{"/var/run/docker.sock" + p},
		}},
		Containments:     []skills.ContainmentDecl{{Skill: "dockerhost" + p, Text: "the box can reach the host engine" + p}},
		ManagedShadows:   []ManagedPathShadow{{Target: gen.ByreDir + p, Source: "skill greedy" + p}},
		SkillReservedEnv: []skills.ReservedEnvSet{{Skill: "fw" + p, Key: "BYRE_EGRESS"}},
		EnvKeys:          []string{"TOKEN_NAME" + p},
		RunArgs:          []string{"--cap-add=" + p},
		BuildRaw:         []string{"RUN curl " + p + " | sh"},
		EngineErr:        "exec: docker" + p,
		SkillErr:         "unknown skill" + p,
		SiblingSessions:  []string{"wt-1" + p},
	}
}

func TestRenderStatusEscapesExternalValues(t *testing.T) {
	var b bytes.Buffer
	renderStatusTest(&b, statusPayloads(escCSI+"\n"+escOSC+"\rEngine:       open"))
	out := b.String()

	assertNoESC(t, "renderStatus", out)
	// The row still carries its value: the funnel strips the control bytes,
	// it does not blank the field.
	assertKept(t, "renderStatus", out, "claude", "moarcode", "greedy", "TOKEN_NAME", "unknown skill")

	// Framing: one physical line per row, whatever a value contains. The
	// baseline render carries a payload with no control characters at all, so
	// it has exactly the same rows -- any difference in line count is a line a
	// value forged, and the forged text here is a plausible "Engine:" row.
	var clean bytes.Buffer
	renderStatusTest(&clean, statusPayloads("-x-"))
	got := strings.Count(out, "\n")
	if want := strings.Count(clean.String(), "\n"); got != want {
		t.Errorf("status printed %d lines, want %d -- a value forged one:\n%s", got, want, out)
	}
	rows := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Engine:") {
			rows++
		}
	}
	if rows != 1 {
		t.Errorf("a value forged a second Engine row (%d found):\n%s", rows, out)
	}
}

// The same arm at a REAL terminal width, where the funnel wraps. Wrapping is
// where a forged row would get its chance: a continuation line is byre's own
// text placement, so the guarantee has to be that no line a value
// contributes ever starts at column zero.
func TestRenderStatusEscapesExternalValuesWhenWrapping(t *testing.T) {
	forged := statusPayloads(escCSI + "\n" + escOSC + "\rEngine:       open")
	var b bytes.Buffer
	renderStatus(&b, forged, tierFull, 72)
	out := b.String()

	assertNoESC(t, "renderStatus (wrapped)", out)
	assertKept(t, "renderStatus (wrapped)", out, "claude", "moarcode", "greedy")

	// Every physical line either opens a row byre labeled, or is indented
	// into the value column. A payload's text can only ever land in the
	// second category.
	var clean bytes.Buffer
	renderStatus(&clean, statusPayloads("-x-"), tierFull, 72)
	starts := func(s string) int {
		n := 0
		for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
			if l != "" && !strings.HasPrefix(l, " ") {
				n++
			}
		}
		return n
	}
	if got, want := starts(out), starts(clean.String()); got != want {
		t.Errorf("wrapped render started %d rows, want %d -- a value forged one:\n%s", got, want, out)
	}
	rows := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Engine:") {
			rows++
		}
	}
	if rows != 1 {
		t.Errorf("a wrapped value forged a second Engine row (%d found):\n%s", rows, out)
	}
}

// --data reaches the same terminal. The JSON encoder is what holds the line
// there -- it writes a control byte as \uXXXX rather than passing it through
// -- so the contract is identical even though the mechanism is not.
func TestStatusDataEscapesExternalValues(t *testing.T) {
	var b bytes.Buffer
	if err := writeStatusData(&b, statusPayloads(escCSI+"\n"+escOSC+"\rEngine: open")); err != nil {
		t.Fatalf("writeStatusData: %v", err)
	}
	out := b.String()
	assertNoESC(t, "status --data", out)
	assertKept(t, "status --data", out, "claude", "moarcode", "greedy", "TOKEN_NAME")
}

func TestStatusDevelopWarningsEscapeExternalValues(t *testing.T) {
	// The develop-time warnings sit OUTSIDE renderStatus, so the row funnel
	// does not cover them: a skill's netns script path and a skill's name are
	// both manifest-authored and both reach the terminal here.
	var fw skills.File
	fw.Runtime.NetnsInit = "/usr/local/bin/" + escCSI + "byre-firewall"
	res := skills.Resolved{Skills: []skills.Skill{{Name: "firewall" + escOSC, File: fw}}}

	var guard bytes.Buffer
	warnGuardCollisions(&guard, config.Config{Files: map[string]string{
		"evil": "/usr/local/bin/" + escCSI + "byre-firewall",
	}}, res)
	assertNoESC(t, "warnGuardCollisions", guard.String())
	assertKept(t, "warnGuardCollisions", guard.String(), "byre-managed security path", "byre-firewall")

	var shadow bytes.Buffer
	warnManagedPathShadows(&shadow, config.Config{}, skills.Resolved{Skills: []skills.Skill{{
		Name: "greedy" + escOSC,
		File: skills.File{Volumes: []config.Volume{{Name: "gv", Role: "state", Target: gen.LauncherPath}}},
	}}})
	assertNoESC(t, "warnManagedPathShadows", shadow.String())
	assertKept(t, "warnManagedPathShadows", shadow.String(), "cannot re-assert over a runtime mount", "greedy")
}

func TestExitReportEscapesWatchedValues(t *testing.T) {
	// The watched files are the agent's to write: a hook filename, a
	// core.hooksPath value, an .env key name.
	empty := exitSnapshot{
		hooks: map[string]string{}, config: map[string]map[string]string{},
		env: map[string]map[string]string{}, hooksWalked: true, configListed: true,
		envListed: true, unreadable: map[string]bool{}, configFromListing: map[string]bool{},
	}
	after := exitSnapshot{
		hooks:  map[string]string{".git/hooks/pre-commit" + escCSI: "sig"},
		config: map[string]map[string]string{".git/config": {"core.hookspath": ".husky/_" + escOSC}},
		env: map[string]map[string]string{
			".env": {"API_KEY" + escCSI: "value"},
		},
		hooksWalked: true, configListed: true, envListed: true,
		unreadable: map[string]bool{}, configFromListing: map[string]bool{},
	}
	var b bytes.Buffer
	reportExit(&b, empty, after)
	out := b.String()

	assertNoESC(t, "reportExit", out)
	assertKept(t, "reportExit", out, "pre-commit", ".husky/_", "API_KEY")
	// The report is line-framed: three findings, plus the two header lines.
	if got := strings.Count(strings.TrimSuffix(out, "\n"), "\n") + 1; got != 5 {
		t.Errorf("expected 5 report lines, got %d: %q", got, out)
	}
}

func TestSelfEditReportEscapesStoreContent(t *testing.T) {
	// The self-edit store is agent-writable by definition -- the config BODY
	// is the site that matters, since the diff prints its bytes back.
	dir := t.TempDir()
	cfg := filepath.Join(dir, "byre.config")
	mustWriteFile(t, cfg, []byte("base = \"node:22\"\n"), 0o644)

	planted := filepath.Join(dir, "context", "planted"+escCSI+".sh")
	mustMkdirAll(t, filepath.Dir(planted), 0o755)

	out := report(t, dir, func() {
		mustWriteFile(t, cfg, []byte("base = \"node:22\"\nagent = \""+escOSC+"claude\"\n"), 0o644)
		if err := os.WriteFile(planted, []byte("#!/bin/sh\n"), 0o644); err != nil {
			testtools.Unavailable(t, "control characters in filenames", err)
		}
	})

	assertNoESC(t, "reportSelfEditChanges", out)
	assertKept(t, "reportSelfEditChanges", out, `+agent = "`, "planted", ".sh")
}

// skillContributions renders the contribution block over a manifest carrying
// the same payload in every field the block prints as TEXT -- inspect is the
// PRE-trust surface, so each of these strings is an author byre has not
// vouched for. The block's remaining output is counts byre computes itself
// (apt, dockerfile lines, the files total), which no manifest can author.
func skillContributions(payload string) string {
	var f skills.File
	f.Agent = &skills.AgentContrib{Command: "run --wild" + payload, State: "state" + payload}
	f.CompanionFor = "acme/agent" + payload
	// companion_for and shared_auth_for are mutually exclusive to the PARSER,
	// but the renderer prints whichever it is handed -- and this arm is about
	// the renderer, so both rows are exercised in one pass.
	f.SharedAuthFor = "acme/agent" + payload
	f.Volumes = []config.Volume{{
		Name: "creds" + payload, Role: "state" + payload,
		Scope: "machine" + payload, Target: "/home/dev/.acme" + payload,
	}}
	// Remote and local MCPs are different branches printing different values,
	// so the arm needs one of each -- the local one is the only place the
	// server's argv reaches the terminal.
	f.MCPs = []config.MCP{{
		Name:    "gh" + payload,
		URL:     "https://mcp.example" + payload,
		Env:     []string{"GITHUB_TOKEN" + payload},
		Egress:  []string{"auth.example" + payload + ":443"},
		Headers: map[string]string{"Authorization" + payload: "Bearer ${TOKEN}" + payload},
	}, {
		Name:    "local" + payload,
		Command: []string{"acme-mcp" + payload, "stdio" + payload},
	}}
	f.Runtime.Mounts = []config.Mount{{
		Host: "/var/run/docker.sock" + payload, Target: "/sock" + payload,
		Mode: "rw" + payload,
	}}
	f.Runtime.Caps = []string{"SYS_PTRACE" + payload}
	f.Runtime.RunArgs = []string{"--privileged" + payload}
	f.Runtime.NetnsInit = "/usr/local/bin/fw" + payload
	f.Runtime.NetworkPosture = "deny-by-default" + payload
	f.Runtime.SockGroups = []string{"/var/run/docker.sock" + payload}
	f.Runtime.Egress = []string{"api.example" + payload + ":443"}
	f.Runtime.EgressOffered = []string{"offered.example" + payload + ":443"}
	f.Runtime.Env = map[string]string{"TOKEN_NAME" + payload: "value" + payload}
	f.Runtime.EnvDocs = map[string]string{"NGROK" + payload: "get one at example.com" + payload}
	f.Runtime.Containment = "reaches the host engine" + payload
	f.Build.Files = map[string]string{
		"payload" + payload + ".sh": "/usr/local/bin/payload",
		"notes" + payload + ".md":   "/opt/notes.md",
	}
	f.Context.File = "context" + payload + ".md"
	var b bytes.Buffer
	printSkillContributions(&b, f)
	return b.String()
}

func TestSkillInspectEscapesManifestValues(t *testing.T) {
	out := skillContributions(escCSI + "\n  cap: SYS_ADMIN\r" + escOSC)

	assertNoESC(t, "printSkillContributions", out)
	assertKept(t, "printSkillContributions", out, "files: 2", "payload", "notes", "SYS_PTRACE",
		"Bearer", "deny-by-default", "sock_groups", "egress_offered", "shared_auth_for", "acme-mcp")

	// Framing: one physical line per contribution, whatever a manifest value
	// contains. The clean render carries the same fields with a payload that
	// frames nothing, so any extra line is one a value forged.
	clean := skillContributions("\x01\x02")
	if got, want := strings.Count(out, "\n"), strings.Count(clean, "\n"); got != want {
		t.Errorf("printSkillContributions wrote %d lines, want %d -- a manifest value forged one:\n%s", got, want, out)
	}

	// The template half of inspect renders the same keys from a template body.
	var tb bytes.Buffer
	printTemplateShape(&tb, []byte("base = \"node:22\"\n\n[files]\n\"payload\\u001B[2K.sh\" = \"/usr/local/bin/payload\"\n"))
	assertNoESC(t, "printTemplateShape", tb.String())
	assertKept(t, "printTemplateShape", tb.String(), "files: 1", "payload")
}

// templateShape renders the template half over a body whose every shown key
// carries payload -- a template is a package like any other, so its bytes are
// its author's, not byre's.
func templateShape(payload string) string {
	var b bytes.Buffer
	printTemplateShape(&b, []byte(`base = "node:22`+payload+`"
engine = "docker`+payload+`"
apt = ["curl`+payload+`", "!wget`+payload+`"]
egress = ["api.example`+payload+`:443"]
worktree_base = "main`+payload+`"

[env]
"TOKEN`+payload+`" = "value`+payload+`"

[env_from_host]
"HOST_KEY`+payload+`" = "env:SOURCE`+payload+`"

[files]
"payload`+payload+`.sh" = "/usr/local/bin/payload"

[[mounts]]
host = "/host`+payload+`"
target = "/target`+payload+`"
mode = "rw"

[[volumes]]
name = "vol`+payload+`"
role = "state"
target = "/data`+payload+`"
seed = { host = "/seed`+payload+`" }

[[ports]]
interface = "127.0.0.1"
host = 15432
container = 5432
`))
	return b.String()
}

func TestTemplateShapeEscapesTemplateValues(t *testing.T) {
	out := templateShape(tomlPayload(escCSI + "\n  base: node:20\r" + escOSC))

	assertNoESC(t, "printTemplateShape", out)
	assertKept(t, "printTemplateShape", out, "node:22", "curl", "removes apt", "seed host=", "15432")

	clean := templateShape(tomlPayload("\x01\x02"))
	if got, want := strings.Count(out, "\n"), strings.Count(clean, "\n"); got != want {
		t.Errorf("printTemplateShape wrote %d lines, want %d -- a template value forged one:\n%s", got, want, out)
	}
}

// tomlPayload renders a payload for a TOML basic string: the parser hands the
// same bytes back, but a control character cannot sit raw in a manifest file.
func tomlPayload(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		case r == '"' || r == '\\':
			b.WriteRune('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// storeSkills lays a store down with two local skills: one that loads, and one
// the catalog REFUSES -- whose refusal reason quotes the offending manifest
// value straight back at the terminal. The payload is the same in both.
func storeSkills(t *testing.T, payload string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	write := func(name, body string) {
		dir := filepath.Join(home, "skills", "pete", name)
		mustMkdirAll(t, dir, 0o755)
		mustWriteFile(t, filepath.Join(dir, "skill.toml"), []byte(body), 0o644)
	}
	write("ok", `description = "does things `+payload+`"

[package]
id = "pete/ok"
kind = "skill"
version = "1.2.3`+payload+`"
`)
	write("bad", `[[runtime.mounts]]
host = "/x"
target = "relative`+payload+`"
`)
	return home
}

// packageListing is the catalog listing: byre's own columns around a
// description and a refusal reason it did not author.
func packageListing(t *testing.T, payload string) string {
	t.Helper()
	storeSkills(t, payload)
	s, out, _ := testStreams("", false)
	if err := PackageList(s, packages.KindSkill); err != nil {
		t.Fatalf("skill list failed: %v", err)
	}
	// The listing only: the store-ensure notices go to stderr, and they fire
	// once per process, which would make the two runs incomparable.
	return out.String()
}

func TestPackageListEscapesManifestValues(t *testing.T) {
	out := packageListing(t, tomlPayload(escCSI+"\npete/innocent                 local             fine\r"+escOSC))

	assertNoESC(t, "skill list", out)
	assertKept(t, "skill list", out, "pete/ok", "does things", "pete/bad", "INVALID")

	clean := packageListing(t, tomlPayload("\x01\x02"))
	if got, want := strings.Count(out, "\n"), strings.Count(clean, "\n"); got != want {
		t.Errorf("skill list printed %d lines, want %d -- a manifest value forged a row:\n%s", got, want, out)
	}
}

// packageInspect is the per-package page: the same manifest values in byre's
// labeled rows, plus the refused package's Status row.
func packageInspect(t *testing.T, payload string) string {
	t.Helper()
	storeSkills(t, payload)
	var b bytes.Buffer
	for _, id := range []string{"pete/ok", "pete/bad"} {
		s, out, _ := testStreams("", false)
		if err := PackageInspect(s, packages.KindSkill, id); err != nil {
			t.Fatalf("skill inspect %s failed: %v", id, err)
		}
		b.WriteString(out.String())
	}
	return b.String()
}

func TestPackageInspectEscapesManifestValues(t *testing.T) {
	out := packageInspect(t, tomlPayload(escCSI+"\nProvenance:  bundled\r"+escOSC))

	assertNoESC(t, "skill inspect", out)
	assertKept(t, "skill inspect", out, "pete/ok", "1.2.3", "does things", "pete/bad")

	clean := packageInspect(t, tomlPayload("\x01\x02"))
	if got, want := strings.Count(out, "\n"), strings.Count(clean, "\n"); got != want {
		t.Errorf("skill inspect printed %d lines, want %d -- a manifest value forged a row:\n%s", got, want, out)
	}
	// The row a payload aimed at: exactly one Provenance line per package.
	rows := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Provenance:") {
			rows++
		}
	}
	if rows != 2 {
		t.Errorf("a manifest value forged a Provenance row (%d found, want 2):\n%s", rows, out)
	}
}

// --- the funnel surfaces: install, layer, preset ---
//
// These three print through dataf, which escapes each ARGUMENT and leaves
// byre's own format string to do the framing. The preset review is the one
// place in the package that means to emit ANSI (the TTY highlight on a
// containment grant), and it says so by passing escaped() -- so its arm asserts
// the highlight SURVIVES while everything around it is data.

// TestDatafRendersArgumentsAsData pins the funnel itself.
func TestDatafRendersArgumentsAsData(t *testing.T) {
	var b bytes.Buffer
	dataf(&b, "byre: %s is contested: %s\n", "acme/x"+escCSI, errors.New("two claimants\nbyre: all fine"))
	out := b.String()

	if n := strings.Count(out, "\n"); n != 1 {
		t.Errorf("dataf wrote %d lines, want 1 -- an argument framed one: %q", n, out)
	}
	if strings.IndexByte(out, 0x1b) >= 0 {
		t.Errorf("dataf printed a raw ESC: %q", out)
	}
	if !strings.Contains(out, "acme/x") || !strings.Contains(out, "two claimants") {
		t.Errorf("dataf dropped an argument: %q", out)
	}

	// escaped() is the exemption, and it is total: what a composer already
	// rendered passes through, styling included.
	var styled bytes.Buffer
	dataf(&styled, "  ⚠ %s\n", escaped("\x1b[1;33mgrant\x1b[0m"))
	if styled.String() != "  ⚠ \x1b[1;33mgrant\x1b[0m\n" {
		t.Errorf("escaped() must pass through untouched, got %q", styled.String())
	}
}

// installSummary renders the install grant summary and the reference block --
// the two composers install prints, both fed from manifest and store bytes.
func installSummary(t *testing.T, payload string) string {
	t.Helper()
	var b bytes.Buffer
	printAcquiredSummary(&b, &packages.Acquired{
		Core: packages.Manifest{
			ID:          "acme/tool" + payload,
			Version:     "1.2.3" + payload,
			Description: "does things" + payload,
		},
		Kind:   packages.KindSkill,
		Digest: "abc123def456",
	})
	b.WriteString(renderRefHits([]refHit{
		{Where: "~/.byre/projects/p" + payload},
		{Where: "~/.byre/projects/q" + payload, Path: "/store/q" + payload, Guarded: true},
	}))
	return b.String()
}

func TestInstallReviewEscapesManifestValues(t *testing.T) {
	out := installSummary(t, escCSI+"\nPackage: acme/innocent 9.9.9\r"+escOSC)

	if i := strings.IndexByte(out, 0x1b); i >= 0 {
		t.Errorf("the install review printed a raw ESC at byte %d: %q", i, out)
	}
	clean := installSummary(t, "\x01\x02")
	if got, want := strings.Count(out, "\n"), strings.Count(clean, "\n"); got != want {
		t.Errorf("the install review printed %d lines, want %d -- a manifest value forged one:\n%s", got, want, out)
	}
	for _, want := range []string{"acme/tool", "1.2.3", "does things", "could not read"} {
		if !strings.Contains(out, want) {
			t.Errorf("the install review dropped %q: %q", want, out)
		}
	}
}

// layerListing lists layers whose DIRECTORY names carry payload -- names byre
// never authored, since anything can drop a directory in the layers dir.
func layerListing(t *testing.T, payload string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	writeLayerFile(t, home, "steady"+payload, "base = \"node:22\"\n")
	writeLayerFile(t, home, "broken"+payload, "base = [oops\n")
	s, out, _ := testStreams("", false)
	if err := LayerList(s); err != nil {
		t.Fatalf("layer list failed: %v", err)
	}
	// The listing only: the store-ensure notices on stderr fire once per
	// process, so counting them would make the two runs incomparable.
	return out.String()
}

func TestLayerListEscapesLayerNames(t *testing.T) {
	out := layerListing(t, escCSI+"\nsteady  extends nothing\r"+escOSC)

	if i := strings.IndexByte(out, 0x1b); i >= 0 {
		t.Errorf("layer list printed a raw ESC at byte %d: %q", i, out)
	}
	clean := layerListing(t, "\x01\x02")
	if got, want := strings.Count(out, "\n"), strings.Count(clean, "\n"); got != want {
		t.Errorf("layer list printed %d lines, want %d -- a directory name forged one:\n%s", got, want, out)
	}
	if !strings.Contains(out, "steady") || !strings.Contains(out, "BROKEN") {
		t.Errorf("layer list dropped a row: %q", out)
	}
}

// presetReview renders the consent review over a preset carrying payload in
// every field it shows, against a stored config so the diff half runs too.
// The body payload stays free of line breaks on purpose: the review DIFFS the
// preset file, and a file that really does have two lines really does diff as
// two lines -- that is content, not a forged report line.
func presetReview(t *testing.T, payload, body string, tty bool) string {
	t.Helper()
	paths, _ := testPaths(t)
	preset := config.Config{
		Base:  "node:22" + payload,
		Agent: "claude" + payload,
		Mounts: []config.Mount{
			// A mount over a byre-managed path: a containment grant, which is
			// what the TTY highlight is for.
			{Host: "/host" + payload, Target: "/usr/local/bin", Mode: "rw"},
		},
	}
	content := []byte("base = \"node:22" + body + "\"\n")
	store := []byte("base = \"node:20\"\n")
	s, _, errBuf := testStreams("", tty)
	renderPresetReview(s, paths, preset, content,
		[]missingRef{{Name: "acme/x" + payload, Kind: packages.KindSkill}},
		"Inspect", store, true)
	return errBuf.String()
}

func TestPresetReviewEscapesPresetValues(t *testing.T) {
	payload := escCSI + "\n  ⚠ nothing to see here\r" + escOSC
	out := presetReview(t, payload, escCSI+escOSC, false)

	if i := strings.IndexByte(out, 0x1b); i >= 0 {
		t.Errorf("the preset review printed a raw ESC at byte %d: %q", i, out)
	}
	clean := presetReview(t, "\x01\x02", "\x01\x02", false)
	if got, want := strings.Count(out, "\n"), strings.Count(clean, "\n"); got != want {
		t.Errorf("the preset review printed %d lines, want %d -- a preset value forged a grant row:\n%s", got, want, out)
	}
	for _, want := range []string{"node:22", "acme/x", "not installed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the preset review dropped %q: %q", want, out)
		}
	}
}

// TestPresetReviewKeepsItsOwnHighlight is the other side of the same rule: the
// funnel must not strip the styling byre applies deliberately, or the loudest
// row in the consent review goes quiet.
func TestPresetReviewKeepsItsOwnHighlight(t *testing.T) {
	out := presetReview(t, escCSI, escCSI, true)

	if !strings.Contains(out, "\x1b[1;33m") {
		t.Fatalf("the TTY containment highlight vanished:\n%q", out)
	}
	// Every escape in the output belongs to byre's own highlight pair; a
	// preset-supplied one would break the count.
	if got, want := strings.Count(out, "\x1b"), strings.Count(out, "\x1b[1;33m")+strings.Count(out, "\x1b[0m"); got != want {
		t.Errorf("%d escapes present, %d accounted for by the highlight:\n%q", got, want, out)
	}
}

// The single-writer refusal prints strings byre did not author: the engine's
// own error text, and the `byre.workdir` label off a CONTAINER -- and a
// container carrying byre's project label need not be one byre created, so
// that label is an attacker's field. A refusal is exactly the surface worth
// dressing up with control sequences: it is the one byre prints when it is
// about to stop, and a rewound line could hide which volume it named.
func TestExclusiveRefusalEscapesEngineAndLabelValues(t *testing.T) {
	p, _ := testPaths(t)
	cfg, vol := exclusiveConfig(p)
	holder := siblingHolding(t, p, vol)
	holder[workdirKey] = "byre-wt" + escCSI + escOSC
	f := &fakeRunner{
		live:       map[string][]string{projectLabel(p): {"sibling-box"}},
		labelsByID: map[string]map[string]string{"sibling-box": holder},
	}
	s, _, errBuf := testStreams("", false)
	if err := develop(f, s, p, combine(cfg, skills.Resolved{}), false, CredentialAsk); err == nil {
		t.Fatal("want a refusal")
	}
	assertNoESC(t, "exclusive volume refusal (forged workdir label)", errBuf.String())
	assertKept(t, "exclusive volume refusal (forged workdir label)", errBuf.String(), `sharing = "exclusive"`, "byre-wt")

	// The same for an engine's own error text, on the arm that reports it.
	p2, _ := testPaths(t)
	cfg2, _ := exclusiveConfig(p2)
	rv := combine(cfg2, skills.Resolved{})
	rv.otherEngines = []sessionRunner{&fakeRunner{
		engine: runner.Podman,
		// Deliberately NOT an unreachable-shaped error: that arm substitutes
		// byre's own wording, and it is the raw engine text that needs the
		// funnel.
		liveErr: errors.New("podman: " + escCSI + "unexpected daemon reply" + escOSC),
	}}
	s2, _, errBuf2 := testStreams("", false)
	if err := develop(&fakeRunner{}, s2, p2, rv, false, CredentialAsk); err == nil {
		t.Fatal("want a refusal")
	}
	assertNoESC(t, "exclusive volume refusal (hostile engine error)", errBuf2.String())
	assertKept(t, "exclusive volume refusal (hostile engine error)", errBuf2.String(), "unexpected daemon reply")
}

// The declined-engine and record-bookkeeping disclosures embed strings byre
// did not author -- the shadowed path (a filename the agent writes under a
// box-writable root, which is the shadow precondition) and the engine's own
// stderr -- so they ride the dataf funnel like every other develop-time
// warning. One arm per funnel entrance; the payload must survive as text
// while the control bytes go.
func TestDeclinedAndRecordDisclosuresEscapeExternalText(t *testing.T) {
	hostile := &hostexec.ShadowError{
		Name: "docker",
		Path: "/proj/.bin/" + escCSI + "docker" + escOSC,
		Root: "/proj",
	}
	d := declinedEngine{Engine: "docker", Err: hostile}
	paths := project.Paths{}

	var buf bytes.Buffer
	noteDeclinedEngines(&buf, []declinedEngine{d}, "It is not in this listing.")
	assertNoESC(t, "noteDeclinedEngines", buf.String())
	assertKept(t, "noteDeclinedEngines", buf.String(), "/proj/.bin/", "docker")

	buf.Reset()
	if _, err := refuseCrossEngineSession(&buf, nil, []declinedEngine{d}, "podman", paths); err != nil {
		t.Fatalf("declined-only refuseCrossEngineSession must not error: %v", err)
	}
	assertNoESC(t, "refuseCrossEngineSession declined arm", buf.String())
	assertKept(t, "refuseCrossEngineSession declined arm", buf.String(), "can't be ruled out")

	buf.Reset()
	f := &fakeRunner{}
	f.imageDigestErr = fmt.Errorf("inspect: %sdaemon said no%s", escCSI, escOSC)
	img := imageRecord(f, &buf, "tag", "")
	if img.Digest != "" {
		t.Fatalf("digest must stay empty on an inspect failure, got %q", img.Digest)
	}
	assertNoESC(t, "imageRecord disclosure", buf.String())
	assertKept(t, "imageRecord disclosure", buf.String(), "daemon said no", "pins the tag only")
}
