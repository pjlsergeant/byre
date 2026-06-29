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

	"byre/internal/config"
	"byre/internal/gen"
	"byre/internal/project"
	"byre/internal/skills"
)

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

	// `files` copies host paths into the image: stage each source into the build
	// context (so the generated COPY can find it) and render COPY <staged> <dest>.
	genFiles, err := stageFiles(paths, cfg.Files)
	if err != nil {
		return "", err
	}

	// Skills can ship files from their own dir into the image: stage each into the
	// build context and fill in the matching skill block's COPY map.
	genSkills, err := stageSkillFiles(paths, res.SkillBlocks, res.SkillFiles)
	if err != nil {
		return "", err
	}

	volDirs := volumeDirs(cfg.Volumes, res.Volumes)
	in := gen.Input{
		Base:         cfg.Base,
		Env:          cfg.Env,
		Files:        genFiles,
		Apt:          cfg.Apt,
		NpmGlobal:    cfg.NpmGlobal,
		Skills:       genSkills,
		AgentCmd:     res.AgentCommand != "",
		AgentContext: res.Context != "",
		// Bake the target (and the self-edit note) whenever the agent declares
		// where it reads memory — even with no skill context — so the launcher can
		// still place a --self-edit note there.
		AgentContextTarget: res.AgentContextTarget != "",
		VolumeDirs:         volDirs,
		DockerfilePre:      cfg.DockerfilePre,
		DockerfilePost:     cfg.DockerfilePost,
	}
	df := gen.Dockerfile(in)

	if err := os.WriteFile(paths.Dockerfile, []byte(df), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(ctxPath(paths, gen.LauncherName), gen.LauncherScript(), 0o755); err != nil {
		return "", err
	}
	if res.AgentCommand != "" {
		if err := os.WriteFile(ctxPath(paths, gen.AgentCmdName), agentScript(res.AgentCommand), 0o755); err != nil {
			return "", err
		}
	}
	if res.Context != "" {
		if err := os.WriteFile(ctxPath(paths, gen.AgentContextName), []byte(res.Context), 0o644); err != nil {
			return "", err
		}
	}
	if res.AgentContextTarget != "" {
		if err := os.WriteFile(ctxPath(paths, gen.AgentContextTargetName), []byte(res.AgentContextTarget+"\n"), 0o644); err != nil {
			return "", err
		}
		// The --self-edit note the launcher appends to the agent's memory when the
		// self-edit mount (this project's store at /home/dev/.byre-self) is present.
		if err := os.WriteFile(ctxPath(paths, gen.SelfEditDocName), []byte(selfEditDoc), 0o644); err != nil {
			return "", err
		}
	}
	// Bake the volume mount-point list so the launcher can re-own them to the
	// runtime user (sorted + deduped to match the Dockerfile's mkdir/chown).
	if dirs := gen.SortedUnique(volDirs); len(dirs) > 0 {
		if err := os.WriteFile(ctxPath(paths, gen.VolumeDirsName), []byte(strings.Join(dirs, "\n")+"\n"), 0o644); err != nil {
			return "", err
		}
	}
	return df, nil
}

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
// and skill-contributed), so gen can pre-create them dev-owned — a fresh Docker
// named volume inherits the image dir's ownership at its mount point. gen
// de-dups, so overlap between the two sources is fine.
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
func agentScript(command string) []byte {
	return []byte("#!/bin/sh\nexec " + command + " \"$@\"\n")
}

// stageFiles copies each `files` source (a path relative to the project dir)
// into the build context under "files/<src>", and returns the COPY map the
// generator should emit (staged-context-path -> image destination). Sources must
// stay within the project dir (no absolute paths, no "../" or symlink escapes);
// destinations must be absolute image paths.
func stageFiles(paths project.Paths, files map[string]string) (map[string]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(files))
	for src, dest := range files {
		if !filepath.IsAbs(dest) {
			return nil, fmt.Errorf("files: destination %q must be an absolute path in the image", dest)
		}
		realSrc, rel, err := safeProjectPath(paths.Canonical, src)
		if err != nil {
			return nil, fmt.Errorf("files: %w", err)
		}
		staged := filepath.Join(paths.ContextDir, "files", rel)
		if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
			return nil, err
		}
		if err := copyPath(realSrc, staged); err != nil {
			return nil, fmt.Errorf("files: copying %s: %w", src, err)
		}
		out[filepath.ToSlash(filepath.Join("files", rel))] = dest
	}
	return out, nil
}

// stageSkillFiles copies each skill's shipped files into the build context under
// "skills/<skill>/<rel>" and returns a copy of the skill blocks with their COPY
// maps (staged-context-path -> image dest) filled in. Sources were already
// validated for containment by skills.Resolve.
func stageSkillFiles(paths project.Paths, blocks []gen.SkillBlock, files []skills.SkillFile) ([]gen.SkillBlock, error) {
	if len(files) == 0 {
		return blocks, nil
	}
	copies := make(map[string]map[string]string) // skill -> (ctxPath -> dest)
	for _, sf := range files {
		ctxRel := filepath.ToSlash(filepath.Join("skills", sf.Skill, sf.Rel))
		staged := filepath.Join(paths.ContextDir, filepath.FromSlash(ctxRel))
		// Defense-in-depth: skill name + rel are validated upstream, but confirm
		// the staged path can't escape the context dir before we write to it.
		if rel, err := filepath.Rel(paths.ContextDir, staged); err != nil ||
			rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("skill %q: staged file path escapes the build context", sf.Skill)
		}
		if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
			return nil, err
		}
		if err := copyPath(sf.Src, staged); err != nil {
			return nil, fmt.Errorf("skill %q files: copying %s: %w", sf.Skill, sf.Rel, err)
		}
		if copies[sf.Skill] == nil {
			copies[sf.Skill] = make(map[string]string)
		}
		copies[sf.Skill][ctxRel] = sf.Dest
	}
	out := make([]gen.SkillBlock, len(blocks))
	for i, b := range blocks {
		b.Files = copies[b.Name]
		out[i] = b
	}
	return out, nil
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
