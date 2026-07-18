// Package build assembles the docker build context for a project: the generated
// Dockerfile, the launcher script, and any skill/agent files COPYed by the
// generated build. Keeping context assembly here keeps the generator (text) and
// the runner (exec) free of filesystem layout concerns.
package build

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// fileCopy is one staged copy job: a source, its destination inside the build
// context, and a description for error messages.
type fileCopy struct {
	// srcRoot, when non-empty, is a TRUSTED root directory that src is resolved
	// relative to via openat (os.Root), so an intermediate component an agent
	// swaps for an escaping symlink after validation cannot redirect the open.
	// planFiles sets it (sources are always project-root relative). When empty,
	// src is an ABSOLUTE pathname; stageCopy still routes it through the project
	// root if it lands inside the agent-writable project, and only uses the
	// by-pathname copyPath for a source genuinely outside it.
	srcRoot string
	src     string
	staged  string
	what    string
}

// buildInput computes the generator input for a project WITHOUT writing anything
// (it reads and validates sources, but stages no bytes) and returns the copy
// jobs that would populate the context. Assemble and Render share it so the
// rendered Dockerfile always matches what a build would actually use.
func buildInput(paths project.Paths, cfg config.Config, res skills.Resolved) (gen.Input, []fileCopy, error) {
	// `files` copies host paths into the image: map each source to its staged
	// context path (so the generated COPY can find it) and record the copy job.
	genFiles, fileJobs, err := planFiles(paths, cfg.Files)
	if err != nil {
		return gen.Input{}, nil, err
	}
	// Skills can ship files from their own dir into the image: map each skill's
	// build block to the generator's, filling its COPY map, and record the jobs.
	genSkills, skillJobs, err := planSkillBlocks(paths, res.BuildBlocks())
	if err != nil {
		return gen.Input{}, nil, err
	}
	// The declared Claude Skill set: validate each source dir as a Claude
	// Skill and stage it under the canonical context tree (the COPY itself is
	// unconditional — gen always emits it, Assemble always creates the tree).
	claudeSkillJobs, err := planClaudeSkills(paths, cfg, res)
	if err != nil {
		return gen.Input{}, nil, err
	}
	in := gen.Input{
		Base:         cfg.Base,
		Env:          cfg.Env,
		Files:        genFiles,
		Apt:          cfg.Apt,
		NpmGlobal:    cfg.NpmGlobal,
		Skills:       genSkills,
		AgentCmd:     res.AgentCommand() != "",
		AgentContext: true, // the chassis paragraph makes context non-empty on every box
		// Bake the target (and the self-edit note) whenever the agent declares
		// where it reads memory — even with no skill context — so the launcher can
		// still place a --self-edit note there.
		AgentContextTarget: res.AgentContextTarget() != "",
		VolumeDirs:         volumeDirs(cfg.Volumes, res.Volumes()),
		DockerfilePre:      cfg.DockerfilePre,
		DockerfilePost:     cfg.DockerfilePost,
		Guard:              planGuard(genSkills, res),
	}
	return in, append(append(fileJobs, skillJobs...), claudeSkillJobs...), nil
}

// planGuard derives the security-critical files gen re-asserts at the Dockerfile
// tail (beyond the launcher, which gen re-COPYs unconditionally). Scope: only a
// network-posture skill contributes here — its launch gate and netns
// enforcement script, which a project `files` clobber could otherwise empty or
// stub while status still reads deny-by-default. The set is DERIVED from the
// resolved skills (res.NetnsInits gives the script path(s); the gate path is the
// launcher's constant), so a future posture skill is covered without editing a
// hardcoded list. Each guarded dest's staged source is looked up from the skill
// files already planned — byre only re-asserts a file it itself staged.
func planGuard(genSkills []gen.SkillBlock, res skills.Resolved) []gen.GuardFile {
	hooks := res.NetnsInits()
	if len(hooks) == 0 {
		return nil
	}
	byDest := map[string]string{} // image dest -> staged context path
	for _, s := range genSkills {
		for staged, dest := range s.Files {
			byDest[dest] = staged
		}
	}
	var guard []gen.GuardFile
	add := func(dest string, exec bool) {
		if staged, ok := byDest[dest]; ok {
			guard = append(guard, gen.GuardFile{Staged: staged, Dest: dest, Exec: exec})
		}
	}
	// The gate the launcher waits on: re-assert so an empty-file clobber can't
	// make the launcher skip the wait (a `-s` test that then fails open).
	add(gen.LaunchGatePath, false)
	// The netns enforcement script(s) the helper runs as its entrypoint from THIS
	// image: re-assert so a clobber can't swap in a rules-free stub. Resolution
	// rejects a second netns_init, so this stays deterministic (single hook).
	for _, h := range hooks {
		add(h.Path, true)
	}
	return guard
}

