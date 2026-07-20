package build

import (
	"testing"

	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// Skill blocks emit in provenance order -- bundled, installed, local, stable
// within a class (ADR 0041). The rule this pins: a volatile skill's layers
// can never precede a stabler skill's, so payload edits in an installed or
// local package don't invalidate bundled installers behind them.
func TestSkillBlocksOrderByProvenance(t *testing.T) {
	blocks := []skills.BuildBlock{
		{Name: "local-b", Provenance: packages.ProvLocal},
		{Name: "inst-a", Provenance: packages.ProvInstalled},
		{Name: "bund-a", Provenance: packages.ProvBundled},
		{Name: "inst-b", Provenance: packages.ProvInstalled},
		{Name: "bund-b", Provenance: packages.ProvBundled},
	}
	out, jobs, err := planSkillBlocks(project.Paths{}, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("no files declared, want no staging jobs, got %d", len(jobs))
	}
	want := []string{"bund-a", "bund-b", "inst-a", "inst-b", "local-b"}
	if len(out) != len(want) {
		t.Fatalf("got %d blocks, want %d", len(out), len(want))
	}
	for i, name := range want {
		if out[i].Name != name {
			got := make([]string, len(out))
			for j, b := range out {
				got[j] = b.Name
			}
			t.Fatalf("block order: got %v, want %v", got, want)
		}
	}
	// The caller's slice must not be reordered in place: enable order is the
	// agent-facing order elsewhere (context composition, status).
	if blocks[0].Name != "local-b" {
		t.Fatal("planSkillBlocks mutated its input slice")
	}
}
