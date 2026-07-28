package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

// pinLaunchClock freezes the record's timestamp so a test can assert on the
// bytes a record hashes to.
func pinLaunchClock(t *testing.T) {
	t.Helper()
	prev := launchNow
	launchNow = func() time.Time { return time.Date(2026, 7, 28, 21, 40, 11, 0, time.UTC) }
	t.Cleanup(func() { launchNow = prev })
}

// sampleLaunchRecord is a record with one of everything, for the serialization
// contract and the round-trip.
func sampleLaunchRecord() launchRecord {
	return launchRecord{
		Record:  LaunchRecordVersion,
		Byre:    "v1.4.0",
		Created: time.Date(2026, 7, 28, 21, 40, 11, 0, time.UTC),
		Project: "byre-dev-4f21bc",
		Workdir: "/home/pete/byre",
		Engine:  "docker",
		EnvKeys: []string{"BYRE_UID", "GIT_AUTHOR_NAME", "NGROK_AUTHTOKEN"},
		RunArgs: []string{"-e", "INTTEST_VM=172.17.0.1"},
		Image: launchImage{
			Tag:    "byre-byre-dev-4f21bc-u1000-g1000",
			Digest: "sha256:9f1c8d2e",
			Base:   "golang:1.26-bookworm",
		},
		Network: launchNetwork{
			Posture:      "deny-by-default",
			PostureSkill: "firewall",
			Egress:       "api.anthropic.com:443 github.com:443",
			EgressDeny:   []string{"statsig.anthropic.com"},
			ReservedEnv:  []string{"skill:knobs BYRE_LAUNCH_GATE_FILE"},
		},
		Binds: []launchBind{
			{Host: "/home/pete/byre", Target: "/workspace", Mode: "rw"},
			{Host: "/home/pete/secrets", Target: "/secrets", Mode: "ro"},
		},
		Ports:   []launchPort{{Interface: "127.0.0.1", Host: 15432, Container: 5432}},
		Volumes: []launchVolume{{Name: "byre-byre-dev-4f21bc-claude-state", Target: "/home/dev/.claude", Decl: "claude-state", Role: "state", Scope: "project"}},
		Skills:  []launchSkill{{Name: "claude", Provenance: "bundled v1.4.0"}},
	}
}

// The serialization is a CONTRACT: the record is content-addressed, so the
// bytes ARE the identity and a field reordered or renamed changes every
// address. Byte-exact on purpose.
const launchRecordGolden = `# byre launch record -- written under the setup lock at container create,
# from the same resolution that fed the engine. This file is addressed by the
# sha256 of its own bytes; the container carries byre.launch=<that hash>, and
# byre re-hashes rather than trusts. It records what byre TOLD THE ENGINE --
# env KEYS only, values never. Delete it and status degrades honestly.
record = 1
byre = 'v1.4.0'
created = 2026-07-28T21:40:11Z
project = 'byre-dev-4f21bc'
workdir = '/home/pete/byre'
engine = 'docker'
env_keys = ['BYRE_UID', 'GIT_AUTHOR_NAME', 'NGROK_AUTHTOKEN']
run_args = ['-e', 'INTTEST_VM=172.17.0.1']

[image]
tag = 'byre-byre-dev-4f21bc-u1000-g1000'
digest = 'sha256:9f1c8d2e'
base = 'golang:1.26-bookworm'

[network]
posture = 'deny-by-default'
posture_skill = 'firewall'
egress = 'api.anthropic.com:443 github.com:443'
egress_deny = ['statsig.anthropic.com']
reserved_env = ['skill:knobs BYRE_LAUNCH_GATE_FILE']

[[binds]]
host = '/home/pete/byre'
target = '/workspace'
mode = 'rw'

[[binds]]
host = '/home/pete/secrets'
target = '/secrets'
mode = 'ro'

[[ports]]
interface = '127.0.0.1'
host = 15432
container = 5432

[[volumes]]
name = 'byre-byre-dev-4f21bc-claude-state'
target = '/home/dev/.claude'
decl = 'claude-state'
role = 'state'
scope = 'project'

[[skills]]
name = 'claude'
provenance = 'bundled v1.4.0'
`

