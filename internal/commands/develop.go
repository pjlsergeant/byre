package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pjlsergeant/byre/internal/build"
	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/deliver"
	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

// selfEditTarget is where --self-edit mounts this project's host-side store
// (~/.byre/projects/<id>/) inside the box, so the agent can edit its OWN
// byre.config — the deliberate "let the agent change its own sandbox" grant.
const selfEditTarget = skills.DevHome + "/.byre-self"

// ExitError signals a process-level exit code that is NOT a byre failure —
// either the agent/container's own exit status, or a deliberate refusal (e.g.
// a session is already running). main distinguishes it from an ordinary error
// so it can os.Exit(Code) directly instead of printing a "byre: ..." banner
// that would misreport the agent's own exit as a byre bug.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// ExitRefused is Develop's exit code when it refuses to start because a
// session is already running for this project — distinct from 0 (ran
// cleanly), 1 (byre error), and 2 (usage error), so a script can tell "byre
// declined to run" from "the agent ran and exited zero".
const ExitRefused = 3

// Develop implements `byre develop`: set up (generate + build) under a setup
// lock and run the container in the foreground. If a container is already
// running for this directory, report it (and how to act) instead of starting one.
//
// flagTemplate/flagAgent come from --template/--agent (empty = unspecified);
// flagSharedAuth from --shared-auth (nil = not given: the picker asks when
// interactive; set = the shared-auth answer itself, no question asked).
// selfEdit (--self-edit) bind-mounts this project's host-side store
// (~/.byre/projects/<id>/, not all of ~/.byre) read-write at selfEditTarget so
// the agent can edit its own byre.config — a deliberate grant.
// credMode is --credentials: how this launch gets its passphrases.
func Develop(s Streams, projectDir, flagTemplate, flagAgent string, flagSharedAuth *bool, selfEdit bool, credMode CredentialMode) error {
	return developCommand(s, projectDir, flagTemplate, flagAgent, flagSharedAuth, selfEdit, credMode, true)
}

// develop carries one enrollment distinction: a direct `byre develop` may
// create/re-enroll the project, while the automatic handoff from `byre
// worktree` must not undo a forget that won during worktree creation.
func developCommand(s Streams, projectDir, flagTemplate, flagAgent string, flagSharedAuth *bool, selfEdit bool, credMode CredentialMode, mayEnroll bool) error {
	if err := requireNonRootHost(s.Err); err != nil {
		return err
	}
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if mayEnroll {
		if err := paths.Bootstrap(); err != nil {
			return err
		}
	} else if err := requireRecorded(paths); err != nil {
		return wrapForgottenWorktreeHandoff(projectDir, err)
	}
	// Store-ensure (bundled mirror + LEGACY notices) rides every develop so an
	// upgraded byre surfaces them with no separate update step.
	if err := builtins.EnsureStoreOut(paths.Home, s.Err); err != nil {
		return err
	}
	// Worktree: announce the inherited identity up front, so any onboarding
	// prompts below are understood as configuring the whole project (all its worktrees).
	announceWorktree(s.Err, paths)
	// A repo-shipped preset is like package.json: cloning gives you a file,
	// not a prompt (the adoption offer is retired). Passive visibility
	// only: state 1 (not applied) and state 3 (diverged) get one note; the
	// steady state is silent. `byre preset apply` is the solicited flow.
	if note := presetNote(projectDir, paths); note != "" {
		fmt.Fprintf(s.Err, "byre: %s\n", note)
	}
	// First-run onboarding is a setup mutation too. Hold the setup lock through
	// its questions and write: reset/forget then refuse as "setup in progress",
	// and a forget that won before this lock cancels onboarding instead of
	// letting it recreate byre.config in a recordless store.
	//
	// Whether --agent seeds or overrides is onboarding's answer: on a project
	// that already had a config, the flag becomes the run-scoped agent
	// override (nothing written); on a first run onboarding consumed it, and
	// the config it just wrote already says so.
	agentOverride := ""
	if wasConfigured, err := setupLockedProject(s.Err, paths, func() (bool, error) {
		if err := requireRecorded(paths); err != nil {
			return false, err
		}
		// The worktree handoff (mayEnroll=false) forwards --agent as the
		// run-scoped override ONLY: on a project with no byre.config the
		// flag would fall through to onboarding and durably configure the
		// whole repo from a flag whose help promises "nothing written".
		// Refuse by name, before onboarding can ask anything. Judged under
		// the same lock onboarding runs under, same probe-failure stance.
		if !mayEnroll && flagAgent != "" {
			cfgPath := filepath.Join(paths.Dir, config.ProjectConfigName)
			ok, perr := hostopen.ExistsNoFollow(cfgPath)
			if perr != nil {
				return false, fmt.Errorf("cannot tell whether %s exists (%v) — fix the store's permissions, or run 'byre forget' to clear it", cfgPath, perr)
			}
			if !ok {
				return false, fmt.Errorf("--agent: this project has no byre.config, so the flag cannot be a run-scoped override — run 'byre develop' here first (or 'byre develop --agent %s' to configure the project with it)", flagAgent)
			}
		}
		return onboardIfNeeded(s, projectDir, paths, flagTemplate, flagAgent, flagSharedAuth)
	}); err != nil {
		if !mayEnroll {
			return wrapForgottenWorktreeHandoff(projectDir, err)
		}
		return err
	} else if wasConfigured && flagAgent != "" {
		agentOverride = flagAgent
	}
	// Validate bind sources before any build/seed side effects: a comma would
	// corrupt a docker --mount value (workspace bind, and worktree git binds).
	if err := checkMountPaths(paths); err != nil {
		return err
	}
	// This read decides what has to happen BEFORE the setup lock: which engine
	// to detect, which host tools to pin. What the lock guards -- the build,
	// the seed, the container -- is read again under it (resolved.refresh),
	// because the editor's save takes the same lock and may land while develop
	// waits for it.
	rv, err := resolveWithAgent(paths, projectDir, s.Err, agentOverride)
	if err != nil {
		return err
	}
	// One root set for every host tool this invocation spawns. The ENGINE is
	// the hard case (decision: nothing safe can proceed on a shadowed engine),
	// so a refusal here ends develop by name rather than degrading.
	roots := boxWritableRoots(paths)
	eng, engExe, err := runner.Detect(rv.cfg.Engine, hostexec.Looker(roots))
	if err != nil {
		return err
	}
	// Single-session must hold across an engine SWITCH: if `engine` was flipped
	// while a box runs on the previous engine, the configured runner can't see
	// it. Hand develop the other installed engines so it can check them under
	// the setup lock (ADR 0004).
	rv.otherEngines, rv.declinedEngines = installedEnginesExcept(eng, roots)
	gitExe, disclosure := hostGitForSession(roots)
	rv.gitExe = gitExe
	if disclosure != "" {
		// The disclosure embeds the shadowed path -- a filename the agent
		// authors under a box-writable root, which is the shadow precondition.
		dataf(s.Err, "%s\n", disclosure)
	}
	derr := develop(runner.New(eng, engExe), s, paths, rv, selfEdit, credMode)
	if derr != nil && !mayEnroll {
		// The engine-facing core takes the setup lock again after credential
		// prompts. If forget won during that human wait, replace its generic
		// "re-run the command" with the worktree-specific recovery.
		return wrapForgottenWorktreeHandoff(projectDir, derr)
	}
	return derr
}

