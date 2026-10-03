package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pjlsergeant/byre/internal/backup"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/version"
)

// BackupOptions are `byre backup`'s flags. Flags only in this cut: the form
// an editor would put over them is a follow-up on its own merits.
type BackupOptions struct {
	// Output is --output: where the file goes. "" is the default name in the
	// current directory.
	Output string
	// NoVolumes is every --no-volume: LOGICAL names (the part after the
	// project prefix), matched as strings against the carried candidates.
	NoVolumes []string
	// NoCredentials deletes the [credentials] block and every credential row
	// from the COPY that goes in the file. The source file is untouched.
	NoCredentials bool
	// Yes skips the terminal prompt.
	Yes bool
	// Ignore is every --ignore-<engine>: the user saying an installed engine is
	// not running and backup should go ahead without checking it. The engine
	// backup READS cannot be ignored -- see Backup.
	Ignore IgnoreEngines
}

// Backup implements `byre backup`: one gzip tar holding the project's config
// and its state volumes, written with the project completely still.
//
// It writes nothing into the project and removes nothing from the engine. The
// only durable thing it leaves behind on a failure is an image it had to pull
// to run the capture, which is what any pull leaves.
func Backup(s Streams, projectDir string, opts BackupOptions) error {
	// A linked worktree resolves to its project (ADR 0009): a backup taken
	// from a side checkout is a backup of the repo's project, named for the
	// main directory.
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	// The collision fence before the store is read at all: a record naming
	// another path means this id is not ours, and backup must never copy a
	// colliding project's config and volumes out.
	if err := paths.ValidateExisting(); err != nil {
		return err
	}
	cfgPath := filepath.Join(paths.Dir, config.ProjectConfigName)
	// The store project dir is what --self-edit mounts, so the probe is
	// no-follow and the read below is bounded.
	switch ok, perr := hostopen.ExistsNoFollow(cfgPath); {
	case perr != nil:
		return fmt.Errorf("checking for this project's config: %w", perr)
	case !ok:
		return fmt.Errorf("this project has no config (%s), so there is nothing to back up — run `byre develop` or `byre preset apply` first", cfgPath)
	}
	// The set develop runs, not the cascade alone: the volume classification
	// and the references both come from it, so a missing layer, template or
	// package refuses HERE with that loader's own message.
	rv, err := resolve(paths, projectDir, s.Err)
	if err != nil {
		return err
	}
	roots := boxWritableRoots(paths)
	// Backup speaks in totals ("the project is still"), so it enumerates
	// engines the way reset and forget do: a declined binary refuses rather
	// than dropping out of the count.
	engines, err := lifecycleEngines(roots)
	if err != nil {
		return err
	}
	eng, exe, err := runner.Detect(rv.cfg.Engine, hostexec.Looker(roots))
	if err != nil {
		return err
	}
	var source engineRunner
	var others []engineRunner
	for _, r := range engines {
		if r.Engine() == eng {
			if source == nil {
				source = r
			}
			continue
		}
		others = append(others, r)
	}
	if source == nil {
		// Detection and enumeration disagreed, which takes a PATH that moved
		// between the two probes. The detected engine is the one the config
		// names, so it is the one to read.
		source = runner.New(eng, exe)
	}
	// develop's own mode-select, so rootless Podman without keep-id refuses
	// here exactly as it refuses there.
	ident, err := resolveIdentity(s.Err, source)
	if err != nil {
		return err
	}
	// One run id for the whole invocation: it labels every helper (including
	// the preflight, which runs before the prompt) and names the staging
	// directory, so cleanup can find exactly this run's containers.
	runID, err := hostopen.NewRunID()
	if err != nil {
		return err
	}
	return (&backupRun{
		s: s, paths: paths, rv: rv, source: source, others: others,
		ident: ident, uid: os.Getuid(), gid: os.Getgid(), runID: runID, opts: opts,
	}).run()
}

// backupRun is one invocation's pinned inputs: what Backup resolved on the
// host (engines, identity, run id) and cannot change under the lock, plus the
// project view, which can and is re-read there.
type backupRun struct {
	s      Streams
	paths  project.Paths
	rv     resolved
	source engineRunner
	others []engineRunner
	ident  runner.Identity
	uid    int
	gid    int
	runID  string
	opts   BackupOptions
	// ignore is what --ignore-<engine> came to over the OTHER installed engines:
	// which ones were dropped from others, and which flag named an engine that
	// is not here at all. Set by run(), so the filter has one owner
	// (splitEngines) and a test can hand in an engine it must never query.
	ignore ignoreSplit
	// image is the helper image step 2 picked and proved -- a project image
	// this engine already had, or the resolved base it pulled. Named in the
	// preview and the summary, because which image ran the capture is part of
	// what the backup is.
	image string

	// sig is this run's ONE SIGINT handler, installed where staging is created
	// and switched to cancelling the context at the first capture. nil when a
	// test pinned its own cancellation.
	sig *runInterrupts
	// cancelled is the cancellation the captures and the publish watch. locked
	// switches the handler to it at the first capture, so a Ctrl-C from there on
	// clears this run's helpers and staging and publishes nothing instead of
	// killing byre with a helper holding a volume open and a staging directory
	// full of volume bytes; a caller that set one already (a test) keeps it, and
	// then no real handler is installed at all.
	cancelled context.Context
	// stopSignals undoes that handler. The cancellation path calls it as its
	// first act, and so does the publish once the file is linked: a helper that
	// cannot be ended leaves the wait bounded but finite, and a SECOND Ctrl-C
	// must then kill byre the ordinary way rather than being swallowed by a
	// handler that already fired.
	stopSignals func()
	// cleanupWait bounds the wait for a cancelled capture's helper. 0 is the
	// engine cleanup timeout; a test pins a shorter one.
	cleanupWait time.Duration
	// payloadOpened is a test seam, nil in production: the publish's own stream
	// is the one stage a unit test cannot interrupt from outside, so a test
	// fires its injected cancellation from here -- as a Ctrl-C lands mid-stream
	// -- and the wrapped reader below ends the write.
	payloadOpened func()
	// streamWritten is the second seam of the same kind: the window between the
	// last payload byte and the link is the staged file's fsync, which no
	// payload reader can be inside, so a test fires its cancellation from here
	// -- as a Ctrl-C lands during that sync -- and beforeLink ends the publish.
	streamWritten func()
}

// beforeLink is the question the publish asks once more after the last payload
// byte is written and fsynced, immediately before the link that commits the
// file: the link is the point past which a Ctrl-C can no longer unmake the
// backup, and the fsync in front of it is the slowest stretch of the whole
// publish, so a cancellation that landed there must fail the publish rather
// than land a file the user has already given up on. The answer travels as
// errBackupCancelled, the cancelled exit every other step takes.
func (b *backupRun) beforeLink() error {
	if b.streamWritten != nil {
		b.streamWritten()
	}
	if b.cancelled.Err() != nil {
		return errBackupCancelled
	}
	return nil
}

