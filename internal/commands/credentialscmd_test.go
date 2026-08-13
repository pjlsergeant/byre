package commands

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/credentials"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/lock"
	"github.com/pjlsergeant/byre/internal/project"
)

// openCredRow reads one config file's credential row back the way a launch
// does: parse the file's own block, unwrap it, decrypt the row.
func openCredRow(t *testing.T, path, passphrase, key string) ([]byte, credentials.Kind) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, ok, err := config.ParseCredentialsBlock(raw)
	if err != nil || !ok {
		t.Fatalf("%s carries no usable [credentials] block: ok=%v err=%v", path, ok, err)
	}
	cfg, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	row, isCred, err := config.ParseEncryptedRow(key, cfg.EnvFromHost[key])
	if err != nil || !isCred {
		t.Fatalf("%s has no credential row %s: %v", path, key, err)
	}
	id, err := credentials.UnwrapIdentity(block.Identity, passphrase)
	if err != nil {
		t.Fatalf("unwrap %s: %v", path, err)
	}
	value, outcome, err := id.DecryptValue(row.Key, row.Kind, row.Blob)
	if err != nil {
		t.Fatalf("decrypt %s: %s %v", key, outcome, err)
	}
	return value, row.Kind
}

func projectConfigPath(t *testing.T, proj string) string {
	t.Helper()
	p, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(p.Dir, config.ProjectConfigName)
}

// The first set mints the file's identity and writes both the block and the
// row; the value round-trips through the config file alone — there is no
// second store.
func TestCredentialsSetMintsTheIdentityAndWritesTheRow(t *testing.T) {
	_, proj := testPaths(t)
	passphraseSeam(t, "pw", "pw", "sk-live-9") // new passphrase, confirm, value
	var errBuf bytes.Buffer
	s := ttyStreams(&errBuf)
	if err := CredentialsSet(s, proj, "STRIPE_KEY", false, ""); err != nil {
		t.Fatalf("set: %v", err)
	}
	if !strings.Contains(errBuf.String(), "minted this file's credentials identity") {
		t.Fatalf("identity notice: %s", errBuf.String())
	}
	path := projectConfigPath(t, proj)
	value, kind := openCredRow(t, path, "pw", "STRIPE_KEY")
	if string(value) != "sk-live-9" || kind != credentials.KindEnv {
		t.Fatalf("round trip: %q %s", value, kind)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "[env_from_host]") || !strings.Contains(string(raw), config.EncryptedScheme) {
		t.Fatalf("the row must live in env_from_host under the scheme:\n%s", raw)
	}
	if strings.Contains(string(raw), "sk-live-9") {
		t.Fatal("the config must never carry the plaintext")
	}

	// A second set on the same file is a COLD write: the recipient is
	// cleartext, so nothing asks for the passphrase again.
	passphraseSeam(t, "tok")
	if err := CredentialsSet(s, proj, "GH_TOKEN", false, ""); err != nil {
		t.Fatalf("second set: %v", err)
	}
	if v, _ := openCredRow(t, path, "pw", "GH_TOKEN"); string(v) != "tok" {
		t.Fatalf("second row: %q", v)
	}
}

// --file writes the encrypted-file: scheme, and the kind is stamped into the
// payload so it cannot be delivered as the other one.
func TestCredentialsSetFileKind(t *testing.T) {
	_, proj := testPaths(t)
	var errBuf bytes.Buffer
	passphraseSeam(t, "pw", "pw")
	piped := Streams{Out: io.Discard, Err: &errBuf, In: strings.NewReader("-----END CERT-----\n"), TTY: false}
	tty := ttyStreams(&errBuf)
	// Minting needs a terminal, so seed the identity with a TTY set first.
	passphraseSeam(t, "pw", "pw", "seed")
	if err := CredentialsSet(tty, proj, "SEED", false, ""); err != nil {
		t.Fatal(err)
	}
	if err := CredentialsSet(piped, proj, "TLS_CERT", true, ""); err != nil {
		t.Fatalf("file set: %v", err)
	}
	path := projectConfigPath(t, proj)
	// A file value is arbitrary bytes: the env newline courtesy must not
	// mutate a PEM's final newline.
	value, kind := openCredRow(t, path, "pw", "TLS_CERT")
	if string(value) != "-----END CERT-----\n" || kind != credentials.KindFile {
		t.Fatalf("file-kind bytes: %q %s", value, kind)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), config.EncryptedFileScheme) {
		t.Fatalf("file rows carry the file scheme:\n%s", raw)
	}
}

