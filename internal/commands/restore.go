package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pjlsergeant/byre/internal/backup"
	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

// backupSubject is the noun restore's review calls the document it reviews.
// The screen is `preset apply`'s, so every line that says "this preset ..."
// has to say "this backup ..." here.
const backupSubject = "backup"

// restoreAuthorshipLine is the last line of the review's "From the backup"
// block. Every digest in the file proves that the bytes are the bytes the
// writer hashed; none of them proves WHO wrote them (ADR 0051), and a review
// that lists an agent's own files without saying so would be read as a
// provenance claim it cannot make.
const restoreAuthorshipLine = "this file's authorship is not proven; digests detect corruption, not authorship"

// forgetFallback is the heavy remedy named by every cleanup line that could
// not finish its own removal. Spelled out rather than hinted at: forget is a
// bigger hammer than the hole it patches, and a user who runs it on this
// advice must know that before they type it.
const forgetFallback = "`byre forget`, run in the project directory, is the fallback: it removes EVERY volume and image of this project, the kept ones included, prompts first, and refuses while any installed engine cannot be queried."

// restoreCancelledLine is the one sentence a restore cancelled before the
// commit point ends on, after the account of what it cleared.
const restoreCancelledLine = "byre: restore cancelled; nothing written"

// RestoreOptions are `byre restore`'s flags.
type RestoreOptions struct {
	// AllowNonempty is --allow-nonempty: the user saying THIS directory is the
	// project directory even though it is neither empty nor the clean root of a
	// checkout (PRINCIPLES.md P1). The run then states what the refusal would
	// have said, in the review and again in the summary.
	AllowNonempty bool
}

// Restore implements `byre restore FILE [DIR]`: one backup file becomes a
// project on this machine -- the config written, every carried volume that is
// not already here poured from the backup's own bytes, nothing built.
//
// It is terminal-only, like the `preset apply` review it extends, and it does
// the whole job itself: after it returns, `byre develop` is the next command,
// not a second restore step. The one thing it never does is decrypt: a
// credential row travels as the ciphertext the source's passphrase opens.
func Restore(s Streams, file, dir string, opts RestoreOptions) error {
	if !s.TTY {
		return errors.New("restore is interactive (the review is the point) -- run it on a TTY")
	}
	// DIR exists (or is created) before anything else: project identity is
	// computed from the real directory, and Canonicalize falls back to the
	// cleaned pathname for a path that is not there -- so an alias path would
	// yield one id now and another after creation.
	target, created, err := restoreTarget(dir)
	if err != nil {
		return err
	}
	rr := &restoreRun{s: s, target: target, created: created, allowNonempty: opts.AllowNonempty}
	rr.detect = rr.detectEngine
	rerr := rr.run(file)
	rr.cleanup(rerr)
	return rerr
}

// restoreTarget resolves DIR and makes sure a real directory is standing
// there: the default is the current directory, an existing entry that is not a
// directory refuses (a symlink to one included -- judged without following,
// like every other path byre is handed), and an absent one is created now.
// created says whether byre made it, which is what every pre-bootstrap exit
// has to undo.
func restoreTarget(dir string) (target string, created bool, err error) {
	if dir == "" {
		dir = "."
	}
	expanded, err := config.ExpandTilde(dir)
	if err != nil {
		return "", false, err
	}
	target, err = filepath.Abs(expanded)
	if err != nil {
		return "", false, err
	}
	switch ok, perr := hostopen.ExistsNoFollow(target); {
	case perr != nil:
		return "", false, fmt.Errorf("checking %s: %w", target, perr)
	case ok:
		fi, serr := hostopen.StatNoFollow(target)
		if serr != nil {
			return "", false, fmt.Errorf("checking %s: %w", target, serr)
		}
		if !fi.IsDir() {
			return "", false, fmt.Errorf("%s is not a directory (byre judges it without following, so a symlink to a directory is an existing entry too) — restore into a fresh directory", target)
		}
		return target, false, nil
	}
	parent, base := filepath.Dir(target), filepath.Base(target)
	switch ok, perr := hostopen.ExistsNoFollow(parent); {
	case perr != nil:
		return "", false, fmt.Errorf("checking %s: %w", parent, perr)
	case !ok:
		return "", false, fmt.Errorf("%s does not exist — create it, or name a directory under an existing one", parent)
	}
	// The split hostopen's own rule names: parent is the user's (a symlinked
	// ~/src is their arrangement), the tail is the name byre is creating and a
	// symlink standing there is refused.
	if err := hostopen.MkdirAllIn(parent, base, 0o755); err != nil {
		return "", false, fmt.Errorf("creating %s: %w", target, err)
	}
	return target, true, nil
}

// restoreTargetRefusal is the ONE spelling of the wrong-target refusal: the
// rule, the directory, the reason it does not fit, and both ways past it --
// name the project directory, or take the risk with the switch (PRINCIPLES.md P1).
func restoreTargetRefusal(dir, reason string) error {
	return fmt.Errorf("byre restore expects an empty directory or the clean root of a git checkout. %s is neither: %s — name the project directory as DIR (byre restore FILE DIR), or run with --allow-nonempty to restore here anyway",
		dir, reason)
}

// restoreNonEmptyLine is the ONE spelling of what --allow-nonempty did: the
// review states it before the y/n and the summary repeats it, so the consent
// and the receipt say the same thing in the same words.
func restoreNonEmptyLine(dir, reason string) string {
	return fmt.Sprintf("restoring into %s, which is not empty (--allow-nonempty): %s", dir, reason)
}

