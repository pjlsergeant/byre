// Package commands implements the byre subcommands. main wires them to argv.
package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"byre/internal/build"
	"byre/internal/builtins"
	"byre/internal/config"
	"byre/internal/lock"
	"byre/internal/project"
	"byre/internal/runner"
	"byre/internal/skills"
)

// labelKey is the FAMILY label: byre.project=<family-id>. Every container of a
// repo family carries it, so blast-radius lifecycle queries (reset/forget/
// rehome/status) find all worktrees' sessions. workdirKey is the per-worktree
// label: byre.workdir=<worktree-id>, used to find a SINGLE worktree's session
// (develop's fast path, shell) so two worktrees can run at once without one
// seeing the other's container. For a plain project the two values are equal.
const (
	labelKey   = "byre.project"
	workdirKey = "byre.workdir"
)

// containerName is the engine container name — keyed on the worktree id so two
// worktrees of one repo get distinct containers (and distinct single-session
// locks). For a plain project WorktreeID == ID, so this is the historical
// byre-<id>.
func containerName(p project.Paths) string { return "byre-" + p.WorktreeID }

// familyLabel selects every container of a repo family (all worktrees).
func familyLabel(p project.Paths) string { return labelKey + "=" + p.ID }

// workdirLabel selects a single worktree's container.
func workdirLabel(p project.Paths) string { return workdirKey + "=" + p.WorktreeID }

// resolve loads the config cascade and the enabled skills for a project, and
// re-validates the combined mount/volume set (config + skill contributions).
func resolve(paths project.Paths, projectDir string) (config.Config, skills.Resolved, error) {
	// Materialize built-ins before loading config (templates feed the cascade)
	// and resolving skills.
	if err := builtins.MaterializeTemplates(filepath.Join(paths.Home, "templates")); err != nil {
		return config.Config{}, skills.Resolved{}, err
	}
	skillsDir := filepath.Join(paths.Home, "skills")
	if err := builtins.MaterializeSkills(skillsDir); err != nil {
		return config.Config{}, skills.Resolved{}, err
	}
	cfg, err := config.Load(projectDir)
	if err != nil {
		return config.Config{}, skills.Resolved{}, err
	}
	res, err := skills.Resolve(cfg, skillsDir)
	if err != nil {
		return config.Config{}, skills.Resolved{}, err
	}
	// Skills add mounts/volumes; re-validate the combined set for target/name
	// collisions across config and skills.
	combined := config.Config{
		Mounts:  append(append([]config.Mount{}, cfg.Mounts...), res.Mounts...),
		Volumes: append(append([]config.Volume{}, cfg.Volumes...), res.Volumes...),
	}
	if err := combined.Validate(); err != nil {
		return config.Config{}, skills.Resolved{}, fmt.Errorf("config + skills: %w", err)
	}
	return cfg, res, nil
}

// Dockerfile implements `byre dockerfile`: resolve identity, resolve the config
// cascade + skills, generate the Dockerfile into the build context, and print it.
func Dockerfile(stdout io.Writer, projectDir string) error {
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
	if cfg.Dockerfile != "" {
		// Opt-out: byre doesn't generate; show the user's hand-written Dockerfile.
		dfPath, rerr := resolveProjectFile(paths.Canonical, cfg.Dockerfile)
		if rerr != nil {
			return rerr
		}
		b, rerr := os.ReadFile(dfPath)
		if rerr != nil {
			return fmt.Errorf("dockerfile %q: %w", cfg.Dockerfile, rerr)
		}
		fmt.Fprintf(stdout, "# byre: generation opted out; using %s verbatim:\n", cfg.Dockerfile)
		_, err = stdout.Write(b)
		return err
	}
	df, err := build.Render(paths, cfg, res)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, df)
	return err
}