func forgottenWorktreeHandoff(projectDir string, err error) error {
	return fmt.Errorf("the project was forgotten while creating the worktree — the worktree remains at %s, but its automatic session was cancelled. To use it, run `byre develop` there: %w", projectDir, err)
}

func wrapForgottenWorktreeHandoff(projectDir string, err error) error {
	var cleared projectClearedError
	if errors.As(err, &cleared) {
		return forgottenWorktreeHandoff(projectDir, err)
	}
	return err
}

// develop is the engine-facing core of Develop — the live-session fast path,
// then build + seed under the setup lock, then the foreground run and its
// exit-status mapping. Split from Develop (which does the host-side resolution
// and onboarding) so it can run end-to-end against a fake engine.
func develop(r engineRunner, s Streams, paths project.Paths, rv resolved, selfEdit bool, credMode CredentialMode) error {
	// Mode-select (ADR 0032): host identity on rootful engines, the generic
	// keep-id identity under rootless Podman — or the old refusal where the
	// engine can't do the mapping. Everything identity-shaped below (image
	// tag, build args, seed chowns, BYRE_UID, --userns) follows this value.
	ident, err := resolveIdentity(s.Err, r)
	if err != nil {
		return err
	}

	// Worktrees inherit the project image (ADR 0009), so file build inputs
	// (`files` sources) resolve from the main worktree, not this one. (A
	// repo-shipped byre.preset is different: preset apply reads it from this
	// worktree, and the drift note reflects this worktree's copy.) Say so
	// every session: a branch that
	// edits a build input would otherwise silently run an image built from
	// other content.
	if paths.IsWorktree {
		fmt.Fprintf(s.Err, "byre: worktree session — the shared project image builds from the main worktree (%s); `files` sources changed only in this worktree don't reach the image.\n", paths.Canonical)
	}

	image := imageTag(paths.ID, ident.UID, ident.GID)

	// Fast path: a session is already running for THIS worktree — report it
	// rather than racing the container name. Queried by the worktree label, not
	// the project label, so another worktree's live session doesn't block this
	// one (running both at once is the point). A query error here is fatal: it's
	// the live-session safety check.
	ids, err := r.RunningContainersByLabel(workdirLabel(paths))
	if err != nil {
		return fmt.Errorf("checking for a running session: %w", err)
	}
	if len(ids) > 0 {
		if selfEdit {
			fmt.Fprintln(s.Err, "byre: --self-edit only applies when starting a container; a session is already running, so it has no effect here.")
		}
		if rv.agentOverride != "" {
			fmt.Fprintln(s.Err, "byre: --agent only applies when starting a container; a session is already running, so it has no effect here.")
		}
		reportRunning(s.Err, r.Engine(), ids, true)
		return ExitError{Code: ExitRefused} // refused, session already live
	}

	// --self-edit hands the agent authorship of its own next sandbox; open the
	// session with the warning. (The store snapshot backing the session-end
	// diff is taken after setup below — setup itself writes the store.)
	if selfEdit {
		fmt.Fprintln(s.Err, "🛑 self-edit is on. A malicious or incompetent agent can change the configuration to grant itself full access to your host on the next run.")
		fmt.Fprintf(s.Err, "   read-write mount: %s\n", paths.Dir)
		// A worktree looks disposable, and that is exactly the wrong intuition
		// here: worktrees INHERIT the repo's identity (ADR 0009), so this store
		// is the repo's, not this worktree's. Config the agent rewrites here
		// governs the main worktree and every sibling worktree's next launch.
		if paths.IsWorktree {
			fmt.Fprintf(s.Err, "   this store is the REPO's, shared with %s and every other worktree of it — not scoped to this worktree.\n", paths.Canonical)
		}
	}
	// A run-scoped agent override changes what the box is ABOUT; say so up
	// front (like the self-edit warning, it frames the session), and say what
	// it does not do — the config is untouched, the next develop reverts.
	// dataf: the agent is a package id byre did not author (P4's funnel).
	if rv.agentOverride != "" {
		dataf(s.Err, "byre: agent for this run: %s (--agent override; nothing written — the next develop uses byre.config again).\n", config.OrNone(rv.cfg.Agent))
	}
	// Credential unlock (launch step 1) is deliberately PRE-lock: a prompt
	// under the setup lock would stall sibling worktrees on a human, and the
	// scrypt unwrap is the expensive step. The groups shown are the pre-lock
	// read's; the decrypt below runs against the authoritative under-lock
	// re-read, and a row that appeared in between stops the launch rather
	// than being delivered unasked-for.
	if rv.credErr != nil {
		return fmt.Errorf("credentials: %w", rv.credErr)
	}
	unlocked, err := unlockCredentials(s, credMode, rv.credFiles)
	if err != nil {
		return err
	}

	// Setup (generate + build + seed) AND container creation are serialized by
	// the lock; the interactive session that follows is not (the lock is
	// per-project, and sibling worktrees running concurrently is the point).
	// Creating the container under the lock closes the race with reset/forget:
	// from here until exit the container — in ANY state, started or not — is
	// this session's ownership marker. The destructive commands take the same
	// lock and must dissolve that marker (clearSessionMarkers) before touching
	// volumes; if one does, the start below fails loudly instead of the
	// session launching against wiped, engine-recreated volumes.
	//
	// Everything after this call reads ONLY prep — the rv this function was
	// handed is the pre-lock read, stale by contract once the authoritative
	// re-read under the lock has happened (prep.rv is that re-read). prep's
	// fields are read-only from here.
	prep, err := setupLockedProject(s.Err, paths, func() (preparedLaunch, error) {
		return prepareLaunchLocked(r, s, paths, rv, image, selfEdit, ident, unlocked, credMode == CredentialSkip)
	})
	if err != nil {
		return err
	}

	// Snapshot the store only now, after setup wrote its own files into it, so
	// the session-end diff (reportSelfEditChanges) shows what the AGENT
	// touched, not byre's own staging.
	var store storeSnapshot
	if selfEdit {
		store = snapshotStore(paths.Dir)
	}
	// Same timing, same reason, for the places the HOST runs code from (ADR
	// 0047): after byre's own setup, so the exit report shows the session's
	// changes rather than byre's staging.
	watch := snapshotExit(paths, prep.rv.gitExe)

	// Every real session opens by showing the walls going up: the terse
	// exposure lines. Printed only once the container exists — a launch that
	// failed setup or lost the name race gets no walls claimed. (The self-edit
	// warning above is consciously pre-create: it guards a decision, not a
	// session.) The config UI renders the same tally (config.Exposure owns the
	// words); `byre status` is the detailed, attributed view.
	exp := exposureOf(prep.rv, selfEdit, prep.hostEnv)
	fmt.Fprintf(s.Err, "byre: exposure: %s\n", exp.GrantsLine())
	fmt.Fprintf(s.Err, "byre: %s\n", exp.NetworkLine())
	// Containment holes (e.g. docker-host): loud standing grant, at least
	// self-edit's 🛑 weight. Skill-owned text; byre frames and attributes.
	for _, c := range prep.rv.skills.Containments() {
		fmt.Fprintf(s.Err, "byre: 🛑 containment hole: %s  (skill: %s)\n", c.Text, c.Skill)
	}
	// Netns-init hooks (e.g. the firewall skill's rules) are applied from
	// OUTSIDE the box, concurrently with the attached session: the box's
	// launcher waits at its launch gate until the hooks land. The wait after
	// the session keeps the goroutine from outliving develop (and its s.Err
	// writes).
	var netnsWait func()
	if len(prep.hooks) > 0 && prep.netnsLabel != "" {
		done := make(chan struct{})
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			runNetnsInits(r, s.Err, prep.netnsLabel, image, prep.hooks, prep.netnsEnv, ident.KeepID, done)
		}()
		netnsWait = func() { close(done); <-finished }
	}
	// Credential delivery (launch step 3) runs concurrently with the
	// attached session, like the netns hooks: the box's launcher waits
	// bounded for the sentinel while this execs the framed stream into the
	// baked receiver. A failure STOPS the box, the same fail-closed shape
	// the netns hooks take — the launcher would exit on the missing
	// sentinel anyway, and a session left attached to a box that is about
	// to die reports the wrong cause. The wait after the session keeps the
	// goroutine from outliving develop (and its s.Err writes).
	var credWait func() error
	if len(prep.creds.values) > 0 {
		done := make(chan struct{})
		finished := make(chan struct{})
		stream := credStream(prep.creds)
		// The honesty epoch is captured HERE, synchronously, before the
		// goroutine is spawned and before StartAttach can run — a goroutine
		// body has no ordering guarantee against the code after `go`, so a
		// timestamp taken inside it could postdate the box start and make
		// the delivered/late measurement underestimate.
		epoch := time.Now()
		var injectErr error
		go func() {
			defer close(finished)
			if injectErr = runCredentialInject(r, s.Err, workdirLabel(paths), prep.containerID, ident, stream, epoch, done); injectErr != nil {
				fmt.Fprintf(s.Err, "byre: %v\n", injectErr)
				stopCredentialsClosed(r, s.Err, prep.containerID)
			}
		}()
		// Reading injectErr here is ordered by the channel close the
		// goroutine defers, which happens after the assignment.
		credWait = func() error { close(done); <-finished; return injectErr }
	}

	runErr := r.StartAttach(containerName(paths))
	var credErr error
	if credWait != nil {
		credErr = credWait()
	}
	if netnsWait != nil {
		netnsWait()
	}
	// Has the observation window actually closed? A StartAttach error is not
	// the same as a session ending: attach can fail with the container still
	// RUNNING, and reporting then would snapshot mid-session, call itself a
	// session-end report while the agent is still working, and only afterwards
	// tell the user a live session exists. Settle liveness first, once, and
	// reuse the answer for the refusal below.
	var live []string
	liveUnknown := false
	if runErr != nil {
		if l, qerr := r.RunningContainersByLabel(workdirLabel(paths)); qerr != nil {
			// Couldn't tell. Not knowing is not the same as knowing it ended:
			// treating a failed query as "no container" would put the premature
			// report straight back.
			liveUnknown = true
		} else {
			live = l
		}
	}
	// The session is over (runErr may just be the agent's own exit status):
	// show what changed before the exit paths below return.
	if len(live) == 0 && !liveUnknown {
		if selfEdit {
			reportSelfEditChanges(s.Err, paths.Dir, store)
		}
		reportExit(s.Err, watch, snapshotExit(paths, prep.rv.gitExe))
	}
	// A failed delivery is a failed LAUNCH, and its cause outranks the exit
	// status of an agent byre just stopped out from under.
	if credErr != nil {
		return credErr
	}
	if runErr != nil {
		if len(live) > 0 {
			reportRunning(s.Err, r.Engine(), live, true)
			return ExitError{Code: ExitRefused} // refused, session already live
		}
		// A start that never ran leaves the created container behind (--rm only
		// fires on exit); remove it best-effort so the name isn't stranded. A
		// forceless rm can't kill a running session, and after a normal agent
		// exit the container is already gone — both failures are ignorable.
		_ = r.ContainerRemove(containerName(paths))
		return decodeAgentExit(runErr)
	}
	return nil
}

