package commands

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// driftedStatus builds the shape the split exists for: a box running with a
// `/secrets` mount, a published port and a deny-by-default network, beside a
// config that has since deleted the mount, moved the port and dropped the
// posture. Returns the info AS STATUS WOULD RENDER IT (record applied, diff
// computed).
func driftedStatus() statusInfo {
	s := statusInfo{
		Agent:     "claude",
		Engine:    "docker",
		ID:        "byre-dev-4f21bc",
		Canonical: "/home/pete/byre",
		Container: "9f3ab2c41d7044ee",
		Base:      "golang:1.27-bookworm",
		// The CURRENT config: the mount is gone, the port moved, the posture
		// dropped, a skill added.
		Binds:   nil,
		Ports:   []config.Port{{Container: 5432, Host: 15433}},
		Volumes: []config.Volume{{Name: "claude-state", Role: "state", Target: "/home/dev/.claude"}},
		Skills:  []string{"claude", "pjlsergeant/devlog"},
	}
	rec := &launchRecord{
		Record:  LaunchRecordVersion,
		Byre:    "v1.4.0",
		Project: "byre-dev-4f21bc",
		Workdir: "/home/pete/byre",
		Engine:  "docker",
		EnvKeys: []string{"BYRE_GID", "BYRE_UID", "GIT_AUTHOR_NAME"},
		Image:   launchImage{Tag: "byre-byre-dev-4f21bc-u1000-g1000", Digest: "sha256:9f1c8d2e0011223344556677889900aabbccddeeff00112233445566778899aa", Base: "golang:1.26-bookworm"},
		Network: launchNetwork{
			Posture: "deny-by-default", PostureSkill: "firewall",
			Egress: "api.anthropic.com:443 github.com:443",
		},
		Binds: []launchBind{
			{Host: "/home/pete/byre", Target: "/workspace", Mode: "rw"},
			{Host: "/home/pete/secrets", Target: "/secrets", Mode: "ro"},
		},
		Ports:   []launchPort{{Interface: "127.0.0.1", Host: 15432, Container: 5432}},
		Volumes: []launchVolume{{Name: "byre-byre-dev-4f21bc-claude-state", Decl: "claude-state", Role: "state", Target: "/home/dev/.claude", Scope: "project"}},
		Skills:  []launchSkill{{Name: "claude", Provenance: "bundled v1.4.0"}},
	}
	s.Launch = rec
	s.LaunchState = launchRecordOK
	s.LaunchHash = strings.Repeat("9f3ab2c41d70", 5) + "abcd"
	now, next := applyLaunchRecord(&s, rec, project.Paths{})
	s.Changes = diffLaunch(now, next)
	return s
}

// The centrepiece: a running box IS the subject, and only what differs shows
// up under Next launch. Asserted row by row rather than as a page golden --
// the page is prose, the rules are the contract.
func TestStatusRendersTheRunningBoxAndTheDelta(t *testing.T) {
	var b strings.Builder
	renderStatusTest(&b, driftedStatus())
	out := b.String()

	// The rows describe the BOX: the mount deleted from the config is still
	// mounted, and the page says so.
	assertRow(t, out, "Host mounts", "/home/pete/secrets -> /secrets  (ro)")
	assertRow(t, out, "Ports", "127.0.0.1:15432 -> 5432  (host -> container)")
	assertRow(t, out, "Network", "deny-by-default  (skill: firewall)")
	assertRow(t, out, "Egress", "api.anthropic.com:443  (launch record)")
	assertRow(t, out, "Image", "byre-byre-dev-4f21bc-u1000-g1000  (sha256:9f1c8d2e0011…  (--full to show); base golang:1.26-bookworm)")

	// And the page says whose box it is describing, on the row where a reader
	// confirms a box is live.
	if got := strings.Join(statusRows(out)["Container"], " "); !strings.Contains(got, "describe THIS box") || !strings.Contains(got, "9f3ab2c41d70") {
		t.Errorf("Container row must name the subject and the record, got %q", got)
	}

	// The delta, in the row vocabulary of the blocks above.
	changes := strings.Join(statusRows(out)["Next launch"], "\n")
	for _, want := range []string{
		"- Bind /home/pete/secrets -> /secrets  (ro)",
		"- Port 127.0.0.1:15432 -> 5432",
		"+ Port 127.0.0.1:15433 -> 5432",
		"- Network deny-by-default",
		"+ Network open  (no posture declared)",
		"+ Skill pjlsergeant/devlog",
		"~ Base golang:1.26-bookworm -> golang:1.27-bookworm",
	} {
		if !strings.Contains(changes, want) {
			t.Errorf("Next launch section missing %q; got:\n%s", want, changes)
		}
	}
}

