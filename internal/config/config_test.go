package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/project"
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

func TestMergeMountsReenableByReplacing(t *testing.T) {
	// disabled is part of the whole-entry replace: a later layer restating the
	// target without it re-enables the mount (no per-field merge).
	base := Config{Mounts: []Mount{{Host: "/h", Target: "/t", Mode: "rw", Disabled: true}}}
	over := Config{Mounts: []Mount{{Host: "/h", Target: "/t", Mode: "rw"}}}
	got := Merge(base, over).Mounts
	if len(got) != 1 || got[0].Disabled {
		t.Errorf("later layer should re-enable by replacement: got %+v", got)
	}
}

func TestValidateDisabledMountStillChecked(t *testing.T) {
	// A disabled mount is still config: shape errors and target collisions
	// fail now, not on re-enable.
	if err := (Config{Mounts: []Mount{{Target: "/t", Disabled: true}}}).Validate(); err == nil || !strings.Contains(err.Error(), "host path is required") {
		t.Errorf("disabled mount without host should still fail validation, got %v", err)
	}
	dup := Config{Mounts: []Mount{
		{Host: "/a", Target: "/t", Disabled: true}, {Host: "/b", Target: "/t"},
	}}
	if err := dup.Validate(); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Errorf("disabled mount should still collide on target, got %v", err)
	}
}

func TestRawBlocksAppendOnly(t *testing.T) {
	got := Merge(Config{DockerfilePre: []string{"RUN a"}}, Config{DockerfilePre: []string{"RUN a", "RUN b"}}).DockerfilePre
	if want := []string{"RUN a", "RUN a", "RUN b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("raw append-only (no dedup/removal): got %v want %v", got, want)
	}
}

func TestAptHonorsRemoval(t *testing.T) {
	// Reversed by ADR 0018: apt takes `!name` like every other string list.
	// (Previously pinned as literal-only; packageRe never admitted a leading
	// '!', so no real package is shadowed by the marker.)
	got := Merge(Config{Apt: []string{"a"}}, Config{Apt: []string{"!a", "b"}}).Apt
	if want := []string{"b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("apt should honor !removal: got %v want %v", got, want)
	}
}

func TestValidateMountHostRequired(t *testing.T) {
	if err := (Config{Mounts: []Mount{{Target: "/t"}}}).Validate(); err == nil || !strings.Contains(err.Error(), "host path is required") {
		t.Fatalf("expected error for mount without host, got %v", err)
	}
}

// TestValidateHostPathShape pins host-path shape at validate time: run
// assembly (expandHostPath) demands ~-anchored or absolute with no comma, and
// a path that can't survive that must fail at save/validate with the file
// open, not at the next develop.
func TestValidateHostPathShape(t *testing.T) {
	for name, host := range map[string]string{
		"tilde": "~", "tilde slash": "~/.claude", "absolute": "/var/run/x.sock",
	} {
		c := Config{Mounts: []Mount{{Host: host, Target: "/t"}}}
		if err := c.Validate(); err != nil {
			t.Errorf("%s mount host rejected: %v", name, err)
		}
	}
	for name, host := range map[string]string{
		"relative": "run/docker.sock", "dot relative": "./x", "tilde user": "~pete/x", "comma": "/a,b",
	} {
		c := Config{Mounts: []Mount{{Host: host, Target: "/t"}}}
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("host path %q", host)) {
			t.Errorf("%s mount host accepted; expandHostPath would refuse it at run assembly, got %v", name, err)
		}
	}
	seeded := Config{Volumes: []Volume{{Name: "v", Role: "state", Target: "/t", Seed: &Seed{Host: "x/y"}}}}
	if err := seeded.Validate(); err == nil || !strings.Contains(err.Error(), `host path "x/y"`) {
		t.Errorf("relative seed host accepted; expandHostPath would refuse it at seed time, got %v", err)
	}
}

func TestValidateTargetCollisions(t *testing.T) {
	cases := map[string]struct {
		cfg     Config
		wantErr string
	}{
		"dup mount target": {Config{Mounts: []Mount{
			{Host: "/a", Target: "/t"}, {Host: "/b", Target: "/t"},
		}}, "mount target /t collides"},
		"dup volume name": {Config{Volumes: []Volume{
			{Name: "v", Role: "cache", Target: "/x"}, {Name: "v", Role: "cache", Target: "/y"},
		}}, "duplicate name"},
		"dup volume target": {Config{Volumes: []Volume{
			{Name: "v1", Role: "cache", Target: "/t"}, {Name: "v2", Role: "cache", Target: "/t"},
		}}, "target /t collides with volume v1"},
		"mount/volume target collision": {Config{
			Mounts:  []Mount{{Host: "/a", Target: "/shared"}},
			Volumes: []Volume{{Name: "v", Role: "cache", Target: "/shared"}},
		}, "target /shared collides with mount /shared"},
	}
	for name, c := range cases {
		if err := c.cfg.Validate(); err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: expected collision error containing %q, got %v", name, c.wantErr, err)
		}
	}
}

func TestLoadMissingTemplateErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()
	writeProjectCfg(t, proj, "template = \"nope\"\n")
	if _, err := Load(proj); err == nil || !strings.Contains(err.Error(), `template "nope"`) {
		t.Fatalf("expected error for missing explicitly-selected template, got %v", err)
	}
}

func TestDockerfileKeyRejectedLoudly(t *testing.T) {
	// The full-Dockerfile opt-out was removed (ADR 0014): a config still
	// carrying the key must fail as unknown, not be silently ignored.
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()
	writeProjectCfg(t, proj, "dockerfile = \"Dockerfile\"\n")
	if _, err := Load(proj); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("dockerfile key must fail loudly as unknown, got: %v", err)
	}
}

