package runner

import (
	"fmt"
	"io"
	"maps"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Helper describes one run-to-completion helper container of the kind the
// backup and restore verbs run: the capture that archives a volume, the pour
// that fills one, and the preflight that proves an image can run those
// commands at all. The flag list the runner builds from it is CLOSED and
// byre's own -- no run_args reach it (ADR 0006) -- and every field here is
// something the caller must pin for the helper to mean what it says:
//
//   - Labels carry byre.helper=<project id> and byre.helper.run=<run id>, so a
//     verb can force-remove exactly ITS helpers on failure.
//   - Env is pinned over the image's own ENV: a built image carries config-
//     authored ENV lines, and TAR_OPTIONS or TAPE set there would change
//     what tar does with no trace in byre's argv.
//   - Volume, when set, is mounted at MountPath with copy-up disabled
//     (`-v <volume>:<path>:nocopy`): read-only alone does not stop the engine
//     populating an empty volume from the helper image before mounting it, and
//     a poured volume must hold the backup's bytes and nothing from the helper
//     image.
//   - No network, no host bind, entrypoint overridden to sh, -u 0:0 in the
//     identity's userns. Root in the box's own userns for a one-shot that
//     exits before any session is ADR 0008's amended scope, which seeding
//     shares; the closed no-bind, no-network argv is the capture's and the
//     pour's alone -- a host-path seed binds its declared source read-only
//     and runs on the engine's default network.
type Helper struct {
	Image     string
	Identity  Identity
	Labels    []string          // "key=value" container labels
	Env       map[string]string // pinned environment, sorted into argv
	Volume    string            // named volume to mount; "" for none
	MountPath string            // where Volume mounts (and the preflight's scratch root)
	ReadOnly  bool              // mount Volume read-only (the capture)
	Script    string            // `sh -c` body
}

// RunHelper runs h to completion: stdin feeds the script, stdout streams to
// the caller (a capture's archive bytes), and the helper's stderr is captured
// (capped) and returned so the caller can print it through the terminal-data
// funnel -- tar's own "socket ignored" lines are data the backup summary
// shows, never byre's voice. A nonzero exit is the error, with the captured
// stderr folded in. The run is synchronous: the container has exited (and,
// with --rm, is gone) when this returns.
func (r *Runner) RunHelper(h Helper, stdin io.Reader, stdout io.Writer) (string, error) {
	return r.pipe(stdin, stdout, r.bin(), helperArgs(r.engine, h)...)
}

// helperArgs builds the helper argv (pure, for testing). Podman additionally
// gets --image-volume=ignore: an image-declared VOLUME becomes an anonymous
// mount on Docker (which cannot land under a per-run mount path) but an
// IMAGE volume on Podman, and the helper must never see one over or under
// its mount. Docker has no such flag.
func helperArgs(e Engine, h Helper) []string {
	args := []string{"run", "--rm", "-i",
		"--entrypoint", "sh", "-u", "0:0",
		"--network", "none"}
	args = appendUserns(args, h.Identity.Userns())
	if e == Podman {
		args = append(args, "--image-volume=ignore")
	}
	for _, l := range h.Labels {
		args = append(args, "--label", l)
	}
	for _, k := range slices.Sorted(maps.Keys(h.Env)) {
		args = append(args, "-e", k+"="+h.Env[k])
	}
	if h.Volume != "" {
		// The `-v` form, not `--mount`: Podman's --mount rejects
		// `volume-nocopy` outright ("invalid mount option") while both engines
		// take `nocopy` as a `-v` option, so one spelling serves both. The
		// source is always a byre volume NAME, never a path, which is what
		// keeps the `-v` form from ever meaning a bind.
		opts := "nocopy"
		if h.ReadOnly {
			opts = "ro,nocopy"
		}
		args = append(args, "-v", h.Volume+":"+h.MountPath+":"+opts)
	}
	return append(args, h.Image, "-c", h.Script)
}

// ImagePull pulls image, streaming the engine's progress. Nothing in the tree
// pulled before this: the engine CLIs pull implicitly on run and build, which
// is engine behaviour, and a verb that must PROVE an image before writing
// any state cannot ride an implicit pull inside the proof itself.
func (r *Runner) ImagePull(image string) error {
	return r.stream(r.bin(), "pull", image)
}

// CleanupTimeout bounds the two cleanup calls below. They run on a path that
// has already failed, and a daemon that stopped answering is one of the
// failures that bring a verb here -- an unbounded `rm -f` would turn the
// cleanup into the hang the verb is trying to report.
//
// Exported because it bounds the cleanup PATH, not just these two calls: a
// cancelled backup or restore ends its synchronous helper by removing the
// helper's container and then waiting for RunHelper to return, and that wait
// gets the same deadline -- an engine that never finished the `rm -f` is the
// same engine that will never end the helper.
const CleanupTimeout = 30 * time.Second

// ContainerForceRemove removes a container whether or not it is running
// (`rm -f`), under CleanupTimeout. Only the backup and restore verbs call it,
// and only on helpers carrying THEIR run id: a session container is never
// force-removed by byre (ContainerRemove stays forceless for that reason).
func (r *Runner) ContainerForceRemove(container string) error {
	_, err := r.captureBounded(CleanupTimeout, r.bin(), "rm", "-f", container)
	return err
}

// ContainersByLabelBounded is ContainersByLabel (any state) under
// CleanupTimeout, for the cleanup path's lookup of a verb's own helpers.
func (r *Runner) ContainersByLabelBounded(label string) ([]string, error) {
	out, err := r.captureBounded(CleanupTimeout, r.bin(), "ps", "-q", "-a", "--filter", "label="+label)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		if id := strings.TrimSpace(line); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// VolumeExistsBounded is VolumeExists under CleanupTimeout, and
// VolumeRemoveBounded is VolumeRemove: the pair a rollback uses to take away
// the volume it was filling. The plain forms stay unbounded -- a develop's own
// volume work waits on the engine as long as the engine needs -- but a rollback
// runs after the failure that brought the verb here, often an engine that
// stopped answering, and it runs holding the setup lock with the config
// removal, the summary and the lock release still to come. Unbounded there,
// the rollback becomes the hang it was reporting.
func (r *Runner) VolumeExistsBounded(name string) (bool, error) {
	out, err := r.captureBounded(CleanupTimeout, r.bin(), volumeLsArgs(name)...)
	if err != nil {
		return false, err
	}
	return volumeListed(out, name), nil
}

// VolumeRemoveBounded is VolumeRemove under CleanupTimeout (see
// VolumeExistsBounded).
func (r *Runner) VolumeRemoveBounded(name string) error {
	_, err := r.captureBounded(CleanupTimeout, r.bin(), "volume", "rm", name)
	return err
}

// pipeExec is the exec seam behind RunHelper: caller-supplied stdin AND
// stdout, stderr captured under the same cap every other captured stderr
// gets. Its own process group with a group kill on a capped stderr is not
// needed here -- stderr is a capBuffer that never blocks the writer -- but
// WaitDelay still bounds a descendant holding the pipes past the child's
// exit.
func pipeExec(stdin io.Reader, stdout io.Writer, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = waitDelay
	stderr := &capBuffer{max: 64 << 10}
	cmd.Stderr = stderr
	err := cmd.Run()
	// capped, not String: a helper whose stderr outgrew the cap comes back with
	// the marker line among tar's own, so the verb that prints them says the
	// list is partial instead of showing a cut-off one as whole.
	msg := stderr.capped()
	if err != nil {
		if msg != "" {
			return msg, fmt.Errorf("%s: %s", err, msg)
		}
		return msg, err
	}
	return msg, nil
}
