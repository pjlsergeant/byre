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
	"syscall"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
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
		"BYRE_CODEX_AUTH_RECONCILE="+reconcile, sharedAuthLibSeam(t, cat, "codex-shared-auth"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
}

// codexLoginHookEnv is the environment every login-hook invocation uses.
// Reap grace is shortened so live_probe's TERM/KILL escalation does not spend
// two wall-clock seconds per call; production still defaults to 1.
func codexLoginHookEnv(t *testing.T, cat *packages.Catalog, extra ...string) []string {
	t.Helper()
	return append(append(os.Environ(), extra...),
		"BYRE_CODEX_REAP_GRACE=0.05", sharedAuthLibSeam(t, cat, "codex"))
}

// codexReconcileEnv is the environment a direct reconcile.sh or firstrun-hook
// invocation runs under: the identity-base and CODEX_HOME seams, plus the
// shared-auth library the companion skill ships (a box's copy lives at
// /usr/local/lib).
func codexReconcileEnv(t *testing.T, cat *packages.Catalog, base, home string, extra ...string) []string {
	t.Helper()
	return append(append(os.Environ(), "BYRE_IDENTITY_BASE="+base, "CODEX_HOME="+home,
		sharedAuthLibSeam(t, cat, "codex-shared-auth")), extra...)
}

// writeCodexSetsidShim supplies setsid on macOS; Linux uses util-linux.
func writeCodexSetsidShim(t *testing.T, bin string) {
	t.Helper()
	testtools.NeedTool(t, "bash", "jq", "date")
	if runtime.GOOS != "darwin" {
		testtools.NeedTool(t, "setsid")
		return
	}
	testtools.NeedTool(t, "perl")
	setsid := "#!/bin/sh\nexec perl -MPOSIX -e 'POSIX::setsid(); exec @ARGV' -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "setsid"), []byte(setsid), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Shared-auth lock tests require util-linux flock, which macOS CI lacks.
func needCodexFlock(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("shared Codex auth hook requires util-linux flock")
	}
	testtools.NeedTool(t, "flock")
}

