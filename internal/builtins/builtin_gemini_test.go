package builtins

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
	lib := sharedAuthLibSeam(t, cat, "gemini-shared-auth")
	runIn := func(base, home string) string {
		t.Helper()
		return runHook(t, "bash", hook, lib, "BYRE_IDENTITY_BASE="+base, "BYRE_GEMINI_DIR="+home)
	}
	base, home := physTempDir(t), physTempDir(t)
	run := func() { t.Helper(); runIn(base, home) }
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

	// An EMPTY local file is no login to promote: it is replaced by the link
	// like any other local file, and nothing announces a promotion.
	empty, emptyHome := physTempDir(t), physTempDir(t)
	if err := os.WriteFile(filepath.Join(emptyHome, "oauth_creds.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out := runIn(empty, emptyHome)
	if strings.Contains(out, "promoted") {
		t.Fatalf("an empty local file must not be announced as a promoted login: %q", out)
	}
	if _, err := os.Lstat(filepath.Join(empty, "gemini", "oauth_creds.json")); !os.IsNotExist(err) {
		t.Fatalf("an empty local file must not become the shared credential (%v)", err)
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
		// must NOT mangle the user's file — left byte-for-byte untouched.
		// Partial-object shapes still seed.
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

// Containment cases: the refusal plus the unchanged victim is the assertion
// (CLAUDE.md, two tiers), so no message fragments are pinned. Each needs its
// own planted base/home.
func TestGeminiSharedAuthHookHardening(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "gemini-shared-auth"), "firstrun.sh")
	lib := sharedAuthLibSeam(t, cat, "gemini-shared-auth")
	runIn := func(base, home string) string {
		t.Helper()
		return runHook(t, "bash", hook, lib, "BYRE_IDENTITY_BASE="+base, "BYRE_GEMINI_DIR="+home)
	}
	localLogin := func(t *testing.T, home, f, body string) string {
		t.Helper()
		p := filepath.Join(home, f)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// A symlink planted AT a shared file is refused for that file: the
	// local login stays a regular file, the planted target is never written,
	// and the OTHER files still get their links (one bad file must not cost
	// the rest theirs).
	t.Run("symlinked shared path refused", func(t *testing.T) {
		base, home := physTempDir(t), physTempDir(t)
		if err := os.MkdirAll(filepath.Join(base, "gemini"), 0o755); err != nil {
			t.Fatal(err)
		}
		decoy := filepath.Join(physTempDir(t), "decoy.json")
		if err := os.WriteFile(decoy, []byte("decoy"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(decoy, filepath.Join(base, "gemini", "oauth_creds.json")); err != nil {
			t.Fatal(err)
		}
		cred := localLogin(t, home, "oauth_creds.json", "local")
		runIn(base, home)
		isRegularWith(t, cred, "local")
		if b, _ := os.ReadFile(decoy); string(b) != "decoy" {
			t.Fatalf("the planted link's target was written: %q", b)
		}
		if got, err := os.Readlink(filepath.Join(home, "google_accounts.json")); err != nil || got != filepath.Join(base, "gemini", "google_accounts.json") {
			t.Fatalf("a refusal on one file must not skip the others: %q (%v)", got, err)
		}
	})

	// A failed promotion (read-only identity volume) keeps the local login,
	// instead of falling through to a link over it.
	t.Run("failed promotion keeps the local login", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores the read-only mode this case relies on")
		}
		base, home := physTempDir(t), physTempDir(t)
		idDir := filepath.Join(base, "gemini")
		if err := os.MkdirAll(idDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(idDir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(idDir, 0o755) })
		cred := localLogin(t, home, "oauth_creds.json", "only-login")
		runIn(base, home)
		isRegularWith(t, cred, "only-login")
		if ents, _ := os.ReadDir(idDir); len(ents) != 0 {
			t.Fatalf("the read-only identity dir gained entries: %v", ents)
		}
	})

	t.Run("symlinked identity-dir ancestor refused", func(t *testing.T) {
		real, home := physTempDir(t), physTempDir(t)
		base := filepath.Join(physTempDir(t), "base")
		if err := os.Symlink(real, base); err != nil {
			t.Fatal(err)
		}
		cred := localLogin(t, home, "oauth_creds.json", "local")
		runIn(base, home)
		isRegularWith(t, cred, "local")
		// Checked BEFORE mkdir -p: nothing at all is created through the link.
		if ents, err := os.ReadDir(real); err != nil || len(ents) != 0 {
			t.Fatalf("the symlinked ancestor's target must stay empty: %v (%v)", ents, err)
		}
	})

	// A link whose target is the shared path plus a trailing newline -- a
	// DIFFERENT file, which $(readlink) would have compared equal -- is
	// re-asserted to the clean path.
	t.Run("newline-suffixed link target re-asserted", func(t *testing.T) {
		base, home := physTempDir(t), physTempDir(t)
		if err := os.MkdirAll(filepath.Join(base, "gemini"), 0o755); err != nil {
			t.Fatal(err)
		}
		shared := filepath.Join(base, "gemini", "oauth_creds.json")
		if err := os.WriteFile(shared, []byte("shared"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(shared+"\n", filepath.Join(home, "oauth_creds.json")); err != nil {
			t.Fatal(err)
		}
		runIn(base, home)
		if got, err := os.Readlink(filepath.Join(home, "oauth_creds.json")); err != nil || got != shared {
			t.Fatalf("a newline-suffixed link must be re-asserted to %q, got %q (%v)", shared, got, err)
		}
	})

	// A FIFO at a shared path is refused for that file: the local login
	// stays a regular file, the hook does not block on it, and the OTHER
	// files still get their links.
	t.Run("FIFO at a shared path refused", func(t *testing.T) {
		base, home := physTempDir(t), physTempDir(t)
		if err := os.MkdirAll(filepath.Join(base, "gemini"), 0o755); err != nil {
			t.Fatal(err)
		}
		fifo := filepath.Join(base, "gemini", "oauth_creds.json")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		cred := localLogin(t, home, "oauth_creds.json", "local")
		runIn(base, home)
		isRegularWith(t, cred, "local")
		if fi, err := os.Lstat(fifo); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
			t.Fatalf("the FIFO must be left alone: %v %v", fi, err)
		}
		if got, err := os.Readlink(filepath.Join(home, "google_accounts.json")); err != nil || got != filepath.Join(base, "gemini", "google_accounts.json") {
			t.Fatalf("a refusal on one file must not skip the others: %q (%v)", got, err)
		}
	})

	// The settings.json seed opens the path only when it is a regular file:
	// a FIFO there would block the launch forever (runHook's bound turns that
	// into a failure) and a dangling symlink would be written through. Both
	// are skipped, named on stderr, and left as found.
	t.Run("FIFO at settings.json left alone", func(t *testing.T) {
		base, home := physTempDir(t), physTempDir(t)
		settings := filepath.Join(home, "settings.json")
		if err := syscall.Mkfifo(settings, 0o600); err != nil {
			t.Fatal(err)
		}
		runIn(base, home)
		if fi, err := os.Lstat(settings); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
			t.Fatalf("the FIFO at settings.json must be left alone: %v %v", fi, err)
		}
	})

	t.Run("dangling settings.json symlink not written through", func(t *testing.T) {
		base, home := physTempDir(t), physTempDir(t)
		victim := filepath.Join(physTempDir(t), "victim.json")
		if err := os.Symlink(victim, filepath.Join(home, "settings.json")); err != nil {
			t.Fatal(err)
		}
		runIn(base, home)
		if _, err := os.Lstat(victim); !os.IsNotExist(err) {
			t.Fatalf("the seed wrote through a dangling settings.json symlink (%v)", err)
		}
	})

	// The seed's rename cannot take GNU's -T, so a symlink to a DIRECTORY raced
	// in after the shape check does not fail it: mv follows the link and deposits
	// the temp INSIDE the target -- and that temp is the user's own settings.json
	// merged with the seed, which holds a stored API key if they have one. The
	// refusal was already right; the deposit staying was the bug. An `mv` stub
	// plants the link in the window the real race would use.
	t.Run("settings seed leaves nothing in a raced-in directory link", func(t *testing.T) {
		if _, err := exec.LookPath("jq"); err != nil {
			t.Skip("no jq")
		}
		realMv, err := exec.LookPath("mv")
		if err != nil {
			t.Skip("no mv on PATH")
		}
		base, home := physTempDir(t), physTempDir(t)
		settings := filepath.Join(home, "settings.json")
		if err := os.WriteFile(settings, []byte(`{"theme":"Default"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		planted := filepath.Join(physTempDir(t), "planted-dir")
		if err := os.MkdirAll(planted, 0o755); err != nil {
			t.Fatal(err)
		}
		bin := physTempDir(t)
		// Only the settings rename is raced; the identity files' own link
		// asserts run through the same `mv` and must be left alone.
		// The window the planter has: settings.json was checked and is still a
		// regular file, so it has to go before the link can take its name.
		stub := "#!/bin/sh\ncase \"$*\" in\n*" + settings +
			") rm -f -- " + strconv.Quote(settings) + " && ln -s -- " + strconv.Quote(planted) +
			" " + strconv.Quote(settings) + " ;;\nesac\nexec " + realMv + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, "mv"), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
		out := runHook(t, "bash", hook, lib, "PATH="+bin+":"+os.Getenv("PATH"),
			"BYRE_IDENTITY_BASE="+base, "BYRE_GEMINI_DIR="+home)
		if !strings.Contains(out, "could not write") {
			t.Fatalf("the refused seed must say so: %q", out)
		}
		if ents, err := os.ReadDir(planted); err != nil || len(ents) != 0 {
			t.Fatalf("the refused seed left the merged settings inside the planted directory: %v (%v)", ents, err)
		}
		if left, _ := filepath.Glob(filepath.Join(home, ".settings.json.*")); len(left) != 0 {
			t.Fatalf("the seed's temp must not be left behind: %v", left)
		}
	})

	// The seed writes a fresh mktemp name and renames it over settings.json,
	// so a symlink planted at a guessable temp name is never written through
	// and settings.json ends up a regular file carrying the merged content.
	// Requires jq, like the seed itself.
	t.Run("planted temp name not written through", func(t *testing.T) {
		if _, err := exec.LookPath("jq"); err != nil {
			t.Skip("no jq")
		}
		base, home := physTempDir(t), physTempDir(t)
		settings := filepath.Join(home, "settings.json")
		if err := os.WriteFile(settings, []byte(`{"theme":"Default"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		victim := filepath.Join(physTempDir(t), "victim.json")
		if err := os.WriteFile(victim, []byte("victim"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, settings+".byre.tmp"); err != nil {
			t.Fatal(err)
		}
		runIn(base, home)
		if b, err := os.ReadFile(victim); err != nil || string(b) != "victim" {
			t.Fatalf("the planted temp link's target was written: %q (%v)", b, err)
		}
		fi, err := os.Lstat(settings)
		if err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("settings.json must still be a regular file: %v %v", fi, err)
		}
		if b, _ := os.ReadFile(settings); !strings.Contains(string(b), "oauth-personal") ||
			!strings.Contains(string(b), `"theme"`) {
			t.Fatalf("settings.json must carry the merged seed: %q", b)
		}
		if left, _ := filepath.Glob(filepath.Join(home, ".settings.json.*")); len(left) != 0 {
			t.Fatalf("the seed's temp must not be left behind: %v", left)
		}
	})
}
