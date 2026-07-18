package builtins

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

// TestCodexSharedAuthCompositionResolves pins the codex-shared-auth companion
// composing with the codex skill: the machine-scoped identity volume and the
// 00-prefixed symlink-assert hook sorting BEFORE codex's own login hook in
// the launcher's glob order (the login hook must see the asserted link).
func TestCodexSharedAuthCompositionResolves(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "codex", Skills: []string{"codex-shared-auth"}}, cat)
	if err != nil {
		t.Fatalf("codex + codex-shared-auth failed to resolve: %v", err)
	}
	var companion string
	var codexHooks []string
	for _, b := range res.BuildBlocks() {
		for _, sf := range b.Files {
			if !strings.HasPrefix(sf.Dest, "/etc/byre/firstrun.d/") {
				continue
			}
			switch b.Name {
			case "byre/codex-shared-auth", "codex-shared-auth":
				companion = path.Base(sf.Dest)
			case "byre/codex", "codex":
				codexHooks = append(codexHooks, path.Base(sf.Dest))
			}
		}
	}
	if companion == "" {
		t.Fatal("symlink-assert hook not shipped")
	}
	if len(codexHooks) == 0 {
		t.Fatal("codex ships no firstrun hooks; the ordering invariant has nothing to order against")
	}
	for _, h := range codexHooks {
		if !(companion < h) {
			t.Errorf("hook ordering invariant broken: companion %q must sort before codex's %q", companion, h)
		}
	}
	var identity bool
	for _, v := range res.Volumes() {
		if v.Name == "codex-identity" && v.MachineScoped() && v.Target == "/home/dev/.byre-identity/codex" {
			identity = true
		}
	}
	if !identity {
		t.Errorf("identity volume missing or mis-declared: %+v", res.Volumes())
	}
}

// runCodexSharedAuthHook executes the real materialized symlink-assert hook
// against a temp identity base + CODEX_HOME (the BYRE_IDENTITY_BASE seam).
func runCodexSharedAuthHook(t *testing.T, identityBase, codexHome string) {
	t.Helper()
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "firstrun.sh")
	cmd := exec.Command("bash", hook)
	cmd.Env = append(os.Environ(), "BYRE_IDENTITY_BASE="+identityBase, "CODEX_HOME="+codexHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
}

