package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"byre/internal/build"
	"byre/internal/config"
	"byre/internal/project"
	"byre/internal/runner"
	"byre/internal/skills"
)

// selfEditTarget is where --self-edit mounts this project's host-side store
// (~/.byre/projects/<id>/) inside the box, so the agent can edit its OWN
// byre.config — the deliberate "let the agent change its own sandbox" grant.
const selfEditTarget = "/home/dev/.byre-self"

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
// flagTemplate/flagAgent come from --template/--agent (empty = unspecified).
// selfEdit (--self-edit) bind-mounts this project's host-side store
// (~/.byre/projects/<id>/, not all of ~/.byre) read-write at selfEditTarget so
// the agent can edit its own byre.config — a deliberate grant.
func Develop(s Streams, projectDir, flagTemplate, flagAgent string, selfEdit bool) error {
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
	// Worktree: announce the inherited identity up front, so any onboarding/adopt
	// prompts below are understood as configuring the whole repo family.
	announceWorktree(s.Err, paths)
	// A committed <project>/byre.config is a proposal: offer to review + adopt it
	// into the host-side store (never trusted automatically — it's in the box's
	// rw mount). Runs before onboarding so adopting satisfies "already configured".
	if err := adoptIfProposed(s, projectDir, paths); err != nil {
		return err
	}
	// First-run onboarding: with no host-side config, pick (or apply flags / fall
	// back to the cascade on non-TTY) and write the store's byre.config.
	if err := onboardIfNeeded(s, projectDir, paths, flagTemplate, flagAgent); err != nil {
		return err
	}
	// Validate bind sources before any build/seed side effects: a comma would
	// corrupt a docker --mount value (workspace bind, and worktree git binds).
	if err := checkMountPaths(paths); err != nil {
		return err
	}
	rv, err := resolve(paths, projectDir)
	if err != nil {
		return err
	}
	if rv.cfg.Dockerfile == "" {
		warnNonDebianBase(s.Err, rv.cfg.Base)
	}
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
	warnRootlessPodman(s.Err, r)

	image := imageTag(paths.ID, os.Getuid(), os.Getgid())

	// Fast path: a session is already running for THIS worktree — report it
	// rather than racing the container name. Queried by the worktree label, not
	// the family label, so another worktree's live session doesn't block this
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

	// Setup (generate + build) is serialized by the lock; the interactive
	// session that follows is not.
	if err := withSetupLock(paths.LockFile, func() error {
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
			return seedPrefs(r, s.Err, paths, image, rv.skills.AgentState(), p.From, p.Files, os.Getuid(), os.Getgid())
		}
		return nil
	}); err != nil {
		return err
	}

	if selfEdit {
		fmt.Fprintln(s.Err, "💡 self-edit on — the agent can edit its own byre.config; changes apply on the next develop.")
		fmt.Fprintf(s.Err, "   read-write mount: %s\n", paths.Dir)
	}
	params, err := runParams(paths, rv, image, selfEdit, s.TTY)
	if err != nil {
		return err
	}
	// The container name makes this atomic: if a concurrent develop won the
	// race, our run fails and a session is now live — report it.
	if runErr := r.Run(runner.RunArgs(params)); runErr != nil {
		if live, qerr := r.RunningContainersByLabel(workdirLabel(paths)); qerr == nil && len(live) > 0 {
			reportRunning(s.Err, r.Engine(), live)
			return ExitError{Code: ExitRefused} // refused, session already live
		}
		// Distinguish the agent/container's own exit from a byre failure: docker
		// reserves 125-127 for engine-level failures (cannot run / not
		// executable / not found), so only codes below that are passed through
		// as the agent's own status (no byre error banner). Anything else —
		// 125-127, a signal-terminated process (ExitCode() == -1), or a
		// non-ExitError failure (e.g. the engine binary itself couldn't run) —
		// stays a byre error.
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

// buildImage builds the project's image, honoring the full-Dockerfile opt-out
// uniformly (so develop and rebuild produce the same image for opt-out projects).
// The generated build bakes the host UID/GID via --build-arg so /home/dev and the
// volume mount points are born owned by the runtime user (no runtime chown). The
// opt-out path gets no build args: the user owns that infra layer.
func buildImage(r imageRunner, paths project.Paths, cfg config.Config, res skills.Resolved, image string, noCache bool) error {
	if cfg.Dockerfile != "" {
		dfPath, err := resolveProjectFile(paths.Canonical, cfg.Dockerfile)
		if err != nil {
			return err
		}
		return r.Build(image, dfPath, paths.Canonical, noCache, nil)
	}
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
// not Debian-derived, since byre's core infra layer assumes apt + glibc.
func warnNonDebianBase(w io.Writer, base string) {
	l := strings.ToLower(base)
	if strings.Contains(l, "alpine") || strings.Contains(l, "scratch") || strings.Contains(l, "distroless") {
		fmt.Fprintf(w, "byre: warning: base %q is not Debian-derived; byre's core infra layer assumes apt + glibc and may fail to build. Use a Debian/Ubuntu base, or a full hand-written Dockerfile (dockerfile = ...).\n", base)
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