// DockerRun implements `byre dockerrun`: print the `docker run` (or `podman run`)
// invocation byre would use for this project — the run-time counterpart to
// `byre dockerfile`. Informational and side-effect-free: it resolves config +
// skills and assembles the exact argv (env, workspace bind, mounts, volumes,
// ports, caps, raw run_args, label, image), but builds/runs nothing, so it works
// even before the image exists or without the engine on PATH.
func DockerRun(stdout io.Writer, projectDir string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if err := paths.Bootstrap(); err != nil {
		return err
	}
	// Same guard develop applies: a comma in a bind source can't be expressed in
	// a docker --mount, so don't advertise a command develop would refuse to run.
	if err := checkMountPaths(paths); err != nil {
		return err
	}
	cfg, res, err := resolve(paths, projectDir)
	if err != nil {
		return err
	}
	image := ImageTag(paths.ID, os.Getuid(), os.Getgid())
	params, err := runParams(paths, cfg, res, image, false)
	if err != nil {
		return err
	}
	// Best-effort engine name for the leading token; fall back to the configured
	// value (or docker) so this stays informational when no engine is installed.
	engine := orDefault(cfg.Engine, "docker")
	if eng, derr := runner.Detect(cfg.Engine, nil); derr == nil {
		engine = string(eng)
	}
	argv := append([]string{engine}, runner.RunArgs(params)...)
	fmt.Fprintln(stdout, shellCommand(argv))
	return nil
}

// shellCommand renders an argv as a copy-pasteable shell command line, quoting
// only the args that need it.
func shellCommand(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shellArg(a)
	}
	return strings.Join(quoted, " ")
}

