package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/pjlsergeant/byre/internal/build"
	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
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
func Develop(s Streams, projectDir, flagTemplate, flagAgent string, flagSharedAuth *bool, selfEdit bool) error {
	if err := requireNonRootHost(s.Err); err != nil {
		return err
	}
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if err := paths.Bootstrap(); err != nil {
		return err
	}
	// Store-ensure (bundled mirror + LEGACY notices) rides every develop so an
	// upgraded byre surfaces D10 without requiring `skill update` (D7b/D10).
	if err := builtins.EnsureStoreOut(paths.Home, s.Err); err != nil {
		return err
	}
	// Worktree: announce the inherited identity up front, so any onboarding
	// prompts below are understood as configuring the whole project (all its worktrees).
	announceWorktree(s.Err, paths)
	// A repo-shipped preset is like package.json: cloning gives you a file,
	// not a prompt (D17 — the adoption offer is retired). Passive visibility
	// only: state 1 (not applied) and state 3 (diverged) get one note; the
	// steady state is silent. `byre preset apply` is the solicited flow.
	if note := presetNote(projectDir, paths); note != "" {
		fmt.Fprintf(s.Err, "byre: %s\n", note)
	}
	// First-run onboarding: with no host-side config, pick (or apply flags / fall
	// back to the cascade on non-TTY) and write the store's byre.config.
	if err := onboardIfNeeded(s, projectDir, paths, flagTemplate, flagAgent, flagSharedAuth); err != nil {
		return err
	}
	// Validate bind sources before any build/seed side effects: a comma would
	// corrupt a docker --mount value (workspace bind, and worktree git binds).
	if err := checkMountPaths(paths); err != nil {
		return err
	}
	rv, err := resolve(paths, projectDir, s.Err)
	if err != nil {
		return err
	}
	warnNonDebianBase(s.Err, rv.cfg.Base)
	eng, err := runner.Detect(rv.cfg.Engine, nil)
	if err != nil {
		return err
	}
	return develop(runner.New(eng), s, paths, rv, selfEdit)
}

