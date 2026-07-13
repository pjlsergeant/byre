package config

import "testing"

func TestParseTemplateBodyBansEmptyCompositionKeys(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"skills empty", "base = \"x\"\nskills = []\n"},
		{"agent empty", "base = \"x\"\nagent = \"\"\n"},
		{"sources empty", "base = \"x\"\n[sources]\n"},
		{"skills non-empty", "base = \"x\"\nskills = [\"firewall\"]\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseTemplateBody([]byte(tc.body)); err == nil {
				t.Fatal("want composition error")
			}
		})
	}
	// Shape-only template is fine.
	if _, err := ParseTemplateBody([]byte("base = \"golang:1.22\"\negress_offered = [\"proxy.golang.org\"]\n")); err != nil {
		t.Fatal(err)
	}
}
