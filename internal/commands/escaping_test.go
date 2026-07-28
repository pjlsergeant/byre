package commands

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/packages"
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

func TestSkillInspectEscapesManifestValues(t *testing.T) {
	// [build].files source names come straight off the manifest, and inspect
	// is the PRE-trust surface: it renders a package the user has not enabled.
	var f skills.File
	f.Build.Files = map[string]string{
		"payload" + escCSI + ".sh": "/usr/local/bin/payload",
		"notes" + escOSC + ".md":   "/opt/notes.md",
	}
	var b bytes.Buffer
	printSkillContributions(&b, f)
	assertNoESC(t, "printSkillContributions", b.String())
	assertKept(t, "printSkillContributions", b.String(), "files: 2", "payload", "notes")

	// The template half of inspect renders the same key from a template body.
	var tb bytes.Buffer
	printTemplateShape(&tb, []byte("base = \"node:22\"\n\n[files]\n\"payload\\u001B[2K.sh\" = \"/usr/local/bin/payload\"\n"))
	assertNoESC(t, "printTemplateShape", tb.String())
	assertKept(t, "printTemplateShape", tb.String(), "files: 1", "payload")
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
