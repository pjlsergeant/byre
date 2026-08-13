package credentials

// inline.go is the credential crypto as BLOBS: a scrypt passphrase-wrapped
// age X25519 identity, and per-value payloads encrypted to that identity's
// recipient. Nothing here touches the filesystem and nothing here knows how a
// config file spells a credential row — the caller carries the blobs (the
// design puts them base64 in the row and the wrapped identity in the same
// file's own [credentials] block).
//
// The passphrase protects the IDENTITY only. Values are encrypted to the
// cleartext recipient, so writing one never prompts, and re-wrapping the
// identity under a new passphrase (Rewrap) leaves every value blob
// byte-identical — which is what lets drift compare credential rows as plain
// bytes.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"filippo.io/age"
)

const (
	// MaxValue caps one credential's plaintext. No real credential is big;
	// base64 + age overhead keeps several such values inside the 1 MiB
	// config read bound, so nothing else has to grow.
	MaxValue = 256 << 10

	// maxKeyBytes bounds the config key stamped into a payload. The key is a
	// header field, so it must be bounded for the decrypt read to be
	// bounded; env var names are far shorter than this.
	maxKeyBytes = 256

	// valueHeader is the value payload's format line. A byre that does not
	// know a header reports unsupported-format rather than misparsing it,
	// and the version distinguishes this payload's key/kind stamps from any
	// earlier shape a file might still carry.
	valueHeader = "byre-credential 2"

	// valueReadCap bounds the plaintext read at decrypt: the largest legal
	// payload plus one byte, so an oversize value is caught by the read
	// rather than by trusting the header.
	valueReadCap = len(valueHeader) + 1 + maxKeyBytes + 1 + len("file") + 1 + MaxValue + 1
)

// Kind is how a credential value lands in the box: KindEnv exported as an
// environment variable, KindFile written to the session tmpfs with its path
// carried in one. It is stamped into the payload, so a blob set for one kind
// cannot be delivered as the other.
type Kind string

const (
	KindEnv  Kind = "env"
	KindFile Kind = "file"
)

// Valid reports whether k is one of the two kinds.
func (k Kind) Valid() bool { return k == KindEnv || k == KindFile }

// NewIdentity mints a fresh X25519 identity and returns it wrapped under
// passphrase — the blob a config file's [credentials] block carries — plus
// its cleartext recipient, which is what values are encrypted to.
func NewIdentity(passphrase string) (wrapped []byte, recipient string, err error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, "", fmt.Errorf("generating the credentials identity: %w", err)
	}
	wrapped, err = wrapIdentity(id, passphrase)
	if err != nil {
		return nil, "", err
	}
	return wrapped, id.Recipient().String(), nil
}

// Identity is an unwrapped credential identity: the private half, live in
// host process memory for as long as the caller holds it and never persisted
// (transient memory residency is the disclosed swap/core residual).
type Identity struct {
	id *age.X25519Identity
}

// UnwrapIdentity unwraps a wrapped identity blob under passphrase — the
// expensive scrypt step. A wrong passphrase returns ErrBadPassphrase (the
// prompt re-asks, bounded); anything else is a corrupt or oversize blob, and
// re-asking would not help.
func UnwrapIdentity(wrapped []byte, passphrase string) (*Identity, error) {
	id, err := unwrapIdentity(wrapped, passphrase)
	if err != nil {
		if errors.Is(err, ErrBadPassphrase) {
			return nil, err
		}
		return nil, fmt.Errorf("credentials identity: %w", err)
	}
	return &Identity{id: id}, nil
}

// Recipient is the identity's cleartext public half — what a config file's
// [credentials] block carries so `set` can encrypt without the passphrase.
func (i *Identity) Recipient() string { return i.id.Recipient().String() }

// Rewrap re-wraps this identity under a new passphrase: the rekey. The
// identity itself does not rotate, so every value encrypted to its recipient
// keeps decrypting and every stored blob stays byte-identical.
func (i *Identity) Rewrap(passphrase string) ([]byte, error) {
	return wrapIdentity(i.id, passphrase)
}

// ValidateRecipient checks a cleartext recipient string, so the config layer
// can refuse an unusable [credentials] block at parse without importing age.
func ValidateRecipient(recipient string) error {
	if _, err := age.ParseX25519Recipient(recipient); err != nil {
		return fmt.Errorf("recipient %q is not an age public key (want age1…)", Echo(recipient))
	}
	return nil
}