// preparedLaunch is what the locked setup phase hands to the launch. rv is the
// AUTHORITATIVE resolution — the one re-read under the lock, describing the
// box that was actually created — and post-lock code reads only this struct:
// the rv develop entered with is stale by contract (the editor's save may have
// landed while develop waited for the lock). The netns fields carry the hook
// plumbing whose labels are already on the created container's argv.
type preparedLaunch struct {
	rv         resolved
	hostEnv    []hostEnvResult
	hooks      []skills.NetnsHook
	netnsLabel string
	netnsEnv   map[string]string
	// containerID is what Create printed — the exact container this launch
	// made, the credential inject's target (an exec by ID cannot land on a
	// same-named successor).
	containerID string
	// creds is the under-lock decrypt's product: values retained in this
	// prepared launch while the attached develop command lives, plus the
	// manifest and record entries already written.
	creds credPayload
}

// prepareLaunchLocked is the setup-lock body of develop: re-enroll, the
// authoritative config re-read, the refusal gates, the build, the seed, the
// launch record, and container creation — in an order that is the contract
// (see the comments at each step). It runs entirely under the setup lock —
// container Create and record reaping included, because the created container
// is the session-ownership marker (never hoist them out). The preparedLaunch
// is built only on the all-success path.
func prepareLaunchLocked(r engineRunner, s Streams, paths project.Paths, rv resolved, image string, selfEdit bool, ident runner.Identity, unlocked unlockedFiles, credSkipped bool) (preparedLaunch, error) {
	none := preparedLaunch{}
	// Re-establish enrollment UNDER the lock before any store/engine mutation.
	// develop resolved config/skills BEFORE taking the lock, so a concurrent
	// `byre forget` could have cleared the store (path record included) while
	// we waited; building now would resurrect a forgotten project.
	if err := requireRecorded(paths); err != nil {
		return none, err
	}
	// The authoritative read of the config cascade and the skill set. The
	// editor's save takes THIS lock, so a save that landed while develop
	// waited for it is the configuration that launches -- re-reading here
	// doesn't merely detect that drift, it resolves it. rv is REPLACED, not
	// shadowed: the build, the seed, the run params and (after the lock) the
	// exposure banner and containment lines all describe the box that was
	// actually created, never the one develop set out to create.
	fresh, err := rv.refresh()
	if err != nil {
		return none, err
	}
	// The engine is the one thing this re-read cannot honor -- develop's
	// ADR 0004 peer set is fixed by the pre-lock detection too -- so the
	// shared refusal fires before anything is built.
	if err := refuseEngineChangedUnderLock(fresh.cfg, r.Engine(), "develop"); err != nil {
		return none, err
	}
	rv = fresh
	// The build warnings speak for the config that is about to be built,
	// which is this one.
	warnNonDebianBase(s.Err, rv.cfg.Base)
	warnGuardCollisions(s.Err, rv.cfg, rv.skills)
	warnManagedPathShadows(s.Err, rv.cfg, rv.skills)
	// One host-env resolution feeds the runtime env, the exposure tally,
	// and (in status) the row -- render-from-effect, no re-derivation.
	hostEnv := resolveHostEnv(rv.cfg, rv.gitExe)
	params, err := runParams(paths, rv, image, selfEdit, s.TTY, ident, hostEnv)
	if err != nil {
		return none, err
	}
	// Credential decrypt (launch step 2) — under the lock, against the
	// authoritative cascade re-read. A deliverable set adds the session
	// tmpfs and arms the launcher's bounded fail-CLOSED wait; a failure
	// here stops the launch, before anything is built.
	if rv.credErr != nil {
		return none, fmt.Errorf("credentials: %w", rv.credErr)
	}
	creds, err := decryptCredentialsLocked(rv.credFiles, unlocked, credSkipped)
	if err != nil {
		return none, err
	}
	// The two views of one cascade, compared at the last point before launch.
	// Skipped when the launch is DELIBERATELY credential-less: --credentials=skip
	// says so on stderr and in the record, so a disagreement changes nothing it
	// would have delivered (decryptCredentialsLocked's own under-lock refusal
	// returns early there for the same reason).
	if !credSkipped {
		if err := refuseCredentialViewMismatch(hostEnv, creds); err != nil {
			return none, err
		}
	}
	if len(creds.values) > 0 {
		params.Tmpfs = append(params.Tmpfs, credTmpfs(creds, ident, r.Engine()))
		// Set only when a delivery is in flight, and it persists with the
		// container: a bare restart finds the emptied tmpfs, waits out the
		// bound, and refuses — the restart refusal, by the same stateless
		// handshake the network gate uses.
		params.Env["BYRE_CRED_EXPECT"] = credExpectFlag
	}
	// Netns-hook plumbing is decided before the container exists: the
	// per-invocation nonce label is the hooks' ownership proof (see naming.go)
	// and must be on the CREATE argv below. Without a nonce (no randomness)
	// the hooks are skipped and the launch gate fails the launch closed.
	hooks := rv.skills.NetnsInits()
	var netnsLabel string
	var netnsEnv map[string]string
	if len(hooks) > 0 {
		if nonce := runNonce(); nonce != "" {
			netnsLabel = runKey + "=" + nonce
			params.Labels = append(params.Labels, netnsLabel)
			// The netns helper needs the resolved allowlist. BYRE_EGRESS is the
			// union of every enabled skill's declared egress plus the config
			// `egress` key (ADR 0019) — computed here, so it can't come from
			// baked image ENV. Copy params.Env so keys added below don't leak
			// into the box's own runtime env. (Under an allowlist posture the
			// box ALSO carries BYRE_EGRESS — runParams set it there so the
			// launcher announces the list in agent memory; same value, so the
			// overwrite below is a no-op on that path.)
			netnsEnv = make(map[string]string, len(params.Env)+1)
			for k, v := range params.Env {
				netnsEnv[k] = v
			}
			netnsEnv["BYRE_EGRESS"] = strings.Join(resolvedEgress(rv), " ")
			// The config's `!host[:port]` closures, as written (portless =
			// every port). The deny-by-default helper never reads this (its
			// allowlist above is already subtracted); the open-denylist
			// helper drops exactly these.
			netnsEnv["BYRE_EGRESS_DENY"] = strings.Join(rv.cfg.EgressClosed, " ")
		} else {
			fmt.Fprintln(s.Err, "byre: no randomness available for the netns ownership nonce; skipping netns init — the launch gate will fail the launch closed.")
		}
	}
	// Single-session across an engine switch (ADR 0004): under the lock, refuse
	// if a competing box exists on another installed engine. The per-worktree
	// engine record scopes the query (crossEnginesToCheck): steady state skips
	// it entirely, a recorded switch narrows it to the engines a prior session
	// implicated, and a missing/invalid record widens it to every other
	// installed engine (#4 ruling, 2026-07-22 -- no ambient "podman isn't
	// reachable" note on every develop beside an installed-but-stopped engine).
	toCheck, tracked := crossEnginesToCheck(s.Err, rv.otherEngines, r.Engine(), paths)
	skipped, err := refuseCrossEngineSession(s.Err, toCheck, rv.declinedEngines, r.Engine(), paths)
	if err != nil {
		return none, err
	}
	// Single-WRITER, where the check above is single-SESSION. A volume may
	// declare `sharing = "exclusive"`, and sibling worktrees mount the
	// identical project-scoped volume set by construction (ADR 0009), so
	// this asks the siblings' launch records what they are actually
	// holding. Placed with the other gates -- before the engine record,
	// the build and the seed -- so a refusal leaves the store exactly as
	// it found it.
	if err := refuseExclusiveVolumeHolders(s.Err, paths, os.Getuid(), rv.volumes, append([]sessionRunner{r}, rv.otherEngines...), rv.declinedEngines); err != nil {
		return none, err
	}
	// Only after sole-session is established: a refusal above must leave the
	// record pointing at the engine that still holds the session. An engine
	// skipped as unreachable stays UNRESOLVED in the record -- but only when
	// the record implicated it (tracked); an untracked check carries no prior
	// session's claim, so its skips are disclosed above and not carried.
	if !tracked {
		skipped = nil
	}
	recordSessionEngine(s.Err, paths, r.Engine(), skipped)
	if berr := buildImageWarn(s.Err, r, paths, rv.cfg, rv.skills, image, false, ident); berr != nil {
		return none, berr
	}
	// Seed fresh state volumes that declare a config-level seed, using the
	// image we just built. One-time; existing volumes are left alone.
	if err := seedVolumes(r, s.Err, paths, image, rv.volumes, ident); err != nil {
		return none, err
	}
	// Opt-in: seed the agent's curated non-secret prefs into its fresh state
	// volume (config seed_prefs). No-op unless enabled and the volume is fresh.
	if p := rv.skills.AgentPrefs(); rv.cfg.SeedPrefsEnabled() && p != nil {
		if err := seedPrefs(r, s.Err, paths, image, rv.skills.AgentState(), p.From, p.Files, ident); err != nil {
			return none, err
		}
	}
	// sock_groups: engine-side gid probe (needs the just-built image) +
	// host-source warning (engine stays the authority; Desktop suppressed).
	// Must land on params before Create so --group-add is on the argv.
	warnSockSources(r, s.Err, params, rv.skills)
	applySockGroups(r, s.Err, image, &params, rv.skills)
	// The launch record: what byre is about to tell the engine, made
	// durable and addressable, so `byre status` can describe THIS box for
	// as long as it runs instead of re-resolving the config. Written last
	// under the lock, from the same params Create receives, so nothing
	// between here and the create can move underneath it. A write failure
	// degrades (the box still launches; status falls back to the config).
	rec := launchRecordOf(paths, rv, params, r.Engine(), imageRecord(r, s.Err, image, rv.cfg.Base))
	rec.CredentialUnlock = creds.unlock
	rec.Credentials = creds.record
	launchLabel, launchHash := recordLaunch(s.Err, paths, rec)
	if launchLabel != "" {
		params.Labels = append(params.Labels, launchLabel)
	}
	// The container name makes the session atomic: losing the name means a
	// concurrent develop won the race (a session is now live — report it)
	// or a leftover container holds it (say which and how to clear it).
	containerID, cerr := r.Create(runner.CreateArgs(params))
	if cerr != nil {
		if live, qerr := r.RunningContainersByLabel(workdirLabel(paths)); qerr == nil && len(live) > 0 {
			reportRunning(s.Err, r.Engine(), live, true)
			return none, ExitError{Code: ExitRefused} // refused, session already live
		}
		return none, fmt.Errorf("creating the session container: %w (if a stale container holds the name: %s rm %s)", cerr, r.Engine(), containerName(paths))
	}
	// Records outlive their containers otherwise: this is the only moment
	// byre holds the lock AND knows which containers of the project exist.
	// Opportunistic by construction -- nothing here can fail the launch.
	// Every engine byre can see, not just this one: sibling worktrees share
	// this store and may run on the other engine, and their records are not
	// ours to delete. The peer set is the one develop already resolved for
	// the ADR 0004 check, so this costs no new host probing.
	reapLaunchRecords(paths, launchHash, append([]sessionRunner{r}, rv.otherEngines...), rv.declinedEngines)
	return preparedLaunch{rv: rv, hostEnv: hostEnv, hooks: hooks, netnsLabel: netnsLabel, netnsEnv: netnsEnv, containerID: containerID, creds: creds}, nil
}