// Piped stdin is the `op read ... | byre credentials set KEY` shape, with one
// trailing newline stripped for an env value.
func TestCredentialsSetPipedStdinStripsOneNewline(t *testing.T) {
	_, proj := testPaths(t)
	var errBuf bytes.Buffer
	passphraseSeam(t, "pw", "pw", "seed")
	if err := CredentialsSet(ttyStreams(&errBuf), proj, "SEED", false, ""); err != nil {
		t.Fatal(err)
	}
	piped := Streams{Out: io.Discard, Err: &errBuf, In: strings.NewReader("tok-from-pipe\n"), TTY: false}
	if err := CredentialsSet(piped, proj, "GH_TOKEN", false, ""); err != nil {
		t.Fatalf("piped set: %v", err)
	}
	if v, _ := openCredRow(t, projectConfigPath(t, proj), "pw", "GH_TOKEN"); string(v) != "tok-from-pipe" {
		t.Fatalf("piped value: %q", v)
	}
}

func TestCredentialsSetRefusals(t *testing.T) {
	_, proj := testPaths(t)
	var errBuf bytes.Buffer
	s := ttyStreams(&errBuf)
	// The key is held to the env_from_host rules BEFORE anything is prompted.
	if err := CredentialsSet(s, proj, "not a var", false, ""); err == nil ||
		!strings.Contains(err.Error(), "not a valid environment variable name") || !strings.Contains(err.Error(), "not a var") {
		t.Fatalf("bad key: %v", err)
	}
	if err := CredentialsSet(s, proj, "BYRE_EGRESS", false, ""); err == nil ||
		!strings.Contains(err.Error(), "BYRE_ namespace") {
		t.Fatalf("reserved key: %v", err)
	}
	// The delivery stream's own reserved item name: a legal env var name, so
	// only this rule keeps a credential off it. Refused before any prompt.
	if err := CredentialsSet(s, proj, config.ReservedCredentialItem, false, ""); err == nil ||
		!strings.Contains(err.Error(), "reserved") ||
		!strings.Contains(err.Error(), config.ReservedCredentialItem) {
		t.Fatalf("reserved manifest key: %v", err)
	}
	// Minting an identity needs a terminal — a passphrase never rides a pipe.
	nonTTY := Streams{Out: io.Discard, Err: &errBuf, In: strings.NewReader("v\n"), TTY: false}
	if err := CredentialsSet(nonTTY, proj, "A", false, ""); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("non-TTY mint: %v", err)
	}
	// Mismatched confirmation and an empty passphrase abort with nothing
	// written.
	passphraseSeam(t, "pw", "other")
	if err := CredentialsSet(s, proj, "A", false, ""); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("mismatch: %v", err)
	}
	passphraseSeam(t, "")
	if err := CredentialsSet(s, proj, "A", false, ""); err == nil || !strings.Contains(err.Error(), "empty passphrase") {
		t.Fatalf("empty passphrase: %v", err)
	}
	// An empty VALUE is refused too — unset is how a row goes away.
	passphraseSeam(t, "pw", "pw", "")
	if err := CredentialsSet(s, proj, "A", false, ""); err == nil || !strings.Contains(err.Error(), "empty value") {
		t.Fatalf("empty value: %v", err)
	}
	if _, err := os.Stat(projectConfigPath(t, proj)); err == nil {
		raw, _ := os.ReadFile(projectConfigPath(t, proj))
		if strings.Contains(string(raw), "[credentials]") {
			t.Fatalf("an aborted set must write nothing:\n%s", raw)
		}
	}
}

