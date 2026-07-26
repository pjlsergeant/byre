package config

import (
	"strings"
	"testing"
)

// A composition key in template.config is banned by PRESENCE, not emptiness --
// the old name said "empty" and one case contradicted it. Each case names the
// rule that must fire, so an unrelated parse failure cannot keep this green.
func TestParseTemplateBodyBansCompositionKeys(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"skills empty", "base = \"x\"\nskills = []\n", "skills is not allowed in template.config"},
		{"skills non-empty", "base = \"x\"\nskills = [\"firewall\"]\n", "skills is not allowed in template.config"},
		{"agent empty", "base = \"x\"\nagent = \"\"\n", "agent is not allowed in template.config"},
		{"sources empty", "base = \"x\"\n[sources]\n", "[sources] is not allowed in template.config"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseTemplateBody([]byte(tc.body))
			if err == nil {
				t.Fatalf("want composition error naming %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("wrong rule fired: got %v, want it to name %q", err, tc.want)
			}
		})
	}
	// Shape-only template is fine.
	if _, err := ParseTemplateBody([]byte("base = \"golang:1.22\"\negress_offered = [\"proxy.golang.org\"]\n")); err != nil {
		t.Fatal(err)
	}
}