// decodeAgentExit distinguishes the agent/container's own exit from a byre
// failure. The 125-127 band docker RUN reserves for engine-level failures is
// not reserved on THIS path: the session is create + `start --attach`, and
// start reports an engine-level failure (the marker container removed by a
// concurrent reset, say) as exit 1 with the cause on stderr. So every code
// the engine hands back below 128 is the agent's own status and passes
// through unbannered — including 126 and 127, which an agent's own shell
// spends on "not executable" and "not found". A signal-terminated process
// (ExitCode() == -1) and a non-ExitError failure (the engine binary itself
// could not run) stay byre errors: the original runErr comes back unchanged.
func decodeAgentExit(runErr error) error {
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		code := exitErr.ExitCode()
		if code >= 0 && code <= 127 {
			return ExitError{Code: code}
		}
		// 128+n usually means the box died on signal n. The bare "exit
		// status 137" reads like a byre bug to the person whose box just
		// vanished; decode it. Only SIGKILL supports the strong
		// killed-out-from-under diagnosis (docker rm -f, engine shutdown,
		// the OOM killer — nothing in a box's normal life SIGKILLs its
		// PID 1). The convention is ambiguous for the rest — a process
		// can literally exit(130) with no signal involved — so other
		// codes in the signal range (1-31 classic, through 64 for Linux
		// realtime signals) decode tentatively, and codes beyond it
		// can't be signals and stay undecoded.
		if code == 128+9 {
			return fmt.Errorf("exit status %d (SIGKILL — the box was killed out from under the session: removed externally, engine shutdown, or the kernel OOM killer)", code)
		}
		if code > 128 && code <= 128+64 {
			return fmt.Errorf("exit status %d (possibly %s)", code, signalName(code-128))
		}
	}
	return runErr
}