func TestLaunchRecordSerializationContract(t *testing.T) {
	content, hash, err := encodeLaunchRecord(sampleLaunchRecord())
	if err != nil {
		t.Fatal(err)
	}
	if content != launchRecordGolden {
		t.Errorf("launch record serialization drifted (it is the content ADDRESS — see the ADR before changing it):\n--- got ---\n%s\n--- want ---\n%s", content, launchRecordGolden)
	}
	sum := sha256.Sum256([]byte(launchRecordGolden))
	if hash != hex.EncodeToString(sum[:]) {
		t.Errorf("hash %q is not the sha256 of the rendered bytes", hash)
	}
}

// The record must survive its own round trip: status reads back what develop
// wrote, and a field that marshals but does not unmarshal is a row that
// silently vanishes from the running box's page.
func TestLaunchRecordRoundTrips(t *testing.T) {
	p, _ := testPaths(t)
	want := sampleLaunchRecord()
	hash, err := writeLaunchRecord(p, want)
	if err != nil {
		t.Fatal(err)
	}
	got, st := readLaunchRecord(p, map[string]string{launchKey: hash})
	if st != launchRecordOK {
		t.Fatalf("state = %v, want launchRecordOK", st)
	}
	if got.Byre != want.Byre || got.Image.Digest != want.Image.Digest || got.Network.Egress != want.Network.Egress {
		t.Errorf("scalars lost in the round trip: %+v", got)
	}
	if len(got.Binds) != 2 || got.Binds[1].Host != "/home/pete/secrets" || got.Binds[1].Mode != "ro" {
		t.Errorf("binds lost in the round trip: %+v", got.Binds)
	}
	if len(got.EnvKeys) != 3 || len(got.RunArgs) != 2 || len(got.Volumes) != 1 || len(got.Ports) != 1 || len(got.Skills) != 1 {
		t.Errorf("collections lost in the round trip: %+v", got)
	}
}

// A record edited in the store no longer hashes to the address the container
// carries. Under --self-edit that directory is the BOX's to write, so status
// must refuse the bytes rather than render them — the verification IS the
// feature, not a nicety.
func TestLaunchRecordTamperIsDisclosedNotTrusted(t *testing.T) {
	p, _ := testPaths(t)
	hash, err := writeLaunchRecord(p, sampleLaunchRecord())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(launchesDir(p), hash+".toml")
	b, err := hostopen.ReadFileBounded(path, false, launchRecordLimit)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(b), "/home/pete/secrets", "/home/pete/harmless", 1)
	if edited == string(b) {
		t.Fatal("test setup: nothing was edited")
	}
	if err := hostopen.PublishFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	rec, st := readLaunchRecord(p, map[string]string{launchKey: hash})
	if st != launchTampered || rec != nil {
		t.Fatalf("state = %v (rec %v), want launchTampered and no record", st, rec)
	}
	if note := launchDegradeNote(st); !strings.Contains(note, "does NOT match its own address") {
		t.Errorf("degrade note must name the rule that fired, got %q", note)
	}
}

// A label value that is not a 64-hex digest can never address a record;
// treating it as a file name would let a container's own label pick the path.
// Containment: the refusal is the contract, and the store is untouched.
func TestLaunchRecordRefusesNonDigestLabel(t *testing.T) {
	p, _ := testPaths(t)
	if _, err := writeLaunchRecord(p, sampleLaunchRecord()); err != nil {
		t.Fatal(err)
	}
	before, err := hostopen.ReadDirNoFollow(launchesDir(p))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../../../etc/passwd", "..", "ABCDEF", strings.Repeat("a", 63)} {
		if rec, st := readLaunchRecord(p, map[string]string{launchKey: bad}); rec != nil || st == launchRecordOK {
			t.Errorf("label %q was accepted (state %v)", bad, st)
		}
	}
	after, err := hostopen.ReadDirNoFollow(launchesDir(p))
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Errorf("the store changed under a refused label: %d -> %d entries", len(before), len(after))
	}
}