// Diagnostics are strictly opt-in and record lifecycle/file metadata without
// copying credential material into the shared log.
func TestCodexSharedAuthDiagnosticsAreGatedAndRedacted(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "firstrun.sh")
	reconcile := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "reconcile.sh")
	base, home := physTempDir(t), physTempDir(t)
	shared := filepath.Join(base, "codex", "auth.json")
	cred := filepath.Join(home, "auth.json")
	logPath := filepath.Join(base, "codex", "byre-auth-diagnostic.log")
	const secret = "must-not-appear-in-diagnostics"

	run := func(enabled bool) {
		t.Helper()
		cmd := exec.Command("bash", hook)
		cmd.Env = codexReconcileEnv(t, cat, base, home, "BYRE_CODEX_AUTH_RECONCILE="+reconcile)
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
	base, home := physTempDir(t), physTempDir(t)
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
	base, home := physTempDir(t), physTempDir(t)
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
	base, home := physTempDir(t), physTempDir(t)
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
	base, home := physTempDir(t), physTempDir(t)
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
	base, home := physTempDir(t), physTempDir(t)
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
	cmd.Env = codexReconcileEnv(t, cat, base, home)
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
	base, home := physTempDir(t), physTempDir(t)
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

// The link step: a link to "$SHARED<newline>" -- a different file, which
// $(readlink) compares equal to "$SHARED" -- is re-asserted to the clean
// path; and a symlink planted AT the shared path is refused, the box's link
// and the planted target both untouched. Containment cases (CLAUDE.md, two
// tiers): refusal plus the unchanged victim, no message fragments.
func TestCodexSharedAuthAssertLinkHardening(t *testing.T) {
	validShared := `{"auth_mode":"chatgpt","tokens":{"access_token":"a","refresh_token":"r"},"last_refresh":"2026-07-20T00:00:00Z"}`

	base, home := physTempDir(t), physTempDir(t)
	shared := filepath.Join(base, "codex", "auth.json")
	cred := filepath.Join(home, "auth.json")
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte(validShared), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared+"\n", cred); err != nil {
		t.Fatal(err)
	}
	runCodexSharedAuthHook(t, base, home)
	if got, err := os.Readlink(cred); err != nil || got != shared {
		t.Fatalf("a newline-suffixed link must be re-asserted to %q, got %q (%v)", shared, got, err)
	}

	base2, home2 := physTempDir(t), physTempDir(t)
	shared2 := filepath.Join(base2, "codex", "auth.json")
	cred2 := filepath.Join(home2, "auth.json")
	if err := os.MkdirAll(filepath.Dir(shared2), 0o755); err != nil {
		t.Fatal(err)
	}
	decoy := filepath.Join(physTempDir(t), "decoy.json")
	if err := os.WriteFile(decoy, []byte(validShared), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(decoy, shared2); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home2, "other.json")
	if err := os.Symlink(other, cred2); err != nil {
		t.Fatal(err)
	}
	runCodexSharedAuthHook(t, base2, home2)
	if got, err := os.Readlink(cred2); err != nil || got != other {
		t.Fatalf("a planted shared symlink must be refused, the local path untouched: %q (%v)", got, err)
	}
	if fi, err := os.Stat(decoy); err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("the planted link's target was touched: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(decoy); string(b) != validShared {
		t.Fatalf("the planted link's target was written: %q", b)
	}

	// The publish path's keep-a-copy step: with a valid local login and a
	// symlink planted at the shared path, the old `[ -f "$SHARED" ] && cp`
	// followed the link and copied the planter's target into auth.json.prev
	// on the shared volume. Refused before anything is staged: no .prev, no
	// publish over the planted link, the local login and the decoy as they
	// were.
	_, cat := testCat(t)
	reconcile := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "reconcile.sh")
	base3, home3 := physTempDir(t), physTempDir(t)
	identity3 := filepath.Join(base3, "codex")
	shared3 := filepath.Join(identity3, "auth.json")
	cred3 := filepath.Join(home3, "auth.json")
	if err := os.MkdirAll(identity3, 0o755); err != nil {
		t.Fatal(err)
	}
	decoy3 := filepath.Join(physTempDir(t), "decoy-secret")
	const decoyBody = "not-yours-to-copy\n"
	if err := os.WriteFile(decoy3, []byte(decoyBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(decoy3, shared3); err != nil {
		t.Fatal(err)
	}
	local3 := `{"auth_mode":"chatgpt","tokens":{"access_token":"l","refresh_token":"lr"},"last_refresh":"2026-07-21T00:00:00Z"}`
	if err := os.WriteFile(cred3, []byte(local3), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", reconcile, "test_planted_shared_publish")
	cmd.Env = codexReconcileEnv(t, cat, base3, home3)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("publishing over a planted shared symlink must be refused, got success:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(identity3, "auth.json.prev")); !os.IsNotExist(err) {
		t.Fatalf("auth.json.prev must not be created from a planted shared symlink (lstat err %v)", err)
	}
	if b, _ := os.ReadFile(decoy3); string(b) != decoyBody {
		t.Fatalf("the planted link's target was written: %q", b)
	}
	if got, err := os.Readlink(shared3); err != nil || got != decoy3 {
		t.Fatalf("the planted shared link must be left as found (not published over): %q (%v)", got, err)
	}
	if fi, err := os.Lstat(cred3); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("the local login must stay a regular file: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(cred3); string(b) != local3 {
		t.Fatalf("the local login was changed: %q", b)
	}
}

func TestCodexSharedAuthMtimeFallbackForAPIKey(t *testing.T) {
	base, home := physTempDir(t), physTempDir(t)
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
	needCodexFlock(t)
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "firstrun.sh")
	reconcile := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "reconcile.sh")
	base, homeOld, homeNew := physTempDir(t), physTempDir(t), physTempDir(t)
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
		cmd.Env = codexReconcileEnv(t, cat, base, home, "BYRE_CODEX_AUTH_RECONCILE="+reconcile)
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

// A held machine-shared lock must skip reconciliation (bounded wait, warn,
// nonzero), never hang the launch and never touch either credential: the lock
// file is dev-writable in every box, so an agent can hold it indefinitely.
func TestCodexSharedAuthLockTimeoutSkipsReconciliation(t *testing.T) {
	needCodexFlock(t)
	_, cat := testCat(t)
	reconcile := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "reconcile.sh")
	base, home := physTempDir(t), physTempDir(t)
	identity := filepath.Join(base, "codex")
	if err := os.MkdirAll(identity, 0o755); err != nil {
		t.Fatal(err)
	}
	oldShared := `{"auth_mode":"chatgpt","tokens":{"access_token":"old","refresh_token":"old-refresh"},"last_refresh":"2026-07-20T00:00:00Z"}`
	newLocal := `{"auth_mode":"chatgpt","tokens":{"access_token":"new","refresh_token":"new-refresh"},"last_refresh":"2026-07-21T00:00:00Z"}`
	shared := filepath.Join(identity, "auth.json")
	cred := filepath.Join(home, "auth.json")
	if err := os.WriteFile(shared, []byte(oldShared), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cred, []byte(newLocal), 0o600); err != nil {
		t.Fatal(err)
	}

	lock := filepath.Join(identity, "auth.lock")
	holder := exec.Command("flock", lock, "sleep", "60")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = holder.Process.Kill()
		_ = holder.Wait()
	})
	held := false
	for range 200 {
		if exec.Command("flock", "-n", lock, "true").Run() != nil {
			held = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !held {
		t.Fatal("could not observe the holder taking the lock")
	}

	cmd := exec.Command("bash", reconcile, "test_lock_timeout")
	cmd.Env = codexReconcileEnv(t, cat, base, home)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("held lock did not produce a nonzero skip:\n%s", out)
	}
	if !strings.Contains(string(out), "skipped credential reconciliation") {
		t.Fatalf("timeout skip was not disclosed:\n%s", out)
	}
	// The remedy must be stop-the-holder, never rm: flock is inode-scoped,
	// so removing the path would let the next launch reconcile unserialized
	// beside the still-live holder.
	if !strings.Contains(string(out), "stop the box") {
		t.Fatalf("timeout warning does not name the stop-the-holder remedy:\n%s", out)
	}
	if strings.Contains(string(out), "rm ") {
		t.Fatalf("timeout warning offers a removal remedy that would split-brain the lock:\n%s", out)
	}
	if got, readErr := os.ReadFile(shared); readErr != nil || string(got) != oldShared {
		t.Fatalf("timeout skip mutated shared auth: %v %q", readErr, got)
	}
	if fi, statErr := os.Lstat(cred); statErr != nil || !fi.Mode().IsRegular() {
		t.Fatalf("timeout skip replaced the local credential: %v", statErr)
	}
	if got, readErr := os.ReadFile(cred); readErr != nil || string(got) != newLocal {
		t.Fatalf("timeout skip mutated local auth: %v %q", readErr, got)
	}
}

