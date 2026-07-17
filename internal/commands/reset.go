package commands

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pjlsergeant/byre/internal/project"
)

// Reset implements `byre reset`: wipe ALL of this project's named volumes (only
// volumes — not the image), across EVERY installed engine — the volumes may
// live in an engine the config no longer names. It names what dies first,
// refuses while a session is live, and serializes with the setup lock. force
// skips the confirmation prompt.
func Reset(s Streams, projectDir string, force bool) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	// No write, no enrollment applies to teardown too: a never-enrolled
	// project has no store or volumes of byre's making, so enrolling one
	// just to report "no volumes to reset" would leave the durable state
	// this command exists to remove. Collisions still fail loudly.
	recorded, err := paths.Recorded()
	if err != nil {
		return err
	}
	if !recorded {
		fmt.Fprintln(s.Err, "byre: this project has never been developed here — nothing to reset.")
		return nil
	}
	if err := paths.Bootstrap(); err != nil {
		return err
	}
	engines, err := lifecycleEngines()
	if err != nil {
		return err
	}
	return reset(s, paths, engines, force)
}

// liveSession lists the running containers of the project (any of its worktrees).
func liveSession(r sessionRunner, id string) ([]string, error) {
	return r.RunningContainersByLabel(labelKey + "=" + id)
}

// clearSessionMarkers is the destructive commands' under-the-lock guard
// against a concurrent develop. develop creates its container under the same
// lock and starts it after release, so a project container in ANY state is a
// session's ownership marker: running means a live session (abort), and
// created-but-not-started means a develop is between create and start —
// remove the marker (forceless, so a session that started meanwhile makes
// the removal fail and we abort) and that develop's start fails loudly
// instead of launching against the state the caller is about to delete.
func clearSessionMarkers(w io.Writer, r sessionRunner, id string) error {
	if live, err := liveSession(r, id); err != nil {
		return fmt.Errorf("checking for a running session: %w", err)
	} else if len(live) > 0 {
		return fmt.Errorf("a session started for this project (%s); aborting", shortID(live[0]))
	}
	all, err := r.ContainersByLabel(labelKey + "=" + id)
	if err != nil {
		return fmt.Errorf("checking for session containers: %w", err)
	}
	for _, c := range all {
		if rerr := r.ContainerRemove(c); rerr != nil {
			return fmt.Errorf("a session appears to be starting for this project (container %s could not be removed: %v); aborting", shortID(c), rerr)
		}
		fmt.Fprintf(w, "byre: removed pre-start container %s (its develop will fail to launch)\n", shortID(c))
	}
	return nil
}

func reset(s Streams, paths project.Paths, engines []engineRunner, force bool) error {
	multi := len(engines) > 1
	// Fast fail: never wipe volumes out from under a running session — on any
	// engine. A query failure is fatal: an engine that can't be inspected
	// can't be declared session-free (or cleaned).
	total := 0
	for _, r := range engines {
		if live, err := liveSession(r, paths.ID); err != nil {
			return fmt.Errorf("checking for a running session (%s): %w", r.Engine(), err)
		} else if len(live) > 0 {
			return fmt.Errorf("a session is running for this project (%s%s); exit it before reset", shortID(live[0]), engineSuffix(multi, r))
		}
		vols, err := projectVolumes(r, paths.Home, paths.ID)
		if err != nil {
			return fmt.Errorf("listing volumes (%s): %w", r.Engine(), err)
		}
		total += len(vols)
		// The machine-volume note comes before the empty-case return: a project
		// whose ONLY volumes are machine-scoped must still hear what was spared
		// and why (review finding on ADR 0017).
		noteMachineVolumes(s.Err, r, os.Getuid())
	}
	if total == 0 {
		fmt.Fprintf(s.Err, "byre: no volumes to reset for %s\n", paths.ID)
		return nil
	}

	noteSharedVolumes(s.Err, paths)
	fmt.Fprintf(s.Err, "byre reset will permanently delete these volumes for %s:\n", paths.ID)
	for _, r := range engines {
		vols, err := projectVolumes(r, paths.Home, paths.ID)
		if err != nil {
			return fmt.Errorf("listing volumes (%s): %w", r.Engine(), err)
		}
		for _, v := range vols {
			fmt.Fprintf(s.Err, "  - %s%s\n", v, engineSuffix(multi, r))
		}
	}
	fmt.Fprintln(s.Err, "State volumes (e.g. agent credentials) will need to be re-created/re-authed on next develop.")

	if !force {
		if !confirmed(s.Err, s.In, "Proceed? [y/N] ") {
			fmt.Fprintln(s.Err, "aborted.")
			return nil
		}
	}

	// Serialize with develop's setup so we don't race a concurrent build/seed.
	return withSetupLock(s.Err, paths.LockFile, func() error {
		var failed []string
		volsTotal := 0
		for _, r := range engines {
			// Under the lock, per engine: abort on a session that started since
			// the prompt, and dissolve any pre-start ownership marker before
			// touching volumes.
			if err := clearSessionMarkers(s.Err, r, paths.ID); err != nil {
				return err
			}

			// Re-list under the lock so a volume created since the prompt (e.g. by
			// a concurrent setup that has since finished) is also wiped, not
			// stranded.
			vols, err := projectVolumes(r, paths.Home, paths.ID)
			if err != nil {
				return fmt.Errorf("listing volumes (%s): %w", r.Engine(), err)
			}
			volsTotal += len(vols)

			// Continue through all volumes (don't leave a half-wipe on first
			// error); report a summary and fail if any failed.
			for _, v := range vols {
				if err := r.VolumeRemove(v); err != nil {
					fmt.Fprintf(s.Err, "byre: FAILED to remove %s%s: %v\n", v, engineSuffix(multi, r), err)
					failed = append(failed, v)
					continue
				}
				fmt.Fprintf(s.Err, "byre: removed %s%s\n", v, engineSuffix(multi, r))
			}
		}
		if len(failed) > 0 {
			return fmt.Errorf("reset incomplete: %d of %d volumes not removed (%s)", len(failed), volsTotal, strings.Join(failed, ", "))
		}
		return nil
	})
}