// develop is the engine-facing core of Develop — the live-session fast path,
// then build + seed under the setup lock, then the foreground run and its
// exit-status mapping. Split from Develop (which does the host-side resolution
// and onboarding) so it can run end-to-end against a fake engine.
func develop(r engineRunner, s Streams, paths project.Paths, rv resolved, selfEdit bool) error {
	if err := requireRootfulEngine(s.Err, r); err != nil {
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

	image := imageTag(paths.ID, os.Getuid(), os.Getgid())

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
		reportRunning(s.Err, r.Engine(), ids)
		return ExitError{Code: ExitRefused} // refused, session already live
	}

	// --self-edit hands the agent authorship of its own next sandbox; open the
	// session with the warning. (The store snapshot backing the session-end
	// diff is taken after setup below — setup itself writes the store.)
	if selfEdit {
		fmt.Fprintln(s.Err, "🛑 self-edit is on. A malicious or incompetent agent can change the configuration to grant itself full access to your host on the next run.")
		fmt.Fprintf(s.Err, "   read-write mount: %s\n", paths.Dir)
	}
	params, err := runParams(paths, rv, image, selfEdit, s.TTY)
	if err != nil {
		return err
	}
	// Netns-hook plumbing is decided before the container exists: the
	// per-invocation nonce label is the hooks' ownership proof (see naming.go)
	// and must be on the CREATE argv below. Without a nonce (no randomness)
	// the hooks are skipped and the launch gate fails the launch closed.
	var netnsLabel string
	var netnsEnv map[string]string
	hooks := rv.skills.NetnsInits()
	if len(hooks) > 0 {
		if nonce := runNonce(); nonce != "" {
			netnsLabel = runKey + "=" + nonce
			params.Labels = append(params.Labels, netnsLabel)
			// The netns helper needs the resolved allowlist. BYRE_EGRESS is the
			// union of every enabled skill's declared egress plus the config
			// `egress` key (ADR 0019) — computed here, so it can't come from
			// baked image ENV. Copy params.Env so the added key doesn't leak
			// into the box's own runtime env.
			netnsEnv = make(map[string]string, len(params.Env)+1)
			for k, v := range params.Env {
				netnsEnv[k] = v
			}
			netnsEnv["BYRE_EGRESS"] = strings.Join(resolvedEgress(rv), " ")
		} else {
			fmt.Fprintln(s.Err, "byre: no randomness available for the netns ownership nonce; skipping netns init — the launch gate will fail the launch closed.")
		}
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
	if err := withSetupLock(s.Err, paths.LockFile, func() error {
		if berr := buildImage(r, paths, rv.cfg, rv.skills, image, false); berr != nil {
			return berr
		}
		// Seed fresh state volumes that declare a config-level seed, using the
		// image we just built. One-time; existing volumes are left alone.
		if err := seedVolumes(r, s.Err, paths, image, rv.volumes, os.Getuid(), os.Getgid()); err != nil {
			return err
		}
		// Opt-in: seed the agent's curated non-secret prefs into its fresh state
		// volume (config seed_prefs). No-op unless enabled and the volume is fresh.
		if p := rv.skills.AgentPrefs(); rv.cfg.SeedPrefs && p != nil {
			if err := seedPrefs(r, s.Err, paths, image, rv.skills.AgentState(), p.From, p.Files, os.Getuid(), os.Getgid()); err != nil {
				return err
			}
		}
		// sock_groups: engine-side gid probe (needs the just-built image) +
		// host-source warning (engine stays the authority; Desktop suppressed).
		// Must land on params before Create so --group-add is on the argv.
		warnSockSources(r, s.Err, params, rv.skills)
		applySockGroups(r, s.Err, image, &params, rv.skills)
		// The container name makes the session atomic: losing the name means a
		// concurrent develop won the race (a session is now live — report it)
		// or a leftover container holds it (say which and how to clear it).
		if cerr := r.Create(runner.CreateArgs(params)); cerr != nil {
			if live, qerr := r.RunningContainersByLabel(workdirLabel(paths)); qerr == nil && len(live) > 0 {
				reportRunning(s.Err, r.Engine(), live)
				return ExitError{Code: ExitRefused} // refused, session already live
			}
			return fmt.Errorf("creating the session container: %w (if a stale container holds the name: %s rm %s)", cerr, r.Engine(), containerName(paths))
		}
		return nil
	}); err != nil {
		return err
	}

	// Snapshot the store only now, after setup wrote its own files into it, so
	// the session-end diff (reportSelfEditChanges) shows what the AGENT
	// touched, not byre's own staging.
	var store storeSnapshot
	if selfEdit {
		store = snapshotStore(paths.Dir)
	}

	// Every real session opens by showing the walls going up: the terse
	// exposure lines. Printed only once the container exists — a launch that
	// failed setup or lost the name race gets no walls claimed. (The self-edit
	// warning above is consciously pre-create: it guards a decision, not a
	// session.) The config UI renders the same tally (config.Exposure owns the
	// words); `byre status` is the detailed, attributed view.
	exp := exposureOf(rv, selfEdit)
	fmt.Fprintf(s.Err, "byre: exposure: %s\n", exp.GrantsLine())
	fmt.Fprintf(s.Err, "byre: %s\n", exp.NetworkLine())
	// Containment holes (e.g. docker-host): loud standing grant, at least
	// self-edit's 🛑 weight. Skill-owned text; byre frames and attributes.
	for _, c := range rv.skills.Containments() {
		fmt.Fprintf(s.Err, "byre: 🛑 containment hole: %s  (skill: %s)\n", c.Text, c.Skill)
	}
	// Netns-init hooks (e.g. the firewall skill's rules) are applied from
	// OUTSIDE the box, concurrently with the attached session: the box's
	// launcher waits at its launch gate until the hooks land. The wait after
	// the session keeps the goroutine from outliving develop (and its s.Err
	// writes).
	var netnsWait func()
	if len(hooks) > 0 && netnsLabel != "" {
		done := make(chan struct{})
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			runNetnsInits(r, s.Err, netnsLabel, image, hooks, netnsEnv, done)
		}()
		netnsWait = func() { close(done); <-finished }
	}

	runErr := r.StartAttach(containerName(paths))
	if netnsWait != nil {
		netnsWait()
	}
	// The session is over (runErr may just be the agent's own exit status):
	// show what a self-edit agent changed before the exit paths below return.
	if selfEdit {
		reportSelfEditChanges(s.Err, paths.Dir, store)
	}
	if runErr != nil {
		if live, qerr := r.RunningContainersByLabel(workdirLabel(paths)); qerr == nil && len(live) > 0 {
			reportRunning(s.Err, r.Engine(), live)
			return ExitError{Code: ExitRefused} // refused, session already live
		}
		// A start that never ran leaves the created container behind (--rm only
		// fires on exit); remove it best-effort so the name isn't stranded. A
		// forceless rm can't kill a running session, and after a normal agent
		// exit the container is already gone — both failures are ignorable.
		_ = r.ContainerRemove(containerName(paths))
		// Distinguish the agent/container's own exit from a byre failure: docker
		// reserves 125-127 for engine-level failures (cannot run / not
		// executable / not found), so only codes below that are passed through
		// as the agent's own status (no byre error banner). Anything else —
		// 125-127, a signal-terminated process (ExitCode() == -1), or a
		// non-ExitError failure (e.g. the engine binary itself couldn't run) —
		// stays a byre error. (`start` reports engine-level failures — e.g. the
		// marker container removed by a concurrent reset — as exit 1 with the
		// cause on stderr, so those pass through as an ordinary failed status.)
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			if code := exitErr.ExitCode(); code >= 0 && code < 125 {
				return ExitError{Code: code}
			}
		}
		return runErr
	}
	return nil
}