// An env value must survive the launcher's export byte-exactly, so NUL bytes
// and the 64 KiB env cap are refused at set, where re-entry is cheap.
func TestCredentialsSetHoldsEnvValuesToTheirKind(t *testing.T) {
	_, proj := testPaths(t)
	var errBuf bytes.Buffer
	passphraseSeam(t, "pw", "pw", "seed")
	if err := CredentialsSet(ttyStreams(&errBuf), proj, "SEED", false, ""); err != nil {
		t.Fatal(err)
	}
	nul := Streams{Out: io.Discard, Err: &errBuf, In: strings.NewReader("a\x00b"), TTY: false}
	if err := CredentialsSet(nul, proj, "A", false, ""); err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("NUL in an env value: %v", err)
	}
	big := Streams{Out: io.Discard, Err: &errBuf, In: strings.NewReader(strings.Repeat("x", credentials.MaxEnvValue+1)), TTY: false}
	if err := CredentialsSet(big, proj, "A", false, ""); err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("oversize env value: %v", err)
	}
	// The same bytes are legal as a FILE value, up to the larger ceiling.
	nul2 := Streams{Out: io.Discard, Err: &errBuf, In: strings.NewReader("a\x00b"), TTY: false}
	if err := CredentialsSet(nul2, proj, "BLOB", true, ""); err != nil {
		t.Fatalf("NUL in a file value must be fine: %v", err)
	}
}

func TestCredentialsUnsetRemovesTheRowAndItsCiphertext(t *testing.T) {
	_, proj := testPaths(t)
	var errBuf bytes.Buffer
	s := ttyStreams(&errBuf)
	passphraseSeam(t, "pw", "pw", "v")
	if err := CredentialsSet(s, proj, "STRIPE_KEY", false, ""); err != nil {
		t.Fatal(err)
	}
	if err := CredentialsUnset(s, proj, "STRIPE_KEY", ""); err != nil {
		t.Fatalf("unset: %v", err)
	}
	raw, _ := os.ReadFile(projectConfigPath(t, proj))
	if strings.Contains(string(raw), "STRIPE_KEY") {
		t.Fatalf("the row must be gone:\n%s", raw)
	}
	// The identity stays: other rows in the file need it.
	if !strings.Contains(string(raw), "[credentials]") {
		t.Fatalf("the file's identity must survive an unset:\n%s", raw)
	}
	if !strings.Contains(errBuf.String(), "ciphertext went with the row") {
		t.Fatalf("the discard must be said out loud: %s", errBuf.String())
	}
	if err := CredentialsUnset(s, proj, "STRIPE_KEY", ""); err == nil || !strings.Contains(err.Error(), "no STRIPE_KEY row") {
		t.Fatalf("second unset: %v", err)
	}
}

// unset is a credential verb: an ordinary env_from_host source is not its to
// remove.
func TestCredentialsUnsetRefusesAPlainSource(t *testing.T) {
	_, proj := testPaths(t)
	path := projectConfigPath(t, proj)
	p, _ := project.Resolve(proj)
	if err := p.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if err := config.AtomicWrite(path, "[env_from_host]\nTERM = \"env:TERM\"\n"); err != nil {
		t.Fatal(err)
	}
	var errBuf bytes.Buffer
	err := CredentialsUnset(ttyStreams(&errBuf), proj, "TERM", "")
	if err == nil || !strings.Contains(err.Error(), "not a credential") || !strings.Contains(err.Error(), "env:TERM") {
		t.Fatalf("want a refusal naming the rule and the value: %v", err)
	}
}