func TestValidateRejectsBadEngine(t *testing.T) {
	if err := (Config{Engine: "containerd"}).Validate(); err == nil || !strings.Contains(err.Error(), `engine: "containerd" invalid`) {
		t.Fatalf("expected invalid-engine rejection, got %v", err)
	}
}

func TestValidateWorktreeBase(t *testing.T) {
	for _, ok := range []string{"", "sibling", "~", "~/worktrees", "/abs/base"} {
		if err := (Config{WorktreeBase: ok}).Validate(); err != nil {
			t.Errorf("worktree_base %q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"relative-dir", "./x", "/has,comma"} {
		if err := (Config{WorktreeBase: bad}).Validate(); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("worktree_base = %q", bad)) {
			t.Errorf("worktree_base %q should be rejected, got %v", bad, err)
		}
	}
}

func TestValidateVolumeRules(t *testing.T) {
	cases := map[string]struct {
		cfg     Config
		wantErr string
	}{
		"bad role":      {Config{Volumes: []Volume{{Name: "v", Role: "nope", Target: "/t"}}}, `role "nope" invalid`},
		"no target":     {Config{Volumes: []Volume{{Name: "v", Role: "cache"}}}, "target is required"},
		"seed on cache": {Config{Volumes: []Volume{{Name: "v", Role: "cache", Target: "/t", Seed: &Seed{Host: "/h"}}}}, "seed is only valid for state-role"},
		"empty seed":    {Config{Volumes: []Volume{{Name: "v", Role: "state", Target: "/t", Seed: &Seed{}}}}, "seed set but empty"},
		"two seed srcs": {Config{Volumes: []Volume{{Name: "v", Role: "state", Target: "/t", Seed: &Seed{Host: "/h", Literal: "x"}}}}, "both host and literal"},
		"no name":       {Config{Volumes: []Volume{{Role: "cache", Target: "/t"}}}, "name is required"},
	}
	for name, c := range cases {
		if err := c.cfg.Validate(); err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: expected validation error containing %q, got %v", name, c.wantErr, err)
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
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "not allowed in a docker volume name") {
		t.Errorf("expected rejection of volume name with '/', got %v", err)
	}
}

func TestValidateVolumeScope(t *testing.T) {
	ok := Config{Volumes: []Volume{{Name: "claude-identity", Role: "state", Target: "/t", Scope: "machine"}}}
	if err := ok.Validate(); err != nil {
		t.Errorf("machine scope rejected: %v", err)
	}
	explicit := Config{Volumes: []Volume{{Name: "v", Role: "cache", Target: "/t", Scope: "project"}}}
	if err := explicit.Validate(); err != nil {
		t.Errorf("explicit project scope rejected: %v", err)
	}
	bad := Config{Volumes: []Volume{{Name: "v", Role: "cache", Target: "/t", Scope: "global"}}}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), `scope "global" invalid`) {
		t.Errorf("expected rejection of unknown scope, got %v", err)
	}
	// seed + machine scope don't compose: the seed pipeline names its target
	// volume project-scoped, and identity volumes are box-born (ADR 0017).
	seeded := Config{Volumes: []Volume{{Name: "v", Role: "state", Target: "/t", Scope: "machine", Seed: &Seed{Host: "~/x"}}}}
	if err := seeded.Validate(); err == nil || !strings.Contains(err.Error(), "not valid on a machine-scoped volume") {
		t.Errorf("expected rejection of seed on a machine-scoped volume, got %v", err)
	}
}

func TestValidateLiteralSeed(t *testing.T) {
	ok := Config{Volumes: []Volume{{Name: "c", Role: "state", Target: "/t", Seed: &Seed{Literal: "x", Path: "a/b.conf"}}}}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid literal seed rejected: %v", err)
	}
	bad := map[string]struct {
		cfg     Config
		wantErr string
	}{
		"literal without path": {Config{Volumes: []Volume{{Name: "c", Role: "state", Target: "/t", Seed: &Seed{Literal: "x"}}}}, "literal seed requires a path"},
		"literal abs path":     {Config{Volumes: []Volume{{Name: "c", Role: "state", Target: "/t", Seed: &Seed{Literal: "x", Path: "/etc/x"}}}}, "must be relative"},
		"literal escape path":  {Config{Volumes: []Volume{{Name: "c", Role: "state", Target: "/t", Seed: &Seed{Literal: "x", Path: "../x"}}}}, "must be relative"},
		"path on host seed":    {Config{Volumes: []Volume{{Name: "c", Role: "state", Target: "/t", Seed: &Seed{Host: "~/x", Path: "a"}}}}, "seed path is only for literal seeds"},
	}
	for name, c := range bad {
		if err := c.cfg.Validate(); err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: expected validation error containing %q, got %v", name, c.wantErr, err)
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
	// Templates are shape only: no skills/agent. Composition is the
	// project's (or a preset's) job.
	writeFile(t, filepath.Join(tmplDir, "template.config"),
		"base = \"node:22\"\napt = [\"build-essential\"]\n")
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
	if want := []string{"base", "proj"}; !reflect.DeepEqual(cfg.Skills, want) {
		t.Errorf("skills cascade: got %v want %v", cfg.Skills, want)
	}
	if want := []string{"build-essential"}; !reflect.DeepEqual(cfg.Apt, want) {
		t.Errorf("apt from template: got %v want %v", cfg.Apt, want)
	}
}

