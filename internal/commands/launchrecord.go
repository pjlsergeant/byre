package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
	"github.com/pjlsergeant/byre/internal/version"
)

// launchrecord.go makes a launch DURABLE.
//
// byre models the current configuration very well and, until this file, did
// not model the resolved configuration that created a particular running box:
// container labels carried identity only, so `byre status` re-resolved the
// config as it is NOW and rendered that beside a container still holding the
// mounts and the network setting of an hour ago. The develop banner was an
// accurate launch-time record and transient stderr.
//
// What the record holds is what byre TOLD THE ENGINE -- the exposure facts as
// they went out the door -- not the config that produced them. It is one step
// CLOSER to reality than config, never a second serialization of a moving
// schema: re-deriving exposure from a recorded config would reproduce the
// very gap this closes. Env KEYS only, values never (the exit report's rule
// holds on every surface); `run_args` verbatim, because status already prints
// it verbatim and the configuration reference already says that is not the
// place for a secret.
//
// The file is named by the sha256 of its own bytes and the container carries
// byre.launch=<that hash>. Content-addressing gives integrity for free, and
// it is not decoration: under --self-edit the box can write this directory,
// so status VERIFIES the record it reads (re-hash, compare to the label)
// rather than trusting it, and a mismatch is DISCLOSED. The record only ever
// informs a human reading status; no host action is driven by it.

// LaunchRecordVersion is the schema version every record carries. A byre
// reading a record from a NEWER byre renders liveness only rather than
// guessing at fields it does not know (the packages index's lenient-decode
// stance, one version further on: lenient about unknown FIELDS, explicit
// about an unknown SCHEMA).
const LaunchRecordVersion = 1

// launchKey is the container label pointing at this session's record:
// byre.launch=<sha256 of the record's bytes>. Identity labels answer "whose
// box is this"; this one answers "what was this box built and launched with".
const launchKey = "byre.launch"

// launchRecordHeader rides every record. It is INSIDE the hashed bytes, so a
// record whose explanation was edited away no longer verifies.
const launchRecordHeader = `# byre launch record -- written under the setup lock at container create,
# from the same resolution that fed the engine. This file is addressed by the
# sha256 of its own bytes; the container carries byre.launch=<that hash>, and
# byre re-hashes rather than trusts. It records what byre TOLD THE ENGINE --
# env KEYS only, values never. Delete it and status degrades honestly.
`

// launchRecordLimit bounds the read. A record is a few KB of paths and keys;
// three orders of magnitude of slack, and the bound is what keeps a box that
// can write this directory from making byre read a terabyte.
const launchRecordLimit = 1 << 20

// launchNow is the record's clock, injectable so a test can pin the bytes a
// record hashes to.
var launchNow = time.Now

// launchRecord is what byre handed the engine for one container.
//
// Field order is the file's order, and it is not free: TOML puts every key
// after a table header INSIDE that table, so the scalars and the primitive
// arrays must be declared before the first sub-table. (The plan's
// illustrative serialization placed env_keys / run_args after [[volumes]],
// which parses as two more volume keys -- the shape is the plan's, the
// ordering is the one that round-trips.)
type launchRecord struct {
	Record  int       `toml:"record"`
	Byre    string    `toml:"byre"`
	Created time.Time `toml:"created"`
	Project string    `toml:"project"`
	Workdir string    `toml:"workdir"`
	Engine  string    `toml:"engine"`
	// EnvKeys are the keys that went onto the engine's argv as `-e`: byre's
	// plumbing vars, the skill runtime env and the env_from_host passthrough,
	// as one set. Config [env] is absent because it never rides `-e` -- it is
	// baked into the image by the Dockerfile, which is a build fact and not
	// something byre told the engine at create. Sorted; values never recorded.
	EnvKeys []string `toml:"env_keys"`
	// RunArgs is the raw passthrough as the engine got it (skill run_args then
	// the project's own, last-wins), verbatim.
	RunArgs []string `toml:"run_args"`
	// CredentialUnlock is THIS launch's unlock outcome — a launch-time
	// fact recorded with the launch it belongs to (never project-global:
	// sibling worktrees each launch their own box). "unlocked",
	// "not-prompted", or the skip/fail outcome word; empty when nothing was
	// declared. Status shows it and claims nothing about live in-box state
	// (the design does not probe the box).
	CredentialUnlock string        `toml:"credential_unlock,omitempty"`
	Image            launchImage   `toml:"image"`
	Network          launchNetwork `toml:"network"`
	Binds            []launchBind  `toml:"binds"`
	Ports            []launchPort  `toml:"ports"`
	// Volumes are the named volumes as mounted. Name is the ENGINE's name (what
	// byre told the engine); Decl is the declared name status renders, because
	// a row reading `byre-proj-4f21bc-claude-state` answers a question nobody
	// asked.
	Volumes []launchVolume `toml:"volumes"`
	Skills  []launchSkill  `toml:"skills"`
	// Credentials are the declared credentials as this launch decrypted
	// them: name/kind/target plus the decrypt-time outcome ("scheduled" for
	// a value handed to delivery — deliberately not "delivered": the record
	// is immutable and written pre-start, and the inject's own outcome is
	// reported on stderr as it happens, never recorded as live state).
	// Names and outcomes only; values never.
	Credentials []launchCredential `toml:"credentials,omitempty"`
}