// A planted non-regular auth.lock must not hang or divert the reconciler:
// a FIFO would block the write-open before any flock timeout applies, and a
// symlink would truncate its target. Both are replaced with a fresh lock.
func TestCodexSharedAuthNonRegularLockIsReplaced(t *testing.T) {
	needCodexFlock(t)
	testtools.NeedTool(t, "mkfifo")
	_, cat := testCat(t)
	reconcile := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "reconcile.sh")

	for name, plant := range map[string]func(t *testing.T, lock, victim string){
		"fifo": func(t *testing.T, lock, _ string) {
			if err := exec.Command("mkfifo", lock).Run(); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, lock, victim string) {
			if err := os.Symlink(victim, lock); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			base, home := physTempDir(t), physTempDir(t)
			identity := filepath.Join(base, "codex")
			if err := os.MkdirAll(identity, 0o755); err != nil {
				t.Fatal(err)
			}
			auth := `{"auth_mode":"chatgpt","tokens":{"access_token":"a","refresh_token":"r"},"last_refresh":"2026-07-21T00:00:00Z"}`
			if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(auth), 0o600); err != nil {
				t.Fatal(err)
			}
			victim := filepath.Join(base, "victim")
			if err := os.WriteFile(victim, []byte("victim-bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			plant(t, filepath.Join(identity, "auth.lock"), victim)

			cmd := exec.Command("timeout", "20", "bash", reconcile, "test_nonregular_lock")
			cmd.Env = codexReconcileEnv(t, cat, base, home)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("reconcile with a planted %s lock failed (a hang shows as timeout exit 124): %v\n%s", name, err, out)
			}
			if fi, statErr := os.Lstat(filepath.Join(identity, "auth.lock")); statErr != nil || !fi.Mode().IsRegular() {
				t.Fatalf("planted %s lock was not replaced with a regular file: %v", name, statErr)
			}
			if got, readErr := os.ReadFile(victim); readErr != nil || string(got) != "victim-bytes" {
				t.Fatalf("planted %s lock diverted a write to its target: %v %q", name, readErr, got)
			}
			if got, readErr := os.ReadFile(filepath.Join(identity, "auth.json")); readErr != nil || string(got) != auth {
				t.Fatalf("reconciliation did not still publish through the fresh lock: %v %q", readErr, got)
			}
		})
	}
}