// shellArg single-quotes an argument when it contains shell-significant
// characters, escaping embedded single quotes; leaves plain args (including the
// = and , that fill docker --mount/-e specs) bare for readability.
func shellArg(s string) string {
	// Includes brace/bracket/tilde/bang/hash so a shell can't expand a raw run
	// arg (e.g. --flag={a,b}) into different argv than develop's exec would pass.
	// = , : / . - _ @ stay bare so --mount/-e specs read cleanly.
	const unsafe = " \t\n\"'$\\|&;<>*?(){}[]~!#"
	if s != "" && !strings.ContainsAny(s, unsafe) && !strings.ContainsRune(s, '`') {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Develop implements `byre develop`: set up (generate + build) under a setup
// lock and run the container in the foreground. If a container is already
// running for this directory, report it (and how to act) instead of starting one.
//
// flagTemplate/flagAgent come from --template/--agent (empty = unspecified).
// selfEdit (--self-edit) bind-mounts the host ~/.byre read-write into the box so
// the agent can edit byre's own skills/templates/favourites — a deliberate grant.
//
// selfEditTarget is where --self-edit mounts this project's host-side store
// (~/.byre/projects/<id>/) inside the box, so the agent can edit its OWN
// byre.config — the deliberate "let the agent change its own sandbox" grant.
const selfEditTarget = "/home/dev/.byre-self"

// ExitError signals a process-level exit code that is NOT a byre failure —
// either the agent/container's own exit status, or a deliberate refusal (e.g.
// a session is already running). main distinguishes it from an ordinary error
// so it can os.Exit(Code) directly instead of printing a "byre: ..." banner
// that would misreport the agent's own exit as a byre bug.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// ExitRefused is Develop's exit code when it refuses to start because a
// session is already running for this project — distinct from 0 (ran
// cleanly), 1 (byre error), and 2 (usage error), so a script can tell "byre
// declined to run" from "the agent ran and exited zero".
const ExitRefused = 3

func Develop(projectDir, flagTemplate, flagAgent string, selfEdit bool) error {
	if err := requireNonRootHost(os.Stderr); err != nil {
		return err
	}
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if err := paths.Bootstrap(); err != nil {
		return err
	}
	// Worktree: announce the inherited identity up front, so any onboarding/adopt
	// prompts below are understood as configuring the whole repo family.
	announceWorktree(os.Stderr, paths)
	// A committed <project>/byre.config is a proposal: offer to review + adopt it
	// into the host-side store (never trusted automatically — it's in the box's
	// rw mount). Runs before onboarding so adopting satisfies "already configured".
	if err := adoptIfProposed(os.Stdout, os.Stdin, isTTY(os.Stdin), projectDir, paths); err != nil {
		return err
	}
	// First-run onboarding: with no host-side config, pick (or apply flags / fall
	// back to the cascade on non-TTY) and write the store's byre.config.
	if err := onboardIfNeeded(projectDir, paths, flagTemplate, flagAgent); err != nil {
		return err
	}
	// Validate bind sources before any build/seed side effects: a comma would
	// corrupt a docker --mount value (workspace bind, and worktree git binds).
	if err := checkMountPaths(paths); err != nil {
		return err
	}
	cfg, res, err := resolve(paths, projectDir)
	if err != nil {
		return err
	}
	if cfg.Dockerfile == "" {
		warnNonDebianBase(os.Stderr, cfg.Base)
	}
	eng, err := runner.Detect(cfg.Engine, nil)
	if err != nil {
		return err
	}
	r := runner.New(eng)
	warnRootlessPodman(os.Stderr, r)

	image := ImageTag(paths.ID, os.Getuid(), os.Getgid())

	// Fast path: a session is already running for THIS worktree — report it
	// rather than racing the container name. Queried by the worktree label, not
	// the family label, so another worktree's live session doesn't block this
	// one (running both at once is the point). A query error here is fatal: it's
	// the live-session safety check.
	ids, err := r.RunningContainersByLabel(workdirLabel(paths))
	if err != nil {
		return fmt.Errorf("checking for a running session: %w", err)
	}
	if len(ids) > 0 {
		if selfEdit {
			fmt.Fprintln(os.Stderr, "byre: --self-edit only applies when starting a container; a session is already running, so it has no effect here.")
		}
		reportRunning(os.Stderr, r.Engine(), ids)
		return ExitError{Code: ExitRefused} // refused, session already live
	}

	// Setup (generate + build) is serialized by the lock; the interactive
	// session that follows is not.
	if err := withSetupLock(paths.LockFile, func() error {
		if berr := buildImage(r, paths, cfg, res, image, false); berr != nil {
			return berr
		}
		// Seed fresh state volumes that declare a config-level seed, using the
		// image we just built. One-time; existing volumes are left alone.
		if err := seedVolumes(r, os.Stderr, paths, image, allVolumes(cfg, res.Volumes), os.Getuid(), os.Getgid()); err != nil {
			return err
		}
		// Opt-in: seed the agent's curated non-secret prefs into its fresh state
		// volume (config seed_prefs). No-op unless enabled and the volume is fresh.
		if cfg.SeedPrefs && res.AgentPrefs != nil {
			return seedPrefs(r, os.Stderr, paths, image, res.AgentState, res.AgentPrefs.From, res.AgentPrefs.Files, os.Getuid(), os.Getgid())
		}
		return nil
	}); err != nil {
		return err
	}

	if selfEdit {
		fmt.Fprintln(os.Stderr, "💡 self-edit on — the agent can edit its own byre.config; changes apply on the next develop.")
		fmt.Fprintf(os.Stderr, "   read-write mount: %s\n", paths.Dir)
	}
	params, err := runParams(paths, cfg, res, image, selfEdit)
	if err != nil {
		return err
	}
	// The container name makes this atomic: if a concurrent develop won the
	// race, our run fails and a session is now live — report it.
	if runErr := r.Run(runner.RunArgs(params)); runErr != nil {
		if live, qerr := r.RunningContainersByLabel(workdirLabel(paths)); qerr == nil && len(live) > 0 {
			reportRunning(os.Stderr, r.Engine(), live)
			return ExitError{Code: ExitRefused} // refused, session already live
		}
		// Distinguish the agent/container's own exit from a byre failure: docker
		// reserves 125-127 for engine-level failures (cannot run / not
		// executable / not found), so only codes below that are passed through
		// as the agent's own status (no byre error banner). Anything else —
		// 125-127, a signal-terminated process (ExitCode() == -1), or a
		// non-ExitError failure (e.g. the engine binary itself couldn't run) —
		// stays a byre error.
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			if code := exitErr.ExitCode(); code >= 0 && code < 125 {
				return ExitError{Code: code}
			}
		}
		return runErr
	}
	return nil
}

// announceWorktree notes, on stderr, that this directory is a linked worktree
// inheriting the main repo's identity — so shared config/volumes/image and any
// onboarding prompts are legible rather than surprising. No-op for a plain project.
func announceWorktree(w io.Writer, paths project.Paths) {
	if paths.IsWorktree {
		fmt.Fprintf(w, "byre: worktree of %s — inheriting its config, volumes, and image.\n", paths.Canonical)
	}
}

// noteSharedVolumes warns, before a destructive lifecycle action, that these
// volumes are shared across the whole repo family — so wiping them from one
// worktree affects them all. No-op for a plain project.
func noteSharedVolumes(w io.Writer, paths project.Paths) {
	if paths.IsWorktree {
		fmt.Fprintf(w, "byre: this is a worktree of %s; its volumes are SHARED — this affects ALL worktrees of this repo.\n", paths.Canonical)
	}
}

// reportRunning tells the user a session is already live and how to act on it,
// rather than silently opening a shell (which conflated "run the agent" with
// "give me a shell" — that's `byre shell` now).
func reportRunning(w io.Writer, eng runner.Engine, ids []string) {
	id := shortID(ids[0])
	if len(ids) > 1 {
		fmt.Fprintf(w, "byre: %d containers match this project; the first is %s\n", len(ids), id)
	}
	fmt.Fprintf(w, "byre: a session is already running for this project (%s).\n", id)
	fmt.Fprintf(w, "  • open a shell in it:  byre shell\n")
	fmt.Fprintf(w, "  • stop it:             %s stop %s\n", eng, id)
}

// Shell opens an interactive shell in this project's running container as the
// dev user — for `codex login`, running tests, poking around. Everything is
// derived from the RUNNING container, not re-resolved from the current config:
// the session is found in whichever installed engine holds it, and the dev
// identity comes from the container's own BYRE_UID/BYRE_GID (so it's right even
// under sudo, or if the config changed since the session started). `docker exec`
// already inherits the container's env (CLAUDE_CONFIG_DIR/CODEX_HOME/git identity
// were set as run-time -e vars), so we only add HOME, which the launcher sets at
// runtime and isn't in the container's configured env.
func Shell(stdout io.Writer, projectDir string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	// Query the worktree label so `byre shell` opens THIS worktree's session, not
	// a sibling worktree's (both carry the family label).
	label := workdirLabel(paths)
	// Find the session in whichever installed engine actually holds it — a session
	// may run under podman even when docker is also installed. Skip engines that
	// aren't installed or can't be queried; the first with a match wins.
	var r *runner.Runner
	var ids []string
	var queryErr error
	for _, e := range []string{"docker", "podman"} {
		eng, derr := runner.Detect(e, nil)
		if derr != nil {
			continue // engine not installed
		}
		rr := runner.New(eng)
		got, qerr := rr.RunningContainersByLabel(label)
		if qerr != nil {
			queryErr = qerr // remember; another engine may still hold the session
			continue
		}
		if len(got) > 0 {
			r, ids = rr, got
			break
		}
	}
	if r == nil {
		// Don't mask a broken engine as "nothing running".
		if queryErr != nil {
			return fmt.Errorf("no running session found for this project (an engine query failed: %w)", queryErr)
		}
		return fmt.Errorf("no session is running for this project; start one with 'byre develop'")
	}
	if len(ids) > 1 {
		fmt.Fprintf(stdout, "byre: %d containers match this project; using %s\n", len(ids), shortID(ids[0]))
	}
	cenv, err := r.ContainerEnv(ids[0])
	if err != nil {
		return fmt.Errorf("reading session environment: %w", err)
	}
	// Fail closed: the dev identity must come from the container. Don't fall back
	// to the caller's uid, which could be 0:0 under sudo.
	uid, uerr := strconv.Atoi(strings.TrimSpace(cenv["BYRE_UID"]))
	gid, gerr := strconv.Atoi(strings.TrimSpace(cenv["BYRE_GID"]))
	if uerr != nil || gerr != nil || uid < 0 || gid < 0 {
		return fmt.Errorf("could not determine a valid dev user (BYRE_UID/BYRE_GID) from container %s", shortID(ids[0]))
	}
	return r.Exec(ids[0], uid, gid, "/workspace", map[string]string{"HOME": "/home/dev"}, isTTY(os.Stdin), "bash", "-l")
}

// withSetupLock runs fn while holding the per-project setup lock, surfacing both
// fn's error and any unlock error.
func withSetupLock(path string, fn func() error) error {
	lk, err := lock.Acquire(path)
	if err != nil {
		return err
	}
	ferr := fn()
	rerr := lk.Release()
	return errors.Join(ferr, rerr)
}

// withTwoSetupLocks holds two setup locks (acquired in a stable order to avoid
// deadlock) while running fn. Used by rehome, which mutates two projects' state.
func withTwoSetupLocks(a, b string, fn func() error) error {
	if a > b {
		a, b = b, a
	}
	la, err := lock.Acquire(a)
	if err != nil {
		return err
	}
	lb, err := lock.Acquire(b)
	if err != nil {
		return errors.Join(err, la.Release())
	}
	ferr := fn()
	return errors.Join(ferr, lb.Release(), la.Release())
}

// runParams assembles the run invocation: workspace bind, host UID/GID and git
// identity as env, config mounts, and named volumes scoped to this project. The
// image already bakes the UID/GID (the container runs as that user), so BYRE_UID/
// BYRE_GID are set at runtime only so `byre shell` can read them back and exec as
// the dev user.
func runParams(paths project.Paths, cfg config.Config, res skills.Resolved, image string, selfEdit bool) (runner.RunParams, error) {
	env := map[string]string{
		"BYRE_UID": fmt.Sprintf("%d", os.Getuid()),
		"BYRE_GID": fmt.Sprintf("%d", os.Getgid()),
	}
	for k, v := range res.Env { // skill runtime env
		env[k] = v
	}
	addGitIdentity(env) // git identity wins over skill env for those keys

	// Mounts and volumes are the union of config and skill contributions.
	mounts := append(append([]config.Mount{}, cfg.Mounts...), res.Mounts...)
	binds := make([]runner.BindMount, 0, len(mounts))
	for _, m := range mounts {
		host, err := expandHostPath(m.Host)
		if err != nil {
			return runner.RunParams{}, err
		}
		binds = append(binds, runner.BindMount{Host: host, Target: m.Target, Mode: m.Mode})
	}
	// Worktree git support: a linked worktree's .git is a pointer into the repo's
	// common git dir (objects/refs live there, outside the worktree), and git's
	// metadata is full of absolute HOST paths. Bind both the common git dir and
	// the worktree at their same host paths (rw) so every pointer resolves in the
	// box and git can commit — without rewriting metadata shared rw with the host
	// (which would corrupt the host repo). See docs/agent-volume-sharing.md.
	if paths.IsWorktree {
		binds = append(binds,
			runner.BindMount{Host: paths.CommonGitDir, Target: paths.CommonGitDir, Mode: "rw"},
			runner.BindMount{Host: paths.WorkDir, Target: paths.WorkDir, Mode: "rw"},
		)
	}
	// --self-edit: mount this project's host-side store (~/.byre/projects/<id>/)
	// rw so the agent can edit its own byre.config (applied on the next develop).
	// Deliberate grant; scoped to this project only, not all of ~/.byre.
	if selfEdit {
		binds = append(binds, runner.BindMount{Host: paths.Dir, Target: selfEditTarget, Mode: "rw"})
	}
	allVols := append(append([]config.Volume{}, cfg.Volumes...), res.Volumes...)
	vols := make([]runner.NamedVolume, 0, len(allVols))
	for _, v := range allVols {
		vols = append(vols, runner.NamedVolume{Name: VolumeName(paths.ID, v.Name), Target: v.Target})
	}
	// Published ports come from config only. Default the bind interface to
	// 127.0.0.1 (localhost-only — binding all interfaces / the LAN must be explicit
	// in the config), and a blank host port (0) to the container port (a
	// predictable mapping, not a random one).
	ports := make([]runner.PortPublish, 0, len(cfg.Ports))
	for _, p := range cfg.Ports {
		iface := p.Interface
		if iface == "" {
			iface = "127.0.0.1"
		}
		host := p.Host
		if host == 0 {
			host = p.Container
		}
		ports = append(ports, runner.PortPublish{Interface: iface, Host: host, Container: p.Container})
	}

	return runner.RunParams{
		Image:           image,
		Name:            containerName(paths),
		Labels:          []string{familyLabel(paths), workdirLabel(paths)},
		WorkspaceHost:   paths.WorkDir,
		WorkspaceTarget: "/workspace",
		Env:             env,
		Binds:           binds,
		Volumes:         vols,
		Ports:           ports,
		Caps:            res.Caps,
		// Skill run_args are generated grants; the project's own run_args come
		// last so the project-level raw escape hatch wins (last-wins).
		RunArgs: append(append([]string{}, res.RunArgs...), cfg.RunArgs...),
		// Only allocate a pseudo-TTY when stdin actually is one — otherwise
		// docker run -t fails under CI/piped invocations.
		TTY: isTTY(os.Stdin),
	}, nil
}

// checkMountPaths rejects any byre-owned bind source that a docker --mount value
// (comma-separated key=value pairs) cannot express. Covers the workspace bind
// and, for a worktree, the same-path git binds — all set by byre, not the user.
func checkMountPaths(paths project.Paths) error {
	for _, p := range []string{paths.WorkDir, paths.CommonGitDir} {
		if strings.Contains(p, ",") {
			return fmt.Errorf("path contains a comma, which docker --mount cannot express: %q", p)
		}
	}
	return nil
}

// expandHostPath expands a leading ~ and requires the result to be absolute, so
// a relative or home-relative mount host can't be misread by the engine.
func expandHostPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = home + strings.TrimPrefix(p, "~")
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("mount host path must be absolute: %q", p)
	}
	// docker --mount values are comma-separated key=value pairs, so a comma in
	// the path can't be expressed; reject it clearly rather than mis-mount.
	if strings.Contains(p, ",") {
		return "", fmt.Errorf("mount host path contains a comma, which docker --mount cannot express: %q", p)
	}
	return p, nil
}