// Templates may not set agent/skills. A hand-made template that does
// is INVALID and Load fails when it is selected. template = "none" still
// resolves as no template at all (not a lookup of a template named "none").
func TestTemplateAgentBannedAndNoneSentinel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()

	tmplDir := filepath.Join(home, "templates", "opinionated")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tmplDir, "template.config"), "agent = \"claude\"\n")
	writeProjectCfg(t, proj, "template = \"opinionated\"\nagent = \"none\"\n")

	if _, err := Load(proj); err == nil || !strings.Contains(err.Error(), "agent is not allowed in template.config") {
		t.Fatalf("template with agent must be a validation error (composition belongs in a preset), got %v", err)
	}

	// template = "none" resolves as no template at all.
	proj2 := t.TempDir()
	writeProjectCfg(t, proj2, "template = \"none\"\nagent = \"none\"\n")
	cfg2, err := Load(proj2)
	if err != nil {
		t.Fatalf("template=none must not be looked up as a template dir: %v", err)
	}
	if cfg2.Template != "" || cfg2.Agent != "" {
		t.Fatalf("sentinels must resolve to empty, got template=%q agent=%q", cfg2.Template, cfg2.Agent)
	}
}

func TestLoadIgnoresDefaultTemplateAndAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir() // no byre.config

	// default.config sets template/agent (picker pre-selections) and
	// shared_auth_declined (vestigial v0.1.7 picker state, ADR 0025 — must
	// still parse and never cascade) plus base/apt.
	writeFile(t, filepath.Join(home, "default.config"),
		"agent = \"claude\"\ntemplate = \"node\"\nshared_auth_declined = [\"claude\"]\nbase = \"debian:bookworm\"\napt = [\"git\"]\n")

	cfg, err := Load(proj)
	if err != nil {
		t.Fatal(err)
	}
	// template/agent must NOT cascade from default.config...
	if cfg.Agent != "" {
		t.Errorf("default agent must not cascade, got %q", cfg.Agent)
	}
	// ...nor the picker-owned decline record...
	if len(cfg.SharedAuthDeclined) != 0 {
		t.Errorf("shared_auth_declined must not cascade, got %v", cfg.SharedAuthDeclined)
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
	if _, err := Load(proj); err == nil || !strings.Contains(err.Error(), "toml") {
		t.Fatalf("expected malformed-TOML error, got %v", err)
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
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("expected rejection of non-absolute volume target, got %v", err)
	}
}

func TestValidateRejectsControlCharInTarget(t *testing.T) {
	c := Config{Volumes: []Volume{{Name: "v", Role: "cache", Target: "/x\nRUN evil"}}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "must not contain control characters") {
		t.Fatalf("expected rejection of newline in volume target (Dockerfile injection), got %v", err)
	}
	m := Config{Mounts: []Mount{{Host: "/h", Target: "/x\ny"}}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "must not contain control characters") {
		t.Fatalf("expected rejection of newline in mount target, got %v", err)
	}
}

func TestValidateRejectsCommaInTarget(t *testing.T) {
	// A comma injects extra fields into docker's comma-delimited --mount value.
	m := Config{Mounts: []Mount{{Host: "/h", Target: "/x,readonly"}}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "must not contain a comma") {
		t.Fatalf("expected rejection of comma in mount target (--mount option injection), got %v", err)
	}
	v := Config{Volumes: []Volume{{Name: "v", Role: "cache", Target: "/x,volume-opt=device=/"}}}
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "must not contain a comma") {
		t.Fatalf("expected rejection of comma in volume target, got %v", err)
	}
}

func TestValidateContent(t *testing.T) {
	bad := map[string]struct {
		cfg     Config
		wantErr string
	}{
		"base newline":     {Config{Base: "debian\nRUN evil"}, "not a valid image reference"},
		"base space":       {Config{Base: "debian AS x"}, "not a valid image reference"},
		"apt shell":        {Config{Apt: []string{"git; curl evil | sh"}}, "not a valid package name"},
		"apt space":        {Config{Apt: []string{"git curl"}}, "not a valid package name"},
		"npm shell":        {Config{NpmGlobal: []string{"pkg && evil"}}, "not a valid package spec"},
		"npm redirect":     {Config{NpmGlobal: []string{"pkg@>1 x"}}, "not a valid package spec"},
		"env key space":    {Config{Env: map[string]string{"A B": "v"}}, "not a valid environment variable name"},
		"env key newline":  {Config{Env: map[string]string{"A\nENV X": "v"}}, "not a valid environment variable name"},
		"env key leading$": {Config{Env: map[string]string{"1A": "v"}}, "not a valid environment variable name"},
	}
	for name, c := range bad {
		if err := c.cfg.Validate(); err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: expected rejection containing %q, got %v", name, c.wantErr, err)
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
	if err := layer.Validate(); err == nil || !strings.Contains(err.Error(), "mount !/x") {
		t.Fatalf("Validate should reject removal markers in a resolved config, got %v", err)
	}
	// ValidateLayer still catches a genuinely malformed real entry.
	bad := Config{Volumes: []Volume{{Name: "creds", Role: "bogus", Target: "/c"}}}
	if err := bad.ValidateLayer(); err == nil || !strings.Contains(err.Error(), `role "bogus" invalid`) {
		t.Fatalf("ValidateLayer should still reject a bad role on a real entry, got %v", err)
	}
}

func TestValidateLayerMarkersAreBare(t *testing.T) {
	// A removal marker takes only its identity field — same stance as port
	// remove entries. Anything more means the author probably intended a real
	// entry with a mistyped `!` (the editor's add path always sets host+mode,
	// and merge would silently turn it into a removal).
	cases := []Config{
		{Mounts: []Mount{{Target: "!/x", Host: "/host"}}},
		{Mounts: []Mount{{Target: "!/x", Mode: "ro"}}},
		{Mounts: []Mount{{Target: "!/x", Disabled: true}}},
		{Volumes: []Volume{{Name: "!creds", Target: "/c"}}},
		{Volumes: []Volume{{Name: "!creds", Role: "state"}}},
		{Volumes: []Volume{{Name: "!creds", Scope: "machine"}}},
		{Volumes: []Volume{{Name: "!creds", Seed: &Seed{Host: "~/x"}}}},
	}
	for i, c := range cases {
		if err := c.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "removal marker takes only") {
			t.Errorf("case %d: marker with extra fields should be rejected: %+v, got %v", i, c, err)
		}
	}
}

