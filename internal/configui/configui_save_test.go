package configui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pjlsergeant/byre/internal/config"
)

func TestSaveRoundTripsAndPreservesRawFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store", "byre.config")
	// Callers own the parent dir (in the product, Bootstrap creates it with
	// the path record; AtomicWrite deliberately never does).
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	in := config.Config{
		Base:    "golang:1.22-bookworm",
		Agent:   "claude",
		Apt:     []string{"jq"},
		Mounts:  []config.Mount{{Host: "~/d", Target: "/d", Mode: "rw"}},
		RunArgs: []string{"--privileged"}, // raw field, must round-trip untouched
	}
	if err := Save(path, in); err != nil {
		t.Fatal(err)
	}
	back, err := config.ParseFile(path)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if back.Base != in.Base || back.Agent != in.Agent {
		t.Errorf("scalars not preserved: %+v", back)
	}
	if !reflect.DeepEqual(back.RunArgs, in.RunArgs) {
		t.Errorf("raw run_args not preserved: %v", back.RunArgs)
	}
	if len(back.Mounts) != 1 || back.Mounts[0].Target != "/d" {
		t.Errorf("mounts not preserved: %v", back.Mounts)
	}
	// omitempty keeps unset fields out of the file (no noise)
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "npm_global") || strings.Contains(string(b), "files") {
		t.Errorf("unset fields should be omitted:\n%s", b)
	}
	if !strings.Contains(string(b), "Managed by `byre config`") {
		t.Errorf("missing managed-by header:\n%s", b)
	}
}

// A layer using the `!name` removal feature must be saveable: the store config
// is one cascade layer, so Save validates it with ValidateLayer, not the
// resolved Validate (which rightly rejects a removal marker as a malformed
// entry). Regression for the bug where any such config was permanently
// unsaveable from the editor.
func TestSaveAcceptsRemovalEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store", "byre.config")
	// Callers own the parent dir (in the product, Bootstrap creates it with
	// the path record; AtomicWrite deliberately never does).
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Skills:  []string{"!devloop"},                          // remove an inherited skill
		Volumes: []config.Volume{{Name: "!creds"}},             // remove an inherited volume
		Mounts:  []config.Mount{{Target: "!/inherited/mount"}}, // remove an inherited mount
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save rejected a valid removal-entry layer: %v", err)
	}
	back, err := config.ParseFile(path)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(back.Skills) != 1 || back.Skills[0] != "!devloop" {
		t.Errorf("removal marker not round-tripped: %v", back.Skills)
	}
}

// Saves preserve hand-written comments and formatting (ADR 0044): the
// reconcile writer edits only what changed, so everything else -- comments
// included -- survives byte-identically. The old destroys-comments warning
// apparatus is gone because there is nothing left to warn about.
func TestSavePreservesHandComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.config")
	orig := "# remember: the LAN port is for the demo\nagent = \"claude\" # chosen for this customer\n\n# glued to env\n[env]\nFOO = \"bar\"\n"
	mustWriteFile(t, path, []byte(orig), 0o644)

	cfg, err := config.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Base = "node:22" // one edit
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# remember: the LAN port is for the demo",
		"agent = \"claude\" # chosen for this customer",
		"# glued to env",
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("comment lost on save: %q missing from:\n%s", want, raw)
		}
	}
	if !strings.Contains(string(raw), "base = \"node:22\"") {
		t.Errorf("edit did not land:\n%s", raw)
	}
	back, err := config.ParseFile(path)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if back.Base != "node:22" || back.Agent != "claude" || back.Env["FOO"] != "bar" {
		t.Errorf("semantic drift after preserving save: %+v", back)
	}
}

