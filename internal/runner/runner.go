// Package runner drives a container engine (Docker or Podman) via its CLI.
//
// byre shells out to the engine CLI rather than binding the Docker SDK, which
// keeps Docker and Podman as two implementations of the same small surface.
// The Runner stays minimal and grows only as commands need build/run/volume
// operations (M3+).
package runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Engine is a supported container engine.
type Engine string

const (
	Docker Engine = "docker"
	Podman Engine = "podman"
)

// LookPath mirrors exec.LookPath; injectable so engine detection is testable
// without a real engine installed.
type LookPath func(string) (string, error)

// Detect resolves which engine to use from the config setting ("auto",
// "docker", or "podman"). With "auto" it prefers docker, then podman. look is
// exec.LookPath in production; tests inject a fake.
func Detect(setting string, look LookPath) (Engine, error) {
	if look == nil {
		look = exec.LookPath
	}
	switch setting {
	case "", "auto":
		for _, e := range []Engine{Docker, Podman} {
			if _, err := look(string(e)); err == nil {
				return e, nil
			}
		}
		return "", fmt.Errorf("no container engine found on PATH (looked for docker, podman)")
	case string(Docker), string(Podman):
		if _, err := look(setting); err != nil {
			return "", fmt.Errorf("engine %q not found on PATH", setting)
		}
		return Engine(setting), nil
	default:
		return "", fmt.Errorf("unknown engine %q (want auto|docker|podman)", setting)
	}
}

// Runner invokes a container engine via its CLI. The three exec seams are
// injectable so command assembly can be unit-tested without a real engine:
// stream connects child stdio (interactive build/run/exec); capture returns
// stdout (ps/inspect); streamIn is stream with a caller-supplied stdin
// (piping literal content into a container).
type Runner struct {
	engine   Engine
	stream   func(name string, args ...string) error
	capture  func(name string, args ...string) (string, error)
	streamIn func(stdin io.Reader, name string, args ...string) error
}

// New returns a Runner for the given engine using real exec.
func New(e Engine) *Runner {
	return &Runner{
		engine:   e,
		stream:   streamExec,
		capture:  captureExec,
		streamIn: streamInExec,
	}
}

// Engine reports the engine this runner invokes.
func (r *Runner) Engine() Engine { return r.engine }

// IsRootlessPodman reports whether this runner drives Podman in ROOTLESS mode.
// It matters because byre bakes the host UID/GID into the image at build time so
// the in-container uid equals the uid on disk — which assumes a ROOTFUL daemon.
// Rootless Podman runs in a user namespace where host↔container uids are
// remapped, so the baked uid no longer matches what lands on disk and files end
// up owned by the wrong id. v0 supports rootful Docker/Podman only; callers use
// this to WARN (not block). The rootless fix (a generic-uid image + keep-id) is a
// sequenced follow-up. Docker — including rootless Docker — is out of scope here
// and reports false. A query error is returned so the caller can stay quiet
// rather than warn on a guess.
func (r *Runner) IsRootlessPodman() (bool, error) {
	if r.engine != Podman {
		return false, nil
	}
	out, err := r.capture(string(r.engine), "info", "--format", "{{.Host.Security.Rootless}}")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
}

// Build builds the image tagged tag from the given context directory and
// Dockerfile. With noCache, the build cache is disabled (--no-cache). buildArgs
// are "KEY=VALUE" pairs passed as --build-arg (byre uses these to bake the host
// UID/GID into the image); pass nil for none.
func (r *Runner) Build(tag, dockerfile, contextDir string, noCache bool, buildArgs []string) error {
	args := []string{"build", "-t", tag, "-f", dockerfile}
	if noCache {
		args = append(args, "--no-cache")
	}
	for _, a := range buildArgs {
		args = append(args, "--build-arg", a)
	}
	return r.stream(string(r.engine), append(args, contextDir)...)
}

// Run runs a container from the assembled run argv.
func (r *Runner) Run(args []string) error {
	return r.stream(string(r.engine), args...)
}