// errBackupCancelled is the capture's Ctrl-C, travelling as an error so the
// locked step can tell it from a failure: a cancellation publishes nothing and
// exits 1 having said so, where a failure reports its cause.
var errBackupCancelled = errors.New("backup cancelled")

// backupCancelledLine is the one sentence a cancelled backup ends on, after
// the account of what it cleared.
const backupCancelledLine = "byre: backup cancelled; nothing written"

// engineSet is every installed engine, the source first: the stillness sweep's
// list, because a container of this project on an engine the config no longer
// names is still a session byre must not copy state out from under.
func (b *backupRun) engineSet() []engineRunner {
	return append([]engineRunner{b.source}, b.others...)
}

// verb is how backup names itself in an engine-query refusal, and which engine
// that refusal must NOT offer to skip: the source engine is what the file is
// made of.
func (b *backupRun) verb() totalsVerb {
	return totalsVerb{name: "backup", source: b.source.Engine()}
}

func (b *backupRun) run() error {
	// The one engine --ignore cannot name: every volume in the file comes from
	// it, so "go ahead without checking it" has no meaning here. Refused rather
	// than ignored with a note, because the user asked for something backup
	// cannot do.
	if src := b.source.Engine(); b.opts.Ignore.Has(src) {
		return fmt.Errorf("--ignore-%s: %s is the engine this backup reads, so it cannot be skipped — drop the flag, or point `engine` at the other engine", src, src)
	}
	// --ignore-<engine> is applied ONCE, here: from this point `others` is the
	// set backup queries, and b.ignore is what it was told to leave out.
	b.ignore = splitEngines(b.others, b.opts.Ignore)
	b.others = b.ignore.query
	// Step 2: what the backup would contain, on this engine, with these flags.
	plan, err := planBackup(b.paths, b.rv, b.source, b.others, b.uid, b.opts, b.verb())
	if err != nil {
		return err
	}
	out, err := backupOutput(b.paths, b.opts.Output, time.Now())
	if err != nil {
		return err
	}
	image, pulled, err := b.pickHelperImage()
	if err != nil {
		return err
	}
	b.image = image
	hp := newHelperPlan(b.paths, image, b.ident, b.runID)
	// Prove the image before anything else runs in it: the capture command,
	// complete, against an empty scratch directory. An image whose tar
	// rejects these flags would otherwise produce a file the format refuses.
	if err := proveHelperImage(b.source, hp, "capture", capturePreflightScript(hp.mnt), nil); err != nil {
		cleanupRun(b.s.Err, b.source, b.runID, nil)
		return err
	}

	// Step 3: the preview and the one y/n. Off a terminal there is no prompt
	// and no preview; every list it would have shown prints in the summary.
	if b.s.TTY {
		b.renderPreview(out, plan, pulled)
		if !b.opts.Yes && !confirmed(b.s.Err, b.s.In, "Proceed? [y/N] ") {
			fmt.Fprintln(b.s.Err, "byre: not backed up; nothing written.")
			if pulled {
				dataf(b.s.Err, "byre: the image %s byre pulled stays, as any pull does.\n", image)
			}
			return nil
		}
	}

	// Steps 4-6: everything that reads the project's state happens under the
	// lock, so a develop cannot start between the stillness sweep and the
	// last capture.
	return withSetupLockProject(b.s.Err, b.paths, func() error {
		return b.locked(plan, out, hp, pulled)
	})
}

// locked is steps 4 to 6: the re-resolution the file actually records, the
// stillness sweep, the captures and the publish.
func (b *backupRun) locked(reviewed backupPlan, out string, hp helperPlan, pulled bool) error {
	// The fence again, under the lock: the store this is about to read must
	// still be this project's.
	if err := b.paths.ValidateExisting(); err != nil {
		return err
	}
	fresh, err := b.rv.refresh()
	if err != nil {
		return err
	}
	b.rv = fresh
	plan, err := planBackup(b.paths, fresh, b.source, b.others, b.uid, b.opts, b.verb())
	if err != nil {
		return err
	}
	// One resolution is what the file records. A config save or a layer edit
	// that landed while the user was reading the preview changes what the
	// backup means, and byre will not quietly write the other one.
	if what := reviewed.drift(plan); what != "" {
		return fmt.Errorf("%s changed while you were reviewing; re-run byre backup", what)
	}
	// The stillness sweep: reset's project-label, any-state, every-engine
	// check with the removal taken out. Nothing is stopped or removed. An
	// --ignore-<engine> engine is not in this set at all, which is the whole of
	// what the flag does.
	for _, r := range b.engineSet() {
		if err := refuseUnlessStill(b.s.Err, b.verb(), r, b.paths.ID); err != nil {
			return err
		}
	}

	// Step 5: the config copy and one capture per carried volume, into
	// staging, hashed on the way.
	// planBackup already stripped the credentials when --no-credentials asked
	// for it, so these are the bytes every surface has been speaking of.
	cfgBytes := plan.cfg
	// The state the index records is derived from the bytes that go IN the
	// file, which is the only thing a restore can check it against.
	credState, err := backup.CredentialState(cfgBytes)
	if err != nil {
		return fmt.Errorf("reading the config's credential state: %w", err)
	}

	noteStaleStaging(b.s.Err, b.paths.Home, b.runID)
	st, err := backup.NewStaging(b.paths.Home, b.runID)
	if err != nil {
		return err
	}
	// Staging exists, so an interrupt now has something to clear: the handler
	// goes on HERE (see runInterrupts). Backup's pre-lock stretch makes nothing
	// of its own, so there is nothing earlier for it to guard.
	b.sig = armRunInterrupts(b.cancelled, func() { b.cancelBeforeTheCaptures(st) })
	defer b.sig.stop()
	// A measurable staging filesystem with no room at all cannot hold the
	// first payload; byre cannot ask the engine how big a volume is, so this
	// is the only pre-capture disk answer there is. The rest of the check is
	// the output filesystem's, below, where the sizes are known.
	if free, ferr := st.FreeBytes(); ferr != nil || free == 0 {
		cleanupRun(b.s.Err, b.source, b.runID, st)
		if ferr != nil {
			return ferr
		}
		return fmt.Errorf("the filesystem holding %s has no free space; the backup's volume payloads are staged there", st.Path())
	}

	// The critical phase begins with the first capture: from here an interrupt
	// cancels the context the captures and the publish watch, because a helper
	// mid-archive has to be ended rather than dropped.
	ctx, stop := b.sig.enterCritical(b.cancelled)
	defer stop()
	b.cancelled, b.stopSignals = ctx, stop

	caps, err := b.capture(st, hp, plan.carried)
	if err != nil {
		if errors.Is(err, errBackupCancelled) {
			// Ending the capture's helper is what made it return, so the sweep
			// is done and only staging is left.
			return b.endCancelled(st, true)
		}
		cleanupRun(b.s.Err, b.source, b.runID, st)
		return err
	}

	// Step 6: the index, then the one exclusive publish.
	var rows []backup.Volume
	var payloads []backup.Payload
	var staged int64
	for _, c := range caps {
		rows = append(rows, c.row)
		staged += c.row.Bytes
		member := c.member
		payloads = append(payloads, backup.Payload{
			Name: c.row.Name, Size: c.row.Bytes,
			// Read through the cancellation: the publish streams every carried
			// volume, which over a large one takes minutes, and a Ctrl-C there
			// has to stop the file from appearing rather than be swallowed.
			Open: func() (io.ReadCloser, error) {
				f, err := st.Open(member)
				if err != nil {
					return nil, err
				}
				if b.payloadOpened != nil {
					b.payloadOpened()
				}
				return &cancelReader{r: f, ctx: b.cancelled}, nil
			},
		})
	}
	if err := checkOutputSpace(out, staged); err != nil {
		cleanupRun(b.s.Err, b.source, b.runID, st)
		return err
	}
	ix := backup.Index{
		Format:         backup.Format,
		ByreVersion:    version.String(),
		MinByreVersion: backup.MinByreVersion,
		Folder:         filepath.Base(b.paths.Canonical),
		Engine:         string(b.source.Engine()),
		Config: backup.ConfigRow{
			Bytes:       int64(len(cfgBytes)),
			SHA256:      backup.DigestBytes(cfgBytes),
			Credentials: credState,
		},
		Volumes:    rows,
		References: plan.refs,
	}
	// A Ctrl-C between the last capture and the publish. The payload readers
	// carry the cancellation INTO the stream, but a config-only backup has no
	// payload to carry it, and everything from here on either lands a file or
	// does not -- so the question is asked once more right before the write.
	if b.cancelled.Err() != nil {
		return b.endCancelled(st, false)
	}
	werr := backup.Write(out, volumePrefix(b.paths.ID), ix, cfgBytes, payloads, b.beforeLink)
	unsynced := errors.Is(werr, hostopen.ErrPublishedUnsynced)
	if werr != nil && !unsynced {
		if errors.Is(werr, errBackupCancelled) {
			// A payload read or the check before the link answered with the
			// cancellation, so Write stopped before the link and hostopen removed
			// its staged temp: there is no output entry.
			return b.endCancelled(st, false)
		}
		cleanupRun(b.s.Err, b.source, b.runID, st)
		if errors.Is(werr, fs.ErrExist) {
			return fmt.Errorf("%s already exists and byre backup never overwrites — move it, or name another path with --output: %w", out, werr)
		}
		return werr
	}
	// The file is LINKED (whole, even when its directory could not be synced),
	// so a Ctrl-C can no longer unmake it: the handler comes off here and an
	// interrupt during the summary kills byre the ordinary way.
	b.stopSignals()
	removeStaging(b.s.Err, st)
	b.renderSummary(out, plan, caps, pulled)
	if unsynced {
		// The file is complete; only the directory entry's durability is
		// unconfirmed. Exit 1 saying exactly that.
		return fmt.Errorf("%s is written, but %w", out, werr)
	}
	return nil
}