// A planted auth.json.prev symlink must not receive the retention write
// through it: retention stages temp+rename like publish, so the entry is
// replaced and the plant's target never sees credential bytes. (A planted
// FIFO is covered by the same rename — mv -f replaces the dirent.)
func TestCodexSharedAuthRetentionDoesNotFollowPlantedPrev(t *testing.T) {
	base, home := physTempDir(t), physTempDir(t)
	identity := filepath.Join(base, "codex")
	shared := filepath.Join(identity, "auth.json")
	if err := os.MkdirAll(identity, 0o755); err != nil {
		t.Fatal(err)
	}
	oldShared := `{"auth_mode":"chatgpt","tokens":{"access_token":"old","refresh_token":"old-refresh"},"last_refresh":"2026-07-20T00:00:00Z"}`
	newLocal := `{"auth_mode":"chatgpt","tokens":{"access_token":"new","refresh_token":"new-refresh"},"last_refresh":"2026-07-21T00:00:00Z"}`
	if err := os.WriteFile(shared, []byte(oldShared), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(newLocal), 0o600); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(base, "victim")
	if err := os.WriteFile(victim, []byte("victim-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	prev := filepath.Join(identity, "auth.json.prev")
	if err := os.Symlink(victim, prev); err != nil {
		t.Fatal(err)
	}

	runCodexSharedAuthHook(t, base, home)

	if got, err := os.ReadFile(victim); err != nil || string(got) != "victim-bytes" {
		t.Fatalf("retention wrote through the planted prev symlink: %v %q", err, got)
	}
	if fi, err := os.Lstat(prev); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("planted prev entry was not replaced by a regular file: %v", err)
	}
	if got, err := os.ReadFile(prev); err != nil || string(got) != oldShared {
		t.Fatalf("displaced shared bytes not retained: %v %q", err, got)
	}
}

// Publishing a winner over an existing shared credential must retain the
// displaced bytes as auth.json.prev (0600): a wrong winner pick is then a
// file restore, not a re-login on every box sharing the credential.
func TestCodexSharedAuthPublishRetainsDisplacedShared(t *testing.T) {
	base, home := physTempDir(t), physTempDir(t)
	shared := filepath.Join(base, "codex", "auth.json")
	cred := filepath.Join(home, "auth.json")
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	oldShared := `{"auth_mode":"chatgpt","tokens":{"access_token":"old","refresh_token":"old-refresh"},"last_refresh":"2026-07-20T00:00:00Z"}`
	newLocal := `{"auth_mode":"chatgpt","tokens":{"access_token":"new","refresh_token":"new-refresh"},"last_refresh":"2026-07-21T00:00:00Z"}`
	if err := os.WriteFile(shared, []byte(oldShared), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cred, []byte(newLocal), 0o600); err != nil {
		t.Fatal(err)
	}

	runCodexSharedAuthHook(t, base, home)

	if got, err := os.ReadFile(shared); err != nil || string(got) != newLocal {
		t.Fatalf("newer local login not published: %v %q", err, got)
	}
	prev := filepath.Join(base, "codex", "auth.json.prev")
	got, err := os.ReadFile(prev)
	if err != nil || string(got) != oldShared {
		t.Fatalf("displaced shared bytes not retained in %s: %v %q", prev, err, got)
	}
	if fi, err := os.Stat(prev); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("retained credential is not 0600: %v %v", fi.Mode(), err)
	}
}

