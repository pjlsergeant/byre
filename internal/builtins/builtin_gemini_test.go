package builtins

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

// gemini-shared-auth: composition + the symlink-assert hook's behaviors for
// all three identity files (fresh -> dangling links; adopt; heal; idempotent).
// The skill is GATE PENDING (ADR 0017) -- these tests pin the mechanism, not
// the rotation-safety claim, which only the host-side gate can settle.
func TestGeminiSharedAuthCompositionAndHook(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "gemini", Skills: []string{"gemini-shared-auth"}}, cat)
	if err != nil {
		t.Fatalf("gemini + gemini-shared-auth failed to resolve: %v", err)
	}
	var identity bool
	for _, v := range res.Volumes() {
		if v.Name == "gemini-identity" && v.MachineScoped() && v.Target == "/home/dev/.byre-identity/gemini" {
			identity = true
		}
	}
	if !identity {
		t.Errorf("identity volume missing or mis-declared: %+v", res.Volumes())
	}

	hook := filepath.Join(skillDir(t, cat, "gemini-shared-auth"), "firstrun.sh")
	base, home := t.TempDir(), t.TempDir()
	run := func() {
		t.Helper()
		cmd := exec.Command("bash", hook)
		cmd.Env = append(os.Environ(), "BYRE_IDENTITY_BASE="+base, "BYRE_GEMINI_DIR="+home)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v (%s)", err, out)
		}
	}
	files := []string{"gemini-credentials.json", "oauth_creds.json", "google_accounts.json", "installation_id"}

	// Fresh: three dangling links, nothing fabricated, trust file untouched.
	if err := os.WriteFile(filepath.Join(home, "trustedFolders.json"), []byte(`{"t":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run()
	for _, f := range files {
		want := filepath.Join(base, "gemini", f)
		if got, err := os.Readlink(filepath.Join(home, f)); err != nil || got != want {
			t.Fatalf("fresh run: %s not a dangling link to %q: %q (%v)", f, want, got, err)
		}
	}
	if fi, err := os.Lstat(filepath.Join(home, "trustedFolders.json")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("trustedFolders.json must stay a per-project regular file")
	}

	// Adopt: a real local login moves into the shared volume.
	if err := os.Remove(filepath.Join(home, "oauth_creds.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "oauth_creds.json"), []byte(`{"adopted":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run()
	if b, err := os.ReadFile(filepath.Join(base, "gemini", "oauth_creds.json")); err != nil || string(b) != `{"adopted":true}` {
		t.Fatalf("login not adopted: %v %q", err, b)
	}

	// Heal: shared copy wins over a local fork; idempotent re-run.
	if err := os.Remove(filepath.Join(home, "oauth_creds.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "oauth_creds.json"), []byte(`{"fork":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run()
	run()
	if b, _ := os.ReadFile(filepath.Join(home, "oauth_creds.json")); string(b) != `{"adopted":true}` {
		t.Fatalf("fork not healed to the shared credential: %q", b)
	}

	// selectedType seed: on a box with no prior choice, the hook seeds
	// oauth-personal so gemini's dialog (which rm's oauth_creds.json and forks
	// the login) never opens. Requires jq (skip cleanly without it).
	if _, err := exec.LookPath("jq"); err == nil {
		settings := filepath.Join(home, "settings.json")
		_ = os.Remove(settings)
		run()
		if b, err := os.ReadFile(settings); err != nil ||
			!strings.Contains(string(b), `"selectedType"`) ||
			!strings.Contains(string(b), "oauth-personal") {
			t.Fatalf("fresh box: selectedType not seeded to oauth-personal: %v %q", err, b)
		}

		// No-clobber: a deliberate api-key choice is preserved, never overwritten.
		if err := os.WriteFile(settings, []byte(`{"security":{"auth":{"selectedType":"gemini-api-key"}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		run()
		if b, _ := os.ReadFile(settings); !strings.Contains(string(b), "gemini-api-key") ||
			strings.Contains(string(b), "oauth-personal") {
			t.Fatalf("deliberate api-key choice must not be clobbered: %q", b)
		}

		// Merge-preserve: an existing settings.json with UNSET selectedType keeps
		// its other keys and gains the seed.
		if err := os.WriteFile(settings, []byte(`{"theme":"Default","ui":{"x":1}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		run()
		b, _ := os.ReadFile(settings)
		if !strings.Contains(string(b), "oauth-personal") || !strings.Contains(string(b), `"theme"`) ||
			!strings.Contains(string(b), `"ui"`) {
			t.Fatalf("merge must add the seed and keep existing keys: %q", b)
		}

		// Odd shape (string-valued security): the seed must NOT error out and
		// must NOT mangle the user's file — left byte-for-byte untouched
		// (codereview 2026-07-16, finding 2). Partial-object shapes still seed.
		for _, odd := range []string{`{"security":"strict"}`, `{"security":{"auth":"external"}}`} {
			if err := os.WriteFile(settings, []byte(odd), 0o600); err != nil {
				t.Fatal(err)
			}
			run()
			if b, _ := os.ReadFile(settings); string(b) != odd {
				t.Fatalf("odd settings shape must be left untouched, got %q from %q", b, odd)
			}
		}
		// A partial object (security present, auth absent) still seeds cleanly.
		if err := os.WriteFile(settings, []byte(`{"security":{"other":true}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		run()
		if b, _ := os.ReadFile(settings); !strings.Contains(string(b), "oauth-personal") ||
			!strings.Contains(string(b), `"other"`) {
			t.Fatalf("partial-object security must seed and keep its keys: %q", b)
		}
	}
}