// ---------------------------------------------------------------- the plan

// notCarried is one volume the backup leaves behind: the logical name, the
// reason every surface prints for it, and the rank that picks WHICH reason a
// --no-volume refusal names when several apply. The ranks are the stated
// order: cache, then machine-scoped, then owned by another project, then
// another engine.
type notCarried struct {
	name   string
	reason string
	rank   int
}

const (
	rankCache = iota
	rankMachine
	rankOtherProject
	rankOtherEngine
	rankDropped
)

// backupPlan is ONE resolution of what the backup would contain. Step 2
// builds it for the preview and step 4 builds it again under the lock: it is
// a value rather than a sequence of statements precisely so the two can be
// compared, and a config save or layer edit that landed in between refuses
// instead of being silently written.
type backupPlan struct {
	engineKey  string          // the config's own `engine` key, "" when unset
	carried    []string        // logical names, sorted
	candidates []string        // what was carried before --no-volume dropped any
	declared   map[string]bool // logical names the resolved set declares
	notCarried []notCarried
	refs       backup.References
	cfg        []byte // the project config's bytes, as the file will carry them
}

// drift names the first difference between the resolution the user reviewed
// and the one under the lock, for the refusal that sends them round again.
// "" means the two agree.
func (p backupPlan) drift(q backupPlan) string {
	switch {
	case p.engineKey != q.engineKey:
		return "the configured engine"
	case !slices.Equal(p.carried, q.carried):
		return "the volumes this backup carries"
	case !slices.Equal(p.notCarried, q.notCarried):
		return "what this backup leaves behind"
	case !reflect.DeepEqual(p.refs, q.refs):
		return "the layers, packages and host paths this project references"
	case !bytes.Equal(p.cfg, q.cfg):
		return "the project's config"
	}
	return ""
}

