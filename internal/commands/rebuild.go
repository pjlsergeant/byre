package commands

import (
	"fmt"
	"io"
	"os"

	"byre/internal/project"
	"byre/internal/runner"
)

// Rebuild implements `byre rebuild`: regenerate the build context and rebuild
// the image with the cache disabled (--no-cache), to pick up new upstream
// tool/package versions. Volumes are untouched; the next `byre develop` runs the
// fresh image.
func Rebuild(stdout io.Writer, projectDir string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if err := paths.Bootstrap(); err != nil {
		return err
	}
	cfg, res, err := resolve(paths, projectDir)
	if err != nil {
		return err
	}
	eng, err := runner.Detect(cfg.Engine, nil)
	if err != nil {
		return err
	}
	r := runner.New(eng)
	image := ImageTag(paths.ID, os.Getuid(), os.Getgid())

	return withSetupLock(paths.LockFile, func() error {
		fmt.Fprintf(stdout, "byre: rebuilding %s with --no-cache...\n", image)
		return buildImage(r, paths, cfg, res, image, true)
	})
}