// The prepare hook (deferred store setup, e.g. enrolling a project dir) must
// run before the first write lands — and only then: its whole point is that
// opening the editor and quitting creates nothing.
func TestPrepareRunsBeforeSaveWrites(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store")
	path := filepath.Join(store, "byre.config")
	m := newModel("t", path, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	calls := 0
	m.prepare = func() error {
		calls++
		return os.MkdirAll(store, 0o755) // what commands.Config's Bootstrap does
	}
	m = m.save()
	if calls != 1 {
		t.Fatalf("prepare ran %d times, want 1", calls)
	}
	if !m.savedOnce {
		t.Fatalf("save failed: %q", m.errMsg)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved file missing: %v", err)
	}
}

func TestPrepareErrorBlocksSaveAndEditor(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store")
	path := filepath.Join(store, "byre.config")
	m := newModel("t", path, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.prepare = func() error { return fmt.Errorf("cannot enroll") }

	m = m.save()
	if m.savedOnce {
		t.Fatal("a failed prepare must block the save")
	}
	if !strings.Contains(m.errMsg, "cannot enroll") {
		t.Fatalf("prepare error not surfaced: %q", m.errMsg)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("failed save left state behind: %v", err)
	}

	// ctrl+e hands the file to $EDITOR, which writes it directly — the same
	// gate applies before the editor may open.
	mm, cmd := m.updateForm(tea.KeyMsg{Type: tea.KeyCtrlE})
	if cmd != nil {
		t.Fatal("ctrl+e must not open $EDITOR when prepare fails")
	}
	if got := mm.(model).errMsg; !strings.Contains(got, "cannot enroll") {
		t.Fatalf("ctrl+e prepare error not surfaced: %q", got)
	}
}

// A save the validator refuses never becomes a write, so it must not run
// prepare (enrollment): cross-item collisions are deliberately deferred to
// save-time ValidateLayer, making this an ordinary-use path.
func TestSaveValidationFailureSkipsPrepare(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store")
	path := filepath.Join(store, "byre.config")
	cfg := config.Config{Mounts: []config.Mount{
		{Host: "/a", Target: "/x", Mode: "ro"},
		{Host: "/b", Target: "/x", Mode: "ro"},
	}}
	m := newModel("t", path, cfg, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	calls := 0
	m.prepare = func() error { calls++; return nil }
	m = m.save()
	if m.savedOnce {
		t.Fatal("an invalid layer must not save")
	}
	if calls != 0 {
		t.Fatalf("a refused save ran prepare %d times (enrolls on a no-op)", calls)
	}
	if !strings.Contains(m.errMsg, "collides") {
		t.Fatalf("validation error not surfaced: %q", m.errMsg)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("refused save left state behind: %v", err)
	}
}

// savedOnce must track writes that actually landed in the $EDITOR round-trip:
// created or changed → saved; look-and-quit → not.
func TestEditorRoundTripMarksSavedOnlyOnWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "byre.config")
	m := newModel("t", path, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)

	// Look-and-quit on a not-yet-existing file: nothing written.
	m.preEditorRaw, m.preEditorErr = os.ReadFile(path)
	if got := m.onEditorClosed(nil); got.savedOnce {
		t.Fatal("no write must not mark savedOnce")
	}
	// $EDITOR created the file: that IS the first write.
	if err := os.WriteFile(path, []byte("agent = \"none\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := m.onEditorClosed(nil); !got.savedOnce {
		t.Fatal("a landed $EDITOR write must mark savedOnce")
	}
	// Re-open on the now-existing file, quit without changing it: not a write.
	m.preEditorRaw, m.preEditorErr = os.ReadFile(path)
	if got := m.onEditorClosed(nil); got.savedOnce {
		t.Fatal("an unchanged file must not mark savedOnce")
	}
	// Deleted inside the editor: a mutation — "config unchanged" would claim
	// the config is intact when it is gone.
	m.preEditorRaw, m.preEditorErr = os.ReadFile(path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got := m.onEditorClosed(nil)
	if !got.savedOnce {
		t.Fatal("a deletion in the editor must mark savedOnce")
	}
	if !strings.Contains(got.status, "deleted") {
		t.Fatalf("deletion must be named in the status, got %q", got.status)
	}
}

// A structured save of a config carrying a shared-auth preference must
// produce a file byre can load again. The old whole-file encoder reflected
// the struct into [shared_auth.Pick] -- a shape the dual-shape decoder
// refuses, bricking default.config on a normal global save (external review
// find, 2026-07-25; reproduced) -- so reconcile's canonical emission is
// pinned for every reachable stored state; mixed canonicalizes to
// picks-only (the EncodeTOMLValue rule: yes-without-pick re-asks).
func TestSaveRoundTripsSharedAuth(t *testing.T) {
	cases := []struct {
		name string
		pref config.SharedAuthPref
		want config.SharedAuthPref
	}{
		{"pick",
			config.SharedAuthPref{Pick: map[string]string{"claude": "claude-shared-auth"}},
			config.SharedAuthPref{Pick: map[string]string{"claude": "claude-shared-auth"}}},
		{"legacy-yes",
			config.SharedAuthPref{Yes: []string{"claude"}},
			config.SharedAuthPref{Yes: []string{"claude"}}},
		{"mixed-canonicalizes-to-picks",
			config.SharedAuthPref{Yes: []string{"grok"}, Pick: map[string]string{"claude": "c"}},
			config.SharedAuthPref{Pick: map[string]string{"claude": "c"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "default.config")
			if err := Save(path, config.Config{Base: "node:22", SharedAuth: tc.pref}); err != nil {
				t.Fatal(err)
			}
			back, err := config.ParseFile(path)
			if err != nil {
				raw, _ := os.ReadFile(path)
				t.Fatalf("re-parse of saved config failed (the brick):\n%v\n%s", err, raw)
			}
			if !reflect.DeepEqual(back.SharedAuth.Pick, tc.want.Pick) || !reflect.DeepEqual(back.SharedAuth.Yes, tc.want.Yes) {
				t.Fatalf("round-trip: got %+v want %+v", back.SharedAuth, tc.want)
			}
		})
	}

	// An empty preference stays omitted -- no shared_auth key materializes.
	path := filepath.Join(t.TempDir(), "byre.config")
	if err := Save(path, config.Config{Base: "node:22"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "shared_auth") {
		t.Fatalf("empty preference must stay omitted:\n%s", raw)
	}
}

// TestReconcileCoversEveryField is the Merge guard's sibling: every
// toml-visible Config field must be written by reconcile, or a UI edit to
// that field silently never reaches the file. For each field, a sample value
// is saved onto an existing (commented) file and must round-trip; the
// reflection walk fails the test when a Config field gains a toml tag
// without a sample here.
func TestReconcileCoversEveryField(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	samples := map[string]config.Config{
		"engine":          {Engine: "podman"},
		"template":        {Template: "go"},
		"agent":           {Agent: "claude"},
		"base":            {Base: "node:22"},
		"extends":         {Extends: "torn"},
		"seed_prefs":      {SeedPrefs: boolPtr(true)},
		"worktree_base":   {WorktreeBase: "sibling"},
		"shared_auth":     {SharedAuth: config.SharedAuthPref{Pick: map[string]string{"claude": "claude-shared-auth"}}},
		"sources":         {Sources: map[string]config.SourceHint{"acme/tool": {URI: "https://x", Digest: ""}}},
		"apt":             {Apt: []string{"jq"}},
		"npm_global":      {NpmGlobal: []string{"prettier"}},
		"env":             {Env: map[string]string{"FOO": "bar"}},
		"env_from_host":   {EnvFromHost: map[string]string{"TERM": "env:TERM"}},
		"files":           {Files: map[string]string{"./seed": "/opt/seed"}},
		"skills":          {Skills: []string{"firewall"}},
		"mounts":          {Mounts: []config.Mount{{Host: "~/d", Target: "/d", Mode: "rw"}}},
		"volumes":         {Volumes: []config.Volume{{Name: "creds", Role: "state", Target: "/c", Seed: &config.Seed{Host: "~/s"}}}},
		"ports":           {Ports: []config.Port{{Container: 3000, Host: 3001, Interface: "0.0.0.0"}}},
		"egress":          {Egress: []string{"api.example.com:443"}},
		"egress_offered":  {EgressOffered: []string{"telemetry.example.com"}},
		"mcp":             {MCPs: []config.MCP{{Name: "github", Command: []string{"gh-mcp", "stdio"}, Env: []string{"GITHUB_TOKEN"}}}},
		"claude_skills":   {ClaudeSkills: []config.ClaudeSkill{{Name: "tdd-loop", Path: "~/cs/tdd-loop"}}},
		"context":         {Contexts: []config.ContextDecl{{Name: "rules", Text: "Line one.\nLine two.\n"}}},
		"dockerfile_pre":  {DockerfilePre: []string{"RUN echo pre"}},
		"dockerfile_post": {DockerfilePost: []string{"RUN echo post"}},
		"run_args":        {RunArgs: []string{"--cpus=2"}},
	}

	// Reflection guard: every toml-tagged Config field needs a sample.
	rt := reflect.TypeOf(config.Config{})
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("toml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		if _, ok := samples[tag]; !ok {
			t.Errorf("Config.%s (toml %q) has no reconcile sample — give it one (and handle it in reconcile)", rt.Field(i).Name, tag)
		}
	}

	for tag, cfg := range samples {
		t.Run(tag, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "byre.config")
			orig := "# hand comment survives\nnpm_global = [\"left-alone\"]\n"
			if tag == "npm_global" {
				orig = "# hand comment survives\napt = [\"left-alone\"]\n"
			}
			mustWriteFile(t, path, []byte(orig), 0o644)
			base, err := config.ParseFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// Overlay the sample onto the file's parsed content, then Save.
			merged := config.Merge(base, cfg)
			if err := Save(path, merged); err != nil {
				t.Fatalf("save: %v", err)
			}
			back, err := config.ParseFile(path)
			if err != nil {
				raw, _ := os.ReadFile(path)
				t.Fatalf("re-parse: %v\n%s", err, raw)
			}
			rv := reflect.ValueOf(cfg)
			bv := reflect.ValueOf(back)
			for i := 0; i < rt.NumField(); i++ {
				fTag := strings.Split(rt.Field(i).Tag.Get("toml"), ",")[0]
				if fTag != tag {
					continue
				}
				if !reflect.DeepEqual(rv.Field(i).Interface(), bv.Field(i).Interface()) {
					raw, _ := os.ReadFile(path)
					t.Fatalf("field %s did not round-trip:\n got %+v\nwant %+v\nfile:\n%s",
						rt.Field(i).Name, bv.Field(i).Interface(), rv.Field(i).Interface(), raw)
				}
			}
			raw, _ := os.ReadFile(path)
			if !strings.Contains(string(raw), "# hand comment survives") {
				t.Fatalf("hand comment lost:\n%s", raw)
			}
		})
	}
}

// Clearing a map spelled with dotted root keys must actually clear it
// (review finding 2026-07-25: the header-only removal path silently left
// `env.FOO = ...` in place).
func TestSaveClearsDottedSpelledMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "byre.config")
	mustWriteFile(t, path, []byte("env.FOO = \"bar\"\nenv.BAZ = \"qux\"\nbase = \"node:22\"\n"), 0o644)
	cfg, err := config.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Env = nil
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	back, err := config.ParseFile(path)
	if err != nil {
		raw, _ := os.ReadFile(path)
		t.Fatalf("re-parse: %v\n%s", err, raw)
	}
	if len(back.Env) != 0 {
		t.Fatalf("env not cleared: %+v", back.Env)
	}
	if back.Base != "node:22" {
		t.Fatalf("unrelated key lost: %+v", back)
	}
}

