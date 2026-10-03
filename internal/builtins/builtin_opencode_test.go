package builtins

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

// TestOpencodeSkillPinsLoadBearingFacts pins the opencode facts unit tests
// can hold still and that are uniquely tempting to "fix" wrong: the --auto
// autonomy flag (headless asks auto-REJECT without it — never hang, but
// never proceed either), the AGENTS.md context target in the XDG config dir
// (NOT the data-dir state volume — opencode splits them), the egress set
// (models.dev is FUNCTIONAL: with it blocked the login picker silently
// degrades to API-key-only, observed live 2026-07-16), and the login hook.
func TestOpencodeSkillPinsLoadBearingFacts(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "opencode"}, cat)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.AgentCommand(), "--auto") {
		t.Errorf("opencode autonomy flag missing from launch command %q", res.AgentCommand())
	}
	// The MCP adapter wiring (ADR 0033; live-verified 2026-07-17): the wrapper
	// is the launch command and the inject vouch rides with it — they flip
	// TOGETHER or not at all.
	if !strings.Contains(res.AgentCommand(), "byre-opencode-mcp-launch") {
		t.Errorf("MCP launch wrapper missing from launch command %q", res.AgentCommand())
	}
	if res.Agent.MCP != "inject" {
		t.Errorf("opencode mcp vouch = %q, want inject (live-verified 2026-07-17)", res.Agent.MCP)
	}
	if res.Agent.Context != "inject" {
		t.Errorf("opencode must vouch context injection (ADR 0046)")
	}
	egress := strings.Join(res.Egress(), " ")
	for _, h := range []string{"models.dev", "api.anthropic.com", "console.anthropic.com", "claude.ai"} {
		if !strings.Contains(egress, h) {
			t.Errorf("egress missing %s (got %q)", h, egress)
		}
	}
	// The state volume mounts at the XDG DATA dir — not ~/.opencode (the
	// binary dir) and not the config dir (the context target's home).
	var vol bool
	for _, v := range res.Volumes() {
		if v.Name == ".opencode" {
			vol = true
			if v.Target != "/home/dev/.local/share/opencode" {
				t.Errorf("state volume must mount at the XDG data dir, got %q", v.Target)
			}
		}
	}
	if !vol {
		t.Fatal("opencode skill should contribute a .opencode state volume")
	}
	var login bool
	for _, b := range res.BuildBlocks() {
		if b.Name != "opencode" && b.Name != "byre/opencode" {
			continue
		}
		for _, sf := range b.Files {
			if sf.Dest == "/etc/byre/firstrun.d/opencode-login" {
				login = true
			}
		}
	}
	if !login {
		t.Error("opencode login firstrun hook not shipped")
	}
	b, err := os.ReadFile(filepath.Join(skillDir(t, cat, "opencode"), "opencode-login.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "opencode auth login") {
		t.Error("login hook lost the auth-login flow")
	}
}