// Render returns the generated Dockerfile text WITHOUT touching the build
// context on disk. `byre dockerfile` is informational and side-effect-free, so
// it must not clear-and-restage the context (which Assemble does) — that would
// race a concurrent `byre develop` build sharing the same context dir.
func Render(paths project.Paths, cfg config.Config, res skills.Resolved) (string, error) {
	in, _, err := buildInput(paths, cfg, res)
	if err != nil {
		return "", err
	}
	return gen.Dockerfile(in), nil
}

// Assemble writes the build context (Dockerfile + launcher + agent files + any
// `files`) and returns the generated Dockerfile text.
func Assemble(paths project.Paths, cfg config.Config, res skills.Resolved) (string, error) {
	// Re-stage from scratch: clear the staging subtrees so a file removed from
	// `files`/skills since the last build can't linger in the context and make the
	// build nondeterministic (or get swept into the image).
	for _, d := range []string{"files", "skills", gen.ClaudeSkillsDirName} {
		if err := os.RemoveAll(filepath.Join(paths.ContextDir, d)); err != nil {
			return "", err
		}
	}
	// Same for the conditional context files: each is written only when its
	// condition holds, so a condition that turned false since the last build
	// (agent removed, context emptied) would otherwise leave a stale file behind.
	for _, name := range []string{gen.AgentCmdName, gen.AgentContextName, gen.AgentContextTargetName, gen.SelfEditDocName} {
		if err := os.Remove(ctxPath(paths, name)); err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}

	in, jobs, err := buildInput(paths, cfg, res)
	if err != nil {
		return "", err
	}
	for _, j := range jobs {
		if err := os.MkdirAll(filepath.Dir(j.staged), 0o755); err != nil {
			return "", err
		}
		if err := stageCopy(paths.WorkDir, j); err != nil {
			return "", fmt.Errorf("%s: %w", j.what, err)
		}
	}
	df := gen.Dockerfile(in)

	if err := os.WriteFile(paths.Dockerfile, []byte(df), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(ctxPath(paths, gen.LauncherName), gen.LauncherScript(), 0o755); err != nil {
		return "", err
	}
	// The /etc/profile.d shim that sources env.d for login shells (COPYed by the
	// core block); 0644, sourced not executed.
	if err := os.WriteFile(ctxPath(paths, gen.ProfileEnvName), gen.ProfileEnvScript(), 0o644); err != nil {
		return "", err
	}
	if cmd := res.AgentCommand(); cmd != "" {
		if err := os.WriteFile(ctxPath(paths, gen.AgentCmdName), agentScript(cmd), 0o755); err != nil {
			return "", err
		}
	}
	// The canonical Claude Skill tree root — created on every assemble even
	// when the declared set is empty (the COPY is unconditional, and claude's
	// --add-dir tolerates an empty skills dir; spike-verified), so the baked
	// path exists in every box. The skill dirs themselves were staged as jobs.
	if err := os.MkdirAll(filepath.Join(paths.ContextDir, gen.ClaudeSkillsDirName, ".claude", "skills"), 0o755); err != nil {
		return "", err
	}
	// The canonical declared MCP set — written on every assemble (the COPY is
	// unconditional), empty set included, so /etc/byre/mcp.json exists in
	// every box and an agent command can inject it unconditionally. resolve()
	// already rejected cross-source duplicates; recomputing here keeps
	// Assemble correct for callers that didn't.
	mcps, err := skills.MCPSet(cfg, res)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(ctxPath(paths, gen.MCPConfigName), config.MCPConfigJSON(skills.MCPList(mcps)), 0o644); err != nil {
		return "", err
	}
	// The chassis speaks first: mechanism facts every box carries (today, the
	// deliver inbox), then the skills' opinions in enable order. Chassis text
	// is a mechanism description like /workspace — not a skill's opinion — so
	// it rides every box, not a skill toggle (ADR 0021).
	ctx := chassisContext
	if sc := res.Context(); sc != "" {
		ctx += "\n\n" + sc
	}
	if err := os.WriteFile(ctxPath(paths, gen.AgentContextName), []byte(ctx), 0o644); err != nil {
		return "", err
	}
	if target := res.AgentContextTarget(); target != "" {
		if err := os.WriteFile(ctxPath(paths, gen.AgentContextTargetName), []byte(target+"\n"), 0o644); err != nil {
			return "", err
		}
		// The --self-edit note the launcher appends to the agent's memory when the
		// self-edit mount (this project's store at /home/dev/.byre-self) is present.
		if err := os.WriteFile(ctxPath(paths, gen.SelfEditDocName), []byte(selfEditDoc), 0o644); err != nil {
			return "", err
		}
	}
	return df, nil
}

// chassisContext is the byre-mechanism paragraph every box's agent context
// carries — facts about the box itself, not workflow opinions (those belong
// to skills). One sentence per mechanism; keep it lean.
const chassisContext = "Files the user delivers from the host land in /inbox, owned by you. The inbox is ephemeral (it dies with the container) — treat it as a hand-off point, not storage."

// selfEditDoc is placed into the agent's memory only when a session is started
// with --self-edit, so the agent knows it can edit its own byre sandbox config —
// including the actual config-key vocabulary so it doesn't have to guess.
//
//go:embed self-edit.md
var selfEditDoc string

func ctxPath(paths project.Paths, name string) string {
	return filepath.Join(paths.ContextDir, name)
}

// volumeDirs returns the mount-point dirs of all named volumes (config-declared
// and skill-contributed), so gen can pre-create them owned by the baked UID/GID —
// a fresh Docker named volume inherits the image dir's ownership at its mount
// point. gen de-dups, so overlap between the two sources is fine.
func volumeDirs(volSets ...[]config.Volume) []string {
	var dirs []string
	for _, vols := range volSets {
		for _, v := range vols {
			if v.Target != "" {
				dirs = append(dirs, v.Target)
			}
		}
	}
	return dirs
}

// agentScript wraps the agent's launch command in an executable shell script, so
// the launcher execs it (preserving quoting) rather than word-splitting text.
// The command is DELIBERATELY an unvalidated shell fragment (flags ride in it:
// "claude --dangerously-skip-permissions"): it comes only from a skill.toml the
// user enabled, and an enabled skill already runs anything it likes via raw
// [build].dockerfile lines and launch hooks. The typed-field allowlists are
// legibility, not containment (skills.go, docs/SECURITY.md "A skill is trusted
// code"); quoting this field would contain nothing.
func agentScript(command string) []byte {
	return []byte("#!/bin/sh\nexec " + command + " \"$@\"\n")
}

// planFiles maps each `files` source (a path relative to the project dir) to its
// staged context path under "files/<src>", returning the COPY map the generator
// emits (staged-context-path -> image destination) and the copy jobs to realize
// it. It validates sources (no absolute paths, no "../" or symlink escapes) and
// destinations (absolute image paths) but writes nothing — the caller stages the
// jobs (Assemble) or discards them (Render).
func planFiles(paths project.Paths, files map[string]string) (map[string]string, []fileCopy, error) {
	if len(files) == 0 {
		return nil, nil, nil
	}
	out := make(map[string]string, len(files))
	var jobs []fileCopy
	for src, dest := range files {
		if !filepath.IsAbs(dest) {
			return nil, nil, fmt.Errorf("files: destination %q must be an absolute path in the image", dest)
		}
		_, rel, err := safeProjectPath(paths.Canonical, src)
		if err != nil {
			return nil, nil, fmt.Errorf("files: %w", err)
		}
		staged := filepath.Join(paths.ContextDir, "files", rel)
		// Anchor at the project root and stage rel through it (openat), rather
		// than by the resolved absolute path: the ancestors of a `files` source
		// are agent-writable, so a by-pathname reopen could be redirected by an
		// ancestor swapped to a symlink after safeProjectPath validated it.
		jobs = append(jobs, fileCopy{srcRoot: paths.Canonical, src: rel, staged: staged, what: "files: copying " + src})
		out[filepath.ToSlash(filepath.Join("files", rel))] = dest
	}
	return out, jobs, nil
}

// planSkillBlocks maps each skill's build block onto the generator's, filling
// its COPY map (staged-context-path -> image dest) for files the skill ships
// under "skills/<skill>/<rel>", and returns the copy jobs. Sources were
// already validated for containment by skills.Resolve; this writes nothing.
func planSkillBlocks(paths project.Paths, blocks []skills.BuildBlock) ([]gen.SkillBlock, []fileCopy, error) {
	if len(blocks) == 0 {
		return nil, nil, nil
	}
	out := make([]gen.SkillBlock, 0, len(blocks))
	var jobs []fileCopy
	for _, b := range blocks {
		gb := gen.SkillBlock{Name: b.Name, Apt: b.Apt, NpmGlobal: b.NpmGlobal, Dockerfile: b.Dockerfile}
		for _, sf := range b.Files {
			ctxRel := filepath.ToSlash(filepath.Join("skills", b.Name, sf.Rel))
			staged := filepath.Join(paths.ContextDir, filepath.FromSlash(ctxRel))
			// Defense-in-depth: skill name + rel are validated upstream, but confirm
			// the staged path can't escape the context dir before we write to it.
			if rel, err := filepath.Rel(paths.ContextDir, staged); err != nil ||
				rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil, nil, fmt.Errorf("skill %q: staged file path escapes the build context", b.Name)
			}
			jobs = append(jobs, fileCopy{src: sf.Src, staged: staged, what: fmt.Sprintf("skill %q files: copying %s", b.Name, sf.Rel)})
			if gb.Files == nil {
				gb.Files = make(map[string]string)
			}
			gb.Files[ctxRel] = sf.Dest
		}
		out = append(out, gb)
	}
	return out, jobs, nil
}

