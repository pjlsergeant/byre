package configui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// Build files and Skill files are two screens because they answer two
// questions. Build files is what THIS config stages for the build; a skill's
// payload must not appear there, or a list that is almost entirely package
// files reads as though the user wrote it.
func TestBuildFilesShowsOnlyThisConfigsStaging(t *testing.T) {
	m := splitFilesModel(t)
	for _, r := range m.fieldRows(fFiles) {
		if r.kind == rowSkill {
			t.Fatalf("a skill payload leaked onto Build files: %+v", r)
		}
	}
	rows := m.fieldRows(fFiles)
	if len(rows) != 1 || !strings.Contains(rows[0].text, "./mine → /opt/mine") {
		t.Fatalf("rows = %+v, want just this config's own entry", rows)
	}
}

// Skill files is the discovery screen: every row attributed, nothing else.
func TestSkillFilesShowsOnlySkillPayloadsWithAttribution(t *testing.T) {
	m := splitFilesModel(t)
	rows := m.fieldRows(fSkillFiles)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one skill row", rows)
	}
	r := rows[0]
	if r.kind != rowSkill {
		t.Errorf("kind = %v, want rowSkill", r.kind)
	}
	if r.source != "skill:firewall" {
		t.Errorf("source = %q, want skill:firewall -- the screen exists to say where it came from", r.source)
	}
	if !strings.Contains(r.text, "/usr/local/bin/byre-firewall") {
		t.Errorf("text = %q, want the payload destination", r.text)
	}
}

// Read-only means no add affordance and no route to one.
func TestSkillFilesIsReadOnly(t *testing.T) {
	if !isReadOnlyField(fSkillFiles) {
		t.Fatal("Skill files must be read-only")
	}
	if isReadOnlyField(fFiles) {
		t.Fatal("Build files must stay editable")
	}
	m := splitFilesModel(t)
	for _, r := range m.fieldRows(fSkillFiles) {
		if got := m.rowChoices(fSkillFiles, r); len(got) != 0 {
			t.Fatalf("a skill payload row offers actions: %+v", got)
		}
	}
}

func splitFilesModel(t *testing.T) model {
	t.Helper()
	inh := Inherited{Skills: map[string]SkillRuntime{
		"firewall": {Files: map[string]string{"firewall.sh": "/usr/local/bin/byre-firewall"}},
	}}
	cfg := config.Config{
		Files:  map[string]string{"./mine": "/opt/mine"},
		Skills: []string{"firewall"},
	}
	return newModel("t", "/tmp/x", cfg, nil, nil, []string{"firewall"}, nil, inh, nil, TargetProject)
}

// The two shape rules belong to config, not the editor: commitItem calls the
// same validator ValidateLayer runs, so the editor cannot accept an entry a
// layer would later reject.
func TestFilesItemEditorRefusesShapesConfigRefuses(t *testing.T) {
	for _, tc := range []struct{ name, src, dest, want string }{
		{"absolute source", "/etc/passwd", "/opt/x", "project-relative"},
		// The ..-prefix half of the escape refusal is path grammar, so the
		// editor refuses it at the point of typing; the symlink half still
		// needs the disk and stays at build (2026-07-27 QA: the editor
		// accepted ../../../etc/shadow and the build refused it later).
		{"escaping source", "../../../etc/shadow", "/opt/leak", "escapes the project dir"},
		{"dot-dot exactly", "..", "/opt/leak", "escapes the project dir"},
		{"relative destination", "./seed", "opt/seed", "absolute path in the image"},
		{"empty source", "", "/opt/seed", "required"},
		{"empty destination", "./seed", "", "required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := filesModel(t, nil)
			m.listField = fFiles
			m = m.startItem(-1)
			m.inputs[0].SetValue(tc.src)
			m.inputs[1].SetValue(tc.dest)
			got := m.commitItem()
			if got.itemErr == "" {
				t.Fatalf("accepted %q → %q; want a refusal", tc.src, tc.dest)
			}
			if !strings.Contains(got.itemErr, tc.want) {
				t.Errorf("error = %q, want it to mention %q", got.itemErr, tc.want)
			}
			if len(got.files) != 0 {
				t.Errorf("a refused entry was still written: %+v", got.files)
			}
		})
	}
}