func TestValidateLayerRejectsEmptyMarkers(t *testing.T) {
	// A bare "!" is a removal marker of nothing — it would merge as a silent
	// no-op (remove the entry named ""). isRemoval requires an identity, so a
	// bare "!" falls through to the real-entry shape checks (packageRe,
	// ParseEgress, validContainerTarget, volumeNameRe), which are all loud;
	// the skills list has no shape grammar, so it gets an explicit guard that
	// also covers an empty-string entry.
	cases := []struct {
		cfg     Config
		wantErr string
	}{
		{Config{Apt: []string{"!"}}, "not a valid package name"},
		{Config{NpmGlobal: []string{"!"}}, "not a valid package spec"},
		{Config{Egress: []string{"!"}}, "not a valid host[:port]"},
		{Config{EgressOffered: []string{"!"}}, "not a valid host[:port]"},
		{Config{Mounts: []Mount{{Target: "!"}}}, "host path is required"},
		{Config{Volumes: []Volume{{Name: "!"}}}, "not allowed in a docker volume name"},
		{Config{Skills: []string{"!"}}, "missing a skill name"},
		{Config{Skills: []string{""}}, "missing a skill name"},
	}
	for i, c := range cases {
		if err := c.cfg.ValidateLayer(); err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("case %d: bare/empty marker should be rejected with %q: %+v, got %v", i, c.wantErr, c.cfg, err)
		}
	}
}

