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

// labelKey is the container label byre stamps to find a project's session.
const labelKey = "byre.project"

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
	df, err := build.Assemble(paths, cfg, res)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, df)
	return err
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

func Develop(projectDir, flagTemplate, flagAgent string, selfEdit bool) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if err := paths.Bootstrap(); err != nil {
		return err
	}
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
	// Validate the project path before any build/seed side effects: a comma
	// would corrupt the docker --mount workspace bind.
	if strings.Contains(paths.Canonical, ",") {
		return fmt.Errorf("project path contains a comma, which docker --mount cannot express: %q", paths.Canonical)
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

	label := labelKey + "=" + paths.ID
	name := "byre-" + paths.ID // also the image tag; the name makes single-session atomic
	image := name

	// Fast path: a session is already running for this project — report it rather
	// than racing the shared per-project volumes. A query error here is fatal:
	// it's the live-session safety check.
	ids, err := r.RunningContainersByLabel(label)
	if err != nil {
		return fmt.Errorf("checking for a running session: %w", err)
	}
	if len(ids) > 0 {
		if selfEdit {
			fmt.Fprintln(os.Stderr, "byre: --self-edit only applies when starting a container; a session is already running, so it has no effect here.")
		}
		reportRunning(os.Stderr, r.Engine(), ids)
		return nil
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
	params, err := runParams(paths, cfg, res, name, image, label, selfEdit)
	if err != nil {
		return err
	}
	// The container name makes this atomic: if a concurrent develop won the
	// race, our run fails and a session is now live — report it.
	if runErr := r.Run(runner.RunArgs(params)); runErr != nil {
		if live, qerr := r.RunningContainersByLabel(label); qerr == nil && len(live) > 0 {
			reportRunning(os.Stderr, r.Engine(), live)
			return nil
		}
		return runErr
	}
	return nil
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
	label := labelKey + "=" + paths.ID
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
	return r.Exec(ids[0], uid, gid, "/workspace", map[string]string{"HOME": "/home/dev"}, "bash", "-l")
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
// identity as env (the launcher consumes them), config mounts, and named
// volumes scoped to this project.
func runParams(paths project.Paths, cfg config.Config, res skills.Resolved, name, image, label string, selfEdit bool) (runner.RunParams, error) {
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

	return runner.RunParams{
		Image:           image,
		Name:            name,
		Label:           label,
		WorkspaceHost:   paths.Canonical,
		WorkspaceTarget: "/workspace",
		Env:             env,
		Binds:           binds,
		Volumes:         vols,
		Caps:            res.Caps,
		// Skill run_args are generated grants; the project's own run_args come
		// last so the project-level raw escape hatch wins (last-wins).
		RunArgs: append(append([]string{}, res.RunArgs...), cfg.RunArgs...),
	}, nil
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
func buildImage(r *runner.Runner, paths project.Paths, cfg config.Config, res skills.Resolved, image string, noCache bool) error {
	if cfg.Dockerfile != "" {
		dfPath, err := resolveProjectFile(paths.Canonical, cfg.Dockerfile)
		if err != nil {
			return err
		}
		return r.Build(image, dfPath, paths.Canonical, noCache)
	}
	if _, err := build.Assemble(paths, cfg, res); err != nil {
		return err
	}
	return r.Build(image, paths.Dockerfile, paths.ContextDir, noCache)
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

// rootlessPodmanWarning explains why rootless Podman is unsupported in v0: its
// user-namespace remap breaks byre's host-uid ownership mapping (see launcher).
const rootlessPodmanWarning = "rootless Podman detected — byre maps the host UID/GID onto the in-box user and chowns its storage to it, which assumes a ROOTFUL daemon. Under rootless Podman the userns remap makes that wrong, so files in the project and volumes can end up owned by the wrong id. v0 supports rootful Docker/Podman only — use one of those for correct ownership."

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