// planClaudeSkills stages the effective Claude Skill set under the canonical
// context tree ("claude-skills/.claude/skills/<name>"), validating each source
// dir as a Claude Skill first (skills.ValidateClaudeSkillDir — SKILL.md,
// frontmatter, bounds; one owner for both homes). A skill contribution's
// source dir was already resolved and containment-checked by Resolve; a
// config declaration's `path` expands here (`~`-anchored or absolute — config
// vocabulary is deliberately wider than the project-relative `files` key, see
// config/claudeskills.go). Staging itself rejects symlinks (copyPath).
func planClaudeSkills(paths project.Paths, cfg config.Config, res skills.Resolved) ([]fileCopy, error) {
	set, err := skills.ClaudeSkillSet(cfg, res)
	if err != nil {
		return nil, err
	}
	var jobs []fileCopy
	for _, d := range set {
		src := d.SrcDir
		if src == "" { // a config declaration: expand its host path
			if src, err = expandHome(d.CS.Path); err != nil {
				return nil, fmt.Errorf("claude skill %s: %w", d.CS.Name, err)
			}
		}
		if err := skills.ValidateClaudeSkillDir(src, d.CS.Name); err != nil {
			return nil, err
		}
		staged := filepath.Join(paths.ContextDir, gen.ClaudeSkillsDirName, ".claude", "skills", d.CS.Name)
		jobs = append(jobs, fileCopy{src: src, staged: staged, what: fmt.Sprintf("claude skill %s: copying %s", d.CS.Name, src)})
	}
	return jobs, nil
}

