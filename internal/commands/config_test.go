package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/configui"
)

// The editor's inherited view folds the same default layer resolution does:
// default.config's template and agent are onboarding favourites the resolver
// blanks, so the editor must blank them too, or the agent picker shows an
// inherit row naming an agent no develop ever delivers (P0). The rest of the
// file is real cascade and stays.
func TestEditorInheritedStripsDefaultFavourites(t *testing.T) {
	home := installHome(t)
	if err := os.WriteFile(filepath.Join(home, "default.config"), []byte("template = \"go\"\nagent = \"claude\"\napt = [\"ripgrep\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := builtins.EnsureStoreOut(home, nil); err != nil {
		t.Fatal(err)
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		t.Fatal(err)
	}
	inh := editorInherited(home, cat, config.ListTemplatesCatalog(cat), nil, configui.TargetProject, "")
	if !inh.HasLower {
		t.Fatal("a project editor has a lower cascade")
	}
	if inh.Default.Agent != "" || inh.Default.Template != "" {
		t.Errorf("default.config favourites must not reach the inherited view: agent=%q template=%q", inh.Default.Agent, inh.Default.Template)
	}
	if len(inh.Default.Apt) != 1 || inh.Default.Apt[0] != "ripgrep" {
		t.Errorf("the default's real cascade keys must survive the strip: %v", inh.Default.Apt)
	}
	if _, ok := inh.Templates["go"]; !ok {
		t.Errorf("bundled templates load into the inherited view: %v", inh.Templates)
	}
}

// The Skills screen's headless suffix reads the manifest through the
// editor's inherited view; a manifest without the field reads as none.
func TestEditorInheritedCarriesAgentHeadless(t *testing.T) {
	home := installHome(t)
	writeLocalSkill(t, home, "agenty", "[agent]\ncommand = \"agenty --go\"\nheadless = \"agenty-run --json\"\n")
	writeLocalSkill(t, home, "plainagent", "[agent]\ncommand = \"plain --go\"\n")
	if err := builtins.EnsureStoreOut(home, nil); err != nil {
		t.Fatal(err)
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		t.Fatal(err)
	}
	inh := editorInherited(home, cat, nil, []string{"agenty", "plainagent"}, configui.TargetProject, "")
	if got := inh.Skills["agenty"].AgentHeadless; got != "agenty-run --json" {
		t.Errorf("agenty headless = %q", got)
	}
	if rt, ok := inh.Skills["plainagent"]; !ok || rt.AgentHeadless != "" {
		t.Errorf("plainagent must load with no headless command: %+v (loaded %v)", rt, ok)
	}
}