// Nothing differs => no section at all. An "unchanged" line is a row nobody
// needs, and its absence reads correctly.
func TestStatusNextLaunchSectionAbsentWhenNothingDiffers(t *testing.T) {
	s := driftedStatus()
	s.Changes = nil
	s.SkillErr = ""
	var b strings.Builder
	renderStatusTest(&b, s)
	if strings.Contains(b.String(), "Next launch") {
		t.Errorf("no delta must render no section:\n%s", b.String())
	}
}

// With no box, nothing is relabelled: the rows ARE the next launch and today's
// semantics stand.
func TestStatusWithNoBoxIsUnchanged(t *testing.T) {
	var b strings.Builder
	renderStatusTest(&b, statusInfo{
		Engine: "docker", Canonical: "/p",
		Binds: []config.Mount{{Host: "/data", Target: "/data", Mode: "ro"}},
	})
	out := b.String()
	assertRow(t, out, "Container", "not running")
	assertRow(t, out, "Host mounts", "/data -> /data  (ro)")
	for _, banned := range []string{"Next launch", "describe THIS box", "launch record", "Image"} {
		if strings.Contains(out, banned) {
			t.Errorf("a project with no box must not gain %q:\n%s", banned, out)
		}
	}
}

// A running box byre could not read a record for: today's render plus ONE
// qualifier. Degrade, never guess.
func TestStatusRunningBoxWithoutARecordQualifiesTheRows(t *testing.T) {
	for _, tc := range []struct {
		state    launchState
		fragment string
	}{
		{launchPreRecord, "predates launch records"},
		{launchMissing, "no longer in the store"},
		{launchTampered, "does NOT match its own address"},
		{launchNewer, "NEWER byre"},
	} {
		var b strings.Builder
		renderStatusTest(&b, statusInfo{
			Engine: "docker", Canonical: "/p", Container: "abcdef0123456789",
			LaunchState: tc.state,
			Binds:       []config.Mount{{Host: "/data", Target: "/data", Mode: "ro"}},
		})
		out := b.String()
		if !strings.Contains(out, tc.fragment) {
			t.Errorf("state %v: qualifier missing (%q):\n%s", tc.state, tc.fragment, out)
		}
		// The rows still describe the config, and say so rather than claiming
		// to describe the box.
		if !strings.Contains(out, "CURRENT CONFIG") {
			t.Errorf("state %v: the qualifier must say what the rows describe:\n%s", tc.state, out)
		}
		if strings.Contains(out, "describe THIS box") {
			t.Errorf("state %v: byre must not claim a box it cannot vouch for:\n%s", tc.state, out)
		}
	}
}

// A skill set that stops resolving is a NEXT-LAUNCH problem: under a record it
// must not blank the running box's posture claim, and it must not vanish.
func TestStatusSkillErrorUnderARecordIsANextLaunchFact(t *testing.T) {
	s := driftedStatus()
	s.SkillErr = "skill \"gone\" not found"
	var b strings.Builder
	renderStatusTest(&b, s)
	out := b.String()
	assertRow(t, out, "Network", "deny-by-default  (skill: firewall)")
	if !strings.Contains(strings.Join(statusRows(out)["Next launch"], "\n"), "Skills do not resolve for the next launch") {
		t.Errorf("an unresolvable skill set must surface as a next-launch change:\n%s", out)
	}
	// The running box's own skills still render, with the provenance it was
	// built with.
	assertRow(t, out, "Skills", "claude  bundled v1.4.0")
}

