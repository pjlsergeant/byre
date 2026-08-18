package commands

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
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
		{launchUnreadable, "present but unreadable"},
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

// deltaOf is the diff between one record and one config-derived view, for the
// tests that pin a single dimension. Returns the rendered lines.
func deltaOf(t *testing.T, rec *launchRecord, s statusInfo) []string {
	t.Helper()
	now, next := applyLaunchRecord(&s, rec, project.Paths{})
	var out []string
	for _, d := range diffLaunch(now, next) {
		out = append(out, d.String())
	}
	return out
}

// The next-launch egress side must be the ENFORCED allowlist, the same last
// step of the resolution that fed the record -- not status's declared union,
// which deliberately keeps a closed entry visible (marked closed-by). Diffing
// the two shapes reports every closed-but-declared endpoint as arriving at the
// next launch, on every render, on the standard claude-minus-statsig config.
func TestNextLaunchEgressSubtractsClosuresLikeTheRecordDid(t *testing.T) {
	rec := &launchRecord{Record: 1, Network: launchNetwork{
		Posture: "deny-by-default",
		// What the netns helper enforced: statsig already subtracted.
		Egress:     "api.anthropic.com:443",
		EgressDeny: []string{"statsig.anthropic.com"},
	}}
	s := statusInfo{
		Canonical: "/p", Container: "abc", Engine: "docker",
		NetPosture: "deny-by-default",
		// What status renders: the DECLARED union, statsig included.
		Egress: []skills.EgressAllow{
			{Skill: "claude", Host: "api.anthropic.com", Port: 443},
			{Skill: "claude", Host: "statsig.anthropic.com", Port: 443},
		},
		EgressClosed: []string{"statsig.anthropic.com"},
	}
	for _, d := range deltaOf(t, rec, s) {
		if strings.Contains(d, "statsig") {
			t.Errorf("a closed endpoint is not arriving at the next launch: %q", d)
		}
	}
	// The subtraction is real, not a suppression of the whole dimension: an
	// endpoint the config genuinely adds still shows.
	s.Egress = append(s.Egress, skills.EgressAllow{Skill: "config", Host: "github.com", Port: 443})
	if got := deltaOf(t, rec, s); len(got) != 1 || !strings.Contains(got[0], "+ Egress github.com:443") {
		t.Errorf("a real addition must still show: %v", got)
	}
}

// A record holds tilde-EXPANDED cleaned absolutes because that is what the
// engine got; a config mount is still spelled `~/qa-secrets`. Compared raw,
// every tilde mount is a standing false pair.
func TestNextLaunchBindsCompareThroughTheSameExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	rec := &launchRecord{Record: 1, Binds: []launchBind{
		{Host: "/p", Target: "/workspace", Mode: "rw"},
		{Host: home + "/qa-secrets", Target: "/secrets", Mode: "ro"},
	}}
	s := statusInfo{Canonical: "/p", Container: "abc", Engine: "docker",
		Binds: []config.Mount{{Host: "~/qa-secrets", Target: "/secrets", Mode: "ro"}}}
	if got := deltaOf(t, rec, s); len(got) != 0 {
		t.Errorf("the same mount in two spellings is not a difference: %v", got)
	}
	// A mount that genuinely moved still reports, in the expanded spelling
	// both sides now share.
	s.Binds = []config.Mount{{Host: "~/other", Target: "/secrets", Mode: "ro"}}
	got := strings.Join(deltaOf(t, rec, s), "\n")
	if !strings.Contains(got, "- Bind "+home+"/qa-secrets") || !strings.Contains(got, "+ Bind "+home+"/other") {
		t.Errorf("a real move must report, expanded on both sides: %s", got)
	}
}

// An empty base is not "unchanged" -- it is byre's own default, and the image
// that gets built is a different image. The delta names the default rather
// than printing a blank.
func TestNextLaunchBaseClearedIsStillADelta(t *testing.T) {
	rec := &launchRecord{Record: 1, Image: launchImage{Base: "golang:1.26-bookworm"}}
	s := statusInfo{Canonical: "/p", Container: "abc", Engine: "docker", Base: ""}
	got := strings.Join(deltaOf(t, rec, s), "\n")
	if !strings.Contains(got, "~ Base golang:1.26-bookworm -> (default: "+gen.DefaultBase+")") {
		t.Errorf("clearing the base must report, naming the default: %q", got)
	}
	// The reverse arm: adopting a base where the box ran on the default.
	rec.Image.Base = ""
	s.Base = "golang:1.26-bookworm"
	got = strings.Join(deltaOf(t, rec, s), "\n")
	if !strings.Contains(got, "~ Base (default: "+gen.DefaultBase+") -> golang:1.26-bookworm") {
		t.Errorf("adopting a base must report, naming the default: %q", got)
	}
}

// The base is compared by EFFECTIVE value, not by config spelling. gen
// substitutes gen.DefaultBase for an empty base, so writing the default out
// explicitly (or deleting that line) changes the config and changes nothing
// about the image -- and a `~ Base` line there would be exactly the standing
// false row this section's rules forbid.
func TestNextLaunchBaseSpellingTheDefaultIsNotADelta(t *testing.T) {
	// Box ran on the default; config now spells it out.
	rec := &launchRecord{Record: 1, Image: launchImage{Base: ""}}
	s := statusInfo{Canonical: "/p", Container: "abc", Engine: "docker", Base: gen.DefaultBase}
	if got := deltaOf(t, rec, s); len(got) != 0 {
		t.Errorf("spelling the default out is not a difference: %v", got)
	}
	// And the reverse: box ran on the explicit spelling, config deleted it.
	rec.Image.Base = gen.DefaultBase
	s.Base = ""
	if got := deltaOf(t, rec, s); len(got) != 0 {
		t.Errorf("deleting the explicit default is not a difference: %v", got)
	}
	// A base that really differs from the default still reports, from the
	// empty side too -- the normalization must not swallow real changes.
	rec.Image.Base = ""
	s.Base = "golang:1.26-bookworm"
	if got := deltaOf(t, rec, s); len(got) != 1 {
		t.Errorf("a real base change must still report: %v", got)
	}
}

