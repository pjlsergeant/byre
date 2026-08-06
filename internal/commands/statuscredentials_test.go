package commands

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

func TestStatusCredentialRows(t *testing.T) {
	s := statusInfo{
		Credentials: []config.CredentialDecl{
			{Name: "stripe", Kind: "env", Target: "STRIPE_KEY"},
			{Name: "cert", Kind: "file", Target: "TLS_CERT"},
		},
		CredentialStates: map[string]bool{"stripe": true},
		CredentialVault:  true,
		CredentialUnlock: launchCredentialUnlocked,
	}
	var got []string
	for _, r := range statusRowsOf(s, tierDefault) {
		got = append(got, r.Label+"|"+r.Value)
	}
	page := strings.Join(got, "\n")
	if !strings.Contains(page, "Credentials|stripe  env → STRIPE_KEY  (set)") {
		t.Fatalf("set row missing:\n%s", page)
	}
	if !strings.Contains(page, "|cert  file → TLS_CERT  (unset)") {
		t.Fatalf("unset row missing:\n%s", page)
	}
	// The unlock line is a launch-time fact; no live-state claim appears.
	if !strings.Contains(page, "this box launched: unlocked") {
		t.Fatalf("unlock line missing:\n%s", page)
	}

	// No vault: the remedy row appears; no unlock row without a record.
	s.CredentialVault = false
	s.CredentialUnlock = ""
	page = ""
	for _, r := range statusRowsOf(s, tierDefault) {
		page += r.Label + "|" + r.Value + "\n"
	}
	if !strings.Contains(page, "byre credentials init") {
		t.Fatalf("no-vault remedy missing:\n%s", page)
	}
	if strings.Contains(page, "this box launched") {
		t.Fatalf("unlock row must not render without a record:\n%s", page)
	}
}

func TestStatusDataCredentials(t *testing.T) {
	s := statusInfo{
		Credentials:      []config.CredentialDecl{{Name: "stripe", Kind: "env", Target: "STRIPE_KEY"}},
		CredentialStates: map[string]bool{"stripe": true},
		CredentialVault:  true,
		CredentialUnlock: "skipped-declined",
	}
	d := statusDataOf(s)
	if len(d.Credentials) != 1 || d.Credentials[0].Name != "stripe" || !d.Credentials[0].Set {
		t.Fatalf("data credentials = %+v", d.Credentials)
	}
	if d.CredentialUnlock != "skipped-declined" {
		t.Fatalf("data unlock = %q", d.CredentialUnlock)
	}
}