// planBackup classifies the project's volumes, resolves the references table
// and reads the config bytes -- everything the preview shows and the index
// records, from one read of the engine and one resolution of the project.
//
// Every volume query failure refuses: backup speaks in totals, and an engine
// it could not ask cannot be reported on. A cleanly unreachable engine's
// refusal names the way past it -- start it, or --ignore-<engine> (totalsVerb).
func planBackup(paths project.Paths, rv resolved, source engineRunner, others []engineRunner, uid int, opts BackupOptions, verb totalsVerb) (backupPlan, error) {
	var p backupPlan
	raw, err := hostopen.ReadFileBounded(filepath.Join(paths.Dir, config.ProjectConfigName), false, config.MaxConfigBytes)
	if err != nil {
		return p, err
	}
	// The bytes that GO IN the file: --no-credentials strips here, so the
	// preview, the drift compare, the summary, the index and Write all speak
	// of one set of bytes. The source file is untouched.
	if opts.NoCredentials {
		stripped, serr := backup.StripCredentials(raw)
		if serr != nil {
			return p, fmt.Errorf("dropping the credentials from the config copy: %w", serr)
		}
		raw = stripped
	}
	p.cfg = raw
	p.engineKey = rv.cfg.Engine
	// A resolved config never carries `extends` -- the chain walk consumed it
	// (config.resolveWithCatalog strips it). The leaf pointer the references
	// table names is the PROJECT config's own, read off the bytes above.
	leaf, perr := config.Parse(p.cfg)
	if perr != nil {
		return p, perr
	}
	refs, err := backupReferences(paths, rv, leaf.Extends)
	if err != nil {
		return p, err
	}
	p.refs = refs

	// What the resolved set declares, split by scope. The declarer never
	// decides whether a volume is machine-scoped for the CARRIED set -- the
	// physical name does (ADR 0017) -- but a declaration is how byre knows a
	// name is machine-scoped before any volume for it exists.
	p.declared = map[string]bool{}
	cache := map[string]bool{}
	machine := map[string]bool{}
	for _, v := range rv.volumes {
		if v.MachineScoped() {
			machine[v.Name] = true
			continue
		}
		p.declared[v.Name] = true
		if v.Role == "cache" {
			cache[v.Name] = true
		}
	}

	prefix := volumePrefix(paths.ID)
	srcVols, err := projectVolumes(source, paths.Home, paths.ID)
	if err != nil {
		return p, verb.queryErr(source.Engine(), "listing volumes", err)
	}
	// Every project volume on the source engine comes along -- including one
	// no skill in the set declares, which is a one-off `develop --agent` run's
	// state and still the project's. Only a cache-role declaration takes one
	// out.
	for _, phys := range srcVols {
		name := strings.TrimPrefix(phys, prefix)
		if cache[name] {
			continue
		}
		p.candidates = append(p.candidates, name)
	}
	sort.Strings(p.candidates)

	// One row per distinct VOLUME, not per logical name: the logical name is
	// the part after a prefix, and three different prefixes (this project's on
	// this engine, this project's on another, the machine's) plus a longer
	// project's id can all spell the same one. A machine-scoped `login` and
	// this project's `login` on the other engine are two volumes and ADR 0059
	// requires both to be named, so the key carries what distinguishes them --
	// the category, the engine, the physical name -- and a logical name appears
	// once per volume behind it. Each row's reason names its own scope
	// (machine-scoped, on <engine>, owned by project <id>), which is what tells
	// two rows of one logical name apart on the screen.
	seen := map[string]bool{}
	add := func(key, name, reason string, rank int) {
		if seen[key] {
			return
		}
		seen[key] = true
		p.notCarried = append(p.notCarried, notCarried{name: name, reason: reason, rank: rank})
	}
	for _, name := range slices.Sorted(maps.Keys(cache)) {
		add("cache:"+name, name, "not carried (cache)", rankCache)
	}
	// Machine-scoped volumes are never offered. Both spellings are listed:
	// what the set declares, and what the engine holds under the machine
	// prefix -- a volume can outlive the skill that declared it.
	physMachine, err := source.VolumesByPrefix(machineVolumePrefix(uid))
	if err != nil {
		return p, verb.queryErr(source.Engine(), "listing machine-scoped volumes", err)
	}
	for _, phys := range physMachine {
		machine[strings.TrimPrefix(phys, machineVolumePrefix(uid))] = true
	}
	for _, name := range slices.Sorted(maps.Keys(machine)) {
		add("machine:"+name, name, "not carried; the destination binds its own machine-scoped "+name, rankMachine)
	}
	// A volume under this project's prefix that a LONGER known id claims is
	// that project's state (both ids and names use "-", so one physical name
	// can spell two projects); projectVolumes already excluded it, and the
	// user hears whose it is.
	known, err := knownProjectIDs(paths.Home)
	if err != nil {
		return p, err
	}
	allPrefixed, err := source.VolumesByPrefix(prefix)
	if err != nil {
		return p, verb.queryErr(source.Engine(), "listing volumes", err)
	}
	for _, phys := range allPrefixed {
		if machineVolumeRe.MatchString(phys) {
			continue
		}
		if oid := claimingLongerID(phys, paths.ID, known); oid != "" {
			add("project:"+phys, strings.TrimPrefix(phys, prefix), "not carried: owned by project "+oid, rankOtherProject)
		}
	}
	// A project volume on an engine this backup is not reading. State can
	// live in an engine the config no longer names, and a backup that read
	// one engine must say so about the other.
	for _, r := range others {
		vols, verr := projectVolumes(r, paths.Home, paths.ID)
		if verr != nil {
			return p, verb.queryErr(r.Engine(), "listing volumes", verr)
		}
		for _, phys := range vols {
			add(string(r.Engine())+":"+phys, strings.TrimPrefix(phys, prefix),
				fmt.Sprintf("not carried (on %s; this backup reads %s)", r.Engine(), source.Engine()),
				rankOtherEngine)
		}
	}

	// replace states a reason over one byre already recorded, for the one
	// case where the user's own instruction outranks byre's reading: a
	// --no-volume name that some other rule also leaves behind is reported as
	// dropped, because that is the answer the user asked for. It works on the
	// LOGICAL name the user typed -- that is all --no-volume knows -- and the
	// rows are appended in rank order, so the first match is the lowest-ranked
	// reason byre had for that name.
	replace := func(name, reason string, rank int) {
		for i := range p.notCarried {
			if p.notCarried[i].name == name {
				p.notCarried[i].reason, p.notCarried[i].rank = reason, rank
				return
			}
		}
		add("drop:"+name, name, reason, rank)
	}

	// --no-volume takes logical names, matched as strings against the carried
	// candidates. Judged against the candidates rather than the shrinking
	// carried set, so a repeat is harmless.
	drop := map[string]bool{}
	for _, name := range opts.NoVolumes {
		if drop[name] {
			continue
		}
		drop[name] = true
		if slices.Contains(p.candidates, name) {
			replace(name, "not carried (--no-volume)", rankDropped)
			continue
		}
		if row, ok := lowestRankReason(p.notCarried, name); ok {
			return p, fmt.Errorf("--no-volume %s: that volume is not carried anyway — %s", name, row)
		}
		if len(p.candidates) == 0 {
			return p, fmt.Errorf("--no-volume %s: this project has no volumes to carry", name)
		}
		return p, fmt.Errorf("--no-volume %s: no such volume; this backup carries %s", name, strings.Join(p.candidates, ", "))
	}
	for _, name := range p.candidates {
		if !drop[name] {
			p.carried = append(p.carried, name)
		}
	}
	sort.Slice(p.notCarried, func(i, j int) bool {
		if p.notCarried[i].rank != p.notCarried[j].rank {
			return p.notCarried[i].rank < p.notCarried[j].rank
		}
		return p.notCarried[i].name < p.notCarried[j].name
	})
	return p, nil
}

// lowestRankReason is the reason a --no-volume refusal names for a name that
// is already not carried: the first that applies in the stated order.
func lowestRankReason(rows []notCarried, name string) (string, bool) {
	best := notCarried{rank: rankDropped + 1}
	for _, r := range rows {
		if r.name == name && r.rank < best.rank {
			best = r
		}
	}
	if best.reason == "" {
		return "", false
	}
	return best.reason, true
}