// The claim-degradation inputs come off the RECORD: a box launched with raw
// run_args keeps its hedge even after the config drops them, and a config that
// gains them does not retroactively hedge the running box.
func TestStatusNetworkHedgeFollowsTheRecordedBox(t *testing.T) {
	s := driftedStatus()
	s.Launch.Network.ProjectRunArgs = true
	now, next := applyLaunchRecord(&s, s.Launch, project.Paths{})
	s.Changes = diffLaunch(now, next)
	var b strings.Builder
	renderStatusTest(&b, s)
	if got := strings.Join(statusRows(b.String())["Network"], " "); !strings.Contains(got, "raw run_args") || !strings.Contains(got, "not guaranteed") {
		t.Errorf("the recorded box's own raw run_args must degrade its posture claim, got %q", got)
	}
}

// The reserved-env keys a record carries degrade the same claims a configured
// set does -- through the one owner, never a second reading.
func TestStatusRecordedReservedEnvDegradesTheSameClaims(t *testing.T) {
	s := driftedStatus()
	s.Launch.Network.ReservedEnv = []string{"skill:knobs BYRE_LAUNCH_GATE_FILE"}
	now, next := applyLaunchRecord(&s, s.Launch, project.Paths{})
	s.Changes = diffLaunch(now, next)
	var b strings.Builder
	renderStatusTest(&b, s)
	out := b.String()
	if got := strings.Join(statusRows(out)["Reserved env"], " "); !strings.Contains(got, "BYRE_LAUNCH_GATE_FILE") {
		t.Errorf("Reserved env row missing for a recorded key, got %q", got)
	}
	if got := strings.Join(statusRows(out)["Network"], " "); !strings.Contains(got, "not guaranteed") {
		t.Errorf("a recorded reserved key must degrade the posture claim, got %q", got)
	}
	if !skills.ReservedEnvTouches(s.SkillReservedEnv, skills.ClaimNetwork) {
		t.Errorf("the recorded set must feed the shared claim predicate")
	}
}

// byre's own worktree git binds are on the argv and in the record, and have
// never been listed on either side of the page (ADR 0009; the Worktree of row
// announces the arrangement). They must not become a permanent delta row.
func TestStatusWorktreeGitBindsAreNotADelta(t *testing.T) {
	paths := project.Paths{IsWorktree: true, WorkDir: "/home/pete/byre/wt", CommonGitDir: "/home/pete/byre/.git"}
	s := statusInfo{Canonical: "/home/pete/byre", Container: "abc", Engine: "docker"}
	rec := &launchRecord{Record: 1, Binds: []launchBind{
		{Host: "/home/pete/byre/wt", Target: "/workspace", Mode: "rw"},
		{Host: "/home/pete/byre/.git", Target: "/home/pete/byre/.git", Mode: "rw"},
		{Host: "/home/pete/byre/wt", Target: "/home/pete/byre/wt", Mode: "rw"},
	}}
	now, next := applyLaunchRecord(&s, rec, paths)
	if len(s.Binds) != 0 {
		t.Errorf("Host mounts must not list byre's own worktree git binds: %+v", s.Binds)
	}
	if d := diffLaunch(now, next); len(d) != 0 {
		t.Errorf("the worktree git binds must not produce a delta: %+v", d)
	}
}

// A self-edit mount is the Self-edit row on either side, never a Host mounts
// row -- and its arrival/departure is a delta in its own words.
func TestStatusSelfEditMountRidesItsOwnRow(t *testing.T) {
	s := statusInfo{Canonical: "/p", Container: "abc", Engine: "docker"}
	rec := &launchRecord{Record: 1, Binds: []launchBind{
		{Host: "/p", Target: "/workspace", Mode: "rw"},
		{Host: "/home/pete/.byre/projects/p", Target: selfEditTarget, Mode: "rw"},
	}}
	now, next := applyLaunchRecord(&s, rec, project.Paths{})
	if s.SelfEdit != "/home/pete/.byre/projects/p" || len(s.Binds) != 0 {
		t.Fatalf("self-edit = %q, binds = %+v", s.SelfEdit, s.Binds)
	}
	deltas := diffLaunch(now, next)
	if len(deltas) != 1 || deltas[0].Sign != "-" || !strings.Contains(deltas[0].Text, "Self-edit store mount") {
		t.Errorf("deltas = %+v", deltas)
	}
}