// ImageTag is the local image tag for a project's build, qualified by the host
// UID/GID baked into the image. The UID/GID are part of the image's identity (the
// dev user, /home/dev, and the volume mount points are all chowned to them at
// build time), so on a shared daemon two users building the same project path get
// distinct images instead of one reusing the other's wrong-owned build. The
// container NAME stays byre-<id> (it makes a single session atomic, and volume
// names are unchanged); only the image tag carries the uid/gid.
func ImageTag(projectID string, uid, gid int) string {
	return fmt.Sprintf("byre-%s-u%d-g%d", projectID, uid, gid)
}

// VolumeName is the docker volume name for a per-project named volume.
// VolumeName is the Docker name for a project's named volume: byre-<id>-<name>.
// The project id namespaces it, so reset/forget/rehome can filter a project's
// volumes by the byre-<id>- prefix. (Worktree volume INHERITANCE, when built,
// works by resolving <id> from the main worktree's path — not by a separate
// volume scope — see docs/agent-volume-sharing.md.)
func VolumeName(projectID, name string) string {
	return "byre-" + projectID + "-" + name
}

// volumeLister is the runner surface needed to enumerate a project's volumes.
type volumeLister interface {
	VolumesByPrefix(prefix string) ([]string, error)
}

// projectVolumes lists the volumes owned by id. Because project ids may contain
// hyphens, a bare `byre-<id>-` prefix can also match another project's volumes
// (when that project's id begins with this id). Each volume is assigned to the
// LONGEST known id whose prefix it carries, so one project never captures
// another's volumes.
func projectVolumes(r volumeLister, home, id string) ([]string, error) {
	vols, err := r.VolumesByPrefix("byre-" + id + "-")
	if err != nil {
		return nil, err
	}
	others := knownProjectIDs(home)
	var owned []string
	for _, v := range vols {
		if !claimedByLongerID(v, id, others) {
			owned = append(owned, v)
		}
	}
	return owned, nil
}

