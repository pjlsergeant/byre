// Package runner drives a container engine (Docker or Podman) via its CLI.
//
// byre shells out to the engine CLI rather than binding the Docker SDK, which
// keeps Docker and Podman as two implementations of the same small surface.
package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Engine is a supported container engine.
type Engine string

const (
	Docker Engine = "docker"
	Podman Engine = "podman"
)

// LookPath mirrors exec.LookPath but returns the path to RUN, which is not
// always the path PATH names: production passes hostexec's resolver, which
// pins the answer for the invocation and refuses a binary sitting in a
// directory the project's box can write. Injectable so engine detection is
// testable without a real engine installed.
type LookPath func(string) (string, error)

// NotInstalledError is Detect's "this engine simply is not on this machine"
// answer, and the ONE failure a caller enumerating engines may treat as
// "skip it". Every other failure -- a declined binary above all -- means byre
// could not establish what is on that engine, which is not the same thing:
// a caller that skips it is claiming a coverage it does not have ("every
// installed engine", "completely removed"). Carrying it as a TYPE rather than
// a message is what lets those callers tell the two apart.
type NotInstalledError struct{ Engine string }

func (e *NotInstalledError) Error() string {
	return fmt.Sprintf("engine %q not found on PATH", e.Engine)
}

// Detect resolves which engine to use from the config setting ("auto",
// "docker", or "podman"), and returns the absolute path to run it by. With
// "auto" it prefers docker, then podman. look is exec.LookPath in tests that
// pass nil; callers with a project in hand pass hostexec.Looker(roots).
//
// The engine NAME and the engine PATH are both returned because they answer
// different questions: the name is what the user configured and what every
// message says, the path is what byre execs. Keeping only the name is what
// let every one of the ~20 engine calls re-read PATH.
func Detect(setting string, look LookPath) (Engine, string, error) {
	if look == nil {
		look = exec.LookPath
	}
	switch setting {
	case "", "auto":
		for _, e := range []Engine{Docker, Podman} {
			p, err := look(string(e))
			if err == nil {
				return e, p, nil
			}
			// Only "not installed" moves on to the next engine. A lookup that
			// failed for any OTHER reason -- hostexec declining a docker
			// resolved out of the project tree -- is reported, never stepped
			// over: falling through to podman would hide the shadowed binary
			// behind a working session.
			if !errors.Is(err, exec.ErrNotFound) {
				return "", "", err
			}
		}
		return "", "", fmt.Errorf("no container engine found on PATH (looked for docker, podman)")
	case string(Docker), string(Podman):
		p, err := look(setting)
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				return "", "", &NotInstalledError{Engine: setting}
			}
			return "", "", err
		}
		return Engine(setting), p, nil
	default:
		return "", "", fmt.Errorf("unknown engine %q (want auto|docker|podman)", setting)
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
	engine Engine
	// exe is the absolute path the engine CLI was resolved to, pinned for the
	// invocation. Every exec below runs THIS, not the bare name: a bare name
	// re-reads PATH per call, so the binary byre resolved and the binary byre
	// ran were two lookups with a window between them.
	exe       string
	stream    func(name string, args ...string) error
	capture   func(name string, args ...string) (string, error)
	streamIn  func(stdin io.Reader, name string, args ...string) error
	captureIn func(stdin io.Reader, name string, args ...string) (string, error)
	streamOut func(stdout io.Writer, name string, args ...string) error
	// captureBounded is capture with a wall-clock deadline, for the engine
	// calls byre makes from a goroutine that has no other way out: the netns
	// helper and the sock-group probe run CONTAINERS, and the network-mode
	// inspect is what stands between the box and running unprotected. A wedged
	// daemon leaves those waiting forever with the box already up; every other
	// captured call is answered by a client that fails on its own.
	captureBounded func(d time.Duration, name string, args ...string) (string, error)
	// captureBoundedIn is captureBounded with a caller-supplied stdin — the
	// credential inject's seam (streaming content in, deadline-bounded,
	// from a goroutine concurrent with the attached session).
	captureBoundedIn func(d time.Duration, stdin io.Reader, name string, args ...string) (string, error)
}