// Config [env] is BAKED into the image and never rides `-e`, so the record
// cannot hold it -- and the row must therefore survive a record rather than be
// blanked by it. Box env is the argv side, and the two do not overlap.
func TestStatusConfigEnvSurvivesARecord(t *testing.T) {
	s := driftedStatus()
	s.EnvKeys = []string{"CGO_ENABLED", "GOFLAGS"}
	var b strings.Builder
	renderStatusTest(&b, s, tierFull)
	out := b.String()
	assertRow(t, out, "Env", "CGO_ENABLED, GOFLAGS  (baked into the image)")
	if got := strings.Join(statusRows(out)["Box env"], " "); strings.Contains(got, "CGO_ENABLED") {
		t.Errorf("Box env is the -e argv side and must not restate baked keys, got %q", got)
	}
	if !strings.Contains(strings.Join(statusRows(out)["Box env"], " "), "GIT_AUTHOR_NAME") {
		t.Errorf("Box env must list the keys the engine was handed, got %q", statusRows(out)["Box env"])
	}
}

// --data carries the same content as --full, qualifiers included: the subject,
// the record (or why there isn't one), and the same delta lines the page
// prints.
func TestStatusDataCarriesTheSubjectAndTheDelta(t *testing.T) {
	var d statusData
	b, err := json.Marshal(statusDataOf(driftedStatus()))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if d.Version != StatusDataVersion || d.Subject != "running_box" {
		t.Fatalf("version=%d subject=%q", d.Version, d.Subject)
	}
	if d.Launch == nil || d.Launch.State != "verified" || d.Launch.Image == nil || d.Launch.Image.Base != "golang:1.26-bookworm" {
		t.Fatalf("launch = %+v", d.Launch)
	}
	if len(d.Launch.BoxEnvKeys) != 3 {
		t.Errorf("box env keys = %v", d.Launch.BoxEnvKeys)
	}
	// The rows the page shows for the RUNNING box are the rows here.
	if len(d.Binds) != 1 || d.Binds[0].Target != "/secrets" {
		t.Errorf("binds = %+v", d.Binds)
	}
	if len(d.Ports) != 1 || d.Ports[0].Host != 15432 {
		t.Errorf("ports = %+v", d.Ports)
	}
	joined := strings.Join(d.Changes, "\n")
	if !strings.Contains(joined, "- Bind /home/pete/secrets -> /secrets") || !strings.Contains(joined, "+ Skill pjlsergeant/devlog") {
		t.Errorf("changes = %v", d.Changes)
	}
	// An env VALUE can never reach this document, and neither can one reach
	// the record it reads from.
	if strings.Contains(string(b), "\"box_env_values\"") {
		t.Errorf("values must never be carried: %s", b)
	}
}

// A running box with no readable record still gets the launch object, so a
// --data reader learns WHY rather than inventing an explanation from absence.
func TestStatusDataCarriesTheDegradeReason(t *testing.T) {
	d := statusDataOf(statusInfo{Engine: "docker", Canonical: "/p", Container: "abc", LaunchState: launchTampered})
	if d.Subject != "next_launch" {
		t.Errorf("subject = %q", d.Subject)
	}
	if d.Launch == nil || d.Launch.State != "tampered" || !strings.Contains(d.Launch.Note, "does NOT match its own address") {
		t.Fatalf("launch = %+v", d.Launch)
	}
	if d.Launch.Record != "" || d.Launch.Image != nil {
		t.Errorf("an unusable record must contribute no fields: %+v", d.Launch)
	}
}

// With no box at all there is no launch object: nothing to report and nothing
// to explain.
func TestStatusDataHasNoLaunchWithoutABox(t *testing.T) {
	if d := statusDataOf(statusInfo{Engine: "docker", Canonical: "/p"}); d.Launch != nil || d.Subject != "next_launch" {
		t.Errorf("launch = %+v, subject = %q", d.Launch, d.Subject)
	}
}
