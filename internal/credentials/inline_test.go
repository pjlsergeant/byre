package credentials

import (
	"filippo.io/age"

	"bytes"
	"errors"
	"strings"
	"testing"
)

func init() {
	// Production's pinned scrypt cost is a deliberate unlock cost at the
	// prompt and pure drag in a suite that mints an identity per test; the
	// wrap/unwrap path is identical at any work factor.
	SetWorkFactorForTesting(10)
}

// newInline mints an identity and returns it unwrapped plus its recipient.
func newInline(t *testing.T, passphrase string) (*Identity, string, []byte) {
	t.Helper()
	wrapped, recipient, err := NewIdentity(passphrase)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	id, err := UnwrapIdentity(wrapped, passphrase)
	if err != nil {
		t.Fatalf("UnwrapIdentity: %v", err)
	}
	if id.Recipient() != recipient {
		t.Fatalf("recipient %q from NewIdentity != %q from the unwrapped identity", recipient, id.Recipient())
	}
	return id, recipient, wrapped
}

func TestInlineRoundtrip(t *testing.T) {
	id, recipient, _ := newInline(t, "pw")
	for _, tc := range []struct {
		kind  Kind
		value []byte
	}{
		{KindEnv, []byte("sk-live-123")},
		{KindFile, []byte("-----BEGIN CERT-----\x00binary is fine for a file\n")},
	} {
		blob, err := EncryptValue(recipient, "STRIPE_KEY", tc.kind, tc.value)
		if err != nil {
			t.Fatalf("EncryptValue(%s): %v", tc.kind, err)
		}
		got, oc, derr := id.DecryptValue("STRIPE_KEY", tc.kind, blob)
		if oc != "" || derr != nil {
			t.Fatalf("DecryptValue(%s): outcome=%s err=%v", tc.kind, oc, derr)
		}
		if !bytes.Equal(got, tc.value) {
			t.Fatalf("value roundtrip (%s): got %q want %q", tc.kind, got, tc.value)
		}
	}
}

func TestInlineEncryptNeedsNoPassphrase(t *testing.T) {
	// The recipient is the cleartext half: a caller holding only it can write
	// a value, and the identity that never left its wrapper reads it back.
	wrapped, recipient, err := NewIdentity("pw")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := EncryptValue(recipient, "TOKEN", KindEnv, []byte("v"))
	if err != nil {
		t.Fatalf("EncryptValue: %v", err)
	}
	id, err := UnwrapIdentity(wrapped, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if got, oc, derr := id.DecryptValue("TOKEN", KindEnv, blob); oc != "" || derr != nil || string(got) != "v" {
		t.Fatalf("set without passphrase, then unlock: outcome=%s err=%v value=%q", oc, derr, got)
	}
}

func TestInlineWrongPassphrase(t *testing.T) {
	_, _, wrapped := newInline(t, "pw")
	if _, err := UnwrapIdentity(wrapped, "nope"); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("wrong passphrase: got %v, want ErrBadPassphrase", err)
	}
	// A damaged blob is NOT a passphrase problem — re-asking would not help.
	if _, err := UnwrapIdentity([]byte("not an age file"), "pw"); err == nil || errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("corrupt identity: got %v, want a non-passphrase failure", err)
	}
}

func TestUnwrapIdentityNamesAnOversizePlaintext(t *testing.T) {
	r, err := age.NewScryptRecipient("pw")
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkFactor(10)
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(bytes.Repeat([]byte("x"), identityReadCap+1)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = UnwrapIdentity(buf.Bytes(), "pw")
	if err == nil || !strings.Contains(err.Error(), "exceeds") || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("oversize identity error = %v", err)
	}
}

