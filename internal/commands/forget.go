package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pjlsergeant/byre/internal/project"
)

// Forget implements `byre forget`: completely remove byre's host-side state for
// the current directory — its named volumes, its image, and its
// ~/.byre/projects/<id>/ dir (which holds the config, the adoption record, and
// the build context). "Completely" means every INSTALLED engine is inspected
// and cleaned: state can live in an engine the config no longer names, and
// deleting the store while the other engine still held credentials would be a
// false success. It does NOT touch the project tree; a committed
// <project>/byre.config is yours to keep. Refuses while a session is live;
// names everything before deleting.
func Forget(s Streams, projectDir string, force bool) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if err := paths.Bootstrap(); err != nil { // ensures the dir+lock exist for the lock
		return err
	}
	engines, err := lifecycleEngines()
	if err != nil {
		return err
	}
	return forget(s, paths, engines, force)
}

func forget(s Streams, paths project.Paths, engines []engineRunner, force bool) error {
	multi := len(engines) > 1
	// Both the current UID-qualified tag and the legacy unqualified `byre-<id>`
	// tag (a project built before the build-time-UID milestone): forget removes
	// whichever exist so it never leaves an orphaned image behind.
	candidates := []string{imageTag(paths.ID, os.Getuid(), os.Getgid()), "byre-" + paths.ID}

	// Preview pass: any engine that can't be fully inspected fails the command
	// before anything is deleted — "completely removed" must never be claimed
	// over an engine that couldn't be queried.
	type engineState struct {
		r    engineRunner
		vols []string
		imgs []string
	}
	var states []engineState
	for _, r := range engines {
		if live, err := liveSession(r, paths.ID); err != nil {
			return fmt.Errorf("checking for a running session (%s): %w", r.Engine(), err)
		} else if len(live) > 0 {
			return fmt.Errorf("a session is running for this project (%s%s); exit it before forget", shortID(live[0]), engineSuffix(multi, r))
		}
		st := engineState{r: r}
		var err error
		st.vols, err = projectVolumes(r, paths.Home, paths.ID)
		if err != nil {
			return fmt.Errorf("listing volumes (%s): %w", r.Engine(), err)
		}
		for _, img := range candidates {
			has, ierr := r.ImageExists(img)
			if ierr != nil {
				return fmt.Errorf("checking image %s (%s): %w", img, r.Engine(), ierr)
			}
			if has {
				st.imgs = append(st.imgs, img)
			}
		}
		states = append(states, st)
	}

	noteSharedVolumes(s.Err, paths)
	for _, st := range states {
		noteMachineVolumes(s.Err, st.r, os.Getuid())
	}
	fmt.Fprintf(s.Err, "byre forget will permanently delete for %s:\n", paths.ID)
	for _, st := range states {
		for _, v := range st.vols {
			fmt.Fprintf(s.Err, "  - volume %s%s\n", v, engineSuffix(multi, st.r))
		}
		for _, img := range st.imgs {
			fmt.Fprintf(s.Err, "  - image %s%s\n", img, engineSuffix(multi, st.r))
		}
	}
	fmt.Fprintf(s.Err, "  - %s/  (config, adoption record, build context)\n", paths.Dir)

	if !force {
		fmt.Fprint(s.Err, "Proceed? [y/N] ")
		if !confirmed(s.In) {
			fmt.Fprintln(s.Err, "aborted.")
			return nil
		}
	}

	// Remove volumes + images under the lock (re-checking live and re-listing,
	// so state created since the preview is also removed); the projects dir
	// holds the lock file, so remove it after the lock is released.
	var failed []string
	if err := withSetupLock(s.Err, paths.LockFile, func() error {
		for _, r := range engines {
			// Abort on a session that started since the prompt, and dissolve any
			// pre-start ownership marker before touching volumes.
			if lerr := clearSessionMarkers(s.Err, r, paths.ID); lerr != nil {
				return lerr
			}
			lockedVols, lerr := projectVolumes(r, paths.Home, paths.ID)
			if lerr != nil {
				return fmt.Errorf("listing volumes (%s): %w", r.Engine(), lerr)
			}
			for _, v := range lockedVols {
				if rerr := r.VolumeRemove(v); rerr != nil {
					fmt.Fprintf(s.Err, "byre: FAILED to remove volume %s%s: %v\n", v, engineSuffix(multi, r), rerr)
					failed = append(failed, v)
				}
			}
			for _, img := range candidates {
				nowImage, ierr := r.ImageExists(img)
				if ierr != nil {
					fmt.Fprintf(s.Err, "byre: could not check image %s%s: %v\n", img, engineSuffix(multi, r), ierr)
					failed = append(failed, img) // unknown -> don't remove local state
				} else if nowImage {
					if rerr := r.ImageRemove(img); rerr != nil {
						fmt.Fprintf(s.Err, "byre: FAILED to remove image %s%s: %v\n", img, engineSuffix(multi, r), rerr)
						failed = append(failed, img)
					}
				}
			}
		}
		// The store contents die INSIDE the critical section (only once the
		// engine state is fully gone, so a partial failure stays recoverable)
		// — a develop queued on this lock must never interleave its own store
		// writes with the deletion. Only the dir + lock file survive to the
		// post-release step below.
		if len(failed) == 0 {
			if cerr := clearStoreContents(paths.Dir); cerr != nil {
				return fmt.Errorf("removing store contents: %w", cerr)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if len(failed) > 0 {
		fmt.Fprintln(s.Err, "byre: engine state not fully removed; leaving the project dir in place.")
		return fmt.Errorf("forget incomplete: %d item(s) not removed (%v)", len(failed), failed)
	}
	if rerr := removeEmptiedStore(paths.Dir); rerr != nil {
		return rerr
	}
	fmt.Fprintf(s.Err, "byre: forgot %s\n", paths.ID)
	return nil
}

// clearStoreContents deletes everything in a project store dir EXCEPT the
// lock file — which must survive the critical section it serializes (the
// caller holds it). Running this under the setup lock is the point: a
// develop queued on that lock can't interleave its own store writes with the
// deletion.
func clearStoreContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.Name() == "lock" {
			continue
		}
		if rerr := os.RemoveAll(filepath.Join(dir, e.Name())); rerr != nil {
			return rerr
		}
	}
	return nil
}

// removeEmptiedStore removes the lock file and then the store dir itself —
// NON-recursively — after the lock is released. The contents died under the
// lock (clearStoreContents); if a concurrent byre repopulated the dir in the
// post-release window, the final remove fails and the fresh state survives:
// deleting a dir whose lock we no longer hold must never take new content
// with it.
func removeEmptiedStore(dir string) error {
	_ = os.Remove(filepath.Join(dir, "lock"))
	if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w (recreated by a concurrent byre? its contents were already deleted)", dir, err)
	}
	return nil
}
