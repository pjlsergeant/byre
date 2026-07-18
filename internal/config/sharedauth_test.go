package config

import "testing"

// Installed agents have qualified owner/name IDs; '/' is illegal in a bare
// TOML key, so the encoder must quote pick keys or the emitted line is
// unparsable and every surgical save for a third-party agent fails its
// semantic verify.
func TestEncodeTOMLLineQuotesQualifiedPickKeys(t *testing.T) {
	pref := SharedAuthPref{Pick: map[string]string{
		"acme/agent": "acme/agent-shared-auth",
		"claude":     "claude-shared-auth",
	}}
	line := pref.EncodeTOMLLine()
	cfg, err := Parse([]byte(line + "\n"))
	if err != nil {
		t.Fatalf("emitted line %q must parse: %v", line, err)
	}
	if got := cfg.SharedAuth.CompanionPick("acme/agent"); got != "acme/agent-shared-auth" {
		t.Fatalf("acme/agent pick round-trip: got %q", got)
	}
	if got := cfg.SharedAuth.CompanionPick("claude"); got != "claude-shared-auth" {
		t.Fatalf("claude pick round-trip: got %q", got)
	}
}