// The symlink-assert hook's four behaviors, driven for real: fresh box gets a
// dangling link; an existing per-project login is ADOPTED (moved, then
// linked); a local fork is healed in favor of the shared credential; and the
// whole thing is idempotent.
func TestCodexSharedAuthHookBehavior(t *testing.T) {
	base, home := t.TempDir(), t.TempDir()
	shared := filepath.Join(base, "codex", "auth.json")
	cred := filepath.Join(home, "auth.json")

	// 1. Fresh: dangling symlink pointing at the (absent) shared credential.
	runCodexSharedAuthHook(t, base, home)
	if got, err := os.Readlink(cred); err != nil || got != shared {
		t.Fatalf("fresh run should leave a dangling link to %q, got %q (%v)", shared, got, err)
	}
	if _, err := os.Stat(shared); !os.IsNotExist(err) {
		t.Fatalf("fresh run must not fabricate a shared credential")
	}

	// 2. Adopt: a real local login and no shared copy — the file MOVES in.
	if err := os.Remove(cred); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cred, []byte(`{"adopted":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runCodexSharedAuthHook(t, base, home)
	if b, err := os.ReadFile(shared); err != nil || string(b) != `{"adopted":true}` {
		t.Fatalf("existing login not adopted into the shared volume: %v %q", err, b)
	}
	if got, _ := os.Readlink(cred); got != shared {
		t.Fatalf("adopted cred not re-linked: %q", got)
	}

	// 3. Heal a fork: local plain file AND shared credential — shared wins.
	if err := os.Remove(cred); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cred, []byte(`{"fork":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runCodexSharedAuthHook(t, base, home)
	if b, _ := os.ReadFile(shared); string(b) != `{"adopted":true}` {
		t.Fatalf("shared credential clobbered by a fork: %q", b)
	}
	if got, _ := os.Readlink(cred); got != shared {
		t.Fatalf("fork not healed to the link: %q", got)
	}

	// 4. Idempotent: run again, nothing changes.
	runCodexSharedAuthHook(t, base, home)
	if b, _ := os.ReadFile(cred); string(b) != `{"adopted":true}` {
		t.Fatalf("idempotent re-run changed the credential: %q", b)
	}
}

// TestCodexLoginHookRejectsForeignSymlink mirrors the opencode login-hook
// coverage for codex's carve-out: the trusted target is the HARDCODED full
// path /home/dev/.byre-identity/codex/auth.json (own-dir + basename equality,
// not a /home/dev/.byre-identity/* wildcard — a wildcard would trust a link
// into a SIBLING agent's identity dir, through which a `codex login` would
// overwrite that agent's machine-wide credential; a dir-only match would
// trust any other name inside codex's dir).
//
// LIMIT of the behavioral half: the trusted base is deliberately hardcoded
// (an env seam would let a config-supplied [env] var redefine the trusted
// namespace — see the opencode hook's comment), so a unit test can't build a
// sibling-identity fixture; a temp-dir target is foreign under BOTH the old
// wildcard and the new equality. The narrowing itself is pinned by the source
// assertions below (codereview 2026-07-17); the behavioral cases cover
// foreign-link removal and the logged-in short-circuit.
func TestCodexLoginHookRejectsForeignSymlink(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh")

	// Pin the WHOLE predicate line in the hook source — the full conjunction,
	// not its halves independently — so weakening either side (or the &&)
	// fails here.
	src, err := os.ReadFile(hook)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src),
		`if [ "$tdir" = "/home/dev/.byre-identity/codex" ] && [ "$(basename "$target")" = "auth.json" ]; then`) {
		t.Error("hook must trust ONLY the full canonical path /home/dev/.byre-identity/codex/auth.json (single && predicate)")
	}

	bin := t.TempDir()
	stamp := filepath.Join(bin, "login-attempted")
	// Stub codex: `login status` reports NOT logged in (exit 1); `login
	// --device-auth` records the attempt. Anything else is a no-op success.
	stub := "#!/bin/sh\n" +
		"case \"$1 $2\" in\n" +
		"'login status') exit 1 ;;\n" +
		"'login --device-auth') touch " + stamp + "; exit 0 ;;\n" +
		"esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	loginAttempted := func() bool { _, err := os.Stat(stamp); return err == nil }
	run := func(codexHome string) {
		t.Helper()
		cmd := exec.Command("sh", hook)
		cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+codexHome)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v (%s)", err, out)
		}
	}

	// A FOREIGN symlinked credential (temp-dir target) is removed; a fresh
	// login runs.
	home := t.TempDir()
	cred := filepath.Join(home, "auth.json")
	planted := filepath.Join(home, "elsewhere.json")
	if err := os.WriteFile(planted, []byte(`{"tokens":{"access_token":"planted"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(planted, cred); err != nil {
		t.Fatal(err)
	}
	run(home)
	if _, err := os.Lstat(cred); !os.IsNotExist(err) {
		t.Fatalf("foreign symlinked credential must be removed, still present (%v)", err)
	}
	if !loginAttempted() {
		t.Fatal("removal must fall through to a fresh login; none was attempted")
	}

	// A logged-in codex (login status = 0) short-circuits: no login attempted.
	_ = os.Remove(stamp)
	if err := os.WriteFile(filepath.Join(bin, "codex"),
		[]byte("#!/bin/sh\ntest \"$1 $2\" = 'login status' && exit 0\ntouch "+stamp+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	home2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(home2, "auth.json"), []byte(`{"tokens":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run(home2)
	if loginAttempted() {
		t.Fatal("a logged-in codex must short-circuit the login; one was attempted")
	}
}
