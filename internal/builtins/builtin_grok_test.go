package builtins

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

// TestGrokSkillPinsLoadBearingFacts pins the grok facts unit tests can hold
// still and that are uniquely tempting to "fix" wrong: the autonomy flag, the
// AGENTS.md context target inside GROK_HOME, the egress set (the device-auth
// flow was observed live against accounts.x.ai), the device-auth login flow —
// which the vendor's TOP-LEVEL README does not document (it lags the binary;
// the flag is real, see the skill.toml evidence note) — and the bundled-skills
// bridge hook (without it the GROK_HOME split silently drops grok's bundled
// product skills).
func TestGrokSkillPinsLoadBearingFacts(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "grok"}, cat)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.AgentCommand(), "--always-approve") {
		t.Errorf("grok autonomy flag missing from launch command %q", res.AgentCommand())
	}
	if got := res.AgentContextTarget(); got != "/home/dev/.grok-home/AGENTS.md" {
		t.Errorf("context target must be AGENTS.md inside GROK_HOME, got %q", got)
	}
	egress := strings.Join(res.Egress(), " ")
	for _, h := range []string{"cli-chat-proxy.grok.com", "auth.x.ai", "accounts.x.ai"} {
		if !strings.Contains(egress, h) {
			t.Errorf("egress missing %s (got %q)", h, egress)
		}
	}
	var login, bundled bool
	for _, b := range res.BuildBlocks() {
		if b.Name != "grok" && b.Name != "byre/grok" {
			continue
		}
		for _, sf := range b.Files {
			switch sf.Dest {
			case "/etc/byre/firstrun.d/grok-login":
				login = true
			case "/etc/byre/firstrun.d/grok-bundled":
				bundled = true
			}
		}
	}
	if !login || !bundled {
		t.Errorf("grok firstrun hooks not both shipped (login=%v bundled=%v)", login, bundled)
	}
	b, err := os.ReadFile(filepath.Join(skillDir(t, cat, "grok"), "grok-login.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "grok login --device-auth") {
		t.Error("login hook lost the device-auth flow (the vendor README omits the flag; the binary has it)")
	}
}

// The bundled-skills bridge hook, driven for real: a fresh GROK_HOME gets the
// symlink to the image-side extraction dir; a real directory (a future grok
// managing bundled/ in place) is left alone; and the assert is idempotent.
func TestGrokBundledHookBehavior(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "grok"), "grok-bundled.sh")
	home := t.TempDir()
	run := func() {
		t.Helper()
		cmd := exec.Command("sh", hook)
		cmd.Env = append(os.Environ(), "GROK_HOME="+home)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v (%s)", err, out)
		}
	}
	link := filepath.Join(home, "bundled")

	run()
	if got, err := os.Readlink(link); err != nil || got != "/home/dev/.grok/bundled" {
		t.Fatalf("fresh run should link bundled to the image tree, got %q (%v)", got, err)
	}
	run() // idempotent
	if got, _ := os.Readlink(link); got != "/home/dev/.grok/bundled" {
		t.Fatalf("re-run changed the link: %q", got)
	}

	// A real directory means grok manages bundled/ in place — hands off.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
	run()
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("a real bundled/ dir must be left alone: %v %v", fi, err)
	}
}

