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

func TestValidateWorktreeBase(t *testing.T) {
	for _, ok := range []string{"", "sibling", "~", "~/worktrees", "/abs/base"} {
		if err := (Config{WorktreeBase: ok}).Validate(); err != nil {
			t.Errorf("worktree_base %q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"relative-dir", "./x", "/has,comma"} {
		if err := (Config{WorktreeBase: bad}).Validate(); err == nil {
			t.Errorf("worktree_base %q should be rejected", bad)
		}
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

func TestValidateRejectsCommaInTarget(t *testing.T) {
	// A comma injects extra fields into docker's comma-delimited --mount value.
	m := Config{Mounts: []Mount{{Host: "/h", Target: "/x,readonly"}}}
	if err := m.Validate(); err == nil {
		t.Fatal("expected rejection of comma in mount target (--mount option injection)")
	}
	v := Config{Volumes: []Volume{{Name: "v", Role: "cache", Target: "/x,volume-opt=device=/"}}}
	if err := v.Validate(); err == nil {
		t.Fatal("expected rejection of comma in volume target")
	}
}

func TestValidateContent(t *testing.T) {
	bad := map[string]Config{
		"base newline":     {Base: "debian\nRUN evil"},
		"base space":       {Base: "debian AS x"},
		"apt shell":        {Apt: []string{"git; curl evil | sh"}},
		"apt space":        {Apt: []string{"git curl"}},
		"npm shell":        {NpmGlobal: []string{"pkg && evil"}},
		"npm redirect":     {NpmGlobal: []string{"pkg@>1 x"}},
		"env key space":    {Env: map[string]string{"A B": "v"}},
		"env key newline":  {Env: map[string]string{"A\nENV X": "v"}},
		"env key leading$": {Env: map[string]string{"1A": "v"}},
	}
	for name, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected rejection, got nil", name)
		}
	}
	// Legitimate specs must still pass.
	ok := Config{
		Base:      "registry.example.com:5000/org/img@sha256:abc",
		Apt:       []string{"git", "build-essential", "libssl-dev", "python3=3.11.2"},
		NpmGlobal: []string{"@anthropic-ai/claude-code", "typescript", "pnpm@8.15.0"},
		Env:       map[string]string{"NODE_ENV": "production", "_FOO": "1"},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid content rejected: %v", err)
	}
}

func TestValidateLayerAllowsRemovals(t *testing.T) {
	// A `!name` removal marker is legal in an unmerged layer but is a malformed
	// entry in a resolved config. ValidateLayer accepts it; Validate rejects it.
	layer := Config{
		Skills:  []string{"!devloop"},
		Volumes: []Volume{{Name: "!creds"}},
		Mounts:  []Mount{{Target: "!/x"}},
	}
	if err := layer.ValidateLayer(); err != nil {
		t.Fatalf("ValidateLayer rejected removal markers: %v", err)
	}
	if err := layer.Validate(); err == nil {
		t.Fatal("Validate should reject removal markers in a resolved config")
	}
	// ValidateLayer still catches a genuinely malformed real entry.
	bad := Config{Volumes: []Volume{{Name: "creds", Role: "bogus", Target: "/c"}}}
	if err := bad.ValidateLayer(); err == nil {
		t.Fatal("ValidateLayer should still reject a bad role on a real entry")
	}
}

func TestValidatePorts(t *testing.T) {
	ok := Config{Ports: []Port{{Container: 8080, Host: 8080, Interface: "127.0.0.1"}, {Container: 3000}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid ports rejected: %v", err)
	}
	bad := map[string]Config{
		"container out of range": {Ports: []Port{{Container: 0}}},
		"host out of range":      {Ports: []Port{{Container: 80, Host: 99999}}},
		"dup host binding":       {Ports: []Port{{Container: 80, Host: 8080}, {Container: 81, Host: 8080}}},
	}
	for name, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
	// Two blank-host ports (mirror distinct container ports) don't collide.
	if err := (Config{Ports: []Port{{Container: 80}, {Container: 81}}}).Validate(); err != nil {
		t.Errorf("ephemeral ports should not collide: %v", err)
	}
}

func TestMergePortsDedup(t *testing.T) {
	base := Config{Ports: []Port{{Container: 8080, Host: 8080}}}
	over := Config{Ports: []Port{{Container: 8080, Host: 8080}, {Container: 3000}}}
	got := Merge(base, over).Ports
	if len(got) != 2 {
		t.Fatalf("expected dedup to 2 ports, got %v", got)
	}
}

func TestListTemplates(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"go", "python"} {
		td := filepath.Join(dir, n)
		os.MkdirAll(td, 0o755)
		os.WriteFile(filepath.Join(td, "template.config"), []byte("base = \"x\"\n"), 0o644)
	}
	os.MkdirAll(filepath.Join(dir, "empty"), 0o755) // no template.config -> excluded
	got := ListTemplates(dir)
	if len(got) != 2 || got[0] != "go" || got[1] != "python" {
		t.Fatalf("ListTemplates = %v", got)
	}
}

// sampleConfig sets EVERY Config field to a non-zero sample. The merge growth
// guard reflects over Config and fails if a field is left zero here, so adding
// a field to Config forces adding it both here and to Merge.
func sampleConfig() Config {
	return Config{
		Engine:         "podman",
		Template:       "go",
		Agent:          "claude",
		Base:           "debian:bookworm",
		Dockerfile:     "Dockerfile.dev",
		SeedPrefs:      true,
		WorktreeBase:   "sibling",
		Apt:            []string{"jq"},
		NpmGlobal:      []string{"typescript"},
		Env:            map[string]string{"K": "v"},
		Files:          map[string]string{"a.txt": "/opt/a.txt"},
		Skills:         []string{"devloop"},
		Mounts:         []Mount{{Host: "/h", Target: "/t", Mode: "ro"}},
		Volumes:        []Volume{{Name: "v", Role: "cache", Target: "/c"}},
		Ports:          []Port{{Container: 8080}},
		DockerfilePre:  []string{"RUN true"},
		DockerfilePost: []string{"RUN false"},
		RunArgs:        []string{"--cap-add=X"},
	}
}

// TestMergeCoversEveryField is the growth guard for Merge's hand enumeration:
// a new Config field Merge doesn't handle would silently VANISH when set in an
// override layer (out starts as base, so only the base->out direction survives
// by default). Merging a fully-populated sample over an empty base — and vice
// versa — must reproduce it exactly, and reflection forces the sample to keep
// covering every field.
func TestMergeCoversEveryField(t *testing.T) {
	sample := sampleConfig()
	v := reflect.ValueOf(sample)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).IsZero() {
			t.Fatalf("sampleConfig leaves Config.%s zero — give it a sample value so the merge guard covers it (and handle it in Merge)", v.Type().Field(i).Name)
		}
	}
	if got := Merge(Config{}, sample); !reflect.DeepEqual(got, sample) {
		t.Errorf("Merge(empty, sample) must reproduce the sample — a field Merge doesn't propagate vanishes from override layers:\ngot  %+v\nwant %+v", got, sample)
	}
	if got := Merge(sample, Config{}); !reflect.DeepEqual(got, sample) {
		t.Errorf("Merge(sample, empty) must reproduce the sample:\ngot  %+v\nwant %+v", got, sample)
	}
}