// backupReferences is what backup SAW in the source's resolved set, for the
// human at restore: pointers, never payloads. Nothing here is asserted at
// restore -- it prints as "what the source saw".
// leafExtends is the project config's own `extends` value, which the resolved
// view cannot supply: a resolved config never carries it.
func backupReferences(paths project.Paths, rv resolved, leafExtends string) (backup.References, error) {
	refs := backup.References{
		Layers:       []string{},
		Template:     rv.cfg.Template,
		Agent:        rv.cfg.Agent,
		Skills:       rv.skills.Names(),
		Mounts:       []string{},
		Context:      []string{},
		ClaudeSkills: []string{},
		Seeds:        []string{},
		Files:        []string{},
		Engine:       rv.cfg.Engine,
		Base:         orDefault(rv.cfg.Base, gen.DefaultBase),
		WorktreeBase: rv.cfg.WorktreeBase,
	}
	if leafExtends != "" {
		// LoadExtendsChain answers root-first (merge order); the references
		// name the chain leaf-first, the order the user reads it in.
		chain, err := config.LoadExtendsChain(paths.Home, rv.cat, leafExtends)
		if err != nil {
			return refs, err
		}
		names := config.ChainNames(chain)
		slices.Reverse(names)
		refs.Layers = names
	}
	// A disabled mount is inert (ADR 0015): it is not a name the destination
	// must satisfy, and it is not listed.
	for _, m := range rv.mounts {
		if m.Disabled {
			continue
		}
		refs.Mounts = append(refs.Mounts, hostPathAsSeen(m.Host))
	}
	for _, cd := range rv.cfg.Contexts {
		if cd.File != "" {
			refs.Context = append(refs.Context, hostPathAsSeen(cd.File))
		}
	}
	for _, cs := range rv.claudeSkills {
		// A skill's own contribution rides the package it came from; only a
		// config-home `path` is a host name this machine has to have.
		if cs.SrcDir == "" && cs.CS.Path != "" {
			refs.ClaudeSkills = append(refs.ClaudeSkills, hostPathAsSeen(cs.CS.Path))
		}
	}
	for _, v := range rv.volumes {
		if v.Seed != nil && v.Seed.Host != "" {
			refs.Seeds = append(refs.Seeds, hostPathAsSeen(v.Seed.Host))
		}
	}
	// [files] sources are project-relative (the build reads them from the
	// checkout), so what the destination must satisfy is a path IN the restored
	// tree -- named here because the restore review's requirements list names
	// them too, and the two sides of that screen must be able to disagree
	// visibly. Sorted: a map's range order is not a file format.
	for _, src := range slices.Sorted(maps.Keys(rv.cfg.Files)) {
		refs.Files = append(refs.Files, hostPathAsSeen(src))
	}
	return refs, nil
}

// hostPathAsSeen renders a declared host path the way the SOURCE resolved it,
// which is what the references table claims to be. A path byre cannot expand
// is carried as written: the table is a note, and refusing a backup over a
// malformed path a later develop would refuse anyway is not its job.
func hostPathAsSeen(p string) string {
	if expanded, err := config.ExpandTilde(p); err == nil {
		return expanded
	}
	return p
}

// ------------------------------------------------------------- the helpers

// helperPlan pins what every helper of ONE invocation shares: the image byre
// proved, the identity develop would run, the two labels that say whose
// helper this is and which run started it, and MNT -- the single per-run path
// the mount, the capture's -C and the preflight all name.
type helperPlan struct {
	image  string
	ident  runner.Identity
	labels []string
	mnt    string
}

func newHelperPlan(paths project.Paths, image string, ident runner.Identity, runID string) helperPlan {
	return helperPlan{
		image:  image,
		ident:  ident,
		labels: []string{helperLabel(paths.ID), helperRunLabel(runID)},
		mnt:    "/.byre-helper-" + runID,
	}
}

// helper builds one helper spec. TAR_OPTIONS is cleared on every one of them:
// a built image carries config-authored ENV lines, and that variable changes
// what tar does with no trace in byre's argv.
func (hp helperPlan) helper(volume string, readOnly bool, script string) runner.Helper {
	return runner.Helper{
		Image:     hp.image,
		Identity:  hp.ident,
		Labels:    hp.labels,
		Env:       map[string]string{"TAR_OPTIONS": ""},
		Volume:    volume,
		MountPath: hp.mnt,
		ReadOnly:  readOnly,
		Script:    script,
	}
}

// captureScript is the ONE spelling of the capture command: `-f -` because
// GNU tar otherwise reads TAPE, which an image can set; posix format so
// mtimes keep their nanoseconds; numeric owner because the destination
// discards ownership anyway; and MNT as the -C argument, so the volume root
// is the tar root.
func captureScript(mnt string) string {
	return "tar --format=posix --numeric-owner -c -f - -C " + mnt + " ."
}

// capturePreflightScript proves the helper image can run captureScript at
// all: tar's own banner on stdout (byre needs GNU tar -- every tar fact this
// format relies on is GNU tar's), then the COMPLETE capture command against
// an empty scratch directory, its archive discarded.
func capturePreflightScript(mnt string) string {
	return "set -e\ntar --version\nmkdir -p " + mnt + "\n" + captureScript(mnt) + " >/dev/null\n"
}

// proveHelperImage runs one preflight helper -- no volume, a scratch
// directory at MNT -- and judges it twice: tar must report itself as GNU tar,
// and the verb's complete command must run. Both refusals name the IMAGE,
// because the image is the thing the user can change.
func proveHelperImage(r helperRunner, hp helperPlan, what, script string, stdin io.Reader) error {
	var out strings.Builder
	stderr, err := r.RunHelper(hp.helper("", false, script), stdin, &out)
	banner := briefly(out.String())
	gnu := strings.Contains(out.String(), "GNU tar")
	// A banner that is not GNU tar's is the more useful answer even when the
	// run then failed: another tar is WHY it failed, and byre needs GNU tar
	// (every tar fact this format relies on is GNU tar's).
	if !gnu && banner != "" {
		return fmt.Errorf("the helper image %s does not have GNU tar, which byre's %s command needs (tar said %q%s)",
			hp.image, what, banner, engineAside(err, stderr))
	}
	if err != nil {
		return fmt.Errorf("the helper image %s cannot run byre's %s command: %w", hp.image, what, err)
	}
	if !gnu {
		return fmt.Errorf("the helper image %s did not say what its tar is, and byre's %s command needs GNU tar%s",
			hp.image, what, engineAside(nil, stderr))
	}
	return nil
}

// engineAside folds an engine error and a helper's stderr into one
// parenthetical clause, quoted so a control character in either cannot break
// the line it is printed on.
func engineAside(err error, stderr string) string {
	switch {
	case err != nil:
		return fmt.Sprintf("; the engine said %q", briefly(err.Error()))
	case strings.TrimSpace(stderr) != "":
		return fmt.Sprintf("; it said %q", briefly(stderr))
	}
	return ""
}

