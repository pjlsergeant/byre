package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	// Under an allowlist posture, hand the box the enforced allowlist so its
	// launcher can announce it in agent memory (the firewall context points
	// there — legibility runs inward). Same resolvedEgress string the netns
	// helper enforces, so announcement and enforcement share one source; set
	// AFTER the host/config env so no [env] key can skew what the box is told
	// byre enforces. Open-denylist boxes have no allowlist to announce.
	if p, _ := rv.skills.NetworkPosture(); config.PostureEnforcesAllowlist(p) {
		env["BYRE_EGRESS"] = strings.Join(resolvedEgress(rv), " ")
	}

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
		if err := checkContainedHostSource(host, paths.WorkDir); err != nil {
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
		Labels:          []string{projectLabel(paths), workdirLabel(paths), clientKey + "=" + strconv.Itoa(os.Getpid())},
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
// and, for a worktree, the git binds (the worktree's same-path bind plus the
// common git dir's source AND target, which differ when the recorded path
// contains symlinks) — all set by byre, not the user.
func checkMountPaths(paths project.Paths) error {
	for _, p := range []string{paths.WorkDir, paths.CommonGitDir, paths.CommonGitDirHost} {
		if strings.Contains(p, ",") {
			return fmt.Errorf("path contains a comma, which docker --mount cannot express: %q", p)
		}
	}
	return nil
}

// checkContainedHostSource guards a mount/seed source that lives INSIDE the
// agent-writable project tree: between sessions the agent can replace such a
// path (or an interior component) with a symlink, so byre must not blindly hand
// the engine a source that now escapes the tree -- e.g. a configured
// <project>/data retargeted to ~/.ssh. A source OUTSIDE the tree is the user's
// own host choice and is left untouched (footgun doctrine -- no nannying). For
// an in-tree source that exists, resolve it and require the real path to stay
// within the tree; refuse an escape. It deliberately does NOT rebind to the
// resolved target -- mounting that would COMPLETE the exfiltration the config
// never named. There is no adversary in the develop-time check-to-mount window:
// the prior session has ended and the new box has not started.
func checkContainedHostSource(host, workDir string) error {
	host = filepath.Clean(host)
	if !inTreeByIdentity(workDir, host) {
		return nil // genuinely outside the tree -- the user's own host path
	}
	resolved, err := filepath.EvalSymlinks(host)
	if err != nil {
		if os.IsNotExist(err) {
			// Nothing exists at host -- including a dangling symlink or a dangling
			// interior component. Passing it on is safe ONLY because binds ride
			// `--mount type=bind` (runner.RunArgs), which REFUSES a missing source:
			// the daemon resolves the path, finds nothing, and the run fails loudly
			// with nothing created host-side. `-v` would instead auto-create the
			// resolved target (as root, wherever a dangling link points), so if
			// binds ever switch to -v this branch must refuse instead.
			return nil
		}
		return fmt.Errorf("host source %q under the project tree could not be resolved (a swapped or broken symlink?): %w", host, err)
	}
	if !inTreeByIdentity(workDir, resolved) {
		return fmt.Errorf("host source %q resolves to %q, outside the project tree — refusing to mount or seed it (an agent may have retargeted a path the config named inside the project; byre won't follow it out)", host, resolved)
	}
	return nil
}

// inTreeByIdentity reports whether p denotes workDir or a descendant of it,
// judged by FILE IDENTITY -- os.SameFile against workDir over real ancestor
// chains -- never by spelling. A lexical comparison misclassifies on a
// case-insensitive filesystem (macOS APFS): a case-variant spelling of an
// in-tree path reads as "outside", skipping the escape check entirely.
//
// Each spelled ancestor of p (deepest first) is resolved and its OWN real
// ancestor chain is compared against workDir (identityUnder). Two chains are
// required, not one: a lexically-in-tree spelling whose interior component
// escapes (<tree>/via/x with via -> /outside) only meets the tree at the
// spelled ancestor <tree> itself, while an alias into a SUBDIRECTORY
// (/tmp/link/data with /tmp/link -> <tree>/subdir) never spells the tree and
// only meets it on the resolved chain above subdir (codex review: comparing
// identity with the root alone missed exactly this alias). Missing or
// unresolvable components (ENOENT, ELOOP) just walk upward; identityUnder
// stats but never opens, so an agent-planted FIFO can't hang the walk (the
// hostopen concern). The final-component escape is judged by the caller on
// the EvalSymlinks'd path, not here. If workDir itself can't be stat'd,
// degrade to the lexical judgment -- develop is about to fail on the
// workspace bind anyway.
func inTreeByIdentity(workDir, p string) bool {
	wd, err := os.Stat(workDir)
	if err != nil {
		return underTree(workDir, p)
	}
	for q := filepath.Clean(p); ; {
		if identityUnder(wd, q) {
			return true
		}
		parent := filepath.Dir(q)
		if parent == q {
			return false // no spelled ancestor resolves into the tree
		}
		q = parent
	}
}

// identityUnder reports whether q resolves to wd or to a descendant of it: q
// is canonicalized (so its lexical ancestors ARE its real ones -- ".." after a
// symlink component would otherwise be resolved against the wrong parent) and
// each ancestor is identity-compared to wd. A q that doesn't exist or can't
// resolve is simply not evidence of containment (false); the caller keeps
// walking its spelled ancestors.
func identityUnder(wd os.FileInfo, q string) bool {
	resolved, err := filepath.EvalSymlinks(q)
	if err != nil {
		return false
	}
	for cur := resolved; ; {
		if fi, ferr := os.Stat(cur); ferr == nil && os.SameFile(wd, fi) {
			return true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
	}
}

// underTree reports whether p is workDir itself or a descendant of it. Both are
// cleaned by filepath.Rel; a p outside workDir yields a rel path that is ".." or
// begins "../".
func underTree(workDir, p string) bool {
	rel, err := filepath.Rel(workDir, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// expandHostPath expands a leading ~, requires the result to be absolute (so a
// relative or home-relative mount host can't be misread by the engine), and
// CLEANS it. Cleaning here is load-bearing, not cosmetic: the cleaned form is
// the ONE spelling both the containment check and the engine receive. Left
// unclean, `<tree>/link/../x` would be VALIDATED on its lexical collapse
// (`<tree>/x`) while the daemon resolves `link` FIRST and applies `..` to the
// link's target -- a checker/use mismatch an agent could aim through a
// retargeted `link` (codex review). byre defines a mount/seed source as its
// cleaned spelling; the check and the mount then agree by construction.
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
	return filepath.Clean(p), nil
}