type launchCredential struct {
	Name    string `toml:"name"`
	Kind    string `toml:"kind"`
	Target  string `toml:"target"`
	Outcome string `toml:"outcome"`
}

// launchImage pins what the box RAN, which the tag alone cannot: `byre
// rebuild` moves the tag while this container keeps the image it was created
// from. Digest is the engine's own image id; Base is the EFFECTIVE base (an
// empty `base` key is resolved to gen.DefaultBase before it is written), so
// the record stays self-contained if a later byre changes that default.
type launchImage struct {
	Tag    string `toml:"tag"`
	Digest string `toml:"digest"`
	// DigestError is why the digest is empty. An honest empty with a stated
	// reason beats a guess: the digest is an inspect away from the build, and
	// an engine that would not answer must not turn into a plausible hash.
	DigestError string `toml:"digest_error,omitempty"`
	Base        string `toml:"base"`
}

// launchNetwork is the posture byre enforced and the lists it enforced it
// with -- the same strings the netns helper received, so the record and the
// enforcement cannot disagree about what the box could reach.
type launchNetwork struct {
	Posture      string `toml:"posture,omitempty"`
	PostureSkill string `toml:"posture_skill,omitempty"`
	// Egress is the resolved allowlist as one space-separated string --
	// BYRE_EGRESS's own spelling. Empty under a posture that enforces no
	// allowlist.
	Egress string `toml:"egress"`
	// EgressDeny is the config's `!host[:port]` closure set, as the helper got
	// it (BYRE_EGRESS_DENY).
	EgressDeny []string `toml:"egress_deny,omitempty"`
	// ReservedEnv are the skill-set BYRE_ keys, attributed: "<skill> <KEY>".
	// Strings, not a map: this is a record of what was set, and the claims each
	// key skews are byre's own inventory to apply at render time.
	ReservedEnv []string `toml:"reserved_env,omitempty"`
	// ProjectRunArgs / RawBuild are the project's own raw escape hatches AS
	// LAUNCHED -- the two inputs (beside ReservedEnv) that make the posture
	// claim stop asserting. Recorded rather than re-read at render time: a
	// hedge computed from today's config would describe the wrong box, which
	// is the whole failure this file exists to end.
	ProjectRunArgs bool `toml:"project_run_args,omitempty"`
	RawBuild       bool `toml:"raw_build,omitempty"`
}

type launchBind struct {
	Host   string `toml:"host"`
	Target string `toml:"target"`
	Mode   string `toml:"mode"`
}

type launchPort struct {
	Interface string `toml:"interface"`
	Host      int    `toml:"host"`
	Container int    `toml:"container"`
}

type launchVolume struct {
	Name   string `toml:"name"`
	Target string `toml:"target"`
	Decl   string `toml:"decl,omitempty"`
	Role   string `toml:"role,omitempty"`
	Scope  string `toml:"scope"`
	// Sharing is the concurrency contract the volume was mounted under,
	// always written (never omitted) so a record that LACKS the key is
	// recognisably one a byre without the vocabulary wrote. Reading it back
	// as shared is not a guess: config and skill files both decode strictly,
	// so a byre that did not know the key refused outright any declaration
	// carrying it -- no box of that vintage can have mounted an exclusive
	// volume.
	Sharing string `toml:"sharing"`
}

type launchSkill struct {
	Name       string `toml:"name"`
	Provenance string `toml:"provenance,omitempty"`
}