// rekey rotates the PASSPHRASE only: the identity is the same, so every value
// row is byte-identical afterwards — which is what lets drift compare
// credential rows as plain bytes.
func TestCredentialsRekeyLeavesValueRowsByteIdentical(t *testing.T) {
	_, proj := testPaths(t)
	var errBuf bytes.Buffer
	s := ttyStreams(&errBuf)
	passphraseSeam(t, "pw", "pw", "v1")
	if err := CredentialsSet(s, proj, "A", false, ""); err != nil {
		t.Fatal(err)
	}
	path := projectConfigPath(t, proj)
	before, _ := config.ParseFile(path, false)

	passphraseSeam(t, "pw", "new", "new")
	if err := CredentialsRekey(s, proj, ""); err != nil {
		t.Fatalf("rekey: %v", err)
	}
	after, _ := config.ParseFile(path, false)
	if before.EnvFromHost["A"] != after.EnvFromHost["A"] {
		t.Fatal("a rekey must leave value rows byte-identical")
	}
	if v, _ := openCredRow(t, path, "new", "A"); string(v) != "v1" {
		t.Fatalf("value after rekey: %q", v)
	}
	raw, _ := os.ReadFile(path)
	block, _, _ := config.ParseCredentialsBlock(raw)
	if _, err := credentials.UnwrapIdentity(block.Identity, "pw"); !errors.Is(err, credentials.ErrBadPassphrase) {
		t.Fatalf("the old passphrase must stop working: %v", err)
	}
}

func TestCredentialsRekeyRefusals(t *testing.T) {
	_, proj := testPaths(t)
	var errBuf bytes.Buffer
	// No identity in the file: nothing to rekey.
	if err := CredentialsRekey(ttyStreams(&errBuf), proj, ""); err == nil ||
		!strings.Contains(err.Error(), "no credentials identity") {
		t.Fatalf("rekey without an identity: %v", err)
	}
	// A passphrase never rides a pipe.
	nonTTY := Streams{Out: io.Discard, Err: &errBuf, In: strings.NewReader("pw\n"), TTY: false}
	if err := CredentialsRekey(nonTTY, proj, ""); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("non-TTY rekey: %v", err)
	}
}

// The compare-and-swap: a write planned against one state of the file must
// not land on another. The race it closes is a `set` encrypting to recipient
// R while a concurrent identity replacement lands R2 — both writes
// "succeed", and the row is permanently undecryptable.
func TestCredentialWritesCompareAndSwap(t *testing.T) {
	_, proj := testPaths(t)
	var errBuf bytes.Buffer
	s := ttyStreams(&errBuf)
	passphraseSeam(t, "pw", "pw", "v1")
	if err := CredentialsSet(s, proj, "A", false, ""); err != nil {
		t.Fatal(err)
	}
	path := projectConfigPath(t, proj)
	beforeRaw, _ := os.ReadFile(path)

	// The second writer lands while the first is still at its value prompt —
	// after the first read the file and captured its recipient.
	old := readPassphrase
	t.Cleanup(func() { readPassphrase = old })
	landed := false
	readPassphrase = func(w io.Writer, prompt string) (string, error) {
		if !landed {
			landed = true
			// A whole new identity, exactly the state that makes the first
			// writer's blob undecryptable if its write is allowed to land.
			wrapped, recipient, err := credentials.NewIdentity("other")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(path)
			cfg, _ := config.Parse(raw)
			_ = cfg
			replaced := strings.Replace(string(raw), string(mustBlockIdentity(t, raw)), b64Of(wrapped), 1)
			replaced = strings.Replace(replaced, mustBlockRecipient(t, raw), recipient, 1)
			if err := config.AtomicWrite(path, replaced); err != nil {
				t.Fatal(err)
			}
		}
		return "v2", nil
	}
	err := CredentialsSet(s, proj, "B", false, "")
	if !errors.Is(err, ErrCredentialFileChanged) {
		t.Fatalf("a write over a changed file must be refused: %v", err)
	}
	afterRaw, _ := os.ReadFile(path)
	if strings.Contains(string(afterRaw), "\nB = ") {
		t.Fatal("the refused write must leave nothing behind")
	}
	if bytes.Equal(beforeRaw, afterRaw) {
		t.Fatal("this test needs the second writer to have actually changed the file")
	}
}

func mustBlockIdentity(t *testing.T, raw []byte) []byte {
	t.Helper()
	b, ok, err := config.ParseCredentialsBlock(raw)
	if err != nil || !ok {
		t.Fatalf("block: %v", err)
	}
	return []byte(b64Of(b.Identity))
}