// The publish rename cannot take GNU's -T (the macOS CI leg runs BSD mv), so a
// directory that races in between the shared-path check and the rename RECEIVES
// the staged login instead of failing -- and so does a SYMLINK to a directory,
// which mv follows, putting this box's login wherever on the identity volume
// the link points. The refusal was already right in both cases; what was wrong
// is that the login then STAYED there, mode 600, on a volume every sibling box
// reads, under a message saying the credential stayed local. A `cp` stub plants
// the object in the window the real race would use -- the hook refuses a
// directory or a symlink that is already there long before the publish, so a
// race is the only way in.
func TestCodexSharedAuthPublishLeavesNothingInARacedInDirectory(t *testing.T) {
	testtools.NeedTool(t, "bash", "jq")
	_, cat := testCat(t)
	reconcile := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "reconcile.sh")
	realCp, err := exec.LookPath("cp")
	if err != nil {
		t.Skip("no cp on PATH")
	}
	local := `{"auth_mode":"chatgpt","tokens":{"access_token":"a","refresh_token":"r"},"last_refresh":"2026-07-21T00:00:00Z"}`
	for _, tc := range []struct {
		name string
		// plant is the shell the stubbed cp runs after copying, in the window
		// between the shared-path check and the rename; it returns the directory
		// that must be empty afterwards.
		plant func(t *testing.T, shared, scratch string) (string, string)
	}{
		{"directory", func(t *testing.T, shared, _ string) (string, string) {
			return "mkdir -p -- " + strconv.Quote(shared) + "\n", shared
		}},
		{"symlink to a directory", func(t *testing.T, shared, scratch string) (string, string) {
			if err := os.MkdirAll(scratch, 0o755); err != nil {
				t.Fatal(err)
			}
			return "ln -s -- " + strconv.Quote(scratch) + " " + strconv.Quote(shared) + "\n", scratch
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, home, bin := physTempDir(t), physTempDir(t), physTempDir(t)
			shared := filepath.Join(base, "codex", "auth.json")
			cred := filepath.Join(home, "auth.json")
			if err := os.WriteFile(cred, []byte(local), 0o600); err != nil {
				t.Fatal(err)
			}
			plant, mustBeEmpty := tc.plant(t, shared, filepath.Join(base, "codex", "planted-dir"))
			stub := "#!/bin/sh\n" + realCp + " \"$@\" || exit 1\n" + plant + "exit 0\n"
			if err := os.WriteFile(filepath.Join(bin, "cp"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", reconcile, "test_raced_in_directory")
			cmd.Env = codexReconcileEnv(t, cat, base, home, "PATH="+bin+":"+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("publishing into a raced-in directory must be refused, got success:\n%s", out)
			}
			if ents, err := os.ReadDir(mustBeEmpty); err != nil || len(ents) != 0 {
				t.Fatalf("the refused publish left this box's login in %s: %v (%v)\n%s", mustBeEmpty, ents, err, out)
			}
			isRegularWith(t, cred, local)
		})
	}
}

// TestCodexLoginHookRejectsForeignSymlink: a symlinked credential whose
// target is not codex's own shared one is removed before anything reads or
// writes through it, and a link the hook cannot remove stops the hook instead
// of falling through to a login. The trusted target is the HARDCODED full path
// /home/dev/.byre-identity/codex/auth.json, so every temp-dir link here is
// foreign; the carve-out itself is driven against a rewritten copy of the hook
// in TestCodexLoginHookTrustsOnlyItsOwnIdentityLink.
func TestCodexLoginHookRejectsForeignSymlink(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh")

	bin := physTempDir(t)
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
		cmd.Env = codexLoginHookEnv(t, cat, "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+codexHome,
			"BYRE_CODEX_AUTH_RECONCILE=/nonexistent")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v (%s)", err, out)
		}
	}

	// A FOREIGN symlinked credential (temp-dir target) is removed; a fresh
	// login runs.
	home := physTempDir(t)
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

	// A foreign link the hook cannot drop (read-only CODEX_HOME) must not
	// fall through to a status probe or login that would go THROUGH it.
	if os.Geteuid() != 0 { // root ignores the mode bits this case relies on
		_ = os.Remove(stamp)
		roHome := physTempDir(t)
		victim := filepath.Join(physTempDir(t), "victim.json")
		if err := os.WriteFile(victim, []byte("victim"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(roHome, "auth.json")); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(roHome, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(roHome, 0o755) })
		run(roHome)
		if loginAttempted() {
			t.Fatal("a foreign link that could not be removed must not lead to a login")
		}
		if b, err := os.ReadFile(victim); err != nil || string(b) != "victim" {
			t.Fatalf("the link's target must be untouched: %v %q", err, b)
		}
	}

	// A logged-in codex (login status = 0) short-circuits: no login attempted.
	_ = os.Remove(stamp)
	if err := os.WriteFile(filepath.Join(bin, "codex"),
		[]byte("#!/bin/sh\ntest \"$1 $2\" = 'login status' && exit 0\ntouch "+stamp+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	home2 := physTempDir(t)
	if err := os.WriteFile(filepath.Join(home2, "auth.json"), []byte(`{"tokens":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run(home2)
	if loginAttempted() {
		t.Fatal("a logged-in codex must short-circuit the login; one was attempted")
	}
}

// The trusted-link carve-out: the hook accepts codex-shared-auth's own link
// into ITS identity dir (dangling included -- the first-login state) and
// removes every other symlink, because `codex login status`, the live probe
// and a login all read and write THROUGH the link. The trust root is
// deliberately hardcoded (an env seam there would let a config-supplied [env]
// var redefine the trusted namespace), so the only way to exercise the
// acceptance is a copy of the hook with that literal rewritten. All three
// halves of the conjunction are driven here: the identity dir, the auth.json
// basename, and the target object being absent or a regular file -- a link
// planted AT the shared auth.json would chain a login's write onward to a
// file of the planter's choosing. A wildcard over the identity base would
// trust a link into a SIBLING agent's dir, through which a `codex login` would
// overwrite that agent's machine-wide credential.
func TestCodexLoginHookTrustsOnlyItsOwnIdentityLink(t *testing.T) {
	testtools.NeedTool(t, "bash")
	_, cat := testCat(t)
	src, err := os.ReadFile(filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh"))
	if err != nil {
		t.Fatal(err)
	}
	bin := physTempDir(t)
	stamp := filepath.Join(bin, "login-attempted")
	// `login status` reports logged in, so the hook judges the link and then
	// short-circuits: no login, and whatever it decided about the link stands.
	if err := os.WriteFile(filepath.Join(bin, "codex"),
		[]byte("#!/bin/sh\ncase \"$1 $2\" in 'login status') exit 0 ;; esac\ntouch "+stamp+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		target func(identity, elsewhere string) string
		plant  func(t *testing.T, target string)
		kept   bool
	}{
		{"dangling link into the identity dir is kept", func(id, _ string) string {
			return filepath.Join(id, "auth.json")
		}, nil, true},
		{"link to a regular shared credential is kept", func(id, _ string) string {
			return filepath.Join(id, "auth.json")
		}, func(t *testing.T, target string) {
			if err := os.WriteFile(target, []byte(`{"tokens":{"access_token":"shared"}}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"link whose shared end is itself a symlink is dropped", func(id, _ string) string {
			return filepath.Join(id, "auth.json")
		}, func(t *testing.T, target string) {
			if err := os.Symlink(filepath.Join(filepath.Dir(target), "chained.json"), target); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"link to another name in the identity dir is dropped", func(id, _ string) string {
			return filepath.Join(id, "other.json")
		}, nil, false},
		{"link outside the identity dir is dropped", func(_, elsewhere string) string {
			return filepath.Join(elsewhere, "auth.json")
		}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, home, elsewhere := physTempDir(t), physTempDir(t), physTempDir(t)
			identity := filepath.Join(base, "codex")
			if err := os.MkdirAll(identity, 0o700); err != nil {
				t.Fatal(err)
			}
			hook := filepath.Join(physTempDir(t), "codex-login.sh")
			if err := os.WriteFile(hook,
				[]byte(strings.ReplaceAll(string(src), "/home/dev/.byre-identity/codex", identity)), 0o755); err != nil {
				t.Fatal(err)
			}
			target := tc.target(identity, elsewhere)
			if tc.plant != nil {
				tc.plant(t, target)
			}
			cred := filepath.Join(home, "auth.json")
			if err := os.Symlink(target, cred); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", hook)
			cmd.Env = codexLoginHookEnv(t, cat, "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+home,
				"BYRE_IDENTITY_BASE="+base, "BYRE_CODEX_AUTH_RECONCILE=/nonexistent")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("hook failed: %v (%s)", err, out)
			}
			if _, err := os.Stat(stamp); err == nil {
				t.Fatal("a logged-in codex must not be offered a login")
			}
			got, err := os.Readlink(cred)
			if tc.kept && (err != nil || got != target) {
				t.Fatalf("the trusted link must be kept, got %q (%v)", got, err)
			}
			if !tc.kept {
				if _, err := os.Lstat(cred); !os.IsNotExist(err) {
					t.Fatalf("an untrusted link must be removed, still present (%v)", err)
				}
			}
		})
	}
}