// restoreTargetState judges WHERE restore is being run and returns "" for a
// target it accepts, or the one reason it does not.
//
// A restore with no DIR takes the current directory, which is how a home
// directory becomes the project. An empty directory is unmistakably meant for
// this, and so is the clean root of a checkout just cloned -- the
// move-to-another-machine flow. Anything else is a guess byre will not make on
// its own.
//
// Git is the only thing that can prove a tree clean, so a host with no git, a
// probe that fails, and a probe that times out all land in the same place as a
// dirty tree: byre could not check, so it refuses and says that is why.
func restoreTargetState(target string, paths project.Paths) (reason string, err error) {
	entries, derr := hostopen.ReadDirNoFollow(target)
	if derr != nil {
		return "", fmt.Errorf("reading %s: %w", target, derr)
	}
	if len(entries) == 0 {
		// Dotfiles counted: a home directory holding nothing but dotfiles is
		// exactly the target this rule exists to refuse. A directory restore
		// itself just created is empty by construction and arrives here.
		return "", nil
	}
	gitExe, _ := hostGit(boxWritableRoots(paths))
	top, perr := gitProbe(gitExe, "-C", target, "rev-parse", "--show-toplevel")
	if perr != nil {
		var exit *exec.ExitError
		// git ran to completion and declined to name a working tree: there is no
		// checkout here, which is an ANSWER. A probe killed by the deadline
		// exits on a signal (ExitCode -1) and is not one.
		if errors.As(perr, &exit) && exit.ExitCode() > 0 {
			return fmt.Sprintf("it holds %d entries and is not a git checkout", len(entries)), nil
		}
		return restoreGitUncheckable(perr), nil
	}
	root := strings.TrimSpace(string(top))
	if root == "" {
		return restoreGitUncheckable(errors.New("git named no working tree root")), nil
	}
	// By identity, never by string: git prints its own resolution of the root,
	// so a target reached through a symlinked path (or spelled in another case
	// on APFS) is the same directory in different words.
	fi, serr := hostopen.StatNoFollow(target)
	if serr != nil {
		return restoreGitUncheckable(serr), nil
	}
	rfi, serr := hostopen.PlainStat(root, hostopen.IdentityChecked)
	if serr != nil {
		return restoreGitUncheckable(serr), nil
	}
	if !os.SameFile(fi, rfi) {
		return fmt.Sprintf("it is inside a git checkout whose root is %s", root), nil
	}
	out, serr := gitProbe(gitExe, "-C", target, "status", "--porcelain")
	if serr != nil {
		return restoreGitUncheckable(serr), nil
	}
	if n := porcelainFiles(out); n > 0 {
		return fmt.Sprintf("it is a git checkout with uncommitted changes (%d files)", n), nil
	}
	return "", nil
}

// restoreGitUncheckable is the reason for every way the git check can fail to
// produce an answer -- no host git, a probe that broke, a probe the deadline
// killed. byre cannot prove the tree clean, so the target does not fit.
func restoreGitUncheckable(err error) string {
	return fmt.Sprintf("byre could not check it with git (%v)", err)
}

