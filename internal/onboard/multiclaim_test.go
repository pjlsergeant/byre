package onboard

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

func TestMultiClaimPickerShowsAll(t *testing.T) {
	var out bytes.Buffer
	offer := SharedAuthOffer{
		Claimants: []string{"claude-shared-auth", "my-claude-auth"},
		Labels:    []string{"bundled, byre's", "local"},
	}
	// Pick 2)
	c, yes, err := OfferSharedAuthChoice(&out, bufio.NewReader(strings.NewReader("2\n")), "claude", offer)
	if err != nil {
		t.Fatal(err)
	}
	if !yes || c != "my-claude-auth" {
		t.Fatalf("got companion=%q yes=%v", c, yes)
	}
	s := out.String()
	if !strings.Contains(s, "1) claude-shared-auth") || !strings.Contains(s, "bundled, byre's") {
		t.Fatalf("missing first claimant:\n%s", s)
	}
	if !strings.Contains(s, "2) my-claude-auth") || !strings.Contains(s, "local") {
		t.Fatalf("missing second claimant:\n%s", s)
	}
	if !strings.Contains(s, "N) none") {
		t.Fatalf("missing none option:\n%s", s)
	}
}

func TestSharedAuthTableShapeRoundTrip(t *testing.T) {
	home := t.TempDir()
	if err := SaveSharedAuthDefaultPick(home, "claude", "claude-shared-auth", true); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseFile(home + "/default.config")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SharedAuth.CompanionPick("claude") != "claude-shared-auth" {
		t.Fatalf("pick = %+v", cfg.SharedAuth)
	}
	// Decline + save removes the entry.
	if err := SaveSharedAuthDefaultPick(home, "claude", "", false); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.ParseFile(home + "/default.config")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SharedAuth.Empty() {
		t.Fatalf("decline should remove entry: %+v", cfg.SharedAuth)
	}
}

func TestLegacyArrayStillPrefillsYes(t *testing.T) {
	home := t.TempDir()
	if err := config.AtomicWrite(home+"/default.config", "shared_auth = [\"claude\"]\n"); err != nil {
		t.Fatal(err)
	}
	if !SharedAuthPreference(home, "claude") {
		t.Fatal("legacy array should prefill yes")
	}
	if SharedAuthPick(home, "claude") != "" {
		t.Fatal("legacy array has no pick")
	}
}

func TestStalePickNotice(t *testing.T) {
	var out bytes.Buffer
	offer := SharedAuthOffer{
		Claimants:       []string{"claude-shared-auth"},
		Labels:          []string{"bundled, byre's"},
		StalePickNotice: `your saved pick "gone" is no longer installed`,
	}
	_, _, err := OfferSharedAuthChoice(&out, bufio.NewReader(strings.NewReader("n\n")), "claude", offer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no longer installed") {
		t.Fatalf("missing stale notice:\n%s", out.String())
	}
}
