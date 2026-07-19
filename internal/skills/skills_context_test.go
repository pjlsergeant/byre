package skills

// Context, prefs, and files path containment: everything a skill ships
// into the box must resolve inside its own tree and land inside the
// box's home — traversal, symlink escapes, and hostile files refused.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/config"
)

func TestResolveContextFromFile(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "ctx", "[context]\nfile = \"ctx.md\"\n", map[string]string{"ctx.md": "from file"})
	res, err := Resolve(config.Config{Skills: []string{"ctx"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.Context() != "from file" {
		t.Errorf("context file not read: %q", res.Context())
	}
}

func TestResolveContextFileTraversalRejected(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "evil", "[context]\nfile = \"../../etc/passwd\"\n", nil)
	// The traversal target EXISTS (skills/evil/../../etc/passwd = dir/etc/
	// passwd), so an ENOENT can't stand in for the containment rejection.
	if err := os.MkdirAll(filepath.Join(dir, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "etc", "passwd"), []byte("root:x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(config.Config{Skills: []string{"evil"}}, catFor(t, dir))
	if err == nil {
		t.Fatal("expected rejection of path-traversal context file")
	}
	if !strings.Contains(err.Error(), "escapes the skill dir") {
		t.Fatalf("expected the escape rejection, got: %v", err)
	}
}

func TestResolveContextSymlinkEscapeRejected(t *testing.T) {
	dir := testHome(t)
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, dir, "evil", "[context]\nfile = \"link\"\n", nil)
	// symlink inside the skill dir pointing outside the bundle
	if err := os.Symlink(outside, filepath.Join(dir, "skills", "evil", "link")); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(config.Config{Skills: []string{"evil"}}, catFor(t, dir))
	if err == nil {
		t.Fatal("expected rejection of symlink escaping the skill dir")
	}
	if !strings.Contains(err.Error(), "escapes the skill dir") {
		t.Fatalf("expected the escape rejection, got: %v", err)
	}
}

// agentWithPrefs is an agent skill that declares a curated prefs block.
const agentWithPrefs = `
[agent]
command = "fake-agent"
state = ".fake"

[agent.prefs]
from = "~/.fake"
files = ["keybindings.json", "themes"]

[[volumes]]
name = ".fake"
role = "state"
target = "/home/dev/.fake"
`

func TestResolvePrefsCollected(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "fake", agentWithPrefs, nil)
	res, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentPrefs() == nil {
		t.Fatal("expected AgentPrefs to be set")
	}
	if res.AgentPrefs().From != "~/.fake" || len(res.AgentPrefs().Files) != 2 {
		t.Fatalf("prefs not parsed: %+v", res.AgentPrefs())
	}
}

func TestResolvePrefsRequireState(t *testing.T) {
	dir := testHome(t)
	// prefs but no [agent].state -> nowhere to seed -> error.
	writeSkill(t, dir, "fake", "[agent]\ncommand = \"x\"\n[agent.prefs]\nfrom = \"~/.fake\"\nfiles = [\"a\"]\n", nil)
	if _, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "requires [agent].state") {
		t.Fatalf("expected error: prefs require a state volume, got %v", err)
	}
}

func TestResolvePrefsRejectsEscapingFile(t *testing.T) {
	dir := t.TempDir()
	toml := "[agent]\ncommand = \"x\"\nstate = \".fake\"\n[agent.prefs]\nfrom = \"~/.fake\"\nfiles = [\"../../etc/passwd\"]\n" +
		"[[volumes]]\nname = \".fake\"\nrole = \"state\"\ntarget = \"/home/dev/.fake\"\n"
	writeSkill(t, dir, "fake", toml, nil)
	if _, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "stay within from") {
		t.Fatalf("expected error: prefs file escapes from-dir, got %v", err)
	}
}

func TestResolvePrefsRejectsWholeDir(t *testing.T) {
	dir := testHome(t)
	// files = ["."] would copy the entire from-dir (incl. secret-bearing files);
	// must be rejected so curation can't be bypassed.
	for _, bad := range []string{".", "./", "x/.."} {
		toml := "[agent]\ncommand = \"x\"\nstate = \".fake\"\n[agent.prefs]\nfrom = \"~/.fake\"\nfiles = [\"" + bad + "\"]\n" +
			"[[volumes]]\nname = \".fake\"\nrole = \"state\"\ntarget = \"/home/dev/.fake\"\n"
		writeSkill(t, dir, "fake", toml, nil)
		if _, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "stay within from") {
			t.Fatalf("expected rejection of prefs file %q (whole-dir copy), got %v", bad, err)
		}
	}
}