func TestInlineKeyBindingMismatch(t *testing.T) {
	// The accident guard: a blob set for one row, delivered as another. The
	// refusal names BOTH keys, so the remedy is obvious.
	id, recipient, _ := newInline(t, "pw")
	blob, err := EncryptValue(recipient, "STRIPE_KEY", KindEnv, []byte("sk"))
	if err != nil {
		t.Fatal(err)
	}
	_, oc, derr := id.DecryptValue("GITHUB_TOKEN", KindEnv, blob)
	if oc != OutcomeRowMismatch || derr == nil {
		t.Fatalf("swapped blob: outcome=%s err=%v", oc, derr)
	}
	msg := derr.Error()
	if !strings.Contains(msg, "stamped for") || !strings.Contains(msg, "GITHUB_TOKEN") || !strings.Contains(msg, "STRIPE_KEY") {
		t.Fatalf("mismatch refusal must name the rule and both keys: %v", derr)
	}

	// The stamped key is decrypted out of a payload anyone holding the public
	// recipient can mint, and this message lands on develop's stderr — so it
	// is quoted, and control bytes never reach the terminal raw.
	esc, err := EncryptValue(recipient, "A\x1b[2JB", KindEnv, []byte("sk"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, derr = id.DecryptValue("STRIPE_KEY", KindEnv, esc)
	if derr == nil || strings.Contains(derr.Error(), "\x1b") {
		t.Fatalf("the stamped key must be quoted, not echoed raw: %v", derr)
	}
}

func TestInlineKindBindingMismatch(t *testing.T) {
	// Same key, other kind: an env value delivered as a tmpfs file (or the
	// reverse) is the same accident with a different shape.
	id, recipient, _ := newInline(t, "pw")
	blob, err := EncryptValue(recipient, "TLS_CERT", KindFile, []byte("cert"))
	if err != nil {
		t.Fatal(err)
	}
	_, oc, derr := id.DecryptValue("TLS_CERT", KindEnv, blob)
	if oc != OutcomeRowMismatch || derr == nil {
		t.Fatalf("kind swap: outcome=%s err=%v", oc, derr)
	}
	msg := derr.Error()
	if !strings.Contains(msg, "stamped for") || !strings.Contains(msg, string(KindFile)) || !strings.Contains(msg, string(KindEnv)) {
		t.Fatalf("kind mismatch refusal must name the rule and both kinds: %v", derr)
	}
}

func TestInlineForeignRecipient(t *testing.T) {
	// A blob encrypted to somebody else's recipient is plain undecryptable —
	// the stamp never gets a chance to speak.
	id, _, _ := newInline(t, "pw")
	_, other, _ := newInline(t, "pw2")
	blob, err := EncryptValue(other, "TOKEN", KindEnv, []byte("theirs"))
	if err != nil {
		t.Fatal(err)
	}
	if _, oc, derr := id.DecryptValue("TOKEN", KindEnv, blob); oc != OutcomeRowUndecryptable || derr == nil {
		t.Fatalf("foreign recipient: outcome=%s err=%v", oc, derr)
	}
}

func TestInlineValueCapAtEncrypt(t *testing.T) {
	_, recipient, _ := newInline(t, "pw")
	big := bytes.Repeat([]byte("x"), MaxValue+1)
	_, err := EncryptValue(recipient, "BIG", KindFile, big)
	if err == nil || !strings.Contains(err.Error(), "per-value cap") ||
		!strings.Contains(err.Error(), "262145") {
		t.Fatalf("oversize value must be refused naming the cap and the size: %v", err)
	}
	// One byte under is fine.
	if _, err := EncryptValue(recipient, "BIG", KindFile, big[:MaxValue]); err != nil {
		t.Fatalf("value at the cap: %v", err)
	}
}

func TestInlineValueCapAtDecrypt(t *testing.T) {
	// The cap is enforced on the way OUT too: a blob minted past it (anyone
	// holding the recipient can) is refused rather than delivered.
	id, recipient, _ := newInline(t, "pw")
	oversize := forgeBlob(t, recipient, valuePayload("BIG", KindFile, bytes.Repeat([]byte("x"), MaxValue+1)))
	_, oc, derr := id.DecryptValue("BIG", KindFile, oversize)
	if oc != OutcomeUnsupportedFormat || derr == nil || !strings.Contains(derr.Error(), "per-value cap") {
		t.Fatalf("oversize payload: outcome=%s err=%v", oc, derr)
	}
	// And the read itself is bounded: a payload far past the cap never lands
	// in memory whole.
	huge := forgeBlob(t, recipient, valuePayload("BIG", KindFile, bytes.Repeat([]byte("x"), MaxValue*4)))
	_, oc, derr = id.DecryptValue("BIG", KindFile, huge)
	if oc != OutcomeUnsupportedFormat || derr == nil || !strings.Contains(derr.Error(), "cap") {
		t.Fatalf("huge payload: outcome=%s err=%v", oc, derr)
	}
}

func TestInlineUnsupportedFormat(t *testing.T) {
	id, recipient, _ := newInline(t, "pw")
	forged := forgeBlob(t, recipient, []byte("byre-credential 999\nSTRIPE_KEY\nenv\nvalue"))
	_, oc, derr := id.DecryptValue("STRIPE_KEY", KindEnv, forged)
	if oc != OutcomeUnsupportedFormat || derr == nil || !strings.Contains(derr.Error(), "format") {
		t.Fatalf("future-format payload: outcome=%s err=%v", oc, derr)
	}
	truncated := forgeBlob(t, recipient, []byte(valueHeader+"\nSTRIPE_KEY"))
	_, oc, derr = id.DecryptValue("STRIPE_KEY", KindEnv, truncated)
	if oc != OutcomeUnsupportedFormat || derr == nil || !strings.Contains(derr.Error(), "truncated") {
		t.Fatalf("truncated payload: outcome=%s err=%v", oc, derr)
	}
}

func TestInlineBindingRefusesUnusableKeyOrKind(t *testing.T) {
	_, recipient, _ := newInline(t, "pw")
	if _, err := EncryptValue(recipient, "TWO\nLINES", KindEnv, []byte("v")); err == nil ||
		!strings.Contains(err.Error(), "line break") {
		t.Fatalf("key with a newline must be refused by the payload rule: %v", err)
	}
	long := strings.Repeat("K", maxKeyBytes+1)
	if _, err := EncryptValue(recipient, long, KindEnv, []byte("v")); err == nil ||
		!strings.Contains(err.Error(), "the config key is 257 bytes") {
		t.Fatalf("oversize key must be refused naming the rule and the size: %v", err)
	}
	if _, err := EncryptValue(recipient, "K", Kind("secret"), []byte("v")); err == nil ||
		!strings.Contains(err.Error(), `kind "secret" invalid`) {
		t.Fatalf("unknown kind must be refused naming the rule and the value: %v", err)
	}
}

func TestInlineEncryptRefusesBadRecipient(t *testing.T) {
	if _, err := EncryptValue("age1notreallyakey", "K", KindEnv, []byte("v")); err == nil ||
		!strings.Contains(err.Error(), "age public key") {
		t.Fatalf("bad recipient must be refused naming the rule: %v", err)
	}
	if err := ValidateRecipient("age1notreallyakey"); err == nil ||
		!strings.Contains(err.Error(), "age public key") {
		t.Fatalf("ValidateRecipient: %v", err)
	}
	_, recipient, _ := newInline(t, "pw")
	if err := ValidateRecipient(recipient); err != nil {
		t.Fatalf("ValidateRecipient(%q): %v", recipient, err)
	}
}

func TestInlineRekeyLeavesValueBlobsByteIdentical(t *testing.T) {
	// Load-bearing for drift: the passphrase wraps only the identity, so a
	// rekey rewrites ONE field and no value blob changes. Byte-compare over a
	// rekey is what lets drift treat a credential row as any other value.
	id, recipient, _ := newInline(t, "pw")
	blob, err := EncryptValue(recipient, "STRIPE_KEY", KindEnv, []byte("sk-live"))
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(blob)

	rewrapped, err := id.Rewrap("new-pw")
	if err != nil {
		t.Fatalf("Rewrap: %v", err)
	}
	if !bytes.Equal(blob, before) {
		t.Fatal("rekey must not touch value blobs")
	}
	if _, err := UnwrapIdentity(rewrapped, "pw"); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("old passphrase after rekey: got %v, want ErrBadPassphrase", err)
	}
	id2, err := UnwrapIdentity(rewrapped, "new-pw")
	if err != nil {
		t.Fatalf("UnwrapIdentity after rekey: %v", err)
	}
	if id2.Recipient() != recipient {
		t.Fatalf("rekey rotated the identity: recipient %q != %q", id2.Recipient(), recipient)
	}
	got, oc, derr := id2.DecryptValue("STRIPE_KEY", KindEnv, blob)
	if oc != "" || derr != nil || string(got) != "sk-live" {
		t.Fatalf("value after rekey: outcome=%s err=%v value=%q", oc, derr, got)
	}
}

// forgeBlob encrypts an arbitrary payload to a recipient — how a test mints
// what anyone holding the public half could: the key/kind stamp is an
// accident guard, not integrity, and these tests say so by exercising it.
func forgeBlob(t *testing.T, recipient string, payload []byte) []byte {
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