func TestValidatePorts(t *testing.T) {
	ok := Config{Ports: []Port{{Container: 8080, Host: 8080, Interface: "127.0.0.1"}, {Container: 3000}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid ports rejected: %v", err)
	}
	bad := map[string]struct {
		cfg     Config
		wantErr string
	}{
		"container out of range": {Config{Ports: []Port{{Container: 0}}}, "container port 0 out of range"},
		"host out of range":      {Config{Ports: []Port{{Container: 80, Host: 99999}}}, "host port 99999 out of range"},
		"dup host binding":       {Config{Ports: []Port{{Container: 80, Host: 8080}, {Container: 81, Host: 8080}}}, "host binding 127.0.0.1:8080 is used by two ports"},
		// The interface lands in docker's colon-delimited -p grammar: only a
		// canonical IPv4 literal may pass, or the value fails (or changes
		// meaning) at engine invocation instead of at validation.
		"hostname interface":      {Config{Ports: []Port{{Container: 80, Interface: "localhost"}}}, "must be an IPv4 address literal"},
		"ipv6 interface":          {Config{Ports: []Port{{Container: 80, Interface: "::1"}}}, "must be an IPv4 address literal"},
		"mapped-ipv4 spelling":    {Config{Ports: []Port{{Container: 80, Interface: "::ffff:127.0.0.1"}}}, "must be an IPv4 address literal"},
		"whitespace interface":    {Config{Ports: []Port{{Container: 80, Interface: " 127.0.0.1"}}}, "must be an IPv4 address literal"},
		"colon-bearing interface": {Config{Ports: []Port{{Container: 80, Interface: "127.0.0.1:80"}}}, "must be an IPv4 address literal"},
	}
	for name, c := range bad {
		if err := c.cfg.Validate(); err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: expected a validation error containing %q, got %v", name, c.wantErr, err)
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
	home := t.TempDir()
	for _, n := range []string{"go", "python"} {
		td := filepath.Join(home, "templates", n)
		mustMkdirAll(t, td, 0o755)
		mustWriteFile(t, filepath.Join(td, "template.config"), []byte("base = \"x\"\n"), 0o644)
	}
	mustMkdirAll(t, filepath.Join(home, "templates", "empty"), 0o755) // no template.config -> excluded
	got := ListTemplates(home)
	if len(got) != 2 || got[0] != "go" || got[1] != "python" {
		t.Fatalf("ListTemplates = %v", got)
	}
}

// sampleConfig sets EVERY Config field to a non-zero sample. The merge growth
// guard reflects over Config and fails if a field is left zero here, so adding
// a field to Config forces adding it both here and to Merge.
func sampleConfig() Config {
	return Config{
		Engine:             "podman",
		Extends:            "torn",
		Template:           "go",
		Agent:              "claude",
		Base:               "debian:bookworm",
		SeedPrefs:          true,
		WorktreeBase:       "sibling",
		Apt:                []string{"jq"},
		NpmGlobal:          []string{"typescript"},
		Env:                map[string]string{"K": "v"},
		Files:              map[string]string{"a.txt": "/opt/a.txt"},
		Skills:             []string{"devloop"},
		SharedAuth:         SharedAuthPref{Yes: []string{"claude"}},
		Sources:            map[string]SourceHint{"pete/x": {URI: "https://example.test/x/skill.toml", Digest: "sha256:ab", From: "project config"}},
		SharedAuthDeclined: []string{"claude"},
		EnvFromHost:        map[string]string{"GIT_AUTHOR_NAME": "git:user.name"},
		Egress:             []string{"grafana.com"},
		EgressClosed:       []string{"statsig.anthropic.com"},
		EgressOffered:      []string{"registry.npmjs.org"},
		Mounts:             []Mount{{Host: "/h", Target: "/t", Mode: "ro"}},
		Volumes:            []Volume{{Name: "v", Role: "cache", Target: "/c"}},
		Ports:              []Port{{Container: 8080}},
		MCPs:               []MCP{{Name: "github", Command: []string{"github-mcp-server"}}},
		MCPClosed:          []string{"linear"},
		ClaudeSkills:       []ClaudeSkill{{Name: "tdd-loop", Path: "~/claude-skills/tdd-loop"}},
		ClaudeSkillsClosed: []string{"review"},
		DockerfilePre:      []string{"RUN true"},
		DockerfilePost:     []string{"RUN false"},
		RunArgs:            []string{"--cap-add=X"},
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
	if err := dupMount.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "mount target /x collides with mount /x in this file") {
		t.Errorf("duplicate mount target within one layer must be rejected, got %v", err)
	}
	dupVol := Config{Volumes: []Volume{
		{Name: "v", Role: "cache", Target: "/c1"},
		{Name: "v", Role: "cache", Target: "/c2"},
	}}
	if err := dupVol.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "volume v appears twice in this file") {
		t.Errorf("duplicate volume name within one layer must be rejected, got %v", err)
	}
	// Cross-kind: a volume claiming a mount's target (or another volume's)
	// fails the resolved Validate at develop time — refuse at save instead.
	mixed := Config{
		Mounts:  []Mount{{Host: "/a", Target: "/x", Mode: "ro"}},
		Volumes: []Volume{{Name: "v", Role: "cache", Target: "/x"}},
	}
	if err := mixed.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "volume v target /x collides with mount /x in this file") {
		t.Errorf("mount + volume on one target within a layer must be rejected, got %v", err)
	}
	twoVols := Config{Volumes: []Volume{
		{Name: "a", Role: "cache", Target: "/x"},
		{Name: "b", Role: "cache", Target: "/x"},
	}}
	if err := twoVols.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "volume b target /x collides with volume a in this file") {
		t.Errorf("two volumes on one target within a layer must be rejected, got %v", err)
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

// TestLoadRejectsWithinLayerCollisionInAnyLayer pins that the per-layer rules
// hold for HAND-EDITED files too, not just editor saves — a duplicate in any
// cascade layer errors at load, naming the file, instead of silently merging
// last-wins. ParseFile stays lenient so the editor can open a broken file.
func TestLoadRejectsWithinLayerCollisionInAnyLayer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()
	dup := "[[mounts]]\nhost = \"/a\"\ntarget = \"/x\"\n[[mounts]]\nhost = \"/b\"\ntarget = \"/x\"\n"

	// Project layer (the host-side store copy).
	p, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	mustMkdirAll(t, p.Dir, 0o755)
	storeCfg := filepath.Join(p.Dir, ProjectConfigName)
	mustWriteFile(t, storeCfg, []byte(dup), 0o644)
	if _, err := Load(proj); err == nil || !strings.Contains(err.Error(), storeCfg) {
		t.Errorf("duplicate in the project layer should fail load naming the file, got %v", err)
	}
	mustRemove(t, storeCfg)

	// Default layer.
	defCfg := filepath.Join(home, "default.config")
	mustWriteFile(t, defCfg, []byte(dup), 0o644)
	if _, err := Load(proj); err == nil || !strings.Contains(err.Error(), defCfg) {
		t.Errorf("duplicate in default.config should fail load naming the file, got %v", err)
	}
	mustRemove(t, defCfg)

	// ParseFile (the editor's open path) must still tolerate it.
	mustWriteFile(t, storeCfg, []byte(dup), 0o644)
	if _, err := ParseFile(storeCfg); err != nil {
		t.Errorf("ParseFile must stay lenient so the editor can open a broken file: %v", err)
	}
}

func TestMergeAptNpmRemoval(t *testing.T) {
	// ADR 0018: package lists take the same `!name` off-switch as skills.
	got := Merge(Config{Apt: []string{"ripgrep", "htop"}}, Config{Apt: []string{"!htop", "jq"}}).Apt
	if want := []string{"ripgrep", "jq"}; !reflect.DeepEqual(got, want) {
		t.Errorf("apt removal: got %v want %v", got, want)
	}
	got = Merge(Config{NpmGlobal: []string{"prettier"}}, Config{NpmGlobal: []string{"!prettier"}}).NpmGlobal
	if len(got) != 0 {
		t.Errorf("npm_global removal: got %v want empty", got)
	}
}

func TestMergePortsRemove(t *testing.T) {
	// remove=true keys on container port ALONE: every inherited binding of that
	// container port dies, whatever its interface/host (ADR 0018).
	base := Config{Ports: []Port{
		{Container: 5432},
		{Container: 5432, Interface: "0.0.0.0", Host: 15432},
		{Container: 3000},
	}}
	over := Config{Ports: []Port{{Container: 5432, Remove: true}}}
	got := Merge(base, over).Ports
	if len(got) != 1 || got[0].Container != 3000 {
		t.Errorf("port remove: got %+v", got)
	}
	for _, p := range got {
		if p.Remove {
			t.Errorf("merge must consume remove markers, got %+v", p)
		}
	}
}

func TestMergePortsRemoveAfterAdditions(t *testing.T) {
	// Removals apply after the same layer's additions, matching the `!name`
	// lists: an add+remove of the same container port in one layer resolves off.
	base := Config{Ports: []Port{{Container: 8080}}}
	over := Config{Ports: []Port{
		{Container: 8080, Host: 18080},
		{Container: 8080, Remove: true},
	}}
	if got := Merge(base, over).Ports; len(got) != 0 {
		t.Errorf("add+remove same layer should resolve off: got %+v", got)
	}
}

