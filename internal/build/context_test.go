package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

func bootstrapped(t *testing.T) project.Paths {
	t.Helper()
	t.Setenv("BYRE_HOME", t.TempDir())
	paths, err := project.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestAssembleWritesDockerfileAndLauncher(t *testing.T) {
	paths := bootstrapped(t)

	df, err := Assemble(paths, config.Config{Base: "node:22"}, skills.Resolved{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(df, "FROM node:22") {
		t.Errorf("returned Dockerfile missing base:\n%s", df)
	}

	onDisk, err := os.ReadFile(paths.Dockerfile)
	if err != nil {
		t.Fatalf("Dockerfile not written: %v", err)
	}
	if string(onDisk) != df {
		t.Error("persisted Dockerfile != returned text")
	}

	info, err := os.Stat(paths.ContextDir + "/" + gen.LauncherName)
	if err != nil {
		t.Fatalf("launcher not written: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("launcher not executable: mode %v", info.Mode())
	}
}

func TestAssembleWritesAgentFiles(t *testing.T) {
	paths := bootstrapped(t)
	res := skills.Resolved{
		Skills: []skills.Skill{{Name: "claude", Context: "be concise"}},
		Agent:  &skills.AgentContrib{Command: "claude --dangerously-skip-permissions"},
	}
	df, err := Assemble(paths, config.Config{Base: "node:22"}, res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(df, "COPY agent-cmd /etc/byre/agent-cmd") {
		t.Errorf("agent-cmd COPY missing:\n%s", df)
	}

	script, err := os.ReadFile(paths.ContextDir + "/" + gen.AgentCmdName)
	if err != nil {
		t.Fatalf("agent-cmd not written: %v", err)
	}
	if !strings.Contains(string(script), "exec claude --dangerously-skip-permissions") {
		t.Errorf("agent script wrong: %s", script)
	}
	ctx, err := os.ReadFile(paths.ContextDir + "/" + gen.AgentContextName)
	if err != nil || string(ctx) != "be concise" {
		t.Errorf("agent context wrong: %q %v", ctx, err)
	}
}

func TestAssembleStagesFiles(t *testing.T) {
	paths := bootstrapped(t)
	// Put a source file in the project dir (paths.Canonical).
	if err := os.WriteFile(filepath.Join(paths.Canonical, "seed.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	df, err := Assemble(paths, config.Config{Base: "debian:bookworm", Files: map[string]string{"seed.txt": "/opt/seed.txt"}}, skills.Resolved{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(df, gen.CopyLine("files/seed.txt", "/opt/seed.txt")) {
		t.Errorf("expected staged COPY line:\n%s", df)
	}
	staged := filepath.Join(paths.ContextDir, "files", "seed.txt")
	if b, err := os.ReadFile(staged); err != nil || string(b) != "hello" {
		t.Errorf("file not staged into context: %q %v", b, err)
	}
}

// Render must produce the same Dockerfile text as Assemble but write NOTHING to
// the context dir — `byre dockerfile` is informational and must not race a
// concurrent build that shares the context (Assemble clears+restages files/).
func TestRenderMatchesAssembleWithoutTouchingContext(t *testing.T) {
	cfg := config.Config{Base: "debian:bookworm", Files: map[string]string{"seed.txt": "/opt/seed.txt"}}

	// Assemble on one bootstrapped project to get the reference text.
	ap := bootstrapped(t)
	if err := os.WriteFile(filepath.Join(ap.Canonical, "seed.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := Assemble(ap, cfg, skills.Resolved{})
	if err != nil {
		t.Fatal(err)
	}

	// Render on a fresh project: same text, but the context dir stays empty.
	rp := bootstrapped(t)
	if err := os.WriteFile(filepath.Join(rp.Canonical, "seed.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Render(rp, cfg, skills.Resolved{})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Render text != Assemble text:\n--- render ---\n%s\n--- assemble ---\n%s", got, want)
	}
	if _, err := os.Stat(filepath.Join(rp.ContextDir, "files", "seed.txt")); !os.IsNotExist(err) {
		t.Errorf("Render staged a file into the context dir (should be side-effect-free): %v", err)
	}
	if _, err := os.Stat(rp.Dockerfile); !os.IsNotExist(err) {
		t.Errorf("Render wrote the Dockerfile to disk (should be side-effect-free): %v", err)
	}
}

func TestAssembleFilesRejectsNestedSymlink(t *testing.T) {
	paths := bootstrapped(t)
	// A directory source containing a symlink that points outside the project.
	assets := filepath.Join(paths.Canonical, "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0o644)
	if err := os.Symlink(outside, filepath.Join(assets, "leak")); err != nil {
		t.Fatal(err)
	}
	if _, err := Assemble(paths, config.Config{Base: "debian:bookworm", Files: map[string]string{"assets": "/opt/assets"}}, skills.Resolved{}); err == nil {
		t.Fatal("expected rejection of a symlink nested in a staged directory")
	}
}

func TestAssembleFilesRejectsEscapeAndRelativeDest(t *testing.T) {
	paths := bootstrapped(t)
	if _, err := Assemble(paths, config.Config{Files: map[string]string{"../etc/passwd": "/x"}}, skills.Resolved{}); err == nil {
		t.Error("expected rejection of '..' escaping source")
	}
	os.WriteFile(filepath.Join(paths.Canonical, "f"), []byte("x"), 0o644)
	if _, err := Assemble(paths, config.Config{Files: map[string]string{"f": "relative/dest"}}, skills.Resolved{}); err == nil {
		t.Error("expected rejection of non-absolute destination")
	}
}

func TestAssembleStagesSkillFiles(t *testing.T) {
	paths := bootstrapped(t)
	// A skill file's Src would normally live under the skill dir; for the staging
	// test any readable file works.
	src := filepath.Join(t.TempDir(), "review.sh")
	if err := os.WriteFile(src, []byte("#!/bin/sh\necho review\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := skills.Resolved{Skills: []skills.Skill{{
		Name:  "tools",
		Files: []skills.SkillFile{{Src: src, Rel: "review.sh", Dest: "/usr/local/bin/byre-review"}},
	}}}
	df, err := Assemble(paths, config.Config{Base: "node:22"}, res)
	if err != nil {
		t.Fatal(err)
	}
	// Staged into the context under skills/<skill>/<rel>.
	staged := filepath.Join(paths.ContextDir, "skills", "tools", "review.sh")
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("skill file not staged into context: %v", err)
	}
	// And the generated Dockerfile COPYs it to the destination.
	if !strings.Contains(df, gen.CopyLine("skills/tools/review.sh", "/usr/local/bin/byre-review")) {
		t.Errorf("generated Dockerfile missing skill-file COPY:\n%s", df)
	}
}

func TestAssembleWritesAgentContextTarget(t *testing.T) {
	paths := bootstrapped(t)
	res := skills.Resolved{
		Skills: []skills.Skill{{Name: "claude", Context: "workflow rules"}},
		Agent:  &skills.AgentContrib{ContextTarget: "/home/dev/.claude/CLAUDE.md"},
	}
	df, err := Assemble(paths, config.Config{Base: "node:22"}, res)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := os.ReadFile(filepath.Join(paths.ContextDir, gen.AgentContextName))
	if err != nil || string(ctx) != "workflow rules" {
		t.Fatalf("context file wrong: %q err=%v", ctx, err)
	}
	tgt, err := os.ReadFile(filepath.Join(paths.ContextDir, gen.AgentContextTargetName))
	if err != nil || string(tgt) != "/home/dev/.claude/CLAUDE.md\n" {
		t.Fatalf("target file wrong: %q err=%v", tgt, err)
	}
	if !strings.Contains(df, "COPY "+gen.AgentContextTargetName+" /etc/byre/"+gen.AgentContextTargetName) {
		t.Errorf("Dockerfile missing context-target COPY:\n%s", df)
	}
}

func TestAssembleContextTargetWithoutSkillContext(t *testing.T) {
	paths := bootstrapped(t)
	// Target set, no skill context: the target + self-edit note are still baked
	// (so the launcher can place a --self-edit note), but no agent-context.md.
	res := skills.Resolved{Agent: &skills.AgentContrib{ContextTarget: "/home/dev/.claude/CLAUDE.md"}}
	df, err := Assemble(paths, config.Config{Base: "node:22"}, res)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(paths.ContextDir, gen.AgentContextName)); !os.IsNotExist(err) {
		t.Error("agent-context.md should NOT be written without skill context")
	}
	if _, err := os.Stat(filepath.Join(paths.ContextDir, gen.AgentContextTargetName)); err != nil {
		t.Errorf("target file should be written when the agent declares a target: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.ContextDir, gen.SelfEditDocName)); err != nil {
		t.Errorf("self-edit doc should be written when the agent declares a target: %v", err)
	}
	if !strings.Contains(df, "COPY "+gen.SelfEditDocName+" /etc/byre/"+gen.SelfEditDocName) {
		t.Errorf("Dockerfile missing self-edit COPY:\n%s", df)
	}
	// The doc must name the real config keys, so the agent doesn't have to guess.
	doc, _ := os.ReadFile(filepath.Join(paths.ContextDir, gen.SelfEditDocName))
	for _, key := range []string{"apt =", "npm_global", "dockerfile_pre", "dockerfile_post", "run_args", "skills =", "mounts =", "volumes =", "ports =", "disabled = true", `scope = "machine"`, "byre.config"} {
		if !strings.Contains(string(doc), key) {
			t.Errorf("self-edit doc should reference %q:\n%s", key, doc)
		}
	}
}

func TestAssembleClearsStaleStagedFiles(t *testing.T) {
	paths := bootstrapped(t)
	src := filepath.Join(t.TempDir(), "review.sh")
	if err := os.WriteFile(src, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withFile := skills.Resolved{Skills: []skills.Skill{{
		Name:  "tools",
		Files: []skills.SkillFile{{Src: src, Rel: "review.sh", Dest: "/x"}},
	}}}
	if _, err := Assemble(paths, config.Config{Base: "node:22"}, withFile); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(paths.ContextDir, "skills", "tools", "review.sh")
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("file not staged on first build: %v", err)
	}
	// Re-assemble with no skill files: the stale staged file must be gone.
	if _, err := Assemble(paths, config.Config{Base: "node:22"}, skills.Resolved{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Errorf("stale staged file not cleared: %v", err)
	}
}

func TestAssembleVolumeDirsBakedUIDOwned(t *testing.T) {
	paths := bootstrapped(t)
	res := skills.Resolved{Skills: []skills.Skill{{Name: "s", File: skills.File{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
		{Name: "node_modules", Role: "cache", Target: "/workspace/node_modules"},
	}}}}}
	df, err := Assemble(paths, config.Config{Base: "node:22"}, res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(df, "chown \"${BYRE_UID}:${BYRE_GID}\" '/home/dev/.claude' '/workspace/node_modules'") {
		t.Errorf("volume mount points not pre-created owned by the baked UID:\n%s", df)
	}
}

func TestAssembleVolumeDirsIncludesConfigVolumes(t *testing.T) {
	paths := bootstrapped(t)
	cfg := config.Config{Base: "node:22", Volumes: []config.Volume{
		{Name: "cargo", Role: "cache", Target: "/home/dev/.cargo"},
	}}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "s", File: skills.File{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
	}}}}}
	df, err := Assemble(paths, cfg, res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(df, "/home/dev/.cargo") || !strings.Contains(df, "/home/dev/.claude") {
		t.Errorf("config + skill volume mount points should both be pre-created:\n%s", df)
	}
}

func TestAssembleClearsStaleAgentFiles(t *testing.T) {
	paths := bootstrapped(t)
	withAgent := skills.Resolved{
		Skills: []skills.Skill{{Name: "claude", Context: "rules"}},
		Agent:  &skills.AgentContrib{Command: "claude", ContextTarget: "/home/dev/.claude/CLAUDE.md"},
	}
	if _, err := Assemble(paths, config.Config{Base: "node:22"}, withAgent); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{gen.AgentCmdName, gen.AgentContextName, gen.AgentContextTargetName, gen.SelfEditDocName} {
		if _, err := os.Stat(filepath.Join(paths.ContextDir, name)); err != nil {
			t.Fatalf("%s not written with an agent: %v", name, err)
		}
	}
	// Re-assemble with the agent removed: every conditional file must be gone,
	// or a stale agent-cmd would be COPYable by a hand-written dockerfile_post
	// (and the context stops reflecting the config).
	if _, err := Assemble(paths, config.Config{Base: "node:22"}, skills.Resolved{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{gen.AgentCmdName, gen.AgentContextName, gen.AgentContextTargetName, gen.SelfEditDocName} {
		if _, err := os.Stat(filepath.Join(paths.ContextDir, name)); !os.IsNotExist(err) {
			t.Errorf("stale %s survived an agent-less re-assemble: %v", name, err)
		}
	}
}
