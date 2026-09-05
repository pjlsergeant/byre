package commands

import (
	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/testtools"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Bundled inspect shows a computed display digest (ADR 0029 deferral closed):
// same line shape as installed rows, stable across invocations.
func TestInspectBundledShowsDisplayDigest(t *testing.T) {
	installHome(t)
	s, out, _ := testStreams("", false)
	if err := PackageInspect(s, packages.KindSkill, "claude"); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`Digest:\s+sha256:([0-9a-f]{64})\n`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no display digest in bundled inspect output:\n%s", out.String())
	}
	s2, out2, _ := testStreams("", false)
	if err := PackageInspect(s2, packages.KindSkill, "claude"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2.String(), m[1]) {
		t.Errorf("display digest must be stable across inspects; first %s, second:\n%s", m[1], out2.String())
	}
}

// Fork's tree copy is judged at the descriptor: a symlink is the user's own
// arrangement of their store, so it is followed and its target's bytes are
// materialized as a regular file, while a FIFO fails loudly instead of
// hanging the copy.
func TestCopyDirFollowsLinksRefusesIrregulars(t *testing.T) {
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte("linked bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "skill.toml"), []byte("# ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(src, "extra")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "dst")
	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir must follow a user symlink: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "extra"))
	if err != nil || string(b) != "linked bytes" {
		t.Fatalf("fork should materialize the link target's bytes, got %q, %v", b, err)
	}
	if fi, err := os.Lstat(filepath.Join(dst, "extra")); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("materialized copy must be a regular file, got %v, %v", fi, err)
	}

	src2 := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(src2, "pipe"), 0o644); err != nil {
		testtools.Unavailable(t, "mkfifo", err)
	}
	dst2 := filepath.Join(t.TempDir(), "dst")
	done := make(chan error, 1)
	go func() { done <- copyDir(src2, dst2) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want FIFO refusal")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copyDir blocked on a FIFO — the exact hang fork must never have")
	}
}

// A failed fork must leave NOTHING at the destination: copying into the
// final name left a partial tree that poisoned retries with "already
// exists" and could carry the source's identity under the fork's path
// before this existed. The fork stages beside the destination
// and publishes with one rename.
func TestForkFailureLeavesNoDestination(t *testing.T) {
	home := installHome(t)
	srcDir := filepath.Join(home, "skills", "pete", "tool")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "skill.toml"), []byte("description = \"local\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(srcDir, "pipe"), 0o644); err != nil {
		testtools.Unavailable(t, "mkfifo", err)
	}
	s, _, _ := testStreams("", false)
	if err := PackageFork(s, packages.KindSkill, "pete/tool", "me/fork"); err == nil {
		t.Fatal("fork of a FIFO-bearing source must fail")
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "me", "fork")); !os.IsNotExist(err) {
		t.Fatalf("failed fork left a destination behind: %v", err)
	}
	ents, _ := os.ReadDir(filepath.Join(home, "skills", "me"))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".fork-stage-") {
			t.Fatalf("failed fork left staging residue %s", e.Name())
		}
	}
}

// The staged fork still publishes: destination exists afterwards with the
// fork's own identity in the rewritten primary.
func TestForkPublishesStagedTree(t *testing.T) {
	home := installHome(t)
	srcDir := filepath.Join(home, "skills", "pete", "tool")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "skill.toml"), []byte("description = \"local\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _, _ := testStreams("", false)
	if err := PackageFork(s, packages.KindSkill, "pete/tool", "me/fork"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, "skills", "me", "fork", "skill.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `id = "me/fork"`) || !strings.Contains(string(b), "Forked from pete/tool") {
		t.Fatalf("published primary must carry the fork identity, got:\n%s", b)
	}
}

// The inspect footer is a command to paste. A local package can sit under a
// directory whose name carries shell metacharacters, so the URI is quoted as
// well as escaped -- unquoted, the pasted line runs `byre skill install`
// against a different argv than the one inspect vouched for.
func TestInspectURIQuotesThePastedSource(t *testing.T) {
	installHome(t)
	dir := filepath.Join(t.TempDir(), "my skills; touch pwned")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "skill.toml")
	if err := os.WriteFile(manifest, []byte(`[package]
id = "pete/quoted"
version = "1.0.0"
kind = "skill"
package_api = 1
requires_byre = ">=0.1.0"
description = "inspect quoting fixture"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, out, _ := testStreams("", false)
	if err := inspectURI(s, packages.KindSkill, manifest); err != nil {
		t.Fatal(err)
	}
	want := packages.ShellArg(manifest)
	if !strings.Contains(out.String(), "install "+want+" ") {
		t.Errorf("the pasted install line must carry the quoted source %s:\n%s", want, out.String())
	}
}

// A fork of a body whose leading keys are bare (every bundled template's
// base, a companion's companion_for) keeps them the body's: the [package]
// table goes below them. Before this, the fork of a template silently lost
// its base to package.base, and the scoping check now refuses that shape.
func TestForkKeepsLeadingBareKeysAboveThePackageHeader(t *testing.T) {
	home := installHome(t)
	srcDir := filepath.Join(home, "templates", "pete", "shape")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "template.config"), []byte("# shape\nbase = \"debian:13\"\negress_offered = [\"proxy.golang.org\"]\n\n[env]\nX = \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _, _ := testStreams("", false)
	if err := PackageFork(s, packages.KindTemplate, "pete/shape", "me/shape"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, "templates", "me", "shape", "template.config"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "# Forked from pete/shape") {
		t.Errorf("the provenance comment leads the file:\n%s", b)
	}
	if err := packages.CheckPackageScoping(packages.KindTemplate, b); err != nil {
		t.Fatalf("the fork must pass the scoping check: %v\n%s", err, b)
	}
	tc, err := config.ParseTemplateBody(b)
	if err != nil {
		t.Fatal(err)
	}
	if tc.Base != "debian:13" || len(tc.EgressOffered) != 1 || tc.Env["X"] != "1" {
		t.Errorf("the fork must keep the body's keys: %+v", tc)
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.ResolveName("me/shape"); err != nil {
		t.Errorf("the fork must load: %v", err)
	}
}

// The scaffolds `init` writes must be loadable as written: the template
// scaffold's base sat below [package] and was package.base -- a fresh
// template built from gen's default base, not the one on the page.
func TestInitScaffoldsAreLoadableWithTheirKeys(t *testing.T) {
	home := installHome(t)
	s, _, _ := testStreams("", false)
	if err := PackageInit(s, packages.KindTemplate, "pete/tpl"); err != nil {
		t.Fatal(err)
	}
	if err := PackageInit(s, packages.KindSkill, "pete/sk"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "templates", "pete", "tpl", "template.config"))
	if err != nil {
		t.Fatal(err)
	}
	tc, err := config.ParseTemplateBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	if tc.Base != "debian:bookworm-slim" {
		t.Errorf("the scaffold's base must reach the template, got %q", tc.Base)
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"pete/tpl", "pete/sk"} {
		if _, err := cat.ResolveName(id); err != nil {
			t.Errorf("scaffold %s must load: %v", id, err)
		}
	}
}
