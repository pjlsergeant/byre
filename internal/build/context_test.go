package build

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"
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

// The project store is agent-writable under develop --self-edit, including
// context/. A planted context symlink must never turn the next host-side
// Assemble into recursive deletion or predictable writes outside the store.
func TestAssembleRefusesSymlinkedContextRoot(t *testing.T) {
	paths := bootstrapped(t)
	if err := os.RemoveAll(paths.ContextDir); err != nil {
		t.Fatal(err)
	}

	victim := t.TempDir()
	files := filepath.Join(victim, "files")
	if err := os.MkdirAll(files, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(files, "keep")
	if err := os.WriteFile(sentinel, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, paths.ContextDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := Assemble(paths, config.Config{}, skills.Resolved{}); err == nil {
		t.Error("Assemble accepted a symlinked context root")
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "outside" {
		t.Fatalf("Assemble touched the symlink target: content %q, error %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(victim, "Dockerfile.generated")); !os.IsNotExist(err) {
		t.Fatalf("Assemble wrote through the symlinked context root: %v", err)
	}
}

// A context symlink whose target is INSIDE the store must be refused too. The
// target here is RELATIVE ("sibling") — os.Root's child form FOLLOWS a
// relative in-root terminal symlink (it refuses only absolute/escaping ones,
// verified on go1.26), so the confinement can't lean on escape-detection; the
// anchor Lstat-rejects a symlinked context outright (grok review, 2026-07-19,
// which the author first wrongly dismissed after probing the absolute shape).
// Without the fix, Assemble redirects its writes onto the sibling store dir.
func TestAssembleRefusesInStoreSymlinkedContextRoot(t *testing.T) {
	paths := bootstrapped(t)
	if err := os.RemoveAll(paths.ContextDir); err != nil {
		t.Fatal(err)
	}
	// A sibling dir beside context/ under the store, with a sentinel a
	// redirected Dockerfile write would clobber.
	sibling := filepath.Join(paths.Dir, "sibling")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(sibling, "Dockerfile.generated")
	if err := os.WriteFile(sentinel, []byte("pre-existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	// RELATIVE target: this is the shape os.Root follows, the actual hole.
	if err := os.Symlink("sibling", paths.ContextDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := Assemble(paths, config.Config{}, skills.Resolved{}); err == nil {
		t.Error("Assemble followed an in-store symlinked context root")
	}
	// The sibling's file was not overwritten by a redirected Dockerfile write.
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "pre-existing" {
		t.Fatalf("Assemble redirected a write onto a sibling store dir: content %q, error %v", got, err)
	}
}

// The confinement also neutralizes an INTERIOR redirect: a real context dir
// whose `files` subtree the agent replaced with a symlink to an outside
// victim. The per-build clear must remove the planted LINK (never recurse
// through it into the victim) and re-stage a real subtree, so the build both
// succeeds and leaves the victim's contents intact.
func TestAssembleNeutralizesSymlinkedContextChild(t *testing.T) {
	paths := bootstrapped(t)

	victim := t.TempDir()
	sentinel := filepath.Join(victim, "keep")
	if err := os.WriteFile(sentinel, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	// context/ is a real dir; context/files is a symlink to the victim.
	filesLink := filepath.Join(paths.ContextDir, "files")
	if err := os.Symlink(victim, filesLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// A `files` entry so staging must write under files/ this build.
	if err := os.WriteFile(filepath.Join(paths.Canonical, "seed.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Assemble(paths, config.Config{Files: map[string]string{"seed.txt": "/opt/seed.txt"}}, skills.Resolved{}); err != nil {
		t.Fatalf("Assemble should neutralize the planted link and succeed: %v", err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "outside" {
		t.Fatalf("Assemble deleted through the symlinked `files`: content %q, error %v", got, err)
	}
	// files/ is now a real directory holding the staged file, not a link.
	if fi, err := os.Lstat(filesLink); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("files/ should be re-staged as a real dir, got mode %v err %v", fi.Mode(), err)
	}
	if b, err := os.ReadFile(filepath.Join(paths.ContextDir, "files", "seed.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("seed not staged into the real files/: %q %v", b, err)
	}
	// The victim gained nothing (staging did not write through the old link).
	if _, err := os.Stat(filepath.Join(victim, "seed.txt")); !os.IsNotExist(err) {
		t.Fatalf("staging wrote through the planted link into the victim: %v", err)
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
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(assets, "leak")); err != nil {
		t.Fatal(err)
	}
	if _, err := Assemble(paths, config.Config{Base: "debian:bookworm", Files: map[string]string{"assets": "/opt/assets"}}, skills.Resolved{}); err == nil {
		t.Fatal("expected rejection of a symlink nested in a staged directory")
	}
}

// Ordinary files and nested directories stage unchanged, preserving modes.
// dstAt opens an os.Root at dst's parent, so a staging helper's now-root-
// relative destination writes land at the real dst path — tests still verify
// content by the absolute dst. Its two returns feed the helpers' trailing
// (dstRoot, dst) parameters directly.
func dstAt(t *testing.T, dst string) (*os.Root, string) {
	t.Helper()
	r, err := os.OpenRoot(filepath.Dir(dst))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, filepath.Base(dst)
}

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
	dr, base := dstAt(t, dst)
	if err := copyPath(src, dr, base); err != nil {
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

	// A trailing slash (Claude skill `path` values are not Clean'd upstream)
	// must still stage — openDirRootNoFollow Cleans before splitting parent/base.
	dst2 := filepath.Join(t.TempDir(), "staged2")
	dr2, base2 := dstAt(t, dst2)
	if err := copyPath(src+string(filepath.Separator), dr2, base2); err != nil {
		t.Fatalf("copyPath of a trailing-slash dir failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst2, "top.txt")); err != nil {
		t.Errorf("trailing-slash source did not stage: %v", err)
	}
}

// copyWithin runs copyPath under a timeout: a FIFO/special that slips past the
// type checks would block the open forever, so a regression must fail, not hang.
func copyWithin(t *testing.T, src, dst string) error {
	t.Helper()
	dr, base := dstAt(t, dst)
	done := make(chan error, 1)
	go func() { done <- copyPath(src, dr, base) }()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("copyPath blocked — O_NONBLOCK / fstat-type-gate regression")
		return nil // unreachable
	}
}

// A FIFO planted in a staged directory (an agent has /workspace rw) must be a
// loud rejection AS A NON-REGULAR FILE — never a blocking open, and never
// staged. The fd-fstat type gate is what enforces this; asserting the message
// pins that it is the gate firing, not an incidental read error.
func TestCopyPathRejectsInteriorFIFO(t *testing.T) {
	src := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	err := copyWithin(t, src, filepath.Join(t.TempDir(), "staged"))
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("interior FIFO should be rejected as non-regular, got: %v", err)
	}
}

// The same for a top-level `files` source that is a FIFO. Statically this is
// caught by copyPath's initial pathname Lstat (non-regular); the fd-fstat gate
// in stageRegularFromFD is the backstop for a regular→FIFO swap after that
// Lstat. Either way the message is "not a regular file".
func TestCopyPathRejectsTopLevelFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	err := copyWithin(t, fifo, filepath.Join(t.TempDir(), "staged"))
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("top-level FIFO should be rejected as non-regular, got: %v", err)
	}
}

// A `files` source is agent-writable and can grow or shrink while it is being
// staged: the copy is bounded at the size fstat observed (an unbounded copy of
// a still-growing file chases the writer indefinitely), and a source that
// mutated mid-copy is refused rather than staged as a torn read. The mid-copy
// mutation is a race a single-threaded test cannot stage, so the bound is
// exercised directly with a size that disagrees with the file's content.
func TestCopyExactlyRefusesMutatedSource(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(src, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	open := func() *os.File {
		f, err := os.Open(src)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	var buf strings.Builder
	if err := copyExactly(&buf, open(), 10, "src"); err != nil {
		t.Fatalf("exact-size copy should succeed, got: %v", err)
	}
	if buf.String() != "0123456789" {
		t.Fatalf("staged bytes = %q", buf.String())
	}
	// Observed 6, file holds 10: it grew after the stat — refuse, don't chase.
	if err := copyExactly(io.Discard, open(), 6, "src"); err == nil || !strings.Contains(err.Error(), "changed while being staged") {
		t.Fatalf("grown source should be refused, got: %v", err)
	}
	// Observed 14, file holds 10: it shrank — the staged file would be short.
	if err := copyExactly(io.Discard, open(), 14, "src"); err == nil || !strings.Contains(err.Error(), "changed while being staged") {
		t.Fatalf("shrunk source should be refused, got: %v", err)
	}
}

// A growth probe that fails outright (I/O error, not EOF) must surface, never
// pass as "didn't grow" — this branch's error was silently discarded once.
func TestCopyExactlyPropagatesProbeError(t *testing.T) {
	in := io.MultiReader(strings.NewReader("0123456789"), iotest.ErrReader(errors.New("boom")))
	err := copyExactly(io.Discard, in, 10, "src")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("failed probe read must surface, got: %v", err)
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
	dr, base := dstAt(t, filepath.Join(t.TempDir(), "staged"))
	if err := copyPath(src, dr, base); err == nil {
		t.Fatal("an in-root symlink must be rejected, not dereferenced into the image")
	}
}

// A symlink escaping the project, whether it is a leaf or an intermediate
// directory component, is rejected during recursion — the walk Lstats each
// component through the root before descending, so the escape never reaches an
// open. (The openat escape refusal is the backstop for the swap-after-Lstat
// race, which a single-threaded test cannot stage.)
func TestCopyPathRejectsEscapingSymlinkComponents(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, src string)
	}{
		{"leaf", func(t *testing.T, src string) {
			if err := os.MkdirAll(filepath.Join(src, "a"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(src, "a", "leak")); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
		{"intermediate-component", func(t *testing.T, src string) {
			// `a` is itself a symlink to an external directory; the walk must
			// reject it before descending, not follow it to enumerate outside.
			if err := os.Symlink(t.TempDir(), filepath.Join(src, "a")); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := t.TempDir()
			tc.plant(t, src)
			dr, base := dstAt(t, filepath.Join(t.TempDir(), "staged"))
			if err := copyPath(src, dr, base); err == nil {
				t.Fatalf("escaping %s symlink must be rejected", tc.name)
			}
		})
	}
}

// A top-level `files` source that is itself a symlink to an external directory
// must be rejected, never followed to stage the external tree. This exercises
// the STATIC guard (copyPath's initial Lstat); the swap-after-Lstat race that
// openDirRootNoFollow additionally closes needs a concurrent mutator a
// single-threaded test cannot stage, so it is design-verified, not pinned here.
func TestCopyPathRejectsTopLevelDirSymlink(t *testing.T) {
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "host-secret"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "assets")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "staged")
	dr, base := dstAt(t, dst)
	if err := copyPath(link, dr, base); err == nil {
		t.Fatal("a top-level directory symlink must be rejected, not followed")
	}
	if _, err := os.Stat(filepath.Join(dst, "host-secret")); err == nil {
		t.Fatal("external file was staged through a top-level directory symlink")
	}
}

// copyRootedEntry refuses an entry whose ANCESTOR component escapes the anchored
// root — the mechanism that closes the ancestor-swap race for `files` sources
// (safeProjectPath rejects a static escape at plan time; this pins that the
// openat anchoring is the backstop when an ancestor is swapped after validation).
func TestCopyRootedEntryRefusesEscapingAncestor(t *testing.T) {
	projRoot := t.TempDir()
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "secret"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	// `sub` inside the root is a symlink to an external directory.
	if err := os.Symlink(external, filepath.Join(projRoot, "sub")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	root, err := os.OpenRoot(projRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// topLevel=true mirrors stageCopy's call on a multi-component configured
	// source; the escaping ancestor is refused regardless of the flag.
	dr, base := dstAt(t, filepath.Join(t.TempDir(), "out"))
	if err := copyRootedEntry(root, filepath.Join("sub", "secret"), dr, base, true); err == nil {
		t.Fatal("an entry reached through an escaping ancestor symlink must be refused by the root")
	}
}

// A `files` source that is itself a symlink to an in-project target IS followed
// and staged — that is the user's explicit naming, resolved by safeProjectPath,
// and must not be rejected the way an agent-planted INTERIOR symlink is. Pins
// the user-vs-agent boundary through the project-root-anchored path.
func TestAssembleFilesFollowsUserNamedTopLevelSymlink(t *testing.T) {
	paths := bootstrapped(t)
	if err := os.WriteFile(filepath.Join(paths.Canonical, "real.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(paths.Canonical, "link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Assemble(paths, config.Config{Base: "debian:bookworm", Files: map[string]string{"link.txt": "/opt/x"}}, skills.Resolved{}); err != nil {
		t.Fatalf("a user-named top-level symlink source must be followed, got: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(paths.ContextDir, "files", "link.txt")); err != nil || string(b) != "hi" {
		t.Errorf("followed symlink content not staged: %q %v", b, err)
	}
}

// A nested `files` directory source stages its whole tree through the
// project-root anchor (exercises copyRootedEntry recursion via Assemble).
func TestAssembleStagesNestedFilesDir(t *testing.T) {
	paths := bootstrapped(t)
	if err := os.MkdirAll(filepath.Join(paths.Canonical, "assets", "img"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Canonical, "assets", "img", "logo"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Assemble(paths, config.Config{Base: "debian:bookworm", Files: map[string]string{"assets": "/opt/assets"}}, skills.Resolved{}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(paths.ContextDir, "files", "assets", "img", "logo")); err != nil || string(b) != "png" {
		t.Errorf("nested files dir not staged: %q %v", b, err)
	}
}

func TestWithinRoot(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "home", "me", "proj")
	cases := []struct {
		path    string
		wantRel string
		wantOK  bool
	}{
		{filepath.Join(root, "a", "b"), filepath.Join("a", "b"), true},
		{root, ".", true},
		{filepath.Join(filepath.Dir(root), "sibling"), "", false},
		{filepath.Join(string(filepath.Separator), "etc", "passwd"), "", false},
	}
	for _, c := range cases {
		rel, ok := withinRoot(root, c.path)
		if ok != c.wantOK || (ok && rel != c.wantRel) {
			t.Errorf("withinRoot(%q,%q) = (%q,%v), want (%q,%v)", root, c.path, rel, ok, c.wantRel, c.wantOK)
		}
	}
}

// A source spelled through a SYMLINK ALIAS of the project root (expandHome does
// not canonicalize, WorkDir is canonical) must still be recognized as in-tree
// and anchored — a purely lexical compare would false-negative it onto the
// unsafe by-pathname route.
func TestAgentWritableRelResolvesAlias(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(real, "a", "myskill"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "linkproj")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	aliased := filepath.Join(alias, "a", "myskill")
	// Lexical-only would miss it.
	if _, ok := withinRoot(real, aliased); ok {
		t.Fatal("precondition: the aliased spelling should not be lexically within root")
	}
	rel, ok := agentWritableRel(real, aliased)
	if !ok || rel != filepath.Join("a", "myskill") {
		t.Fatalf("agentWritableRel(%q,%q) = (%q,%v), want (%q,true)", real, aliased, rel, ok, filepath.Join("a", "myskill"))
	}

	// Conversely, a path spelled UNDER root whose intermediate escapes must stay
	// anchored (lexical match wins), so os.Root refuses it — not demoted to the
	// by-pathname copyPath route by resolving first.
	if err := os.Symlink(string(filepath.Separator)+"etc", filepath.Join(real, "sub")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	rel, ok = agentWritableRel(real, filepath.Join(real, "sub", "passwd"))
	if !ok || rel != filepath.Join("sub", "passwd") {
		t.Fatalf("a lexically in-tree path with an escaping intermediate must stay anchored, got (%q,%v)", rel, ok)
	}
}

// A `[[claude_skills]].path` INSIDE the writable project must stage through the
// project-root anchor, not the by-pathname copyPath route (whose O_NOFOLLOW
// guards only the leaf) — else an agent could swap a project-local ancestor.
// Staged content confirms the anchored route handles the project-local case.
func TestAssembleStagesProjectLocalClaudeSkill(t *testing.T) {
	paths := bootstrapped(t)
	src := filepath.Join(paths.Canonical, "myskill")
	writeClaudeSkillDir(t, src, "myskill")
	if err := os.WriteFile(filepath.Join(src, "support.txt"), []byte("quokka"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Assemble(paths, config.Config{Base: "node:22", ClaudeSkills: []config.ClaudeSkill{{Name: "myskill", Path: src}}}, skills.Resolved{}); err != nil {
		t.Fatalf("project-local claude skill should stage: %v", err)
	}
	staged := filepath.Join(paths.ContextDir, gen.ClaudeSkillsDirName, ".claude", "skills", "myskill", "support.txt")
	if b, err := os.ReadFile(staged); err != nil || string(b) != "quokka" {
		t.Errorf("project-local claude skill not staged through the anchor: %q %v", b, err)
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
	if err := os.WriteFile(filepath.Join(paths.Canonical, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
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

// TestPlanGuardDerivesFromNetnsSkill pins that the security guard set is DERIVED
// from the resolved skills — a network-posture skill contributes its launch gate
// (non-exec) and netns script (exec, since a staged script arrives 0644), each
// with the staged source looked up from the skill's own planned files. This is
// what keeps the set from rotting: a future posture skill is covered without
// touching a hardcoded path list.
func TestPlanGuardDerivesFromNetnsSkill(t *testing.T) {
	genSkills := []gen.SkillBlock{{
		Name: "firewall",
		Files: map[string]string{
			"skills/firewall/firewall.sh": "/usr/local/bin/byre-firewall",
			"skills/firewall/launch-gate": gen.LaunchGatePath,
		},
	}}
	var f skills.File
	f.Runtime.NetnsInit = "/usr/local/bin/byre-firewall"
	res := skills.Resolved{Skills: []skills.Skill{{Name: "firewall", File: f}}}

	guard := planGuard(genSkills, res)
	got := map[string]gen.GuardFile{}
	for _, g := range guard {
		got[g.Dest] = g
	}
	gate, ok := got[gen.LaunchGatePath]
	if !ok || gate.Exec || gate.Staged != "skills/firewall/launch-gate" {
		t.Fatalf("gate guard wrong: %+v", gate)
	}
	fw, ok := got["/usr/local/bin/byre-firewall"]
	if !ok || !fw.Exec || fw.Staged != "skills/firewall/firewall.sh" {
		t.Fatalf("firewall guard wrong: %+v", fw)
	}
}

// TestPlanGuardEmptyWithoutNetnsSkill: with no netns posture there is nothing to
// clobber-protect beyond the launcher (which gen re-asserts unconditionally), so
// the derived guard is empty — a plain box carries no extra guard COPYs.
func TestPlanGuardEmptyWithoutNetnsSkill(t *testing.T) {
	genSkills := []gen.SkillBlock{{Name: "tools", Files: map[string]string{"skills/tools/x.sh": "/usr/local/bin/x"}}}
	if guard := planGuard(genSkills, skills.Resolved{}); guard != nil {
		t.Fatalf("expected no guard without a netns skill, got %+v", guard)
	}
}
