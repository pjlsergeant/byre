// Package runner drives a container engine (Docker or Podman) via its CLI.
//
// byre shells out to the engine CLI rather than binding the Docker SDK, which
// keeps Docker and Podman as two implementations of the same small surface.
// The Runner stays minimal and grows only as commands need build/run/volume
// operations.
package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
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

// Runner invokes a container engine via its CLI. The exec seams are
// injectable so command assembly can be unit-tested without a real engine:
// stream connects child stdio (interactive build/run/exec); capture returns
// stdout (ps/inspect); streamIn is stream with a caller-supplied stdin
// (piping literal content into a container); captureIn is capture with a
// caller-supplied stdin (streaming content in AND reading a result back);
// streamOut connects child stdout to a caller-supplied writer (streaming
// arbitrary content OUT of a container, too big or too binary to capture).
type Runner struct {
	engine    Engine
	stream    func(name string, args ...string) error
	capture   func(name string, args ...string) (string, error)
	streamIn  func(stdin io.Reader, name string, args ...string) error
	captureIn func(stdin io.Reader, name string, args ...string) (string, error)
	streamOut func(stdout io.Writer, name string, args ...string) error
}

// New returns a Runner for the given engine using real exec.
func New(e Engine) *Runner {
	return &Runner{
		engine:    e,
		stream:    streamExec,
		capture:   captureExec,
		streamIn:  streamInExec,
		captureIn: captureInExec,
		streamOut: streamOutExec,
	}
}

// Engine reports the engine this runner invokes.
func (r *Runner) Engine() Engine { return r.engine }

// IsRootlessPodman reports whether this runner drives Podman in ROOTLESS mode.
// It is the identity mode-select's pivot (ADR 0032): rootful engines bake the
// host UID/GID into the image (in-container uid == uid on disk), while
// rootless Podman remaps user namespaces, so byre switches to the generic-uid
// image + keep-id mapping there (SupportsKeepIDMapping gates the fallback
// refusal for pre-4.3 Podman). Docker — including rootless Docker — is out of
// scope here and reports false. A query error is returned so the caller can
// stay quiet rather than act on a guess.
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

// Create creates (without starting) a container from the assembled create
// argv (CreateArgs). The container name is the handle for the StartAttach
// that follows; a name conflict surfaces here, in the engine's stderr.
// Output is captured (create prints the id), not streamed.
func (r *Runner) Create(args []string) error {
	_, err := r.capture(string(r.engine), args...)
	return err
}

// StartAttach starts a created container in the foreground: attached, with
// stdin open (the -i/-t attach shape was fixed at create time). The exit
// status is the container's own, like `docker run`'s — but unlike run, an
// engine-level start failure exits 1, not the reserved 125-127 band, so
// callers can't fully distinguish it from an agent exit 1; the engine's
// stderr (streamed) names the cause.
func (r *Runner) StartAttach(container string) error {
	return r.stream(string(r.engine), "start", "--attach", "--interactive", container)
}

// RunningContainersByLabel returns the ids of running containers carrying label
// ("key=value"). Normally at most one (the container name enforces uniqueness),
// but callers handle the list explicitly.
func (r *Runner) RunningContainersByLabel(label string) ([]string, error) {
	return r.containersByLabel(label, false)
}

// ContainersByLabel is RunningContainersByLabel over containers in ANY state
// (created/exited/running). Lifecycle commands use it to see a develop that
// has created its container under the setup lock but not yet started it —
// the pre-start ownership marker (see commands' clearSessionMarkers).
func (r *Runner) ContainersByLabel(label string) ([]string, error) {
	return r.containersByLabel(label, true)
}

