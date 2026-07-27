package configui

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// The Baked files screen exists to answer "what is going into my image and
// who put it there". files is overwhelmingly a SKILL's key -- every builtin
// agent skill ships its payload through it -- so a screen that showed only
// the user's own entries would miss most of the answer.
func TestFilesRowsAttributeEverySource(t *testing.T) {
	inh := Inherited{Skills: map[string]SkillRuntime{
		"firewall": {Files: map[string]string{"firewall.sh": "/usr/local/bin/byre-firewall"}},
	}}
	cfg := config.Config{
		Files:  map[string]string{"./mine": "/opt/mine"},
		Skills: []string{"firewall"},
	}
	m := newModel("t", "/tmp/x", cfg, nil, nil, []string{"firewall"}, nil, inh, nil, TargetProject)
	rows := m.fieldRows(fFiles)

	var local, skill int
	for _, r := range rows {
		switch r.kind {
		case rowLocal:
			local++
			if !strings.Contains(r.text, "./mine → /opt/mine") {
				t.Errorf("local row text = %q, want the source → destination copy", r.text)
			}
		case rowSkill:
			skill++
			if r.source != "skill:firewall" {
				t.Errorf("skill row source = %q, want skill:firewall", r.source)
			}
			if !strings.Contains(r.text, "/usr/local/bin/byre-firewall") {
				t.Errorf("skill row text = %q, want the payload destination", r.text)
			}
		}
	}
	if local != 1 || skill != 1 {
		t.Fatalf("rows = %+v; want one local and one skill row", rows)
	}
}

// The two shape rules belong to config, not the editor: commitItem calls the
// same validator ValidateLayer runs, so the editor cannot accept an entry a
// layer would later reject.
func TestFilesItemEditorRefusesShapesConfigRefuses(t *testing.T) {
	for _, tc := range []struct{ name, src, dest, want string }{
		{"absolute source", "/etc/passwd", "/opt/x", "project-relative"},
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