// knownProjectIDs lists the ids byre has a ~/.byre/projects/<id>/ dir for.
func knownProjectIDs(home string) []string {
	entries, err := os.ReadDir(filepath.Join(home, "projects"))
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids
}

// claimedByLongerID reports whether vol belongs to a different, more-specific
// project id (a longer `byre-<oid>-` prefix) than id.
func claimedByLongerID(vol, id string, others []string) bool {
	p := "byre-" + id + "-"
	for _, oid := range others {
		op := "byre-" + oid + "-"
		if oid != id && len(op) > len(p) && strings.HasPrefix(vol, op) {
			return true
		}
	}
	return false
}

// buildImage builds the project's image, honoring the full-Dockerfile opt-out
// uniformly (so develop and rebuild produce the same image for opt-out projects).
// The generated build bakes the host UID/GID via --build-arg so /home/dev and the
// volume mount points are born owned by the runtime user (no runtime chown). The
// opt-out path gets no build args: the user owns that infra layer.
func buildImage(r *runner.Runner, paths project.Paths, cfg config.Config, res skills.Resolved, image string, noCache bool) error {
	if cfg.Dockerfile != "" {
		dfPath, err := resolveProjectFile(paths.Canonical, cfg.Dockerfile)
		if err != nil {
			return err
		}
		return r.Build(image, dfPath, paths.Canonical, noCache, nil)
	}
	if _, err := build.Assemble(paths, cfg, res); err != nil {
		return err
	}
	return r.Build(image, paths.Dockerfile, paths.ContextDir, noCache, uidBuildArgs())
}

