package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
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
	renderStatus(&b, statusPayloads(escCSI+"\n"+escOSC+"\rEngine:       open"))
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
	renderStatus(&clean, statusPayloads("-x-"))
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