// A record from a NEWER byre renders liveness only: byre does not guess at a
// schema it has never met.
func TestLaunchRecordFromNewerByreRendersLivenessOnly(t *testing.T) {
	p, _ := testPaths(t)
	rec := sampleLaunchRecord()
	rec.Record = LaunchRecordVersion + 1
	hash, err := writeLaunchRecord(p, rec)
	if err != nil {
		t.Fatal(err)
	}
	got, st := readLaunchRecord(p, map[string]string{launchKey: hash})
	if st != launchNewer || got != nil {
		t.Fatalf("state = %v (rec %v), want launchNewer and no record", st, got)
	}
	if note := launchDegradeNote(st); !strings.Contains(note, "NEWER byre") {
		t.Errorf("degrade note must name the rule that fired, got %q", note)
	}
}

// A box with no byre.launch label at all (started by an older byre) is the
// degrade the plan calls out by name: say so, never guess.
func TestLaunchRecordAbsentLabelDegradesHonestly(t *testing.T) {
	p, _ := testPaths(t)
	rec, st := readLaunchRecord(p, map[string]string{})
	if rec != nil || st != launchPreRecord {
		t.Fatalf("state = %v, want launchPreRecord", st)
	}
	if note := launchDegradeNote(st); !strings.Contains(note, "predates launch records") {
		t.Errorf("degrade note must name the rule that fired, got %q", note)
	}
	// Labelled but deleted: a distinct state with a distinct note.
	if _, st := readLaunchRecord(p, map[string]string{launchKey: strings.Repeat("b", 64)}); st != launchMissing {
		t.Fatalf("state = %v, want launchMissing", st)
	}
	if note := launchDegradeNote(launchMissing); !strings.Contains(note, "no longer in the store") {
		t.Errorf("degrade note must name the rule that fired, got %q", note)
	}
}