// Within-layer duplicates are silent last-wins at merge — the losing entry
// vanishes before the resolved Validate can see it — so ValidateLayer must
// reject them. Cross-layer overrides stay legal (not this check's concern).
func TestValidateLayerRejectsWithinLayerDuplicates(t *testing.T) {
	dupMount := Config{Mounts: []Mount{
		{Host: "/a", Target: "/x", Mode: "ro"},
		{Host: "/b", Target: "/x", Mode: "ro"},
	}}
	if err := dupMount.ValidateLayer(); err == nil {
		t.Error("duplicate mount target within one layer must be rejected")
	}
	dupVol := Config{Volumes: []Volume{
		{Name: "v", Role: "cache", Target: "/c1"},
		{Name: "v", Role: "cache", Target: "/c2"},
	}}
	if err := dupVol.ValidateLayer(); err == nil {
		t.Error("duplicate volume name within one layer must be rejected")
	}
	// Cross-kind: a volume claiming a mount's target (or another volume's)
	// fails the resolved Validate at develop time — refuse at save instead.
	mixed := Config{
		Mounts:  []Mount{{Host: "/a", Target: "/x", Mode: "ro"}},
		Volumes: []Volume{{Name: "v", Role: "cache", Target: "/x"}},
	}
	if err := mixed.ValidateLayer(); err == nil {
		t.Error("mount + volume on one target within a layer must be rejected")
	}
	twoVols := Config{Volumes: []Volume{
		{Name: "a", Role: "cache", Target: "/x"},
		{Name: "b", Role: "cache", Target: "/x"},
	}}
	if err := twoVols.ValidateLayer(); err == nil {
		t.Error("two volumes on one target within a layer must be rejected")
	}

	// A removal marker plus the real entry it removes elsewhere is fine.
	withRemoval := Config{Mounts: []Mount{
		{Target: "!/x"},
		{Host: "/a", Target: "/x", Mode: "ro"},
	}}
	if err := withRemoval.ValidateLayer(); err != nil {
		t.Errorf("removal marker + real entry must stay saveable: %v", err)
	}
}
