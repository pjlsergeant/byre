package commands

import (
	"io"

	"github.com/pjlsergeant/byre/internal/runner"
)

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
	ContainersByLabel(label string) ([]string, error)
	ContainerEnv(id string) (map[string]string, error)
	ContainerLabels(id string) (map[string]string, error)
	NetworkMode(container string) (string, error)
	Stop(container string) error
	Create(args []string) error
	StartAttach(container string) error
	ContainerRemove(container string) error
	NetnsInit(image, container, entrypoint string, env map[string]string, joinUserns bool) error
	// WorktreeAdd runs the one-shot in-box worktree registration container
	// (see runner.Runner.WorktreeAdd): the project image, the box identity,
	// and exactly the three repo binds — every mutating git operation on the
	// repo stays in the box.
	WorktreeAdd(image, name string, id runner.Identity, commonHost, commonTarget, mainDir, target, branch string) error
	// SupportsKeepIDMapping reports whether the engine can do the explicit
	// keep-id userns mapping (rootless Podman path — see resolveIdentity).
	SupportsKeepIDMapping() (bool, error)
	// ProbeSockGroup is the engine-side gid discovery for sock_groups (see
	// runner.Runner.ProbeSockGroup). Used at create time so --group-add
	// matches the gid the box will actually see.
	ProbeSockGroup(image, hostPath, targetPath, userns string) (int, error)
	// IsDockerDesktop softens host-side socket-source warnings (Desktop
	// resolves the bind inside a VM; a missing host path is a false-negative).
	IsDockerDesktop() (bool, error)
	Exec(containerID string, uid, gid int, workdir string, env map[string]string, tty bool, command ...string) error
	ExecInput(containerID string, uid, gid int, stdin io.Reader, command ...string) (string, error)
	ExecOutput(containerID string, uid, gid int, stdout io.Writer, command ...string) error
}

// volumeRunner is the named-volume surface: enumeration, lifecycle, and the
// root-privileged data moves (seeding, migration) that fill volumes.
type volumeRunner interface {
	VolumesByPrefix(prefix string) ([]string, error)
	VolumeExists(name string) (bool, error)
	VolumeCreate(name string) error
	VolumeRemove(name string) error
	SeedVolume(name, hostPath, image string, id runner.Identity) error
	SeedLiteral(volName, destPath, content, image string, id runner.Identity) error
	SeedFiles(volName, srcDir string, files []string, image string, id runner.Identity) error
	MigrateVolume(src, dst, image string, id runner.Identity) error
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