// porcelainFiles counts the paths `git status --porcelain` named: one line per
// path, untracked files included (a tree with stray files in it is not clean).
func porcelainFiles(out []byte) int {
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// restoreRun is one invocation: what the host and the file decided before the
// review (identity, engine, the verified payloads) and the two things cleanup
// has to know -- whether byre created the directory, and whether the store has
// been enrolled yet.
type restoreRun struct {
	s      Streams
	target string
	paths  project.Paths
	store  string // <store>/byre.config, the one file this verb writes
	runID  string

	created      bool
	bootstrapped bool

	// allowNonempty is --allow-nonempty, and nonEmpty is the reason the target
	// did not fit, kept for the review and the summary to state. Computed ONCE
	// in step 1: the review is re-rendered byte-for-byte under the lock, and a
	// file the user touched meanwhile must not turn the consent record into a
	// "this project changed" refusal.
	allowNonempty bool
	nonEmpty      string

	st       *backup.Staging
	file     *backup.File
	proposal config.Config
	rv       resolved

	// detect resolves the DESTINATION engine from the config's own key -- the
	// one host probe inside the run, and the seam unit tests replace.
	detect func(engineKey string) (engineRunner, error)
	r      engineRunner
	ident  runner.Identity
	base   string
	hp     helperPlan

	// sig is this run's ONE SIGINT handler, installed the moment staging exists
	// (there is something to clear from then on) and switched to cancelling the
	// context when the critical section begins. nil when a test pinned its own
	// cancellation.
	sig *runInterrupts
	// stopSignals undoes the SIGINT handler the critical section switched sig to.
	// The cancellation path calls it as its first act: a helper that cannot be
	// found by its own label (a container the engine had not created yet) leaves
	// the wait for the pour unbounded, and a SECOND Ctrl-C must then kill byre
	// the ordinary way rather than being swallowed by a handler that already
	// fired.
	stopSignals func()
	// cleanupWait bounds the wait for a cancelled pour's helper. 0 is the
	// engine cleanup timeout; a test pins a shorter one.
	cleanupWait time.Duration
	// cancelled is the cancellation the pours watch. locked switches the handler
	// to it the moment before the config is written, so a Ctrl-C mid pour runs
	// the failure path instead of killing byre between a created volume and the
	// config that explains it; a caller that set one already (a test) keeps it,
	// and then no real handler is installed at all.
	cancelled context.Context

	plan   restorePlan
	review []byte
	// poured are the volumes whose extraction AND chown both finished: what
	// stays when a later pour fails.
	poured []string
	// helpersCleared says this run's helpers have already been force-removed
	// (the cancellation path does it to end a synchronous helper), so the
	// failure summary does not report the same removal twice.
	helpersCleared bool
	// createUnconfirmed says the engine never answered this run's `volume
	// create` within the cleanup deadline, so the volume may appear after byre
	// has gone. The rollback cannot remove what is not there yet, so it says so
	// instead (noteVolumeMayStillBeCreated).
	createUnconfirmed bool
}

func (rr *restoreRun) run(file string) error {
	// Step 1: the project this is about, and the three refusals that come
	// before the file is even opened.
	paths, err := project.Resolve(rr.target)
	if err != nil {
		return err
	}
	if paths.IsWorktree {
		// A linked worktree IS its main tree's project (ADR 0009), and
		// restoring the main tree's state from a side checkout whose main tree
		// may be missing or unconfigured has no good answer.
		return fmt.Errorf("%s is a linked worktree of %s; restore in the main worktree", rr.target, paths.Canonical)
	}
	// WHERE this is being run, before the file is opened: nothing is read or
	// staged into a directory that was never meant to be the project.
	reason, err := restoreTargetState(rr.target, paths)
	if err != nil {
		return err
	}
	if reason != "" {
		if !rr.allowNonempty {
			return restoreTargetRefusal(rr.target, reason)
		}
		rr.nonEmpty = reason
	}
	if err := paths.ValidateExisting(); err != nil {
		return err
	}
	rr.paths = paths
	rr.store = filepath.Join(paths.Dir, config.ProjectConfigName)
	switch ok, perr := hostopen.ExistsNoFollow(rr.store); {
	case perr != nil:
		return fmt.Errorf("checking for this project's config: %w", perr)
	case ok:
		return fmt.Errorf("this project already has a config (%s); restore into a fresh checkout", rr.store)
	}
	if err := builtins.EnsureStoreOut(paths.Home, rr.s.Err); err != nil {
		return err
	}

	// Step 2: the file, verified end to end into staging before anything on
	// this machine is asked about it.
	if err := rr.read(file); err != nil {
		return err
	}
	// Step 3: the packages the config names, then the set develop would run.
	if err := rr.prepare(); err != nil {
		return err
	}
	// Step 4: one review, one y/n.
	rr.review = rr.renderReview()
	if _, err := rr.s.Err.Write(rr.review); err != nil {
		return err
	}
	if !confirmed(rr.s.Err, rr.s.In, fmt.Sprintf("Restore this backup? byre.config will be written and %d volume(s) created. [y/N] ", len(rr.plan.create))) {
		fmt.Fprintln(rr.s.Err, "byre: not restored; nothing written.")
		return nil
	}
	// Steps 5-7.
	return rr.commit()
}

// read is step 2: open FILE, verify every payload into staging, and parse the
// config bytes that come out of it. Any failure refuses here, before this
// machine has been asked a single question.
func (rr *restoreRun) read(path string) error {
	expanded, err := config.ExpandTilde(path)
	if err != nil {
		return err
	}
	// The user named this path, so the open follows -- but it is still
	// fd-judged (a FIFO or a directory at that name fails loudly rather than
	// hanging the read).
	f, _, err := hostopen.OpenRegular(expanded, true)
	if err != nil {
		return fmt.Errorf("reading the backup file %s: %w", expanded, err)
	}
	defer f.Close()
	runID, err := hostopen.NewRunID()
	if err != nil {
		return err
	}
	rr.runID = runID
	noteStaleStaging(rr.s.Err, rr.paths.Home, runID)
	st, err := backup.NewStaging(rr.paths.Home, runID)
	if err != nil {
		return err
	}
	rr.st = st
	// Staging exists, so an interrupt now has something to clear: the handler
	// goes on HERE, long before the critical section, because everything between
	// -- the install offers, the review, the lock wait -- would otherwise leave
	// a 0700 directory of plaintext volume contents and the project directory
	// byre created behind.
	rr.sig = armRunInterrupts(rr.cancelled, rr.cancelBeforeTheCommit)
	// The destination's own volume prefix: every logical name in the file is
	// checked against it before it is joined into a physical name.
	bf, err := backup.Read(f, st, volumePrefix(rr.paths.ID))
	if err != nil {
		return fmt.Errorf("%s is not a backup byre can restore: %w", expanded, err)
	}
	rr.file = bf
	proposal, err := config.Parse(bf.Config)
	if err != nil {
		return fmt.Errorf("the config in %s: %w", expanded, err)
	}
	// Held to the per-layer rules too, because these bytes become the file
	// every later `byre develop` loads as the project layer: a same-layer
	// collision that loadLayer refuses must refuse here rather than landing a
	// config no verb can read.
	if err := proposal.ValidateLayer(); err != nil {
		return fmt.Errorf("the config in %s: %w", expanded, err)
	}
	rr.proposal = proposal
	return nil
}

// prepare is step 3: acquire the packages the config names, in the order the
// loaders read them, then resolve the set develop would run and read this
// machine for everything the review has to state.
//
// Restore does not continue past a package that is still missing, unlike
// apply: the volume set it must pour, and the engine, base and mounts it must
// prove, all come from the resolved set, and skills.Resolve returns nothing at
// all while a package is missing.
func (rr *restoreRun) prepare() error {
	home := rr.paths.Home
	// The template first: the cascade cannot load while the template it
	// selects is missing, so its hint can only come from the two [sources]
	// tables that exist that early (default.config's and this file's).
	tmpl, err := missingTemplateRef(home, rr.proposal, backupSubject)
	if err != nil {
		return err
	}
	if tmpl != nil {
		rr.offerInstall(*tmpl)
		again, aerr := missingTemplateRef(home, rr.proposal, backupSubject)
		if aerr != nil {
			return aerr
		}
		if again != nil {
			return stillMissing(*again)
		}
	}
	// The chain and the template layer: a missing layer stops here with the
	// chain walk's own message (the path to create), as every strict load does.
	effective, err := config.ResolveProposed(rr.proposal)
	if err != nil {
		return err
	}
	missing, err := missingEffectiveRefs(home, effective.Config)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		for _, m := range missing {
			rr.offerInstall(m)
		}
		still, serr := missingEffectiveRefs(home, effective.Config)
		if serr != nil {
			return serr
		}
		if len(still) > 0 {
			// Declining an offer is not a clean end: it is the reason the
			// package is still missing, so restore stops as it does for any
			// missing package.
			return stillMissing(still[0])
		}
	}
	// Nothing is missing now, so a skills.Resolve failure here is one of the
	// errors a set can only answer for (an invalid name, two postures, an
	// agent with no command) -- restore stops with it rather than showing a
	// review whose skill grants were omitted.
	rv, err := resolveProposal(rr.paths, rr.proposal)
	if err != nil {
		return err
	}
	rr.rv = rv

	// The destination engine, and only it: restore is not a totals command.
	r, err := rr.detect(rv.cfg.Engine)
	if err != nil {
		return err
	}
	rr.r = r
	// develop's own mode-select, so rootless Podman without keep-id refuses
	// here exactly as it refuses there.
	ident, err := resolveIdentity(rr.s.Err, rr.r)
	if err != nil {
		return err
	}
	rr.ident = ident
	// The pour runs in the effective BASE image, never a built one: there is
	// no built image on a fresh machine, and an unset `base` is the
	// generator's default.
	rr.base = orDefault(rv.cfg.Base, gen.DefaultBase)
	rr.hp = newHelperPlan(rr.paths, rr.base, ident, rr.runID)

	if err := rr.refuseBusy(); err != nil {
		return err
	}
	plan, err := rr.planRestore()
	if err != nil {
		return err
	}
	rr.plan = plan
	return nil
}

