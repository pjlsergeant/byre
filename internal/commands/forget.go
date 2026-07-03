package commands

import (
	"fmt"
	"io"
	"os"

	"byre/internal/config"
	"byre/internal/project"
	"byre/internal/runner"
)

// forgetRunner is the runner surface byre forget needs (interface for testing).
type forgetRunner interface {
	RunningContainersByLabel(label string) ([]string, error)
	VolumesByPrefix(prefix string) ([]string, error)
	VolumeRemove(name string) error
	ImageExists(tag string) (bool, error)
	ImageRemove(tag string) error
}

// Forget implements `byre forget`: completely remove byre's host-side state for
// the current directory — its named volumes, its image, and its
// ~/.byre/projects/<id>/ dir (which holds the config, the adoption record, and
// the build context). It does NOT touch the project tree; a committed
// <project>/byre.config is yours to keep. Refuses while a session is live; names
// everything before deleting.
func Forget(stdout io.Writer, stdin io.Reader, projectDir string, force bool) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if err := paths.Bootstrap(); err != nil { // ensures the dir+lock exist for the lock
		return err
	}
	engine := "auto"
	if cfg, cerr := config.Load(projectDir); cerr == nil {
		engine = cfg.Engine
	}
	eng, err := runner.Detect(engine, nil)
	if err != nil {
		return err
	}
	return forget(stdout, stdin, paths, runner.New(eng), force)
}

func forget(stdout io.Writer, stdin io.Reader, paths project.Paths, r forgetRunner, force bool) error {
	if live, err := liveSession(r, paths.ID); err != nil {
		return fmt.Errorf("checking for a running session: %w", err)
	} else if len(live) > 0 {
		return fmt.Errorf("a container is running for this project (%s); exit it before forget", shortID(live[0]))
	}

	vols, err := projectVolumes(r, paths.Home, paths.ID)
	if err != nil {
		return err
	}
	// Both the current UID-qualified tag and the legacy unqualified `byre-<id>`
	// tag (a project built before the build-time-UID milestone): forget removes
	// whichever exist so it never leaves an orphaned image behind.
	candidates := []string{ImageTag(paths.ID, os.Getuid(), os.Getgid()), "byre-" + paths.ID}
	var images []string
	for _, img := range candidates {
		has, ierr := r.ImageExists(img)
		if ierr != nil {
			return ierr
		}
		if has {
			images = append(images, img)
		}
	}

	noteSharedVolumes(stdout, paths)
	fmt.Fprintf(stdout, "byre forget will permanently delete for %s:\n", paths.ID)
	for _, v := range vols {
		fmt.Fprintf(stdout, "  - volume %s\n", v)
	}
	for _, img := range images {
		fmt.Fprintf(stdout, "  - image %s\n", img)
	}
	fmt.Fprintf(stdout, "  - %s/  (config, adoption record, build context)\n", paths.Dir)

	if !force {
		fmt.Fprint(stdout, "Proceed? [y/N] ")
		if !confirmed(stdin) {
			fmt.Fprintln(stdout, "aborted.")
			return nil
		}
	}

	// Remove volumes + image under the lock (re-checking live and re-listing, so
	// state created since the preview is also removed); the projects dir holds
	// the lock file, so remove it after the lock is released.
	var failed []string
	if err := withSetupLock(paths.LockFile, func() error {
		if live, lerr := liveSession(r, paths.ID); lerr != nil {
			return fmt.Errorf("checking for a running session: %w", lerr)
		} else if len(live) > 0 {
			return fmt.Errorf("a session started for this project (%s); aborting forget", shortID(live[0]))
		}
		lockedVols, lerr := projectVolumes(r, paths.Home, paths.ID)
		if lerr != nil {
			return lerr
		}
		for _, v := range lockedVols {
			if rerr := r.VolumeRemove(v); rerr != nil {
				fmt.Fprintf(stdout, "byre: FAILED to remove volume %s: %v\n", v, rerr)
				failed = append(failed, v)
			}
		}
		for _, img := range candidates {
			nowImage, ierr := r.ImageExists(img)
			if ierr != nil {
				fmt.Fprintf(stdout, "byre: could not check image %s: %v\n", img, ierr)
				failed = append(failed, img) // unknown -> don't remove local state
			} else if nowImage {
				if rerr := r.ImageRemove(img); rerr != nil {
					fmt.Fprintf(stdout, "byre: FAILED to remove image %s: %v\n", img, rerr)
					failed = append(failed, img)
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// Only remove the host-side project dir once the engine state is fully gone,
	// so a partial failure stays recoverable.
	if len(failed) > 0 {
		fmt.Fprintln(stdout, "byre: engine state not fully removed; leaving the project dir in place.")
		return fmt.Errorf("forget incomplete: %d item(s) not removed (%v)", len(failed), failed)
	}
	if rerr := os.RemoveAll(paths.Dir); rerr != nil {
		return fmt.Errorf("removing %s: %w", paths.Dir, rerr)
	}
	fmt.Fprintf(stdout, "byre: forgot %s\n", paths.ID)
	return nil
}