// buildImage generates the build context and builds the project's image. The
// build bakes the host UID/GID via --build-arg so /home/dev and the volume
// mount points are born owned by the runtime user (no runtime chown).
func buildImage(r imageRunner, paths project.Paths, cfg config.Config, res skills.Resolved, image string, noCache bool) error {
	if _, err := build.Assemble(paths, cfg, res); err != nil {
		return err
	}
	return r.Build(image, paths.Dockerfile, paths.ContextDir, noCache, uidBuildArgs())
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

// uidBuildArgs returns the --build-arg pairs that bake the invoking user's
// UID/GID into the image. byre develop builds and runs as the same user in one
// invocation, so build-UID == run-UID by construction.
func uidBuildArgs() []string {
	return []string{
		fmt.Sprintf("BYRE_UID=%d", os.Getuid()),
		fmt.Sprintf("BYRE_GID=%d", os.Getgid()),
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

// reportRunning tells the user a session is already live and how to act on it,
// rather than silently opening a shell (which conflated "run the agent" with
// "give me a shell" — that's `byre shell` now).
func reportRunning(w io.Writer, eng runner.Engine, ids []string) {
	id := shortID(ids[0])
	if len(ids) > 1 {
		fmt.Fprintf(w, "byre: %d containers match this project; the first is %s\n", len(ids), id)
	}
	fmt.Fprintf(w, "byre: a session is already running for this project (%s).\n", id)
	fmt.Fprintf(w, "  • open a shell in it:  byre shell\n")
	fmt.Fprintf(w, "  • stop it:             %s stop %s\n", eng, id)
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
// mirrors status's networkLine honesty rules. --self-edit's rw store mount
// gets its own named segment (like status's Self-edit row), not a bump of the
// host-mount count. A worktree's same-path git binds are consciously NOT
// counted: they're the project's own repo (ADR 0009 — worktrees inherit
// project identity), status doesn't list them either, and the worktree
// banner already announces the arrangement. Caps and skill run_args are
// also consciously out of the count's scope (mounts/ports/env/network):
// status's Skill grants rows carry that attribution.
func exposureOf(rv resolved, selfEdit bool) config.Exposure {
	envKeys := map[string]bool{}
	for k := range rv.cfg.Env {
		envKeys[k] = true
	}
	for k := range rv.skills.Env() {
		envKeys[k] = true
	}
	for k, src := range rv.cfg.EnvFromHost {
		if src != "" {
			envKeys[k] = true
		}
	}
	e := config.Exposure{
		Workspace:  true,
		SelfEdit:   selfEdit,
		Ports:      len(rv.cfg.Ports),
		Env:        len(envKeys),
		Egress:     len(resolvedEgress(rv)),
		RawRunArgs: len(rv.cfg.RunArgs) > 0,
		RawBuild:   len(rv.cfg.DockerfilePre)+len(rv.cfg.DockerfilePost) > 0,
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
// every enabled skill's declared egress plus the config `egress` key
// (ADR 0019), deduped as host:port. The config entries are already validated
// by the resolved config, so a parse failure here is unreachable and skipped.
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
	return out
}