// launchRecordOf captures the record from the post-lock resolution and the
// assembled run params. It DERIVES nothing: every field is read off what the
// engine is about to be handed, which is the whole point -- a record computed
// from config a second time would be a second chance to be wrong.
func launchRecordOf(paths project.Paths, rv resolved, params runner.RunParams, eng runner.Engine, img launchImage) launchRecord {
	rec := launchRecord{
		Record:  LaunchRecordVersion,
		Byre:    version.String(),
		Created: launchNow().UTC().Truncate(time.Second),
		Project: paths.ID,
		Workdir: paths.WorkDir,
		Engine:  string(eng),
		RunArgs: append([]string{}, params.RunArgs...),
		Image:   img,
	}
	rec.EnvKeys = slices.Sorted(maps.Keys(params.Env))
	posture, postureSkill := rv.skills.NetworkPosture()
	rec.Network = launchNetwork{
		Posture:        posture,
		PostureSkill:   postureSkill,
		Egress:         strings.Join(resolvedEgress(rv), " "),
		EgressDeny:     append([]string{}, rv.cfg.EgressClosed...),
		ProjectRunArgs: len(rv.cfg.RunArgs) > 0,
		RawBuild:       len(rv.cfg.DockerfilePre)+len(rv.cfg.DockerfilePost) > 0,
	}
	for _, e := range rv.skills.ReservedEnv() {
		rec.Network.ReservedEnv = append(rec.Network.ReservedEnv, e.Skill+" "+e.Key)
	}
	// The workspace bind leads, exactly as RunArgs emits it, so the record's
	// bind list IS the engine's --mount list in order.
	if params.WorkspaceHost != "" {
		rec.Binds = append(rec.Binds, launchBind{Host: params.WorkspaceHost, Target: params.WorkspaceTarget, Mode: "rw"})
	}
	for _, b := range params.Binds {
		mode := "ro"
		if b.Mode == "rw" {
			mode = "rw"
		}
		rec.Binds = append(rec.Binds, launchBind{Host: b.Host, Target: b.Target, Mode: mode})
	}
	for _, p := range params.Ports {
		rec.Ports = append(rec.Ports, launchPort{Interface: p.Interface, Host: p.Host, Container: p.Container})
	}
	// The declared name/role/scope come from the resolved set the volume names
	// were minted from, matched on the engine name -- the same pairing
	// runParams made one step earlier.
	decl := map[string]config.Volume{}
	for _, v := range rv.volumes {
		decl[scopedVolumeName(paths.ID, os.Getuid(), v)] = v
	}
	for _, v := range params.Volumes {
		lv := launchVolume{Name: v.Name, Target: v.Target, Scope: "project", Sharing: "shared"}
		if d, ok := decl[v.Name]; ok {
			lv.Decl, lv.Role = d.Name, d.Role
			if d.MachineScoped() {
				lv.Scope = "machine"
			}
			if d.Exclusive() {
				lv.Sharing = "exclusive"
			}
		}
		rec.Volumes = append(rec.Volumes, lv)
	}
	for _, name := range rv.skills.Names() {
		id, prov := pkgParts(rv.cat, name, tierFull)
		rec.Skills = append(rec.Skills, launchSkill{Name: id, Provenance: prov})
	}
	return rec
}

// imageRecord asks the engine what the freshly-built tag resolves to. The
// digest is the record's quiet win -- `byre rebuild` moves the tag, and only
// this pins what the running box was BUILT from -- so it is worth an inspect,
// and worth saying so when the inspect fails: an empty digest with a stated
// reason is a fact, a plausible-looking hash byre did not obtain is not.
func imageRecord(r imageRunner, w io.Writer, tag, base string) launchImage {
	// The EFFECTIVE base, not the config spelling. An empty `base` key means
	// gen.DefaultBase, and a record holding "" would mean "whatever
	// DefaultBase meant on the byre that wrote this" -- a value only
	// re-derivable by asking a LATER byre what its default is now, which is
	// the exact re-derivation this file exists to abolish. Upgrade byre with
	// a new default and every such record silently starts describing an image
	// its box never ran. Resolved once, here, where the record is assembled.
	img := launchImage{Tag: tag, Base: baseEffective(base)}
	digest, err := r.ImageDigest(tag)
	if err != nil {
		img.DigestError = firstLine(err.Error())
		// The engine's own stderr rides the error text -- data, not control.
		dataf(w, "byre: couldn't read the image digest for the launch record (%s) — the record pins the tag only\n", img.DigestError)
		return img
	}
	img.Digest = digest
	return img
}

// encodeLaunchRecord renders the record and returns it with its own sha256.
// The hash covers the WHOLE file, header comment included: the address and
// the bytes at that address are one thing, and a reader re-hashing what it
// read is what makes the address mean anything on a store a box can write.
func encodeLaunchRecord(rec launchRecord) (content, hash string, err error) {
	b, err := toml.Marshal(rec)
	if err != nil {
		return "", "", err
	}
	content = launchRecordHeader + string(b)
	sum := sha256.Sum256([]byte(content))
	return content, hex.EncodeToString(sum[:]), nil
}