// TestGrokSharedAuthBrokerShape pins the v2 rebuild (ADR 0036, replacing the
// ADR 0023 retired stub this test used to pin): the companion contributes the
// broker env (grok's external-auth seam), the machine-scoped identity volume,
// the seeding hook + broker script, and its own auth.x.ai egress. The vouch
// key stays companion_for until the live field gate runs — shared_auth_for
// would put the skill in the onboarding offer (ADR 0025), and the v1 lesson
// is that the vouch follows the field gate.
func TestGrokSharedAuthBrokerShape(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "grok", Skills: []string{"grok-shared-auth"}}, cat)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := res.Env()["GROK_AUTH_PROVIDER_COMMAND"]; got != "bash /etc/byre/grok-auth-broker" {
		t.Errorf("broker env not contributed, got %q", got)
	}
	var vol *config.Volume
	for _, v := range res.Volumes() {
		if v.Name == "grok-identity" {
			v := v
			vol = &v
			break
		}
	}
	if vol == nil {
		t.Fatal("grok-identity volume missing")
	}
	if vol.Scope != "machine" || vol.Target != "/home/dev/.byre-identity/grok" {
		t.Errorf("identity volume must be machine-scoped at the identity path, got %+v", vol)
	}
	staged := map[string]bool{}
	for _, b := range res.BuildBlocks() {
		if b.Name == "grok-shared-auth" || b.Name == "byre/grok-shared-auth" {
			for _, f := range b.Files {
				staged[f.Dest] = true
			}
		}
	}
	for _, dst := range []string{"/etc/byre/firstrun.d/00-grok-shared-auth", "/etc/byre/grok-auth-broker"} {
		if !staged[dst] {
			t.Errorf("file %s not staged (got %v)", dst, staged)
		}
	}
	egress := false
	for _, h := range res.Egress() {
		if h == "auth.x.ai:443" {
			egress = true
		}
	}
	if !egress {
		t.Error("auth.x.ai egress missing")
	}
	b, err := os.ReadFile(filepath.Join(skillDir(t, cat, "grok-shared-auth"), "skill.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `companion_for = "grok"`) || strings.Contains(string(b), "\nshared_auth_for") {
		t.Error("vouch shape wrong: want companion_for (field gate pending), not shared_auth_for")
	}
}

// TestGrokLoginHookStandsDownForSharedAuth pins the handoff between the grok
// skill's login hook and the shared-auth companion: with the broker env set,
// the hook must not start a per-box login (an orphaned chain) — but the
// symlink heal still runs first, since a planted link misbehaves either way.
func TestGrokLoginHookStandsDownForSharedAuth(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "grok"), "grok-login.sh")
	home := t.TempDir()

	// A fake grok on PATH records any invocation; the hook must never reach it.
	bin := t.TempDir()
	marker := filepath.Join(home, "grok-was-called")
	fake := "#!/bin/sh\ntouch " + marker + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "grok"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "auth.json")
	if err := os.Symlink("/nonexistent-target", link); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", hook)
	cmd.Env = append(os.Environ(),
		"GROK_HOME="+home,
		"PATH="+bin+":"+os.Getenv("PATH"),
		"GROK_AUTH_PROVIDER_COMMAND=bash /etc/byre/grok-auth-broker",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("symlinked credential must still be healed before standing down")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("hook must not invoke grok when the shared-auth broker is configured")
	}
}

