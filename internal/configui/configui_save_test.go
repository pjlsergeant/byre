package configui

import (
	"errors"
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
	if err := Save(path, false, in, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	back, err := config.ParseFile(path, true)
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
	if strings.Contains(string(b), "dockerfile_pre") || strings.Contains(string(b), "files") {
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
	if err := Save(path, false, cfg, nil, nil, true); err != nil {
		t.Fatalf("Save rejected a valid removal-entry layer: %v", err)
	}
	back, err := config.ParseFile(path, true)
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

	cfg, err := config.ParseFile(path, true)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Base = "node:22" // one edit
	if err := Save(path, false, cfg, nil, nil, true); err != nil {
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
	back, err := config.ParseFile(path, true)
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
// refuses, bricking default.config on a normal global save (reproduced)
// -- so reconcile's canonical emission is
// pinned for every reachable stored state; yes-without-pick is parse-only
// since the 2026-08-23 ADR 0049 amendment, so a save persists the Saveable
// (picks-only) projection and yes-only entries are dropped, not written.
func TestSaveRoundTripsSharedAuth(t *testing.T) {
	cases := []struct {
		name string
		pref config.SharedAuthPref
		want config.SharedAuthPref
	}{
		{"pick",
			config.SharedAuthPref{Pick: map[string]string{"claude": "claude-shared-auth"}},
			config.SharedAuthPref{Pick: map[string]string{"claude": "claude-shared-auth"}}},
		{"legacy-yes-dropped",
			config.SharedAuthPref{Yes: []string{"claude"}},
			config.SharedAuthPref{}},
		{"mixed-canonicalizes-to-picks",
			config.SharedAuthPref{Yes: []string{"grok"}, Pick: map[string]string{"claude": "c"}},
			config.SharedAuthPref{Pick: map[string]string{"claude": "c"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "default.config")
			if err := Save(path, false, config.Config{Base: "node:22", Defaults: config.Defaults{SharedAuth: tc.pref}}, nil, nil, true); err != nil {
				t.Fatal(err)
			}
			back, err := config.ParseFile(path, true)
			if err != nil {
				raw, _ := os.ReadFile(path)
				t.Fatalf("re-parse of saved config failed (the brick):\n%v\n%s", err, raw)
			}
			if !reflect.DeepEqual(back.StoredSharedAuth().Pick, tc.want.Pick) || !reflect.DeepEqual(back.StoredSharedAuth().Yes, tc.want.Yes) {
				t.Fatalf("round-trip: got %+v want %+v", back.StoredSharedAuth(), tc.want)
			}
		})
	}

	// An empty preference stays omitted -- no shared_auth key materializes.
	path := filepath.Join(t.TempDir(), "byre.config")
	if err := Save(path, false, config.Config{Base: "node:22"}, nil, nil, true); err != nil {
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
		"defaults":        {Defaults: config.Defaults{SharedAuth: config.SharedAuthPref{Pick: map[string]string{"claude": "claude-shared-auth"}}}},
		"sources":         {Sources: map[string]config.SourceHint{"acme/tool": {URI: "https://x", Digest: ""}}},
		"apt":             {Apt: []string{"jq"}},
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

	// migrationOnly names fields a save deliberately does NOT round-trip: a
	// compat spelling whose whole job is to be read once and rewritten in
	// its new home. Round-tripping one would defeat the migration, so the
	// guard needs the exemption stated rather than the field silently
	// missing from the samples.
	migrationOnly := map[string]string{
		"shared_auth": "pre-2026-07-28 top-level spelling; a save migrates it into [defaults] (ADR 0025/0049)",
	}

	// Reflection guard: every toml-tagged Config field needs a sample.
	rt := reflect.TypeOf(config.Config{})
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("toml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		if _, ok := samples[tag]; !ok && migrationOnly[tag] == "" {
			t.Errorf("Config.%s (toml %q) has no reconcile sample — give it one (and handle it in reconcile)", rt.Field(i).Name, tag)
		}
	}

	for tag, cfg := range samples {
		t.Run(tag, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "byre.config")
			orig := "# hand comment survives\napt = [\"left-alone\"]\n"
			if tag == "apt" {
				orig = "# hand comment survives\nengine = \"podman\"\n"
			}
			mustWriteFile(t, path, []byte(orig), 0o644)
			base, err := config.ParseFile(path, true)
			if err != nil {
				t.Fatal(err)
			}
			// Overlay the sample onto the file's parsed content, then Save.
			merged := config.Merge(base, cfg)
			if err := Save(path, false, merged, nil, nil, true); err != nil {
				t.Fatalf("save: %v", err)
			}
			back, err := config.ParseFile(path, true)
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
// -- the header-only removal path silently left `env.FOO = ...` in place.
func TestSaveClearsDottedSpelledMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "byre.config")
	mustWriteFile(t, path, []byte("env.FOO = \"bar\"\nenv.BAZ = \"qux\"\nbase = \"node:22\"\n"), 0o644)
	cfg, err := config.ParseFile(path, true)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Env = nil
	if err := Save(path, false, cfg, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	back, err := config.ParseFile(path, true)
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
// vocabularies write in sorted order. Raw map ranging made fresh-file
// layout nondeterministic.
func TestSaveIsDeterministicForMaps(t *testing.T) {
	cfg := config.Config{
		Base:    "node:22",
		Env:     map[string]string{"ZED": "1", "ALPHA": "2", "MID": "3"},
		Files:   map[string]string{"./b": "/opt/b", "./a": "/opt/a"},
		Sources: map[string]config.SourceHint{"z/tool": {URI: "https://z"}, "a/tool": {URI: "https://a"}},
	}
	render := func() string {
		path := filepath.Join(t.TempDir(), "byre.config")
		if err := Save(path, false, cfg, nil, nil, true); err != nil {
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
// form, and clearing each. Subtable updates wrote files byre then refused
// to load; clears were silent no-ops.
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
			cfg, err := config.ParseFile(path, true)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Sources = hint("https://new")
			if err := Save(path, false, cfg, nil, nil, true); err != nil {
				t.Fatalf("update: %v", err)
			}
			back, err := config.ParseFile(path, true)
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
			cfg2, _ := config.ParseFile(path, true)
			cfg2.Sources = hint("https://third")
			if err := Save(path, false, cfg2, nil, nil, true); err != nil {
				t.Fatalf("second update: %v", err)
			}

			// Clear.
			cfg3, _ := config.ParseFile(path, true)
			cfg3.Sources = nil
			if err := Save(path, false, cfg3, nil, nil, true); err != nil {
				t.Fatalf("clear: %v", err)
			}
			back, err = config.ParseFile(path, true)
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
		return config.Config{Base: "node:22", Defaults: config.Defaults{SharedAuth: config.SharedAuthPref{Pick: map[string]string{"claude": c}}}}
	}
	if err := Save(path, false, pick("first"), nil, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, false, pick("second"), nil, nil, true); err != nil {
		t.Fatalf("second write: %v", err)
	}
	back, err := config.ParseFile(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if back.StoredSharedAuth().CompanionPick("claude") != "second" {
		t.Fatalf("pick = %+v", back.StoredSharedAuth())
	}
}

// Multiple [sources."id"] subtables reconcile safely -- a per-id edit beside
// a still-open sibling subtable nested the insert under the sibling, silently
// dropping entries. Update one, update all, delete
// one, add beside.
func TestSaveSourcesMultiSubtable(t *testing.T) {
	orig := "base = \"node:22\"\n\n[sources.\"a/tool\"]\nuri = \"https://a\"\n\n[sources.\"b/tool\"]\nuri = \"https://b\"\n"
	setup := func(t *testing.T) (string, config.Config) {
		path := filepath.Join(t.TempDir(), "byre.config")
		mustWriteFile(t, path, []byte(orig), 0o644)
		cfg, err := config.ParseFile(path, true)
		if err != nil {
			t.Fatal(err)
		}
		return path, cfg
	}
	check := func(t *testing.T, path string, want map[string]string) {
		back, err := config.ParseFile(path, true)
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
		if err := Save(path, false, cfg, nil, nil, true); err != nil {
			t.Fatal(err)
		}
		check(t, path, map[string]string{"a/tool": "https://a2", "b/tool": "https://b"})
	})
	t.Run("delete-one", func(t *testing.T) {
		path, cfg := setup(t)
		delete(cfg.Sources, "a/tool")
		if err := Save(path, false, cfg, nil, nil, true); err != nil {
			t.Fatal(err)
		}
		check(t, path, map[string]string{"b/tool": "https://b"})
	})
	t.Run("add-beside", func(t *testing.T) {
		path, cfg := setup(t)
		cfg.Sources["c/tool"] = config.SourceHint{URI: "https://c"}
		if err := Save(path, false, cfg, nil, nil, true); err != nil {
			t.Fatal(err)
		}
		check(t, path, map[string]string{"a/tool": "https://a", "b/tool": "https://b", "c/tool": "https://c"})
	})
	t.Run("clear-all", func(t *testing.T) {
		path, cfg := setup(t)
		cfg.Sources = nil
		if err := Save(path, false, cfg, nil, nil, true); err != nil {
			t.Fatal(err)
		}
		check(t, path, nil)
	})
}

// reportSaved (Run's saved return) judges $EDITOR-only sessions by NET
// content: an edit round-trip that ends byte-identical to the open-time file
// must report "unchanged" — the QA playbook's ^e journey edits a bad line in
// and back out, and "wrote <path>" for that contradicted the on-disk truth.
// A ctrl+s save reports written
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

// A read failure that is NOT absence (permissions, I/O) must not masquerade
// as created/deleted: reportSaved degrades to the coarse truth (writes landed
// this session) instead of comparing against bytes it couldn't read, and
// onEditorClosed sets no mutation flag it can't prove.
func TestReportSavedUnreadableEdgesDegradeCoarse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "byre.config")
	if err := os.WriteFile(path, []byte("agent = \"none\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModel("t", path, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)

	// Open-time read failed for a non-absence reason; editor writes landed.
	// The net comparison is untrustworthy — report written, never "unchanged".
	m.openRaw, m.openErr = nil, errors.New("permission denied")
	m.savedOnce = true
	if !m.reportSaved() {
		t.Fatal("an unreadable open endpoint must degrade to reporting written")
	}

	// onEditorClosed: a non-absence pre-editor error plus a readable file is
	// NOT proof of creation — savedOnce must stay unset.
	clean := newModel("t", path, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	clean.preEditorRaw, clean.preEditorErr = nil, errors.New("permission denied")
	if got := clean.onEditorClosed(nil); got.savedOnce {
		t.Fatal("a non-absence read error must not count as a landed write")
	}
}

// An $EDITOR session that changes the file and leaves it UNREADABLE sets no
// mutation flag — but the quit report must not call that "unchanged": the
// readability transition itself is observable. The
// unreadable quit endpoint is simulated by replacing the file with a
// directory (EISDIR: a non-absence read failure that works under any uid).
func TestReportSavedUnreadableQuitEndpointReportsWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "byre.config")
	if err := os.WriteFile(path, []byte("agent = \"none\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModel("t", path, config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)

	// $EDITOR breaks the file: onEditorClosed can't prove a write landed…
	m.preEditorRaw, m.preEditorErr = os.ReadFile(path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	m = m.onEditorClosed(nil)
	if m.savedOnce {
		t.Fatal("precondition: an unreadable post-editor file must not set savedOnce")
	}
	// …but quit must still report written, never "config unchanged."
	if !m.reportSaved() {
		t.Fatal("readable→unreadable across the session must report written")
	}
}

// Concurrent worktree sessions share one project store, so two editors open
// on the same config is ordinary. The editor's desired config is built on
// what it READ at open, so a key another session added since is absent from
// it -- writing would reconcile that key away. Save refuses instead, and only
// force (the y answer to the overwrite prompt) writes through.
func TestSaveRefusesDriftAndForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "byre.config")
	if err := os.WriteFile(path, []byte("base = \"debian\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	openRaw, openErr := os.ReadFile(path)
	if openErr != nil {
		t.Fatal(openErr)
	}

	// Another session lands a grant-bearing change while this editor is open.
	if err := os.WriteFile(path, []byte("base = \"debian\"\negress = [\"api.example.com\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	desired := config.Config{Base: "ubuntu"} // what our editor would write
	if err := Save(path, false, desired, openRaw, openErr, false); !errors.Is(err, ErrDrift) {
		t.Fatalf("a moved file must refuse with ErrDrift, got %v", err)
	}
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), "api.example.com") {
		t.Errorf("the refused save must leave the other session's change intact:\n%s", after)
	}

	if err := Save(path, false, desired, openRaw, openErr, true); err != nil {
		t.Fatalf("force must write through: %v", err)
	}
	forced, _ := os.ReadFile(path)
	if !strings.Contains(string(forced), "ubuntu") {
		t.Errorf("force must write this session's config:\n%s", forced)
	}
	if strings.Contains(string(forced), "api.example.com") {
		t.Errorf("overwrite is wholesale by ruling -- the other session's key does not survive:\n%s", forced)
	}
}

// An unchanged file saves normally, and a file absent at open and still
// absent saves normally too (absence on both sides is not drift).
func TestSaveAllowsUnchangedAndConsistentAbsence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "byre.config")
	if err := os.WriteFile(path, []byte("base = \"debian\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	openRaw, _ := os.ReadFile(path)
	if err := Save(path, false, config.Config{Base: "ubuntu"}, openRaw, nil, false); err != nil {
		t.Fatalf("an unchanged file must save: %v", err)
	}

	fresh := filepath.Join(dir, "new.config")
	_, absErr := os.ReadFile(fresh)
	if err := Save(fresh, false, config.Config{Base: "ubuntu"}, nil, absErr, false); err != nil {
		t.Fatalf("absent at open and still absent must save: %v", err)
	}
}

// A layer may legally hold both a `remove = true` marker and a binding for
// the same container port (drop the inherited one, publish mine). The editor
// keyed port blocks on the container port alone, so the two blocks were one
// identity and a save destroyed the binding.
func TestSavePreservesPortRemoveMarkerBesideBinding(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "byre.config")
	initial := "# keep me: the marker's own note\n[[ports]]\ncontainer = 5432\nremove = true\n\n[[ports]]\ncontainer = 5432\nhost = 15432\n\n# and this one, on an unrelated port\n[[ports]]\ncontainer = 8080\nhost = 8080\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	cur, err := config.ParseFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cur.Ports) != 3 {
		t.Fatalf("fixture should parse three port blocks, got %+v", cur.Ports)
	}
	// An EDIT, not a zero-edit save: reconcileBlocks short-circuits on
	// DeepEqual, so only a changed set exercises the identity collision that
	// destroyed the marker.
	edited := config.Config{Ports: append([]config.Port{}, cur.Ports...)}
	for i := range edited.Ports {
		if !edited.Ports[i].Remove && edited.Ports[i].Container == 5432 {
			edited.Ports[i].Host = 15433
		}
	}
	if err := Save(path, false, edited, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	back, err := config.ParseFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Ports) != 3 {
		t.Fatalf("every port block must survive a save, got %+v", back.Ports)
	}
	var marker, binding bool
	for _, p := range back.Ports {
		if p.Container == 5432 && p.Remove {
			marker = true
		}
		if p.Container == 5432 && p.Host == 15433 {
			binding = true
		}
	}
	if !marker || !binding {
		t.Errorf("marker=%v binding=%v — a save must not collapse them: %+v", marker, binding, back.Ports)
	}
	// ADR 0044: bytes outside the edited construct survive. A rewrite of the
	// whole ports construct would satisfy the parse assertions above and
	// still take every port block's comments with it.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# keep me: the marker's own note", "# and this one, on an unrelated port"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("comment lost by the save: %q\n%s", want, raw)
		}
	}
}

// Onboarding records BOTH axes explicitly, sentinel included, because
// `agent = "none"` must beat a template's choice if one is added later. The
// editor must give that answer back: with nothing inherited the sentinel and
// absence mean the same thing TODAY, but deleting a key the user's config
// deliberately states is the round-trip destruction this work exists to end.
func TestScalarPickersPreserveAStoredSentinelWithNothingBelow(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{Template: config.NoneLabel, Agent: config.NoneLabel},
		[]string{"go"}, []string{"claude"}, nil, nil, Inherited{}, nil, TargetProject)
	if hasInheritRow(m.agentOpts) {
		t.Fatalf("nothing below means no inherit row: %v", m.agentOpts)
	}
	got := m.assemble()
	if got.Agent != config.NoneLabel || got.Template != config.NoneLabel {
		t.Errorf("a stored sentinel must survive a zero-edit save: agent=%q template=%q", got.Agent, got.Template)
	}
	// A config that never said it still writes absent -- no churn.
	m2 := newModel("t", "/tmp/x", config.Config{}, []string{"go"}, []string{"claude"}, nil, nil, Inherited{}, nil, TargetProject)
	if got := m2.assemble(); got.Agent != "" || got.Template != "" {
		t.Errorf("an unstated sentinel must stay unstated: agent=%q template=%q", got.Agent, got.Template)
	}
}

// Custody, not just semantics: dropping the marker from [marker, binding]
// must delete the MARKER's block and leave the binding's bytes -- and its
// comment -- untouched. Assigning occurrences by position alone rewrote
// occurrence 0 into the binding and deleted occurrence 1, which parses
// identically while swapping which comment survives (ADR 0044).
func TestSavePortRemovalKeepsTheSurvivingBlocksComment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "byre.config")
	initial := "# marker's note\n[[ports]]\ncontainer = 5432\nremove = true\n\n# binding's note\n[[ports]]\ncontainer = 5432\nhost = 15432\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	cur, err := config.ParseFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	// Drop the marker, keep the binding exactly as it was.
	var want config.Config
	for _, p := range cur.Ports {
		if !p.Remove {
			want.Ports = append(want.Ports, p)
		}
	}
	if err := Save(path, false, want, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "# binding's note") {
		t.Errorf("the surviving block must keep its own comment:\n%s", raw)
	}
	if strings.Contains(string(raw), "# marker's note") {
		t.Errorf("the removed block's comment must go with it:\n%s", raw)
	}
	back, err := config.ParseFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Ports) != 1 || back.Ports[0].Remove || back.Ports[0].Host != 15432 {
		t.Errorf("wrong ports after removal: %+v", back.Ports)
	}
}

// Custody one user action further: drop the marker AND edit the binding in
// one save. Neither block exact-matches, so slot assignment decides which
// comment lives -- and a changed entry must take a slot of its own CLASS,
// or the edited binding claims the marker's block and the original
// binding's comment is deleted with a block it never occupied.
func TestSavePortMarkerDropWithEditKeepsTheBindingsComment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "byre.config")
	initial := "# marker's note\n[[ports]]\ncontainer = 5432\nremove = true\n\n# binding's note\n[[ports]]\ncontainer = 5432\nhost = 15432\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	cur, err := config.ParseFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	var want config.Config
	for _, p := range cur.Ports {
		if !p.Remove {
			p.Host = 15433 // edited in the same save
			want.Ports = append(want.Ports, p)
		}
	}
	if err := Save(path, false, want, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "# binding's note") {
		t.Errorf("the surviving (edited) block must keep its own comment:\n%s", raw)
	}
	if strings.Contains(string(raw), "# marker's note") {
		t.Errorf("the removed marker's comment must go with it:\n%s", raw)
	}
	back, err := config.ParseFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Ports) != 1 || back.Ports[0].Host != 15433 {
		t.Errorf("wrong ports after drop+edit: %+v", back.Ports)
	}
}

