package builtins

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
	"github.com/pjlsergeant/byre/internal/testtools"
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
	var reconciler bool
	var codexHooks []string
	for _, b := range res.BuildBlocks() {
		for _, sf := range b.Files {
			if (b.Name == "byre/codex-shared-auth" || b.Name == "codex-shared-auth") &&
				sf.Dest == "/usr/local/lib/byre-codex-auth-reconcile" {
				reconciler = true
			}
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
	if !reconciler {
		t.Fatal("shared-auth reconciliation helper not shipped")
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
	reconcile := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "reconcile.sh")
	cmd := exec.Command("bash", hook)
	cmd.Env = append(os.Environ(), "BYRE_IDENTITY_BASE="+identityBase, "CODEX_HOME="+codexHome,
		"BYRE_CODEX_AUTH_RECONCILE="+reconcile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
}

// writeCodexHookShims gives the materialized Linux hook its process-group and
// file-lock primitives on macOS CI too. The system Perl supplies both there;
// exec preserves the PID just like util-linux setsid in the shipped image.
func writeCodexHookShims(t *testing.T, bin string) {
	t.Helper()
	testtools.NeedTool(t, "bash", "jq", "date", "perl", "ps")
	// Linux must exercise the exact util-linux CLI shipped in the box. The
	// shims exist only for stock macOS, which has neither setsid nor flock.
	if runtime.GOOS != "darwin" {
		testtools.NeedTool(t, "setsid", "flock")
		return
	}
	setsid := "#!/bin/sh\nexec perl -MPOSIX -e 'POSIX::setsid(); exec @ARGV' -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "setsid"), []byte(setsid), 0o755); err != nil {
		t.Fatal(err)
	}
	flock := `#!/usr/bin/perl
use Fcntl qw(LOCK_EX LOCK_UN);
my $fd = $ARGV[-1];
open(my $fh, ">&=$fd") or exit 1;
my $op = (grep { $_ eq '-u' } @ARGV) ? LOCK_UN : LOCK_EX;
flock($fh, $op) or exit 1;
`
	if err := os.WriteFile(filepath.Join(bin, "flock"), []byte(flock), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Diagnostics are strictly opt-in and record lifecycle/file metadata without
// copying credential material into the shared log.
func TestCodexSharedAuthDiagnosticsAreGatedAndRedacted(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "firstrun.sh")
	reconcile := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "reconcile.sh")
	base, home := t.TempDir(), t.TempDir()
	shared := filepath.Join(base, "codex", "auth.json")
	cred := filepath.Join(home, "auth.json")
	logPath := filepath.Join(base, "codex", "byre-auth-diagnostic.log")
	const secret = "must-not-appear-in-diagnostics"

	run := func(enabled bool) {
		t.Helper()
		cmd := exec.Command("bash", hook)
		cmd.Env = append(os.Environ(), "BYRE_IDENTITY_BASE="+base, "CODEX_HOME="+home,
			"BYRE_CODEX_AUTH_RECONCILE="+reconcile)
		if enabled {
			cmd.Env = append(cmd.Env, "CODEX_AUTH_DIAGNOSTIC_BYRE=1")
		} else {
			cmd.Env = append(cmd.Env, "CODEX_AUTH_DIAGNOSTIC_BYRE=")
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v (%s)", err, out)
		}
	}

	run(false)
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("diagnostic log must not exist when disabled: %v", err)
	}

	if err := os.Remove(cred); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"`+secret+`","refresh_token":"shared-refresh"},"last_refresh":"2026-07-20T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cred, []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"`+secret+`","refresh_token":"local-refresh"},"last_refresh":"2026-07-21T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run(true)

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(logged)
	for _, want := range []string{
		"component=shared-auth",
		"event=reconcile_start",
		"event=winner_local_newer",
		"event=local_published",
		"state=local_before",
		"kind=non_symlink",
		"state=local_final",
		"kind=symlink",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("diagnostic log missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, secret) {
		t.Fatalf("diagnostic log leaked credential contents:\n%s", text)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("diagnostic log mode = %v, want 0600", info.Mode().Perm())
	}
}