// launchesDir is where a project's records live. Shared across worktrees like
// everything else in the store (ADR 0009): each worktree box writes its own
// record, and the container label -- not the path -- is what points at it.
func launchesDir(paths project.Paths) string { return filepath.Join(paths.Dir, "launches") }

// launchHashRe is the shape a record hash has. Every path component and every
// label value byre builds from a hash is checked against it, because the
// label comes back off a CONTAINER and the file name comes off a DIRECTORY --
// both agent-reachable under --self-edit, and a hash that reached
// filepath.Join unchecked would be a path traversal wearing a digest's
// clothes.
var launchHashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// writeLaunchRecord publishes the record and returns its hash, which the
// caller puts on the container as byre.launch=<hash>. Called under the setup
// lock, from the post-lock resolution, immediately before Create.
func writeLaunchRecord(paths project.Paths, rec launchRecord) (string, error) {
	content, hash, err := encodeLaunchRecord(rec)
	if err != nil {
		return "", err
	}
	// The tail byre creates for itself, anchored: a symlink standing at
	// `launches` is byre's own directory replaced, and every record written
	// through it would land wherever it points.
	if err := hostopen.MkdirAllIn(paths.Home, filepath.Join("projects", paths.ID, "launches"), 0o700); err != nil {
		return "", err
	}
	if err := hostopen.PublishFile(filepath.Join(launchesDir(paths), hash+".toml"), content, 0o600); err != nil {
		return "", err
	}
	return hash, nil
}

// launchState is what status learned about the running box's record. Every
// value other than launchRecordOK means status must qualify its rows rather
// than describe a box it cannot vouch for.
type launchState int

const (
	launchNone       launchState = iota // no box running, or no record to look for
	launchRecordOK                      // read and verified against the label
	launchPreRecord                     // a running box with no byre.launch label (an older byre)
	launchMissing                       // labelled, but the record is gone
	launchTampered                      // the bytes on disk do not hash to the label
	launchUnreadable                    // present, unverifiable, or malformed
	launchNewer                         // a schema this byre does not know
)

// launchDegradeNote is the qualifier status prints for a running box whose
// record it could not use. One sentence per state, naming the state and what
// the rows therefore describe -- never a guess about the box.
func launchDegradeNote(st launchState) string {
	switch st {
	case launchPreRecord:
		return "no launch record — this box predates launch records (an older byre, or the record write failed at launch), so the rows above describe the CURRENT CONFIG, not this box"
	case launchMissing:
		return "launch record missing — the box points at a record that is no longer in the store, so the rows above describe the CURRENT CONFIG, not this box"
	case launchTampered:
		return "launch record does NOT match its own address — the stored bytes hash to something else, so byre will not read them; the rows above describe the CURRENT CONFIG"
	case launchUnreadable:
		return "launch record present but unreadable (byre could not look: not a regular file, oversize, or refused) — the rows above describe the CURRENT CONFIG, not this box"
	case launchNewer:
		return "launch record written by a NEWER byre (schema beyond this build) — byre reports liveness only for this box; the rows above describe the CURRENT CONFIG"
	default:
		return ""
	}
}

