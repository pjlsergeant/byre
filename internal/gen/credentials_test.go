package gen

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// runReceiver drives the real embedded receiver under bash with a stream on
// stdin and BYRE_CRED_DIR pointed at a temp dir.
func runReceiver(t *testing.T, dir, stream string) (int, string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "receiver.sh")
	if err := os.WriteFile(script, ReceiverScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	cmd.Stdin = strings.NewReader(stream)
	cmd.Env = launcherEnv("BYRE_CRED_DIR=" + dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("receiver did not run: %v (%s)", err, out)
	return -1, ""
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func TestReceiverWritesValuesAndDoneLast(t *testing.T) {
	dir := t.TempDir()
	value := []byte("sk-live\nwith\x01binary bytes and trailing newline\n")
	stream := "byre-credentials 1\n" +
		"item manifest\n" + b64([]byte("STRIPE_KEY env\n")) + "\n" +
		"item STRIPE_KEY\n" + b64(value) + "\n" +
		"done\n"
	code, out := runReceiver(t, dir, stream)
	if code != 0 {
		t.Fatalf("receiver exit %d: %s", code, out)
	}
	got, err := os.ReadFile(filepath.Join(dir, "credentials", "STRIPE_KEY"))
	if err != nil || !bytes.Equal(got, value) {
		t.Fatalf("value roundtrip: %v %q", err, got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".done")); err != nil {
		t.Fatalf(".done sentinel: %v", err)
	}
	// Files land private to the dev uid (umask 077).
	fi, _ := os.Stat(filepath.Join(dir, "credentials", "STRIPE_KEY"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("value file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestReceiverIncompleteStreamLeavesNoSentinel(t *testing.T) {
	dir := t.TempDir()
	stream := "byre-credentials 1\n" +
		"item manifest\n" + b64([]byte("STRIPE_KEY env\n")) + "\n" +
		"item STRIPE_KEY\n" + b64([]byte("v")) + "\n" // EOF without done
	code, _ := runReceiver(t, dir, stream)
	if code == 0 {
		t.Fatal("incomplete stream must not exit 0")
	}
	if _, err := os.Stat(filepath.Join(dir, ".done")); !os.IsNotExist(err) {
		t.Fatal("incomplete stream must leave no .done — the launcher's wait then fails the launch closed")
	}
}

func TestReceiverRefusesUnknownVersionAndBadNames(t *testing.T) {
	dir := t.TempDir()
	if code, out := runReceiver(t, dir, "byre-credentials 999\n"); code == 0 || !strings.Contains(out, "not a credential stream") {
		t.Fatalf("unknown version: exit %d out %q", code, out)
	}
	bad := "byre-credentials 1\nitem manifest\n" + b64([]byte("A env\n")) + "\nitem ../escape\n" + b64([]byte("v")) + "\ndone\n"
	if code, out := runReceiver(t, dir, bad); code == 0 || !strings.Contains(out, "malformed item name") {
		t.Fatalf("bad name: exit %d out %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".done")); !os.IsNotExist(err) {
		t.Fatal("refused stream must leave no .done")
	}
	// The manifest is positional: a stream that opens with a value frame is
	// not one this receiver will write anything for.
	noManifest := "byre-credentials 1\nitem A\n" + b64([]byte("v")) + "\ndone\n"
	if code, out := runReceiver(t, t.TempDir(), noManifest); code == 0 || !strings.Contains(out, "manifest frame") {
		t.Fatalf("missing manifest prologue: exit %d out %q", code, out)
	}
}

// "manifest" is a legal environment variable name, so a credential can be
// keyed it — and a receiver that honoured the name would write the SECRET
// over the manifest, deliver nothing, and hand the launcher the secret's own
// bytes to parse as export lines. Host-side the key is refused outright; this
// is the receiver's own layer of that.
func TestReceiverRefusesACredentialKeyedManifest(t *testing.T) {
	dir := t.TempDir()
	realManifest := []byte("STRIPE_KEY env\n")
	stream := "byre-credentials 1\n" +
		"item manifest\n" + b64(realManifest) + "\n" +
		"item manifest\n" + b64([]byte("sk-live-secret")) + "\n" +
		"done\n"
	code, out := runReceiver(t, dir, stream)
	if code == 0 {
		t.Fatalf("a second manifest frame must be refused: %s", out)
	}
	got, err := os.ReadFile(filepath.Join(dir, "manifest"))
	if err != nil || !bytes.Equal(got, realManifest) {
		t.Fatalf("the manifest was clobbered: %v %q", err, got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".done")); !os.IsNotExist(err) {
		t.Fatal("refused stream must leave no .done")
	}
}

// firstrunDirWith returns a real firstrun.d holding script as its one hook,
// for the tests that care where the credential pass sits relative to the loop.
func firstrunDirWith(t *testing.T, script string) string {
	t.Helper()
	firstrun := filepath.Join(t.TempDir(), "firstrun.d")
	if err := os.MkdirAll(firstrun, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(firstrun, "10-hook"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return firstrun
}

// runLauncherCreds drives the real launcher with the credential env seams
// set and a command that prints the export targets, so the test observes
// exactly what the agent process would see. firstrun, when given, is the
// firstrun.d the launcher runs (firstrunDirWith builds one); without it the
// seam points at a directory that does not exist, so no hook runs.
func runLauncherCreds(t *testing.T, dir string, expect bool, wait, printCmd string, firstrun ...string) (int, string) {
	t.Helper()
	td := t.TempDir()
	script := filepath.Join(td, "launcher.sh")
	if err := os.WriteFile(script, LauncherScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	firstrunDir := filepath.Join(td, "no-firstrun")
	if len(firstrun) == 1 {
		firstrunDir = firstrun[0]
	} else if len(firstrun) > 1 {
		t.Fatalf("runLauncherCreds takes at most one firstrun dir, got %d", len(firstrun))
	}
	cmd := exec.Command("bash", script, "bash", "-c", printCmd)
	env := launcherEnv(
		"BYRE_LAUNCH_GATE_FILE="+filepath.Join(td, "no-such-gate"),
		"BYRE_FIRSTRUN_DIR="+firstrunDir,
		"BYRE_ENVD_DIR="+filepath.Join(td, "no-envd"),
		"BYRE_CRED_DIR="+dir,
		"BYRE_CRED_WAIT="+wait,
	)
	if expect {
		env = append(env, "BYRE_CRED_EXPECT=1")
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("launcher did not run: %v (%s)", err, out)
	return -1, ""
}

// deliver writes a delivered tree the way the receiver would.
func deliverTree(t *testing.T, dir string, manifest string, values map[string][]byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "credentials"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	for n, v := range values {
		if err := os.WriteFile(filepath.Join(dir, "credentials", n), v, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".done"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLauncherExportsEnvAndFileKinds(t *testing.T) {
	dir := t.TempDir()
	deliverTree(t, dir,
		"STRIPE_KEY env\nTLS_CERT_PATH file\n",
		map[string][]byte{"STRIPE_KEY": []byte("sk-123\n"), "TLS_CERT_PATH": []byte("PEM")})
	code, out := runLauncherCreds(t, dir, true, "5",
		`printf '%s|%s' "$STRIPE_KEY" "$TLS_CERT_PATH"`)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	// Byte-exact env export (the trailing newline survives — $(cat) would
	// eat it); file kind exports the tmpfs path.
	want := "sk-123\n|" + filepath.Join(dir, "credentials", "TLS_CERT_PATH")
	if out != want {
		t.Fatalf("agent saw %q, want %q", out, want)
	}
}

// A delivery that never lands fails the launch CLOSED, the same direction
// the network gate takes: the agent never runs, and the message names the
// deliberate way to launch without.
func TestLauncherCredWaitFailsClosed(t *testing.T) {
	dir := t.TempDir() // nothing delivered, no .done
	code, out := runLauncherCreds(t, dir, true, "1", `printf 'ran:%s' "${STRIPE_KEY:-unset}"`)
	if code == 0 {
		t.Fatalf("a launch without its declared credentials must not run the agent; out: %s", out)
	}
	if strings.Contains(out, "ran:") {
		t.Fatalf("the agent ran anyway: %q", out)
	}
	if !strings.Contains(out, "failing closed") || !strings.Contains(out, "--credentials=skip") {
		t.Fatalf("the refusal must name the rule and the remedy: %q", out)
	}
}

// The restart refusal is the same mechanism: the tmpfs empties, so a
// restarted box observes exactly the never-arrived state above and exits.
func TestLauncherRestartWithoutRedeliveryRefuses(t *testing.T) {
	dir := t.TempDir()
	deliverTree(t, dir, "STRIPE_KEY env\n", map[string][]byte{"STRIPE_KEY": []byte("v")})
	if code, out := runLauncherCreds(t, dir, true, "1", `printf ok`); code != 0 {
		t.Fatalf("the delivered launch must run: exit %d %s", code, out)
	}
	// The restart: same container flag, empty tmpfs.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	code, out := runLauncherCreds(t, dir, true, "1", `printf ok`)
	if code == 0 || strings.Contains(out, "ok") {
		t.Fatalf("a restart with scheduled credentials must refuse: exit %d %q", code, out)
	}
}

func TestLauncherNoExpectNoWait(t *testing.T) {
	// Without the flag the launcher must not wait at all — a launch under
	// --credentials=skip, and a config with no credential rows at all, cost
	// nothing. (Every OTHER shape sets the flag: credentials are blocking, so
	// a declared row byre could not deliver stops the launch host-side rather
	// than reaching a launcher that shrugs.)
	dir := t.TempDir()
	code, out := runLauncherCreds(t, dir, false, "60", `printf ok`)
	if code != 0 || !strings.Contains(out, "ok") {
		t.Fatalf("exit %d: %s", code, out)
	}
}

func TestLauncherCredExportWinsEnvdCollision(t *testing.T) {
	// The exports are re-applied after the env.d loop (the second
	// byre_credentials_apply call), so a credential target beats an env.d
	// hook exporting the same variable even though the first pass ran above
	// it (ADR 0028 ordering). This is the test that proves the second pass
	// exists.
	dir := t.TempDir()
	deliverTree(t, dir, "STRIPE_KEY env\n", map[string][]byte{"STRIPE_KEY": []byte("from-credential")})
	td := t.TempDir()
	envd := filepath.Join(td, "env.d")
	if err := os.MkdirAll(envd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envd, "10-clash.sh"), []byte("export STRIPE_KEY=from-envd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(td, "launcher.sh")
	if err := os.WriteFile(script, LauncherScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script, "bash", "-c", `printf '%s' "$STRIPE_KEY"`)
	cmd.Env = launcherEnv(
		"BYRE_LAUNCH_GATE_FILE="+filepath.Join(td, "no-such-gate"),
		"BYRE_FIRSTRUN_DIR="+filepath.Join(td, "no-firstrun"),
		"BYRE_ENVD_DIR="+envd,
		"BYRE_CRED_DIR="+dir,
		"BYRE_CRED_WAIT=5",
		"BYRE_CRED_EXPECT=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launcher: %v (%s)", err, out)
	}
	if string(out) != "from-credential" {
		t.Fatalf("collision winner = %q, want the credential export", out)
	}
}

// A credential named IFS is legal under the key grammar (byre does not
// reserve shell names, P1). The first pass exports it into the launcher's
// own shell, so a manifest read that inherited IFS took "KEY env" as one
// key on the second pass and failed a good delivery closed -- the read's
// split is pinned to a space. IFS is listed first so the first pass's own
// later rows are read under it too.
func TestLauncherCredNamedIFSSurvivesBothPasses(t *testing.T) {
	dir := t.TempDir()
	deliverTree(t, dir, "IFS env\nSTRIPE_KEY env\n", map[string][]byte{
		"IFS":        []byte(":"),
		"STRIPE_KEY": []byte("sk_test"),
	})
	code, out := runLauncherCreds(t, dir, true, "5", `printf '%s' "$STRIPE_KEY"`)
	if code != 0 || out != "sk_test" {
		t.Fatalf("exit %d, out %q: a credential named IFS must not break either pass", code, out)
	}
}

// The seam snapshots are readonly: env.d is SOURCED into the launcher's
// shell between the two passes, and a hook that blanked BYRE_cred_expect
// would make the second pass a no-op (its own export then beats the
// credential), one that unset BYRE_cred_dir would kill the launcher on
// nounset. Both are refused; the credential still wins the collision.
func TestLauncherCredSnapshotsSurviveEnvd(t *testing.T) {
	dir := t.TempDir()
	deliverTree(t, dir, "STRIPE_KEY env\n", map[string][]byte{"STRIPE_KEY": []byte("from-credential")})
	td := t.TempDir()
	envd := filepath.Join(td, "env.d")
	if err := os.MkdirAll(envd, 0o755); err != nil {
		t.Fatal(err)
	}
	hook := "BYRE_cred_expect=\nunset BYRE_cred_dir\nBYRE_cred_wait=0\nexport STRIPE_KEY=from-envd\n"
	if err := os.WriteFile(filepath.Join(envd, "10-clobber.sh"), []byte(hook), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(td, "launcher.sh")
	if err := os.WriteFile(script, LauncherScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script, "bash", "-c", `printf '%s' "$STRIPE_KEY"`)
	cmd.Env = launcherEnv(
		"BYRE_LAUNCH_GATE_FILE="+filepath.Join(td, "no-such-gate"),
		"BYRE_FIRSTRUN_DIR="+filepath.Join(td, "no-firstrun"),
		"BYRE_ENVD_DIR="+envd,
		"BYRE_CRED_DIR="+dir,
		"BYRE_CRED_WAIT=5",
		"BYRE_CRED_EXPECT=1",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("launcher: %v (%s)", err, stderr.String())
	}
	if string(out) != "from-credential" {
		t.Fatalf("collision winner = %q, want the credential export (stderr: %s)", out, stderr.String())
	}
}

// A firstrun hook is a child process of the launcher, so it sees exactly the
// exports in force when it starts, and the credential pass runs above the hook
// loop: a login hook that stands down on an env credential sees the one the
// user delivered instead of prompting for it.
func TestLauncherCredExportVisibleToFirstrunHooks(t *testing.T) {
	dir := t.TempDir()
	deliverTree(t, dir, "STRIPE_KEY env\n", map[string][]byte{"STRIPE_KEY": []byte("sk-from-delivery")})
	marker := filepath.Join(t.TempDir(), "seen")
	code, out := runLauncherCreds(t, dir, true, "5", "true",
		firstrunDirWith(t, "#!/bin/sh\nprintf '%s' \"${STRIPE_KEY:-unset}\" > "+marker+"\n"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the hook never ran: %v (%s)", err, out)
	}
	if string(got) != "sk-from-delivery" {
		t.Fatalf("firstrun hook saw STRIPE_KEY=%q, want the delivered value", got)
	}
}

// The wait sits above the hook loop too, so a launch that fails closed on
// credentials fails before any hook runs: no login prompt opens on a box
// that is about to refuse to start.
func TestLauncherCredWaitPrecedesFirstrunHooks(t *testing.T) {
	dir := t.TempDir() // nothing delivered, no .done
	marker := filepath.Join(t.TempDir(), "hook-ran")
	code, out := runLauncherCreds(t, dir, true, "1", "true",
		firstrunDirWith(t, "#!/bin/sh\ntouch "+marker+"\n"))
	if code == 0 {
		t.Fatalf("a launch without its declared credentials must fail closed: %s", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("a firstrun hook ran on a launch that then failed closed on credentials:\n%s", out)
	}
}

// A manifest line the launcher cannot honor is a corrupt delivery, not a row
// to drop: dropping it would run the agent on a SUBSET of the declared
// credentials, which is the fail-open the wait above exists to prevent. Every
// arm exits, the agent never runs, and the message names the manifest LINE
// without echoing a byte of it — a manifest that is not byre's may itself be
// a credential's plaintext.
func TestLauncherManifestRejectionsFailClosed(t *testing.T) {
	secret := "sk-live-do-not-echo"
	for _, tc := range []struct {
		name     string
		manifest string
		values   map[string][]byte
		line     string
	}{
		{"reserved key", "BYRE_EGRESS env\n", map[string][]byte{"BYRE_EGRESS": []byte(secret)}, "line 1"},
		{"malformed key", "GOOD env\n" + secret + " env\n", map[string][]byte{"GOOD": []byte("z")}, "line 2"},
		{"unknown kind", "GOOD_ONE " + secret + "\n", map[string][]byte{"GOOD_ONE": []byte("z")}, "line 1"},
		{"value never landed", "GOOD_ONE env\n", nil, "line 1"},
		// A blank line is not a row to skip past: byre's composer writes one
		// "KEY kind" line per value and never an empty one, so a manifest
		// carrying one is not byre's -- and continuing would export the rest,
		// which is the partial credential set every other arm exits over.
		{"blank line", "GOOD_ONE env\n\nSECOND_KEY env\n",
			map[string][]byte{"GOOD_ONE": []byte("z"), "SECOND_KEY": []byte("z")}, "line 2"},
		{"empty manifest", "", nil, "line 0"},
		// A manifest cut mid-line: `read` ends the loop on the partial
		// record without failing, so the rows BEFORE it would export and
		// the agent would run on a subset. Line 0 is the assertion that
		// bites — only the whole-file check reports it, so a per-line rule
		// happening to reject the fragment cannot make this pass.
		{"truncated final line", "GOOD_ONE env\n" + secret + " en",
			map[string][]byte{"GOOD_ONE": []byte("z")}, "line 0"},
		// The same cut landing exactly on a record boundary: every per-line
		// rule passes, both values are on the tmpfs, and the only thing
		// separating this from a complete delivery is the missing newline —
		// and the rows the cut took away, which nothing in the file names.
		{"truncated on a record boundary", "GOOD_ONE env\nSECOND_KEY env",
			map[string][]byte{"GOOD_ONE": []byte("z"), "SECOND_KEY": []byte("z")}, "line 0"},
		// A NUL byre never writes, with an unterminated record behind it: the
		// whole-file read stops AT the NUL, so a check that judged only what it
		// read would pass on the prefix while the loop dropped the record after
		// it -- and `read` discards NULs, so every per-line rule passes too.
		// The agent would have run on GOOD_ONE alone.
		{"NUL before an unterminated record", "GOOD_ONE env\n\x00" + secret + " env",
			map[string][]byte{"GOOD_ONE": []byte("z")}, "line 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			deliverTree(t, dir, tc.manifest, tc.values)
			code, out := runLauncherCreds(t, dir, true, "5", `printf 'ran:%s' "${GOOD_ONE:-unset}"`)
			if code == 0 || strings.Contains(out, "ran:") {
				t.Fatalf("the agent must never run on a corrupt manifest: exit %d out %q", code, out)
			}
			if !strings.Contains(out, tc.line) || !strings.Contains(out, "failing closed") ||
				!strings.Contains(out, "--credentials=skip") {
				t.Fatalf("the refusal must name the line and the remedy: %q", out)
			}
			if strings.Contains(out, secret) {
				t.Fatalf("the refusal echoed manifest content: %q", out)
			}
		})
	}
}

// End-to-end: the host-side frame format (as composeStream in commands will
// build it) through the real receiver, then the real launcher export.
func TestReceiverThenLauncherRoundtrip(t *testing.T) {
	dir := t.TempDir()
	value := []byte("tok-abc")
	stream := fmt.Sprintf("byre-credentials 1\nitem manifest\n%s\nitem GH_TOKEN\n%s\ndone\n",
		b64([]byte("GH_TOKEN env\n")), b64(value))
	if code, out := runReceiver(t, dir, stream); code != 0 {
		t.Fatalf("receiver exit %d: %s", code, out)
	}
	code, out := runLauncherCreds(t, dir, true, "5", `printf '%s' "$GH_TOKEN"`)
	if code != 0 || out != "tok-abc" {
		t.Fatalf("roundtrip: exit %d out %q", code, out)
	}
}

// TestReceiverNameGrammarMatchesEnvKeys pins the receiver's bash restatement
// byte-identical to the Go owner — the clock-pin pattern for a rule that
// must exist in two languages. A delivered item now travels under its CONFIG
// KEY, so the rule it restates is the env-var-name grammar.
func TestReceiverNameGrammarMatchesEnvKeys(t *testing.T) {
	if !strings.Contains(string(ReceiverScript()), config.EnvKeyGrammar) {
		t.Fatalf("credential-receiver.sh no longer restates the env key grammar %q byte-identically — the two spellings have drifted", config.EnvKeyGrammar)
	}
}

// The launcher restates the same grammar, for the same reason: it decides
// which manifest key becomes an exported variable.
func TestLauncherExportKeyGrammarMatchesEnvKeys(t *testing.T) {
	if !strings.Contains(string(LauncherScript()), config.EnvKeyGrammar) {
		t.Fatalf("launcher.sh no longer restates the env key grammar %q byte-identically — the two spellings have drifted", config.EnvKeyGrammar)
	}
}

// A credential may be named after any shell variable the launcher itself
// keeps, and its export attribute survives a later plain assignment -- so the
// launcher's own state is BYRE_-named (which the key check refuses) and a key
// spelled like one of them is just a key. Here the whole delivery is spelled
// after launcher state the loop, the firstrun loop and the exec once used, and
// both the hook and the agent must see the DELIVERED values. The names are
// BYRE_-prefixed by TestLauncherStateIsAllByrePrefixed; this is what that
// rule buys.
func TestLauncherCredNamedLikeLauncherStateSurvives(t *testing.T) {
	dir := t.TempDir()
	deliverTree(t, dir,
		"cred_lineno env\ncred_exported env\ncred_key env\ncred_val env\nFIRSTRUN_DIR env\nhook env\nCMD env\n",
		map[string][]byte{
			// "9x" is the one that killed the launcher outright: read back as
			// the loop's counter it was evaluated as arithmetic, which under
			// set -u exits and echoes part of the value in bash's own error.
			"cred_lineno":   []byte("9x"),
			"cred_exported": []byte("7y"),
			"cred_key":      []byte("k-secret"),
			"cred_val":      []byte("v-secret"),
			"FIRSTRUN_DIR":  []byte("fd-secret"),
			"hook":          []byte("hook-secret"),
			"CMD":           []byte("cmd-secret"),
		})
	marker := filepath.Join(t.TempDir(), "seen")
	code, out := runLauncherCreds(t, dir, true, "5",
		`printf '%s|%s|%s|%s|%s' "$cred_lineno" "$cred_exported" "$cred_key" "$cred_val" "${CMD-unset}"`,
		firstrunDirWith(t, "printf '%s|%s' \"$FIRSTRUN_DIR\" \"$hook\" > "+marker+"\n"))
	want := "9x|7y|k-secret|v-secret|cmd-secret"
	if code != 0 || out != want {
		t.Fatalf("exit %d, agent saw %q, want exit 0 and %q", code, out, want)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the hook never ran: %v (%s)", err, out)
	}
	if string(got) != "fd-secret|hook-secret" {
		t.Fatalf("firstrun hook saw %q, want the delivered values", got)
	}
}

// A name bash itself owns cannot carry a credential faithfully (the taxonomy
// is on config.bashOwnedCredentialNames), so the launcher refuses the delivery
// as corrupt: the agent never runs, the message names the line, and the value
// never appears. MEMBERSHIP is the pin's job (the case arm is byte-identical
// to the Go list, TestLauncherBashOwnedListMatchesConfig); these three are one
// name per failure mode that reaches this path -- arithmetic, readonly, and
// one of the two that export cleanly past the read-back backstop.
func TestLauncherRefusesBashOwnedCredentialNames(t *testing.T) {
	secret := "sk live+do-not-echo"
	for _, k := range []string{"SECONDS", "UID", "SHLVL"} {
		t.Run(k, func(t *testing.T) {
			dir := t.TempDir()
			deliverTree(t, dir, "GOOD_ONE env\n"+k+" env\n",
				map[string][]byte{"GOOD_ONE": []byte("z"), k: []byte(secret)})
			code, out := runLauncherCreds(t, dir, true, "5", `printf 'ran:%s' "${GOOD_ONE:-unset}"`)
			if code == 0 || strings.Contains(out, "ran:") {
				t.Fatalf("the agent must never run on a bash-owned key: exit %d out %q", code, out)
			}
			if !strings.Contains(out, "line 2") || !strings.Contains(out, "a name bash itself owns") ||
				!strings.Contains(out, "failing closed") {
				t.Fatalf("the refusal must name the line and the reason: %q", out)
			}
			if strings.Contains(out, secret) || strings.Contains(out, "do-not-echo") {
				t.Fatalf("the refusal echoed the value: %q", out)
			}
		})
	}
	// The list is exact, not a prefix or substring match: a user's own key that
	// merely starts with "BASH", or merely contains an owned name, is
	// delivered.
	for _, k := range []string{"BASHFUL_KEY", "MY_SECONDS"} {
		t.Run(k, func(t *testing.T) {
			dir := t.TempDir()
			deliverTree(t, dir, k+" env\n", map[string][]byte{k: []byte(secret)})
			code, out := runLauncherCreds(t, dir, true, "5", `printf 'ran:%s' "$`+k+`"`)
			if code != 0 || !strings.Contains(out, "ran:"+secret) {
				t.Fatalf("%s is not bash's and must be delivered: exit %d out %q", k, code, out)
			}
		})
	}
}

// The denylist is a list, so the export behind it is checked too: whatever
// the list misses must still fail CLOSED, naming the line and not the value.
// `_` is not on the list -- bash rewrites it after every command, so its
// read-back never matches -- which makes it the probe for that backstop. The
// wording is the export failure's own: byre wrote the key, so the refusal does
// not blame the manifest.
func TestLauncherCredExportBackstopFailsClosed(t *testing.T) {
	secret := "sk-live-do-not-echo"
	dir := t.TempDir()
	deliverTree(t, dir, "GOOD_ONE env\n_ env\n",
		map[string][]byte{"GOOD_ONE": []byte("z"), "_": []byte(secret)})
	code, out := runLauncherCreds(t, dir, true, "5", `printf 'ran:%s' "${GOOD_ONE:-unset}"`)
	if code == 0 || strings.Contains(out, "ran:") {
		t.Fatalf("the agent must never run on an export that did not survive: exit %d out %q", code, out)
	}
	if !strings.Contains(out, "line 2") || !strings.Contains(out, "could not be exported") {
		t.Fatalf("the refusal must name the line and the reason: %q", out)
	}
	if strings.Contains(out, "manifest") {
		t.Fatalf("an export failure is not the manifest's fault: %q", out)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("the refusal echoed the value: %q", out)
	}
}

// An env.d hook that makes a credential name readonly (or gives it an
// attribute) is the field shape of that backstop: the FIRST pass exports
// cleanly, the hook runs, and the second pass meets a name bash will not let
// it export. Fails closed, with the export failure's wording rather than a
// refusal that blames the delivery byre itself wrote.
func TestLauncherCredExportFailureAfterEnvdNamesTheLine(t *testing.T) {
	secret := "sk-live-do-not-echo"
	dir := t.TempDir()
	deliverTree(t, dir, "GOOD_ONE env\nSTRIPE_KEY env\n",
		map[string][]byte{"GOOD_ONE": []byte("z"), "STRIPE_KEY": []byte(secret)})
	td := t.TempDir()
	envd := filepath.Join(td, "env.d")
	if err := os.MkdirAll(envd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envd, "10-readonly.sh"), []byte("readonly STRIPE_KEY\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(td, "launcher.sh")
	if err := os.WriteFile(script, LauncherScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script, "bash", "-c", `printf 'ran:%s' "${GOOD_ONE:-unset}"`)
	cmd.Env = launcherEnv(
		"BYRE_LAUNCH_GATE_FILE="+filepath.Join(td, "no-such-gate"),
		"BYRE_FIRSTRUN_DIR="+filepath.Join(td, "no-firstrun"),
		"BYRE_ENVD_DIR="+envd,
		"BYRE_CRED_DIR="+dir,
		"BYRE_CRED_WAIT=5",
		"BYRE_CRED_EXPECT=1",
	)
	out, err := cmd.CombinedOutput()
	if err == nil || strings.Contains(string(out), "ran:") {
		t.Fatalf("a credential bash will not export must fail the launch closed: %v %q", err, out)
	}
	if !strings.Contains(string(out), "line 2") || !strings.Contains(string(out), "could not be exported") {
		t.Fatalf("the refusal must name the line and the reason: %q", out)
	}
	if strings.Contains(string(out), "manifest") || strings.Contains(string(out), secret) {
		t.Fatalf("the refusal must not blame the manifest or echo the value: %q", out)
	}
}

// A value file that exists but cannot be READ fails the launch closed, like
// one that never landed at all. The read-to-EOF the env kind uses returns
// non-zero on every successful read of a NUL-free value, so the "|| true" that
// absorbs that cannot tell an open failure from a value either: without the
// open check the row exported the EMPTY string and the agent launched on it.
func TestLauncherUnreadableValueFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the mode this case relies on")
	}
	secret := "sk-live-do-not-echo"
	dir := t.TempDir()
	deliverTree(t, dir, "GOOD_ONE env\nSTRIPE_KEY env\n",
		map[string][]byte{"GOOD_ONE": []byte("z"), "STRIPE_KEY": []byte(secret)})
	if err := os.Chmod(filepath.Join(dir, "credentials", "STRIPE_KEY"), 0o000); err != nil {
		t.Fatal(err)
	}
	code, out := runLauncherCreds(t, dir, true, "5", `printf 'ran:%s' "${STRIPE_KEY-unset}"`)
	if code == 0 || strings.Contains(out, "ran:") {
		t.Fatalf("an unreadable value must not reach the agent: exit %d out %q", code, out)
	}
	if !strings.Contains(out, "line 2") || !strings.Contains(out, "could not be read") {
		t.Fatalf("the refusal must name the line and the reason: %q", out)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("the refusal echoed the value: %q", out)
	}
}

// PATH is a name a credential may carry (byre discloses that, it does not
// reserve it), and the SECOND pass runs with every delivered value already
// exported -- so any external command the export loop used was one a delivered
// value could make unfindable. The truncation refusal used `tail`: with PATH
// re-pointed its substitution came back empty, the check passed vacuously, and
// a manifest shortened between the passes exported only the rows before the cut
// and launched the agent on them. Staged exactly that way: PATH is delivered as
// a directory holding nothing but bash (so the hook and the agent still run,
// and `tail` does not resolve), and a firstrun hook strips the manifest's final
// newline between the two passes -- the session tmpfs is dev-owned, so first-run
// code can do this, which is why the second pass re-validates at all.
func TestLauncherTruncationCheckSurvivesAPathCredential(t *testing.T) {
	realBash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH")
	}
	bin := t.TempDir()
	if err := os.Symlink(realBash, filepath.Join(bin, "bash")); err != nil {
		t.Fatal(err)
	}
	secret := "sk-live-do-not-echo"
	dir := t.TempDir()
	deliverTree(t, dir, "PATH env\nSTRIPE_KEY env\n",
		map[string][]byte{"PATH": []byte(bin), "STRIPE_KEY": []byte(secret)})
	// Builtins only: the hook inherits the delivered PATH too.
	hook := "m=\"$BYRE_CRED_DIR/manifest\"\ns=\"\"\nIFS= read -rd '' s <\"$m\" || true\nprintf '%s' \"${s%$'\\n'}\" >\"$m\"\n"
	code, out := runLauncherCreds(t, dir, true, "5", `printf 'ran:%s' "${STRIPE_KEY:-unset}"`,
		firstrunDirWith(t, hook))
	if code == 0 || strings.Contains(out, "ran:") {
		t.Fatalf("a manifest truncated between the passes must fail the launch closed: exit %d out %q", code, out)
	}
	if !strings.Contains(out, "line 0") || !strings.Contains(out, "truncated") {
		t.Fatalf("the refusal must name the whole-file rule: %q", out)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("the refusal echoed the value: %q", out)
	}
}

// Every variable the launcher binds from byre_credentials_apply's own body to
// the exec is BYRE_-named: a credential's export attribute survives a later
// plain assignment, so an unprefixed name there would hand a hook or the agent
// the launcher's own value in place of a delivered credential of that name, and
// could be read back as launcher state. The scan starts at the function
// DEFINITION rather than its first call -- the export loop's own counters live
// in the body, which sits ABOVE that call, so a scan from the call left them
// unpinned. The behavioural case covers the names that have gone wrong; this is
// what keeps a new one out, since a fresh unprefixed loop variable would stay
// green there.
func TestLauncherStateIsAllByrePrefixed(t *testing.T) {
	src := string(LauncherScript())
	body := strings.Index(src, "\nbyre_credentials_apply() {\n")
	if body < 0 {
		t.Fatal("launcher.sh no longer defines byre_credentials_apply")
	}
	if !strings.Contains(src, "\nbyre_credentials_apply\n") {
		t.Fatal("launcher.sh no longer calls byre_credentials_apply at top level")
	}
	// SECONDS, which the wait loop resets, is the one exception and needs no
	// prefix: bash owns the name, so config.BashOwnsName refuses it at `byre
	// credentials set` and the export loop refuses the delivery -- no credential
	// can be carrying it when the loop assigns it.
	allowed := map[string]bool{"SECONDS": true}
	// `IFS= read ...` and `IFS=' ' read ...` are command-PREFIX assignments:
	// they bind IFS for that one command and nothing else, which is how the
	// export loop keeps an INHERITED IFS (a credential may legally be named
	// IFS) out of its own reads. A persistent `IFS=...` on a line of its own is
	// a different thing and still a hole, so the exemption insists a command
	// follows on the same line.
	cmdPrefix := regexp.MustCompile(`^\s*IFS=(?:'[^']*')?\s+\S`)
	assign := regexp.MustCompile(`^\s*(?:readonly\s+|export\s+|local\s+)?([A-Za-z_][A-Za-z0-9_]*)=`)
	loop := regexp.MustCompile(`^\s*for\s+([A-Za-z_][A-Za-z0-9_]*)\s`)
	for _, line := range strings.Split(src[body+1:], "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") || cmdPrefix.MatchString(line) {
			continue
		}
		for _, re := range []*regexp.Regexp{assign, loop} {
			m := re.FindStringSubmatch(line)
			if m == nil || strings.HasPrefix(m[1], "BYRE_") || allowed[m[1]] {
				continue
			}
			t.Errorf("launcher.sh binds %q at or after byre_credentials_apply's definition — every name it keeps from there to the exec must be BYRE_-prefixed, which the credential key check refuses (%q)", m[1], strings.TrimSpace(line))
		}
	}
}

// The bash-owned list exists in two languages: config refuses it at
// `byre credentials set`, the launcher refuses it at delivery. The launcher's
// case arm is pinned byte-identical to the Go owner (the EnvKeyGrammar
// precedent), so the two cannot drift.
func TestLauncherBashOwnedListMatchesConfig(t *testing.T) {
	want := "    " + config.BashOwnedCredentialPattern() + ")\n"
	if !strings.Contains(string(LauncherScript()), want) {
		t.Fatalf("launcher.sh no longer restates config.BashOwnedCredentialPattern %q as its case arm byte-identically — the two lists have drifted", config.BashOwnedCredentialPattern())
	}
}