func (r *Runner) containersByLabel(label string, all bool) ([]string, error) {
	args := []string{"ps", "-q", "--filter", "label=" + label}
	if all {
		args = append(args, "-a")
	}
	out, err := r.capture(string(r.engine), args...)
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

// ContainerRemove removes a container — deliberately WITHOUT force, so it can
// only ever remove a container that isn't running. Lifecycle commands rely on
// that: removing a pre-start marker succeeds, while a session that started in
// the meantime makes the removal fail and the caller abort.
func (r *Runner) ContainerRemove(container string) error {
	_, err := r.capture(string(r.engine), "rm", container)
	return err
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

// ContainerLabels returns a running container's labels, so callers can read
// the identity byre stamped at run time (byre.project / byre.workdir) off the
// container itself rather than re-deriving it from host state.
func (r *Runner) ContainerLabels(id string) (map[string]string, error) {
	out, err := r.capture(string(r.engine), "inspect", "-f", "{{json .Config.Labels}}", id)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" || trimmed == "null" {
		return labels, nil
	}
	if err := json.Unmarshal([]byte(trimmed), &labels); err != nil {
		return nil, fmt.Errorf("parsing container labels: %w", err)
	}
	return labels, nil
}

// ExecInput runs a non-interactive command in a running container as the given
// uid:gid, feeding it stdin and capturing its stdout — deliver's exec-stream
// transport (content goes in, the landed in-box path comes back). No -t (never
// a terminal), no -w. HOME is set like Exec's callers set it (the launcher
// exports it at run time, so `exec` doesn't inherit it — ADR 0021's attach
// model is byre shell's, HOME included); /home/dev is the chassis dev home.
func (r *Runner) ExecInput(containerID string, uid, gid int, stdin io.Reader, command ...string) (string, error) {
	return r.captureIn(stdin, string(r.engine), execInputArgs(containerID, uid, gid, command...)...)
}

// execInputArgs builds the engine `exec -i` argv (pure, for testing).
func execInputArgs(containerID string, uid, gid int, command ...string) []string {
	args := []string{"exec", "-i", "-u", fmt.Sprintf("%d:%d", uid, gid), "-e", "HOME=/home/dev", containerID}
	return append(args, command...)
}

// ExecOutput runs a non-interactive command in a running container as the given
// uid:gid, streaming its stdout to the given writer — grab's exec-stream
// transport (the mirror of ExecInput: content comes out instead of going in).
// Same attach model: no -t, no -w, HOME set explicitly. Stdout is streamed
// rather than captured because grabbed content is arbitrary in size and shape;
// stderr is captured and surfaces in the error.
func (r *Runner) ExecOutput(containerID string, uid, gid int, stdout io.Writer, command ...string) error {
	return r.streamOut(stdout, string(r.engine), execInputArgs(containerID, uid, gid, command...)...)
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
// joinUserns joins the BOX's user namespace too (keep-id mode) — set when the
// box runs under a non-default userns, since NET_ADMIN over its netns only
// exists inside the userns that owns it.
//
// Output is captured, not streamed: the helper runs concurrently with the
// box's interactive `run`, so it must not contend for the TTY. On failure the
// engine's stderr is folded into the error; on success the launch gate
// opening is the signal, not text.
func (r *Runner) NetnsInit(image, container, entrypoint string, env map[string]string, joinUserns bool) error {
	_, err := r.capture(string(r.engine), netnsInitArgs(image, container, entrypoint, env, joinUserns)...)
	return err
}

// ProbeSockGroup discovers the gid the box will see on targetPath by running a
// one-shot probe container with the same bind the box will get. Engine-side
// for every case (Docker Desktop's VM and remote contexts split host/VM, so a
// host-side stat can report a gid the in-container socket does not carry).
// image is the box's own just-built image (has core tools; entrypoint bypassed).
// userns is the box's own --userns value (Identity.Userns; empty = none): a
// gid probed under a different mapping would not be the gid the box sees.
// Returns the numeric gid; a probe failure is returned to the caller for
// attributed warning -- never silently defaulted.
func (r *Runner) ProbeSockGroup(image, hostPath, targetPath, userns string) (int, error) {
	out, err := r.capture(string(r.engine), probeSockGroupArgs(image, hostPath, targetPath, userns)...)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(out)
	gid, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("probe returned non-numeric gid %q: %w", s, err)
	}
	return gid, nil
}

// probeSockGroupArgs builds the engine-side gid probe argv (pure, for testing).
// --entrypoint bypasses the box launcher; --user 0 so the probe can read any
// socket mode; the bind matches the box's own mount, and so does the userns
// mapping (gid numbers are only comparable inside one mapping).
func probeSockGroupArgs(image, hostPath, targetPath, userns string) []string {
	args := []string{
		"run", "--rm",
		"--user", "0:0",
		"--entrypoint", "stat",
		"--mount", fmt.Sprintf("type=bind,source=%s,target=%s", hostPath, targetPath),
	}
	args = appendUserns(args, userns)
	return append(args,
		image,
		"-c", "%g", targetPath,
	)
}

// IsDockerDesktop reports whether the engine is Docker Desktop (macOS, Windows,
// or Desktop-for-Linux). Used to soften host-side socket-source warnings: under
// Desktop the bind resolves inside the VM, so a missing host path is a
// false-negative, not a real failure. A query error returns false, nil-ish
// via the error so callers can stay quiet rather than warn on a guess.
func (r *Runner) IsDockerDesktop() (bool, error) {
	if r.engine != Docker {
		return false, nil
	}
	// OperatingSystem is "Docker Desktop" on Desktop; native Linux reports the
	// host OS (e.g. "Debian GNU/Linux ..."). Name alone is unreliable.
	out, err := r.capture(string(r.engine), "info", "--format", "{{.OperatingSystem}}")
	if err != nil {
		return false, err
	}
	return strings.Contains(strings.ToLower(out), "docker desktop"), nil
}

// netnsInitArgs builds the netns-init helper argv (pure, for testing). Env
// keys are sorted for deterministic argument order. With joinUserns the
// helper joins the box's own user namespace (--userns=container:<box>) — not
// a fresh identical mapping: a netns is owned by the userns that created it,
// and CAP_NET_ADMIN over it only exists inside that owner, so a sibling
// namespace (even byte-identical) gets EPERM from iptables.
func netnsInitArgs(image, container, entrypoint string, env map[string]string, joinUserns bool) []string {
	args := []string{"run", "--rm",
		"-u", "0:0",
		"--net", "container:" + container,
		"--cap-add", "NET_ADMIN",
		"--entrypoint", entrypoint,
	}
	if joinUserns {
		args = appendUserns(args, "container:"+container)
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
// chowning to the box identity. Used by rehome (Docker has no volume rename).
// image supplies cp/chown; the entrypoint is bypassed and it runs as root, in
// the box's own userns mapping when the identity carries one.
func (r *Runner) MigrateVolume(src, dst, image string, id Identity) error {
	script := fmt.Sprintf("cp -a /from/. /to/ && chown -R %d:%d /to", id.UID, id.GID)
	args := []string{"run", "--rm",
		"--entrypoint", "sh", "-u", "0:0"}
	args = appendUserns(args, id.Userns())
	args = append(args,
		"--mount", "type=volume,source="+src+",target=/from,readonly",
		"--mount", "type=volume,source="+dst+",target=/to",
		image, "-c", script)
	return r.stream(string(r.engine), args...)
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
// volume diverges immediately) and chowns it to the box identity so
// credential writes succeed regardless of the runtime UID mapping. image
// supplies cp/chown.
//
// It overrides the image ENTRYPOINT (the byre launcher) and runs as root —
// in the box's own userns mapping when the identity carries one, so the
// chown target means what it will mean to the box — since a fresh volume is
// root-owned and the cp/chown must run privileged.
func (r *Runner) SeedVolume(name, hostPath, image string, id Identity) error {
	script := fmt.Sprintf("cp -a /src/. /dest/ && chown -R %d:%d /dest", id.UID, id.GID)
	args := []string{"run", "--rm",
		"--entrypoint", "sh", "-u", "0:0"}
	args = appendUserns(args, id.Userns())
	args = append(args,
		"--mount", "type=volume,source="+name+",target=/dest",
		"--mount", "type=bind,source="+hostPath+",target=/src,readonly",
		image, "-c", script)
	return r.stream(string(r.engine), args...)
}

// SeedLiteral writes content to destPath inside a fresh named volume (creating
// parent dirs) and chowns the volume to the box identity. The content is piped
// via stdin and destPath via an env var, so neither can inject shell. Runs as
// root with the image entrypoint bypassed, in the box's own userns mapping
// when the identity carries one.
func (r *Runner) SeedLiteral(volName, destPath, content, image string, id Identity) error {
	script := fmt.Sprintf(`mkdir -p "/dest/$(dirname "$BYRE_DEST")" && cat > "/dest/$BYRE_DEST" && chown -R %d:%d /dest`, id.UID, id.GID)
	args := []string{"run", "--rm", "-i",
		"--entrypoint", "sh", "-u", "0:0"}
	args = appendUserns(args, id.Userns())
	args = append(args,
		"-e", "BYRE_DEST="+destPath,
		"--mount", "type=volume,source="+volName+",target=/dest",
		image, "-c", script)
	return r.streamIn(strings.NewReader(content), string(r.engine), args...)
}

// SeedFiles copies a curated subset of srcDir (the relative paths in files,
// each a file or dir) into a fresh named volume at the SAME relative location,
// then chowns the volume to the box identity. Used to seed an agent's
// non-secret prefs (theme, keybindings) into a fresh state volume. Like
// SeedVolume it overrides the entrypoint and runs as root (a fresh volume is
// root-owned), in the box's own userns mapping when the identity carries one.
//
// The file list is passed as positional ARGV (never interpolated into the
// script), so a path can't inject shell. A listed path missing in srcDir is
// skipped, not an error (the host may simply not have that pref yet).
func (r *Runner) SeedFiles(volName, srcDir string, files []string, image string, id Identity) error {
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
		"--entrypoint", "sh", "-u", "0:0"}
	args = appendUserns(args, id.Userns())
	args = append(args,
		"-e", fmt.Sprintf("BYRE_OWNER=%d:%d", id.UID, id.GID),
		"--mount", "type=volume,source="+volName+",target=/dest",
		"--mount", "type=bind,source="+srcDir+",target=/src,readonly",
		image, "-c", script, "seed-prefs")
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

func captureInExec(stdin io.Reader, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// Surface the child's stderr — otherwise failures are just "exit status 1".
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return string(out), fmt.Errorf("%s: %s", err, msg)
		}
	}
	return string(out), err
}

func streamOutExec(stdout io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = stdout
	// The child's stderr is agent-shaped (grab enumerates an agent-controlled
	// tree; find can emit an error per path), so cap it: enough to diagnose a
	// failure, never an unbounded buffer the box can grow to OOM host byre.
	stderr := &capBuffer{max: 64 << 10}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		// Surface the child's stderr — otherwise failures are just "exit status 1".
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s: %s", err, msg)
		}
		return err
	}
	return nil
}

// capBuffer is an io.Writer that keeps at most max bytes but always reports a
// full write, so a child writing past the cap is never blocked on its stderr
// pipe (it just stops being recorded).
type capBuffer struct {
	b   bytes.Buffer
	max int
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.b.Len(); room > 0 {
		if len(p) > room {
			c.b.Write(p[:room])
		} else {
			c.b.Write(p)
		}
	}
	return len(p), nil
}

func (c *capBuffer) String() string { return c.b.String() }

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