func TestValidateLayerAcceptsPackageMarkers(t *testing.T) {
	c := Config{Apt: []string{"!htop"}, NpmGlobal: []string{"!prettier"}}
	if err := c.ValidateLayer(); err != nil {
		t.Errorf("layer with package removal markers should validate: %v", err)
	}
	// A marker surviving to a RESOLVED config is a bug and must be rejected.
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), `apt package "!htop"`) {
		t.Errorf("resolved config with a `!name` package should fail validation, got %v", err)
	}
}

func TestValidateLayerPortRemoveEntries(t *testing.T) {
	ok := Config{Ports: []Port{{Container: 5432, Remove: true}}}
	if err := ok.ValidateLayer(); err != nil {
		t.Errorf("layer with a port remove marker should validate: %v", err)
	}
	// Removal ignores host/interface; an entry setting them implies a narrower
	// removal than will happen — refused at save.
	narrow := Config{Ports: []Port{{Container: 5432, Host: 15432, Remove: true}}}
	if err := narrow.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "remove takes only a container port") {
		t.Errorf("port remove with host set should fail layer validation, got %v", err)
	}
	if err := ok.Validate(); err == nil || !strings.Contains(err.Error(), "remove is only meaningful in a cascade layer") {
		t.Errorf("resolved config with a port remove marker should fail validation, got %v", err)
	}
}

func TestValidateLayerPortRemoveNoCollision(t *testing.T) {
	// A remove marker binds nothing: it must not count toward host-port
	// collision accounting in its own layer.
	c := Config{Ports: []Port{
		{Container: 5432, Remove: true},
		{Container: 5432},
	}}
	if err := c.ValidateLayer(); err != nil {
		t.Errorf("remove marker + real binding of same port should validate: %v", err)
	}
}

func TestMergeEgressUnionAndRemoval(t *testing.T) {
	got := Merge(Config{Egress: []string{"grafana.com", "internal:8443"}},
		Config{Egress: []string{"!internal:8443", "api.stripe.com"}})
	if want := []string{"grafana.com", "api.stripe.com"}; !reflect.DeepEqual(got.Egress, want) {
		t.Errorf("egress merge: got %v want %v", got.Egress, want)
	}
	// Unlike every other `!name` list, the closure is kept, not consumed: it
	// must go on to subtract the endpoint from the derived allowlist (where
	// skill egress unions in) after the cascade is done.
	if want := []string{"internal:8443"}; !reflect.DeepEqual(got.EgressClosed, want) {
		t.Errorf("egress closures: got %v want %v", got.EgressClosed, want)
	}
}

func TestMergeEgressClosureSemantics(t *testing.T) {
	t.Run("portless closure removes every port", func(t *testing.T) {
		got := Merge(Config{Egress: []string{"internal:8443", "internal:9000", "grafana.com"}},
			Config{Egress: []string{"!internal"}})
		if want := []string{"grafana.com"}; !reflect.DeepEqual(got.Egress, want) {
			t.Errorf("open: got %v want %v", got.Egress, want)
		}
		if want := []string{"internal"}; !reflect.DeepEqual(got.EgressClosed, want) {
			t.Errorf("closed: got %v want %v", got.EgressClosed, want)
		}
	})
	t.Run("ported closure matches the portless open spelling", func(t *testing.T) {
		// Open grammar reads portless as :443, and matching honors that.
		got := Merge(Config{Egress: []string{"statsig.anthropic.com"}},
			Config{Egress: []string{"!statsig.anthropic.com:443"}})
		if len(got.Egress) != 0 {
			t.Errorf("open: got %v want none", got.Egress)
		}
	})
	t.Run("later plain entry re-opens, deleting the closure whole", func(t *testing.T) {
		got := Merge(Config{Egress: []string{"!statsig.anthropic.com"}},
			Config{Egress: []string{"statsig.anthropic.com:443"}})
		if want := []string{"statsig.anthropic.com:443"}; !reflect.DeepEqual(got.Egress, want) {
			t.Errorf("open: got %v want %v", got.Egress, want)
		}
		// No partial narrowing: the portless closure does not survive as
		// "every port except 443".
		if len(got.EgressClosed) != 0 {
			t.Errorf("closed: got %v want none", got.EgressClosed)
		}
	})
	t.Run("within one layer the closure wins", func(t *testing.T) {
		got := Merge(Config{}, Config{Egress: []string{"x.example.com", "!x.example.com"}})
		if len(got.Egress) != 0 {
			t.Errorf("open: got %v want none", got.Egress)
		}
	})
	t.Run("closures survive re-merging and dedup by identity", func(t *testing.T) {
		// The resolved cascade is Merge(Merge(def, tmpl), proj): a closure
		// from the first step must ride EgressClosed through the second, and
		// the same host closed portless and at :443 are distinct closures.
		step1 := Merge(Config{Egress: []string{"!statsig.anthropic.com"}},
			Config{Egress: []string{"!statsig.anthropic.com:443"}})
		got := Merge(step1, Config{Egress: []string{"!statsig.anthropic.com"}})
		if want := []string{"statsig.anthropic.com", "statsig.anthropic.com:443"}; !reflect.DeepEqual(got.EgressClosed, want) {
			t.Errorf("closed: got %v want %v", got.EgressClosed, want)
		}
	})
}