// buildImageWarn generates the build context and builds the project's image,
// with the operator's stderr attached so assemble-time disclosures (the
// [[context]] prose size tiers) reach the user on the paths a human watches
// (develop, rebuild); tests pass io.Discard. The build bakes the identity's
// UID/GID via --build-arg so /home/dev and the volume mount points are born
// owned by the runtime user (no runtime chown) — the host user's ids on the
// rootful path, the generic keep-id ids under rootless Podman (ADR 0032).
func buildImageWarn(warn io.Writer, r imageRunner, paths project.Paths, cfg config.Config, res skills.Resolved, image string, noCache bool, ident runner.Identity) error {
	if _, err := build.AssembleWarn(paths, cfg, res, warn); err != nil {
		return err
	}
	return r.Build(image, paths.Dockerfile, paths.ContextDir, noCache, uidBuildArgs(ident))
}

// requireNonRootHost refuses to build/run as uid or gid 0. byre bakes the
// invoking user's id into the image as the `dev` user, so running as root makes
// the in-container agent root — it would write root-owned files onto host bind
// mounts, defeating byre's unprivileged-agent design. Determined users can
// override with BYRE_ALLOW_ROOT=1, which only prints a warning. warn receives
// that warning (human-facing, so callers pass stderr).
func requireNonRootHost(warn io.Writer) error {
	if os.Getuid() != 0 && os.Getgid() != 0 {
		return nil
	}
	if os.Getenv("BYRE_ALLOW_ROOT") == "1" {
		fmt.Fprintln(warn, "byre: WARNING: running as root (BYRE_ALLOW_ROOT=1). The container's dev user is UID 0, so the agent runs as root and any files it writes to host mounts are root-owned. This defeats byre's unprivileged-agent design — you're on your own.")
		return nil
	}
	return errors.New("refusing to run as root: byre would bake UID 0 as the container's dev user, so the agent would run as root and create root-owned files on your host mounts. Run byre as your normal user, or set BYRE_ALLOW_ROOT=1 to override anyway.")
}