// EncryptValue encrypts one credential value to recipient, stamped with the
// config key and kind it was set for. No passphrase: the recipient is the
// cleartext half, which is the whole point of the split.
func EncryptValue(recipient, key string, kind Kind, value []byte) ([]byte, error) {
	if err := validateBinding(key, kind); err != nil {
		return nil, err
	}
	if len(value) > MaxValue {
		return nil, fmt.Errorf("credential %s: value is %d bytes; the per-value cap is %d", key, len(value), MaxValue)
	}
	rcp, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return nil, fmt.Errorf("credential %s: recipient %q is not an age public key — the file's [credentials] block is damaged", key, Echo(recipient))
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, rcp)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(valuePayload(key, kind, value)); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecryptValue decrypts one value blob and holds it to the key and kind it is
// being delivered AS. Success is the zero Outcome ("") with a nil error; a
// failure outcome's error carries the notice text for the row.
//
// The key/kind stamp is an ACCIDENT guard, not integrity: it catches a blob
// swapped between rows, replayed from history onto a renamed key,
// transplanted from another file, or pasted into the wrong kind. Anyone
// holding the public recipient can mint a correctly-stamped blob, so it
// proves nothing about who wrote the value.
func (i *Identity) DecryptValue(key string, kind Kind, blob []byte) ([]byte, Outcome, error) {
	if err := validateBinding(key, kind); err != nil {
		return nil, OutcomeUnsupportedFormat, err
	}
	rd, err := age.Decrypt(bytes.NewReader(blob), i.id)
	if err != nil {
		return nil, OutcomeRowUndecryptable, fmt.Errorf("credential %s: corrupt, or encrypted to a different recipient: %v", key, err)
	}
	payload, err := io.ReadAll(io.LimitReader(rd, int64(valueReadCap)))
	if err != nil {
		return nil, OutcomeRowUndecryptable, fmt.Errorf("credential %s: corrupt, or encrypted to a different recipient: %v", key, err)
	}
	if len(payload) == valueReadCap {
		return nil, OutcomeUnsupportedFormat, fmt.Errorf("credential %s: the stored value exceeds the per-value cap of %d bytes", key, MaxValue)
	}
	gotKey, gotKind, value, err := parseValuePayload(payload)
	if err != nil {
		return nil, OutcomeUnsupportedFormat, fmt.Errorf("credential %s: %w", key, err)
	}
	if gotKey != key || gotKind != kind {
		// %q, not %s: the stamped key and kind come out of a payload anyone
		// holding the public recipient can mint, and this message lands on
		// develop's stderr — an unquoted one could carry ESC or CR and
		// rewrite the terminal around the refusal.
		return nil, OutcomeRowMismatch, fmt.Errorf("credential %s (%s): the stored value is stamped for %q (%q) — a blob copied from another row, a renamed key, or a value restored from history? Not delivering it", key, kind, Echo(gotKey), Echo(string(gotKind)))
	}
	if len(value) > MaxValue {
		return nil, OutcomeUnsupportedFormat, fmt.Errorf("credential %s: the stored value is %d bytes; the per-value cap is %d", key, len(value), MaxValue)
	}
	return value, "", nil
}

// validateBinding holds the two stamped fields to what the payload format can
// carry: the key is a header line (so it cannot contain a newline, and is
// bounded so the decrypt read can be), and the kind is one of the two.
func validateBinding(key string, kind Kind) error {
	if key == "" {
		return errors.New("credential: no config key to bind the value to")
	}
	if strings.ContainsAny(key, "\n\r") {
		return fmt.Errorf("credential %q: a config key cannot contain a line break", Echo(key))
	}
	if len(key) > maxKeyBytes {
		return fmt.Errorf("credential %q: the config key is %d bytes; the cap is %d", Echo(key), len(key), maxKeyBytes)
	}
	if !kind.Valid() {
		return fmt.Errorf("credential %s: kind %q invalid (want %s|%s)", key, Echo(string(kind)), KindEnv, KindFile)
	}
	return nil
}

// valuePayload composes the value payload: the format line, the key and kind
// stamps, then the raw value bytes.
func valuePayload(key string, kind Kind, value []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\n%s\n%s\n", valueHeader, key, kind)
	b.Write(value)
	return b.Bytes()
}

// parseValuePayload splits a decrypted value payload. A header line this byre
// does not know is unsupported-format (written by a newer byre?), never a
// misparse.
func parseValuePayload(p []byte) (key string, kind Kind, value []byte, err error) {
	rest, ok := bytes.CutPrefix(p, []byte(valueHeader+"\n"))
	if !ok {
		return "", "", nil, errors.New("the stored value's format is not one this byre understands (written by a newer byre?)")
	}
	i := bytes.IndexByte(rest, '\n')
	if i < 0 {
		return "", "", nil, errors.New("the stored value is truncated")
	}
	key = string(rest[:i])
	rest = rest[i+1:]
	j := bytes.IndexByte(rest, '\n')
	if j < 0 {
		return "", "", nil, errors.New("the stored value is truncated")
	}
	return key, Kind(rest[:j]), rest[j+1:], nil
}

// Echo bounds a value echoed into an error message so a pasted wall of input
// cannot become the message. One owner for every credential surface (config's
// row parsing and the commands' flag echoes included): a terminal-safety or
// escaping fix lands once, not per copy.
func Echo(s string) string {
	r := []rune(s)
	if len(r) > 64 {
		return string(append(r[:64], '…'))
	}
	return s
}
