package build

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

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
	if err != nil || string(ctx) != chassisContext+"\n\nbe concise" {
		t.Errorf("agent context wrong: %q %v", ctx, err)
	}
}

// The canonical MCP file is baked on EVERY assemble — empty set included —
// so /etc/byre/mcp.json exists in every box and the claude skill's
// --mcp-config flag is unconditionally safe. Declared sets render the
// config+skill union minus config closures (skills.MCPSet), byte-stable.
func TestAssembleWritesMCPConfig(t *testing.T) {
	paths := bootstrapped(t)
	if _, err := Assemble(paths, config.Config{Base: "node:22"}, skills.Resolved{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(paths.ContextDir, gen.MCPConfigName))
	if err != nil {
		t.Fatalf("mcp.json not written on the empty set: %v", err)
	}
	if string(b) != "{\n  \"mcpServers\": {}\n}\n" {
		t.Fatalf("empty mcp.json = %q", b)
	}

	cfg := config.Config{
		Base:      "node:22",
		MCPs:      []config.MCP{{Name: "github", Command: []string{"gh-mcp", "stdio"}}},
		MCPClosed: []string{"telemetry"},
	}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "pete/tools", File: skills.File{MCPs: []config.MCP{
		{Name: "linear", URL: "https://mcp.linear.app/mcp"},
		{Name: "telemetry", Command: []string{"t"}},
	}}}}}
	df, err := Assemble(paths, cfg, res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(df, "COPY mcp.json /etc/byre/mcp.json") {
		t.Errorf("mcp.json COPY missing:\n%s", df)
	}
	b, err = os.ReadFile(filepath.Join(paths.ContextDir, gen.MCPConfigName))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{`"github"`, `"linear"`, `"url": "https://mcp.linear.app/mcp"`} {
		if !strings.Contains(got, want) {
			t.Errorf("mcp.json missing %s:\n%s", want, got)
		}
	}
	// The closure reached the skill-declared server (post-union subtraction).
	if strings.Contains(got, "telemetry") {
		t.Errorf("closed server leaked into mcp.json:\n%s", got)
	}
}

// A cross-source duplicate (the reject MCPSet owns) must fail Assemble too —
// callers that skipped resolve()'s validate can't bake an ambiguous file.
func TestAssembleRejectsDuplicateMCP(t *testing.T) {
	paths := bootstrapped(t)
	cfg := config.Config{Base: "node:22", MCPs: []config.MCP{{Name: "github", Command: []string{"a"}}}}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "pete/tools", File: skills.File{MCPs: []config.MCP{{Name: "github", Command: []string{"b"}}}}}}}
	if _, err := Assemble(paths, cfg, res); err == nil || !strings.Contains(err.Error(), "declared by both") {
		t.Fatalf("err = %v", err)
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

// Ordinary files and nested directories stage unchanged, preserving modes.
func TestCopyPathStagesRegularTree(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "top.txt"), []byte("top"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "exec.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "deep", "leaf"), []byte("leaf"), 0o600); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "staged")
	if err := copyPath(src, dst); err != nil {
		t.Fatalf("copyPath of a plain tree failed: %v", err)
	}
	for rel, wantMode := range map[string]os.FileMode{
		"top.txt":       0o644,
		"sub/exec.sh":   0o755,
		"sub/deep/leaf": 0o600,
	} {
		fi, err := os.Stat(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s not staged: %v", rel, err)
		}
		if fi.Mode().Perm() != wantMode {
			t.Errorf("%s mode = %v, want %v", rel, fi.Mode().Perm(), wantMode)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "sub", "deep", "leaf")); string(b) != "leaf" {
		t.Errorf("leaf content = %q, want %q", b, "leaf")
	}
}

// copyWithin runs copyPath under a timeout: a FIFO/special that slips past the
// type checks would block the open forever, so a regression must fail, not hang.
func copyWithin(t *testing.T, src, dst string) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- copyPath(src, dst) }()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("copyPath blocked — O_NONBLOCK / fstat-type-gate regression")
		return nil // unreachable
	}
}