// "Migrated on the next write" has to be true of ANY write, not only one
// that changes the preference: keying the migration on a changed value left
// an unrelated global edit with the old top-level spelling intact, and both
// homes coexisting indefinitely.
func TestSaveMigratesLegacySharedAuthOnAnUnrelatedEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "default.config")
	if err := os.WriteFile(path, []byte("shared_auth = { claude = \"claude-shared-auth\" }\nbase = \"debian\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cur, err := config.ParseFile(path, true)
	if err != nil {
		t.Fatal(err)
	}
	// Change something else entirely; the preference is untouched.
	want := cur
	want.Base = "ubuntu"
	if err := Save(path, true, want, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sec := strings.Index(string(raw), "[defaults]")
	if sec < 0 {
		t.Fatalf("an unrelated write must still migrate the preference:\n%s", raw)
	}
	if strings.Contains(string(raw)[:sec], "shared_auth") {
		t.Errorf("the legacy spelling must not survive beside its new home:\n%s", raw)
	}
	back, err := config.ParseFile(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if back.StoredSharedAuth().CompanionPick("claude") != "claude-shared-auth" {
		t.Errorf("the preference itself must survive the move: %+v", back.StoredSharedAuth())
	}
}

// The picker writes to the model; the FILE is the contract. commitItem set
// Sharing and every in-memory assertion passed while renderVolume dropped the
// key on the way to disk -- so an exclusive volume chosen in the editor was
// silently shared again on save, and editing any other field of a hand-written
// exclusive volume stripped the declaration out of the user's file.
func TestSaveWritesVolumeSharing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "byre.config")

	// Adding an exclusive volume writes the key.
	cfg := config.Config{Volumes: []config.Volume{
		{Name: "ledger", Role: "state", Target: "/var/lib/ledger", Sharing: "exclusive"},
	}}
	if err := Save(path, false, cfg, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `sharing = "exclusive"`) {
		t.Fatalf("the save dropped the single-writer declaration:\n%s", raw)
	}
	back, err := config.ParseFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Volumes[0].Exclusive() {
		t.Fatalf("round trip lost sharing: %+v", back.Volumes[0])
	}

	// Editing an unrelated field of a HAND-WRITTEN exclusive volume must not
	// strip it. reconcileBlocks short-circuits on DeepEqual, so only a real
	// edit exercises the rewrite that did the stripping.
	hand := filepath.Join(dir, "hand.config")
	initial := "# the ledger takes one writer\n[[volumes]]\nname = \"ledger\"\nrole = \"state\"\ntarget = \"/var/lib/ledger\"\nsharing = \"exclusive\"\n"
	if err := os.WriteFile(hand, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	cur, err := config.ParseFile(hand, false)
	if err != nil {
		t.Fatal(err)
	}
	edited := config.Config{Volumes: append([]config.Volume{}, cur.Volumes...)}
	edited.Volumes[0].Target = "/var/lib/ledger2"
	if err := Save(hand, false, edited, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	after, err := config.ParseFile(hand, false)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Volumes[0].Exclusive() || after.Volumes[0].Target != "/var/lib/ledger2" {
		t.Errorf("retyping a target must not un-declare single-writer: %+v", after.Volumes[0])
	}

	// The default answer writes no key: `sharing = "shared"` in every block
	// would be noise in a file people hand-edit.
	plain := filepath.Join(dir, "plain.config")
	if err := Save(plain, false, config.Config{Volumes: []config.Volume{
		{Name: "deps", Role: "cache", Target: "/workspace/node_modules"},
	}}, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	praw, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(praw), "sharing") {
		t.Errorf("the default answer must write no key:\n%s", praw)
	}
}

// The class the sharing bug belonged to: a [[block]] field the model carries
// and the renderer forgets is invisible everywhere except the file. Every
// toml-tagged field of every block vocabulary must reach its renderer, so the
// next added field cannot be silently unsaveable.
func TestBlockRenderersEmitEveryTaggedField(t *testing.T) {
	cases := []struct {
		vocab  string
		render func(reflect.Value) string
		zero   any
	}{
		{"mounts", func(v reflect.Value) string { return renderMount(v.Interface().(config.Mount)) }, config.Mount{}},
		{"volumes", func(v reflect.Value) string { return renderVolume(v.Interface().(config.Volume)) }, config.Volume{}},
		{"ports", func(v reflect.Value) string { return renderPort(v.Interface().(config.Port)) }, config.Port{}},
		{"mcp", func(v reflect.Value) string { return renderMCP(v.Interface().(config.MCP)) }, config.MCP{}},
		{"claude_skills", func(v reflect.Value) string { return renderClaudeSkill(v.Interface().(config.ClaudeSkill)) }, config.ClaudeSkill{}},
		{"context", func(v reflect.Value) string { return renderContext(v.Interface().(config.ContextDecl)) }, config.ContextDecl{}},
	}
	for _, tc := range cases {
		rt := reflect.TypeOf(tc.zero)
		full := reflect.New(rt).Elem()
		fillNonZero(t, full)
		out := tc.render(full)
		for i := 0; i < rt.NumField(); i++ {
			tag := strings.Split(rt.Field(i).Tag.Get("toml"), ",")[0]
			if tag == "" || tag == "-" {
				continue
			}
			if !strings.Contains(out, tag+" = ") {
				t.Errorf("[[%s]]: render%s drops %q — a field the editor can set and the file never gets is lost on save\n%s",
					tc.vocab, rt.Name(), tag, out)
			}
		}
	}
}

// fillNonZero gives every field a value a renderer's `if set` branch accepts.
func fillNonZero(t *testing.T, v reflect.Value) {
	t.Helper()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("x")
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int:
			f.SetInt(1)
		case reflect.Slice:
			f.Set(reflect.Append(f, reflect.ValueOf("x")))
		case reflect.Map:
			m := reflect.MakeMap(f.Type())
			m.SetMapIndex(reflect.ValueOf("k"), reflect.ValueOf("v"))
			f.Set(m)
		case reflect.Pointer:
			f.Set(reflect.New(f.Type().Elem()))
			fillNonZero(t, f.Elem())
		default:
			t.Fatalf("fillNonZero has no case for %s (%s) — extend it with the new field's kind", v.Type().Field(i).Name, f.Kind())
		}
	}
}