// New returns a Runner for the given engine using real exec. exe is the
// absolute path Detect resolved the CLI to.
func New(e Engine, exe string) *Runner {
	return &Runner{
		engine: e,
		exe:    exe,
		// stream/capture are the no-input cases of their ...In siblings:
		// os.Stdin for the interactive form, a nil Reader (== no stdin) for
		// the captured one. Separate implementations drifted -- one grew a
		// stderr cap the other never got.
		stream:           func(name string, args ...string) error { return streamInExec(os.Stdin, name, args...) },
		capture:          func(name string, args ...string) (string, error) { return captureInExec(nil, name, args...) },
		streamIn:         streamInExec,
		captureIn:        captureInExec,
		streamOut:        streamOutExec,
		captureBounded:   captureBoundedExec,
		captureBoundedIn: captureBoundedInExec,
	}
}

// The wall-clock bounds on the three hang-prone engine calls. Sized to "the
// engine is wedged", never to "this is slow": a container launch on a cold
// machine, or a netns helper resolving a long allowlist over slow DNS, must
// finish comfortably inside them. Passing one means byre stops waiting and
// reports, which for the netns pair means the box fails CLOSED rather than
// hanging with the agent parked at the launch gate.
const (
	netnsProbeTimeout = 2 * time.Minute  // an inspect: only a wedged daemon takes this
	netnsInitTimeout  = 10 * time.Minute // runs a helper container: rules + DNS for the whole allowlist
	sockProbeTimeout  = 5 * time.Minute  // runs a one-shot probe container against a local image
	// helperKillTimeout bounds the cleanup kill itself: it runs on a path that
	// already failed, and a second wedge there would undo the first bound.
	helperKillTimeout = 30 * time.Second
	// waitDelay is how long a killed child's output pipes may stay open before
	// the wait gives up on them (a descendant that inherited them).
	waitDelay = 5 * time.Second
	// captureBoundedMax caps a bounded call's stdout. These answer with a
	// container id, a network mode or a gid; the cap is what keeps a child
	// gone wrong from growing byre's memory instead of failing.
	captureBoundedMax = 8 << 20
)

// helperName mints the --name byre gives a run-to-completion helper container.
// A package var so tests can pin it; production always uses fresh randomness.
//
// The name exists for ONE reason: a deadline kills the engine CLIENT, not the
// container it started. An unnamed helper is then unreachable — still running,
// still holding CAP_NET_ADMIN over the box's netns, still able to finish its
// rules and open the launch gate — while byre has already reported the hook
// as failed. Named, byre can stop it.
var helperName = func(kind string) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// No name, no cleanup handle. byre does not start a container it
		// cannot later stop, so the caller hears this instead (for the netns
		// hook that means no hooks, which fails the launch closed).
		return "", fmt.Errorf("no randomness to name the %s helper: %w", kind, err)
	}
	return "byre-" + kind + "-" + hex.EncodeToString(b), nil
}

// runHelperBounded runs a one-shot helper container under a wall-clock bound,
// with the cleanup the bound needs to mean anything: argv is built around a
// byre-minted --name, and ANY failure kills that container through the engine
// before returning. Any failure, not just a timeout -- a client that died for
// some other reason proves just as little about what the daemon is still
// running, and `--rm` only fires once the container itself exits.
func (r *Runner) runHelperBounded(d time.Duration, kind string, argv func(name string) []string) (string, error) {
	name, err := helperName(kind)
	if err != nil {
		return "", err
	}
	out, cerr := r.captureBounded(d, r.bin(), argv(name)...)
	if cerr != nil {
		// Best-effort: an already-exited container makes this a harmless
		// error, and a kill that cannot land leaves the caller's own
		// fail-closed handling as the backstop (which is why runNetnsInits
		// stops the box rather than trusting this).
		_, _ = r.captureBounded(helperKillTimeout, r.bin(), "kill", name)
	}
	return out, cerr
}

// Engine reports the engine this runner invokes.
func (r *Runner) Engine() Engine { return r.engine }