func TestEgressClosureMatches(t *testing.T) {
	cases := []struct {
		closure, entry string
		want           bool
	}{
		{"statsig.anthropic.com", "statsig.anthropic.com:443", true},
		{"statsig.anthropic.com", "statsig.anthropic.com:8443", true}, // portless = every port
		{"statsig.anthropic.com", "statsig.anthropic.com", true},
		{"statsig.anthropic.com:443", "statsig.anthropic.com", true}, // open portless reads :443
		{"statsig.anthropic.com:8443", "statsig.anthropic.com", false},
		{"statsig.anthropic.com:443", "statsig.anthropic.com:8443", false},
		{"statsig.anthropic.com", "api.anthropic.com:443", false},
		{"bad host", "bad host", true}, // unparseable: raw equality, never greedy
		{"bad host", "other.example.com", false},
	}
	for _, c := range cases {
		if got := EgressClosureMatches(c.closure, c.entry); got != c.want {
			t.Errorf("EgressClosureMatches(%q, %q) = %v, want %v", c.closure, c.entry, got, c.want)
		}
	}
}

func TestValidateEgressEntries(t *testing.T) {
	if err := (Config{Egress: []string{"grafana.com", "internal:8443"}}).ValidateLayer(); err != nil {
		t.Errorf("valid egress should pass: %v", err)
	}
	if err := (Config{Egress: []string{"internal:99999"}}).ValidateLayer(); err == nil || !strings.Contains(err.Error(), "port out of range") {
		t.Errorf("out-of-range egress port should fail, got %v", err)
	}
	if err := (Config{Egress: []string{"bad host"}}).ValidateLayer(); err == nil || !strings.Contains(err.Error(), "not a valid host[:port]") {
		t.Errorf("egress host with a space should fail, got %v", err)
	}
	// Markers: legal in a layer, a bug in a resolved config.
	marker := Config{Egress: []string{"!grafana.com"}}
	if err := marker.ValidateLayer(); err != nil {
		t.Errorf("egress removal marker should pass layer validation: %v", err)
	}
	if err := marker.Validate(); err == nil || !strings.Contains(err.Error(), `"!grafana.com"`) {
		t.Errorf("egress marker surviving to a resolved config should fail, got %v", err)
	}
	// A closure's stripped name is NOT exempt from the entry grammar (unlike
	// package markers): it survives the cascade and travels to the netns
	// helper's env, so it is held to the same injection-safe parser.
	if err := (Config{Egress: []string{"!bad host"}}).ValidateLayer(); err == nil || !strings.Contains(err.Error(), "not a valid host[:port]") {
		t.Errorf("closure marker with a malformed name should fail layer validation, got %v", err)
	}
	if err := (Config{EgressClosed: []string{"bad host"}}).Validate(); err == nil || !strings.Contains(err.Error(), "not a valid host[:port]") {
		t.Errorf("malformed EgressClosed entry should fail resolved validation, got %v", err)
	}
}

func TestParseEgress(t *testing.T) {
	if h, p, err := ParseEgress("deb.debian.org:80"); err != nil || h != "deb.debian.org" || p != 80 {
		t.Fatalf("ParseEgress explicit = (%q,%d,%v)", h, p, err)
	}
	if h, p, err := ParseEgress("grafana.com"); err != nil || h != "grafana.com" || p != 443 {
		t.Fatalf("ParseEgress default = (%q,%d,%v)", h, p, err)
	}
	for bad, wantErr := range map[string]string{
		"":           "empty egress entry",
		"host:0":     "port out of range",
		"host:99999": "port out of range",
		"::1":        "write it bracketed",
		"a b":        "not a valid host[:port]",
	} {
		if _, _, err := ParseEgress(bad); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("ParseEgress(%q) should fail with %q, got %v", bad, wantErr, err)
		}
	}
}

// DNS is case-insensitive, so a case-variant spelling is the SAME endpoint:
// it must parse to one lowercase identity, dedup with its lowercase twin,
// and never slip past a closure written in the other case.
func TestEgressHostCaseInsensitive(t *testing.T) {
	if h, p, err := ParseEgress("API.Example.COM:8443"); err != nil || h != "api.example.com" || p != 8443 {
		t.Fatalf("ParseEgress mixed-case = (%q,%d,%v), want lowercased host", h, p, err)
	}
	if !EgressClosureMatches("api.example.com", "API.EXAMPLE.COM") {
		t.Error("lowercase closure must close an uppercase-spelled entry")
	}
	if !EgressClosureMatches("API.EXAMPLE.COM:443", "api.example.com") {
		t.Error("uppercase closure must close a lowercase-spelled entry")
	}
	if egressKey("API.EXAMPLE.COM") != egressKey("api.example.com") {
		t.Error("case-variant spellings must dedup as one open entry")
	}
	if closureKey("API.EXAMPLE.COM") != closureKey("api.example.com") {
		t.Error("case-variant spellings must dedup as one closure")
	}
}

// shared_auth (and the vestigial shared_auth_declined) is stripped from
// EVERY resolved config, whatever layer carried it — picker-owned state must
// not ride the cascade (ADR 0025).
func TestSharedAuthKeysNeverResolve(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()
	writeProjectCfg(t, proj, "agent = \"claude\"\nshared_auth = [\"claude\"]\nshared_auth_declined = [\"claude\"]\n")
	cfg, err := Load(proj)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SharedAuth.Empty() {
		t.Fatalf("project-layer shared_auth must be stripped from the resolved config, got %+v", cfg.SharedAuth)
	}
	if len(cfg.SharedAuthDeclined) != 0 {
		t.Fatalf("project-layer shared_auth_declined must be stripped from the resolved config, got %v", cfg.SharedAuthDeclined)
	}
}

