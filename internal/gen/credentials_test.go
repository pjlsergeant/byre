package gen

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	cmd.Env = append(os.Environ(), "BYRE_CRED_DIR="+dir)
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

// runLauncherCreds drives the real launcher with the credential env seams
// set and a command that prints the export targets, so the test observes
// exactly what the agent process would see.
func runLauncherCreds(t *testing.T, dir string, expect bool, wait, printCmd string) (int, string) {
	t.Helper()
	td := t.TempDir()
	script := filepath.Join(td, "launcher.sh")
	if err := os.WriteFile(script, LauncherScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script, "bash", "-c", printCmd)
	env := append(os.Environ(),
		"BYRE_LAUNCH_GATE_FILE="+filepath.Join(td, "no-such-gate"),
		"BYRE_FIRSTRUN_DIR="+filepath.Join(td, "no-firstrun"),
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
	// The export step runs AFTER the env.d loop, so a credential target
	// beats an env.d hook exporting the same variable.
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
	cmd.Env = append(os.Environ(),
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