// offerInstall is apply's chauffeur for one missing reference: the install
// under its own consent where the file carries a hint, and nothing at all
// where it does not -- there stillMissing names the package and the command.
func (rr *restoreRun) offerInstall(m missingRef) {
	if m.Hint == nil {
		return
	}
	dataf(rr.s.Err, "\nbyre: this backup references %s %q, not installed here. Its hint:\n", m.Kind, m.Name)
	if err := installForKind(rr.s, m.Kind, m.Hint.URI, m.Hint.Digest); err != nil {
		dataf(rr.s.Err, "byre: %q not installed (%v).\n", m.Name, err)
	}
}

// stillMissing is the one refusal for a package restore cannot do without,
// with the exact install command where anything knows one.
func stillMissing(m missingRef) error {
	cmd := fmt.Sprintf("byre %s install <manifest-url>", m.Kind)
	if m.Hint != nil {
		cmd = m.Hint.InstallHint(string(m.Kind))
	}
	return fmt.Errorf("this backup's config needs %s %q, which is not installed here: %s — install it, then run byre restore again",
		m.Kind, m.Name, cmd)
}

// refuseBusy refuses while anything of this project is on the destination
// engine: a leftover helper (it may still hold a volume this restore is about
// to fill) or a container of any state under the project label (a box can
// outlive a hand-deleted config, and a linked worktree's box shares the id).
// Run before the review and again under the lock. A query failure refuses:
// an engine byre cannot ask cannot be declared idle.
func (rr *restoreRun) refuseBusy() error {
	if err := refuseLeftoverHelpers(rr.r, rr.paths.ID); err != nil {
		return err
	}
	ids, err := rr.r.ContainersByLabel(projectLabel(rr.paths))
	if err != nil {
		return fmt.Errorf("checking for session containers (%s): %w", rr.r.Engine(), err)
	}
	if len(ids) > 0 {
		reportRunning(rr.s.Err, rr.r.Engine(), ids, false)
		return fmt.Errorf("a container for this project is still present on %s (%s) and byre restore writes the state it holds — remove it, then re-run: %s rm %s",
			rr.r.Engine(), shortID(ids[0]), rr.r.Engine(), shortID(ids[0]))
	}
	return nil
}

// ------------------------------------------------------------------ the plan

// restorePlan is what this restore will do to this machine, from one reading
// of the verified file against one resolution of the destination's set. Step 3
// builds it for the review and the locked step builds it again: the review
// TEXT is the comparison, so a volume that appeared, a layer edit that moved a
// grant, or a declaration that changed scope all come back as the same refusal.
type restorePlan struct {
	create []string // carried, in play, not here yet: poured
	keep   []string // carried, in play, already here: kept, payload dropped
	// notRestored are carried names THIS machine's set declares machine-scoped:
	// develop here would mount the machine volume, so a poured project volume
	// would be an orphan nobody reads.
	notRestored []string
	// absent are volumes the destination's set declares that the backup does
	// not carry: the first develop handles each exactly as today.
	absent []string
	// machine are the machine-scoped names the destination's set declares.
	machine []string

	cache      map[string]bool // in-play names this set declares role = "cache"
	undeclared map[string]bool // in-play names this set declares not at all
	empty      map[string]bool // in-play names whose payload has no entries
	// seeds are the host seed paths that are still live here: a volume the
	// backup does not carry, that develop will create, whose declaration names
	// a host source.
	seeds []seedRequirement
}

// seedRequirement is one host path a later develop reads for a volume this
// restore leaves to it.
type seedRequirement struct {
	volume string
	host   string
}