// uidBuildArgs returns the --build-arg pairs that bake the identity's UID/GID
// into the image. byre develop builds and runs in one invocation, so
// build-identity == run-identity by construction: the invoking user on the
// rootful path, the generic keep-id ids under rootless Podman.
func uidBuildArgs(ident runner.Identity) []string {
	return []string{
		fmt.Sprintf("BYRE_UID=%d", ident.UID),
		fmt.Sprintf("BYRE_GID=%d", ident.GID),
	}
}

// warnNonDebianBase prints a friendly warning when the base image is obviously
// not Debian-derived, since byre's core block assumes apt + glibc.
func warnNonDebianBase(w io.Writer, base string) {
	l := strings.ToLower(base)
	if strings.Contains(l, "alpine") || strings.Contains(l, "scratch") || strings.Contains(l, "distroless") {
		fmt.Fprintf(w, "byre: warning: base %q is not Debian-derived; byre's core block assumes apt + glibc and may fail to build. Use a Debian/Ubuntu base (other bases are unsupported — use docker directly).\n", base)
	}
}

// announceWorktree notes, on stderr, that this directory is a linked worktree
// inheriting the main repo's identity — so shared config/volumes/image and any
// onboarding prompts are legible rather than surprising. No-op for a plain project.
func announceWorktree(w io.Writer, paths project.Paths) {
	if paths.IsWorktree {
		fmt.Fprintf(w, "byre: worktree of %s — inheriting its config, volumes, and image.\n", paths.Canonical)
	}
}