func mustBlockRecipient(t *testing.T, raw []byte) string {
	t.Helper()
	b, _, err := config.ParseCredentialsBlock(raw)
	if err != nil {
		t.Fatal(err)
	}
	return b.Recipient
}

// --layer writes to the named layer's file, and says what that means BEFORE
// the value is accepted: layer changes propagate live to every project
// extending it.
func TestCredentialsSetLayerDisclosesTheWriteTarget(t *testing.T) {
	_, proj := testPaths(t)
	home, err := project.Home()
	if err != nil {
		t.Fatal(err)
	}
	layerPath := config.LayerPath(home, "acme")
	if err := hostopen.PlainMkdirAll(filepath.Dir(layerPath), 0o755, hostopen.StoreOwned); err != nil {
		t.Fatal(err)
	}
	if err := config.AtomicWrite(layerPath, "[env]\nA = \"b\"\n"); err != nil {
		t.Fatal(err)
	}
	// One project extending it, so the count is a real one.
	if err := config.AtomicWrite(projectConfigPath(t, proj), "extends = \"acme\"\n"); err != nil {
		t.Fatal(err)
	}
	var errBuf bytes.Buffer
	passphraseSeam(t, "pw", "pw", "layer-value")
	if err := CredentialsSet(ttyStreams(&errBuf), proj, "SHARED", false, "acme"); err != nil {
		t.Fatalf("layer set: %v", err)
	}
	out := errBuf.String()
	if !strings.Contains(out, "writes to layer acme") || !strings.Contains(out, "used by 1 project") ||
		!strings.Contains(out, "every project extending it") {
		t.Fatalf("write-target disclosure: %s", out)
	}
	if v, _ := openCredRow(t, layerPath, "pw", "SHARED"); string(v) != "layer-value" {
		t.Fatalf("layer row: %q", v)
	}
	// The project's own config is untouched — a layer write goes to the layer.
	raw, _ := os.ReadFile(projectConfigPath(t, proj))
	if strings.Contains(string(raw), "SHARED") {
		t.Fatalf("the project config must be untouched:\n%s", raw)
	}
	if err := CredentialsSet(ttyStreams(&errBuf), proj, "X", false, "nosuch"); err == nil ||
		!strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("unknown layer: %v", err)
	}
}

// list reads the cascade and decrypts nothing: key, kind, source file, and
// whether the winning row carries a value.
func TestCredentialsList(t *testing.T) {
	_, proj := testPaths(t)
	var out, errBuf bytes.Buffer
	s := Streams{Out: &out, Err: &errBuf, In: strings.NewReader(""), TTY: true}
	if err := CredentialsList(s, proj); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no credentials in this project's cascade") {
		t.Fatalf("empty list: %s", out.String())
	}

	passphraseSeam(t, "pw", "pw", "v")
	if err := CredentialsSet(s, proj, "STRIPE_KEY", false, ""); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := CredentialsList(s, proj); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "STRIPE_KEY\tenv\tproject\tset") {
		t.Fatalf("list: %s", out.String())
	}
	if strings.Contains(out.String(), "\"v\"") {
		t.Fatal("list must decrypt nothing")
	}

	// A declared row the cascade does not deliver: the value IS set — the
	// ciphertext sits in that file — so the list says set, and says what is
	// beating it. "unset" would send the user to re-enter a value they have.
	raw, _ := os.ReadFile(projectConfigPath(t, proj))
	if err := config.AtomicWrite(projectConfigPath(t, proj), string(raw)+"\n[env]\nSTRIPE_KEY = \"literal\"\n"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := CredentialsList(s, proj); err != nil {
		t.Fatal(err)
	}
	line := out.String()
	if !strings.Contains(line, "STRIPE_KEY\tenv\tproject\tset — not delivered:") ||
		!strings.Contains(line, "[env] literal in project") {
		t.Fatalf("overridden row must read set, and name what takes it: %s", line)
	}
	if strings.Contains(line, "unset") {
		t.Fatalf("a row whose file carries the value is not unset: %s", line)
	}
}