// files is a MAP on disk, so two rows sharing a source collapse silently on
// save -- and planFiles refuses two spellings of one source outright, because
// which survives would be map-iteration order.
func TestFilesItemEditorRefusesADuplicateSource(t *testing.T) {
	m := filesModel(t, map[string]string{"./seed": "/opt/seed"})
	m.listField = fFiles
	m = m.startItem(-1)
	m.inputs[0].SetValue("seed") // same path, different spelling
	m.inputs[1].SetValue("/opt/other")
	if got := m.commitItem(); !strings.Contains(got.itemErr, "duplicate source") {
		t.Fatalf("itemErr = %q, want a duplicate-source refusal", got.itemErr)
	}
}

func TestFilesItemEditorAcceptsAValidEntry(t *testing.T) {
	m := filesModel(t, nil)
	m.listField = fFiles
	m = m.startItem(-1)
	m.inputs[0].SetValue("./seed")
	m.inputs[1].SetValue("/opt/seed")
	got := m.commitItem()
	if got.itemErr != "" {
		t.Fatalf("refused a valid entry: %s", got.itemErr)
	}
	if len(got.files) != 1 || got.files[0].Key != "./seed" || got.files[0].Value != "/opt/seed" {
		t.Fatalf("files = %+v, want the entry stored", got.files)
	}
}

// config now owns the shapes, so a bad [files] entry fails at layer
// validation rather than surviving to build time.
func TestValidateFilesIsReachedByLayerValidation(t *testing.T) {
	err := config.Config{Files: map[string]string{"/etc/passwd": "/opt/x"}}.ValidateLayer()
	if err == nil || !strings.Contains(err.Error(), "project-relative") {
		t.Fatalf("ValidateLayer err = %v, want it to refuse an absolute source", err)
	}
}

// filesModel is a real editor model (commitItem assembles the whole config at
// the end, so a bare struct literal is not enough).
func filesModel(t *testing.T, files map[string]string) model {
	t.Helper()
	return newModel("t", "/tmp/x", config.Config{Files: files}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
}

// "." is a legal source meaning the WHOLE directory, and packages that ship a
// tree use it. Rendered raw it produced rows like ". → /etc/byre/x", which
// tells a reader nothing about what is being copied.
func TestFilesRowsRenderAWholeDirectorySourceLegibly(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"acme/bundle": {Files: map[string]string{".": "/etc/byre/acme-bundle"}},
	}}
	cfg := config.Config{Skills: []string{"acme/bundle"}}
	m := newModel("t", "/tmp/x", cfg, nil, nil, []string{"acme/bundle"}, nil, inh, nil, TargetProject)

	var got string
	for _, r := range m.fieldRows(fSkillFiles) {
		if r.kind == rowSkill {
			got = r.text
		}
	}
	if strings.HasPrefix(got, ". ") {
		t.Fatalf("row = %q, want the bare dot spelled out", got)
	}
	if !strings.Contains(got, "whole skill directory") || !strings.Contains(got, "/etc/byre/acme-bundle") {
		t.Fatalf("row = %q, want it to name the whole skill directory and the destination", got)
	}
}

// The Build files editor warns on a source that is not on disk -- the same
// affordance the Claude Skills editor has, on the same terms: accepted
// anyway (the file can be created before the next develop), but never
// silently, because the deferred failure was a raw lstat error at build.
func TestFilesEditorNotesAMissingSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := filesSourceNote(dir, "not-yet.txt"); !strings.Contains(got, "build will fail") {
		t.Errorf("missing source must note the deferred failure, got %q", got)
	}
	if got := filesSourceNote(dir, "real.txt"); got != "" {
		t.Errorf("an existing source must stay silent, got %q", got)
	}
	// No project to ask: the global/layer editors resolve against no tree,
	// so a probe would answer about the wrong one. Silence, not a guess.
	if got := filesSourceNote("", "not-yet.txt"); got != "" {
		t.Errorf("no project dir must mean no note, got %q", got)
	}
	// Shapes other rules own stay theirs: the escape refusal and the
	// absolute-source refusal already name what fired.
	if got := filesSourceNote(dir, "../outside.txt"); got != "" {
		t.Errorf("an escaping source is the escape rule's, got %q", got)
	}
}