// requireNonRootHost refuses to build/run as uid or gid 0. byre bakes the
// invoking user's id into the image as the `dev` user, so running as root makes
// the in-container agent root — it would write root-owned files onto host bind
// mounts, defeating byre's unprivileged-agent design. Determined users can
// override with BYRE_ALLOW_ROOT=1, which only prints a warning. warn receives
// that warning (human-facing, so callers pass stderr).
func requireNonRootHost(warn io.Writer) error {
	if os.Getuid() != 0 && os.Getgid() != 0 {
		return nil
	}
	if os.Getenv("BYRE_ALLOW_ROOT") == "1" {
		fmt.Fprintln(warn, "byre: WARNING: running as root (BYRE_ALLOW_ROOT=1). The container's dev user is UID 0, so the agent runs as root and any files it writes to host mounts are root-owned. This defeats byre's unprivileged-agent design — you're on your own.")
		return nil
	}
	return errors.New("refusing to run as root: byre would bake UID 0 as the container's dev user, so the agent would run as root and create root-owned files on your host mounts. Run byre as your normal user, or set BYRE_ALLOW_ROOT=1 to override anyway.")
}

// uidBuildArgs returns the --build-arg pairs that bake the invoking user's
// UID/GID into the image. byre develop builds and runs as the same user in one
// invocation, so build-UID == run-UID by construction.
func uidBuildArgs() []string {
	return []string{
		fmt.Sprintf("BYRE_UID=%d", os.Getuid()),
		fmt.Sprintf("BYRE_GID=%d", os.Getgid()),
	}
}