// The other two ways a declared credential fails to arrive, each named.
func TestCredentialsListNamesWhatTakesTheRow(t *testing.T) {
	for _, tc := range []struct{ name, tail, want string }{
		{"disabled", "\n[env_from_host]\nSTRIPE_KEY = \"\"\n", `disabled by "" in project`},
		{"replaced", "\n[env_from_host]\nSTRIPE_KEY = \"env:STRIPE_KEY\"\n", "replaced by env:STRIPE_KEY in project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, proj := testPaths(t)
			var out, errBuf bytes.Buffer
			s := Streams{Out: &out, Err: &errBuf, In: strings.NewReader(""), TTY: true}
			passphraseSeam(t, "pw", "pw", "v")
			// The row is set in a LAYER, so a nearer file can beat it while
			// the ciphertext stays where it was written.
			layerPath := config.LayerPath(mustHome(t), "acme")
			if err := hostopen.PlainMkdirAll(filepath.Dir(layerPath), 0o755, hostopen.StoreOwned); err != nil {
				t.Fatal(err)
			}
			if err := config.AtomicWrite(layerPath, ""); err != nil {
				t.Fatal(err)
			}
			if err := CredentialsSet(s, proj, "STRIPE_KEY", false, "acme"); err != nil {
				t.Fatal(err)
			}
			if err := config.AtomicWrite(projectConfigPath(t, proj), "extends = \"acme\"\n"+tc.tail); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			if err := CredentialsList(s, proj); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "layer acme\tset — not delivered: "+tc.want) {
				t.Fatalf("want the losing row set in the layer and the reason %q: %s", tc.want, out.String())
			}
		})
	}
}