// Identical configs emit identical bytes on a fresh file — map-backed
// vocabularies write in sorted order (review round 4: raw map ranging made
// fresh-file layout nondeterministic).
func TestSaveIsDeterministicForMaps(t *testing.T) {
	cfg := config.Config{
		Base:    "node:22",
		Env:     map[string]string{"ZED": "1", "ALPHA": "2", "MID": "3"},
		Files:   map[string]string{"./b": "/opt/b", "./a": "/opt/a"},
		Sources: map[string]config.SourceHint{"z/tool": {URI: "https://z"}, "a/tool": {URI: "https://a"}},
	}
	render := func() string {
		path := filepath.Join(t.TempDir(), "byre.config")
		if err := Save(path, cfg); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	first := render()
	for i := 0; i < 8; i++ {
		if got := render(); got != first {
			t.Fatalf("nondeterministic save:\n--- first ---\n%s\n--- got ---\n%s", first, got)
		}
	}
	if !strings.Contains(first, "ALPHA") {
		t.Fatalf("sample missing env:\n%s", first)
	}
}

// Every [sources] spelling reconciles: house shape re-edited, the
// [sources."id"] subtable form (normalized on change), the root-inline
// form, and clearing each (grok review find, 2026-07-25 — subtable updates
// wrote files byre then refused to load; clears were silent no-ops).
func TestSaveSourcesSpellings(t *testing.T) {
	hint := func(uri string) map[string]config.SourceHint {
		return map[string]config.SourceHint{"acme/tool": {URI: uri}}
	}
	cases := []struct{ name, orig string }{
		{"house-inline", "[sources]\n\"acme/tool\" = { uri = \"https://old\" }\n"},
		{"subtable", "[sources.\"acme/tool\"]\nuri = \"https://old\"\n"},
		{"root-inline", "sources = { \"acme/tool\" = { uri = \"https://old\" } }\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "byre.config")
			// The comment is DETACHED (blank line): a glued comment belongs
			// to the construct and rightly goes when normalization rewrites
			// it; a detached one must survive every spelling's rewrite.
			mustWriteFile(t, path, []byte("# keep me\n\n"+tc.orig), 0o644)
			cfg, err := config.ParseFile(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Sources = hint("https://new")
			if err := Save(path, cfg); err != nil {
				t.Fatalf("update: %v", err)
			}
			back, err := config.ParseFile(path)
			if err != nil {
				raw, _ := os.ReadFile(path)
				t.Fatalf("re-parse after update: %v\n%s", err, raw)
			}
			if back.Sources["acme/tool"].URI != "https://new" {
				t.Fatalf("update lost: %+v", back.Sources)
			}
			raw, _ := os.ReadFile(path)
			if !strings.Contains(string(raw), "# keep me") {
				t.Fatalf("comment lost:\n%s", raw)
			}

			// Second update exercises the house shape the first write left.
			cfg2, _ := config.ParseFile(path)
			cfg2.Sources = hint("https://third")
			if err := Save(path, cfg2); err != nil {
				t.Fatalf("second update: %v", err)
			}

			// Clear.
			cfg3, _ := config.ParseFile(path)
			cfg3.Sources = nil
			if err := Save(path, cfg3); err != nil {
				t.Fatalf("clear: %v", err)
			}
			back, err = config.ParseFile(path)
			if err != nil {
				raw, _ := os.ReadFile(path)
				t.Fatalf("re-parse after clear: %v\n%s", err, raw)
			}
			if len(back.Sources) != 0 {
				raw, _ := os.ReadFile(path)
				t.Fatalf("clear was a no-op: %+v\n%s", back.Sources, raw)
			}
		})
	}
}

// The second save of a changed shared-auth pick — the first write leaves the
// house inline table; the re-edit must replace it, not error (grok find:
// the inline-table span bug made every second write fail).
func TestSaveSharedAuthSecondWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "default.config")
	pick := func(c string) config.Config {
		return config.Config{Base: "node:22", SharedAuth: config.SharedAuthPref{Pick: map[string]string{"claude": c}}}
	}
	if err := Save(path, pick("first")); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, pick("second")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	back, err := config.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.SharedAuth.CompanionPick("claude") != "second" {
		t.Fatalf("pick = %+v", back.SharedAuth)
	}
}

