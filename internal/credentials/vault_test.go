package credentials

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestMain(m *testing.M) {
	// Production's pinned work factor is a deliberate unlock cost; paying it
	// on every test unlock makes the suite ~30s for no coverage. The unwrap
	// path is identical at any logN (SetMaxWorkFactor still bounds above).
	scryptWorkFactor = 10
	os.Exit(m.Run())
}

// testVault creates a store dir and a vault handle for it.
func testVault(t *testing.T) (*Vault, string) {
	t.Helper()
	store := t.TempDir()
	return Open(store, "proj-1234"), store
}

// created returns a vault with a fresh identity under passphrase "pw".
func created(t *testing.T) (*Vault, string) {
	t.Helper()
	v, store := testVault(t)
	if err := v.Create("pw"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return v, store
}

func TestCreateUnlockRoundtrip(t *testing.T) {
	v, _ := created(t)
	if !v.Exists() {
		t.Fatal("vault should exist after Create")
	}
	u, err := v.Unlock("pw")
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	idx, err := v.ReadIndex()
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	if idx.Recipient != u.Recipient() {
		t.Fatalf("index recipient %q != identity recipient %q", idx.Recipient, u.Recipient())
	}
	if idx.ProjectID != "proj-1234" {
		t.Fatalf("index project id = %q", idx.ProjectID)
	}
}

func TestUnlockWrongPassphrase(t *testing.T) {
	v, _ := created(t)
	if _, err := v.Unlock("nope"); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("wrong passphrase: got %v, want ErrBadPassphrase", err)
	}
}

func TestUnlockNoVault(t *testing.T) {
	v, _ := testVault(t)
	if _, err := v.Unlock("pw"); !errors.Is(err, ErrNoVault) {
		t.Fatalf("no vault: got %v, want ErrNoVault", err)
	}
}

func TestUnlockCorruptIdentity(t *testing.T) {
	v, store := created(t)
	path := filepath.Join(store, DirName, "identity.age")
	if err := os.WriteFile(path, []byte("not an age file"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := v.Unlock("pw")
	if err == nil || errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("corrupt identity: got %v, want a non-passphrase failure", err)
	}
}

func TestUnlockOversizeIdentityBounded(t *testing.T) {
	v, store := created(t)
	path := filepath.Join(store, DirName, "identity.age")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), identityReadCap+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := v.Unlock("pw")
	if err == nil || errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("oversize identity: got %v, want a bounded-read failure", err)
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversize identity error should name the bound: %v", err)
	}
}

func TestCreateRefusesExisting(t *testing.T) {
	v, _ := created(t)
	if err := v.Create("other"); !errors.Is(err, ErrVaultExists) {
		t.Fatalf("second Create: got %v, want ErrVaultExists", err)
	}
}

func TestCreateIsOneStep(t *testing.T) {
	// An interrupted creation must leave no half-vault: simulate the debris
	// of a crash (a staging dir) and confirm a fresh Create still works and
	// sweeps it.
	v, store := testVault(t)
	debris := filepath.Join(store, stagingPrefix+"deadbeef")
	if err := os.MkdirAll(debris, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := v.Create("pw"); err != nil {
		t.Fatalf("Create with staging debris present: %v", err)
	}
	if _, err := os.Lstat(debris); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging debris should be swept on Create")
	}
	// The published vault has both halves: identity AND index.
	for _, f := range []string{"identity.age", "index.toml"} {
		if _, err := os.Lstat(filepath.Join(store, DirName, f)); err != nil {
			t.Fatalf("published vault missing %s: %v", f, err)
		}
	}
}

