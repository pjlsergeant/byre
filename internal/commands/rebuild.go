package commands

import (
	"fmt"
	"io"
	"os"

	"byre/internal/config"
	"byre/internal/project"
	"byre/internal/runner"
	"byre/internal/skills"
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
	rv, err := resolve(paths, projectDir)
	if err != nil {
		return err
	}
	eng, err := runner.Detect(rv.cfg.Engine, nil)
	if err != nil {
		return err
	}
	return rebuild(s.Err, runner.New(eng), paths, rv.cfg, rv.skills)
}

// rebuild is Rebuild's engine-facing core, split out so it can run against a
// fake engine. w gets the progress note (stderr in production).
func rebuild(w io.Writer, r imageRunner, paths project.Paths, cfg config.Config, res skills.Resolved) error {
	image := ImageTag(paths.ID, os.Getuid(), os.Getgid())
	return withSetupLock(paths.LockFile, func() error {
		fmt.Fprintf(w, "byre: rebuilding %s with --no-cache...\n", image)
		return buildImage(r, paths, cfg, res, image, true)
	})
}