// A FIFO planted in a staged directory (an agent has /workspace rw) must be a
// loud rejection, never an indefinite blocking open of the rebuild.
func TestCopyPathRejectsInteriorFIFO(t *testing.T) {
	src := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if err := copyWithin(t, src, filepath.Join(t.TempDir(), "staged")); err == nil {
		t.Fatal("a FIFO nested in a staged directory must be rejected")
	}
}

// The same for a top-level `files` source that is a FIFO.
func TestCopyPathRejectsTopLevelFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if err := copyWithin(t, fifo, filepath.Join(t.TempDir(), "staged")); err == nil {
		t.Fatal("a top-level FIFO source must be rejected")
	}
}

// A non-escaping in-root symlink is rejected too, not silently dereferenced:
// os.Root would follow it, so copyPath's Lstat-through-root must catch it. (The
// escaping variant is covered by TestAssembleFilesRejectsNestedSymlink.)
func TestCopyPathRejectsInteriorSymlink(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "real"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(src, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := copyPath(src, filepath.Join(t.TempDir(), "staged")); err == nil {
		t.Fatal("an in-root symlink must be rejected, not dereferenced into the image")
	}
}

// A symlink DEEP in the tree that escapes the project is refused by os.Root's
// per-component openat, proving a swapped directory component can't pull an
// external file in during recursion.
func TestCopyPathRejectsDeepEscapingSymlink(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(src, "a", "b", "leak")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := copyPath(src, filepath.Join(t.TempDir(), "staged")); err == nil {
		t.Fatal("a deep escaping symlink must be rejected")
	}
}

func TestAssembleFilesRejectsEscapeAndRelativeDest(t *testing.T) {
	paths := bootstrapped(t)
	// The escaping source EXISTS (a sibling of the project dir), so the
	// rejection must be the containment guard, not a file-not-found error.
	escaped := filepath.Join(filepath.Dir(paths.Canonical), "escape.txt")
	if err := os.WriteFile(escaped, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Assemble(paths, config.Config{Files: map[string]string{"../escape.txt": "/x"}}, skills.Resolved{})
	if err == nil {
		t.Error("expected rejection of '..' escaping source")
	} else if !strings.Contains(err.Error(), "escapes the project dir") {
		t.Errorf("expected the escape rejection, got: %v", err)
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
	if err != nil || string(ctx) != chassisContext+"\n\nworkflow rules" {
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
	// Target set, no skill context: the target + self-edit note are baked, and
	// the context file still exists — the chassis paragraph (the /inbox fact)
	// rides every box even with no skill contributing context.
	res := skills.Resolved{Agent: &skills.AgentContrib{ContextTarget: "/home/dev/.claude/CLAUDE.md"}}
	df, err := Assemble(paths, config.Config{Base: "node:22"}, res)
	if err != nil {
		t.Fatal(err)
	}
	if ctx, err := os.ReadFile(filepath.Join(paths.ContextDir, gen.AgentContextName)); err != nil || string(ctx) != chassisContext {
		t.Errorf("agent-context.md should carry exactly the chassis paragraph without skill context: %q %v", ctx, err)
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
	for _, name := range []string{gen.AgentCmdName, gen.AgentContextTargetName, gen.SelfEditDocName} {
		if _, err := os.Stat(filepath.Join(paths.ContextDir, name)); !os.IsNotExist(err) {
			t.Errorf("stale %s survived an agent-less re-assemble: %v", name, err)
		}
	}
	// agent-context.md is no longer conditional: the chassis paragraph keeps it
	// present (and truthful) on every box, agent or not.
	if ctx, err := os.ReadFile(filepath.Join(paths.ContextDir, gen.AgentContextName)); err != nil || string(ctx) != chassisContext {
		t.Errorf("agent-context.md should persist with the chassis paragraph: %q %v", ctx, err)
	}
}

// writeClaudeSkillDir lays down a minimal well-formed Claude Skill.
func writeClaudeSkillDir(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "---\nname: " + name + "\ndescription: Use when testing byre.\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAssembleStagesClaudeSkills(t *testing.T) {
	paths := bootstrapped(t)

	// Empty set: the canonical tree root still exists (the COPY is
	// unconditional) and the Dockerfile carries the COPY.
	df, err := Assemble(paths, config.Config{Base: "node:22"}, skills.Resolved{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(df, "COPY claude-skills /etc/byre/claude-skills") {
		t.Errorf("Dockerfile missing the unconditional claude-skills COPY:\n%s", df)
	}
	root := filepath.Join(paths.ContextDir, gen.ClaudeSkillsDirName, ".claude", "skills")
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		t.Fatalf("empty-set tree root not created: %v", err)
	}

	// A config-declared skill (absolute path) and a skill contribution both
	// stage under the canonical tree.
	src := filepath.Join(t.TempDir(), "tdd-loop")
	writeClaudeSkillDir(t, src, "tdd-loop")
	if err := os.WriteFile(filepath.Join(src, "support.txt"), []byte("quokka"), 0o644); err != nil {
		t.Fatal(err)
	}
	contribSrc := filepath.Join(t.TempDir(), "review-loop")
	writeClaudeSkillDir(t, contribSrc, "review-loop")

	cfg := config.Config{Base: "node:22", ClaudeSkills: []config.ClaudeSkill{{Name: "tdd-loop", Path: src}}}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "pete/tools", ClaudeSkills: []skills.ClaudeSkillDecl{
		{Skill: "pete/tools", CS: config.ClaudeSkill{Name: "review-loop", From: "cs/review-loop"}, SrcDir: contribSrc},
	}}}}
	if _, err := Assemble(paths, cfg, res); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tdd-loop", "review-loop"} {
		if _, err := os.Stat(filepath.Join(root, name, "SKILL.md")); err != nil {
			t.Errorf("staged skill %s missing: %v", name, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "tdd-loop", "support.txt")); string(b) != "quokka" {
		t.Errorf("support file not staged: %q", b)
	}

	// A closure removes the staged dir on the next assemble (re-staged from
	// scratch, no residue).
	cfg.ClaudeSkillsClosed = []string{"tdd-loop"}
	cfg.ClaudeSkills = nil
	if _, err := Assemble(paths, cfg, res); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tdd-loop")); !os.IsNotExist(err) {
		t.Errorf("removed declaration left residue in the context")
	}
	if _, err := os.Stat(filepath.Join(root, "review-loop", "SKILL.md")); err != nil {
		t.Errorf("surviving skill lost: %v", err)
	}
}

func TestAssembleRejectsBadClaudeSkill(t *testing.T) {
	paths := bootstrapped(t)

	// Not a skill: no SKILL.md.
	bad := t.TempDir()
	cfg := config.Config{Base: "node:22", ClaudeSkills: []config.ClaudeSkill{{Name: "nope", Path: bad}}}
	if _, err := Assemble(paths, cfg, skills.Resolved{}); err == nil || !strings.Contains(err.Error(), "no SKILL.md") {
		t.Fatalf("bad dir: %v", err)
	}

	// Frontmatter name mismatch is attributed.
	mm := filepath.Join(t.TempDir(), "mm")
	writeClaudeSkillDir(t, mm, "other-name")
	cfg.ClaudeSkills = []config.ClaudeSkill{{Name: "mm", Path: mm}}
	if _, err := Assemble(paths, cfg, skills.Resolved{}); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("mismatch: %v", err)
	}

	// Cross-source duplicates hard-reject at assemble too (ClaudeSkillSet).
	ok := filepath.Join(t.TempDir(), "dup")
	writeClaudeSkillDir(t, ok, "dup")
	cfg.ClaudeSkills = []config.ClaudeSkill{{Name: "dup", Path: ok}}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "pete/tools", ClaudeSkills: []skills.ClaudeSkillDecl{
		{Skill: "pete/tools", CS: config.ClaudeSkill{Name: "dup", From: "x"}, SrcDir: ok},
	}}}}
	if _, err := Assemble(paths, cfg, res); err == nil || !strings.Contains(err.Error(), "declared by both") {
		t.Fatalf("duplicate: %v", err)
	}
}