func TestReplaceDiscards(t *testing.T) {
	v, _ := created(t)
	if err := v.Set("stripe", []byte("sk-old"), "env"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := v.Replace("new-pw"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, err := v.Unlock("pw"); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("old passphrase after Replace: got %v, want ErrBadPassphrase", err)
	}
	u, err := v.Unlock("new-pw")
	if err != nil {
		t.Fatalf("Unlock after Replace: %v", err)
	}
	if _, oc, _ := u.Decrypt("stripe"); oc != OutcomeMissingValue {
		t.Fatalf("entry should be gone after Replace; outcome = %s", oc)
	}
}

func TestSetDecryptRoundtrip(t *testing.T) {
	v, _ := created(t)
	value := []byte("sk-live-123\x01binary ok for file kind")
	if err := v.Set("stripe", value, "file"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	u, err := v.Unlock("pw")
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	got, oc, derr := u.Decrypt("stripe")
	if derr != nil || oc != "" {
		t.Fatalf("Decrypt: outcome=%s err=%v", oc, derr)
	}
	if !bytes.Equal(got, value) {
		t.Fatalf("value roundtrip: got %q want %q", got, value)
	}
}

func TestDecryptMissingValue(t *testing.T) {
	v, _ := created(t)
	u, _ := v.Unlock("pw")
	_, oc, err := u.Decrypt("absent")
	if oc != OutcomeMissingValue || err == nil {
		t.Fatalf("missing entry: outcome=%s err=%v", oc, err)
	}
}

func TestDecryptCorruptEntry(t *testing.T) {
	v, store := created(t)
	if err := os.MkdirAll(filepath.Join(store, DirName, "entries"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, DirName, "entries", "bad.age"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	u, _ := v.Unlock("pw")
	_, oc, err := u.Decrypt("bad")
	if oc != OutcomeEntryUndecryptable || err == nil {
		t.Fatalf("corrupt entry: outcome=%s err=%v", oc, err)
	}
}

func TestDecryptOversizeEntryBounded(t *testing.T) {
	v, store := created(t)
	if err := os.MkdirAll(filepath.Join(store, DirName, "entries"), 0o700); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("x"), entryReadCap+1)
	if err := os.WriteFile(filepath.Join(store, DirName, "entries", "big.age"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	u, _ := v.Unlock("pw")
	_, oc, err := u.Decrypt("big")
	if oc != OutcomeEntryUndecryptable || err == nil {
		t.Fatalf("oversize entry: outcome=%s err=%v", oc, err)
	}
}

func TestDecryptForeignRecipient(t *testing.T) {
	// An entry encrypted to a DIFFERENT vault's recipient is plain
	// undecryptable — no quarantine class, per the outcome vocabulary.
	v, store := created(t)
	other := Open(t.TempDir(), "proj-1234")
	if err := other.Create("pw2"); err != nil {
		t.Fatal(err)
	}
	if err := other.Set("stripe", []byte("theirs"), "env"); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(other.storeDir, DirName, "entries", "stripe.age")
	dst := filepath.Join(store, DirName, "entries", "stripe.age")
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		t.Fatal(err)
	}
	u, _ := v.Unlock("pw")
	_, oc, derr := u.Decrypt("stripe")
	if oc != OutcomeEntryUndecryptable || derr == nil {
		t.Fatalf("foreign-recipient entry: outcome=%s err=%v", oc, derr)
	}
}

func TestDecryptCrossProjectMismatch(t *testing.T) {
	// Same recipient (identity copied), different project stamp: the
	// accident guard fires — entry-mismatch, value not delivered.
	store := t.TempDir()
	vA := Open(store, "proj-A")
	if err := vA.Create("pw"); err != nil {
		t.Fatal(err)
	}
	if err := vA.Set("stripe", []byte("a-value"), "env"); err != nil {
		t.Fatal(err)
	}
	// Open the SAME store under a different project id, as a wrong-project
	// restore would.
	vB := Open(store, "proj-B")
	u, err := vB.Unlock("pw")
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	_, oc, derr := u.Decrypt("stripe")
	if oc != OutcomeEntryMismatch || derr == nil {
		t.Fatalf("cross-project entry: outcome=%s err=%v", oc, derr)
	}
}

func TestDecryptRenamedEntryMismatch(t *testing.T) {
	v, store := created(t)
	if err := v.Set("stripe", []byte("sk"), "env"); err != nil {
		t.Fatal(err)
	}
	// Re-label the file: payload stamp still says "stripe".
	dir := filepath.Join(store, DirName, "entries")
	if err := os.Rename(filepath.Join(dir, "stripe.age"), filepath.Join(dir, "github.age")); err != nil {
		t.Fatal(err)
	}
	u, _ := v.Unlock("pw")
	_, oc, err := u.Decrypt("github")
	if oc != OutcomeEntryMismatch || err == nil {
		t.Fatalf("re-labelled entry: outcome=%s err=%v", oc, err)
	}
}

func TestDecryptUnsupportedFormat(t *testing.T) {
	// A payload with a future version line: unsupported-format, stated as
	// such (the rule is the version check, not a parse accident).
	v, _ := created(t)
	u, _ := v.Unlock("pw")
	// Forge an entry encrypted to OUR recipient but with an unknown header.
	idx, _ := v.ReadIndex()
	forged := forgeEntry(t, idx.Recipient, []byte("byre-credential 999\nproj-1234\nstripe\nvalue"))
	dir := filepath.Join(v.storeDir, DirName, "entries")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stripe.age"), forged, 0o600); err != nil {
		t.Fatal(err)
	}
	_, oc, err := u.Decrypt("stripe")
	if oc != OutcomeUnsupportedFormat || err == nil {
		t.Fatalf("future-format entry: outcome=%s err=%v", oc, err)
	}
	if !strings.Contains(err.Error(), "format") {
		t.Fatalf("unsupported-format error should name the format rule: %v", err)
	}
}

func TestRekeyRotatesPassphraseNotIdentity(t *testing.T) {
	v, _ := created(t)
	if err := v.Set("stripe", []byte("sk"), "env"); err != nil {
		t.Fatal(err)
	}
	u, _ := v.Unlock("pw")
	if err := u.Rekey("new-pw"); err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	if _, err := v.Unlock("pw"); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("old passphrase after Rekey: got %v, want ErrBadPassphrase", err)
	}
	u2, err := v.Unlock("new-pw")
	if err != nil {
		t.Fatalf("Unlock after Rekey: %v", err)
	}
	// The identity did NOT rotate: existing entries still decrypt.
	got, oc, derr := u2.Decrypt("stripe")
	if oc != "" || derr != nil || string(got) != "sk" {
		t.Fatalf("entry after Rekey: outcome=%s err=%v value=%q", oc, derr, got)
	}
}

func TestRekeyRefusesReplacedVault(t *testing.T) {
	// The rekey race: unlock runs pre-lock; a concurrent init --replace
	// lands a NEW vault before the rekey's under-lock write. Publishing the
	// old identity would silently corrupt the new vault, so Rekey compares
	// the on-disk identity first and refuses.
	v, _ := created(t)
	u, err := v.Unlock("pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Replace("new-vault-pw"); err != nil {
		t.Fatal(err)
	}
	if err := v.Set("stripe", []byte("new-vault-value"), "env"); err != nil {
		t.Fatal(err)
	}
	if err := u.Rekey("rotated"); !errors.Is(err, ErrVaultChanged) {
		t.Fatalf("rekey over a replaced vault: got %v, want ErrVaultChanged", err)
	}
	// The victim: the replacement vault is untouched and fully working.
	u2, err := v.Unlock("new-vault-pw")
	if err != nil {
		t.Fatalf("replacement vault after refused rekey: %v", err)
	}
	if val, oc, _ := u2.Decrypt("stripe"); oc != "" || string(val) != "new-vault-value" {
		t.Fatalf("replacement vault entries: %s %q", oc, val)
	}
}

func TestUnsetAndEntryNames(t *testing.T) {
	v, _ := created(t)
	for _, n := range []string{"stripe", "github"} {
		if err := v.Set(n, []byte("v"), "env"); err != nil {
			t.Fatal(err)
		}
	}
	if got := v.EntryNames(); !equalStrings(got, []string{"github", "stripe"}) {
		t.Fatalf("EntryNames = %v", got)
	}
	if err := v.Unset("stripe"); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	if got := v.EntryNames(); !equalStrings(got, []string{"github"}) {
		t.Fatalf("EntryNames after Unset = %v", got)
	}
	// Unsetting an absent name is a no-op, not an error.
	if err := v.Unset("absent"); err != nil {
		t.Fatalf("Unset absent: %v", err)
	}
}

func TestSetValidatesEnvValues(t *testing.T) {
	v, _ := created(t)
	if err := v.Set("a", []byte("has\x00nul"), "env"); err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("NUL env value: %v", err)
	}
	if err := v.Set("a", bytes.Repeat([]byte("x"), MaxEnvValue+1), "env"); err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("oversize env value: %v", err)
	}
	// The same bytes are fine as file kind (binary allowed, bigger cap).
	if err := v.Set("a", []byte("has\x00nul"), "file"); err != nil {
		t.Fatalf("file kind with NUL: %v", err)
	}
	if err := v.Set("b", bytes.Repeat([]byte("x"), MaxFileValue+1), "file"); err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("oversize file value: %v", err)
	}
}