// expandHome expands a leading ~ against the current user's home and requires
// the result to be absolute (the shape config validation promised).
func expandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = home + strings.TrimPrefix(p, "~")
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("path must be absolute or ~/…: %q", p)
	}
	return p, nil
}

// safeProjectPath resolves src (relative to projectDir) and confirms — after
// symlink resolution — that it stays within projectDir. Returns the real source
// path and the cleaned relative path.
func safeProjectPath(projectDir, src string) (real, rel string, err error) {
	if filepath.IsAbs(src) {
		return "", "", fmt.Errorf("source %q must be relative to the project dir", src)
	}
	clean := filepath.Clean(src)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("source %q escapes the project dir", src)
	}
	realDir, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return "", "", err
	}
	real, err = filepath.EvalSymlinks(filepath.Join(realDir, clean))
	if err != nil {
		return "", "", err
	}
	within, err := filepath.Rel(realDir, real)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("source %q escapes the project dir via symlink", src)
	}
	return real, clean, nil
}

// stageCopy realizes one copy job, anchoring the source at the agent-writable
// tree (agentRoot = WorkDir) whenever it lives inside it, so no agent-swappable
// ancestor is followed by pathname. A job may declare srcRoot itself (planFiles,
// always project-root relative); otherwise an ABSOLUTE src is routed here: if it
// resolves inside agentRoot (e.g. a project-local `[[claude_skills]].path`), it
// is anchored there too; only a source genuinely OUTSIDE the agent-writable tree
// (skills shipped from elsewhere on the host — the main worktree of a linked
// worktree is not agent-writable) falls through to the by-pathname copyPath.
// This ENFORCES — rather than assumes — that no by-pathname reopen happens for
// an agent-writable source.
func stageCopy(agentRoot string, j fileCopy) error {
	root, src := j.srcRoot, j.src
	if root == "" {
		if rel, ok := agentWritableRel(agentRoot, j.src); ok {
			root, src = agentRoot, rel
		}
	}
	if root == "" {
		return copyPath(j.src, j.staged)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	// The configured source itself (top level) may be a symlink the USER named;
	// safeProjectPath / validation already resolved it within the project, and
	// os.Root follows it while refusing escapes. Its interior is agent territory:
	// symlinks there are rejected (copyRootedEntry with topLevel=false).
	return copyRootedEntry(r, src, j.staged, true)
}

// agentWritableRel reports whether path is inside root, returning the relative
// path to anchor it at. It tries the LEXICAL spelling first: a path already
// spelled under root is anchored (openat), so an escaping intermediate component
// is REFUSED by os.Root rather than demoted to the by-pathname route — resolving
// first would send exactly that case to copyPath, since EvalSymlinks would land
// outside root. Only if the lexical spelling misses does it EvalSymlinks, to
// still catch a source spelled through a symlink alias of root (expandHome does
// not canonicalize; root — WorkDir — is already Canonicalize'd). Either way the
// resolve only ROUTES; os.Root re-resolves at open time.
func agentWritableRel(root, path string) (string, bool) {
	if rel, ok := withinRoot(root, path); ok {
		return rel, true
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return withinRoot(root, resolved)
	}
	return "", false
}

// withinRoot reports whether path (absolute) lies inside root (absolute,
// cleaned), returning the cleaned relative path if so. Purely lexical — the
// routing decision; os.Root enforces the actual openat containment, so a
// symlink under a lexically-contained path still cannot escape.
func withinRoot(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// copyRootedEntry copies the entry at rel (relative to root) into dst, opening
// every object THROUGH root (openat per component) so no pathname is re-resolved
// between classification and use and no component can escape the root. topLevel
// is the configured source itself — a user-named symlink there is followed;
// interior entries reject symlinks (agent-planted). The fd's fstat is the only
// thing trusted for the entry's type, and opens are O_NONBLOCK so a FIFO returns
// instead of blocking. Mirrors internal/deliver/transport.go.
func copyRootedEntry(root *os.Root, rel, dst string, topLevel bool) error {
	if !topLevel {
		// Lstat through the root to reject an interior symlink WITHOUT following
		// it: os.Root silently follows an in-root symlink on Open, and copyPath's
		// contract stages no symlinks (an escaping one is refused by the root
		// regardless; this also rejects an in-root one rather than dereferencing
		// it into the image).
		//
		// Residual (accepted): between this Lstat and the open below, an agent
		// could swap the entry to an in-root symlink, which os.Root would then
		// follow. That stages a DIFFERENT in-project file — content the agent
		// already controls, going into its own image — never a host-file escape
		// (an escaping swap is still refused by os.Root's openat). No gain to the
		// agent, so it is not worth openat+O_NOFOLLOW-per-component machinery
		// that os.Root does not expose.
		li, err := root.Lstat(rel)
		if err != nil {
			return err
		}
		if li.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink %s not allowed in `files` (copy plain files/dirs)", filepath.Join(root.Name(), rel))
		}
	}
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	switch {
	case fi.IsDir():
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		entries, err := f.ReadDir(-1)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyRootedEntry(root, filepath.Join(rel, e.Name()), filepath.Join(dst, e.Name()), false); err != nil {
				return err
			}
		}
		return nil
	case fi.Mode().IsRegular():
		return stageRegularFromFD(f, dst)
	default:
		return fmt.Errorf("%s is not a regular file (only plain files/dirs may be staged in `files`)", filepath.Join(root.Name(), rel))
	}
}