// bin is argv[0] for every engine call: the path Detect pinned. A Runner
// assembled without one -- the exec-seam fakes in tests -- falls back to the
// bare name, which is also what the fakes assert on.
func (r *Runner) bin() string {
	if r.exe == "" {
		return string(r.engine)
	}
	return r.exe
}

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
	out, err := r.capture(r.bin(), "info", "--format", "{{.Host.Security.Rootless}}")
	if err != nil {
		return false, err
	}
	// Only the two answers this template can give count as answers. Reading
	// "anything that isn't true" as false hands a rootless engine the rootful
	// path -- and the shapes that reach here otherwise (an empty string, Go's
	// "<no value>" for a field a future Podman moved, a wrapper's banner) all
	// look like false to that test. Inconclusive is an error, which is the
	// state every caller already handles.
	switch answer := strings.TrimSpace(out); answer {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s info gave no usable rootless answer (%q)", r.engine, truncate(answer, 60))
	}
}

// truncate bounds an engine-authored string interpolated into an error: the
// answer above is one word when the query works, and an engine that instead
// hands back a page of text must not become a page of byre error.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
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
	return r.stream(r.bin(), append(args, contextDir)...)
}

// Create creates (without starting) a container from the assembled create
// argv (CreateArgs) and returns the container ID the engine printed. The
// container name is the handle for the StartAttach that follows (a name
// conflict surfaces here, in the engine's stderr); the ID is the handle for
// anything that must address THIS container exactly — the credential
// inject's `exec` targets it rather than re-resolving the name.
func (r *Runner) Create(args []string) (string, error) {
	out, err := r.capture(r.bin(), args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// StartAttach starts a created container in the foreground: attached, with
// stdin open (the -i/-t attach shape was fixed at create time). The exit
// status is the container's own, like `docker run`'s — but unlike run, an
// engine-level start failure exits 1, not the reserved 125-127 band, so
// callers can't fully distinguish it from an agent exit 1; the engine's
// stderr (streamed) names the cause.
func (r *Runner) StartAttach(container string) error {
	return r.stream(r.bin(), "start", "--attach", "--interactive", container)
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
	out, err := r.capture(r.bin(), args...)
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
	_, err := r.capture(r.bin(), "rm", container)
	return err
}

// ContainerEnv returns a running container's configured environment (image ENV
// plus the `-e` vars set at run time), so callers can act on the identity/env
// the session ACTUALLY started with rather than re-deriving it.
func (r *Runner) ContainerEnv(id string) (map[string]string, error) {
	out, err := r.capture(r.bin(), "inspect", "-f", "{{range .Config.Env}}{{println .}}{{end}}", id)
	if err != nil {
		return nil, err
	}
	return parseEnvLines(out), nil
}

// ContainerLabels returns a running container's labels, so callers can read
// the identity byre stamped at run time (byre.project / byre.workdir) off the
// container itself rather than re-deriving it from host state.
func (r *Runner) ContainerLabels(id string) (map[string]string, error) {
	out, err := r.capture(r.bin(), "inspect", "-f", "{{json .Config.Labels}}", id)
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
	return r.captureIn(stdin, r.bin(), execInputArgs(containerID, uid, gid, command...)...)
}

// ExecInputBounded is ExecInput under a wall-clock deadline with capped
// output — the credential inject's transport. The deadline exists because
// this exec runs from develop's launch path concurrently with the attached
// session: a wedged daemon (or a receiver that never reads) must cost at
// most the bound, never a goroutine byre waits on forever. Delivery failure
// then fails the LAUNCH — the caller stops the box, whose own launcher would
// refuse to start the agent without the values anyway.
func (r *Runner) ExecInputBounded(d time.Duration, containerID string, uid, gid int, stdin io.Reader, command ...string) (string, error) {
	return r.captureBoundedIn(d, stdin, r.bin(), execInputArgs(containerID, uid, gid, command...)...)
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
	return r.streamOut(stdout, r.bin(), execInputArgs(containerID, uid, gid, command...)...)
}

// NetworkMode returns a container's network mode as the engine reports it
// (HostConfig.NetworkMode): "host", "container:<id>", "none", or a private
// network ("default"/"bridge"/a network name). Callers that mutate a
// container's network namespace (NetnsInit) use this to establish the
// namespace is actually the container's own before touching it.
func (r *Runner) NetworkMode(container string) (string, error) {
	out, err := r.captureBounded(netnsProbeTimeout, r.bin(), "inspect", "-f", "{{.HostConfig.NetworkMode}}", container)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Stop stops a running container (short grace period, then SIGKILL). Used when
// byre must actively end a session it cannot let run — e.g. netns hooks were
// refused and the launch gate can't be trusted to fail the launch closed.
func (r *Runner) Stop(container string) error {
	_, err := r.capture(r.bin(), "stop", "-t", "2", container)
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
	return r.stream(r.bin(), execArgs(containerID, uid, gid, workdir, env, tty, command...)...)
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
	_, err := r.runHelperBounded(netnsInitTimeout, "netns", func(name string) []string {
		return netnsInitArgs(name, image, container, entrypoint, env, joinUserns)
	})
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
	out, err := r.runHelperBounded(sockProbeTimeout, "sockprobe", func(name string) []string {
		return probeSockGroupArgs(name, image, hostPath, targetPath, userns)
	})
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
func probeSockGroupArgs(name, image, hostPath, targetPath, userns string) []string {
	args := []string{
		"run", "--rm",
		"--name", name,
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
	out, err := r.capture(r.bin(), "info", "--format", "{{.OperatingSystem}}")
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
func netnsInitArgs(name, image, container, entrypoint string, env map[string]string, joinUserns bool) []string {
	args := []string{"run", "--rm",
		"--name", name,
		"-u", "0:0",
		"--net", "container:" + container,
		"--cap-add", "NET_ADMIN",
		"--entrypoint", entrypoint,
	}
	if joinUserns {
		args = appendUserns(args, "container:"+container)
	}
	for _, k := range slices.Sorted(maps.Keys(env)) {
		args = append(args, "-e", k+"="+env[k])
	}
	return append(args, image)
}

// VolumeExists reports whether a named volume exists.
func (r *Runner) VolumeExists(name string) (bool, error) {
	out, err := r.capture(r.bin(), "volume", "ls", "-q", "--filter", "name=^"+name+"$")
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
	_, err := r.capture(r.bin(), "volume", "create", name)
	return err
}

// ImageExists reports whether an image with the given tag exists locally.
func (r *Runner) ImageExists(tag string) (bool, error) {
	out, err := r.capture(r.bin(), "images", "-q", tag)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// ImageDigest returns the engine's own id for the image a tag currently
// resolves to ("sha256:..."), which is what a container created from that tag
// actually runs. Deliberately .Id and not RepoDigests: byre's images are built
// locally and never pushed, so a registry digest does not exist for them,
// while the image id does and is exactly the fact `byre rebuild` moves the tag
// away from. A caller that cannot get an answer records the failure rather
// than a guess.
func (r *Runner) ImageDigest(tag string) (string, error) {
	out, err := r.capture(r.bin(), "image", "inspect", "-f", "{{.Id}}", tag)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return "", fmt.Errorf("%s image inspect gave no id for %s", r.engine, tag)
	}
	return id, nil
}

// ImageRemove removes an image by tag.
func (r *Runner) ImageRemove(tag string) error {
	_, err := r.capture(r.bin(), "image", "rm", tag)
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
	return r.stream(r.bin(), args...)
}

// VolumesByPrefix lists existing volume names beginning with prefix.
func (r *Runner) VolumesByPrefix(prefix string) ([]string, error) {
	out, err := r.capture(r.bin(), "volume", "ls", "-q", "--filter", "name="+prefix)
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
	_, err := r.capture(r.bin(), "volume", "rm", name)
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
	return r.stream(r.bin(), args...)
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
	return r.streamIn(strings.NewReader(content), r.bin(), args...)
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
	return r.stream(r.bin(), args...)
}

func streamInExec(stdin io.Reader, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = stdin
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// captureInExec runs name with stdin and returns its stdout. A nil stdin leaves
// Cmd.Stdin nil, which is the no-input case (there is no separate no-stdin
// variant). Stderr is capped for the same reason streamOutExec caps it: this
// backs Runner.ExecInput, whose callers (grab, the deliver transports) run
// children over an agent-controlled tree, so the error text must never become
// an unbounded buffer the box can grow to OOM host byre. New() also routes the
// plain capture seam here, so every captured engine call gets the cap. Payload
// output uses streamOutExec/ExecOutput instead; captured stdout is always a
// control reply (an id, mode, path, label, or similar small answer).
func captureInExec(stdin io.Reader, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = stdin
	// Match the bounded capture sibling: an engine client may spawn helpers
	// that inherit stdout. Killing only the direct child on overflow leaves
	// Wait blocked on those writers, turning the memory bound into a hang.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = waitDelay
	stderr := &capBuffer{max: 64 << 10}
	cmd.Stderr = stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	out, rerr := io.ReadAll(io.LimitReader(pipe, captureBoundedMax+1))
	over := len(out) > captureBoundedMax
	if over {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	err = cmd.Wait()
	if over {
		return "", fmt.Errorf("%s: output exceeds %d bytes", name, captureBoundedMax)
	}
	if rerr != nil {
		return string(out), rerr
	}
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

// captureBoundedExec is captureInExec with a wall-clock deadline: the child is
// killed when d expires and the timeout is reported as itself, not as a bare
// "signal: killed". Only the calls whose failure mode is a HANG use it (see
// Runner.captureBounded) -- the bound exists so byre's own goroutine can
// report and fail closed instead of waiting forever with a box already
// running. Generous by construction: these are container launches, so the
// deadline is sized to "something is wrong", never to "this is slow".
func captureBoundedExec(d time.Duration, name string, args ...string) (string, error) {
	return captureBoundedInExec(d, nil, name, args...)
}

// captureBoundedInExec is captureBoundedExec's stdin-carrying core (nil
// stdin = no input, the plain bounded capture).
func captureBoundedInExec(d time.Duration, stdin io.Reader, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	// Own process group, and cancel it as a GROUP. An engine client spawns
	// local helpers of its own (credential and transport helpers), and
	// CommandContext's default kills the direct child only -- so the deadline
	// would leave those running while byre walked away. Setpgid is also what
	// makes the negative-pid kill legal: it needs a group leader. These
	// children are non-interactive and byre-internal (an inspect, a
	// run-to-completion helper, a one-shot probe -- none reads the tty), so
	// taking them out of the foreground group costs nothing a user sees.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// Killing the child is not enough to return: cmd.Wait blocks until every
	// writer of the output pipes closes, and a DESCENDANT that inherited them
	// keeps them open past its parent's death -- one that changed its own
	// group escapes the kill above too. WaitDelay is the deadline on that
	// second wait; without it the bound stops the child and byre goes on
	// waiting anyway, which is the wedge the bound exists to prevent.
	cmd.WaitDelay = waitDelay
	stderr := &capBuffer{max: 64 << 10}
	cmd.Stderr = stderr
	pipe, perr := cmd.StdoutPipe()
	if perr != nil {
		return "", perr
	}
	if serr := cmd.Start(); serr != nil {
		return "", serr
	}
	// Bounded like the stderr buffer, and for the same reason: these calls
	// answer with an id, a mode string or a gid, so anything approaching the
	// cap is a child gone wrong, and reading it whole would make byre's memory
	// the child's to grow.
	out, rerr := io.ReadAll(io.LimitReader(pipe, captureBoundedMax+1))
	over := len(out) > captureBoundedMax
	if over {
		cancel() // stop the writer; a capped read never waits the child out
	}
	err := cmd.Wait()
	switch {
	case ctx.Err() != nil && !over:
		return string(out), fmt.Errorf("%s: no answer within %s (gave up)", name, d)
	case over:
		return "", fmt.Errorf("%s: output exceeds %d bytes", name, captureBoundedMax)
	case rerr != nil:
		return string(out), rerr
	case err != nil:
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return string(out), fmt.Errorf("%s: %s", err, msg)
		}
	}
	return string(out), err
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