// The reconciliation hook's core behaviors, driven for real: fresh box gets a
// dangling link; an existing per-project login is published; a newer local
// login replaces stale shared auth; a newer shared login beats a stale local
// copy; and the whole thing is idempotent.
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
	adopted := `{"auth_mode":"chatgpt","tokens":{"access_token":"adopted","refresh_token":"adopted-refresh"},"last_refresh":"2026-07-20T00:00:00Z"}`
	if err := os.WriteFile(cred, []byte(adopted), 0o600); err != nil {
		t.Fatal(err)
	}
	runCodexSharedAuthHook(t, base, home)
	if b, err := os.ReadFile(shared); err != nil || string(b) != adopted {
		t.Fatalf("existing login not adopted into the shared volume: %v %q", err, b)
	}
	if got, _ := os.Readlink(cred); got != shared {
		t.Fatalf("adopted cred not re-linked: %q", got)
	}

	// 3. A fresh login is local because Codex unlinks before login; newer local
	// auth must replace the now-revoked shared credential.
	if err := os.Remove(cred); err != nil {
		t.Fatal(err)
	}
	freshLocal := `{"auth_mode":"chatgpt","tokens":{"access_token":"fresh-local","refresh_token":"fresh-refresh"},"last_refresh":"2026-07-21T00:00:00Z"}`
	if err := os.WriteFile(cred, []byte(freshLocal), 0o600); err != nil {
		t.Fatal(err)
	}
	runCodexSharedAuthHook(t, base, home)
	if b, _ := os.ReadFile(shared); string(b) != freshLocal {
		t.Fatalf("newer local login not published: %q", b)
	}
	if got, _ := os.Readlink(cred); got != shared {
		t.Fatalf("fresh local login not re-linked: %q", got)
	}

	// 4. A stale local copy must not replace a newer shared credential.
	if err := os.Remove(cred); err != nil {
		t.Fatal(err)
	}
	staleLocal := `{"auth_mode":"chatgpt","tokens":{"access_token":"stale-local","refresh_token":"stale-refresh"},"last_refresh":"2026-07-19T00:00:00Z"}`
	if err := os.WriteFile(cred, []byte(staleLocal), 0o600); err != nil {
		t.Fatal(err)
	}
	runCodexSharedAuthHook(t, base, home)
	if b, _ := os.ReadFile(shared); string(b) != freshLocal {
		t.Fatalf("stale local login replaced newer shared auth: %q", b)
	}
	if got, _ := os.Readlink(cred); got != shared {
		t.Fatalf("stale local login not re-linked: %q", got)
	}

	// 5. Idempotent: run again, nothing changes.
	runCodexSharedAuthHook(t, base, home)
	if b, _ := os.ReadFile(cred); string(b) != freshLocal {
		t.Fatalf("idempotent re-run changed the credential: %q", b)
	}
}

func TestCodexSharedAuthMalformedLocalCannotReplaceShared(t *testing.T) {
	base, home := t.TempDir(), t.TempDir()
	shared := filepath.Join(base, "codex", "auth.json")
	cred := filepath.Join(home, "auth.json")
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	validShared := `{"auth_mode":"chatgpt","tokens":{"access_token":"valid","refresh_token":"valid-refresh"},"last_refresh":"2026-07-20T00:00:00Z"}`
	if err := os.WriteFile(shared, []byte(validShared), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cred, []byte(`{"tokens":`), 0o600); err != nil {
		t.Fatal(err)
	}

	runCodexSharedAuthHook(t, base, home)

	if got, err := os.ReadFile(shared); err != nil || string(got) != validShared {
		t.Fatalf("valid shared auth changed: %v %q", err, got)
	}
	if got, err := os.Readlink(cred); err != nil || got != shared {
		t.Fatalf("malformed local auth was not replaced by shared link: %q (%v)", got, err)
	}
}