// env_from_host (ADR 0026): byre's core layer (git identity + TERM/TZ) is a
// real config layer — on by default, visible in the resolved config,
// disable-able and overridable per key by any higher layer; sources are a
// closed scheme set (git:/env:/tz:).
func TestEnvFromHostCoreLayerAndValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()
	writeProjectCfg(t, proj, "agent = \"none\"\n")
	cfg, err := Load(proj)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EnvFromHost["GIT_AUTHOR_NAME"] != "git:user.name" || cfg.EnvFromHost["GIT_COMMITTER_EMAIL"] != "git:user.email" {
		t.Fatalf("shipped core defaults must resolve on: %v", cfg.EnvFromHost)
	}
	if cfg.EnvFromHost["TERM"] != "env:TERM" || cfg.EnvFromHost["TZ"] != "tz:" {
		t.Fatalf("shipped TERM/TZ passthrough must resolve on: %v", cfg.EnvFromHost)
	}

	// A higher layer disables one key and redirects another.
	proj2 := t.TempDir()
	writeProjectCfg(t, proj2, "agent = \"none\"\n[env_from_host]\nGIT_AUTHOR_NAME = \"\"\nGIT_COMMITTER_NAME = \"git:custom.name\"\n")
	cfg2, err := Load(proj2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.EnvFromHost["GIT_AUTHOR_NAME"] != "" {
		t.Fatalf("a layer's \"\" must disable the key: %v", cfg2.EnvFromHost)
	}
	if cfg2.EnvFromHost["GIT_COMMITTER_NAME"] != "git:custom.name" {
		t.Fatalf("a layer must override a core source: %v", cfg2.EnvFromHost)
	}

	// The scheme set is closed: env:/tz: are legal, anything else is a loud
	// error naming the schemes; a literal value is pointed at [env].
	ok := Config{EnvFromHost: map[string]string{
		"GEMINI_API_KEY": "env:GEMINI_API_KEY",
		"BOX_NAME":       "env:HOST_NAME",
		"TZ":             "tz:",
	}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("env:/tz: sources must validate: %v", err)
	}
	bad := Config{EnvFromHost: map[string]string{"FOO": "literal-value"}}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "[env]") {
		t.Fatalf("unknown source must be rejected pointing at [env], got %v", err)
	}
	badEnv := Config{EnvFromHost: map[string]string{"FOO": "env:not a var"}}
	if err := badEnv.Validate(); err == nil || !strings.Contains(err.Error(), "not a valid env var name") {
		t.Fatalf("env: with an invalid var name must be rejected, got %v", err)
	}
	badTz := Config{EnvFromHost: map[string]string{"TZ": "tz:Europe/London"}}
	if err := badTz.Validate(); err == nil || !strings.Contains(err.Error(), "no argument") {
		t.Fatalf("tz: with an argument must be rejected, got %v", err)
	}
	badKey := Config{EnvFromHost: map[string]string{"BAD KEY": "git:user.name"}}
	if err := badKey.Validate(); err == nil || !strings.Contains(err.Error(), "not a valid environment variable name") {
		t.Fatalf("invalid env key must be rejected, got %v", err)
	}
}

// Bracketed IPv6 egress entries (RFC 3986 form, parsed to RFC 5952 canonical
// text): the grammar the rest of the planet uses, adopted so IPv6 endpoints
// stop being unsayable (grilling ruling 2026-07-15). The host round-trips
// WITH brackets so every "%s:%d" composition re-parses.
func TestParseEgressBracketedIPv6(t *testing.T) {
	h, p, err := ParseEgress("[2001:DB8::1]:8443")
	if err != nil || h != "[2001:db8::1]" || p != 8443 {
		t.Fatalf("got %s:%d %v", h, p, err)
	}
	// Portless defaults to 443 for OPEN entries; a portless CLOSURE still
	// means every port (ClosurePortless reads the distinction).
	h, p, err = ParseEgress("[::1]")
	if err != nil || h != "[::1]" || p != 443 {
		t.Fatalf("portless: %s:%d %v", h, p, err)
	}
	if !ClosurePortless("[::1]") || ClosurePortless("[::1]:443") {
		t.Fatal("portless distinction must survive brackets")
	}
	// A portless closure reaches every port of the canonical host.
	if !EgressClosureMatches("[2001:DB8::1]", "[2001:db8::1]:8443") {
		t.Fatal("portless v6 closure must match any port, canonicalized")
	}

	for entry, want := range map[string]string{
		"[2001:db8::1":     "unterminated",
		"[not-an-ip]:443":  "must hold an IPv6 literal",
		"[192.0.2.7]:443":  "must hold an IPv6 literal",
		"[::1]junk":        "not a valid [addr]:port",
		"[::1]:notaport":   "not a valid [addr]:port",
		"2001:db8::1":      "write it bracketed",
		"2001:db8::1:8443": "write it bracketed",
	} {
		if _, _, err := ParseEgress(entry); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want contains %q", entry, err, want)
		}
	}
}

// AtomicWrite must never create the parent directory: for a project store,
// dir + path record are created together (Bootstrap), and a write re-creating
// the dir would resurrect a store a concurrent forget deleted WITHOUT its
// record — a half-enrollment the id-collision check can't see.
func TestAtomicWriteRequiresParentDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	err := AtomicWrite(filepath.Join(missing, "byre.config"), "x = 1\n")
	if err == nil || !strings.Contains(err.Error(), "parent directory is missing") {
		t.Fatalf("err = %v, want parent-directory-missing error", err)
	}
	if _, serr := os.Stat(missing); !os.IsNotExist(serr) {
		t.Fatalf("AtomicWrite created the missing parent (stat err = %v)", serr)
	}
}
