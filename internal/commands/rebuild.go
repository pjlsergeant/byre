package commands

import (
	"fmt"
	"io"

	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

// Rebuild implements `byre rebuild`: regenerate the build context and rebuild
// the image with the cache disabled (--no-cache), to pick up new upstream
// tool/package versions. Volumes are untouched; the next `byre develop` runs the
// fresh image.
func Rebuild(s Streams, projectDir string) error {
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
	rv, err := resolve(paths, projectDir, s.Err)
	if err != nil {
		return err
	}
	eng, engExe, err := runner.Detect(rv.cfg.Engine, hostexec.Looker(boxWritableRoots(paths)))
	if err != nil {
		return err
	}
	rr := runner.New(eng, engExe)
	// Same mode-select develop applies: the rebuilt image must carry the same
	// identity the next develop will run with.
	ident, err := resolveIdentity(s.Err, rr)
	if err != nil {
		return err
	}
	return rebuild(s.Err, rr, eng, paths, rv, ident)
}

// rebuild is Rebuild's engine-facing core, split out so it can run against a
// fake engine. w gets the progress note (stderr in production).
func rebuild(w io.Writer, r imageRunner, eng runner.Engine, paths project.Paths, rv resolved, ident runner.Identity) error {
	image := imageTag(paths.ID, ident.UID, ident.GID)
	return withSetupLockProject(w, paths, func() error {
		// Re-establish enrollment under the lock, same as develop: a concurrent
		// forget could have cleared the store while rebuild waited.
		if err := requireRecorded(paths); err != nil {
			return err
		}
		// And, same as develop, the config the image is built from is read
		// under the lock the editor's save takes: rebuild's pre-lock read only
		// had to name the engine.
		fresh, err := rv.refresh()
		if err != nil {
			return err
		}
		// The image is only worth building for the engine that will run it:
		// eng, the identity mode behind ident, and the image tag all come from
		// the pre-lock detection, so a save that renames the engine gets the
		// same refusal develop gives rather than a rebuild reported as done
		// for an image the next develop never looks at.
		if err := refuseEngineChangedUnderLock(fresh.cfg, eng, "rebuild"); err != nil {
			return err
		}
		fmt.Fprintf(w, "byre: rebuilding %s with --no-cache...\n", image)
		return buildImageWarn(w, r, paths, fresh.cfg, fresh.skills, image, true, ident)
	})
}