// The false-NEGATIVE twin: byre's own default can move across an upgrade, and
// with both sides spelled "" a compare-time normalization answers with
// TODAY's default on both -- no delta, while the running box is on the old
// one and FROM really did change. The record holds the EFFECTIVE base for
// exactly this: a recorded "" would mean "whatever DefaultBase meant on the
// byre that wrote this", which is the re-derivation the record abolishes.
func TestNextLaunchBaseSurvivesADefaultBaseChange(t *testing.T) {
	// imageRecord is where a record's base is resolved; assert it resolves.
	f := &fakeRunner{}
	if img := imageRecord(f, io.Discard, "byre-img", ""); img.Base != gen.DefaultBase {
		t.Fatalf("record base = %q, want the effective %q", img.Base, gen.DefaultBase)
	}
	if img := imageRecord(f, io.Discard, "byre-img", "golang:1.26-bookworm"); img.Base != "golang:1.26-bookworm" {
		t.Fatalf("an explicit base must survive untouched, got %q", img.Base)
	}
	// A box recorded under an OLDER default, beside a config that still says
	// nothing: the delta must fire, because FROM changes at the next build.
	rec := &launchRecord{Record: 1, Image: launchImage{Base: "debian:bullseye"}} // the old default
	s := statusInfo{Canonical: "/p", Container: "abc", Engine: "docker", Base: ""}
	got := strings.Join(deltaOf(t, rec, s), "\n")
	if !strings.Contains(got, "~ Base debian:bullseye -> (default: "+gen.DefaultBase+")") {
		t.Errorf("a moved default must report against a recorded effective base: %q", got)
	}
}

// run_args are compared as SLICES. Joining on a space is not injective, so a
// joined compare calls two different argvs equal and reports no change across
// an edit that changes what the engine is handed.
func TestNextLaunchRunArgsCompareAsArgvNotAsAJoinedString(t *testing.T) {
	rec := &launchRecord{Record: 1, RunArgs: []string{"--label", "x=a b"}}
	s := statusInfo{Canonical: "/p", Container: "abc", Engine: "docker",
		RunArgs: []string{"--label x=a", "b"}} // same joined string, different argv
	got := strings.Join(deltaOf(t, rec, s), "\n")
	if !strings.Contains(got, "~ Raw run args") {
		t.Fatalf("two argvs with the same joined spelling are still two argvs: %q", got)
	}
	// And the line itself has to distinguish them, or it reports a change the
	// reader cannot see.
	if !strings.Contains(got, "'x=a b'") {
		t.Errorf("the delta must render an unambiguous argv: %q", got)
	}
	// Genuinely identical argvs are no delta.
	s.RunArgs = []string{"--label", "x=a b"}
	if d := deltaOf(t, rec, s); len(d) != 0 {
		t.Errorf("identical argvs are not a difference: %v", d)
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

// The Agent row joins the running-box subject swap only when the record can
// speak for it, and any difference is attributed: a --agent override names
// the flag, a config edited since launch names the drift, and a pre-agent
// record leaves the row config-derived rather than claiming "agentless".
func TestStatusAgentRowSpeaksForTheLaunchedBox(t *testing.T) {
	apply := func(rec *launchRecord) statusInfo {
		s := statusInfo{Agent: "byre/claude", Launch: rec, LaunchState: launchRecordOK}
		applyLaunchRecord(&s, rec, project.Paths{})
		return s
	}

	s := apply(&launchRecord{Agent: "byre/codex", AgentOverride: true})
	if s.Agent != "byre/codex" || !strings.Contains(s.AgentNote, "--agent override") || !strings.Contains(s.AgentNote, "byre/claude") {
		t.Errorf("override: agent=%q note=%q (want the record's agent, the flag, and the config's answer)", s.Agent, s.AgentNote)
	}

	// --agent none: the override marker is what distinguishes a deliberate
	// agentless launch from a record too old to say.
	s = apply(&launchRecord{AgentOverride: true})
	if s.Agent != "" || !strings.Contains(s.AgentNote, "--agent override") {
		t.Errorf("agentless override: agent=%q note=%q", s.Agent, s.AgentNote)
	}

	// No override, but the config moved since launch: attributed as drift.
	s = apply(&launchRecord{Agent: "byre/codex"})
	if s.Agent != "byre/codex" || !strings.Contains(s.AgentNote, "config now") {
		t.Errorf("drift: agent=%q note=%q", s.Agent, s.AgentNote)
	}

	// A pre-agent record cannot speak for the row: config-derived, no note.
	s = apply(&launchRecord{})
	if s.Agent != "byre/claude" || s.AgentNote != "" {
		t.Errorf("pre-agent record: agent=%q note=%q", s.Agent, s.AgentNote)
	}

	// Agreement needs no qualifier.
	s = apply(&launchRecord{Agent: "byre/claude"})
	if s.Agent != "byre/claude" || s.AgentNote != "" {
		t.Errorf("agreement: agent=%q note=%q", s.Agent, s.AgentNote)
	}
}