// copyPath copies a file or directory tree named by an ABSOLUTE pathname
// (skill sources, whose ancestors are outside the agent-writable project;
// `files` sources go through stageCopy/copyRootedEntry instead, anchored at the
// project root). Only plain files and directories are staged; symlinks and other
// non-regular files (FIFOs, devices, sockets) are rejected, so nothing can pull
// content from outside into the image or stall the rebuild.
//
// The project stays writable while a session runs, and `byre rebuild` can stage
// a context concurrently, so an agent can swap an entry between classification
// and copy (the check/open race). copyPath is race-hardened accordingly:
// interior entries of a directory are opened THROUGH an os.Root anchored at the
// directory (openat per component, never a re-walked pathname), which refuses
// any component that resolves outside the root — so a regular file swapped for
// an escaping symlink after classification cannot pull an external file in.
// Opens are O_NONBLOCK and the type is trusted only from the fd's fstat, so a
// swap to a FIFO returns instead of hanging and is rejected rather than staged.
// This mirrors internal/deliver/transport.go.
func copyPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symlink %s not allowed in `files` (copy plain files/dirs)", src)
	}
	if info.IsDir() {
		// Anchor a root at the directory (no-follow: a dir swapped to a symlink
		// after the Lstat above must not re-anchor the walk elsewhere) and copy
		// its interior through it. A concurrent swap of a deeper directory
		// component to an escaping symlink is refused by os.Root's
		// per-component openat, not re-resolved by name.
		root, err := hostopen.OpenDirRootNoFollow(src)
		if err != nil {
			if errors.Is(err, hostopen.ErrSymlinkRoot) {
				// copyPath's contract stages no symlinks; keep the `files` language.
				return fmt.Errorf("symlink %s not allowed in `files` (copy plain files/dirs)", src)
			}
			return err
		}
		defer root.Close()
		return copyRootedEntry(root, ".", dst, false)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file (only plain files/dirs may be staged in `files`)", src)
	}
	// Reopen the top-level file with O_NOFOLLOW|O_NONBLOCK and trust the fd's
	// stat, so a swap to a symlink or FIFO between the Lstat above and here is
	// rejected rather than followed or blocked on.
	in, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	return stageRegularFromFD(in, dst)
}

