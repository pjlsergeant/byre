package builtins

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/skills"
)

// envdPurityArms maps an env.d hook's image destination to the test that pins
// that hook against ADR 0028's purity contract. BUNDLED skills only: hooks
// from installed or third-party packages are a reviewer's tripwire per the
// ADR, not something byre's own suite can enumerate. This table is itself a
// review tripwire -- it proves a shipped hook has a named purity test, never
// that the named test ran or that it is any good. The names resolve to real
// test functions via the doctrine index (docs/adr/README.md's 0028 arms,
// checked by TestDoctrineIndexArmsResolve), so a rename cannot rot this
// silently.
var envdPurityArms = map[string]string{
	"/etc/byre/env.d/50-claude-shared-auth.sh": "TestClaudeSharedAuthEnvHookExportsOnly",
	"/etc/byre/env.d/50-docker-host.sh":        "TestDockerHostComposeEnvHookIsPure",
}

// A new bundled env.d hook must arrive with a purity test naming it -- the
// contract is prose, so the only thing standing between a hook and an
// unreviewed side effect is somebody having looked. This walks the bundled
// skills' declared [build].files (the stage-2 body map: what the image gets,
// not the [[package.files]] payload list) and fails in both directions: a
// hook with no arm, and an arm naming a hook that no longer ships.
func TestBundledEnvdHooksHavePurityArms(t *testing.T) {
	_, cat := testCat(t)
	shipped := map[string]string{} // dest -> "<skill id>:<source>"
	for _, ent := range cat.List(packages.KindSkill) {
		if ent.Provenance != packages.ProvBundled {
			continue
		}
		raw, err := ent.ReadPrimary()
		if err != nil {
			t.Fatalf("%s: reading skill.toml: %v", ent.ID, err)
		}
		f, err := skills.ParsePrimaryBytes(raw)
		if err != nil {
			t.Fatalf("%s: parsing skill.toml: %v", ent.ID, err)
		}
		for src, dest := range f.Build.Files {
			if strings.HasPrefix(dest, "/etc/byre/env.d/") {
				shipped[dest] = ent.ID + ":" + src
			}
		}
	}
	if len(shipped) == 0 {
		t.Fatal("no bundled env.d hooks found -- the walk broke, or the mechanism moved")
	}
	for dest, origin := range shipped {
		if envdPurityArms[dest] == "" {
			t.Errorf("bundled env.d hook %s (%s) has no purity test in envdPurityArms -- write one against ADR 0028 and name it here", dest, origin)
		}
	}
	for dest := range envdPurityArms {
		if shipped[dest] == "" {
			t.Errorf("envdPurityArms names %s, which no bundled skill ships any more -- drop the row", dest)
		}
	}
}