// RunningContainersByLabel returns the ids of running containers carrying label
// ("key=value"). Normally at most one (the container name enforces uniqueness),
// but callers handle the list explicitly.
func (r *Runner) RunningContainersByLabel(label string) ([]string, error) {
	out, err := r.capture(string(r.engine), "ps", "-q", "--filter", "label="+label)
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

// ContainerEnv returns a running container's configured environment (image ENV
// plus the `-e` vars set at run time), so callers can act on the identity/env
// the session ACTUALLY started with rather than re-deriving it.
func (r *Runner) ContainerEnv(id string) (map[string]string, error) {
	out, err := r.capture(string(r.engine), "inspect", "-f", "{{range .Config.Env}}{{println .}}{{end}}", id)
	if err != nil {
		return nil, err
	}
	return parseEnvLines(out), nil
}

// NetworkMode returns a container's network mode as the engine reports it
// (HostConfig.NetworkMode): "host", "container:<id>", "none", or a private
// network ("default"/"bridge"/a network name). Callers that mutate a
// container's network namespace (NetnsInit) use this to establish the
// namespace is actually the container's own before touching it.
func (r *Runner) NetworkMode(container string) (string, error) {
	out, err := r.capture(string(r.engine), "inspect", "-f", "{{.HostConfig.NetworkMode}}", container)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Stop stops a running container (short grace period, then SIGKILL). Used when
// byre must actively end a session it cannot let run — e.g. netns hooks were
// refused and the launch gate can't be trusted to fail the launch closed.
func (r *Runner) Stop(container string) error {
	_, err := r.capture(string(r.engine), "stop", "-t", "2", container)
	return err
}

// parseEnvLines parses newline-separated KEY=VALUE lines into a map (pure, for
// testing). Lines without '=' (or with an empty key) are skipped.
func parseEnvLines(out string) map[string]string {
	env := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if i := strings.IndexByte(line, '='); i > 0 {
			env[line[:i]] = line[i+1:]
		}
	}
	return env
}

// Exec runs an interactive command in a running container as the given uid:gid,
// in workdir, with env. Used by `byre shell` — running as the dev uid (not root)
// and re-passing the run-time skill env so claude/codex find their config. tty
// mirrors RunParams.TTY: pass -t only when stdin is an actual terminal, so a
// non-TTY caller (CI, a script piping into byre) doesn't hit "the input device
// is not a TTY".
func (r *Runner) Exec(containerID string, uid, gid int, workdir string, env map[string]string, tty bool, command ...string) error {
	return r.stream(string(r.engine), execArgs(containerID, uid, gid, workdir, env, tty, command...)...)
}

// execArgs builds the engine `exec` argv (pure, for testing). Env keys are
// sorted so the argument order is deterministic.
func execArgs(containerID string, uid, gid int, workdir string, env map[string]string, tty bool, command ...string) []string {
	args := []string{"exec", "-i"}
	if tty {
		args = append(args, "-t")
	}
	args = append(args, "-u", fmt.Sprintf("%d:%d", uid, gid))
	if workdir != "" {
		args = append(args, "-w", workdir)
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}
	args = append(args, containerID)
	return append(args, command...)
}

// NetnsInit runs a skill's declared netns-init entrypoint in the target
// container's network namespace: a run-to-completion helper container sharing
// ONLY the netns (not fs, not pid), as root with CAP_NET_ADMIN — the one
// place that capability exists; the box itself never gets it. image is the
// box's own image (the skill baked its tooling there; inert to the capless
// agent inside). env is the box's resolved runtime env, re-passed so the
// helper sees the same configuration (e.g. an allowlist extension var).
//
// Output is captured, not streamed: the helper runs concurrently with the
// box's interactive `run`, so it must not contend for the TTY. On failure the
// engine's stderr is folded into the error; on success the launch gate
// opening is the signal, not text.
func (r *Runner) NetnsInit(image, container, entrypoint string, env map[string]string) error {
	_, err := r.capture(string(r.engine), netnsInitArgs(image, container, entrypoint, env)...)
	return err
}

// netnsInitArgs builds the netns-init helper argv (pure, for testing). Env
// keys are sorted for deterministic argument order.
func netnsInitArgs(image, container, entrypoint string, env map[string]string) []string {
	args := []string{"run", "--rm",
		"-u", "0:0",
		"--net", "container:" + container,
		"--cap-add", "NET_ADMIN",
		"--entrypoint", entrypoint,
	}
	for _, k := range sortedKeys(env) {
		args = append(args, "-e", k+"="+env[k])
	}
	return append(args, image)
}

// VolumeExists reports whether a named volume exists.
func (r *Runner) VolumeExists(name string) (bool, error) {
	out, err := r.capture(string(r.engine), "volume", "ls", "-q", "--filter", "name=^"+name+"$")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

// VolumeCreate creates a named volume.
func (r *Runner) VolumeCreate(name string) error {
	_, err := r.capture(string(r.engine), "volume", "create", name)
	return err
}

// ImageExists reports whether an image with the given tag exists locally.
func (r *Runner) ImageExists(tag string) (bool, error) {
	out, err := r.capture(string(r.engine), "images", "-q", tag)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// ImageRemove removes an image by tag.
func (r *Runner) ImageRemove(tag string) error {
	_, err := r.capture(string(r.engine), "image", "rm", tag)
	return err
}

// MigrateVolume copies the contents of src into dst (which must already exist),
// chowning to uid:gid. Used by rehome (Docker has no volume rename). image
// supplies cp/chown; the entrypoint is bypassed and it runs as root.
func (r *Runner) MigrateVolume(src, dst, image string, uid, gid int) error {
	script := fmt.Sprintf("cp -a /from/. /to/ && chown -R %d:%d /to", uid, gid)
	return r.stream(string(r.engine), "run", "--rm",
		"--entrypoint", "sh", "-u", "0:0",
		"--mount", "type=volume,source="+src+",target=/from,readonly",
		"--mount", "type=volume,source="+dst+",target=/to",
		image, "-c", script)
}

// VolumesByPrefix lists existing volume names beginning with prefix.
func (r *Runner) VolumesByPrefix(prefix string) ([]string, error) {
	out, err := r.capture(string(r.engine), "volume", "ls", "-q", "--filter", "name="+prefix)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		// docker's name filter is a substring match, so confirm the prefix.
		if n := strings.TrimSpace(line); strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}
	return names, nil
}

// VolumeRemove removes a named volume.
func (r *Runner) VolumeRemove(name string) error {
	_, err := r.capture(string(r.engine), "volume", "rm", name)
	return err
}

// SeedVolume copies hostPath into a fresh named volume (a one-way copy — the
// volume diverges immediately) and chowns it to uid:gid so credential writes
// succeed regardless of the runtime UID mapping. image supplies cp/chown.
//
// It overrides the image ENTRYPOINT (the byre launcher) and runs as root, since
// a fresh Docker volume is root-owned and the cp/chown must run privileged.
func (r *Runner) SeedVolume(name, hostPath, image string, uid, gid int) error {
	script := fmt.Sprintf("cp -a /src/. /dest/ && chown -R %d:%d /dest", uid, gid)
	return r.stream(string(r.engine), "run", "--rm",
		"--entrypoint", "sh", "-u", "0:0",
		"--mount", "type=volume,source="+name+",target=/dest",
		"--mount", "type=bind,source="+hostPath+",target=/src,readonly",
		image, "-c", script)
}

// SeedLiteral writes content to destPath inside a fresh named volume (creating
// parent dirs) and chowns the volume to uid:gid. The content is piped via stdin
// and destPath via an env var, so neither can inject shell. Runs as root with
// the image entrypoint bypassed.
func (r *Runner) SeedLiteral(volName, destPath, content, image string, uid, gid int) error {
	script := fmt.Sprintf(`mkdir -p "/dest/$(dirname "$BYRE_DEST")" && cat > "/dest/$BYRE_DEST" && chown -R %d:%d /dest`, uid, gid)
	args := []string{"run", "--rm", "-i",
		"--entrypoint", "sh", "-u", "0:0",
		"-e", "BYRE_DEST=" + destPath,
		"--mount", "type=volume,source=" + volName + ",target=/dest",
		image, "-c", script}
	return r.streamIn(strings.NewReader(content), string(r.engine), args...)
}

// SeedFiles copies a curated subset of srcDir (the relative paths in files,
// each a file or dir) into a fresh named volume at the SAME relative location,
// then chowns the volume to uid:gid. Used to seed an agent's non-secret prefs
// (theme, keybindings) into a fresh state volume. Like SeedVolume it overrides
// the entrypoint and runs as root (a fresh volume is root-owned).
//
// The file list is passed as positional ARGV (never interpolated into the
// script), so a path can't inject shell. A listed path missing in srcDir is
// skipped, not an error (the host may simply not have that pref yet).
func (r *Runner) SeedFiles(volName, srcDir string, files []string, image string, uid, gid int) error {
	// set -e so a failed mkdir/cp aborts with non-zero (the trailing chown must
	// not mask a copy failure — the caller's rollback depends on the exit status).
	// A listed path missing in /src is skipped via the [ -e ] guard, not a failure.
	const script = `set -e
for f in "$@"; do
  if [ -e "/src/$f" ]; then
    mkdir -p "/dest/$(dirname "$f")"
    cp -a "/src/$f" "/dest/$f"
  fi
done
chown -R "$BYRE_OWNER" /dest`
	args := []string{"run", "--rm",
		"--entrypoint", "sh", "-u", "0:0",
		"-e", fmt.Sprintf("BYRE_OWNER=%d:%d", uid, gid),
		"--mount", "type=volume,source=" + volName + ",target=/dest",
		"--mount", "type=bind,source=" + srcDir + ",target=/src,readonly",
		image, "-c", script, "seed-prefs"}
	args = append(args, files...)
	return r.stream(string(r.engine), args...)
}

func streamExec(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func streamInExec(stdin io.Reader, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = stdin
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func captureExec(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// Surface the engine's stderr — otherwise failures are just "exit status 1".
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return string(out), fmt.Errorf("%s: %s", err, msg)
		}
	}
	return string(out), err
}