func (rr *restoreRun) planRestore() (restorePlan, error) {
	p := restorePlan{
		cache:      map[string]bool{},
		undeclared: map[string]bool{},
		empty:      map[string]bool{},
	}
	declared := map[string]config.Volume{}
	machine := map[string]bool{}
	for _, v := range rr.rv.volumes {
		if v.MachineScoped() {
			machine[v.Name] = true
			continue
		}
		declared[v.Name] = v
	}
	p.machine = slices.Sorted(maps.Keys(machine))

	carried := map[string]bool{}
	var inPlay []string
	for _, v := range rr.file.Volumes {
		carried[v.Name] = true
		if machine[v.Name] {
			p.notRestored = append(p.notRestored, v.Name)
			continue
		}
		inPlay = append(inPlay, v.Name)
		// Entries counts what the validator accepted OR dropped, and the
		// rebuilt stream carries only the accepted ones: a volume whose every
		// entry was a FIFO or a device pours an empty volume, and the review
		// has to say so. The dropped entries are still listed, below.
		if v.Report.Entries-int64(len(v.Report.Dropped)) == 0 {
			p.empty[v.Name] = true
		}
		d, ok := declared[v.Name]
		switch {
		case !ok:
			p.undeclared[v.Name] = true
		case d.Role == "cache":
			// Poured anyway: the file carries it, and only BACKUP's own
			// classification drops a cache volume.
			p.cache[v.Name] = true
		}
	}
	// Ownership, both directions, before any name is created or adopted.
	if err := refuseVolumeNameOwnership(rr.r, rr.paths.Home, rr.paths.ID, inPlay); err != nil {
		return p, err
	}
	for _, name := range inPlay {
		exists, err := rr.volumeExists(name)
		if err != nil {
			return p, err
		}
		if exists {
			p.keep = append(p.keep, name)
		} else {
			p.create = append(p.create, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(declared)) {
		if carried[name] {
			continue
		}
		exists, err := rr.volumeExists(name)
		if err != nil {
			return p, err
		}
		if exists {
			continue // already here and nothing in the file touches it
		}
		p.absent = append(p.absent, name)
		if seed := declared[name].Seed; seed != nil && seed.Host != "" {
			p.seeds = append(p.seeds, seedRequirement{volume: name, host: hostPathAsSeen(seed.Host)})
		}
	}
	return p, nil
}

func (rr *restoreRun) volumeExists(logical string) (bool, error) {
	phys := volumeName(rr.paths.ID, logical)
	exists, err := rr.r.VolumeExists(phys)
	if err != nil {
		return false, fmt.Errorf("checking volume %s (%s): %w", phys, rr.r.Engine(), err)
	}
	return exists, nil
}

// ---------------------------------------------------------------- the review

// renderReview renders the whole screen into a buffer: apply's review with
// restore's three sections in it. A buffer rather than the terminal because
// the text IS the consent record -- the locked step renders it again and
// refuses unless the bytes are identical, so a grant-only change in a layer
// refuses exactly like a volume change.
func (rr *restoreRun) renderReview() []byte {
	var buf bytes.Buffer
	s := rr.s
	s.Err, s.Out = &buf, &buf
	// No store to diff against (restore refuses a project that has a config)
	// and nothing may still be missing (step 3 stopped if anything was).
	renderPresetReview(s, rr.paths, rr.proposal, rr.file.Config, nil, "Restore", backupSubject, nil, false, rr.renderSections)
	return buf.Bytes()
}

// renderSections is the three blocks restore adds before the grant summary:
// what this machine must satisfy, what the source saw, and what the file
// holds.
func (rr *restoreRun) renderSections(w io.Writer) {
	rr.renderRequirements(w)
	fmt.Fprintln(w, "  What the source saw (the backup's own note; byre asserts nothing from it):")
	renderReferences(w, "    ", rr.file.Index.References)
	rr.renderFromBackup(w)
}

// renderRequirements is "Names this machine must satisfy": every host name the
// destination's resolved set carries, with WHAT reads it and HOW it fails.
// Restore refuses on none of them -- the verbs that read each one fail or
// degrade exactly as they do today, and saying so is the whole point of the
// list.
//
// It does MARK the ones that are not here now, from a probe that degrades: a
// mark, not a gate -- a user answering y/n should see which of these names the
// first develop will not find.
func (rr *restoreRun) renderRequirements(w io.Writer) {
	if rr.nonEmpty != "" {
		dataf(w, "  %s\n", restoreNonEmptyLine(rr.target, rr.nonEmpty))
	}
	fmt.Fprintln(w, "  Names this machine must satisfy (restore refuses on none of them):")
	n := 0
	row := func(format string, a ...any) {
		n++
		dataf(w, "    - "+format+"\n", a...)
	}
	for _, m := range rr.rv.mounts {
		// A disabled mount is inert (ADR 0015): it is not a name this machine
		// has to have.
		if m.Disabled {
			continue
		}
		host := hostPathAsSeen(m.Host)
		row("mount %s -> %s: `byre develop` fails naming it at launch if it is not there%s", host, m.Target, missingNowMark(host))
	}
	for _, cd := range rr.rv.cfg.Contexts {
		if cd.File != "" {
			file := hostPathAsSeen(cd.File)
			row("context file %s: the build fails naming context and path if it is not there%s", file, missingNowMark(file))
		}
	}
	for _, src := range slices.Sorted(maps.Keys(rr.rv.cfg.Files)) {
		seen := hostPathAsSeen(src)
		row("files source %s: the build fails naming it if it is not there%s", seen, missingNowMark(seen))
	}
	for _, cs := range rr.rv.claudeSkills {
		// A skill's own contribution rides the package it came from; only a
		// config-named `path` is a host name this machine has to have.
		if cs.SrcDir == "" && cs.CS.Path != "" {
			p := hostPathAsSeen(cs.CS.Path)
			row("Claude Skill %s: the build validates it as a skill directory and fails naming it%s", p, missingNowMark(p))
		}
	}
	for _, sd := range rr.plan.seeds {
		row("seed source %s for volume %s: develop seeds from it if it is there, else %s starts empty%s", sd.host, sd.volume, sd.volume, missingNowMark(sd.host))
	}
	if e := rr.rv.cfg.Engine; e != "" {
		row("engine %s: the config names it, and this restore uses it", e)
	} else {
		row("engine: the config names none; this restore uses %s, the one byre found", rr.r.Engine())
	}
	if wb := rr.rv.cfg.WorktreeBase; wb != "" {
		base := hostPathAsSeen(wb)
		row("worktree_base %s: `byre worktree` reads it, nothing else%s", base, missingNowMark(base))
	}
	if n == 0 {
		fmt.Fprintln(w, "    (none)")
	}
}

// missingNowMark is the review's mark for a host name that is not on this
// machine at the moment the review is rendered. Already escaped, because the
// row composes it into its own format string.
func missingNowMark(path string) escaped {
	if hostPathMissing(path) {
		return escaped("   (missing on this machine now)")
	}
	return ""
}

// renderFromBackup is "From the backup": every volume in play with what will
// happen to it, the credential state of the carried config, the symlink
// targets and dropped entries the validation pass found, and the one sentence
// about what the digests do not prove.
func (rr *restoreRun) renderFromBackup(w io.Writer) {
	p := rr.plan
	fmt.Fprintln(w, "  From the backup:")
	note := func(name string) string {
		switch {
		case p.undeclared[name]:
			return "   (this project's set does not declare it)"
		case p.cache[name]:
			return "   (this project's set declares it a cache volume; poured all the same)"
		}
		return ""
	}
	for _, name := range p.create {
		if p.empty[name] {
			dataf(w, "    - %s: will be restored, empty — the first develop populates it from the box image at its mount point, exactly as a fresh volume%s\n", name, escaped(note(name)))
			continue
		}
		dataf(w, "    - %s: will be restored%s\n", name, escaped(note(name)))
	}
	for _, name := range p.keep {
		dataf(w, "    - %s: exists here; the backed-up copy is dropped, and it is not seeded (it exists)%s\n", name, escaped(note(name)))
	}
	for _, name := range p.notRestored {
		dataf(w, "    - %s: not restored: this machine declares %s machine-scoped\n", name, name)
	}
	for _, name := range p.absent {
		dataf(w, "    - %s: not in the backup; the first develop handles it as today (seeded, or started empty)\n", name)
	}
	for _, name := range p.machine {
		dataf(w, "    - %s: binds this machine's own machine-scoped volume\n", name)
	}
	if len(p.create)+len(p.keep)+len(p.notRestored)+len(p.absent)+len(p.machine) == 0 {
		fmt.Fprintln(w, "    - volumes: none — this backup carries the config alone")
	}
	dataf(w, "    credentials: %s\n", escaped(credentialStateLine(rr.file.CredentialState)))
	rep := rr.reportLists()
	if len(rep.abs) > 0 || len(rep.trav) > 0 {
		fmt.Fprintln(w, "    symlinks restored verbatim (their targets are the box's own content; byre resolves none of them):")
		for _, l := range rep.abs {
			dataf(w, "      - %s   (absolute)\n", l)
		}
		for _, l := range rep.trav {
			dataf(w, "      - %s   (leaves the volume)\n", l)
		}
	}
	if len(rep.dropped) > 0 {
		fmt.Fprintln(w, "    in the file, but not restored (byre writes no FIFOs or devices):")
		for _, d := range rep.dropped {
			dataf(w, "      - %s\n", d)
		}
	}
	dataf(w, "    %s\n", restoreAuthorshipLine)
}

// reportLists are every carried payload's validation lists, by volume.
func (rr *restoreRun) reportLists() reportLists {
	var out reportLists
	for _, v := range rr.file.Volumes {
		out.add(v.Name, v.Report)
	}
	return out
}

// credentialStateLine says what the carried config does about credentials, in
// the state DERIVED from its bytes rather than the one its index claims. All
// four legal states are named: a config can hold rows with an identity, rows
// without one, an identity with no rows, or neither.
func credentialStateLine(state string) string {
	switch state {
	case backup.CredRows:
		return "encrypted rows, carried as they are — encrypted under the source config's own passphrase; develop asks for it"
	case backup.CredRowsNoIdentity:
		return "encrypted rows with no [credentials] identity; the first develop refuses them, as it does today"
	case backup.CredIdentityOnly:
		return "a [credentials] identity and no rows; nothing here needs its passphrase yet"
	case backup.CredNone:
		return "none in the backup"
	}
	return "unknown (" + state + ")"
}

// ---------------------------------------------------------------- the commit

// commit is steps 5 to 7: prove the base before any project state exists,
// enrol the store, and do the whole job under one lock hold -- so a develop
// waiting on that lock sees the config and every poured volume together.
func (rr *restoreRun) commit() error {
	has, err := rr.r.ImageExists(rr.base)
	if err != nil {
		return fmt.Errorf("checking image %s (%s): %w", rr.base, rr.r.Engine(), err)
	}
	if !has {
		if perr := rr.r.ImagePull(rr.base); perr != nil {
			return fmt.Errorf("pulling %s to pour this backup's volumes (%s): %w", rr.base, rr.r.Engine(), perr)
		}
		dataf(rr.s.Err, "byre: pulled %s to pour the volumes; it stays, as any pull does.\n", rr.base)
	}
	// The pour command itself, complete, against a scratch directory, fed a
	// valid empty archive -- before a single byte of project state is written.
	// A base whose tar is not GNU tar, or that has no shell or no chown,
	// refuses here, naming the image.
	if err := proveHelperImage(rr.r, rr.hp, "pour", pourPreflightScript(rr.hp.mnt, rr.ident), bytes.NewReader(backup.EmptyArchive())); err != nil {
		cleanupRun(rr.s.Err, rr.r, rr.runID, nil)
		return err
	}
	if err := rr.paths.Bootstrap(); err != nil {
		return err
	}
	rr.bootstrapped = true
	return withSetupLockProject(rr.s.Err, rr.paths, rr.locked)
}

// locked is the critical section: everything the review stated, re-checked
// against the machine, then the config and the pours.
func (rr *restoreRun) locked() error {
	// A forget that won the lock meanwhile is cancellation, not permission to
	// write into an emptied store. No second bootstrap.
	if err := requireRecorded(rr.paths); err != nil {
		return err
	}
	switch ok, perr := hostopen.ExistsNoFollow(rr.store); {
	case perr != nil:
		return fmt.Errorf("checking for this project's config: %w", perr)
	case ok:
		return fmt.Errorf("this project got a config (%s) while you were reviewing; re-run byre restore", rr.store)
	}
	rv, err := resolveProposal(rr.paths, rr.proposal)
	if err != nil {
		return err
	}
	rr.rv = rv
	if err := rr.refuseBusy(); err != nil {
		return err
	}
	plan, err := rr.planRestore()
	if err != nil {
		return err
	}
	rr.plan = plan
	// The whole screen again. Byte-identical or nothing: consent was to THAT
	// text, and a layer edit that moved only a grant row changed what the
	// user agreed to as surely as one that moved a volume.
	if again := rr.renderReview(); !bytes.Equal(again, rr.review) {
		return errors.New("this project changed while you were reviewing; re-run byre restore")
	}
	// The critical section begins here, with the first host write on the next
	// line: from now on an interrupt cancels the context below instead of
	// clearing and exiting, because what follows has to be UNDONE (see
	// runInterrupts). Everything above is a wait or a re-check that writes
	// nothing.
	ctx, stop := rr.sig.enterCritical(rr.cancelled)
	defer stop()
	rr.cancelled, rr.stopSignals = ctx, stop

	// The one host write: the bytes the review showed, byte for byte. No
	// applied marker -- no preset was applied.
	if err := config.AtomicWrite(rr.store, string(rr.file.Config)); err != nil {
		return rr.failed("", fmt.Errorf("writing %s: %w", rr.store, err))
	}
	for _, name := range rr.plan.create {
		if err := rr.pour(name); err != nil {
			return err
		}
	}
	// Every pour is done and there is nothing left for a cancellation to
	// unmake: the handler comes off here, so a Ctrl-C during the summary kills
	// byre the ordinary way rather than being swallowed by a context nobody
	// reads any more.
	rr.stopSignals()
	rr.renderSummary()
	return nil
}

// pour creates one volume and replays its rebuilt stream into a helper. A
// volume counts as poured whole only once the extraction AND the chown have
// both succeeded -- they are one command, so a chown failure is a failed pour
// and the volume goes.
func (rr *restoreRun) pour(logical string) error {
	phys := volumeName(rr.paths.ID, logical)
	if err := rr.cancelled.Err(); err != nil {
		return rr.failed(logical, fmt.Errorf("cancelled before volume %s was created (%w)", logical, err))
	}
	if err := rr.createVolume(logical, phys); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- rr.runPour(logical, phys) }()
	select {
	case err := <-done:
		if err != nil {
			return rr.failed(logical, err)
		}
	case <-rr.cancelled.Done():
		rr.stopSignals()
		// The helper is mid-extraction and RunHelper does not return until its
		// container is gone; endCancelledHelper is what takes it away, and why
		// that wait is bounded is its own doc.
		rr.helpersCleared = true
		if !endCancelledHelper(rr.s.Err, rr.r, rr.runID, done, rr.cleanupWait) {
			noteHelperMayStillRun(rr.s.Err, rr.r, rr.runID, logical, rr.cleanupWait)
			dataf(rr.s.Err, "byre: the volume byre was filling can only be removed once it is gone — the rollback below tries now and names the volume if the removal failed.\n")
		}
		return rr.failed(logical, fmt.Errorf("cancelled while pouring volume %s", logical))
	}
	rr.poured = append(rr.poured, logical)
	return nil
}

// createVolume creates one volume under the cancellation, the same shape the
// pour has: a daemon stalled in `volume create` would otherwise take the
// interrupt with nothing to act on it and sit there holding the setup lock. The
// wait is bounded because nothing byre can do will MAKE the create return --
// there is no helper container to take away here -- and the rollback then
// removes the volume if the create landed after all, through its bounded calls.
func (rr *restoreRun) createVolume(logical, phys string) error {
	done := make(chan error, 1)
	go func() { done <- rr.r.VolumeCreate(phys) }()
	select {
	case err := <-done:
		if err != nil {
			return rr.failed(logical, fmt.Errorf("creating volume %s (%s): %w", phys, rr.r.Engine(), err))
		}
		return nil
	case <-rr.cancelled.Done():
		rr.stopSignals()
		if !waitBounded(done, rr.cleanupWait) {
			rr.createUnconfirmed = true
			dataf(rr.s.Err, "byre: %s has not finished creating volume %s within %s — the rollback below removes it if it is there.\n",
				rr.r.Engine(), phys, orCleanupTimeout(rr.cleanupWait))
		}
		return rr.failed(logical, fmt.Errorf("cancelled while creating volume %s", logical))
	}
}

// runPour streams one payload into the helper. The stream is the one the
// validator REBUILDS entry by entry, never the file's own bytes: what the
// helper extracts is what the contract approved.
func (rr *restoreRun) runPour(logical, phys string) error {
	var staged backup.StagedVolume
	for _, v := range rr.file.Volumes {
		if v.Name == logical {
			staged = v
		}
	}
	pr, pw := io.Pipe()
	rebuilt := make(chan error, 1)
	go func() {
		_, err := rr.file.RebuildTo(staged, rr.st, pw)
		// The helper's stdin ends here either way: a rebuild that stopped must
		// not leave tar waiting for bytes that will never come.
		pw.CloseWithError(err)
		rebuilt <- err
	}()
	stderr, rerr := rr.r.RunHelper(rr.hp.helper(phys, false, pourScript(rr.hp.mnt, rr.ident)), pr, io.Discard)
	// Unblock the rebuild if the helper went away first; its write then fails
	// instead of hanging this call forever.
	pr.Close()
	reb := <-rebuilt
	if rerr != nil {
		// The helper's own failure is the cause, and the rebuild's error on
		// that path is only the closed pipe.
		return fmt.Errorf("extracting the backup's %s payload%s: %w", logical, engineAside(nil, stderr), rerr)
	}
	if reb != nil {
		return fmt.Errorf("replaying the backup's %s payload: %w", logical, reb)
	}
	return nil
}

// failed is step 6: the critical section's cleanup, in the order that matters
// -- this run's helpers (one of them is holding the volume), the volume being
// poured, the config restore wrote -- then the summary of what is left. The
// lock is released by the caller returning, and staging by run's cleanup.
//
// Volumes poured whole before the failure stay: the next restore meets them as
// "exists here; kept", and their content is this backup's.
func (rr *restoreRun) failed(logical string, cause error) error {
	w := rr.s.Err
	if logical != "" {
		dataf(w, "byre: restoring volume %s failed: %v — rolling back what this restore wrote.\n", logical, cause)
	} else {
		dataf(w, "byre: this restore failed: %v — rolling back what it wrote.\n", cause)
	}
	if !rr.helpersCleared {
		if err := removeRunHelpers(w, rr.r, rr.runID); err != nil {
			dataf(w, "byre: %v\n", err)
		}
		rr.helpersCleared = true
	}
	if logical != "" {
		phys := volumeName(rr.paths.ID, logical)
		// A create that failed left nothing to remove; anything byre cannot ask
		// about, it tries to remove anyway. Both calls are the BOUNDED forms
		// (see runner.VolumeExistsBounded).
		exists, qerr := rr.r.VolumeExistsBounded(phys)
		if qerr != nil || exists {
			if rerr := rr.r.VolumeRemoveBounded(phys); rerr != nil {
				dataf(w, "byre: the volume byre was filling could not be removed (%v) — remove it by hand: %s volume rm %s. Until it is gone a re-run keeps whatever occupies that name. %s\n",
					rerr, rr.r.Engine(), phys, escaped(forgetFallback))
			}
		}
		// Nothing was there to remove, and the create that would put it there
		// has not come back: the same outcome as a removal that failed, reached
		// the other way round, so it gets the same account.
		if rr.createUnconfirmed && !exists {
			noteVolumeMayStillBeCreated(w, rr.r, phys)
		}
	}
	if rerr := removeIn(rr.paths.Dir, config.ProjectConfigName); rerr != nil && !os.IsNotExist(rerr) {
		dataf(w, "byre: the config byre wrote could not be removed (%v) — this project now has a config (%s) and the next byre restore will refuse until it is removed. %s\n",
			rerr, rr.store, escaped(forgetFallback))
	}
	if len(rr.poured) > 0 {
		dataf(w, "byre: already restored, and kept: %s (a re-run reports them as kept)\n", renderList(rr.poured))
	}
	fmt.Fprintln(w, "byre: fix the cause and run byre restore again.")
	// The whole account is above, printed through the funnel. Returning the
	// cause as well would have byre print it a second time, under a banner and
	// unescaped, so this path exits 1 silently -- deliver's cancel and develop's
	// refusal do the same.
	return ExitError{Code: 1}
}

// detectEngine is the host probe behind prepare's engine choice: the
// configured engine when the config names one, else whatever byre finds. Other
// installed engines are never consulted -- restore writes one engine's state
// and speaks in no totals -- and a declined binary for THIS engine refuses
// exactly as develop refuses it.
func (rr *restoreRun) detectEngine(key string) (engineRunner, error) {
	eng, exe, err := runner.Detect(key, hostexec.Looker(boxWritableRoots(rr.paths)))
	if err != nil {
		return nil, err
	}
	return runner.New(eng, exe), nil
}

// renderSummary is step 7.
func (rr *restoreRun) renderSummary() {
	w := rr.s.Err
	p := rr.plan
	dataf(w, "byre: restored %s into %s\n", filepath.Base(rr.target), rr.target)
	if rr.nonEmpty != "" {
		dataf(w, "  %s\n", restoreNonEmptyLine(rr.target, rr.nonEmpty))
	}
	dataf(w, "  config:  %s (%d bytes, as the backup carried them)\n", rr.store, len(rr.file.Config))
	if len(p.create) == 0 {
		fmt.Fprintln(w, "  volumes restored: none")
	} else {
		dataf(w, "  volumes restored: %s\n", renderList(p.create))
	}
	if len(p.keep) > 0 {
		dataf(w, "  volumes kept (already here; the backed-up copy was dropped): %s\n", renderList(p.keep))
	}
	if len(p.notRestored) > 0 {
		dataf(w, "  not restored (this machine declares them machine-scoped): %s\n", renderList(p.notRestored))
	}
	if dropped := rr.reportLists().dropped; len(dropped) > 0 {
		fmt.Fprintln(w, "  in the file, but not restored (byre writes no FIFOs or devices):")
		for _, d := range dropped {
			dataf(w, "    - %s\n", d)
		}
	}
	fmt.Fprintln(w, "byre: next: byre develop")
	// A repo-shipped preset is the one thing that would quietly undo this.
	if ok, err := hostopen.ExistsNoFollow(filepath.Join(rr.paths.WorkDir, PresetName)); ok && err == nil {
		dataf(w, "byre: this checkout ships a %s, so develop will suggest `byre preset apply` — applying it REPLACES the config this restore just wrote.\n", PresetName)
	}
}

// cancelBeforeTheCommit is what an interrupt does while this restore has
// written nothing of the user's: this run's helpers go (a preflight may be
// mid-run), then staging -- a 0700 directory holding the backup's volume
// contents in plaintext -- and the project directory byre created while it is
// still empty. Then the one closing line; the handler ends the process after it.
//
// A test calls this directly: it is the only way to reach the pre-commit
// behaviour without a terminal to interrupt from.
func (rr *restoreRun) cancelBeforeTheCommit() {
	// rr.r is nil until step 3 resolved the engine; before that nothing can have
	// run on one.
	if rr.r != nil {
		if err := removeRunHelpers(rr.s.Err, rr.r, rr.runID); err != nil {
			dataf(rr.s.Err, "byre: %v\n", err)
		}
	}
	rr.cleanup(nil)
	fmt.Fprintln(rr.s.Err, restoreCancelledLine)
}

// cleanup runs on every exit, success or failure: the handler comes off, then
// staging always, and the directory restore created while the store is not
// enrolled yet. rmdir semantics only, so nothing a user put there is ever
// touched. Idempotent -- the cancellation path above runs it and the ordinary
// exit runs it again.
func (rr *restoreRun) cleanup(runErr error) {
	rr.sig.stop()
	removeStaging(rr.s.Err, rr.st)
	rr.st = nil
	if !rr.created {
		return
	}
	if rr.bootstrapped {
		// An exit after the bootstrap leaves the directory and the enrolled
		// store, as preset apply's same window does by design. The next
		// restore of that path proceeds: no config, same id.
		if runErr != nil {
			dataf(rr.s.Err, "byre: %s and this project's store stay (byre had enrolled the project by then); the next byre restore into that path carries on.\n", rr.target)
		}
		return
	}
	if err := removeIn(filepath.Dir(rr.target), filepath.Base(rr.target)); err != nil {
		dataf(rr.s.Err, "byre: the directory byre created for this restore stays (%s): %v\n", rr.target, err)
		return
	}
	rr.created = false
	dataf(rr.s.Err, "byre: removed the empty directory byre created for this restore (%s).\n", rr.target)
}

// ------------------------------------------------------------- the pour tool

// pourScript is the ONE spelling of the pour command: `-f -` because GNU tar
// otherwise reads TAPE, which an image can set; --no-same-owner so an archived
// uid outside this machine's user namespace can never fail an extraction;
// --delay-directory-restore so a directory revisited later in the stream keeps
// its archived mtime; MNT as the -C argument and the chown's target, so the
// volume root is the tar root. The chown is part of the same command: the
// restored tree's ownership is the box identity's, and a volume whose chown
// did not run is not a poured volume.
func pourScript(mnt string, id runner.Identity) string {
	return fmt.Sprintf("tar -x -f - --no-same-owner --delay-directory-restore -C %s && chown -R %d:%d %s", mnt, id.UID, id.GID, mnt)
}

// pourPreflightScript proves the base can run pourScript at all: tar's own
// banner on stdout (byre needs GNU tar -- every tar fact this format relies on
// is GNU tar's), then the COMPLETE pour command against a scratch directory,
// fed a valid empty archive by the caller. A base with no shell, no tar, or no
// chown fails here, before any project state exists.
func pourPreflightScript(mnt string, id runner.Identity) string {
	return "set -e\ntar --version\nmkdir -p " + mnt + "\n" + pourScript(mnt, id) + "\n"
}
