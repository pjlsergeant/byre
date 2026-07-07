package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/pjlsergeant/byre/internal/project"
)

// Reset implements `byre reset`: wipe ALL of this project's named volumes (only
// volumes — not the image). It names what dies first, refuses while a session is
// live, and serializes with the setup lock. force skips the confirmation prompt.
func Reset(s Streams, projectDir string, force bool) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if err := paths.Bootstrap(); err != nil {
		return err
	}
	r, err := resolveEngine(s.Err, projectDir)
	if err != nil {
		return err
	}
	return reset(s, paths, r, force)
}

// liveSession lists the running containers of the project (any of its worktrees).
func liveSession(r sessionRunner, id string) ([]string, error) {
	return r.RunningContainersByLabel(labelKey + "=" + id)
}

func reset(s Streams, paths project.Paths, r engineRunner, force bool) error {
	// Fast fail: never wipe volumes out from under a running session.
	if live, err := liveSession(r, paths.ID); err != nil {
		return fmt.Errorf("checking for a running session: %w", err)
	} else if len(live) > 0 {
		return fmt.Errorf("a session is running for this project (%s); exit it before reset", shortID(live[0]))
	}

	vols, err := projectVolumes(r, paths.Home, paths.ID)
	if err != nil {
		return err
	}
	// The machine-volume note comes before the empty-case return: a project
	// whose ONLY volumes are machine-scoped must still hear what was spared
	// and why (review finding on ADR 0017).
	noteMachineVolumes(s.Err, r, os.Getuid())
	if len(vols) == 0 {
		fmt.Fprintf(s.Err, "byre: no volumes to reset for %s\n", paths.ID)
		return nil
	}

	noteSharedVolumes(s.Err, paths)
	fmt.Fprintf(s.Err, "byre reset will permanently delete these volumes for %s:\n", paths.ID)
	for _, v := range vols {
		fmt.Fprintf(s.Err, "  - %s\n", v)
	}
	fmt.Fprintln(s.Err, "State volumes (e.g. agent credentials) will need to be re-created/re-authed on next develop.")

	if !force {
		fmt.Fprint(s.Err, "Proceed? [y/N] ")
		if !confirmed(s.In) {
			fmt.Fprintln(s.Err, "aborted.")
			return nil
		}
	}

	// Serialize with develop's setup so we don't race a concurrent build/seed.
	return withSetupLock(s.Err, paths.LockFile, func() error {
		// Re-check live under the lock, immediately before deletion, to catch a
		// session that started between the initial check and now.
		if live, err := liveSession(r, paths.ID); err != nil {
			return fmt.Errorf("checking for a running session: %w", err)
		} else if len(live) > 0 {
			return fmt.Errorf("a session started for this project (%s); aborting reset", shortID(live[0]))
		}

		// Re-list under the lock so a volume created since the prompt (e.g. by a
		// concurrent setup that has since finished) is also wiped, not stranded.
		vols, err := projectVolumes(r, paths.Home, paths.ID)
		if err != nil {
			return err
		}

		// Continue through all volumes (don't leave a half-wipe on first error);
		// report a summary and fail if any failed.
		var failed []string
		for _, v := range vols {
			if err := r.VolumeRemove(v); err != nil {
				fmt.Fprintf(s.Err, "byre: FAILED to remove %s: %v\n", v, err)
				failed = append(failed, v)
				continue
			}
			fmt.Fprintf(s.Err, "byre: removed %s\n", v)
		}
		if len(failed) > 0 {
			return fmt.Errorf("reset incomplete: %d of %d volumes not removed (%s)", len(failed), len(vols), strings.Join(failed, ", "))
		}
		return nil
	})
}
