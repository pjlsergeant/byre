package commands

import (
	"regexp"
	"strings"
	"testing"
)

// Bundled inspect shows a computed display digest (ADR 0029 deferral closed):
// same line shape as installed rows, stable across invocations.
func TestInspectBundledShowsDisplayDigest(t *testing.T) {
	installHome(t)
	s, out, _ := testStreams("", false)
	if err := SkillInspect(s, "claude"); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`Digest:\s+sha256:([0-9a-f]{64})\n`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no display digest in bundled inspect output:\n%s", out.String())
	}
	s2, out2, _ := testStreams("", false)
	if err := SkillInspect(s2, "claude"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2.String(), m[1]) {
		t.Errorf("display digest must be stable across inspects; first %s, second:\n%s", m[1], out2.String())
	}
}
