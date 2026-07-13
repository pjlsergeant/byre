// Package build assembles the docker build context for a project: the generated
// Dockerfile, the launcher script, and any skill/agent files COPYed by the
// generated build. Keeping context assembly here keeps the generator (text) and
// the runner (exec) free of filesystem layout concerns.
package build

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// fileCopy is one staged copy job: a real source path, its destination inside
// the build context, and a description for error messages.
type fileCopy struct{ src, staged, what string }

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
	}
	return in, append(fileJobs, skillJobs...), nil
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
	for _, d := range []string{"files", "skills"} {
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
		if err := copyPath(j.src, j.staged); err != nil {
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
		realSrc, rel, err := safeProjectPath(paths.Canonical, src)
		if err != nil {
			return nil, nil, fmt.Errorf("files: %w", err)
		}
		staged := filepath.Join(paths.ContextDir, "files", rel)
		jobs = append(jobs, fileCopy{src: realSrc, staged: staged, what: "files: copying " + src})
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

// copyPath copies a file or directory tree, preserving file modes. Symlinks are
// rejected (Lstat) so a symlink nested in a staged directory can't pull a file
// from outside the project into the image.
func copyPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symlink %s not allowed in `files` (copy plain files/dirs)", src)
	}
	if info.IsDir() {
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyPath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	o, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer o.Close()
	_, err = io.Copy(o, in)
	return err
}
