package configui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/skills"
)

// A skill whose primary parses but whose FULL load fails (here: a mount target
// that is not absolute) is a problem row, not an absence. Before this it was
// dropped from every lister with a healthy catalog row behind it, so the
// editor showed nothing at all and the user had no reason to read.
func TestUnloadableSkillShowsDisabledWithReason(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "skills", "brokenmount")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "description = \"b\"\n[agent]\ncommand = \"x\"\n\n[[runtime.mounts]]\nhost = \"/tmp\"\ntarget = \"relative\"\n"
	if err := os.WriteFile(filepath.Join(dir, "skill.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := packages.LoadCatalog(home, nil, "v0.2.0", "0.2.0",
		packages.Stage2Hooks{Skill: skills.ValidatePrimaryBytes})
	if err != nil {
		t.Fatal(err)
	}
	// The listers the caller runs before opening the editor are what mark the
	// row (commands.Config calls both).
	skillOpts := skills.ListSkills(cat)
	agents := skills.ListAgentSkills(cat)
	if len(skillOpts) != 0 || len(agents) != 0 {
		t.Fatalf("an unloadable skill is still not offerable: %v / %v", skillOpts, agents)
	}

	m := newModel("t", filepath.Join(home, "byre.config"), config.Config{},
		nil, agents, skillOpts, nil, Inherited{Catalog: cat}, nil, TargetProject)

	// The skills screen lists it, disabled, with the rule that fired.
	var found *skillEntry
	for _, e := range m.skillEntries() {
		if e.name == "brokenmount" {
			found = &e
			break
		}
	}
	if found == nil {
		t.Fatal("the skills screen must list a broken skill rather than hide it")
	}
	if !strings.Contains(found.disabled, "mount target") {
		t.Errorf("the row must carry the reason: %q", found.disabled)
	}

	// The agent picker too: the primary declares [agent], so a user whose
	// agent this is sees why it is unselectable instead of an empty list.
	if !contains(m.agentOpts, "brokenmount") {
		t.Fatalf("the agent picker must show the broken agent skill: %v", m.agentOpts)
	}
	if d := m.optDisabled("brokenmount"); !strings.Contains(d, "mount target") {
		t.Errorf("the picker row must be disabled-with-reason, got %q", d)
	}
}
