package commands

import (
	"fmt"
	"io"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
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
	eng, err := runner.Detect(rv.cfg.Engine, nil)
	if err != nil {
		return err
	}
	rr := runner.New(eng)
	// Same mode-select develop applies: the rebuilt image must carry the same
	// identity the next develop will run with.
	ident, err := resolveIdentity(s.Err, rr)
	if err != nil {
		return err
	}
	return rebuild(s.Err, rr, paths, rv.cfg, rv.skills, ident)
}

// rebuild is Rebuild's engine-facing core, split out so it can run against a
// fake engine. w gets the progress note (stderr in production).
func rebuild(w io.Writer, r imageRunner, paths project.Paths, cfg config.Config, res skills.Resolved, ident runner.Identity) error {
	image := imageTag(paths.ID, ident.UID, ident.GID)
	return withSetupLock(w, paths.LockFile, func() error {
		// Re-establish enrollment under the lock, same as develop: a concurrent
		// forget could have cleared the store while rebuild waited.
		if err := requireRecorded(paths); err != nil {
			return err
		}
		fmt.Fprintf(w, "byre: rebuilding %s with --no-cache...\n", image)
		return buildImage(r, paths, cfg, res, image, true, ident)
	})
}