func TestResolveSkillFiles(t *testing.T) {
	dir := t.TempDir()
	const toml = `
[build]
files = { "review.sh" = "/usr/local/bin/byre-review", "lib/helper.sh" = "/opt/helper.sh" }
`
	writeSkill(t, dir, "tools", toml, map[string]string{
		"review.sh":     "#!/bin/sh\necho review\n",
		"lib/helper.sh": "echo help\n",
	})
	res, err := Resolve(config.Config{Skills: []string{"tools"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	files := res.BuildBlocks()[0].Files
	if len(files) != 2 {
		t.Fatalf("want 2 skill files, got %d: %+v", len(files), files)
	}
	// Sorted by source for determinism: "lib/helper.sh" < "review.sh".
	if files[0].Rel != "lib/helper.sh" || files[0].Dest != "/opt/helper.sh" {
		t.Errorf("first file wrong: %+v", files[0])
	}
	if files[1].Rel != "review.sh" || files[1].Dest != "/usr/local/bin/byre-review" {
		t.Errorf("second file wrong: %+v", files[1])
	}
	if res.BuildBlocks()[0].Name != "tools" {
		t.Errorf("skill name not recorded: %+v", res.BuildBlocks()[0])
	}
}

func TestResolveSkillFilesRejectsRelativeDest(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "bad", "[build]\nfiles = { \"x.sh\" = \"relative/dest\" }\n",
		map[string]string{"x.sh": "x\n"})
	if _, err := Resolve(config.Config{Skills: []string{"bad"}}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "must be an absolute image path") {
		t.Fatalf("expected rejection of non-absolute file destination, got %v", err)
	}
}

func TestResolveSkillFilesRejectsEscape(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "bad", "[build]\nfiles = { \"../escape.sh\" = \"/x.sh\" }\n", nil)
	// The escaping target EXISTS: the rejection must be the containment
	// guard, not a file-not-found error standing in for it.
	if err := os.WriteFile(filepath.Join(dir, "skills", "escape.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(config.Config{Skills: []string{"bad"}}, catFor(t, dir))
	if err == nil {
		t.Fatal("expected rejection of source escaping the skill dir")
	}
	if !strings.Contains(err.Error(), "escapes the skill dir") {
		t.Fatalf("expected the escape rejection, got: %v", err)
	}
}

func TestResolveAgentContextTarget(t *testing.T) {
	dir := t.TempDir()
	const toml = `
[agent]
command = "fake --go"
state = ".fake"
context_target = "/home/dev/.fake/MEM.md"
[context]
text = "workflow rules"
[[volumes]]
name = ".fake"
role = "state"
target = "/home/dev/.fake"
`
	writeSkill(t, dir, "fake", toml, nil)
	res, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentContextTarget() != "/home/dev/.fake/MEM.md" {
		t.Errorf("context target not resolved: %q", res.AgentContextTarget())
	}
	if res.Context() != "workflow rules" {
		t.Errorf("context not resolved: %q", res.Context())
	}
}

func TestResolveAgentContextTargetMustBeAbsolute(t *testing.T) {
	dir := t.TempDir()
	const toml = `
[agent]
command = "fake --go"
state = ".fake"
context_target = "rel/MEM.md"
[[volumes]]
name = ".fake"
role = "state"
target = "/home/dev/.fake"
`
	writeSkill(t, dir, "fake", toml, nil)
	if _, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("expected rejection of non-absolute context_target, got %v", err)
	}
}

func TestResolveContextTargetMustBeWithinHome(t *testing.T) {
	dir := t.TempDir()
	const toml = `
[agent]
command = "fake --go"
state = ".fake"
context_target = "/etc/passwd"
[[volumes]]
name = ".fake"
role = "state"
target = "/home/dev/.fake"
`
	writeSkill(t, dir, "fake", toml, nil)
	if _, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "strictly within") {
		t.Fatalf("expected rejection of context_target outside /home/dev, got %v", err)
	}
}

func TestResolveContextTargetRejectsHomeItself(t *testing.T) {
	dir := t.TempDir()
	const toml = `
[agent]
command = "fake --go"
state = ".fake"
context_target = "/home/dev"
[[volumes]]
name = ".fake"
role = "state"
target = "/home/dev/.fake"
`
	writeSkill(t, dir, "fake", toml, nil)
	if _, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "strictly within") {
		t.Fatalf("expected rejection of context_target == /home/dev (not a file), got %v", err)
	}
}

// A hostile [context] file must fail the skill load, never wedge develop: a
// FIFO named as the context file blocks a plain read forever, and an
// oversized file must stop at the cap instead of ballooning the host
// process. Both are judged at the descriptor (same discipline as every
// package-content read).
func TestLoadHostileContextFileFailsNotBlocks(t *testing.T) {
	home := testHome(t)
	writeSkill(t, home, "evilctx", "[context]\nfile = \"context.md\"\n", nil)
	if err := syscall.Mkfifo(filepath.Join(home, "skills", "evilctx", "context.md"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	cat := catFor(t, home)
	done := make(chan error, 1)
	go func() {
		_, err := Load(cat, "evilctx")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("Load must refuse a FIFO context file as not-a-regular-file, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Load blocked on a FIFO context file — the exact hang skill load must never have")
	}

	home2 := testHome(t)
	writeSkill(t, home2, "hugectx", "[context]\nfile = \"context.md\"\n",
		map[string]string{"context.md": strings.Repeat("x", MaxContextBytes+1)})
	if _, err := Load(catFor(t, home2), "hugectx"); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversized context: want limit error, got %v", err)
	}
}