func TestCodexSharedAuthHollowTokensCannotReplaceShared(t *testing.T) {
	base, home := t.TempDir(), t.TempDir()
	shared := filepath.Join(base, "codex", "auth.json")
	cred := filepath.Join(home, "auth.json")
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	validShared := `{"auth_mode":"chatgpt","tokens":{"access_token":"valid","refresh_token":"valid-refresh"},"last_refresh":"2026-07-20T00:00:00Z"}`
	if err := os.WriteFile(shared, []byte(validShared), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cred, []byte(`{"auth_mode":"chatgpt","tokens":{},"last_refresh":"2026-07-21T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	runCodexSharedAuthHook(t, base, home)

	if got, err := os.ReadFile(shared); err != nil || string(got) != validShared {
		t.Fatalf("hollow local tokens replaced valid shared auth: %v %q", err, got)
	}
	if got, err := os.Readlink(cred); err != nil || got != shared {
		t.Fatalf("hollow local auth was not replaced by shared link: %q (%v)", got, err)
	}
}

func TestCodexSharedAuthWhitespaceTokensCannotReplaceShared(t *testing.T) {
	base, home := t.TempDir(), t.TempDir()
	shared := filepath.Join(base, "codex", "auth.json")
	cred := filepath.Join(home, "auth.json")
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	validShared := `{"auth_mode":"chatgpt","tokens":{"access_token":"valid","refresh_token":"valid-refresh"},"last_refresh":"2026-07-20T00:00:00Z"}`
	if err := os.WriteFile(shared, []byte(validShared), 0o600); err != nil {
		t.Fatal(err)
	}
	blankLocal := `{"auth_mode":"chatgpt","tokens":{"access_token":" ","refresh_token":"   "},"last_refresh":"2026-07-21T00:00:00Z"}`
	if err := os.WriteFile(cred, []byte(blankLocal), 0o600); err != nil {
		t.Fatal(err)
	}

	runCodexSharedAuthHook(t, base, home)

	if got, err := os.ReadFile(shared); err != nil || string(got) != validShared {
		t.Fatalf("whitespace-only local tokens replaced valid shared auth: %v %q", err, got)
	}
}

func TestCodexSharedAuthPublishFailureReturnsNonzero(t *testing.T) {
	_, cat := testCat(t)
	reconcile := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "reconcile.sh")
	base, home := t.TempDir(), t.TempDir()
	identity := filepath.Join(base, "codex")
	if err := os.MkdirAll(identity, 0o755); err != nil {
		t.Fatal(err)
	}
	local := `{"auth_mode":"chatgpt","tokens":{"access_token":"local","refresh_token":"local-refresh"},"last_refresh":"2026-07-21T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(identity, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(identity, 0o700) })

	cmd := exec.Command("bash", reconcile, "test_publish_failure")
	cmd.Env = append(os.Environ(), "BYRE_IDENTITY_BASE="+base, "CODEX_HOME="+home)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("publish failure returned success:\n%s", out)
	}
	if !strings.Contains(string(out), "keeping the local login") {
		t.Fatalf("publish failure was not disclosed:\n%s", out)
	}
	if got, readErr := os.ReadFile(filepath.Join(home, "auth.json")); readErr != nil || string(got) != local {
		t.Fatalf("publish failure did not preserve local login: %v %q", readErr, got)
	}
}

func TestCodexSharedAuthMissingLocalNeverDeletesShared(t *testing.T) {
	base, home := t.TempDir(), t.TempDir()
	shared := filepath.Join(base, "codex", "auth.json")
	cred := filepath.Join(home, "auth.json")
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	validShared := `{"auth_mode":"chatgpt","tokens":{"access_token":"still-valid","refresh_token":"still-valid-refresh"},"last_refresh":"2026-07-20T00:00:00Z"}`
	if err := os.WriteFile(shared, []byte(validShared), 0o600); err != nil {
		t.Fatal(err)
	}

	runCodexSharedAuthHook(t, base, home)

	if got, err := os.ReadFile(shared); err != nil || string(got) != validShared {
		t.Fatalf("missing local path caused shared auth mutation: %v %q", err, got)
	}
	if got, err := os.Readlink(cred); err != nil || got != shared {
		t.Fatalf("missing local path did not become shared link: %q (%v)", got, err)
	}
}

func TestCodexSharedAuthMtimeFallbackForAPIKey(t *testing.T) {
	base, home := t.TempDir(), t.TempDir()
	shared := filepath.Join(base, "codex", "auth.json")
	cred := filepath.Join(home, "auth.json")
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"older"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(100, 0)
	if err := os.Chtimes(shared, old, old); err != nil {
		t.Fatal(err)
	}
	freshLocal := `{"auth_mode":"apikey","OPENAI_API_KEY":"newer"}`
	if err := os.WriteFile(cred, []byte(freshLocal), 0o600); err != nil {
		t.Fatal(err)
	}
	newer := time.Unix(200, 0)
	if err := os.Chtimes(cred, newer, newer); err != nil {
		t.Fatal(err)
	}

	runCodexSharedAuthHook(t, base, home)

	if got, err := os.ReadFile(shared); err != nil || string(got) != freshLocal {
		t.Fatalf("newer API-key auth did not win by mtime: %v %q", err, got)
	}
}

func TestCodexSharedAuthConcurrentPromotesKeepNewest(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "firstrun.sh")
	reconcile := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "reconcile.sh")
	base, homeOld, homeNew := t.TempDir(), t.TempDir(), t.TempDir()
	oldAuth := `{"auth_mode":"chatgpt","tokens":{"access_token":"old","refresh_token":"old-refresh"},"last_refresh":"2026-07-20T00:00:00Z"}`
	newAuth := `{"auth_mode":"chatgpt","tokens":{"access_token":"new","refresh_token":"new-refresh"},"last_refresh":"2026-07-21T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(homeOld, "auth.json"), []byte(oldAuth), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeNew, "auth.json"), []byte(newAuth), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(home string, done chan<- error) {
		cmd := exec.Command("bash", hook)
		cmd.Env = append(os.Environ(), "BYRE_IDENTITY_BASE="+base, "CODEX_HOME="+home,
			"BYRE_CODEX_AUTH_RECONCILE="+reconcile)
		_, err := cmd.CombinedOutput()
		done <- err
	}
	done := make(chan error, 2)
	go run(homeOld, done)
	go run(homeNew, done)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("concurrent reconciliation failed: %v", err)
		}
	}

	shared := filepath.Join(base, "codex", "auth.json")
	if got, err := os.ReadFile(shared); err != nil || string(got) != newAuth {
		t.Fatalf("concurrent reconciliation did not retain newest auth: %v %q", err, got)
	}
	for _, home := range []string{homeOld, homeNew} {
		if got, err := os.Readlink(filepath.Join(home, "auth.json")); err != nil || got != shared {
			t.Fatalf("%s not linked to shared auth: %q (%v)", home, got, err)
		}
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
// assertions below; the behavioral cases cover
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
		cmd := exec.Command("bash", hook)
		cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+codexHome,
			"BYRE_CODEX_AUTH_RECONCILE=/nonexistent")
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

func TestCodexLoginHookPublishesSuccessfulDeviceLogin(t *testing.T) {
	_, cat := testCat(t)
	loginHook := filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh")
	reconcile := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "reconcile.sh")
	base, home, bin := t.TempDir(), t.TempDir(), t.TempDir()
	shared := filepath.Join(base, "codex", "auth.json")
	freshAuth := `{"auth_mode":"chatgpt","tokens":{"access_token":"fresh","refresh_token":"fresh-refresh"},"last_refresh":"2026-07-21T00:00:00Z"}`

	stub := "#!/bin/sh\n" +
		"if [ \"$1 $2\" = 'login status' ]; then exit 1; fi\n" +
		"if [ \"$1 $2\" = 'login --device-auth' ]; then\n" +
		"  printf '%s' '" + freshAuth + "' > \"$CODEX_HOME/auth.json\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", loginHook)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":/usr/bin:/bin",
		"CODEX_HOME="+home,
		"BYRE_IDENTITY_BASE="+base,
		"BYRE_CODEX_AUTH_RECONCILE="+reconcile,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("login hook failed: %v (%s)", err, out)
	}
	if got, err := os.ReadFile(shared); err != nil || string(got) != freshAuth {
		t.Fatalf("successful device login was not published: %v %q", err, got)
	}
	if got, err := os.Readlink(filepath.Join(home, "auth.json")); err != nil || got != shared {
		t.Fatalf("successful device login was not re-linked: %q (%v)", got, err)
	}
}

func TestCodexLoginHookColdStartProbe(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh")

	tests := []struct {
		name            string
		accessToken     string
		lastRefresh     string
		appServerBody   string
		refreshesFile   bool
		wantProbe       bool
		wantLogin       bool
		wantWarning     bool
		wantUnconfirmed bool
	}{
		{
			name:        "fresh credential avoids network probe",
			lastRefresh: time.Now().UTC().Format(time.RFC3339),
		},
		{
			name:        "fresh jwt overrides old refresh timestamp",
			accessToken: "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":`+strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)+`}`)) + ".x",
			lastRefresh: "2020-01-01T00:00:00Z",
		},
		{
			name:          "expiring jwt requires probe",
			accessToken:   "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":`+strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10)+`}`)) + ".x",
			lastRefresh:   time.Now().UTC().Format(time.RFC3339),
			appServerBody: `{"id":1,"result":{"account":{"type":"chatgpt","email":"private@example.test","planType":"pro"},"requiresOpenaiAuth":true}}`,
			refreshesFile: true,
			wantProbe:     true,
		},
		{
			name:          "stale credential refreshes through app server",
			lastRefresh:   "2020-01-01T00:00:00.123456789Z",
			appServerBody: `{"id":1,"result":{"account":{"type":"chatgpt","email":"private@example.test","planType":"pro"},"requiresOpenaiAuth":true}}`,
			refreshesFile: true,
			wantProbe:     true,
		},
		{
			name:            "account present without persisted refresh warns but launches",
			lastRefresh:     "2020-01-01T00:00:00Z",
			appServerBody:   `{"id":1,"result":{"account":{"type":"chatgpt","email":"private@example.test","planType":"pro"},"requiresOpenaiAuth":true}}`,
			wantProbe:       true,
			wantUnconfirmed: true,
		},
		{
			name:        "ambiguous probe preserves credential",
			lastRefresh: "2020-01-01T00:00:00Z",
			wantProbe:   true,
			wantWarning: true,
		},
		{
			name:          "unavailable account starts recovery",
			lastRefresh:   "2020-01-01T00:00:00Z",
			appServerBody: `{"id":1,"result":{"account":null,"requiresOpenaiAuth":true}}`,
			wantProbe:     true,
			wantLogin:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home, bin := t.TempDir(), t.TempDir()
			writeCodexHookShims(t, bin)
			probeStamp := filepath.Join(home, "probe")
			loginStamp := filepath.Join(home, "login")
			accessToken := tt.accessToken
			if accessToken == "" {
				accessToken = "opaque"
			}
			auth := `{"auth_mode":"chatgpt","tokens":{"access_token":` + strconv.Quote(accessToken) + `,"refresh_token":"refresh"},"last_refresh":` + strconv.Quote(tt.lastRefresh) + `}`
			if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(auth), 0o600); err != nil {
				t.Fatal(err)
			}
			stub := "#!/bin/sh\n" +
				"if [ \"$1 $2\" = 'login status' ]; then exit 0; fi\n" +
				"if [ \"$1\" = app-server ]; then\n" +
				"  touch " + strconv.Quote(probeStamp) + "\n"
			if tt.refreshesFile {
				fresh := `{"auth_mode":"chatgpt","tokens":{"access_token":"opaque","refresh_token":"new-refresh"},"last_refresh":` + strconv.Quote(time.Now().UTC().Format(time.RFC3339)) + `}`
				stub += "  printf '%s' " + strconv.Quote(fresh) + " > \"$CODEX_HOME/auth.json\"\n"
			}
			if tt.appServerBody != "" {
				stub += "  printf '%s\\n' " + strconv.Quote(tt.appServerBody) + "\n"
			}
			stub += "  sleep 1; exit 0\nfi\n" +
				"if [ \"$1 $2\" = 'login --device-auth' ]; then touch " + strconv.Quote(loginStamp) + "; exit 0; fi\n" +
				"exit 1\n"
			if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("bash", hook)
			cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+home,
				"BYRE_CODEX_AUTH_RECONCILE=/nonexistent")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("hook failed: %v (%s)", err, out)
			}
			_, probeErr := os.Stat(probeStamp)
			if got := probeErr == nil; got != tt.wantProbe {
				t.Errorf("probe ran = %v, want %v (%s)", got, tt.wantProbe, out)
			}
			_, loginErr := os.Stat(loginStamp)
			if got := loginErr == nil; got != tt.wantLogin {
				t.Errorf("login ran = %v, want %v (%s)", got, tt.wantLogin, out)
			}
			if got := strings.Contains(string(out), "could not verify"); got != tt.wantWarning {
				t.Errorf("ambiguous warning = %v, want %v (%s)", got, tt.wantWarning, out)
			}
			if got := strings.Contains(string(out), "refresh was not persisted"); got != tt.wantUnconfirmed {
				t.Errorf("unconfirmed-refresh warning = %v, want %v (%s)", got, tt.wantUnconfirmed, out)
			}
			if strings.Contains(string(out), "private@example.test") {
				t.Fatalf("account email leaked from private RPC response: %s", out)
			}
		})
	}
}

func TestCodexLoginHookReapsAppServer(t *testing.T) {
	testtools.NeedTool(t, "ps")
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh")
	home, bin := t.TempDir(), t.TempDir()
	writeCodexHookShims(t, bin)
	auth := `{"auth_mode":"chatgpt","tokens":{"access_token":"opaque","refresh_token":"refresh"},"last_refresh":"2020-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(auth), 0o600); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(home, "app-server.pid")
	childPIDFile := filepath.Join(home, "app-server-child.pid")
	stub := "#!/bin/sh\n" +
		"if [ \"$1 $2\" = 'login status' ]; then exit 0; fi\n" +
		"if [ \"$1\" = app-server ]; then\n" +
		"  printf '%s' \"$$\" > " + strconv.Quote(pidFile) + "\n" +
		"  printf '%s\\n' '{\"id\":1,\"result\":{\"account\":{\"type\":\"chatgpt\"},\"requiresOpenaiAuth\":true}}'\n" +
		"  trap '' TERM\n" +
		"  sleep 1000 &\n" +
		"  printf '%s' \"$!\" > " + strconv.Quote(childPIDFile) + "\n" +
		"  wait\n" +
		"fi\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", hook)
	cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+home,
		"BYRE_CODEX_AUTH_RECONCILE=/nonexistent")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
	for _, pidPath := range []string{pidFile, childPIDFile} {
		b, err := os.ReadFile(pidPath)
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(string(b))
		if err != nil {
			t.Fatal(err)
		}
		processActive := func() bool {
			out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
			state := strings.TrimSpace(string(out))
			return err == nil && state != "" && state[0] != 'Z'
		}
		for i := 0; i < 20; i++ {
			if !processActive() {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if processActive() {
			t.Fatalf("app-server process %d from %s survived probe cleanup", pid, filepath.Base(pidPath))
		}
	}
}

func TestCodexLoginHookDetachesSharedLinkBeforeDeviceLogin(t *testing.T) {
	_, cat := testCat(t)
	src, err := os.ReadFile(filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh"))
	if err != nil {
		t.Fatal(err)
	}
	base, home, bin := t.TempDir(), t.TempDir(), t.TempDir()
	writeCodexHookShims(t, bin)
	identity := filepath.Join(base, "codex")
	if err := os.MkdirAll(identity, 0o700); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(identity, "auth.json")
	stale := `{"auth_mode":"chatgpt","tokens":{"access_token":"opaque","refresh_token":"dead"},"last_refresh":"2020-01-01T00:00:00Z"}`
	if err := os.WriteFile(shared, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(home, "auth.json")); err != nil {
		t.Fatal(err)
	}

	// The production trust root is intentionally not configurable. Materialize a
	// test-only copy with that one literal replaced so the destructive boundary
	// can be exercised without sharing /home/dev state between parallel tests.
	testSrc := strings.Replace(string(src), `/home/dev/.byre-identity/codex`, identity, 1)
	testHook := filepath.Join(t.TempDir(), "codex-login.sh")
	if err := os.WriteFile(testHook, []byte(testSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	loginStamp := filepath.Join(home, "safe-login")
	fresh := `{"auth_mode":"chatgpt","tokens":{"access_token":"opaque","refresh_token":"fresh"},"last_refresh":"2030-01-01T00:00:00Z"}`
	stub := "#!/bin/sh\n" +
		"if [ \"$1 $2\" = 'login status' ]; then exit 0; fi\n" +
		"if [ \"$1\" = app-server ]; then printf '%s\\n' '{\"id\":1,\"result\":{\"account\":null,\"requiresOpenaiAuth\":true}}'; sleep 1; exit 0; fi\n" +
		"if [ \"$1 $2\" = 'login --device-auth' ]; then\n" +
		"  test ! -e \"$CODEX_HOME/auth.json\" && test ! -L \"$CODEX_HOME/auth.json\" || exit 41\n" +
		"  test -s " + strconv.Quote(shared) + " || exit 42\n" +
		"  echo shared-login-stderr-visible >&2\n" +
		"  touch " + strconv.Quote(loginStamp) + "\n" +
		"  printf '%s' " + strconv.Quote(fresh) + " > \"$CODEX_HOME/auth.json\"\n" +
		"  exit 0\nfi\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	reconcile := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "reconcile.sh")
	cmd := exec.Command("bash", testHook)
	cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+home,
		"BYRE_IDENTITY_BASE="+base, "BYRE_CODEX_AUTH_RECONCILE="+reconcile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
	if !strings.Contains(string(out), "shared-login-stderr-visible") {
		t.Fatalf("shared lock setup swallowed later stderr: %s", out)
	}
	if _, err := os.Stat(loginStamp); err != nil {
		t.Fatalf("safe device login did not run: %v", err)
	}
	if got, err := os.ReadFile(shared); err != nil || string(got) != fresh {
		t.Fatalf("fresh login was not published safely: %v %q", err, got)
	}
	if got, err := os.Readlink(filepath.Join(home, "auth.json")); err != nil || got != shared {
		t.Fatalf("shared link was not restored: %q (%v)", got, err)
	}
}

func TestCodexLoginHookAdoptsDelayedSiblingRefresh(t *testing.T) {
	_, cat := testCat(t)
	src, err := os.ReadFile(filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh"))
	if err != nil {
		t.Fatal(err)
	}
	base, home, bin := t.TempDir(), t.TempDir(), t.TempDir()
	writeCodexHookShims(t, bin)
	identity := filepath.Join(base, "codex")
	if err := os.MkdirAll(identity, 0o700); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(identity, "auth.json")
	stale := `{"auth_mode":"chatgpt","tokens":{"access_token":"opaque","refresh_token":"loser"},"last_refresh":"2020-01-01T00:00:00Z"}`
	fresh := `{"auth_mode":"chatgpt","tokens":{"access_token":"opaque","refresh_token":"winner"},"last_refresh":"2030-01-01T00:00:00Z"}`
	if err := os.WriteFile(shared, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(home, "auth.json")); err != nil {
		t.Fatal(err)
	}
	testSrc := strings.Replace(string(src), `/home/dev/.byre-identity/codex`, identity, 1)
	testHook := filepath.Join(t.TempDir(), "codex-login.sh")
	if err := os.WriteFile(testHook, []byte(testSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	badLogin := filepath.Join(home, "login-must-not-run")
	stub := "#!/bin/sh\n" +
		"if [ \"$1 $2\" = 'login status' ]; then exit 0; fi\n" +
		"if [ \"$1\" = app-server ]; then\n" +
		"  (sleep 0.05; printf '%s' " + strconv.Quote(fresh) + " > " + strconv.Quote(shared) + ") &\n" +
		"  printf '%s\\n' '{\"id\":1,\"result\":{\"account\":null,\"requiresOpenaiAuth\":true}}'\n" +
		"  sleep 2; exit 0\nfi\n" +
		"if [ \"$1 $2\" = 'login --device-auth' ]; then touch " + strconv.Quote(badLogin) + "; exit 0; fi\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", testHook)
	cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+home,
		"BYRE_IDENTITY_BASE="+base, "CODEX_AUTH_DIAGNOSTIC_BYRE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
	if _, err := os.Stat(badLogin); !os.IsNotExist(err) {
		t.Fatalf("sibling refresh must suppress device login: %v", err)
	}
	if got, err := os.ReadFile(shared); err != nil || string(got) != fresh {
		t.Fatalf("sibling's refreshed credential was not retained: %v %q", err, got)
	}
	log, err := os.ReadFile(filepath.Join(identity, "byre-auth-diagnostic.log"))
	if err != nil || !strings.Contains(string(log), "live_probe_sibling_changed_credential") {
		t.Fatalf("sibling refresh was not classified: %v %s", err, log)
	}
}

func TestCodexLoginHookRestoresSharedLinkAfterFailedLogin(t *testing.T) {
	_, cat := testCat(t)
	src, err := os.ReadFile(filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh"))
	if err != nil {
		t.Fatal(err)
	}
	base, home, bin := t.TempDir(), t.TempDir(), t.TempDir()
	writeCodexHookShims(t, bin)
	identity := filepath.Join(base, "codex")
	if err := os.MkdirAll(identity, 0o700); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(identity, "auth.json")
	stale := `{"auth_mode":"chatgpt","tokens":{"access_token":"opaque","refresh_token":"dead"},"last_refresh":"2020-01-01T00:00:00Z"}`
	if err := os.WriteFile(shared, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	cred := filepath.Join(home, "auth.json")
	if err := os.Symlink(shared, cred); err != nil {
		t.Fatal(err)
	}
	testSrc := strings.Replace(string(src), `/home/dev/.byre-identity/codex`, identity, 1)
	testHook := filepath.Join(t.TempDir(), "codex-login.sh")
	if err := os.WriteFile(testHook, []byte(testSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\n" +
		"if [ \"$1 $2\" = 'login status' ]; then exit 0; fi\n" +
		"if [ \"$1\" = app-server ]; then printf '%s\\n' '{\"id\":1,\"result\":{\"account\":null,\"requiresOpenaiAuth\":true}}'; exit 0; fi\n" +
		"if [ \"$1 $2\" = 'login --device-auth' ]; then test ! -e \"$CODEX_HOME/auth.json\" && exit 1; fi\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", testHook)
	cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+home,
		"BYRE_IDENTITY_BASE="+base, "BYRE_CODEX_AUTH_RECONCILE=/nonexistent")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
	if got, err := os.Readlink(cred); err != nil || got != shared {
		t.Fatalf("failed device login did not restore shared link: %q (%v)", got, err)
	}
	if got, err := os.ReadFile(shared); err != nil || string(got) != stale {
		t.Fatalf("failed device login changed shared credential: %v %q", err, got)
	}
}