// briefly holds someone else's output -- a tar banner, an engine complaint --
// to one capped line for an error byre composes around it.
func briefly(s string) string {
	s = firstLine(strings.TrimSpace(s))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// pickHelperImage picks the image the capture runs in: a project image this
// engine already has (the identity's tag first), else the resolved base,
// pulled if absent. A pulled image stays, as any pull does.
func (b *backupRun) pickHelperImage() (image string, pulled bool, err error) {
	for _, tag := range imageTagCandidates(b.source, b.paths.ID, b.uid, b.gid) {
		has, herr := b.source.ImageExists(tag)
		if herr != nil {
			return "", false, b.verb().queryErr(b.source.Engine(), "checking image "+tag, herr)
		}
		if has {
			return tag, false, nil
		}
	}
	base := orDefault(b.rv.cfg.Base, gen.DefaultBase)
	has, herr := b.source.ImageExists(base)
	if herr != nil {
		return "", false, b.verb().queryErr(b.source.Engine(), "checking image "+base, herr)
	}
	if has {
		return base, false, nil
	}
	if perr := b.source.ImagePull(base); perr != nil {
		return "", false, fmt.Errorf("pulling %s to run the capture (%s): %w", base, b.source.Engine(), perr)
	}
	return base, true, nil
}

// captured is one carried volume's capture: the index row, what validating
// the staged archive found, and tar's own voice.
type captured struct {
	member string
	row    backup.Volume
	report backup.Report
	stderr string
}

// captureResult is one capture goroutine's answer, so the cancellation can
// select against the call rather than block on it.
type captureResult struct {
	c   captured
	err error
}

// capture runs one helper per carried volume, in sorted order, and stages
// each archive. The staging members are POSITIONAL (0.tar, 1.tar): no name
// from the project ever chooses a path.
//
// Every capture is watched for a Ctrl-C, which wins over the call's own
// outcome: a helper that failed because its container was taken away is that
// cancellation's consequence, not a separate failure, and reporting it as one
// would have byre blame the engine for what the user asked for.
func (b *backupRun) capture(st *backup.Staging, hp helperPlan, carried []string) ([]captured, error) {
	var out []captured
	for i, name := range carried {
		if b.cancelled.Err() != nil {
			return nil, b.cancelCapture(name, nil)
		}
		// Buffered, so a capture byre stopped waiting for can still report
		// into it and exit rather than leaking on a send nobody reads.
		done := make(chan captureResult, 1)
		go func() {
			c, err := captureVolume(b.source, hp, st, fmt.Sprintf("%d.tar", i), name, volumeName(b.paths.ID, name))
			done <- captureResult{c: c, err: err}
		}()
		select {
		case r := <-done:
			if b.cancelled.Err() != nil {
				return nil, b.cancelCapture(name, nil)
			}
			if r.err != nil {
				return nil, r.err
			}
			out = append(out, r.c)
		case <-b.cancelled.Done():
			return nil, b.cancelCapture(name, done)
		}
	}
	return out, nil
}

// cancelBeforeTheCaptures is what an interrupt does in the one window where
// this backup has made something but read nothing: staging exists and no
// capture has started. The same clearing endCancelled does, minus the exit,
// which the handler makes itself (see runInterrupts).
//
// A test calls this directly: it is the only way to reach the pre-capture
// behaviour without a terminal to interrupt from.
func (b *backupRun) cancelBeforeTheCaptures(st *backup.Staging) {
	cleanupRun(b.s.Err, b.source, b.runID, st)
	fmt.Fprintln(b.s.Err, backupCancelledLine)
}

// endCancelled is the one exit a cancelled backup takes, at whichever step the
// Ctrl-C landed: this run's helpers swept unless ending one is what brought us
// here, then staging -- it holds the volume bytes and the plaintext credentials
// among them -- then the one closing line, exit 1. Nothing is published, and the
// account of what was cleared is already on stderr, so nothing is said twice.
func (b *backupRun) endCancelled(st *backup.Staging, helpersCleared bool) error {
	b.stopSignals()
	if helpersCleared {
		removeStaging(b.s.Err, st)
	} else {
		cleanupRun(b.s.Err, b.source, b.runID, st)
	}
	fmt.Fprintln(b.s.Err, backupCancelledLine)
	return ExitError{Code: 1}
}

// cancelReader is one payload's staged bytes with the run's cancellation over
// them: once it is cancelled the next Read is the cancellation itself, so
// backup.Write fails before the link and hostopen removes the staged temp it
// was filling (PublishStreamExclusive). The cancellation travels as
// errBackupCancelled, which Write wraps, so the publish's caller tells a
// Ctrl-C from a failure the same way the capture's caller does.
type cancelReader struct {
	r   io.ReadCloser
	ctx context.Context
}

func (c *cancelReader) Read(p []byte) (int, error) {
	if c.ctx.Err() != nil {
		return 0, errBackupCancelled
	}
	return c.r.Read(p)
}

func (c *cancelReader) Close() error { return c.r.Close() }

// cancelCapture is the capture's Ctrl-C: the handler comes off, this run's
// helper containers go -- which is what makes a synchronous RunHelper return --
// and the wait for an in-flight one is bounded. pending is nil when the helper
// has already returned, and nothing but the removal is left to do.
func (b *backupRun) cancelCapture(volume string, pending <-chan captureResult) error {
	b.stopSignals()
	if pending == nil {
		if err := removeRunHelpers(b.s.Err, b.source, b.runID); err != nil {
			dataf(b.s.Err, "byre: %v\n", err)
		}
		return errBackupCancelled
	}
	if !endCancelledHelper(b.s.Err, b.source, b.runID, pending, b.cleanupWait) {
		noteHelperMayStillRun(b.s.Err, b.source, b.runID, volume, b.cleanupWait)
	}
	return errBackupCancelled
}

// captureVolume archives one volume into staging and then reads the staged
// bytes back header by header. The second pass is not a formality: it is
// where the symlink lists and the entry count come from, and a refusal there
// is a failed backup, because the file byre writes must be one byre reads.
func captureVolume(r helperRunner, hp helperPlan, st *backup.Staging, member, logical, physical string) (captured, error) {
	c := captured{member: member}
	f, err := st.Create(member)
	if err != nil {
		return c, err
	}
	h := backup.NewDigest()
	cw := &backup.Counter{W: io.MultiWriter(f, h)}
	stderr, rerr := r.RunHelper(hp.helper(physical, true, captureScript(hp.mnt)), nil, cw)
	c.stderr = stderr
	cerr := f.Close()
	if rerr != nil {
		return c, fmt.Errorf("archiving volume %s: %w", logical, rerr)
	}
	if cerr != nil {
		return c, fmt.Errorf("staging volume %s: %w", logical, cerr)
	}
	staged, err := st.Open(member)
	if err != nil {
		return c, err
	}
	defer staged.Close()
	rep, verr := backup.Validate(staged)
	if verr != nil {
		return c, fmt.Errorf("the archive of volume %s is not one byre could restore: %w", logical, verr)
	}
	c.report = rep
	c.row = backup.Volume{
		Name:    logical,
		Bytes:   cw.N,
		SHA256:  backup.HexDigest(h),
		Entries: rep.Entries,
	}
	return c, nil
}

// cleanupRun is the failure path's cleanup, in the order that matters: this
// invocation's helpers first (one of them is holding a volume), then staging.
// Never another invocation's helpers.
func cleanupRun(w io.Writer, r helperCleaner, runID string, st *backup.Staging) {
	if err := removeRunHelpers(w, r, runID); err != nil {
		dataf(w, "byre: %v\n", err)
	}
	removeStaging(w, st)
}

// removeStaging takes a run's staging directory away -- it holds the volume
// bytes and the plaintext credentials among them -- on every exit a verb makes,
// success or failure. A failure to remove it reports through the summary rather
// than replacing whatever brought the verb here. A nil staging is a run that
// never made one.
func removeStaging(w io.Writer, st *backup.Staging) {
	if st == nil {
		return
	}
	if err := st.Remove(); err != nil {
		dataf(w, "byre: %v\n", err)
	}
}

// noteStaleStaging names any other run's staging directory and leaves it:
// byre cannot tell a crashed verb's leftovers from a concurrent verb's live
// staging, and the directory holds volume bytes. The probe is unsolicited, so
// a failure to make it degrades to a note rather than stopping the backup.
func noteStaleStaging(w io.Writer, home, runID string) {
	stale, err := backup.StaleStaging(home, runID)
	if err != nil {
		dataf(w, "byre: could not check for leftover staging directories: %v\n", err)
		return
	}
	for _, dir := range stale {
		dataf(w, "byre: a staging directory from another byre run is still there: %s (it holds volume bytes; byre leaves it alone — remove it yourself once no byre is running)\n", dir)
	}
}

// checkOutputSpace refuses before the publish when the output filesystem
// cannot hold the archive. The staged payloads are the bound: gzip can only
// make them smaller.
func checkOutputSpace(out string, staged int64) error {
	dir := filepath.Dir(out)
	free, err := backup.FreeBytesIn(dir)
	if err != nil {
		return fmt.Errorf("measuring free space for %s: %w", out, err)
	}
	if free < uint64(staged) {
		return fmt.Errorf("not enough room for the backup: %s has %d bytes free and the archive needs up to %d — free some space, or name another filesystem with --output", dir, free, staged)
	}
	return nil
}

// refuseUnlessStill is backup's stillness sweep: reset's project-label,
// any-state, every-engine check with the removal taken out. A running
// container prints the remedies and refuses; one in any other state refuses
// with the engine's own `rm` line; a leftover helper refuses with its
// `rm -f` line; a query byre could not make refuses too, because an engine it
// cannot inspect cannot be declared idle -- an unreachable one through the
// verb, so the refusal names the two ways past it. Nothing is stopped or
// removed --
// the container is still there after the refusal.
func refuseUnlessStill(w io.Writer, verb totalsVerb, r sessionRunner, id string) error {
	live, err := liveSession(r, id)
	if err != nil {
		return verb.queryErr(r.Engine(), "checking for a running session", err)
	}
	if len(live) > 0 {
		reportRunning(w, r.Engine(), live, true)
		return fmt.Errorf("a session is running for this project (%s, %s); byre backup needs the project completely still", shortID(live[0]), r.Engine())
	}
	all, err := r.ContainersByLabel(labelKey + "=" + id)
	if err != nil {
		return verb.queryErr(r.Engine(), "checking for session containers", err)
	}
	if err := refuseLeftoverHelpers(r, id); err != nil {
		return err
	}
	if len(all) > 0 {
		return fmt.Errorf("a container for this project is still present on %s (%s) and byre backup needs the project completely still — remove it, then re-run: %s rm %s",
			r.Engine(), shortID(all[0]), r.Engine(), shortID(all[0]))
	}
	return nil
}

// ------------------------------------------------------------ the surfaces

// backupOutput resolves where the file goes: --output as given, else
// <folder>-<date>.byre-backup.tar.gz in the current directory. An existing
// entry of ANY kind refuses -- a second backup the same day names --output
// rather than overwriting -- and so does a missing parent directory.
func backupOutput(paths project.Paths, flag string, now time.Time) (string, error) {
	name := flag
	if name == "" {
		name = defaultBackupName(paths, now)
	}
	expanded, err := config.ExpandTilde(name)
	if err != nil {
		return "", err
	}
	// Abs resolves a relative path against the process's working directory,
	// which is "the current directory" the default name lands in.
	out, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	// Judged without following, like every other output path: a symlink at
	// the name is an existing entry, not a target to write through.
	switch ok, perr := hostopen.ExistsNoFollow(out); {
	case perr != nil:
		return "", fmt.Errorf("checking the output path %s: %w", out, perr)
	case ok:
		return "", fmt.Errorf("%s already exists and byre backup never overwrites — move it, or name another path with --output", out)
	}
	dir := filepath.Dir(out)
	switch ok, perr := hostopen.ExistsNoFollow(dir); {
	case perr != nil:
		return "", fmt.Errorf("checking the output directory %s: %w", dir, perr)
	case !ok:
		return "", fmt.Errorf("the output directory %s does not exist — create it, or name a path under an existing one with --output", dir)
	}
	return out, nil
}

// defaultBackupName is <folder>-<YYYY-MM-DD>.byre-backup.tar.gz, where folder
// is the base name of the project's MAIN directory: projects have no names in
// byre, and a backup taken from a linked worktree is a backup of the project
// (ADR 0009), so it is named for the project. The date is local.
func defaultBackupName(paths project.Paths, now time.Time) string {
	return fmt.Sprintf("%s-%s.byre-backup.tar.gz", filepath.Base(paths.Canonical), now.Format("2006-01-02"))
}

// renderPreview is step 3's screen: what the file will hold, what it will
// leave behind and why, what the destination will have to satisfy, and the
// stillness the next step requires.
func (b *backupRun) renderPreview(out string, p backupPlan, pulled bool) {
	w := b.s.Err
	dataf(w, "byre backup will write %s\n", out)
	b.renderBody(w, "runs in", p)
	if pulled {
		dataf(w, "byre: pulled %s to run the capture.\n", b.image)
	}
	fmt.Fprintln(w, "byre: the project must be completely still: no container for it in ANY state, on ANY installed engine, in ANY worktree.")
}

// renderSummary is step 6's: what was written, and every list the preview
// showed -- off a terminal this is the only place they appear, so nothing
// left out of the file is silent (P4).
func (b *backupRun) renderSummary(out string, p backupPlan, caps []captured, pulled bool) {
	w := b.s.Err
	size := int64(-1)
	if fi, err := hostopen.StatNoFollow(out); err == nil {
		size = fi.Size()
	}
	if size >= 0 {
		dataf(w, "byre: wrote %s (%d bytes)\n", out, size)
	} else {
		dataf(w, "byre: wrote %s\n", out)
	}
	b.renderBody(w, "ran in", p)
	if pulled {
		dataf(w, "byre: pulled %s to run the capture; it stays, as any pull does.\n", b.image)
	}
	var rep reportLists
	for i, c := range caps {
		rep.add(p.carried[i], c.report)
	}
	if len(rep.abs) > 0 || len(rep.trav) > 0 {
		fmt.Fprintln(w, "  symlinks carried verbatim (their targets are the box's own content; byre resolved none of them):")
		for _, l := range rep.abs {
			dataf(w, "    - %s   (absolute)\n", l)
		}
		for _, l := range rep.trav {
			dataf(w, "    - %s   (leaves the volume)\n", l)
		}
	}
	if len(rep.dropped) > 0 {
		fmt.Fprintln(w, "  in the file, but dropped by `byre restore` (it writes no FIFOs or devices):")
		for _, d := range rep.dropped {
			dataf(w, "    - %s\n", d)
		}
	}
	var lines []string
	for i, c := range caps {
		for _, line := range strings.Split(strings.TrimRight(c.stderr, "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s: %s", p.carried[i], line))
		}
	}
	if len(lines) > 0 {
		// tar's own lines, as tar said them. byre parses nothing out of
		// them -- the `socket ignored` lines ARE the socket list. One line
		// here can be byre's instead: a helper's stderr is captured under a
		// cap, and the runner marks a list it had to cut off in its own
		// bracketed voice, which travels through this funnel like any other
		// line rather than being matched for here (P4).
		fmt.Fprintln(w, "  tar said:")
		for _, l := range lines {
			dataf(w, "    - %s\n", l)
		}
	}
}

