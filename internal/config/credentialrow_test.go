package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/credentials"
)

// mintIdentity makes a credentials identity for a test file: the wrapped
// blob its block would carry, and the recipient its rows encrypt to.
func mintIdentity(t *testing.T, passphrase string) (wrapped []byte, recipient string) {
	t.Helper()
	// Production's work factor is a deliberate unlock cost; a suite that mints
	// several identities would pay seconds for no coverage.
	credentials.SetWorkFactorForTesting(10)
	wrapped, recipient, err := credentials.NewIdentity(passphrase)
	if err != nil {
		t.Fatal(err)
	}
	return wrapped, recipient
}

// blockTOML renders the [credentials] block a file carrying this identity
// would hold.
func blockTOML(wrapped []byte, recipient string) string {
	return fmt.Sprintf("[credentials]\nidentity = %q\nrecipient = %q\n",
		base64.StdEncoding.EncodeToString(wrapped), recipient)
}

// encRow renders one encrypted env row value for a key.
func encRow(t *testing.T, recipient, key string, kind credentials.Kind, value string) string {
	t.Helper()
	blob, err := credentials.EncryptValue(recipient, key, kind, []byte(value))
	if err != nil {
		t.Fatal(err)
	}
	v, err := FormatEncryptedRow(kind, blob)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParseEncryptedRowSchemes(t *testing.T) {
	_, recipient := mintIdentity(t, "pw")
	env := encRow(t, recipient, "STRIPE_KEY", credentials.KindEnv, "sk-live")
	file := encRow(t, recipient, "TLS_CERT", credentials.KindFile, "cert")

	row, ok, err := ParseEncryptedRow("STRIPE_KEY", env)
	if err != nil || !ok || row.Kind != credentials.KindEnv || row.Key != "STRIPE_KEY" {
		t.Fatalf("env scheme: row=%+v ok=%v err=%v", row, ok, err)
	}
	if row, ok, err := ParseEncryptedRow("TLS_CERT", file); err != nil || !ok || row.Kind != credentials.KindFile {
		t.Fatalf("file scheme: row=%+v ok=%v err=%v", row, ok, err)
	}
	// An ordinary literal is still a literal — including "", the disable.
	for _, plain := range []string{"", "sk-live-plaintext", "encrypted", "not-encrypted:x"} {
		if _, ok, err := ParseEncryptedRow("K", plain); ok || err != nil {
			t.Fatalf("literal %q: ok=%v err=%v", plain, ok, err)
		}
	}
}

func TestParseEncryptedRowRejections(t *testing.T) {
	// A row that NAMES the scheme and carries an unusable payload is a stop,
	// and the refusal names the rule that fired plus the offending value.
	_, _, err := ParseEncryptedRow("STRIPE_KEY", "encrypted:not base64!!")
	if err == nil || !strings.Contains(err.Error(), "not valid base64") ||
		!strings.Contains(err.Error(), "STRIPE_KEY") || !strings.Contains(err.Error(), "not base64") {
		t.Fatalf("bad base64: %v", err)
	}
	if _, _, err := ParseEncryptedRow("STRIPE_KEY", "encrypted:"); err == nil ||
		!strings.Contains(err.Error(), "no ciphertext") || !strings.Contains(err.Error(), EncryptedScheme) {
		t.Fatalf("empty payload: %v", err)
	}
	if _, _, err := ParseEncryptedRow("TLS_CERT", "encrypted-file:"); err == nil ||
		!strings.Contains(err.Error(), "no ciphertext") || !strings.Contains(err.Error(), EncryptedFileScheme) {
		t.Fatalf("empty file payload: %v", err)
	}
	// The echo is bounded: a pasted wall of ciphertext cannot become the
	// message.
	long := strings.Repeat("!", 500)
	err = mustRowErr(t, "K", "encrypted:"+long)
	if len(err.Error()) > 200 {
		t.Fatalf("refusal echoes the whole value: %d chars", len(err.Error()))
	}
}

func mustRowErr(t *testing.T, key, value string) error {
	t.Helper()
	_, _, err := ParseEncryptedRow(key, value)
	if err == nil {
		t.Fatalf("ParseEncryptedRow(%q) should have failed", value)
	}
	return err
}

func TestFormatEncryptedRowRoundTrip(t *testing.T) {
	_, recipient := mintIdentity(t, "pw")
	blob, err := credentials.EncryptValue(recipient, "K", credentials.KindFile, []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := FormatEncryptedRow(credentials.KindFile, blob)
	if err != nil {
		t.Fatal(err)
	}
	row, ok, err := ParseEncryptedRow("K", value)
	if err != nil || !ok || row.Kind != credentials.KindFile || string(row.Blob) != string(blob) {
		t.Fatalf("round trip: ok=%v err=%v kind=%s", ok, err, row.Kind)
	}
	if _, err := FormatEncryptedRow(credentials.Kind("secret"), blob); err == nil ||
		!strings.Contains(err.Error(), `kind "secret" invalid`) {
		t.Fatalf("unknown kind must be refused naming the rule and the value: %v", err)
	}
}

func TestParseCredentialsBlock(t *testing.T) {
	wrapped, recipient := mintIdentity(t, "pw")
	raw := "[env]\nA = \"b\"\n\n" + blockTOML(wrapped, recipient)
	b, ok, err := ParseCredentialsBlock([]byte(raw))
	if err != nil || !ok {
		t.Fatalf("block: ok=%v err=%v", ok, err)
	}
	if b.Recipient != recipient || string(b.Identity) != string(wrapped) {
		t.Fatalf("block round trip: %+v", b)
	}
	// A file with no block says so, without an error.
	if _, ok, err := ParseCredentialsBlock([]byte("[env]\nA = \"b\"\n")); ok || err != nil {
		t.Fatalf("no block: ok=%v err=%v", ok, err)
	}
}

func TestParseCredentialsBlockRejections(t *testing.T) {
	wrapped, recipient := mintIdentity(t, "pw")
	id64 := base64.StdEncoding.EncodeToString(wrapped)
	cases := map[string]struct{ raw, want string }{
		"unknown key": {
			fmt.Sprintf("[credentials]\nidentity = %q\nrecipient = %q\npassphrase = \"hunter2\"\n", id64, recipient),
			`unknown key "passphrase"`,
		},
		"no recipient": {
			fmt.Sprintf("[credentials]\nidentity = %q\n", id64),
			"needs both identity",
		},
		"no identity": {
			fmt.Sprintf("[credentials]\nrecipient = %q\n", recipient),
			"needs both identity",
		},
		"identity not base64": {
			fmt.Sprintf("[credentials]\nidentity = \"not base64!!\"\nrecipient = %q\n", recipient),
			"identity is not valid base64",
		},
		"recipient not a key": {
			fmt.Sprintf("[credentials]\nidentity = %q\nrecipient = \"age1nope\"\n", id64),
			"age public key",
		},
		"not a string": {
			fmt.Sprintf("[credentials]\nidentity = %q\nrecipient = 3\n", id64),
			"recipient must be a string",
		},
		"array of tables": {
			"[[credentials]]\nname = \"stripe\"\n",
			"must be a [credentials] table",
		},
	}
	for name, c := range cases {
		_, ok, err := ParseCredentialsBlock([]byte(c.raw))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want a refusal naming %q, got ok=%v err=%v", name, c.want, ok, err)
		}
	}
}

// credentialCascade builds a two-file cascade in memory: a layer and the
// project, each with its OWN identity and one encrypted row.
type credentialCascade struct {
	files []CascadeFile
	// pass is each file's own identity passphrase, by label.
	pass map[string]string
}

func buildCredentialCascade(t *testing.T) credentialCascade {
	t.Helper()
	layerWrapped, layerRcp := mintIdentity(t, "layer-pw")
	projWrapped, projRcp := mintIdentity(t, "proj-pw")
	layer := CascadeFile{
		Label: "layer:acme",
		Path:  "/home/u/.byre/layers/acme/layer.config",
		Raw:   []byte(blockTOML(layerWrapped, layerRcp)),
		Cfg: Config{EnvFromHost: map[string]string{
			"ACME_TOKEN": encRow(t, layerRcp, "ACME_TOKEN", credentials.KindEnv, "acme-secret"),
			"PLAIN":      "env:PLAIN",
		}},
	}
	proj := CascadeFile{
		Label: "project",
		Path:  "/home/u/.byre/projects/p/byre.config",
		Raw:   []byte(blockTOML(projWrapped, projRcp)),
		Cfg: Config{EnvFromHost: map[string]string{
			"STRIPE_KEY": encRow(t, projRcp, "STRIPE_KEY", credentials.KindEnv, "sk-live"),
		}},
	}
	return credentialCascade{
		files: []CascadeFile{layer, proj},
		pass:  map[string]string{"layer:acme": "layer-pw", "project": "proj-pw"},
	}
}

// The load-bearing semantic: a [credentials] block belongs to the file that
// carries it. Two files, two identities, and each winning row resolves
// against its OWN file's block — the project's identity is never reached for
// to open the layer's row.
func TestEncryptedRowsAreFileLocal(t *testing.T) {
	c := buildCredentialCascade(t)
	groups, err := EncryptedRows(c.files)
	if err != nil {
		t.Fatalf("EncryptedRows: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("want one group per contributing file, got %d: %+v", len(groups), groups)
	}
	if groups[0].Label != "layer:acme" || groups[1].Label != "project" {
		t.Fatalf("groups must come root-most first: %s, %s", groups[0].Label, groups[1].Label)
	}
	want := map[string]string{"layer:acme": "acme-secret", "project": "sk-live"}
	for _, g := range groups {
		if !g.HasBlock {
			t.Fatalf("%s: block missing", g.Label)
		}
		if len(g.Rows) != 1 {
			t.Fatalf("%s: rows = %+v", g.Label, g.Rows)
		}
		id, err := credentials.UnwrapIdentity(g.Block.Identity, c.pass[g.Label])
		if err != nil {
			t.Fatalf("%s: unwrap under its own passphrase: %v", g.Label, err)
		}
		got, oc, derr := id.DecryptValue(g.Rows[0].Key, g.Rows[0].Kind, g.Rows[0].Blob)
		if oc != "" || derr != nil || string(got) != want[g.Label] {
			t.Fatalf("%s: decrypt under its own identity: outcome=%s err=%v value=%q", g.Label, oc, derr, got)
		}
	}

	// The other direction, stated as the refusal it is: the project's
	// identity cannot open the layer's row.
	projID, err := credentials.UnwrapIdentity(groups[1].Block.Identity, c.pass["project"])
	if err != nil {
		t.Fatal(err)
	}
	layerRow := groups[0].Rows[0]
	if _, oc, derr := projID.DecryptValue(layerRow.Key, layerRow.Kind, layerRow.Blob); derr == nil {
		t.Fatalf("a project identity must not open a layer's row: outcome=%s", oc)
	}
}

func TestEncryptedRowsFollowTheOrdinaryMerge(t *testing.T) {
	c := buildCredentialCascade(t)
	_, projRcp := mintIdentity(t, "proj-pw")
	// The project overrides the layer's credential: with another source (the
	// row stops being a credential at all), and adds its own second credential
	// over a key the layer set encrypted.
	c.files[1].Cfg.EnvFromHost["ACME_TOKEN"] = "env:ACME_TOKEN"
	c.files[0].Cfg.EnvFromHost["SHARED"] = encRow(t, projRcp, "SHARED", credentials.KindEnv, "layer-value")
	c.files[1].Cfg.EnvFromHost["SHARED"] = encRow(t, projRcp, "SHARED", credentials.KindEnv, "project-value")

	groups, err := EncryptedRows(c.files)
	if err != nil {
		t.Fatalf("EncryptedRows: %v", err)
	}
	// The layer contributes nothing now: one row was overridden by a literal,
	// the other by a nearer encrypted row.
	if len(groups) != 1 || groups[0].Label != "project" {
		t.Fatalf("overridden rows must not survive: %+v", groups)
	}
	var keys []string
	for _, r := range groups[0].Rows {
		keys = append(keys, r.Key)
	}
	if strings.Join(keys, ",") != "SHARED,STRIPE_KEY" {
		t.Fatalf("project rows = %v", keys)
	}
	// Emptying the row is the idiomatic disable, and leaves no credential.
	c.files[1].Cfg.EnvFromHost["SHARED"] = ""
	c.files[1].Cfg.EnvFromHost["STRIPE_KEY"] = ""
	groups, err = EncryptedRows(c.files)
	if err != nil {
		t.Fatalf("EncryptedRows: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("emptied rows must leave nothing to unlock: %+v", groups)
	}
}

// env_from_host's standing precedence reaches credential rows too: an
// explicit [env] key wins, so the row is neither delivered nor prompted for.
func TestEncryptedRowsLoseToAnExplicitEnvLiteral(t *testing.T) {
	c := buildCredentialCascade(t)
	c.files[1].Cfg.Env = map[string]string{"ACME_TOKEN": "literal-wins"}
	groups, err := EncryptedRows(c.files)
	if err != nil {
		t.Fatalf("EncryptedRows: %v", err)
	}
	if len(groups) != 1 || groups[0].Label != "project" {
		t.Fatalf("an [env] literal must take the layer's row out: %+v", groups)
	}
}

// [env] is NOT a credential table: a literal there beginning "encrypted:" is
// an ordinary value, unrestricted as it has always been.
func TestEnvLiteralsAreNeverCredentialRows(t *testing.T) {
	c := buildCredentialCascade(t)
	c.files[1].Cfg.Env = map[string]string{"LOOKALIKE": "encrypted:not base64!!"}
	if _, err := EncryptedRows(c.files); err != nil {
		t.Fatalf("an [env] literal must not be parsed as a credential row: %v", err)
	}
	if err := (Config{Env: map[string]string{"LOOKALIKE": "encrypted:not base64!!"}}).ValidateLayer(); err != nil {
		t.Fatalf("an [env] literal must not be held to the scheme rules: %v", err)
	}
}

func TestEncryptedRowsWithoutABlock(t *testing.T) {
	// A row copied into a file whose block did not come with it. It is
	// reported as blockless — never opened with a neighbour's identity.
	c := buildCredentialCascade(t)
	c.files[1].Raw = []byte("[env]\nA = \"b\"\n")
	groups, err := EncryptedRows(c.files)
	if err != nil {
		t.Fatalf("EncryptedRows: %v", err)
	}
	if len(groups) != 2 || groups[1].HasBlock || groups[1].Block.Recipient != "" {
		t.Fatalf("a blockless file must report as such: %+v", groups[1])
	}
}

func TestEncryptedRowsRefusalsNameTheFile(t *testing.T) {
	c := buildCredentialCascade(t)
	c.files[0].Cfg.EnvFromHost["ACME_TOKEN"] = "encrypted:not base64!!"
	_, err := EncryptedRows(c.files)
	if err == nil || !strings.Contains(err.Error(), "not valid base64") ||
		!strings.Contains(err.Error(), "layer:acme") || !strings.Contains(err.Error(), "ACME_TOKEN") {
		t.Fatalf("a broken row must name the file and the key: %v", err)
	}

	c = buildCredentialCascade(t)
	c.files[0].Raw = []byte("[credentials]\nidentity = \"aGk=\"\n")
	_, err = EncryptedRows(c.files)
	if err == nil || !strings.Contains(err.Error(), "needs both identity") ||
		!strings.Contains(err.Error(), "layer:acme") {
		t.Fatalf("a broken block must name the file: %v", err)
	}
}

// The cascade walk itself: labels, merge order, and per-file grouping against
// files on disk. The [credentials] block cannot ride a real config file until
// the [[credentials]] declaration genus is gone (it owns the same TOML key),
// so this pins the half that can: the rows, their provenance, and the honest
// report that no file here carries an identity.
func TestEncryptedRowsOverTheRealCascade(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()
	_, rcp := mintIdentity(t, "pw")

	writeFile(t, filepath.Join(home, "default.config"),
		"[env_from_host]\nDEFAULT_TOKEN = \""+encRow(t, rcp, "DEFAULT_TOKEN", credentials.KindEnv, "d")+"\"\n")
	writeLayer(t, home, "acme",
		"[env_from_host]\nACME_TOKEN = \""+encRow(t, rcp, "ACME_TOKEN", credentials.KindFile, "a")+"\"\nPLAIN = \"env:PLAIN\"\n")
	writeProjectCfg(t, proj, "extends = \"acme\"\n[env_from_host]\nDEFAULT_TOKEN = \"\"\n")

	files, err := CascadeFiles(proj)
	if err != nil {
		t.Fatalf("CascadeFiles: %v", err)
	}
	var labels []string
	for _, f := range files {
		labels = append(labels, f.Label)
	}
	if strings.Join(labels, ",") != "default,layer:acme,project" {
		t.Fatalf("cascade files = %v", labels)
	}
	groups, err := EncryptedRows(files)
	if err != nil {
		t.Fatalf("EncryptedRows: %v", err)
	}
	// default's row was disabled by the project; only the layer contributes.
	if len(groups) != 1 || groups[0].Label != "layer:acme" || len(groups[0].Rows) != 1 {
		t.Fatalf("groups = %+v", groups)
	}
	if groups[0].Rows[0].Key != "ACME_TOKEN" || groups[0].Rows[0].Kind != credentials.KindFile {
		t.Fatalf("row = %+v", groups[0].Rows[0])
	}
	if groups[0].Path != LayerPath(home, "acme") {
		t.Fatalf("row provenance path = %q", groups[0].Path)
	}
	if groups[0].HasBlock {
		t.Fatal("no file in this cascade carries an identity")
	}
}

// The cascade view every credential surface reads from is the same walk the
// [[context]] attribution view uses, so a layer that fails to load degrades
// (fewer labels) while a broken PROJECT layer stops the caller.
func TestCascadeFilesDegradation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()
	writeFile(t, filepath.Join(home, "default.config"), "base = \"debian\"\n")
	writeProjectCfg(t, proj, "[env]\nA = \"b\"\n")

	files, err := CascadeFiles(proj)
	if err != nil {
		t.Fatalf("CascadeFiles: %v", err)
	}
	if len(files) != 2 || files[0].Label != "default" || files[1].Label != "project" {
		t.Fatalf("files = %+v", files)
	}
	if len(files[1].Raw) == 0 || files[1].Cfg.Env["A"] != "b" {
		t.Fatalf("project file carries both its bytes and its parsed layer: %+v", files[1])
	}
	// A broken project layer is the caller's problem, loudly.
	writeProjectCfg(t, proj, "nonsense = true\n")
	if _, err := CascadeFiles(proj); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("broken project layer: %v", err)
	}
	// A broken DEFAULT layer just drops out.
	writeProjectCfg(t, proj, "[env]\nA = \"b\"\n")
	if err := os.WriteFile(filepath.Join(home, "default.config"), []byte("nonsense = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err = CascadeFiles(proj)
	if err != nil {
		t.Fatalf("CascadeFiles with a broken default: %v", err)
	}
	if len(files) != 1 || files[0].Label != "project" {
		t.Fatalf("files = %+v", files)
	}
}