// Multiple [sources."id"] subtables reconcile safely (grok round 2: a
// per-id edit beside a still-open sibling subtable nested the insert under
// the sibling, silently dropping entries): update one, update all, delete
// one, add beside.
func TestSaveSourcesMultiSubtable(t *testing.T) {
	orig := "base = \"node:22\"\n\n[sources.\"a/tool\"]\nuri = \"https://a\"\n\n[sources.\"b/tool\"]\nuri = \"https://b\"\n"
	setup := func(t *testing.T) (string, config.Config) {
		path := filepath.Join(t.TempDir(), "byre.config")
		mustWriteFile(t, path, []byte(orig), 0o644)
		cfg, err := config.ParseFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return path, cfg
	}
	check := func(t *testing.T, path string, want map[string]string) {
		back, err := config.ParseFile(path)
		if err != nil {
			raw, _ := os.ReadFile(path)
			t.Fatalf("re-parse: %v\n%s", err, raw)
		}
		if len(back.Sources) != len(want) {
			raw, _ := os.ReadFile(path)
			t.Fatalf("sources = %+v, want %v\n%s", back.Sources, want, raw)
		}
		for id, uri := range want {
			if back.Sources[id].URI != uri {
				raw, _ := os.ReadFile(path)
				t.Fatalf("sources[%s] = %+v, want %s\n%s", id, back.Sources[id], uri, raw)
			}
		}
		if back.Base != "node:22" {
			t.Fatalf("unrelated key lost: %+v", back)
		}
	}

	t.Run("update-one", func(t *testing.T) {
		path, cfg := setup(t)
		cfg.Sources["a/tool"] = config.SourceHint{URI: "https://a2"}
		if err := Save(path, cfg); err != nil {
			t.Fatal(err)
		}
		check(t, path, map[string]string{"a/tool": "https://a2", "b/tool": "https://b"})
	})
	t.Run("delete-one", func(t *testing.T) {
		path, cfg := setup(t)
		delete(cfg.Sources, "a/tool")
		if err := Save(path, cfg); err != nil {
			t.Fatal(err)
		}
		check(t, path, map[string]string{"b/tool": "https://b"})
	})
	t.Run("add-beside", func(t *testing.T) {
		path, cfg := setup(t)
		cfg.Sources["c/tool"] = config.SourceHint{URI: "https://c"}
		if err := Save(path, cfg); err != nil {
			t.Fatal(err)
		}
		check(t, path, map[string]string{"a/tool": "https://a", "b/tool": "https://b", "c/tool": "https://c"})
	})
	t.Run("clear-all", func(t *testing.T) {
		path, cfg := setup(t)
		cfg.Sources = nil
		if err := Save(path, cfg); err != nil {
			t.Fatal(err)
		}
		check(t, path, nil)
	})
}