// stageRegularFromFD copies an already-open source file into dst, preserving its
// permission bits. It re-checks the fd's type so no pathname is re-resolved: the
// top-level caller opens by name (a swap to a FIFO with O_NONBLOCK, or to a
// directory, opens successfully), so trusting the fd's fstat here — not a prior
// pathname Lstat — is what actually keeps a non-regular file out of the image.
//
// The copy is bounded at the size this fstat observed: the source is
// agent-writable, and an unbounded io.Copy of a file being appended to chases
// the writer indefinitely (the same stall class O_NONBLOCK closes for FIFOs).
// A source that grew or shrank mid-copy is refused rather than staged — the
// bytes would be a torn read either way, and the context must be deterministic.
func stageRegularFromFD(in *os.File, dst string) error {
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file (only plain files/dirs may be staged in `files`)", in.Name())
	}
	o, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm()&fs.ModePerm)
	if err != nil {
		return err
	}
	defer o.Close()
	return copyExactly(o, in, fi.Size(), in.Name())
}

// copyExactly copies exactly size bytes from in to out, refusing a source that
// holds more or fewer. The limit makes a shrink visible (the copy falls short)
// but hides growth — one read past the promise tells them apart. Mirrors
// deliver's send-time check (internal/deliver/remote.go).
func copyExactly(out io.Writer, in io.Reader, size int64, name string) error {
	n, err := io.Copy(out, io.LimitReader(in, size))
	if err != nil {
		return err
	}
	var extra int
	if n == size {
		// ReadFull, not a bare Read: it loops past a legal zero-byte read, and a
		// probe that fails outright must surface, not pass as "didn't grow".
		var b [1]byte
		var rerr error
		extra, rerr = io.ReadFull(in, b[:])
		if rerr != nil && rerr != io.EOF {
			return fmt.Errorf("%s: checking for growth past the observed size: %w", name, rerr)
		}
	}
	if n != size || extra > 0 {
		return fmt.Errorf("%s changed while being staged (observed %d bytes)", name, size)
	}
	return nil
}