// TestGrokAuthBrokerBehavior exercises the broker script against a fake curl:
// the fresh fast path, a rotating refresh, the invalid_grant move-aside (the
// self-heal for a dead chain, v1's corpse included), and the transient-failure
// degrade to the cached token. The store fixtures are grok-native auth.json
// shapes (scope-keyed map, refresh_token-bearing OIDC entry) — the same file
// `GROK_AUTH_PATH` seeding writes.
func TestGrokAuthBrokerBehavior(t *testing.T) {
	for _, dep := range []string{"bash", "jq", "flock", "date"} {
		if _, err := exec.LookPath(dep); err != nil {
			t.Skipf("%s not on PATH", dep)
		}
	}
	_, cat := testCat(t)
	broker := filepath.Join(skillDir(t, cat, "grok-shared-auth"), "grok-auth-broker.sh")

	type out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Issuer      string `json:"issuer"`
	}
	// run executes the broker with BYRE_IDENTITY_BASE=base, the given fake
	// curl body (empty = no curl override) and any extra env, returning
	// stdout, stderr, exit err.
	run := func(t *testing.T, base, fakeCurl string, extraEnv ...string) (string, string, error) {
		t.Helper()
		bin := t.TempDir()
		if fakeCurl != "" {
			if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(fakeCurl), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("bash", broker)
		cmd.Env = append(append(os.Environ(), "BYRE_IDENTITY_BASE="+base, "PATH="+bin+":"+os.Getenv("PATH")), extraEnv...)
		var so, se strings.Builder
		cmd.Stdout, cmd.Stderr = &so, &se
		err := cmd.Run()
		return so.String(), se.String(), err
	}
	// seedStoreAt builds an identity base (what BYRE_IDENTITY_BASE points at)
	// whose store entry was minted `minted` ago and expires in `ttl`; the
	// broker's files live under its grok/ subdir.
	seedStoreAt := func(t *testing.T, ttl, minted time.Duration) (idbase, dir string) {
		t.Helper()
		idbase = t.TempDir()
		base := filepath.Join(idbase, "grok")
		if err := os.MkdirAll(base, 0o700); err != nil {
			t.Fatal(err)
		}
		store := fmt.Sprintf(`{"https://auth.x.ai::client-123":{"key":"old-access","auth_mode":"Oidc","create_time":%q,"user_id":"u1","refresh_token":"rt-1","expires_at":%q,"oidc_issuer":"https://auth.x.ai","oidc_client_id":"client-123"}}`,
			time.Now().UTC().Add(-minted).Format(time.RFC3339),
			time.Now().UTC().Add(ttl).Format(time.RFC3339))
		if err := os.WriteFile(filepath.Join(base, "auth.json"), []byte(store), 0o600); err != nil {
			t.Fatal(err)
		}
		// Pre-warm the endpoint cache: the refresh path must never need
		// discovery inside its 5s budget.
		if err := os.WriteFile(filepath.Join(base, "token_endpoint"), []byte("https://auth.x.ai/token"), 0o600); err != nil {
			t.Fatal(err)
		}
		return idbase, base
	}
	seedStore := func(t *testing.T, ttl time.Duration) (idbase, dir string) {
		t.Helper()
		return seedStoreAt(t, ttl, 12*time.Hour)
	}

	t.Run("no store fails with re-seed guidance", func(t *testing.T) {
		_, se, err := run(t, t.TempDir(), "")
		if err == nil {
			t.Fatal("want non-zero exit with no store")
		}
		if !strings.Contains(se, "relaunch") {
			t.Errorf("stderr should tell the user how to re-seed, got %q", se)
		}
	})

	t.Run("fresh token emitted without refresh", func(t *testing.T) {
		idbase, _ := seedStore(t, 2*time.Hour)
		// A curl that explodes proves the fast path makes no network call.
		so, se, err := run(t, idbase, "#!/bin/sh\necho fake-curl-invoked >&2\nexit 97\n")
		if err != nil {
			t.Fatalf("broker failed: %v (stderr %q)", err, se)
		}
		var o out
		if err := json.Unmarshal([]byte(so), &o); err != nil {
			t.Fatalf("stdout not the provider JSON: %v (%q)", err, so)
		}
		if o.AccessToken != "old-access" || o.ExpiresIn < 3600 || o.Issuer != "https://auth.x.ai" {
			t.Errorf("bad emit: %+v", o)
		}
	})

	t.Run("stale token refreshes and rotates the store", func(t *testing.T) {
		idbase, base := seedStore(t, 100*time.Second) // below the 420s margin
		fake := "#!/bin/sh\n" +
			`printf '%s\n%s' '{"access_token":"new-access","refresh_token":"rt-2","expires_in":21600}' 200` + "\n"
		so, se, err := run(t, idbase, fake)
		if err != nil {
			t.Fatalf("broker failed: %v (stderr %q)", err, se)
		}
		var o out
		if err := json.Unmarshal([]byte(so), &o); err != nil {
			t.Fatalf("stdout not the provider JSON: %v (%q)", err, so)
		}
		if o.AccessToken != "new-access" || o.ExpiresIn != 21600 {
			t.Errorf("bad emit after refresh: %+v", o)
		}
		b, err := os.ReadFile(filepath.Join(base, "auth.json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"new-access"`, `"rt-2"`} {
			if !strings.Contains(string(b), want) {
				t.Errorf("store not rotated, missing %s: %s", want, b)
			}
		}
		if strings.Contains(string(b), "rt-1") {
			t.Error("spent refresh token still in the store")
		}
	})

	t.Run("invalid_grant moves the dead store aside", func(t *testing.T) {
		idbase, base := seedStore(t, 100*time.Second)
		fake := "#!/bin/sh\n" +
			`printf '%s\n%s' '{"error":"invalid_grant"}' 400` + "\n"
		_, se, err := run(t, idbase, fake)
		if err == nil {
			t.Fatal("want non-zero exit on a dead chain")
		}
		if _, err := os.Stat(filepath.Join(base, "auth.json")); !os.IsNotExist(err) {
			t.Error("dead store must be moved aside")
		}
		dead, _ := filepath.Glob(filepath.Join(base, "auth.json.dead-*"))
		if len(dead) != 1 {
			t.Errorf("want one dead-store file, got %v", dead)
		}
		if !strings.Contains(se, "grok login --device-auth") {
			t.Errorf("stderr should carry the re-seed command, got %q", se)
		}
	})

	t.Run("transient failure degrades to the cached token", func(t *testing.T) {
		// 400s: stale enough to try a refresh (< the 420s margin), alive
		// enough to emit (> grok's 300s early-invalidation + jitter floor).
		idbase, _ := seedStoreAt(t, 400*time.Second, 12*time.Hour)
		so, se, err := run(t, idbase, "#!/bin/sh\nexit 6\n")
		if err != nil {
			t.Fatalf("broker failed: %v (stderr %q)", err, se)
		}
		var o out
		if err := json.Unmarshal([]byte(so), &o); err != nil {
			t.Fatalf("stdout not the provider JSON: %v (%q)", err, so)
		}
		if o.AccessToken != "old-access" || o.ExpiresIn <= 360 || o.ExpiresIn > 400 {
			t.Errorf("degrade should emit the cached token with its true remaining life: %+v", o)
		}
	})

	t.Run("degrade never emits a token grok would instantly re-expire", func(t *testing.T) {
		// 200s remaining is under grok's 300s early-invalidation buffer:
		// emitting it would thrash the refresh loop. Fail closed instead.
		idbase, _ := seedStoreAt(t, 200*time.Second, 12*time.Hour)
		_, _, err := run(t, idbase, "#!/bin/sh\nexit 6\n")
		if err == nil {
			t.Fatal("want non-zero exit when the cached token is under grok's expiry buffer")
		}
	})

	t.Run("GROK_AUTH_EXPIRED forces a refresh past a fresh-looking store", func(t *testing.T) {
		// grok flags its token dead (covers 401 rejection) while the store
		// still looks wall-clock fresh: the broker must refresh, not re-emit
		// the possibly-rejected pair.
		idbase, base := seedStoreAt(t, 2*time.Hour, 12*time.Hour)
		fake := "#!/bin/sh\n" +
			`printf '%s\n%s' '{"access_token":"new-access","refresh_token":"rt-2","expires_in":21600}' 200` + "\n"
		so, se, err := run(t, idbase, fake, "GROK_AUTH_EXPIRED=1")
		if err != nil {
			t.Fatalf("broker failed: %v (stderr %q)", err, se)
		}
		var o out
		if err := json.Unmarshal([]byte(so), &o); err != nil {
			t.Fatalf("stdout not the provider JSON: %v (%q)", err, so)
		}
		if o.AccessToken != "new-access" {
			t.Errorf("flagged call must return a refreshed token, got %+v", o)
		}
		if b, _ := os.ReadFile(filepath.Join(base, "auth.json")); !strings.Contains(string(b), "rt-2") {
			t.Error("store not rotated by the forced refresh")
		}
	})

	t.Run("GROK_AUTH_EXPIRED trusts only a sibling's just-rotated pair", func(t *testing.T) {
		// A pair minted seconds ago is almost always a sibling's rotation —
		// a different token from the caller's — so it is emitted without
		// spending the refresh token (the caller-rotated-it residual is
		// bounded by the 60s window). The exploding curl proves no network
		// call happens.
		idbase, _ := seedStoreAt(t, 2*time.Hour, 5*time.Second)
		so, se, err := run(t, idbase, "#!/bin/sh\nexit 97\n", "GROK_AUTH_EXPIRED=1")
		if err != nil {
			t.Fatalf("broker failed: %v (stderr %q)", err, se)
		}
		var o out
		if err := json.Unmarshal([]byte(so), &o); err != nil {
			t.Fatalf("stdout not the provider JSON: %v (%q)", err, so)
		}
		if o.AccessToken != "old-access" {
			t.Errorf("just-rotated pair should be emitted as-is, got %+v", o)
		}
	})

	t.Run("lock loser adopts a winner's just-rotated pair", func(t *testing.T) {
		// The lock is held past the flagged call's wait budget, but the
		// store already carries a just-rotated pair (as after a winner's
		// refresh) — the loser must emit it rather than fail into grok's
		// ~300s backoff.
		idbase, base := seedStoreAt(t, 2*time.Hour, 5*time.Second)
		holder := exec.Command("flock", filepath.Join(base, "broker.lock"), "sleep", "15")
		if err := holder.Start(); err != nil {
			t.Fatalf("lock holder: %v", err)
		}
		defer func() { _ = holder.Process.Kill(); _ = holder.Wait() }()
		time.Sleep(200 * time.Millisecond) // let the holder take the flock
		so, se, err := run(t, idbase, "#!/bin/sh\nexit 97\n", "GROK_AUTH_EXPIRED=1")
		if err != nil {
			t.Fatalf("broker failed: %v (stderr %q)", err, se)
		}
		var o out
		if err := json.Unmarshal([]byte(so), &o); err != nil {
			t.Fatalf("stdout not the provider JSON: %v (%q)", err, so)
		}
		if o.AccessToken != "old-access" {
			t.Errorf("loser should emit the winner's pair, got %+v", o)
		}
	})

	t.Run("GROK_AUTH_EXPIRED never degrades to the cached token", func(t *testing.T) {
		// Refresh fails transiently on a flagged call: re-emitting the pair
		// grok flagged dead would 401-loop; fail closed so grok backs off.
		idbase, _ := seedStoreAt(t, 2*time.Hour, 12*time.Hour)
		_, se, err := run(t, idbase, "#!/bin/sh\nexit 6\n", "GROK_AUTH_EXPIRED=1")
		if err == nil {
			t.Fatalf("want non-zero exit on a flagged call with a failed refresh (stderr %q)", se)
		}
	})
}

// TestGrokSharedAuthSeedHookBehavior exercises the firstrun hook's non-
// interactive paths: an already-seeded store is left alone, and a real
// per-box login (refresh_token present) is promoted to the machine store
// and dropped locally so exactly one copy of the chain exists.
func TestGrokSharedAuthSeedHookBehavior(t *testing.T) {
	for _, dep := range []string{"jq", "date", "flock"} {
		if _, err := exec.LookPath(dep); err != nil {
			t.Skipf("%s not on PATH", dep)
		}
	}
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "grok-shared-auth"), "00-grok-shared-auth.sh")

	run := func(t *testing.T, idbase, home string) string {
		t.Helper()
		bin := t.TempDir()
		// fake grok: the hook needs one on PATH; seeding must not be reached
		// in these subtests, so reaching it is a loud failure.
		if err := os.WriteFile(filepath.Join(bin, "grok"), []byte("#!/bin/sh\necho SEED-LOGIN-REACHED\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		// fake curl keeps the endpoint-cache warmer off the network.
		if err := os.WriteFile(filepath.Join(bin, "curl"), []byte("#!/bin/sh\nexit 6\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", hook)
		cmd.Env = append(os.Environ(),
			"BYRE_IDENTITY_BASE="+idbase, "GROK_HOME="+home,
			"PATH="+bin+":"+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hook failed: %v (%s)", err, out)
		}
		return string(out)
	}
	pair := `{"https://auth.x.ai::c":{"key":"k","refresh_token":"rt","oidc_issuer":"https://auth.x.ai","oidc_client_id":"c"}}`

	t.Run("seeded store is left alone", func(t *testing.T) {
		idbase, home := t.TempDir(), t.TempDir()
		store := filepath.Join(idbase, "grok", "auth.json")
		if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(store, []byte(pair), 0o600); err != nil {
			t.Fatal(err)
		}
		out := run(t, idbase, home)
		if strings.Contains(out, "SEED-LOGIN-REACHED") {
			t.Error("hook must not log in when the store is seeded")
		}
		if b, _ := os.ReadFile(store); string(b) != pair {
			t.Error("seeded store was modified")
		}
	})

	t.Run("local login is promoted and dropped", func(t *testing.T) {
		idbase, home := t.TempDir(), t.TempDir()
		local := filepath.Join(home, "auth.json")
		if err := os.WriteFile(local, []byte(pair), 0o600); err != nil {
			t.Fatal(err)
		}
		out := run(t, idbase, home)
		if !strings.Contains(out, "promoted") {
			t.Errorf("promotion must be announced, got %q", out)
		}
		if b, err := os.ReadFile(filepath.Join(idbase, "grok", "auth.json")); err != nil || string(b) != pair {
			t.Errorf("pair not promoted to the store: %v %q", err, b)
		}
		if _, err := os.Stat(local); !os.IsNotExist(err) {
			t.Error("local copy must be dropped after promotion (one chain, one home)")
		}
	})

	t.Run("seeded store wins over a local login", func(t *testing.T) {
		idbase, home := t.TempDir(), t.TempDir()
		other := `{"https://auth.x.ai::c":{"key":"k2","refresh_token":"rt-other","oidc_issuer":"https://auth.x.ai","oidc_client_id":"c"}}`
		store := filepath.Join(idbase, "grok", "auth.json")
		if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(store, []byte(other), 0o600); err != nil {
			t.Fatal(err)
		}
		local := filepath.Join(home, "auth.json")
		if err := os.WriteFile(local, []byte(pair), 0o600); err != nil {
			t.Fatal(err)
		}
		run(t, idbase, home)
		if b, _ := os.ReadFile(store); string(b) != other {
			t.Error("an already-seeded store must never be overwritten by a local login")
		}
		if _, err := os.Stat(local); err != nil {
			t.Error("the local login must be left in place when the store is already seeded (it goes inert)")
		}
	})
}

// TestGrokLoginHookHealsRetiredSymlink drives the real grok-login hook with a
// stub `grok` binary. The retirement (ADR 0023) made the anti-planting rule
// absolute again: a symlinked auth.json NEVER counts — even a link into the
// identity volume holding credential-shaped content (v1's carve-out kept
// exactly that, which is how dead shared credentials clobbered working
// boxes). The hook must remove the link and proceed to a fresh login; a
// valid REGULAR file must still short-circuit the login entirely.
func TestGrokLoginHookHealsRetiredSymlink(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "grok"), "grok-login.sh")

	// Stub grok on PATH: records that a login was attempted, succeeds.
	bin := t.TempDir()
	stamp := filepath.Join(bin, "login-attempted")
	stub := "#!/bin/sh\ntouch " + stamp + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "grok"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(home string) {
		t.Helper()
		cmd := exec.Command("sh", hook)
		cmd.Env = append(os.Environ(),
			"PATH="+bin+":/usr/bin:/bin",
			"GROK_HOME="+home,
			"XAI_API_KEY=", // must not short-circuit
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v (%s)", err, out)
		}
	}

	// A symlinked credential — even dressed as v1's identity-volume link with
	// valid-looking shared content — is removed and a fresh login runs.
	home := t.TempDir()
	shared := filepath.Join(home, "identity-volume", "auth.json")
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte(`{"scope":{"key":"dead-but-plausible"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cred := filepath.Join(home, "auth.json")
	if err := os.Symlink(shared, cred); err != nil {
		t.Fatal(err)
	}
	run(home)
	if _, err := os.Lstat(cred); !os.IsNotExist(err) {
		t.Fatalf("symlinked credential must be removed, still present (%v)", err)
	}
	if _, err := os.Stat(stamp); err != nil {
		t.Fatal("removal must fall through to a fresh login; none was attempted")
	}

	// A valid regular file short-circuits: kept, no login attempted.
	if err := os.Remove(stamp); err != nil {
		t.Fatal(err)
	}
	home2 := t.TempDir()
	cred2 := filepath.Join(home2, "auth.json")
	if err := os.WriteFile(cred2, []byte(`{"scope":{"key":"live"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run(home2)
	if b, err := os.ReadFile(cred2); err != nil || !strings.Contains(string(b), "live") {
		t.Fatalf("valid per-box credential must be left alone: %v %q", err, b)
	}
	if _, err := os.Stat(stamp); !os.IsNotExist(err) {
		t.Fatal("valid credential must short-circuit the login; one was attempted")
	}

	// Healing must run BEFORE the XAI_API_KEY short-circuit: a stored
	// credential shadows the key (vendor auth guide), so a dead link left in
	// place would override a working key. Link removed, key path taken (no
	// login attempted).
	home3 := t.TempDir()
	cred3 := filepath.Join(home3, "auth.json")
	if err := os.Symlink(filepath.Join(home3, "nowhere"), cred3); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", hook)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":/usr/bin:/bin",
		"GROK_HOME="+home3,
		"XAI_API_KEY=xai-static-key",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
	if _, err := os.Lstat(cred3); !os.IsNotExist(err) {
		t.Fatal("API-key boxes must still shed a symlinked credential (it would shadow the key)")
	}
	if _, err := os.Stat(stamp); !os.IsNotExist(err) {
		t.Fatal("with XAI_API_KEY set, no file login should be attempted")
	}
}