// configLine says what the carried config is and what it does about
// credentials -- the four states a config can legally be in, named rather
// than implied.
func (b *backupRun) configLine(p backupPlan) string {
	if b.opts.NoCredentials {
		return fmt.Sprintf("%s (%d bytes, credentials dropped: no rows, no [credentials] block)",
			config.ProjectConfigName, len(p.cfg))
	}
	// A config these bytes came out of parses; a count of 0 from a file that
	// does not is the honest answer for a line the publish will refuse anyway.
	rows, _ := backup.CredentialRows(p.cfg)
	switch rows {
	case 0:
		return fmt.Sprintf("%s (%d bytes, no credential rows)", config.ProjectConfigName, len(p.cfg))
	case 1:
		return fmt.Sprintf("%s (%d bytes, 1 credential row, carried encrypted under this file's own passphrase)", config.ProjectConfigName, len(p.cfg))
	default:
		return fmt.Sprintf("%s (%d bytes, %d credential rows, carried encrypted under this file's own passphrase)", config.ProjectConfigName, len(p.cfg), rows)
	}
}

// renderBody is the block the preview and the summary both print: what the
// file holds, what it leaves behind, and what the destination must satisfy.
// ran is the tense the capture line takes, which is the only difference
// between the two surfaces.
func (b *backupRun) renderBody(w io.Writer, ran string, p backupPlan) {
	dataf(w, "  engine:  %s\n", b.source.Engine())
	dataf(w, "  capture: %s %s\n", escaped(ran), b.image)
	dataf(w, "  config:  %s\n", b.configLine(p))
	b.renderVolumeLists(w, p)
	fmt.Fprintln(w, "  references (the destination must have these before `byre develop` runs there):")
	renderReferences(w, "    ", p.refs)
	// renderBody is both surfaces, so an ignored engine is named in the preview
	// AND in the summary -- off a terminal the summary is the only surface there
	// is, and an engine byre did not look at must not be silent (P4).
	b.ignore.note(w, func(eng string) string {
		return "volumes of this project there, if any, are not in this backup, and a session there could not be ruled out"
	})
}