// refuseCrossEngineSession enforces ADR 0004 single-session across an engine
// switch: once `engine` is flipped mid-session, a box on the PREVIOUS engine is
// invisible to the configured runner, so a second develop would launch a second
// autonomous agent on the same working tree. Under the setup lock, query the
// given OTHER engines (scoped by the per-worktree engine record — see
// crossEnginesToCheck) for a container in ANY state (pre-start included) on
// this worktree and refuse if one exists. Any query failure that ISN'T a clean
// unreachability is fatal, since sole-session can't then be established.
//
// Residual, DECIDED (Pete, 2026-07-22): a cleanly-unreachable OTHER engine is
// SKIPPED with a loud note, NOT failed closed. Failing closed would brick every
// develop whenever podman is installed-but-never-started (the common Mac case) --
// a non-starter. The residual codex raised (Docker live-restore or a remote
// Podman can keep a box RUNNING while the daemon is unreachable, so a skipped
// engine could host a competing box) is real but vanishingly narrow: it needs
// live-restore/remote + an outage + a running box + a mid-session engine switch.
// Disclosed, not gated -- degrade the claim, never block the user (footgun
// doctrine). Skipped engine names are RETURNED so an implicated engine's
// uncertainty lands back in the engine record as unresolved and keeps being
// re-checked -- an inconclusive check must never advance the record into
// silence.
func refuseCrossEngineSession(w io.Writer, others []sessionRunner, declined []declinedEngine, self runner.Engine, paths project.Paths) (skipped []string, err error) {
	// A DECLINED engine takes the unreachable arm's treatment, one step
	// earlier: byre never got a runner for it, so it cannot be queried at all.
	// Same shape and for the same reason -- refusing outright would brick
	// develop over an engine that may hold nothing, while a silent skip would
	// let a live docker session sit invisible while `engine = podman` starts a
	// second agent on the same tree. NOT scoped by crossEnginesToCheck: the
	// engine record answers "was this engine implicated", and this answers
	// "byre will not run a binary on this machine", which is worth saying
	// whatever the record holds.
	for _, d := range declined {
		dataf(w, "byre: %v A competing session under %s can't be ruled out — single-session isn't guaranteed against it.\n", error(d), d.Engine)
		skipped = append(skipped, d.Engine)
	}
	label := workdirLabel(paths)
	for _, rr := range others {
		ids, err := rr.ContainersByLabel(label)
		if err != nil {
			if deliver.IsUnreachable(err) {
				fmt.Fprintf(w, "byre: %s isn't reachable, so a competing session there can't be ruled out — single-session isn't guaranteed against it (start %s to check).\n", rr.Engine(), rr.Engine())
				skipped = append(skipped, string(rr.Engine()))
				continue
			}
			return nil, fmt.Errorf("checking %s for a competing session on this project: %w", rr.Engine(), err)
		}
		if len(ids) > 0 {
			fmt.Fprintf(w, "byre: a session for this project already exists under %s, but the configured engine is now %s — refusing to start a second box on the same working tree.\n", rr.Engine(), self)
			reportRunning(w, rr.Engine(), ids, false)
			// This match came from ps -a (ownership markers count), so unlike
			// the same-engine refusal it can name a container that is not
			// running — where attach/shell/stop all fail and only removal
			// clears the refusal. Say so.
			fmt.Fprintf(w, "  • not running? remove it:  %s rm %s\n", rr.Engine(), shortID(ids[0]))
			return nil, ExitError{Code: ExitRefused}
		}
	}
	return skipped, nil
}

// reportRunning tells the user a session already holds this project and how
// to act on it, rather than silently opening a shell (which conflated "run
// the agent" with "give me a shell" — that's `byre shell` now). The detach
// keys are pinned in the attach command because both engines let config
// override the default sequence, and the caveat must be true as printed.
// live distinguishes the callers: the same-engine arms only ever match
// running containers, but the cross-engine arm matches via ps -a, where an
// exited ownership marker is possible — its lead line must not assert
// "running" two lines above its own "not running? remove it" bullet.
func reportRunning(w io.Writer, eng runner.Engine, ids []string, live bool) {
	id := shortID(ids[0])
	if len(ids) > 1 {
		fmt.Fprintf(w, "byre: %d containers match this project; the first is %s\n", len(ids), id)
	}
	if live {
		fmt.Fprintf(w, "byre: a session is already running for this project (%s).\n", id)
	} else {
		fmt.Fprintf(w, "byre: the matched container may be running or stopped (%s).\n", id)
	}
	fmt.Fprintf(w, "  • re-attach to it:     %s attach --detach-keys=ctrl-p,ctrl-q %s   (detach again: Ctrl-P Ctrl-Q; Ctrl-C reaches the agent)\n", eng, id)
	fmt.Fprintf(w, "  • open a shell in it:  byre shell\n")
	fmt.Fprintf(w, "  • stop it:             %s stop %s\n", eng, id)
}