// resolveProjectFile resolves a project-relative file, following symlinks and
// confirming containment within the project dir (so an opt-out `dockerfile`
// can't point outside via a symlink).
func resolveProjectFile(projectDir, rel string) (string, error) {
	realDir, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(filepath.Join(realDir, rel))
	if err != nil {
		return "", fmt.Errorf("dockerfile %q: %w", rel, err)
	}
	within, err := filepath.Rel(realDir, real)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("dockerfile %q escapes the project dir", rel)
	}
	return real, nil
}

// warnNonDebianBase prints a friendly warning when the base image is obviously
// not Debian-derived, since byre's core infra layer assumes apt + glibc.
func warnNonDebianBase(w io.Writer, base string) {
	l := strings.ToLower(base)
	if strings.Contains(l, "alpine") || strings.Contains(l, "scratch") || strings.Contains(l, "distroless") {
		fmt.Fprintf(w, "byre: warning: base %q is not Debian-derived; byre's core infra layer assumes apt + glibc and may fail to build. Use a Debian/Ubuntu base, or a full hand-written Dockerfile (dockerfile = ...).\n", base)
	}
}

// rootlessPodmanWarning explains why rootless Podman is unsupported in v0: byre
// bakes the host UID/GID into the image so the in-container uid matches the uid
// on disk, which assumes a ROOTFUL daemon; rootless Podman's user-namespace remap
// breaks that. A generic-uid image + --userns=keep-id is the planned fix.
const rootlessPodmanWarning = "rootless Podman detected — byre bakes the host UID/GID into the image so files it writes land owned by you, which assumes a ROOTFUL daemon. Under rootless Podman the userns remap makes that wrong, so files in the project and volumes can end up owned by the wrong id. v0 supports rootful Docker/Podman only — use one of those for correct ownership."

// rootlessChecker is the runner subset warnRootlessPodman needs (an interface so
// the warning wiring is testable without a real engine).
type rootlessChecker interface {
	IsRootlessPodman() (bool, error)
}

// warnRootlessPodman prints the rootless-Podman warning to w when r drives it. A
// detection error is ignored — better silent than warning on a guess.
func warnRootlessPodman(w io.Writer, r rootlessChecker) {
	if rootless, err := r.IsRootlessPodman(); err == nil && rootless {
		fmt.Fprintln(w, "byre: warning: "+rootlessPodmanWarning)
	}
}

// addGitIdentity copies only the host git user.name/user.email into env as the
// GIT_*_NAME/EMAIL vars — the one narrow exception to host-env isolation.
func addGitIdentity(env map[string]string) {
	if name := gitConfig("user.name"); name != "" {
		env["GIT_AUTHOR_NAME"] = name
		env["GIT_COMMITTER_NAME"] = name
	}
	if email := gitConfig("user.email"); email != "" {
		env["GIT_AUTHOR_EMAIL"] = email
		env["GIT_COMMITTER_EMAIL"] = email
	}
}

func gitConfig(key string) string {
	out, err := exec.Command("git", "config", "--get", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