// A FIFO (or any other non-regular object) at the credential path is not a
// credential codex wrote: the hook stops before `codex login status`, jq or
// the live probe open it -- opening a FIFO blocks -- and runs no codex at
// all. Containment: no codex invocation, exit 0, the object left alone.
func TestCodexLoginHookRefusesNonRegularCredential(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh")
	testtools.NeedTool(t, "bash")
	bin := physTempDir(t)
	marker := filepath.Join(bin, "codex-ran")
	if err := os.WriteFile(filepath.Join(bin, "codex"),
		[]byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := physTempDir(t)
	fifo := filepath.Join(home, "auth.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	runHook(t, "bash", hook, "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+home,
		"BYRE_CODEX_AUTH_RECONCILE=/nonexistent", "BYRE_CODEX_REAP_GRACE=0.05",
		sharedAuthLibSeam(t, cat, "codex"))
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a FIFO at the credential path must not lead to any codex invocation")
	}
	if fi, err := os.Lstat(fifo); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("the FIFO must be left alone: %v %v", fi, err)
	}
}

func TestCodexLoginHookPublishesSuccessfulDeviceLogin(t *testing.T) {
	_, cat := testCat(t)
	loginHook := filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh")
	reconcile := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "reconcile.sh")
	base, home, bin := physTempDir(t), physTempDir(t), physTempDir(t)
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
	cmd.Env = codexLoginHookEnv(t, cat,
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
			t.Parallel()
			home, bin := physTempDir(t), physTempDir(t)
			writeCodexSetsidShim(t, bin)
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
			stub += "  sleep 0.1; exit 0\nfi\n" +
				"if [ \"$1 $2\" = 'login --device-auth' ]; then touch " + strconv.Quote(loginStamp) + "; exit 0; fi\n" +
				"exit 1\n"
			if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("bash", hook)
			cmd.Env = codexLoginHookEnv(t, cat, "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+home,
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
	home, bin := physTempDir(t), physTempDir(t)
	writeCodexSetsidShim(t, bin)
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
	cmd.Env = codexLoginHookEnv(t, cat, "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+home,
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
	needCodexFlock(t)
	_, cat := testCat(t)
	src, err := os.ReadFile(filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh"))
	if err != nil {
		t.Fatal(err)
	}
	base, home, bin := physTempDir(t), physTempDir(t), physTempDir(t)
	writeCodexSetsidShim(t, bin)
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
	// test-only copy with EVERY occurrence of that literal replaced -- it
	// appears twice, as the predicate's dir and as its tfile -- so the
	// destructive boundary can be exercised without sharing /home/dev state
	// between parallel tests.
	testSrc := strings.ReplaceAll(string(src), `/home/dev/.byre-identity/codex`, identity)
	testHook := filepath.Join(physTempDir(t), "codex-login.sh")
	if err := os.WriteFile(testHook, []byte(testSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	loginStamp := filepath.Join(home, "safe-login")
	fresh := `{"auth_mode":"chatgpt","tokens":{"access_token":"opaque","refresh_token":"fresh"},"last_refresh":"2030-01-01T00:00:00Z"}`
	stub := "#!/bin/sh\n" +
		"if [ \"$1 $2\" = 'login status' ]; then exit 0; fi\n" +
		"if [ \"$1\" = app-server ]; then printf '%s\\n' '{\"id\":1,\"result\":{\"account\":null,\"requiresOpenaiAuth\":true}}'; sleep 0.1; exit 0; fi\n" +
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
	cmd.Env = codexLoginHookEnv(t, cat, "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+home,
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

// The accepted shared link is judged by its TARGET, but the shared-auth path
// then works by the identity dir's SPELLING (mkdir -p, the auth.lock open,
// the diagnostic log). An identity base that resolves through a symlink must
// not get a dir, lock or log created on the far side: the hook drops shared
// auth and its link with one stderr line and goes on per-project -- the
// device login still runs and writes a LOCAL regular file, and the shared
// credential is not touched.
func TestCodexLoginHookRefusesSymlinkedIdentityDir(t *testing.T) {
	testtools.NeedTool(t, "bash", "jq")
	_, cat := testCat(t)
	src, err := os.ReadFile(filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh"))
	if err != nil {
		t.Fatal(err)
	}
	realBase, home, bin := physTempDir(t), physTempDir(t), physTempDir(t)
	identity := filepath.Join(realBase, "codex")
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
	// The seam spells the identity base through a symlink to the real one:
	// the link's target still resolves to the (test) trust root, so only the
	// spelling check stands between the hook and the far side.
	linkBase := filepath.Join(physTempDir(t), "base-link")
	if err := os.Symlink(realBase, linkBase); err != nil {
		t.Fatal(err)
	}
	testSrc := strings.ReplaceAll(string(src), `/home/dev/.byre-identity/codex`, identity)
	testHook := filepath.Join(physTempDir(t), "codex-login.sh")
	if err := os.WriteFile(testHook, []byte(testSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	loginStamp := filepath.Join(home, "login-ran")
	fresh := `{"auth_mode":"chatgpt","tokens":{"access_token":"opaque","refresh_token":"fresh"},"last_refresh":"2030-01-01T00:00:00Z"}`
	stub := "#!/bin/sh\n" +
		"if [ \"$1 $2\" = 'login status' ]; then test -e \"$CODEX_HOME/auth.json\"; exit $?; fi\n" +
		"if [ \"$1 $2\" = 'login --device-auth' ]; then\n" +
		"  test ! -e \"$CODEX_HOME/auth.json\" && test ! -L \"$CODEX_HOME/auth.json\" || exit 41\n" +
		"  touch " + strconv.Quote(loginStamp) + "\n" +
		"  printf '%s' " + strconv.Quote(fresh) + " > \"$CODEX_HOME/auth.json\"\n" +
		"  exit 0\nfi\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", testHook)
	cmd.Env = codexLoginHookEnv(t, cat, "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+home,
		"BYRE_IDENTITY_BASE="+linkBase, "CODEX_AUTH_DIAGNOSTIC_BYRE=1",
		"BYRE_CODEX_AUTH_RECONCILE="+filepath.Join(physTempDir(t), "no-reconcile"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
	// Containment test: the refusal (nothing created through the link, the
	// login landing local) and the untouched shared file are the contract;
	// the stderr wording is not pinned (CLAUDE.md, two tiers).
	for _, name := range []string{"auth.lock", "byre-auth-diagnostic.log"} {
		if _, err := os.Lstat(filepath.Join(identity, name)); !os.IsNotExist(err) {
			t.Fatalf("%s was created through the symlinked identity base (%v)", name, err)
		}
	}
	if _, err := os.Stat(loginStamp); err != nil {
		t.Fatalf("the per-project device login must still run: %v (%s)", err, out)
	}
	fi, err := os.Lstat(filepath.Join(home, "auth.json"))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("the login must land as a local regular file: %v %v", fi, err)
	}
	if got, err := os.ReadFile(shared); err != nil || string(got) != stale {
		t.Fatalf("the shared credential must be untouched: %v %q", err, got)
	}
}

func TestCodexLoginHookAdoptsDelayedSiblingRefresh(t *testing.T) {
	needCodexFlock(t)
	_, cat := testCat(t)
	src, err := os.ReadFile(filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh"))
	if err != nil {
		t.Fatal(err)
	}
	base, home, bin := physTempDir(t), physTempDir(t), physTempDir(t)
	writeCodexSetsidShim(t, bin)
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
	testSrc := strings.ReplaceAll(string(src), `/home/dev/.byre-identity/codex`, identity)
	testHook := filepath.Join(physTempDir(t), "codex-login.sh")
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
	cmd.Env = codexLoginHookEnv(t, cat, "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+home,
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
	needCodexFlock(t)
	_, cat := testCat(t)
	src, err := os.ReadFile(filepath.Join(skillDir(t, cat, "codex"), "codex-login.sh"))
	if err != nil {
		t.Fatal(err)
	}
	base, home, bin := physTempDir(t), physTempDir(t), physTempDir(t)
	writeCodexSetsidShim(t, bin)
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
	testSrc := strings.ReplaceAll(string(src), `/home/dev/.byre-identity/codex`, identity)
	testHook := filepath.Join(physTempDir(t), "codex-login.sh")
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
	cmd.Env = codexLoginHookEnv(t, cat, "PATH="+bin+":/usr/bin:/bin", "CODEX_HOME="+home,
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

// shared_unsafe vets only the leaf auth.json: a symlinked identity dir, or
// any symlinked ancestor, was followed by mkdir -p, the lock and the publish
// rename, so a valid local login was published to wherever the link
// pointed. Both shapes are refused before anything is created: non-zero
// exit, nothing at the link's target, the local login exactly as it was.
func TestCodexSharedAuthRefusesSymlinkedIdentityDir(t *testing.T) {
	_, cat := testCat(t)
	reconcile := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "reconcile.sh")
	local := `{"auth_mode":"chatgpt","tokens":{"access_token":"l","refresh_token":"lr"},"last_refresh":"2026-07-21T00:00:00Z"}`
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, target string) (base string)
	}{
		{"symlinked identity base", func(t *testing.T, target string) string {
			base := filepath.Join(physTempDir(t), "identity-link")
			if err := os.Symlink(target, base); err != nil {
				t.Fatal(err)
			}
			return base
		}},
		{"symlinked codex dir in a real base", func(t *testing.T, target string) string {
			base := physTempDir(t)
			if err := os.Symlink(target, filepath.Join(base, "codex")); err != nil {
				t.Fatal(err)
			}
			return base
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := physTempDir(t)
			base := tc.plant(t, target)
			home := physTempDir(t)
			cred := filepath.Join(home, "auth.json")
			if err := os.WriteFile(cred, []byte(local), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", reconcile, "test_symlinked_identity")
			cmd.Env = codexReconcileEnv(t, cat, base, home)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("a symlinked identity dir must be refused, got success:\n%s", out)
			}
			// Containment test: the refusal (non-zero exit, nothing created
			// at the link's target) and the untouched local login are the
			// contract; the stderr wording is not pinned (CLAUDE.md, two tiers).
			if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
				t.Fatalf("something was created at the link's target: %v %v", entries, err)
			}
			if fi, err := os.Lstat(cred); err != nil || !fi.Mode().IsRegular() {
				t.Fatalf("the local login must stay a regular file: %v %v", fi, err)
			}
			if b, _ := os.ReadFile(cred); string(b) != local {
				t.Fatalf("the local login was changed: %q", b)
			}
		})
	}
}

// The publish and keep-a-copy renames: a plain `mv` onto a
// symlink-to-directory planted at auth.json / auth.json.prev moves the file
// INTO the link's target, and GNU's -T is not available on the macOS CI
// leg (BSD mv). So the .prev rename is `mv -f` after a vet -- a symlink
// there is dropped first, a directory skips retention -- plus a post-check
// that .prev is a regular non-symlink file: the planted link is replaced
// and its target never sees a byte. (shared_unsafe already refuses a
// symlinked auth.json before publish, and the publish rename is checked
// after too; this pins the .prev rename.)
func TestCodexSharedAuthPrevRenameDoesNotFollowDirLink(t *testing.T) {
	base, home := physTempDir(t), physTempDir(t)
	identity := filepath.Join(base, "codex")
	if err := os.MkdirAll(identity, 0o755); err != nil {
		t.Fatal(err)
	}
	oldShared := `{"auth_mode":"chatgpt","tokens":{"access_token":"old","refresh_token":"old-refresh"},"last_refresh":"2026-07-20T00:00:00Z"}`
	newLocal := `{"auth_mode":"chatgpt","tokens":{"access_token":"new","refresh_token":"new-refresh"},"last_refresh":"2026-07-21T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(identity, "auth.json"), []byte(oldShared), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(newLocal), 0o600); err != nil {
		t.Fatal(err)
	}
	elsewhere := physTempDir(t)
	if err := os.Symlink(elsewhere, filepath.Join(identity, "auth.json.prev")); err != nil {
		t.Fatal(err)
	}

	runCodexSharedAuthHook(t, base, home)

	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Fatalf("the .prev rename moved a file into the planted directory link's target: %v %v", entries, err)
	}
	if fi, err := os.Lstat(filepath.Join(identity, "auth.json.prev")); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("the planted .prev link must be replaced by a regular file: %v %v", fi, err)
	}
	if got, err := os.ReadFile(filepath.Join(identity, "auth.json")); err != nil || string(got) != newLocal {
		t.Fatalf("the newer local login must still publish: %v %q", err, got)
	}
}
