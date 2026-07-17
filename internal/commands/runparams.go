package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

// runParams assembles the run invocation: workspace bind, the dev identity and
// the env_from_host passthrough as env, config mounts, and named volumes scoped
// to this project. The image already bakes the identity's UID/GID (the
// container runs as that user), so BYRE_UID/BYRE_GID are set at runtime only so
// `byre shell` and deliver can read them back and exec as the dev user.
func runParams(paths project.Paths, rv resolved, image string, selfEdit, tty bool, ident runner.Identity) (runner.RunParams, error) {
	// BYRE_UID/GID: the box's IN-CONTAINER dev identity (shell/deliver exec as
	// it) — the host user's ids on the rootful path, the generic keep-id ids
	// under rootless Podman, where the userns maps them back to the host user.
	// BYRE_PROJECT/WORKTREE: plumbing legibility for skills (docker-host keys
	// compose on WorktreeID -- Paths.ID is shared across worktrees, so
	// project-keyed compose would still collide).
	env := map[string]string{
		"BYRE_UID":      fmt.Sprintf("%d", ident.UID),
		"BYRE_GID":      fmt.Sprintf("%d", ident.GID),
		"BYRE_PROJECT":  paths.ID,
		"BYRE_WORKTREE": paths.WorktreeID,
	}
	for k, v := range rv.skills.Env() { // skill runtime env
		env[k] = v
	}
	addEnvFromHost(env, rv.cfg) // host passthrough beats skill env for its keys; explicit [env] beats it

	binds := make([]runner.BindMount, 0, len(rv.mounts))
	for _, m := range rv.mounts {
		// A disabled mount produces no bind at all. Skipped BEFORE host-path
		// expansion, so a mount whose host path is currently absent or invalid
		// can be switched off without blocking develop -- that's its point.
		if m.Disabled {
			continue
		}
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
	// (which would corrupt the host repo). See docs/adr/0009-worktrees-inherit-project-identity.md.
	if paths.IsWorktree {
		// Source is the symlink-resolved CommonGitDirHost (no mutable symlink
		// component an agent could retarget between detection and mount); target
		// stays the git-recorded CommonGitDir so in-box pointers resolve. They
		// differ only when the recorded path contains symlinks.
		binds = append(binds,
			runner.BindMount{Host: paths.CommonGitDirHost, Target: paths.CommonGitDir, Mode: "rw"},
			runner.BindMount{Host: paths.WorkDir, Target: paths.WorkDir, Mode: "rw"},
		)
	}
	// --self-edit: mount this project's host-side store (~/.byre/projects/<id>/)
	// rw so the agent can edit its own byre.config (applied on the next develop).
	// Deliberate grant: the agent authors its own next sandbox. The config stays
	// legible (status names the grants; unknown keys fail loudly), but byre
	// neither polices the store's raw bytes (decided 2026-07-06) nor makes the
	// user review the diff before the next develop.
	if selfEdit {
		binds = append(binds, runner.BindMount{Host: paths.Dir, Target: selfEditTarget, Mode: "rw"})
	}
	vols := make([]runner.NamedVolume, 0, len(rv.volumes))
	for _, v := range rv.volumes {
		vols = append(vols, runner.NamedVolume{Name: scopedVolumeName(paths.ID, os.Getuid(), v), Target: v.Target})
	}
	// Published ports come from config only, normalized to the publish defaults.
	ports := make([]runner.PortPublish, 0, len(rv.cfg.Ports))
	for _, p := range rv.cfg.Ports {
		iface, host := config.PortEffective(p)
		ports = append(ports, runner.PortPublish{Interface: iface, Host: host, Container: p.Container})
	}

	return runner.RunParams{
		Image:           image,
		Name:            containerName(paths),
		Labels:          []string{projectLabel(paths), workdirLabel(paths)},
		WorkspaceHost:   paths.WorkDir,
		WorkspaceTarget: "/workspace",
		Env:             env,
		Binds:           binds,
		Volumes:         vols,
		Ports:           ports,
		Caps:            rv.skills.Caps(),
		Userns:          ident.Userns(),
		// Skill run_args are generated grants; the project's own run_args come
		// last so the project-level raw escape hatch wins (last-wins).
		RunArgs: append(append([]string{}, rv.skills.RunArgs()...), rv.cfg.RunArgs...),
		// Only allocate a pseudo-TTY when stdin actually is one — otherwise
		// docker run -t fails under CI/piped invocations.
		TTY: tty,
	}, nil
}

// checkMountPaths rejects any byre-owned bind source that a docker --mount value
// (comma-separated key=value pairs) cannot express. Covers the workspace bind
// and, for a worktree, the same-path git binds — all set by byre, not the user.
func checkMountPaths(paths project.Paths) error {
	for _, p := range []string{paths.WorkDir, paths.CommonGitDir, paths.CommonGitDirHost} {
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
