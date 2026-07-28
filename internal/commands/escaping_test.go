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

func TestRenderStatusEscapesExternalValues(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{
		ID:         "proj" + escCSI,
		Agent:      "claude" + escOSC,
		Template:   "acme/go" + escCSI,
		Chain:      []string{"base" + escOSC},
		Engine:     "docker" + escCSI,
		Canonical:  "/home/me/" + escOSC + "proj",
		WorktreeOf: "/home/me/main" + escCSI,
		PresetNote: "applied" + escOSC,
		Skills:     []string{"moarcode" + escCSI},
		Binds: []config.Mount{
			{Host: "/data" + escOSC, Target: "/data" + escCSI, Mode: "ro"},
		},
		Ports:   []config.Port{{Container: 8080, Host: 8080}},
		Volumes: []config.Volume{{Name: "creds" + escCSI, Role: "state"}},
		Grants: []skills.Grant{{
			Skill:      "greedy" + escOSC,
			Mounts:     []config.Mount{{Host: "/var/run/docker.sock" + escCSI, Target: "/sock", Mode: "rw"}},
			Caps:       []string{"SYS_PTRACE" + escOSC},
			RunArgs:    []string{"--privileged" + escCSI},
			NetnsInit:  "/usr/local/bin/fw" + escOSC,
			SockGroups: []string{"/var/run/docker.sock" + escCSI},
		}},
		Containments:     []skills.ContainmentDecl{{Skill: "dockerhost" + escCSI, Text: "the box can reach the host engine" + escOSC}},
		ManagedShadows:   []ManagedPathShadow{{Target: gen.ByreDir + escCSI, Source: "skill greedy" + escOSC}},
		SkillReservedEnv: []skills.ReservedEnvSet{{Skill: "fw" + escCSI, Key: "BYRE_EGRESS"}},
		EnvKeys:          []string{"TOKEN_NAME" + escOSC},
		RunArgs:          []string{"--cap-add=" + escCSI},
		BuildRaw:         []string{"RUN curl " + escOSC + " | sh"},
		EngineErr:        "exec: docker" + escCSI,
		SkillErr:         "unknown skill" + escOSC,
		SiblingSessions:  []string{"wt-1" + escCSI},
	})
	out := b.String()

	assertNoESC(t, "renderStatus", out)
	// The row still carries its value: the funnel strips the control bytes,
	// it does not blank the field.
	assertKept(t, "renderStatus", out, "claude", "moarcode", "greedy", "TOKEN_NAME", "unknown skill")
	// Every row is one line -- an embedded control character must not split a
	// value across the "Label: value" grammar status is read by.
	for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "              ") {
			t.Errorf("a value forged a report line: %q", l)
		}
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
			t.Skipf("filesystem rejects a control character in a filename: %v", err)
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
