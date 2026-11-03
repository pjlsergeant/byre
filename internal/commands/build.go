package commands

import (
	"fmt"
	"path/filepath"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

// BuildOptions is `byre build`'s parsed command line.
type BuildOptions struct {
	// Agent is --agent: on a configured project, the run-scoped override
	// (the image is built with that agent, nothing written); on a new one,
	// with Template, the project's agent.
	Agent string
	// Template is --template: a NEW project's template ("none" = none).
	Template string
}

// Build builds under the setup lock and prints the image tag on stdout; the
// engine's build stream goes to stderr; no session.
func Build(s Streams, projectDir string, o BuildOptions) error {
	if err := requireNonRootHost(s.Err); err != nil {
		return err
	}
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	// The non-TTY onboarding arm would build whatever the cascade says
	// without writing a config, and the picker's questions have no place in
	// a build a job runs: an unconfigured project is judged here, as run
	// judges it -- both axis flags configure it, anything less is refused.
	// Judged before Bootstrap, so a refusal enrolls nothing.
	cfgPath := filepath.Join(paths.Dir, config.ProjectConfigName)
	configured, perr := hostopen.ExistsNoFollow(cfgPath)
	if perr != nil {
		return fmt.Errorf("cannot tell whether %s exists (%v) -- fix the store's permissions, or run 'byre forget' to clear it", cfgPath, perr)
	}
	if !configured && (o.Template == "" || o.Agent == "") {
		fmt.Fprintln(s.Err, `byre: this project is not configured here: pass --template <t> and --agent <a> (use "none" for no template), or configure it first with byre develop / byre preset apply`)
		return ExitError{Code: 1}
	}
	if err := paths.Bootstrap(); err != nil {
		return err
	}
	if err := builtins.EnsureStoreOut(paths.Home, s.Err); err != nil {
		return err
	}
	developNotices(s.Err, projectDir, paths, nil)
	// Onboarding writes byre.config, a setup mutation: under the lock, as
	// develop runs it. On a configured project --agent comes back as the
	// run-scoped override.
	wasConfigured, err := setupLockedProject(s.Err, paths, func() (bool, error) {
		if err := requireRecorded(paths); err != nil {
			return false, err
		}
		return onboardIfNeeded(s, projectDir, paths, o.Template, o.Agent, nil)
	})
	if err != nil {
		return err
	}
	agentOverride := ""
	if wasConfigured && o.Agent != "" {
		agentOverride = o.Agent
	}
	rv, err := resolveWithAgent(paths, projectDir, s.Err, agentOverride)
	if err != nil {
		return err
	}
	eng, engExe, err := runner.Detect(rv.cfg.Engine, hostexec.Looker(boxWritableRoots(paths)))
	if err != nil {
		return err
	}
	r := runner.New(eng, engExe).WithStreamStdout(s.Err)
	return buildImage(r, s, paths, rv)
}

// buildImage is Build's engine-facing core, split out so it runs against a
// fake engine. It is develop's setup-lock body cut off after the build.
func buildImage(r engineRunner, s Streams, paths project.Paths, rv resolved) error {
	// The identity develop will run with: the tag and the baked uid/gid
	// follow it, so the image built here is the one develop picks up.
	ident, err := resolveIdentity(s.Err, r)
	if err != nil {
		return err
	}
	if paths.IsWorktree {
		fmt.Fprintf(s.Err, "byre: worktree -- the shared project image builds from the main worktree (%s); `files` sources changed only in this worktree don't reach the image.\n", paths.Canonical)
	}
	if rv.agentOverride != "" {
		dataf(s.Err, "byre: agent for this build: %s (--agent override; nothing written).\n", config.OrNone(rv.cfg.Agent))
	}
	image := imageTag(paths.ID, ident.UID, ident.GID)
	if err := withSetupLockProject(s.Err, paths, func() error {
		// A forget that won while this waited must not be undone by a build.
		if err := requireRecorded(paths); err != nil {
			return err
		}
		// The editor's save takes this lock: the config built is the one on
		// disk now, not the pre-lock read.
		fresh, err := rv.refresh()
		if err != nil {
			return err
		}
		if err := refuseEngineChangedUnderLock(fresh.cfg.Config, r.Engine(), "build"); err != nil {
			return err
		}
		warnNonDebianBase(s.Err, fresh.cfg.Base)
		warnGuardCollisions(s.Err, fresh.cfg.Config, fresh.skills)
		warnManagedPathShadows(s.Err, fresh.cfg.Config, fresh.skills)
		warnCompatLegacy(s.Err, paths.WorkDir)
		return buildImageWarn(s.Err, r, paths, fresh.cfg, fresh.skills, image, false, ident)
	}); err != nil {
		return err
	}
	fmt.Fprintln(s.Out, image)
	return nil
}