// Only a PROVABLE absence is "missing". Every other read failure is byre
// unable to LOOK, and under --self-edit a box can arrange each of these
// deliberately -- so reporting them as "no longer in the store" would hand an
// agent a way to make status say the record was deleted while it sits there.
func TestLaunchRecordUnreadableIsNotReportedAsMissing(t *testing.T) {
	p, _ := testPaths(t)
	if err := hostopen.MkdirAllIn(p.Home, filepath.Join("projects", p.ID, "launches"), 0o700); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("c", 64)
	path := filepath.Join(launchesDir(p), hash+".toml")

	// A DIRECTORY where the record should be: OpenRegular judges the
	// descriptor, so this is refused rather than read.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if rec, st := readLaunchRecord(p, map[string]string{launchKey: hash}); rec != nil || st != launchUnreadable {
		t.Fatalf("non-regular record: state = %v (rec %v), want launchUnreadable", st, rec)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	// An OVERSIZE record: refused, never truncated and parsed.
	if err := os.WriteFile(path, make([]byte, launchRecordLimit+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec, st := readLaunchRecord(p, map[string]string{launchKey: hash}); rec != nil || st != launchUnreadable {
		t.Fatalf("oversize record: state = %v (rec %v), want launchUnreadable", st, rec)
	}

	// PERMISSION DENIED: the file is right there and byre may not read it.
	// (Root bypasses the mode, so the arm only means something unprivileged.)
	if os.Geteuid() != 0 {
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatal(err)
		}
		if rec, st := readLaunchRecord(p, map[string]string{launchKey: hash}); rec != nil || st != launchUnreadable {
			t.Fatalf("unreadable mode: state = %v (rec %v), want launchUnreadable", st, rec)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	// A SYMLINK at the record name, pointing at a perfectly readable record.
	// The read is O_NOFOLLOW (hostopen.OpenRegular with follow=false), so this
	// is ELOOP -- not absence, and emphatically not the target's content: a
	// box that can write this directory could otherwise aim byre's own record
	// reader at any file the user can read.
	decoy := filepath.Join(launchesDir(p), "decoy")
	if err := os.WriteFile(decoy, []byte("record = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(decoy, path); err != nil {
		t.Fatal(err)
	}
	if rec, st := readLaunchRecord(p, map[string]string{launchKey: hash}); rec != nil || st != launchUnreadable {
		t.Fatalf("symlinked record: state = %v (rec %v), want launchUnreadable", st, rec)
	}

	// And the note says byre could not LOOK, not that the record is gone.
	note := launchDegradeNote(launchUnreadable)
	if !strings.Contains(note, "unreadable") || strings.Contains(note, "no longer in the store") {
		t.Errorf("the unreadable note must not borrow the missing one, got %q", note)
	}
}

// The record is what byre TOLD THE ENGINE: it is captured off the assembled
// run params, so a mount that reached the argv reaches the record and nothing
// that did not, does.
func TestLaunchRecordCapturesWhatTheEngineWasTold(t *testing.T) {
	pinLaunchClock(t)
	p, _ := testPaths(t)
	rv := combine(config.Config{
		Base:         "golang:1.26-bookworm",
		Ports:        []config.Port{{Container: 5432, Host: 15432}},
		Mounts:       []config.Mount{{Host: "/tmp", Target: "/secrets", Mode: "ro"}},
		Volumes:      []config.Volume{{Name: "state", Role: "state", Target: "/home/dev/.claude"}},
		RunArgs:      []string{"--pids-limit", "512"},
		EgressClosed: []string{"statsig.anthropic.com"},
	}, skills.Resolved{})
	params, err := runParams(p, rv, "byre-img", false, false, runner.Identity{UID: 1000, GID: 1000}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := launchRecordOf(p, rv, params, runner.Docker, launchImage{Tag: "byre-img", Digest: "sha256:deadbeef", Base: rv.cfg.Base})

	if rec.Record != LaunchRecordVersion || rec.Engine != "docker" || rec.Project != p.ID || rec.Workdir != p.WorkDir {
		t.Errorf("identity fields wrong: %+v", rec)
	}
	if rec.Created.IsZero() || rec.Byre == "" {
		t.Errorf("provenance fields wrong: created=%v byre=%q", rec.Created, rec.Byre)
	}
	// The workspace bind leads, then the config mount — the engine's own order.
	if len(rec.Binds) != 2 || rec.Binds[0].Target != "/workspace" || rec.Binds[0].Mode != "rw" ||
		rec.Binds[1].Target != "/secrets" || rec.Binds[1].Mode != "ro" {
		t.Errorf("binds = %+v", rec.Binds)
	}
	if len(rec.Ports) != 1 || rec.Ports[0].Interface != "127.0.0.1" || rec.Ports[0].Host != 15432 {
		t.Errorf("ports = %+v (must be the NORMALIZED publication the engine got)", rec.Ports)
	}
	if len(rec.Volumes) != 1 || rec.Volumes[0].Name != volumeName(p.ID, "state") || rec.Volumes[0].Decl != "state" || rec.Volumes[0].Role != "state" {
		t.Errorf("volumes = %+v (engine name plus the declared name status renders)", rec.Volumes)
	}
	if strings.Join(rec.RunArgs, " ") != "--pids-limit 512" {
		t.Errorf("run_args = %v (verbatim)", rec.RunArgs)
	}
	if len(rec.Network.EgressDeny) != 1 || rec.Network.EgressDeny[0] != "statsig.anthropic.com" {
		t.Errorf("egress_deny = %v", rec.Network.EgressDeny)
	}
	// Env KEYS, never values — the exit report's rule, on every surface.
	params.Env["SECRET_TOKEN"] = "sk-live-do-not-record-me"
	rec = launchRecordOf(p, rv, params, runner.Docker, launchImage{})
	content, _, err := encodeLaunchRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "'BYRE_UID'") || !strings.Contains(content, "'SECRET_TOKEN'") {
		t.Errorf("env keys missing from the record:\n%s", content)
	}
	if strings.Contains(content, "sk-live-do-not-record-me") {
		t.Errorf("an env VALUE reached the record — keys only:\n%s", content)
	}
}

// The reap is opportunistic, not load-bearing: it drops records nothing points
// at, keeps this session's own, and keeps a live sibling worktree's.
func TestReapLaunchRecordsKeepsLiveAndOwn(t *testing.T) {
	p, _ := testPaths(t)
	mine, err := writeLaunchRecord(p, sampleLaunchRecord())
	if err != nil {
		t.Fatal(err)
	}
	sibling := sampleLaunchRecord()
	sibling.Workdir = "/home/pete/byre/wt"
	siblingHash, err := writeLaunchRecord(p, sibling)
	if err != nil {
		t.Fatal(err)
	}
	stale := sampleLaunchRecord()
	stale.Byre = "v1.0.0"
	staleHash, err := writeLaunchRecord(p, stale)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{
		allContainers: map[string][]string{projectLabel(p): {"sib1"}},
		labels:        map[string]string{launchKey: siblingHash},
	}
	reapLaunchRecords(p, mine, []sessionRunner{f}, nil)

	for _, h := range []string{mine, siblingHash} {
		if _, err := hostopen.ReadFileBounded(filepath.Join(launchesDir(p), h+".toml"), false, launchRecordLimit); err != nil {
			t.Errorf("record %s was reaped but is still referenced: %v", h[:8], err)
		}
	}
	if _, err := hostopen.ReadFileBounded(filepath.Join(launchesDir(p), staleHash+".toml"), false, launchRecordLimit); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unreferenced record %s survived the reap (err=%v)", staleHash[:8], err)
	}
}

// An engine that will not answer is not evidence a record is stale: the reap
// must leave everything alone rather than delete a live box's record.
func TestReapLaunchRecordsKeepsEverythingWhenTheEngineWontAnswer(t *testing.T) {
	p, _ := testPaths(t)
	hash, err := writeLaunchRecord(p, sampleLaunchRecord())
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{allErr: errors.New("daemon down")}
	reapLaunchRecords(p, "", []sessionRunner{f}, nil)
	if _, err := hostopen.ReadFileBounded(filepath.Join(launchesDir(p), hash+".toml"), false, launchRecordLimit); err != nil {
		t.Errorf("a record was reaped on an unanswerable engine: %v", err)
	}
}

// The live set spans EVERY engine byre can see. ADR 0004 stops two boxes
// existing for one WORKTREE across engines; it says nothing about siblings,
// and worktrees of a project share this store (ADR 0009) while each may
// legitimately run on a different engine. A docker launch in worktree A must
// not unlink the record of worktree B's live podman box -- B's status would
// then report a record "missing" for a box that is running.
func TestReapLaunchRecordsKeepsASiblingOnAnotherEngine(t *testing.T) {
	p, _ := testPaths(t)
	mine, err := writeLaunchRecord(p, sampleLaunchRecord())
	if err != nil {
		t.Fatal(err)
	}
	sibling := sampleLaunchRecord()
	sibling.Workdir = "/home/pete/byre/wt"
	siblingHash, err := writeLaunchRecord(p, sibling)
	if err != nil {
		t.Fatal(err)
	}
	// docker holds only this session (not listed yet); podman holds the
	// sibling worktree's live box.
	docker := &fakeRunner{}
	podman := &fakeRunner{
		engine:        runner.Podman,
		allContainers: map[string][]string{projectLabel(p): {"wt-box"}},
		labels:        map[string]string{launchKey: siblingHash},
	}
	reapLaunchRecords(p, mine, []sessionRunner{docker, podman}, nil)

	for _, h := range []string{mine, siblingHash} {
		if _, err := hostopen.ReadFileBounded(filepath.Join(launchesDir(p), h+".toml"), false, launchRecordLimit); err != nil {
			t.Errorf("record %s was reaped but a live box points at it: %v", h[:8], err)
		}
	}
	// The peer engine is consulted, not assumed: with podman NOT in the set,
	// the same call reaps the sibling — which is the bug this test pins.
	reapLaunchRecords(p, mine, []sessionRunner{docker}, nil)
	if _, err := hostopen.ReadFileBounded(filepath.Join(launchesDir(p), siblingHash+".toml"), false, launchRecordLimit); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("test is not exercising the peer set: sibling survived a docker-only reap (err=%v)", err)
	}
}

// An engine byre found and will NOT run may be holding a sibling right now,
// and byre cannot look. Uncertainty abandons the reap rather than narrowing
// the live set: a record kept too long is litter, a record deleted too early
// is a live box byre can no longer describe.
func TestReapLaunchRecordsAbortsOverADeclinedEngine(t *testing.T) {
	p, _ := testPaths(t)
	stale, err := writeLaunchRecord(p, sampleLaunchRecord())
	if err != nil {
		t.Fatal(err)
	}
	docker := &fakeRunner{}
	declined := []declinedEngine{{Engine: "podman", Err: errors.New("resolved out of a box-writable directory")}}
	reapLaunchRecords(p, "", []sessionRunner{docker}, declined)
	if _, err := hostopen.ReadFileBounded(filepath.Join(launchesDir(p), stale+".toml"), false, launchRecordLimit); err != nil {
		t.Errorf("a declined engine must abandon the reap, not widen deletion: %v", err)
	}
}

// launchReservedEnv feeds the ONE claim vocabulary, so a recorded box degrades
// the same claims a configured one does. A malformed entry is dropped rather
// than turned into a skill named after a sentence.
func TestLaunchReservedEnvParsesBackIntoTheClaimVocabulary(t *testing.T) {
	got := launchReservedEnv([]string{"pjlsergeant/devlog BYRE_SCRATCH", "junk", "", "a b c"})
	if len(got) != 1 || got[0].Skill != "pjlsergeant/devlog" || got[0].Key != "BYRE_SCRATCH" {
		t.Fatalf("got %+v", got)
	}
	if !skills.ReservedEnvTouches(got, skills.ClaimNetwork) {
		t.Errorf("a recorded reserved key must degrade the same claims as a configured one")
	}
}

// develop stamps the container with the record's address and the record lands
// in the store — the two halves of the pointer, asserted together.
func TestDevelopWritesTheLaunchRecordAndLabelsTheContainer(t *testing.T) {
	pinLaunchClock(t)
	p, _ := testPaths(t)
	f := &fakeRunner{}
	s, _, _ := testStreams("", false)
	rv := combine(config.Config{Base: "golang:1.26-bookworm"}, skills.Resolved{})
	if err := develop(f, s, p, rv, false); err != nil {
		t.Fatal(err)
	}
	if len(f.creates) != 1 {
		t.Fatalf("creates = %v", f.creates)
	}
	var hash string
	for i, a := range f.creates[0] {
		if a == "--label" && i+1 < len(f.creates[0]) && strings.HasPrefix(f.creates[0][i+1], launchKey+"=") {
			hash = strings.TrimPrefix(f.creates[0][i+1], launchKey+"=")
		}
	}
	if !launchHashRe.MatchString(hash) {
		t.Fatalf("create argv carries no %s=<sha256> label: %v", launchKey, f.creates[0])
	}
	rec, st := readLaunchRecord(p, map[string]string{launchKey: hash})
	if st != launchRecordOK {
		t.Fatalf("the labelled record does not verify: %v", st)
	}
	if rec.Image.Digest == "" || rec.Image.Base != "golang:1.26-bookworm" {
		t.Errorf("image record = %+v", rec.Image)
	}
}

// Raw run_args can name any --label, including byre's own. RunArgs re-asserts
// byre's labels AFTER the passthrough and the engine takes the last one, so a
// forged byre.launch cannot point status at a record of the author's choosing.
// Pinned here because only byre.project was pinned before, and this label is
// the one that decides what the page describes.
func TestLaunchLabelSurvivesASpoofingRunArg(t *testing.T) {
	p, _ := testPaths(t)
	forged := launchKey + "=" + strings.Repeat("f", 64)
	f := &fakeRunner{}
	s, _, _ := testStreams("", false)
	rv := combine(config.Config{RunArgs: []string{"--label", forged}}, skills.Resolved{})
	if err := develop(f, s, p, rv, false); err != nil {
		t.Fatal(err)
	}
	var seen []string
	for i, a := range f.creates[0] {
		if a == "--label" && i+1 < len(f.creates[0]) && strings.HasPrefix(f.creates[0][i+1], launchKey+"=") {
			seen = append(seen, f.creates[0][i+1])
		}
	}
	if len(seen) < 2 || seen[0] != forged {
		t.Fatalf("test setup: the forged label must reach the argv first, got %v", seen)
	}
	// Last wins at the engine, and the last one is byre's.
	last := strings.TrimPrefix(seen[len(seen)-1], launchKey+"=")
	if last == strings.Repeat("f", 64) || !launchHashRe.MatchString(last) {
		t.Fatalf("byre's own launch label must be re-asserted last, got %v", seen)
	}
	if _, st := readLaunchRecord(p, map[string]string{launchKey: last}); st != launchRecordOK {
		t.Errorf("the surviving label must address a real record: %v", st)
	}
}

// An engine that cannot answer the digest question records the failure. An
// honest empty with a stated reason beats a plausible hash byre never obtained.
func TestLaunchRecordDigestFailureIsRecordedNotGuessed(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{imageDigestErr: errors.New("no such image")}
	s, _, stderr := testStreams("", false)
	if err := develop(f, s, p, combine(config.Config{}, skills.Resolved{}), false); err != nil {
		t.Fatal(err)
	}
	var hash string
	for i, a := range f.creates[0] {
		if a == "--label" && i+1 < len(f.creates[0]) && strings.HasPrefix(f.creates[0][i+1], launchKey+"=") {
			hash = strings.TrimPrefix(f.creates[0][i+1], launchKey+"=")
		}
	}
	rec, st := readLaunchRecord(p, map[string]string{launchKey: hash})
	if st != launchRecordOK {
		t.Fatalf("state = %v", st)
	}
	if rec.Image.Digest != "" || !strings.Contains(rec.Image.DigestError, "no such image") {
		t.Errorf("image = %+v, want an empty digest with the reason recorded", rec.Image)
	}
	if !strings.Contains(stderr.String(), "couldn't read the image digest") {
		t.Errorf("the degradation must be disclosed: %s", stderr.String())
	}
}

// A store byre cannot write degrades: the box still launches, the disclosure
// says what was lost, and no launch label is stamped (a label pointing at a
// record that does not exist would be worse than none).
func TestLaunchRecordWriteFailureDegradesNeverBlocks(t *testing.T) {
	p, _ := testPaths(t)
	// A regular file where the launches DIRECTORY must go: the create fails,
	// nothing else does.
	if err := hostopen.PublishFile(launchesDir(p), "not a directory\n", 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{}
	s, _, stderr := testStreams("", false)
	if err := develop(f, s, p, combine(config.Config{}, skills.Resolved{}), false); err != nil {
		t.Fatalf("develop must not fail over its own bookkeeping: %v", err)
	}
	if !strings.Contains(stderr.String(), "couldn't write the launch record") {
		t.Errorf("the degradation must be disclosed: %s", stderr.String())
	}
	for _, a := range f.creates[0] {
		if strings.HasPrefix(a, launchKey+"=") {
			t.Errorf("a launch label was stamped with no record behind it: %v", f.creates[0])
		}
	}
	if len(f.starts) != 1 {
		t.Errorf("the session must still launch: starts = %v", f.starts)
	}
}

// Every worktree box writes its OWN record; they share the project store
// (ADR 0009) and are told apart by the address, not the path.
func TestLaunchRecordsAreOnePerBoxInOneProjectStore(t *testing.T) {
	p, _ := testPaths(t)
	a := sampleLaunchRecord()
	b := sampleLaunchRecord()
	b.Workdir = p.WorkDir + "/wt-feature"
	ha, err := writeLaunchRecord(p, a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := writeLaunchRecord(p, b)
	if err != nil {
		t.Fatal(err)
	}
	if ha == hb {
		t.Fatal("two different launches must not share an address")
	}
	entries, err := hostopen.ReadDirNoFollow(launchesDir(p))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("both records must live in the ONE project store: %d entries", len(entries))
	}
	for _, h := range []string{ha, hb} {
		if _, st := readLaunchRecord(p, map[string]string{launchKey: h}); st != launchRecordOK {
			t.Errorf("record %s does not verify: %v", h[:8], st)
		}
	}
}

var _ = project.Paths{}
