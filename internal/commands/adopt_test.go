package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/packages"
)

func adoptRepoDir(t *testing.T, id, kindLine string) string {
	t.Helper()
	dir := t.TempDir()
	body := `[package]
id = "` + id + `"
version = "1.1.0"
package_api = 1
requires_byre = ">=0.1.0"
` + kindLine + `
description = "adopted test skill"

[context]
text = "hello"
`
	if err := os.WriteFile(filepath.Join(dir, "skill.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestAdoptRoundTrip pins the publishing round trip adopt exists for: a
// distribution-repo checkout becomes the local source for its declared id
// (a symlink at the store path), the catalog lists it LOCAL, and pack works
// on it again.
func TestAdoptRoundTrip(t *testing.T) {
	home := installHome(t)
	repo := adoptRepoDir(t, "pete/tool", `kind = "skill"`)
	s, _, errBuf := testStreams("", false)
	if err := PackageAdopt(s, packages.KindSkill, repo); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "adopted pete/tool") {
		t.Fatalf("adopt must announce the id and link, got:\n%s", errBuf.String())
	}
	link := filepath.Join(home, "skills", "pete", "tool")
	target, err := os.Readlink(link)
	if err != nil || target != repo {
		t.Fatalf("store entry must be a symlink to the adopted dir, got %q, %v", target, err)
	}
	// The round trip's point: pack under the SAME id now works.
	s2, out, errBuf2 := testStreams("", false)
	if err := PackagePack(s2, packages.KindSkill, "pete/tool", ""); err != nil {
		t.Fatalf("pack after adopt: %v\n%s", err, errBuf2.String())
	}
	if !strings.Contains(out.String(), `id = "pete/tool"`) {
		t.Fatalf("packed manifest should carry the id, got:\n%s", out.String())
	}
}

// TestPackOutIntoPackedDir pins -o's reason to exist: the output file IS the
// manifest inside the adopted (symlinked) package dir — the path a shell
// redirect would truncate before byre reads it. Written after all reads, the
// result must be the full packed manifest, not a self-truncated stub.
func TestPackOutIntoPackedDir(t *testing.T) {
	installHome(t)
	repo := adoptRepoDir(t, "pete/tool", `kind = "skill"`)
	s, _, _ := testStreams("", false)
	if err := PackageAdopt(s, packages.KindSkill, repo); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(repo, "skill.toml")
	s2, _, errBuf := testStreams("", false)
	if err := PackagePack(s2, packages.KindSkill, "pete/tool", manifest); err != nil {
		t.Fatalf("pack -o: %v\n%s", err, errBuf.String())
	}
	b, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `id = "pete/tool"`) || !strings.Contains(string(b), `version = "1.1.0"`) {
		t.Fatalf("packed manifest must survive being its own output target, got:\n%s", b)
	}
	if !strings.Contains(errBuf.String(), "packed pete/tool -> "+manifest) {
		t.Fatalf("pack -o must announce the written file, got:\n%s", errBuf.String())
	}
}

// TestAdoptShadowsInstalled: adopting on the machine where the id is already
// installed (the publisher's own) must yield the LOCAL entry shadowing the
// snapshot — announced, not a conflict row.
func TestAdoptShadowsInstalled(t *testing.T) {
	installHome(t)
	uri, digest := publishSkill(t, "pete/tool", "1.0.0", "")
	s0, _, _ := testStreams("", false)
	if err := PackageInstall(s0, packages.KindSkill, uri, "sha256:"+digest, false); err != nil {
		t.Fatal(err)
	}
	repo := adoptRepoDir(t, "pete/tool", `kind = "skill"`)
	s, _, errBuf := testStreams("", false)
	if err := PackageAdopt(s, packages.KindSkill, repo); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "shadows installed 1.0.0") {
		t.Fatalf("adopt must announce the shadowed snapshot, got:\n%s", errBuf.String())
	}
}

// Adopt's refusals each name their rule: several rules can reject one
// directory, and the wrong one keeping a test green is the failure mode the
// fragments below guard against.
func TestAdoptRefusals(t *testing.T) {
	home := installHome(t)

	// No declared id: adopt cannot name a store path.
	noID := t.TempDir()
	if err := os.WriteFile(filepath.Join(noID, "skill.toml"), []byte("description = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _, _ := testStreams("", false)
	if err := PackageAdopt(s, packages.KindSkill, noID); err == nil || !strings.Contains(err.Error(), "declare an id in [package]") {
		t.Fatalf("want declared-id refusal, got %v", err)
	}

	// Kind mismatch: a template dir under `skill adopt`.
	tmpl := adoptRepoDir(t, "pete/shape", `kind = "template"`)
	s2, _, _ := testStreams("", false)
	if err := PackageAdopt(s2, packages.KindSkill, tmpl); err == nil || !strings.Contains(err.Error(), `declares kind "template"`) {
		t.Fatalf("want kind-mismatch refusal, got %v", err)
	}

	// Occupied store path (even a dangling link is an occupant).
	repo := adoptRepoDir(t, "pete/tool", `kind = "skill"`)
	if err := os.MkdirAll(filepath.Join(home, "skills", "pete"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "nowhere"), filepath.Join(home, "skills", "pete", "tool")); err != nil {
		t.Fatal(err)
	}
	s3, _, _ := testStreams("", false)
	if err := PackageAdopt(s3, packages.KindSkill, repo); err == nil || !strings.Contains(err.Error(), "already exists; remove it first") {
		t.Fatalf("want occupied-path refusal, got %v", err)
	}
	if err := os.Remove(filepath.Join(home, "skills", "pete", "tool")); err != nil {
		t.Fatal(err)
	}

	// Existing local package for the id: adopt must not manufacture a
	// duplicate-id conflict.
	other := filepath.Join(home, "skills", "pete", "tool")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "skill.toml"), []byte("[package]\nid = \"pete/tool\"\npackage_api = 1\nkind = \"skill\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	elsewhere := adoptRepoDir(t, "pete/tool", `kind = "skill"`)
	s4, _, _ := testStreams("", false)
	if err := PackageAdopt(s4, packages.KindSkill, elsewhere); err == nil || !strings.Contains(err.Error(), "already a local package") {
		t.Fatalf("want already-local refusal, got %v", err)
	}
}

// TestAdoptRollsBackInvalid: the catalog reload is adopt's real gate — a dir
// that ingests as INVALID (stage-2 unknown key) must not stay linked into
// the store.
func TestAdoptRollsBackInvalid(t *testing.T) {
	home := installHome(t)
	bad := t.TempDir()
	body := `not_a_real_key = true

[package]
id = "pete/broken"
package_api = 1
kind = "skill"
`
	if err := os.WriteFile(filepath.Join(bad, "skill.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _, _ := testStreams("", false)
	err := PackageAdopt(s, packages.KindSkill, bad)
	if err == nil || !strings.Contains(err.Error(), "adopt rolled back") {
		t.Fatalf("want rollback error, got %v", err)
	}
	if _, lerr := os.Lstat(filepath.Join(home, "skills", "pete", "broken")); !os.IsNotExist(lerr) {
		t.Fatalf("rolled-back adopt must remove the link, got %v", lerr)
	}
}
