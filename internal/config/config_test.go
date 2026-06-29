package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"byre/internal/project"
)

func TestMergeScalarOverride(t *testing.T) {
	got := Merge(Config{Engine: "docker", Base: "debian"}, Config{Engine: "podman"})
	if got.Engine != "podman" {
		t.Errorf("engine override: got %q", got.Engine)
	}
	if got.Base != "debian" {
		t.Errorf("base should persist when over is empty: got %q", got.Base)
	}
}

func TestMergeStringUnionDedup(t *testing.T) {
	got := Merge(Config{Skills: []string{"a", "b"}}, Config{Skills: []string{"b", "c"}}).Skills
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("union: got %v want %v", got, want)
	}
}

func TestMergeStringRemoval(t *testing.T) {
	got := Merge(Config{Skills: []string{"a", "b"}}, Config{Skills: []string{"!a", "c"}}).Skills
	if want := []string{"b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("removal: got %v want %v", got, want)
	}
}

func TestMergeMap(t *testing.T) {
	got := Merge(
		Config{Env: map[string]string{"X": "1", "Y": "2"}},
		Config{Env: map[string]string{"Y": "3", "Z": "4"}},
	).Env
	want := map[string]string{"X": "1", "Y": "3", "Z": "4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("map merge: got %v want %v", got, want)
	}
}

func TestMergeVolumesOverrideAndRemove(t *testing.T) {
	base := Config{Volumes: []Volume{
		{Name: "cache", Role: "cache", Target: "/c"},
		{Name: "creds", Role: "state", Target: "/s"},
	}}
	over := Config{Volumes: []Volume{
		{Name: "cache", Role: "cache", Target: "/c2"}, // override target
		{Name: "!creds"}, // remove
	}}
	got := Merge(base, over).Volumes
	if len(got) != 1 || got[0].Name != "cache" || got[0].Target != "/c2" {
		t.Errorf("volume override/remove: got %+v", got)
	}
}

func TestMergeMountsByTarget(t *testing.T) {
	base := Config{Mounts: []Mount{{Host: "/h", Target: "/t", Mode: "ro"}}}
	over := Config{Mounts: []Mount{{Host: "/h2", Target: "/t", Mode: "rw"}}}
	got := Merge(base, over).Mounts
	if len(got) != 1 || got[0].Mode != "rw" || got[0].Host != "/h2" {
		t.Errorf("mount override by target: got %+v", got)
	}
}

func TestRawBlocksAppendOnly(t *testing.T) {
	got := Merge(Config{DockerfilePre: []string{"RUN a"}}, Config{DockerfilePre: []string{"RUN a", "RUN b"}}).DockerfilePre
	if want := []string{"RUN a", "RUN a", "RUN b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("raw append-only (no dedup/removal): got %v want %v", got, want)
	}
}

func TestAptDoesNotHonorRemoval(t *testing.T) {
	// "!name" is reserved for named lists; in apt it's a literal package name.
	got := Merge(Config{Apt: []string{"a"}}, Config{Apt: []string{"!a", "b"}}).Apt
	if want := []string{"a", "!a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("apt should not honor !removal: got %v want %v", got, want)
	}
}

func TestValidateMountHostRequired(t *testing.T) {
	if err := (Config{Mounts: []Mount{{Target: "/t"}}}).Validate(); err == nil {
		t.Fatal("expected error for mount without host")
	}
}

func TestValidateTargetCollisions(t *testing.T) {
	cases := map[string]Config{
		"dup mount target": {Mounts: []Mount{
			{Host: "/a", Target: "/t"}, {Host: "/b", Target: "/t"},
		}},
		"dup volume name": {Volumes: []Volume{
			{Name: "v", Role: "cache", Target: "/x"}, {Name: "v", Role: "cache", Target: "/y"},
		}},
		"dup volume target": {Volumes: []Volume{
			{Name: "v1", Role: "cache", Target: "/t"}, {Name: "v2", Role: "cache", Target: "/t"},
		}},
		"mount/volume target collision": {
			Mounts:  []Mount{{Host: "/a", Target: "/shared"}},
			Volumes: []Volume{{Name: "v", Role: "cache", Target: "/shared"}},
		},
	}
	for name, c := range cases {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected collision error", name)
		}
	}
}

func TestLoadMissingTemplateErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()
	writeProjectCfg(t, proj, "template = \"nope\"\n")
	if _, err := Load(proj); err == nil {
		t.Fatal("expected error for missing explicitly-selected template")
	}
}

func TestValidateOptOutPath(t *testing.T) {
	if err := (Config{Dockerfile: "Dockerfile"}).Validate(); err != nil {
		t.Errorf("relative opt-out dockerfile should be allowed: %v", err)
	}
	if err := (Config{Dockerfile: "/etc/Dockerfile"}).Validate(); err == nil {
		t.Error("absolute opt-out dockerfile should be rejected")
	}
	if err := (Config{Dockerfile: "../Dockerfile"}).Validate(); err == nil {
		t.Error("escaping opt-out dockerfile should be rejected")
	}
}

func TestValidateRejectsBadEngine(t *testing.T) {
	if err := (Config{Engine: "containerd"}).Validate(); err == nil {
		t.Fatal("expected invalid-engine rejection")
	}
}

func TestValidateVolumeRules(t *testing.T) {
	cases := map[string]Config{
		"bad role":      {Volumes: []Volume{{Name: "v", Role: "nope", Target: "/t"}}},
		"no target":     {Volumes: []Volume{{Name: "v", Role: "cache"}}},
		"seed on cache": {Volumes: []Volume{{Name: "v", Role: "cache", Target: "/t", Seed: &Seed{Host: "/h"}}}},
		"empty seed":    {Volumes: []Volume{{Name: "v", Role: "state", Target: "/t", Seed: &Seed{}}}},
		"two seed srcs": {Volumes: []Volume{{Name: "v", Role: "state", Target: "/t", Seed: &Seed{Host: "/h", Literal: "x"}}}},
		"no name":       {Volumes: []Volume{{Role: "cache", Target: "/t"}}},
	}
	for name, c := range cases {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestValidateVolumeNameCharset(t *testing.T) {
	// Dotfile-style state volume names must be accepted (.claude etc).
	ok := Config{Volumes: []Volume{{Name: ".claude", Role: "state", Target: "/home/dev/.claude", Seed: &Seed{Host: "~/.claude"}}}}
	if err := ok.Validate(); err != nil {
		t.Errorf(".claude volume name rejected: %v", err)
	}
	bad := Config{Volumes: []Volume{{Name: "bad/name", Role: "cache", Target: "/t"}}}
	if err := bad.Validate(); err == nil {
		t.Error("expected rejection of volume name with '/'")
	}
}

func TestValidateLiteralSeed(t *testing.T) {
	ok := Config{Volumes: []Volume{{Name: "c", Role: "state", Target: "/t", Seed: &Seed{Literal: "x", Path: "a/b.conf"}}}}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid literal seed rejected: %v", err)
	}
	bad := map[string]Config{
		"literal without path": {Volumes: []Volume{{Name: "c", Role: "state", Target: "/t", Seed: &Seed{Literal: "x"}}}},
		"literal abs path":     {Volumes: []Volume{{Name: "c", Role: "state", Target: "/t", Seed: &Seed{Literal: "x", Path: "/etc/x"}}}},
		"literal escape path":  {Volumes: []Volume{{Name: "c", Role: "state", Target: "/t", Seed: &Seed{Literal: "x", Path: "../x"}}}},
		"path on host seed":    {Volumes: []Volume{{Name: "c", Role: "state", Target: "/t", Seed: &Seed{Host: "~/x", Path: "a"}}}},
	}
	for name, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestValidateAcceptsGoodConfig(t *testing.T) {
	c := Config{
		Engine:  "auto",
		Volumes: []Volume{{Name: "creds", Role: "state", Target: "/home/dev/.claude", Seed: &Seed{Host: "~/.claude"}}},
		Mounts:  []Mount{{Host: "/data", Target: "/data", Mode: "ro"}},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("good config rejected: %v", err)
	}
}

func TestLoadCascade(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()

	writeFile(t, filepath.Join(home, "default.config"),
		"engine = \"docker\"\nbase = \"debian:bookworm\"\nskills = [\"base\"]\n")
	tmplDir := filepath.Join(home, "templates", "node")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tmplDir, "template.config"),
		"base = \"node:22\"\napt = [\"build-essential\"]\nskills = [\"node-tools\"]\n")
	writeProjectCfg(t, proj,
		"template = \"node\"\nagent = \"claude\"\nskills = [\"proj\"]\n")

	cfg, err := Load(proj)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Base != "node:22" {
		t.Errorf("base: template should override default: got %q", cfg.Base)
	}
	if cfg.Engine != "docker" {
		t.Errorf("engine: should come from default: got %q", cfg.Engine)
	}
	if cfg.Agent != "claude" {
		t.Errorf("agent: got %q", cfg.Agent)
	}
	if want := []string{"base", "node-tools", "proj"}; !reflect.DeepEqual(cfg.Skills, want) {
		t.Errorf("skills cascade: got %v want %v", cfg.Skills, want)
	}
	if want := []string{"build-essential"}; !reflect.DeepEqual(cfg.Apt, want) {
		t.Errorf("apt from template: got %v want %v", cfg.Apt, want)
	}
}

func TestLoadIgnoresDefaultTemplateAndAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir() // no byre.config

	// default.config sets template/agent (picker pre-selections) plus base/apt.
	writeFile(t, filepath.Join(home, "default.config"),
		"agent = \"claude\"\ntemplate = \"node\"\nbase = \"debian:bookworm\"\napt = [\"git\"]\n")

	cfg, err := Load(proj)
	if err != nil {
		t.Fatal(err)
	}
	// template/agent must NOT cascade from default.config...
	if cfg.Agent != "" {
		t.Errorf("default agent must not cascade, got %q", cfg.Agent)
	}
	if cfg.Base != "debian:bookworm" {
		t.Errorf("default template must not apply (base should stay debian), got %q", cfg.Base)
	}
	// ...but base/apt still do.
	if len(cfg.Apt) != 1 || cfg.Apt[0] != "git" {
		t.Errorf("default base/apt should still cascade, got %v", cfg.Apt)
	}
}

func TestLoadMalformedTOML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()
	writeProjectCfg(t, proj, "this is = not = valid toml\n")
	if _, err := Load(proj); err == nil {
		t.Fatal("expected malformed-TOML error")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeProjectCfg writes a project's byre.config to the host-side store
// (~/.byre/projects/<id>/byre.config), where Load now reads it.
func writeProjectCfg(t *testing.T, projectDir, content string) {
	t.Helper()
	p, err := project.Resolve(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(p.Dir, ProjectConfigName), content)
}

func TestValidateRejectsNonAbsoluteVolumeTarget(t *testing.T) {
	c := Config{Volumes: []Volume{{Name: "v", Role: "cache", Target: "rel/path"}}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected rejection of non-absolute volume target")
	}
}

func TestValidateRejectsControlCharInTarget(t *testing.T) {
	c := Config{Volumes: []Volume{{Name: "v", Role: "cache", Target: "/x\nRUN evil"}}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected rejection of newline in volume target (Dockerfile injection)")
	}
	m := Config{Mounts: []Mount{{Host: "/h", Target: "/x\ny"}}}
	if err := m.Validate(); err == nil {
		t.Fatal("expected rejection of newline in mount target")
	}
}