// signalName names the signal behind a 128+n container exit for the decoded
// killed-out-from-under message. Only the signals an engine or kernel actually
// delivers to a whole box get names; anything exotic stays numeric.
func signalName(n int) string {
	switch n {
	case 1:
		return "SIGHUP"
	case 2:
		return "SIGINT"
	case 9:
		return "SIGKILL"
	case 15:
		return "SIGTERM"
	}
	return fmt.Sprintf("signal %d", n)
}

// exposureOf tallies the resolved view for the launch exposure lines. The
// counts must match what actually happens at run time: disabled mounts
// produce no bind (runParams skips them), ports come from config only, env is
// the distinct keys the box gets (baked config env ∪ skill runtime env ∪ the
// env_from_host passthrough — a restated key is one variable, not two), and
// egress is the enforced deduped union. Plumbing env (BYRE_UID) isn't counted
// — it's how every box works, not this box's exposure; env_from_host IS
// counted: named host-value passthrough is a real grant, however it got
// configured (the shipped git-identity defaults included). The network claim
// consumes status's networkLine degradation inputs rather than restating
// them: the banner and the Network row are two renderings of one claim, and
// a set only one of them reads is a set they can drift on.
// --self-edit's rw store mount gets its own named segment (like status's
// Self-edit row), not a bump of the host-mount count. A worktree's same-path git binds are consciously NOT
// counted: they're the project's own repo (ADR 0009 — worktrees inherit
// project identity), status doesn't list them either, and the worktree
// banner already announces the arrangement. Caps and skill run_args are
// also consciously out of the count's scope (mounts/ports/env/network):
// status's Skill grants rows carry that attribution.
func exposureOf(rv resolved, selfEdit bool, hostEnv []hostEnvResult) config.Exposure {
	envKeys := map[string]bool{}
	for k := range rv.cfg.Env {
		envKeys[k] = true
	}
	for k := range rv.skills.Env() {
		envKeys[k] = true
	}
	// Delivered only: a source that resolved empty put nothing in the box,
	// and the tally counts what crossed, not what was configured to try.
	for _, r := range hostEnv {
		if r.State == hostEnvDelivered {
			envKeys[r.Key] = true
		}
	}
	e := config.Exposure{
		Workspace:   true,
		SelfEdit:    selfEdit,
		Ports:       len(rv.cfg.Ports),
		Env:         len(envKeys),
		Credentials: countRows(rv.credFiles),
		Egress:      len(resolvedEgress(rv)),
		Closed:      len(rv.cfg.EgressClosed),
		RawRunArgs:  len(rv.cfg.RunArgs) > 0,
		RawBuild:    len(rv.cfg.DockerfilePre)+len(rv.cfg.DockerfilePost) > 0,
		// The third degradation input, from the predicate status's Network row
		// consults over the same resolved set: a skill holding byre's own
		// network knobs makes the posture claim describe a construction that
		// is no longer in force (ADR 0050).
		SkillNetControls: skills.ReservedEnvTouches(rv.skills.ReservedEnv(), skills.ClaimNetwork),
	}
	for _, m := range rv.mounts {
		if m.Disabled {
			e.DisabledMounts++
		} else {
			e.Mounts++
		}
	}
	e.Posture, _ = rv.skills.NetworkPosture()
	return e
}

// resolvedEgress is the full normalized allowlist the netns helper enforces:
// every enabled skill's declared egress, the config `egress` key (ADR 0019),
// and the egress the declared MCP set CARRIES (each remote server's URL
// endpoint plus its declared extras — implied by the wiring, attributed
// mcp:<name> on status), deduped as host:port. The config entries are already
// validated by the resolved config, so a parse failure here is unreachable
// and skipped.
func resolvedEgress(rv resolved) []string {
	out := rv.skills.Egress()
	seen := map[string]bool{}
	for _, e := range out {
		seen[e] = true
	}
	for _, e := range rv.cfg.Egress {
		host, port, err := config.ParseEgress(e)
		if err != nil {
			continue
		}
		hp := fmt.Sprintf("%s:%d", host, port)
		if !seen[hp] {
			seen[hp] = true
			out = append(out, hp)
		}
	}
	for _, a := range skills.MCPEgress(rv.mcps) {
		hp := fmt.Sprintf("%s:%d", a.Host, a.Port)
		if !seen[hp] {
			seen[hp] = true
			out = append(out, hp)
		}
	}
	// Closures subtract LAST — after the skill union — which is what puts
	// skill-declared entries in their reach (`claude` minus its statsig; the
	// cascade merge already consumed any config entry a closure matched).
	return egressAfterClosures(out, rv.cfg.EgressClosed)
}

// egressAfterClosures subtracts the config's `!host[:port]` closures from a
// deduped host:port list — the last step that turns a declared union into the
// ENFORCED allowlist.
//
// Its own function because two surfaces must reach the same answer and had
// drifted. What byre hands the netns helper (and records at launch) is this
// list; status's Egress ROWS instead show the declared union with a closed
// entry marked closed-by rather than removed, which is the whole point of
// `!host` reaching past the cascade. The next-launch diff compares against a
// record, so it needs the enforced form — and computing it a second time by
// hand is exactly how two spellings of one rule stop agreeing.
func egressAfterClosures(entries, closures []string) []string {
	if len(closures) == 0 {
		return entries
	}
	kept := entries[:0]
	for _, hp := range entries {
		host, port, _ := config.ParseEgress(hp)
		if _, closed := closedBy(closures, host, port); !closed {
			kept = append(kept, hp)
		}
	}
	return kept
}