// renderVolumeLists prints the carried volumes and everything left behind
// with its reason -- the same lists on the terminal and off it.
func (b *backupRun) renderVolumeLists(w io.Writer, p backupPlan) {
	if len(p.carried) == 0 {
		fmt.Fprintln(w, "  volumes: none — this backup carries the config alone")
	} else {
		fmt.Fprintln(w, "  volumes carried:")
		for _, name := range p.carried {
			mark := ""
			if !p.declared[name] {
				// A volume no declaration covers is still the project's
				// state: a one-off `develop --agent` run's, most likely.
				mark = "   (this project's set does not declare it)"
			}
			dataf(w, "    - %s%s\n", name, mark)
		}
	}
	if len(p.notCarried) > 0 {
		fmt.Fprintln(w, "  left behind:")
		for _, row := range p.notCarried {
			dataf(w, "    - %s: %s\n", row.name, row.reason)
		}
	}
}

// reportLists are the three lists a payload's validation feeds, in the one
// spelling backup's summary and restore's review and summary all print:
// "<volume>: <entry> -> <target>" per symlink, "<volume>: <entry> (<kind>)" per
// dropped entry. The headings differ per surface; the lines do not.
type reportLists struct {
	abs, trav, dropped []string
}

func (l *reportLists) add(volume string, rep backup.Report) {
	for _, k := range rep.Absolute {
		l.abs = append(l.abs, fmt.Sprintf("%s: %s -> %s", volume, k.Name, k.Target))
	}
	for _, k := range rep.Traversing {
		l.trav = append(l.trav, fmt.Sprintf("%s: %s -> %s", volume, k.Name, k.Target))
	}
	for _, d := range rep.Dropped {
		l.dropped = append(l.dropped, fmt.Sprintf("%s: %s (%s)", volume, d.Name, d.Kind))
	}
}

// renderReferences prints the references table as what the SOURCE saw. Every
// key appears, empty or not, so a reader never has to guess whether a missing
// line means "none" or "not recorded".
func renderReferences(w io.Writer, indent string, refs backup.References) {
	list := func(label string, v []string) {
		dataf(w, "%s%-14s %s\n", escaped(indent), escaped(label), renderList(v))
	}
	one := func(label, v string) {
		dataf(w, "%s%-14s %s\n", escaped(indent), escaped(label), orDefault(v, "(none)"))
	}
	list("layers:", refs.Layers)
	one("template:", refs.Template)
	one("agent:", refs.Agent)
	list("skills:", refs.Skills)
	list("mounts:", refs.Mounts)
	list("context:", refs.Context)
	list("claude skills:", refs.ClaudeSkills)
	list("seeds:", refs.Seeds)
	list("files:", refs.Files)
	one("engine:", refs.Engine)
	one("base:", refs.Base)
	one("worktree base:", refs.WorktreeBase)
}

func renderList(v []string) string {
	if len(v) == 0 {
		return "(none)"
	}
	return strings.Join(v, ", ")
}