func mustHome(t *testing.T) string {
	t.Helper()
	home, err := project.Home()
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func b64Of(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// The one seam the CLI tests and the launch tests do not meet at: resolve()
// putting the cascade's credential rows on the view develop reads. Set a
// credential the ordinary way, then take the ordinary resolution and open the
// row with the passphrase — which is the whole loop, end to end.
func TestResolveCarriesTheCascadeCredentialRows(t *testing.T) {
	paths, proj := testPaths(t)
	var errBuf bytes.Buffer
	passphraseSeam(t, "pw", "pw", "sk-live")
	if err := CredentialsSet(ttyStreams(&errBuf), proj, "STRIPE_KEY", false, ""); err != nil {
		t.Fatal(err)
	}
	rv, err := resolve(paths, proj, io.Discard)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rv.credErr != nil {
		t.Fatalf("credErr: %v", rv.credErr)
	}
	if len(rv.credFiles) != 1 || len(rv.credFiles[0].Rows) != 1 || rv.credFiles[0].Label != "project" {
		t.Fatalf("credFiles = %+v", rv.credFiles)
	}
	unlocked, err := unlockCredentials(ttyStreamsWith(&errBuf, "pw"), CredentialStdin, rv.credFiles)
	if err != nil {
		t.Fatalf("unlock: %v", err)
	}
	p, err := decryptCredentialsLocked(rv.credFiles, unlocked, false)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(p.values["STRIPE_KEY"]) != "sk-live" || p.manifest != "STRIPE_KEY env\n" {
		t.Fatalf("payload = %q / %q", p.values["STRIPE_KEY"], p.manifest)
	}
}

// ttyStreamsWith is ttyStreams with a stdin the stdin-mode unlock can read.
// A layer file is reachable from every project extending it, so the lock a
// credential write takes belongs to the FILE, not to the caller: two projects
// writing one layer take the same lock, and neither takes its own.
func TestLayerCredentialWritesTakeTheLayerLock(t *testing.T) {
	pA, projA := testPaths(t)
	projB := t.TempDir()
	pB, err := project.Resolve(projB)
	if err != nil {
		t.Fatal(err)
	}
	if err := pB.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	layerPath := config.LayerPath(pA.Home, "acme")
	if err := hostopen.PlainMkdirAll(filepath.Dir(layerPath), 0o755, hostopen.StoreOwned); err != nil {
		t.Fatal(err)
	}
	if err := config.AtomicWrite(layerPath, "[env]\nA = \"b\"\n"); err != nil {
		t.Fatal(err)
	}

	tA, err := credentialTarget(projA, "acme")
	if err != nil {
		t.Fatal(err)
	}
	tB, err := credentialTarget(projB, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if tA.lockFile != tB.lockFile || tA.lockFile != config.LayerLockPath(pA.Home, "acme") {
		t.Fatalf("two projects writing one layer must share the layer's own lock: %q vs %q", tA.lockFile, tB.lockFile)
	}
	if tA.lockFile == pA.LockFile || tB.lockFile == pB.LockFile {
		t.Fatal("a project setup lock cannot serialize two projects writing one layer")
	}
	// A project target keeps the project lock — sibling worktree sessions
	// share one store, and that is the contender there.
	own, err := credentialTarget(projA, "")
	if err != nil {
		t.Fatal(err)
	}
	if own.lockFile != pA.LockFile {
		t.Fatalf("project target lock = %q, want the project setup lock", own.lockFile)
	}

	// And it is a real mutex, not a path: held as the other project would
	// hold it, this project's write waits instead of racing the compare.
	holder, err := lock.Acquire(tB.lockFile)
	if err != nil {
		t.Fatal(err)
	}
	passphraseSeam(t, "pw", "pw", "layer-value")
	var out safeBuffer
	var wg sync.WaitGroup
	var done atomic.Bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := CredentialsSet(Streams{Out: io.Discard, Err: &out, In: strings.NewReader(""), TTY: true}, projA, "SHARED", false, "acme"); err != nil {
			t.Errorf("contended layer set: %v", err)
		}
		done.Store(true)
	}()
	for i := 0; i < 200 && !strings.Contains(out.String(), "waiting"); i++ {
		sleepMs(10)
	}
	if done.Load() {
		t.Fatal("the write landed while another project held the layer lock")
	}
	if err := holder.Release(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if v, _ := openCredRow(t, layerPath, "pw", "SHARED"); string(v) != "layer-value" {
		t.Fatalf("value after the lock freed: %q", v)
	}
}

// `byre dockerrun` prints what develop would run — and for a project with
// declared credentials, what develop runs is a box ARMED to fail closed
// without them. A printed command that omitted the arming would hand the
// user a silent credential-less launch, so the gate rides the argv and the
// stderr note explains it, exactly as the firewall's does.
func TestDockerRunCarriesTheCredentialGate(t *testing.T) {
	_, proj := testPaths(t)
	var errBuf bytes.Buffer
	passphraseSeam(t, "pw", "pw", "sk-live-9")
	if err := CredentialsSet(ttyStreams(&errBuf), proj, "STRIPE_KEY", false, ""); err != nil {
		t.Fatal(err)
	}
	s, out, runErr := testStreams("", false)
	if err := DockerRun(s, proj); err != nil {
		t.Fatal(err)
	}
	cmd := out.String()
	for _, want := range []string{"-e BYRE_CRED_EXPECT=1", credTmpfsTarget} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("the printed command must carry %q so the box fails closed:\n%s", want, cmd)
		}
	}
	if strings.Contains(cmd, "credential gate") {
		t.Error("the note must not pollute stdout")
	}
	if !strings.Contains(runErr.String(), "credential gate") || !strings.Contains(runErr.String(), "byre develop") {
		t.Fatalf("dockerrun must say the box stops at its credential gate: %q", runErr.String())
	}
}

// No declared credentials, no gate and no note.
func TestDockerRunWithoutCredentialsStaysQuiet(t *testing.T) {
	_, proj := testPaths(t)
	s, out, errBuf := testStreams("", false)
	if err := DockerRun(s, proj); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "BYRE_CRED_EXPECT") || strings.Contains(errBuf.String(), "credential gate") {
		t.Fatalf("unarmed project got a credential gate:\n%s\n%s", out.String(), errBuf.String())
	}
}

func ttyStreamsWith(errBuf *bytes.Buffer, stdin string) Streams {
	return Streams{Out: io.Discard, Err: errBuf, In: strings.NewReader(stdin + "\n"), TTY: false}
}
