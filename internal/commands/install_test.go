package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/packages"
)

// publishSkill writes a distributable skill (manifest + payloads) into a
// directory and returns the manifest path and its D5f digest.
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
	// Fresh ID, no references: proceeds in a pipe (D9c).
	if err := SkillInstall(s, uri, "sha256:"+digest, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "grants nothing until enabled") {
		t.Fatalf("missing boundary statement:\n%s", errBuf.String())
	}
	// Snapshot exists; resolves as installed through a fresh catalog.
	cat, err := packages.LoadCatalog(home, nil, "v0.2.0", "0.2.0")
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
}

func TestInstallAsActivationGuard(t *testing.T) {
	home := installHome(t)
	// A stored config references the id BEFORE it is installed (D9b').
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
	os.MkdirAll(pdir, 0o755)
	os.WriteFile(filepath.Join(pdir, "byre.config"), []byte("agent = \"none\"\nskills = [\"pete/tool\"]\n"), 0o644)

	// Pipe without --yes refuses (always, D9c).
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
