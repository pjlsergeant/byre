package commands

import "byre/internal/runner"

// The engine surface the commands consume, sliced into three composable
// interfaces (satisfied by *runner.Runner; tests inject fakeRunner). A command
// that stays within one slice takes that slice; a command that crosses slices
// (reset, forget, rehome, develop) takes engineRunner rather than growing a
// bespoke per-command interface.

// sessionRunner is the container-session surface: engine identity, finding and
// inspecting live sessions, and starting or entering them.
type sessionRunner interface {
	Engine() runner.Engine
	IsRootlessPodman() (bool, error)
	RunningContainersByLabel(label string) ([]string, error)
	ContainerEnv(id string) (map[string]string, error)
	NetworkMode(container string) (string, error)
	Stop(container string) error
	Run(args []string) error
	NetnsInit(image, container, entrypoint string, env map[string]string) error
	Exec(containerID string, uid, gid int, workdir string, env map[string]string, tty bool, command ...string) error
}

// volumeRunner is the named-volume surface: enumeration, lifecycle, and the
// root-privileged data moves (seeding, migration) that fill volumes.
type volumeRunner interface {
	VolumesByPrefix(prefix string) ([]string, error)
	VolumeExists(name string) (bool, error)
	VolumeCreate(name string) error
	VolumeRemove(name string) error
	SeedVolume(name, hostPath, image string, uid, gid int) error
	SeedLiteral(volName, destPath, content, image string, uid, gid int) error
	SeedFiles(volName, srcDir string, files []string, image string, uid, gid int) error
	MigrateVolume(src, dst, image string, uid, gid int) error
}

// imageRunner is the image surface: build and image lifecycle.
type imageRunner interface {
	Build(tag, dockerfile, contextDir string, noCache bool, buildArgs []string) error
	ImageExists(tag string) (bool, error)
	ImageRemove(tag string) error
}

// engineRunner is the full engine surface.
type engineRunner interface {
	sessionRunner
	volumeRunner
	imageRunner
}

var _ engineRunner = (*runner.Runner)(nil)