// The opencode login hook, driven for real with a stub `opencode` binary:
// a foreign symlinked credential is removed (anti-planting) and a fresh
// login runs; a credentialed regular file short-circuits; an empty store
// ({}) and a TRUNCATED store (an interrupted in-place write) do NOT count
// as logged in; a static provider key skips the login. The trusted dir is
// the hardcoded absolute /home/dev/.byre-identity/opencode (an env seam
// there would let config [env] redefine the trusted namespace — the codex
// precedent), so every temp-dir link here is correctly classified foreign;
// the carve-out itself is driven against a rewritten copy of the hook in
// TestOpencodeLoginHookTrustsOnlyItsOwnIdentityLink.
func TestOpencodeLoginHookBehavior(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "opencode"), "opencode-login.sh")
	lib := sharedAuthLibSeam(t, cat, "opencode")

	bin := physTempDir(t)
	stamp := filepath.Join(bin, "login-attempted")
	stub := "#!/bin/sh\ntouch " + stamp + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(dataHome, apiKey string) {
		t.Helper()
		runHook(t, "sh", hook, lib,
			"PATH="+bin+":/usr/bin:/bin",
			"XDG_DATA_HOME="+dataHome,
			"ANTHROPIC_API_KEY="+apiKey,
			"OPENCODE_API_KEY=",
		)
	}
	loginAttempted := func() bool {
		_, err := os.Stat(stamp)
		return err == nil
	}
	reset := func() {
		_ = os.Remove(stamp)
	}
	credPath := func(dataHome string) string {
		dir := filepath.Join(dataHome, "opencode")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(dir, "auth.json")
	}

	// A FOREIGN symlinked credential is removed; a fresh login runs.
	data1 := physTempDir(t)
	cred1 := credPath(data1)
	planted := filepath.Join(data1, "elsewhere.json")
	if err := os.WriteFile(planted, []byte(`{"anthropic":{"type":"api","key":"planted"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(planted, cred1); err != nil {
		t.Fatal(err)
	}
	run(data1, "")
	if _, err := os.Lstat(cred1); !os.IsNotExist(err) {
		t.Fatalf("foreign symlinked credential must be removed, still present (%v)", err)
	}
	if !loginAttempted() {
		t.Fatal("removal must fall through to a fresh login; none was attempted")
	}

	// A credentialed regular file short-circuits (no login attempted)...
	reset()
	data2 := physTempDir(t)
	if err := os.WriteFile(credPath(data2), []byte(`{"anthropic":{"type":"api","key":"live"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run(data2, "")
	if loginAttempted() {
		t.Fatal("valid credential must short-circuit the login; one was attempted")
	}

	// ...but an EMPTY store ({} — no "type" member) does not count...
	reset()
	data3 := physTempDir(t)
	if err := os.WriteFile(credPath(data3), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(data3, "")
	if !loginAttempted() {
		t.Fatal("an empty credential store must not count as logged in")
	}

	// ...and neither does a TRUNCATED store — an interrupted in-place write
	// can leave a partial file that already contains a "type" token; the
	// trailing-brace check must reject it.
	reset()
	data4 := physTempDir(t)
	if err := os.WriteFile(credPath(data4), []byte(`{"anthropic":{"type":"oauth","access":"eyJtrunc`), 0o600); err != nil {
		t.Fatal(err)
	}
	run(data4, "")
	if !loginAttempted() {
		t.Fatal("a truncated credential store must not count as logged in")
	}

	// A static provider key skips the login entirely.
	reset()
	data5 := physTempDir(t)
	credPath(data5) // dir exists, no credential
	run(data5, "sk-ant-static")
	if loginAttempted() {
		t.Fatal("a static provider key must skip the login")
	}

	// A FIFO at the credential path is not a credential opencode wrote: the
	// hook stops before reading it and offers no login (the old hook's -s
	// test skipped the sniff on the empty FIFO and ran the login into it,
	// whose in-place write blocks with no reader; no tty guard saves a
	// headless launch). Containment:
	// the refusal and the untouched object are the contract.
	reset()
	data6 := physTempDir(t)
	fifo := credPath(data6)
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	run(data6, "")
	if loginAttempted() {
		t.Fatal("a FIFO at the credential path must not lead to a login")
	}
	if fi, err := os.Lstat(fifo); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("the FIFO must be left alone: %v %v", fi, err)
	}

	// A foreign link the hook cannot drop (read-only data dir) must not
	// fall through to a login that would write THROUGH it.
	if os.Geteuid() != 0 { // root ignores the mode bits this case relies on
		reset()
		data7 := physTempDir(t)
		cred7 := credPath(data7)
		victim := filepath.Join(data7, "victim.json")
		if err := os.WriteFile(victim, []byte("victim"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, cred7); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Dir(cred7), 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Dir(cred7), 0o755) })
		run(data7, "")
		if loginAttempted() {
			t.Fatal("a foreign link that could not be removed must not lead to a login")
		}
		if b, err := os.ReadFile(victim); err != nil || string(b) != "victim" {
			t.Fatalf("the link's target must be untouched: %v %q", err, b)
		}
	}
}

// The trusted-link carve-out: the hook accepts opencode-shared-auth's own
// link into ITS identity dir (dangling included -- the first-login state) and
// removes every other symlink. The trust root is deliberately hardcoded (an
// env seam there would let a config-supplied [env] var redefine the trusted
// namespace -- the codex precedent), so the only way to exercise the
// acceptance is a copy of the hook with that literal rewritten: the fixture
// the codex login-hook test uses. All three halves of the conjunction are
// driven here, so weakening any one of them fails this test.
func TestOpencodeLoginHookTrustsOnlyItsOwnIdentityLink(t *testing.T) {
	_, cat := testCat(t)
	src, err := os.ReadFile(filepath.Join(skillDir(t, cat, "opencode"), "opencode-login.sh"))
	if err != nil {
		t.Fatal(err)
	}
	lib := sharedAuthLibSeam(t, cat, "opencode")
	bin := physTempDir(t)
	stamp := filepath.Join(bin, "login-attempted")
	if err := os.WriteFile(filepath.Join(bin, "opencode"),
		[]byte("#!/bin/sh\ntouch "+stamp+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// target names the object the data-dir link points at, inside (or beside)
	// the rewritten trust root; plant stages it. The result is the link's fate.
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
			if err := os.WriteFile(target, []byte(`{"anthropic":{"type":"api","key":"shared"}}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}, true},
		// A link planted AT the shared auth.json would chain the login's
		// write onward to a file of the planter's choosing.
		{"link whose shared end is itself a symlink is dropped", func(id, _ string) string {
			return filepath.Join(id, "auth.json")
		}, func(t *testing.T, target string) {
			if err := os.Symlink(filepath.Join(filepath.Dir(target), "chained.json"), target); err != nil {
				t.Fatal(err)
			}
		}, false},
		// Any OTHER name inside the trusted dir is not what the companion
		// links, so a dir-only match would have accepted this.
		{"link to another name in the identity dir is dropped", func(id, _ string) string {
			return filepath.Join(id, "other.json")
		}, nil, false},
		{"link outside the identity dir is dropped", func(_, elsewhere string) string {
			return filepath.Join(elsewhere, "auth.json")
		}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, dataHome, elsewhere := physTempDir(t), physTempDir(t), physTempDir(t)
			identity := filepath.Join(base, "opencode")
			if err := os.MkdirAll(identity, 0o755); err != nil {
				t.Fatal(err)
			}
			hook := filepath.Join(physTempDir(t), "opencode-login.sh")
			if err := os.WriteFile(hook,
				[]byte(strings.ReplaceAll(string(src), "/home/dev/.byre-identity/opencode", identity)), 0o755); err != nil {
				t.Fatal(err)
			}
			cred := filepath.Join(dataHome, "opencode", "auth.json")
			if err := os.MkdirAll(filepath.Dir(cred), 0o755); err != nil {
				t.Fatal(err)
			}
			target := tc.target(identity, elsewhere)
			if tc.plant != nil {
				tc.plant(t, target)
			}
			if err := os.Symlink(target, cred); err != nil {
				t.Fatal(err)
			}
			runHook(t, "sh", hook, lib,
				"PATH="+bin+":/usr/bin:/bin", "XDG_DATA_HOME="+dataHome,
				"ANTHROPIC_API_KEY=", "OPENCODE_API_KEY=")
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

// TestOpencodeSharedAuthCompositionResolves: the companion resolves
// alongside the agent, ships the 00- ordered hook (must sort before
// opencode's own login hook), and mounts the machine-scoped identity
// volume. It declares shared_auth_for (vouched 2026-07-17 — the two-box
// API-key field gate passed live, TestOpencodeSharedAuthLiveGate); that
// fact's canonical pin is the TestBuiltinSharedAuthDeclarations table in
// the skills package. The hook itself is codex-shaped and covered
// behaviorally below.
func TestOpencodeSharedAuthCompositionResolves(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "opencode", Skills: []string{"opencode-shared-auth"}}, cat)
	if err != nil {
		t.Fatalf("opencode + opencode-shared-auth failed to resolve: %v", err)
	}
	var companion string
	var agentHooks []string
	for _, b := range res.BuildBlocks() {
		for _, sf := range b.Files {
			if !strings.HasPrefix(sf.Dest, "/etc/byre/firstrun.d/") {
				continue
			}
			switch b.Name {
			case "byre/opencode-shared-auth", "opencode-shared-auth":
				companion = path.Base(sf.Dest)
			case "byre/opencode", "opencode":
				agentHooks = append(agentHooks, path.Base(sf.Dest))
			}
		}
	}
	if companion == "" {
		t.Fatal("symlink-assert hook not shipped")
	}
	if len(agentHooks) == 0 {
		t.Fatal("opencode ships no firstrun hooks; the ordering invariant has nothing to order against")
	}
	for _, h := range agentHooks {
		if !(companion < h) {
			t.Errorf("hook ordering invariant broken: companion %q must sort before opencode's %q", companion, h)
		}
	}
	var identity bool
	for _, v := range res.Volumes() {
		if v.Name == "opencode-identity" && v.MachineScoped() && v.Target == "/home/dev/.byre-identity/opencode" {
			identity = true
		}
	}
	if !identity {
		t.Errorf("identity volume missing or mis-declared: %+v", res.Volumes())
	}
	b, err := os.ReadFile(filepath.Join(skillDir(t, cat, "opencode-shared-auth"), "skill.toml"))
	if err != nil {
		t.Fatal(err)
	}
	// shared_auth_for, no longer companion_for: the two-box field gate passed
	// 2026-07-17 (the vouch follows its field gate — and the keys are
	// mutually exclusive, so companion_for must be GONE).
	if !strings.Contains(string(b), `shared_auth_for = "opencode"`) || strings.Contains(string(b), `companion_for = "opencode"`) {
		t.Error("vouch shape wrong: want shared_auth_for (field gate passed 2026-07-17), without companion_for")
	}
	// The API-key-only scope must be on the record (OAuth entries are
	// unsupported and warned; they still ride the whole-file share).
	if !strings.Contains(string(b), "API-KEY LOGINS ONLY") {
		t.Error("API-key-only scope missing from the skill.toml record")
	}
}

// The opencode symlink-assert hook's four behaviors, driven for real (the
// codex-shared-auth suite, retargeted): fresh box gets a dangling link; an
// existing per-project login is ADOPTED; a local fork is healed in favor of
// the shared credential; and the whole thing is idempotent.
func TestOpencodeSharedAuthHookBehavior(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "opencode-shared-auth"), "firstrun.sh")
	lib := sharedAuthLibSeam(t, cat, "opencode-shared-auth")
	runIn := func(identityBase, dataHome string) string {
		t.Helper()
		return runHook(t, "bash", hook, lib,
			"BYRE_IDENTITY_BASE="+identityBase, "XDG_DATA_HOME="+dataHome)
	}
	base, dataHome := physTempDir(t), physTempDir(t)
	shared := filepath.Join(base, "opencode", "auth.json")
	cred := filepath.Join(dataHome, "opencode", "auth.json")

	// 1. Fresh: dangling symlink pointing at the (absent) shared credential.
	runIn(base, dataHome)
	if got, err := os.Readlink(cred); err != nil || got != shared {
		t.Fatalf("fresh run should leave a dangling link to %q, got %q (%v)", shared, got, err)
	}
	if _, err := os.Stat(shared); !os.IsNotExist(err) {
		t.Fatalf("fresh run must not fabricate a shared credential")
	}

	// 2. Adopt: a real local login and no shared copy — the file is COPIED in
	// (temp copy + exclusive hard link onto the shared name), and the local
	// name is then renamed over by the link.
	if err := os.Remove(cred); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cred, []byte(`{"adopted":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runIn(base, dataHome)
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
	runIn(base, dataHome)
	if b, _ := os.ReadFile(shared); string(b) != `{"adopted":true}` {
		t.Fatalf("shared credential clobbered by a fork: %q", b)
	}
	if got, _ := os.Readlink(cred); got != shared {
		t.Fatalf("fork not healed to the link: %q", got)
	}

	// 4. Idempotent: run again, nothing changes.
	runIn(base, dataHome)
	if b, _ := os.ReadFile(cred); string(b) != `{"adopted":true}` {
		t.Fatalf("idempotent re-run changed the credential: %q", b)
	}

	// 5. An EMPTY local auth.json is no login: nothing is promoted and
	// nothing announces one. It is replaced by the link like any other local
	// file.
	emptyBase, emptyHome := physTempDir(t), physTempDir(t)
	emptyCred := filepath.Join(emptyHome, "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(emptyCred), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(emptyCred, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if out := runIn(emptyBase, emptyHome); strings.Contains(out, "promoted") {
		t.Fatalf("an empty auth.json must not be announced as a promoted login: %q", out)
	}
	if _, err := os.Lstat(filepath.Join(emptyBase, "opencode", "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("an empty auth.json must not become the shared credential (%v)", err)
	}
}

// A planted symlink at the shared path or on the identity dir's route is
// refused; a failed promotion keeps the only login; a newline-suffixed target
// is not mistaken for the trusted one. Containment cases (CLAUDE.md, two
// tiers): the refusal plus the unchanged victim is the contract, so no
// message fragments are pinned.
func TestOpencodeSharedAuthHookHardening(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "opencode-shared-auth"), "firstrun.sh")
	lib := sharedAuthLibSeam(t, cat, "opencode-shared-auth")
	runIn := func(identityBase, dataHome string) string {
		t.Helper()
		return runHook(t, "bash", hook, lib,
			"BYRE_IDENTITY_BASE="+identityBase, "XDG_DATA_HOME="+dataHome)
	}
	localLogin := func(t *testing.T, dataHome, body string) string {
		t.Helper()
		cred := filepath.Join(dataHome, "opencode", "auth.json")
		if err := os.MkdirAll(filepath.Dir(cred), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cred, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return cred
	}

	t.Run("symlinked shared path refused", func(t *testing.T) {
		base, dataHome := physTempDir(t), physTempDir(t)
		if err := os.MkdirAll(filepath.Join(base, "opencode"), 0o755); err != nil {
			t.Fatal(err)
		}
		decoy := filepath.Join(physTempDir(t), "decoy.json")
		if err := os.WriteFile(decoy, []byte("decoy"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(decoy, filepath.Join(base, "opencode", "auth.json")); err != nil {
			t.Fatal(err)
		}
		cred := localLogin(t, dataHome, "local")
		runIn(base, dataHome)
		isRegularWith(t, cred, "local")
		if b, _ := os.ReadFile(decoy); string(b) != "decoy" {
			t.Fatalf("the planted link's target was written: %q", b)
		}
	})

	// A FIFO at the shared path: the old hook replaced the local login with
	// a link to it (and opencode's in-place write then blocked on it).
	t.Run("FIFO at the shared path refused", func(t *testing.T) {
		base, dataHome := physTempDir(t), physTempDir(t)
		if err := os.MkdirAll(filepath.Join(base, "opencode"), 0o755); err != nil {
			t.Fatal(err)
		}
		fifo := filepath.Join(base, "opencode", "auth.json")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		cred := localLogin(t, dataHome, "local")
		runIn(base, dataHome)
		isRegularWith(t, cred, "local")
		if fi, err := os.Lstat(fifo); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
			t.Fatalf("the FIFO must be left alone: %v %v", fi, err)
		}
	})

	// A DIRECTORY at the shared path is refused like any other non-regular
	// object, before the promote is reached -- so the promote's own
	// put-it-inside-a-directory hazard needs a race, which is pinned at the
	// library level (TestSharedAuthLibRefusedPromoteLeavesNothingInADirectory;
	// gemini rides the same primitive). Here: nothing is promoted into it.
	t.Run("directory at the shared path refused", func(t *testing.T) {
		base, dataHome := physTempDir(t), physTempDir(t)
		shared := filepath.Join(base, "opencode", "auth.json")
		if err := os.MkdirAll(shared, 0o755); err != nil {
			t.Fatal(err)
		}
		cred := localLogin(t, dataHome, "local")
		runIn(base, dataHome)
		isRegularWith(t, cred, "local")
		if ents, err := os.ReadDir(shared); err != nil || len(ents) != 0 {
			t.Fatalf("nothing may be written into a directory at the shared path: %v (%v)", ents, err)
		}
	})

	t.Run("symlinked identity-dir ancestor refused", func(t *testing.T) {
		real, dataHome := physTempDir(t), physTempDir(t)
		base := filepath.Join(physTempDir(t), "base")
		if err := os.Symlink(real, base); err != nil {
			t.Fatal(err)
		}
		cred := localLogin(t, dataHome, "local")
		runIn(base, dataHome)
		isRegularWith(t, cred, "local")
		// Checked BEFORE mkdir -p: nothing at all is created through the link.
		if ents, err := os.ReadDir(real); err != nil || len(ents) != 0 {
			t.Fatalf("the symlinked ancestor's target must stay empty: %v (%v)", ents, err)
		}
	})

	t.Run("failed promotion keeps the local login", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores the read-only mode this case relies on")
		}
		base, dataHome := physTempDir(t), physTempDir(t)
		idDir := filepath.Join(base, "opencode")
		if err := os.MkdirAll(idDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(idDir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(idDir, 0o755) })
		cred := localLogin(t, dataHome, "only-login")
		runIn(base, dataHome)
		isRegularWith(t, cred, "only-login")
		if ents, _ := os.ReadDir(idDir); len(ents) != 0 {
			t.Fatalf("the read-only identity dir gained entries: %v", ents)
		}
	})

	t.Run("newline-suffixed link target re-asserted", func(t *testing.T) {
		base, dataHome := physTempDir(t), physTempDir(t)
		shared := filepath.Join(base, "opencode", "auth.json")
		cred := filepath.Join(dataHome, "opencode", "auth.json")
		if err := os.MkdirAll(filepath.Dir(cred), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(shared+"\n", cred); err != nil {
			t.Fatal(err)
		}
		runIn(base, dataHome)
		if got, err := os.Readlink(cred); err != nil || got != shared {
			t.Fatalf("link not re-asserted to the clean path: %q (%v)", got, err)
		}
	})

	// Two boxes promoting at once, as a smoke case: both find no shared copy
	// and both claim. What it checks is the WHOLE-FILE outcome -- the shared
	// credential is one box's entire login, both links land on it, exactly one
	// box says it promoted, and no temp is left behind. It cannot prove the
	// claim is exclusive: two unsynchronized processes may simply not overlap,
	// and the torn-copy the exclusive create exists to prevent needs the
	// identity volume on another device (mv = copy+unlink), which a unit test
	// cannot stage.
	t.Run("concurrent promotion", func(t *testing.T) {
		base := physTempDir(t)
		homes := []string{physTempDir(t), physTempDir(t)}
		bodies := []string{`{"box":"a"}`, `{"box":"b"}`}
		for j := range homes {
			localLogin(t, homes[j], bodies[j])
		}
		outs := make(chan string, len(homes))
		for j := range homes {
			go func(dataHome string) { outs <- runIn(base, dataHome) }(homes[j])
		}
		promoted := 0
		for range homes {
			promoted += strings.Count(<-outs, "promoted this box's existing OpenCode login")
		}
		if promoted != 1 {
			t.Fatalf("%d boxes claimed the promotion, want exactly 1", promoted)
		}
		shared := filepath.Join(base, "opencode", "auth.json")
		b, err := os.ReadFile(shared)
		if err != nil || (string(b) != bodies[0] && string(b) != bodies[1]) {
			t.Fatalf("shared credential is not one box's whole login: %v %q", err, b)
		}
		for _, h := range homes {
			cred := filepath.Join(h, "opencode", "auth.json")
			if got, err := os.Readlink(cred); err != nil || got != shared {
				t.Fatalf("%s not linked to the shared credential: %q (%v)", cred, got, err)
			}
			if ents, _ := os.ReadDir(filepath.Dir(cred)); len(ents) != 1 {
				t.Fatalf("temp litter in the data dir: %v", ents)
			}
		}
		if ents, _ := os.ReadDir(filepath.Dir(shared)); len(ents) != 1 {
			t.Fatalf("temp litter in the identity dir: %v", ents)
		}
	})
}

// The API-key-only scope: an OAuth entry in the shared store
// draws a friendly warning and is NEVER touched; an API-key-only store is
// silent.
func TestOpencodeSharedAuthWarnsOnOAuthEntry(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "opencode-shared-auth"), "firstrun.sh")

	lib := sharedAuthLibSeam(t, cat, "opencode-shared-auth")
	warns := func(authJSON string) (string, bool) {
		t.Helper()
		base, dataHome := physTempDir(t), physTempDir(t)
		if err := os.MkdirAll(filepath.Join(base, "opencode"), 0o755); err != nil {
			t.Fatal(err)
		}
		shared := filepath.Join(base, "opencode", "auth.json")
		if err := os.WriteFile(shared, []byte(authJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		s := runHook(t, "bash", hook, lib,
			"BYRE_IDENTITY_BASE="+base, "XDG_DATA_HOME="+dataHome)
		before, contentSurvives := os.ReadFile(shared)
		if contentSurvives != nil || string(before) != authJSON {
			t.Fatalf("the credential must never be touched, got %q (%v)", before, contentSurvives)
		}
		return s, strings.Contains(s, "API-key logins only")
	}

	// OAuth entry (tolerate the JSON.stringify(...,2) spacing) -> warns.
	if _, w := warns(`{"anthropic": {"type": "oauth", "access": "x", "refresh": "y"}}`); !w {
		t.Fatal("an OAuth entry in the shared store must draw the API-key-only warning")
	}
	// API-key-only store -> silent.
	if _, w := warns(`{"anthropic": {"type": "api", "key": "sk-ant-live"}}`); w {
		t.Fatal("an API-key-only store must NOT warn")
	}
}