// readLaunchRecord loads and VERIFIES the record a container's label points
// at. Verification is the contract, not a nicety: under --self-edit the box
// owns this directory, so byre re-hashes what it read and compares it to the
// label the container carries. A failure of any kind returns a state, never a
// partial record -- status then qualifies its rows instead of rendering
// half a box.
func readLaunchRecord(paths project.Paths, labels map[string]string) (*launchRecord, launchState) {
	hash := labels[launchKey]
	if hash == "" {
		return nil, launchPreRecord
	}
	if !launchHashRe.MatchString(hash) {
		// A label value that is not a hash cannot address a record; treating it
		// as a file name would let a container's own label choose the path.
		return nil, launchUnreadable
	}
	b, err := hostopen.ReadFileBounded(filepath.Join(launchesDir(paths), hash+".toml"), false, launchRecordLimit)
	if err != nil {
		// PROVABLE absence is the only failure that means "missing". Every
		// other shape -- a FIFO where the record was, an oversize file, an
		// unreadable mode, an I/O error -- is byre unable to LOOK, and under
		// --self-edit a box can arrange each of them deliberately. Reporting
		// those as "no longer in the store" would hand an agent a way to make
		// status say the record was deleted when it is sitting right there.
		if errors.Is(err, fs.ErrNotExist) {
			return nil, launchMissing
		}
		return nil, launchUnreadable
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != hash {
		return nil, launchTampered
	}
	var rec launchRecord
	// Lenient about unknown FIELDS (two byre versions share one store; a field
	// added later must not make the record unreadable), explicit about an
	// unknown SCHEMA below.
	if err := toml.Unmarshal(b, &rec); err != nil {
		return nil, launchUnreadable
	}
	if rec.Record > LaunchRecordVersion {
		return nil, launchNewer
	}
	if rec.Record < 1 {
		return nil, launchUnreadable
	}
	return &rec, launchRecordOK
}

// reapLaunchRecords removes records no container of this project still points
// at. Opportunistic and never load-bearing: it runs under the setup lock after
// a create, every failure abandons the whole reap, and a record that survives
// costs a few hundred bytes. keep is this session's own hash, which the engine
// may not have listed yet.
//
// engines must be EVERY engine byre can see, not the configured one. ADR 0004
// stops two boxes existing for one WORKTREE across engines; it says nothing
// about siblings, and worktrees of a project share this store (ADR 0009) while
// each may legitimately run on a different engine. Reaping from the configured
// engine's view alone would let a docker launch in worktree A unlink the
// record of worktree B's live podman box, and B's status would then report a
// record "missing" for a box that is running -- byre lying about the one thing
// this file exists to tell the truth about.
//
// declined is the same asymmetry installedEngines draws: an engine byre found
// and will not run may be holding a sibling right now, and byre cannot look.
// Every uncertainty ABANDONS the reap rather than narrowing the live set,
// because the two outcomes are not symmetric -- a record kept too long is
// litter, a record deleted too early is a live box byre can no longer describe.
func reapLaunchRecords(paths project.Paths, keep string, engines []sessionRunner, declined []declinedEngine) {
	if len(declined) > 0 {
		return // an engine byre cannot drive may be holding a sibling's box
	}
	live := map[string]bool{keep: true}
	for _, r := range engines {
		ids, err := r.ContainersByLabel(projectLabel(paths))
		if err != nil {
			return // an unanswerable engine is not evidence that a record is stale
		}
		for _, id := range ids {
			labels, lerr := r.ContainerLabels(id)
			if lerr != nil {
				return
			}
			if h := labels[launchKey]; h != "" {
				live[h] = true
			}
		}
	}
	dir := launchesDir(paths)
	entries, err := hostopen.ReadDirNoFollow(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".toml")
		if !ok || !launchHashRe.MatchString(name) || live[name] {
			continue
		}
		// Unlink, which acts on the name and never on what a link points at.
		_ = hostopen.PlainRemove(filepath.Join(dir, e.Name()), hostopen.StoreOwned)
	}
}

// recordLaunch writes the record and hands back the label to stamp on the
// container. A failure DEGRADES: the box still launches, and the disclosure
// says exactly what the user loses (status will describe the next launch
// rather than this box) -- byre never blocks a session over its own
// bookkeeping.
func recordLaunch(w io.Writer, paths project.Paths, rec launchRecord) (label, hash string) {
	h, err := writeLaunchRecord(paths, rec)
	if err != nil {
		dataf(w, "byre: couldn't write the launch record (%v) — `byre status` will describe the next launch, not this box\n", err)
		return "", ""
	}
	return launchKey + "=" + h, h
}

// launchReservedEnv parses the record's attributed reserved-env strings back
// into the vocabulary every claim surface consults (skills.ReservedEnvSet), so
// a recorded box degrades the same claims a configured one does -- through the
// one owner, not a second reading of the same fact. An entry that is not
// "<skill> <KEY>" is dropped: a malformed line must not become a skill named
// after a whole sentence.
func launchReservedEnv(entries []string) []skills.ReservedEnvSet {
	var out []skills.ReservedEnvSet
	for _, e := range entries {
		skill, key, ok := strings.Cut(strings.TrimSpace(e), " ")
		if !ok || skill == "" || key == "" || strings.Contains(key, " ") {
			continue
		}
		out = append(out, skills.ReservedEnvSet{Skill: skill, Key: key})
	}
	return out
}

// launchEgress splits the recorded allowlist string back into host:port
// entries. Attribution is deliberately NOT recorded -- the record holds the
// one string the enforcement consumed, so the rows it feeds say the launch
// record asked for them rather than inventing a skill name.
func launchEgress(s string) []string { return strings.Fields(s) }