// reportSaved (Run's saved return) judges $EDITOR-only sessions by NET
// content: an edit round-trip that ends byte-identical to the open-time file
// must report "unchanged" — the QA playbook's ^e journey edits a bad line in
// and back out, and "wrote <path>" for that contradicted the on-disk truth
// (finding 2026-07-18, fixed 2026-07-22). A ctrl+s save reports written
// unconditionally; a lasting $EDITOR change reports written.
func TestReportSavedEditorNetNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "byre.config")
	orig := []byte("agent = \"none\"\n")
	if err := os.WriteFile(path, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModel("t", path, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)

	// $EDITOR writes a bad line, the UI reloads, a second $EDITOR fixes it
	// back: savedOnce is true (real writes landed) but the net content is the
	// open-time bytes — reportSaved must say unchanged.
	m.preEditorRaw, m.preEditorErr = os.ReadFile(path)
	if err := os.WriteFile(path, []byte("agent = \"none\"\npackages = [\"x\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = m.onEditorClosed(nil)
	m.preEditorRaw, m.preEditorErr = os.ReadFile(path)
	if err := os.WriteFile(path, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	m = m.onEditorClosed(nil)
	if !m.savedOnce {
		t.Fatal("precondition: the round-trip's writes must mark savedOnce")
	}
	if m.reportSaved() {
		t.Fatal("a net-identical $EDITOR round-trip must report unchanged")
	}

	// A LASTING $EDITOR change reports written.
	lasting := m
	lasting.preEditorRaw, lasting.preEditorErr = os.ReadFile(path)
	if err := os.WriteFile(path, []byte("agent = \"claude\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lasting = lasting.onEditorClosed(nil)
	if !lasting.reportSaved() {
		t.Fatal("a lasting $EDITOR change must report written")
	}

	// ctrl+s reports written unconditionally, net content notwithstanding.
	if err := os.WriteFile(path, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	saved := m
	saved.uiWrote = true
	if !saved.reportSaved() {
		t.Fatal("a ctrl+s save must always report written")
	}
}
