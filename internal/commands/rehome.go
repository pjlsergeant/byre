package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"byre/internal/project"
)

// Rehome implements `byre rehome <old-id>`: migrate a previous project's named
// volumes onto the identity of the current directory (after a move/rename, which
// changes the path-derived id). Docker has no volume rename, so each volume is
// copy-then-remove. Refuses while a session is live for either id; pre-checks
// for destination conflicts; removes the old volumes only after every copy
// succeeds.
func Rehome(s Streams, projectDir, oldID string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	// A worktree has no identity of its own to rehome — it inherits the main
	// worktree's. Its path only feeds the container name/label, derived fresh each
	// run, so a moved worktree self-heals. Point at the main tree instead of
	// migrating the shared (inherited) volumes onto a worktree-derived id.
	if paths.IsWorktree {
		return fmt.Errorf("this is a worktree of %s; run rehome from the main worktree if the repo moved (a worktree inherits its identity and needs no rehome)", paths.Canonical)
	}
	if err := paths.Bootstrap(); err != nil {
		return err
	}
	r, err := resolveEngine(s.Err, projectDir)
	if err != nil {
		return err
	}
	return rehome(s, paths, oldID, r, os.Getuid(), os.Getgid())
}

func rehome(s Streams, paths project.Paths, oldID string, r engineRunner, uid, gid int) error {
	newID := paths.ID
	if oldID == newID {
		return fmt.Errorf("already homed here (id %s)", newID)
	}

	// All checks and mutations run under BOTH the old and new projects' setup
	// locks (so a concurrent develop/seed on either id can't race the
	// migration), with the live-session checks re-evaluated inside.
	oldLock := filepath.Join(paths.Home, "projects", oldID, "lock")
	if err := os.MkdirAll(filepath.Dir(oldLock), 0o755); err != nil {
		return err
	}
	return withTwoSetupLocks(s.Err, paths.LockFile, oldLock, func() error {
		for _, id := range []string{oldID, newID} {
			if live, err := r.RunningContainersByLabel(labelKey + "=" + id); err != nil {
				return fmt.Errorf("checking for a running session: %w", err)
			} else if len(live) > 0 {
				return fmt.Errorf("a session is running for %s (%s); exit it before rehome", id, shortID(live[0]))
			}
		}

		oldPrefix := "byre-" + oldID + "-"
		oldVols, err := projectVolumes(r, paths.Home, oldID)
		if err != nil {
			return err
		}
		if len(oldVols) == 0 {
			fmt.Fprintf(s.Err, "byre: no volumes found for old id %s; nothing to migrate\n", oldID)
			return nil
		}

		// An image is needed to run the copy; prefer the old one, else the new one.
		image, err := pickCopyImage(r, oldID, newID, uid, gid)
		if err != nil {
			return err
		}

		// Pre-check all destinations for conflicts before mutating anything.
		type pair struct{ src, dst string }
		var plan []pair
		for _, src := range oldVols {
			dst := "byre-" + newID + "-" + strings.TrimPrefix(src, oldPrefix)
			exists, err := r.VolumeExists(dst)
			if err != nil {
				return err
			}
			if exists {
				return fmt.Errorf("destination volume %s already exists; resolve the conflict (e.g. byre reset) before rehome", dst)
			}
			plan = append(plan, pair{src, dst})
		}

		// Copy each old volume into a fresh destination. On any failure, roll back
		// the destinations created so far and leave the originals intact.
		var created []string
		for _, p := range plan {
			if err := r.VolumeCreate(p.dst); err != nil {
				rollback(r, created)
				return fmt.Errorf("creating %s: %w", p.dst, err)
			}
			created = append(created, p.dst)
			if err := r.MigrateVolume(p.src, p.dst, image, uid, gid); err != nil {
				rollback(r, created)
				return fmt.Errorf("copying %s -> %s: %w", p.src, p.dst, err)
			}
			fmt.Fprintf(s.Err, "byre: migrated %s -> %s\n", p.src, p.dst)
		}

		// All copies succeeded — now remove the originals.
		for _, p := range plan {
			if err := r.VolumeRemove(p.src); err != nil {
				fmt.Fprintf(s.Err, "byre: warning: copied but could not remove old volume %s: %v\n", p.src, err)
			}
		}
		fmt.Fprintf(s.Err, "byre: rehomed %s -> %s. Run `byre develop` to rebuild the image.\n", oldID, newID)
		return nil
	})
}

func pickCopyImage(r imageRunner, oldID, newID string, uid, gid int) (string, error) {
	// Try each id's current (UID-qualified) tag, then its legacy unqualified
	// `byre-<id>` tag — a project built before the build-time-UID milestone still
	// has only that. The copy one-shot (MigrateVolume) bypasses the entrypoint,
	// runs as root, and chowns explicitly, so the image's own baked uid is
	// irrelevant: any byre image for these ids works as the copy vehicle.
	for _, id := range []string{oldID, newID} {
		for _, img := range []string{imageTag(id, uid, gid), "byre-" + id} {
			if ok, err := r.ImageExists(img); err != nil {
				return "", err
			} else if ok {
				return img, nil
			}
		}
	}
	return "", fmt.Errorf("no byre image exists to run the volume copy; run `byre develop` first")
}

func rollback(r volumeRunner, created []string) {
	for _, v := range created {
		_ = r.VolumeRemove(v)
	}
}
