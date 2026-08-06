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
		"item manifest\n" + b64([]byte("stripe env STRIPE_KEY\n")) + "\n" +
		"item stripe\n" + b64(value) + "\n" +
		"done\n"
	code, out := runReceiver(t, dir, stream)
	if code != 0 {
		t.Fatalf("receiver exit %d: %s", code, out)
	}
	got, err := os.ReadFile(filepath.Join(dir, "credentials", "stripe"))
	if err != nil || !bytes.Equal(got, value) {
		t.Fatalf("value roundtrip: %v %q", err, got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".done")); err != nil {
		t.Fatalf(".done sentinel: %v", err)
	}
	// Files land private to the dev uid (umask 077).
	fi, _ := os.Stat(filepath.Join(dir, "credentials", "stripe"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("value file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestReceiverIncompleteStreamLeavesNoSentinel(t *testing.T) {
	dir := t.TempDir()
	stream := "byre-credentials 1\n" +
		"item stripe\n" + b64([]byte("v")) + "\n" // EOF without done
	code, _ := runReceiver(t, dir, stream)
	if code == 0 {
		t.Fatal("incomplete stream must not exit 0")
	}
	if _, err := os.Stat(filepath.Join(dir, ".done")); !os.IsNotExist(err) {
		t.Fatal("incomplete stream must leave no .done — the launcher's wait is the fail-open")
	}
}

func TestReceiverRefusesUnknownVersionAndBadNames(t *testing.T) {
	dir := t.TempDir()
	if code, out := runReceiver(t, dir, "byre-credentials 999\n"); code == 0 || !strings.Contains(out, "not a credential stream") {
		t.Fatalf("unknown version: exit %d out %q", code, out)
	}
	bad := "byre-credentials 1\nitem ../escape\n" + b64([]byte("v")) + "\ndone\n"
	if code, out := runReceiver(t, dir, bad); code == 0 || !strings.Contains(out, "malformed item name") {
		t.Fatalf("bad name: exit %d out %q", code, out)
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
		"stripe env STRIPE_KEY\ncert file TLS_CERT_PATH\n",
		map[string][]byte{"stripe": []byte("sk-123\n"), "cert": []byte("PEM")})
	code, out := runLauncherCreds(t, dir, true, "5",
		`printf '%s|%s' "$STRIPE_KEY" "$TLS_CERT_PATH"`)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	// Byte-exact env export (the trailing newline survives — $(cat) would
	// eat it); file kind exports the tmpfs path.
	want := "sk-123\n|" + filepath.Join(dir, "credentials", "cert")
	if out != want {
		t.Fatalf("agent saw %q, want %q", out, want)
	}
}

func TestLauncherCredWaitFailsOpen(t *testing.T) {
	dir := t.TempDir() // nothing delivered, no .done
	code, out := runLauncherCreds(t, dir, true, "1", `printf 'ran:%s' "${STRIPE_KEY:-unset}"`)
	if code != 0 {
		t.Fatalf("fail-open launch must still run the agent; exit %d: %s", code, out)
	}
	if !strings.Contains(out, "ran:unset") {
		t.Fatalf("agent output: %q", out)
	}
	if !strings.Contains(out, "launching without them") {
		t.Fatalf("the fail-open must be said out loud: %q", out)
	}
}

func TestLauncherNoExpectNoWait(t *testing.T) {
	// Without the flag the launcher must not wait at all — declined,
	// non-TTY, and empty-vault launches cost nothing.
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
	deliverTree(t, dir, "stripe env STRIPE_KEY\n", map[string][]byte{"stripe": []byte("from-vault")})
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
	if string(out) != "from-vault" {
		t.Fatalf("collision winner = %q, want the credential export", out)
	}
}

func TestLauncherSkipsMalformedManifestTargets(t *testing.T) {
	dir := t.TempDir()
	deliverTree(t, dir,
		"a env BYRE_EGRESS\nb env not a var\nc env GOOD_ONE\n",
		map[string][]byte{"a": []byte("x"), "b": []byte("y"), "c": []byte("z")})
	code, out := runLauncherCreds(t, dir, true, "5",
		`printf '%s|%s' "${BYRE_EGRESS:-safe}" "$GOOD_ONE"`)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.HasSuffix(out, "safe|z") {
		t.Fatalf("agent saw %q, want reserved/malformed skipped and the good row exported", out)
	}
	if c := strings.Count(out, "skipping malformed export target"); c != 2 {
		t.Fatalf("want 2 skip notices, got %d in %q", c, out)
	}
}

// End-to-end: the host-side frame format (as composeStream in commands will
// build it) through the real receiver, then the real launcher export.
func TestReceiverThenLauncherRoundtrip(t *testing.T) {
	dir := t.TempDir()
	value := []byte("tok-abc")
	stream := fmt.Sprintf("byre-credentials 1\nitem manifest\n%s\nitem github\n%s\ndone\n",
		b64([]byte("github env GH_TOKEN\n")), b64(value))
	if code, out := runReceiver(t, dir, stream); code != 0 {
		t.Fatalf("receiver exit %d: %s", code, out)
	}
	code, out := runLauncherCreds(t, dir, true, "5", `printf '%s' "$GH_TOKEN"`)
	if code != 0 || out != "tok-abc" {
		t.Fatalf("roundtrip: exit %d out %q", code, out)
	}
}