func TestSetRejectsBadNames(t *testing.T) {
	v, _ := created(t)
	for _, bad := range []string{"", "9lead", "UPPER", "has_underscore", "has/slash", "..", strings.Repeat("a", 64)} {
		if err := v.Set(bad, []byte("v"), "env"); err == nil || !strings.Contains(err.Error(), "name") {
			t.Fatalf("Set(%q): %v — want the name rule", bad, err)
		}
	}
}

func TestSetWithoutVault(t *testing.T) {
	v, _ := testVault(t)
	if err := v.Set("stripe", []byte("v"), "env"); !errors.Is(err, ErrNoVault) {
		t.Fatalf("Set without vault: got %v, want ErrNoVault", err)
	}
}

func TestStripTrailingNewline(t *testing.T) {
	for in, want := range map[string]string{
		"v\n":      "v",
		"v\r\n":    "v",
		"v":        "v",
		"v\n\n":    "v\n", // ONE newline stripped
		"v\ninner": "v\ninner",
	} {
		if got := string(StripTrailingNewline([]byte(in))); got != want {
			t.Fatalf("StripTrailingNewline(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRepairIndex(t *testing.T) {
	v, store := created(t)
	// Damage the index: wrong recipient, missing kinds.
	if err := os.WriteFile(filepath.Join(store, DirName, "index.toml"), []byte("recipient = \"garbage\"\nproject_id = \"other\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u, _ := v.Unlock("pw")
	u.RepairIndex(map[string]string{"stripe": "env"})
	idx, err := v.ReadIndex()
	if err != nil {
		t.Fatalf("ReadIndex after repair: %v", err)
	}
	if idx.Recipient != u.Recipient() || idx.ProjectID != "proj-1234" || idx.Kinds["stripe"] != "env" {
		t.Fatalf("repaired index = %+v", idx)
	}
	// The repaired recipient makes cold writes work again.
	if err := v.Set("stripe", []byte("sk"), "env"); err != nil {
		t.Fatalf("Set after repair: %v", err)
	}
}

// forgeEntry encrypts a raw payload to a recipient (test helper for format
// cases the real writer refuses to produce).
func forgeEntry(t *testing.T, recipient string, payload []byte) []byte {
	t.Helper()
	rcp, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, rcp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
