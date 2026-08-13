// Package credentials is byre's credential crypto: a scrypt passphrase-wrapped
// age identity that lives in a config file's own [credentials] block, and the
// per-value payloads encrypted to that identity's recipient which live as
// env_from_host rows beside it. The security content is confidentiality at
// rest against off-box disk access; it claims no integrity against its own
// files (anyone who can write the config can roll back, forge, or delete —
// the disclosed store-integrity residual), and the key/kind stamps inside a
// payload are accident guards, not integrity mechanisms.
//
// Nothing here touches the filesystem: the config layer carries the blobs in
// and out, which is what keeps a file-local identity structurally file-local.
// See inline.go for the value crypto; this file holds the passphrase wrap the
// identity rides on and the rules a value is held to.
package credentials

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"filippo.io/age"
)

const (
	// scryptMaxWorkFactor bounds the unwrap so a corrupt or
	// absurdly-parameterised header cannot stall the launch — a liveness
	// bound, never a defense.
	scryptMaxWorkFactor = 20

	// identityReadCap bounds the unwrapped identity read. An age X25519
	// identity string is ~75 bytes; this is a generous multiple.
	identityReadCap = 16 << 10

	// MaxEnvValue caps an env-kind value (headroom under MAX_ARG_STRLEN);
	// MaxValue is the file-kind ceiling.
	MaxEnvValue = 64 << 10
)

// ValueState is the one spelling of the value-state cell: "set" when a row
// carries a value, "unset" otherwise. Every surface that renders the pair
// speaks through it, so a third word cannot land on one surface only.
func ValueState(stored bool) string {
	if stored {
		return "set"
	}
	return "unset"
}

// EmptyPassphraseWorthless is the shared refusal prose for an empty
// passphrase: the CLI and the unlock prompt both speak it.
const EmptyPassphraseWorthless = "an empty passphrase would leave the at-rest encryption worthless"

// ErrBadPassphrase is the wrong-passphrase answer, distinguished from a
// corrupt or oversize identity so a prompt can re-ask (bounded) on a typo but
// not on a damaged blob.
var ErrBadPassphrase = errors.New("wrong passphrase")

// scryptWorkFactor is the pinned creation work factor (age's own default
// tier). A var only for the test seam: the suite lowers it so every unlock
// doesn't pay production's deliberate hundreds of milliseconds.
var scryptWorkFactor = 18

// SetWorkFactorForTesting lowers the identity-wrap work factor so suites in
// OTHER packages (commands' launch wiring) don't pay production's deliberate
// unlock cost on every test vault. Test harness only; production never
// calls it.
func SetWorkFactorForTesting(logN int) { scryptWorkFactor = logN }

// wrapIdentity age-encrypts the identity string under the passphrase at the
// pinned work factor.
func wrapIdentity(id *age.X25519Identity, passphrase string) ([]byte, error) {
	r, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}
	r.SetWorkFactor(scryptWorkFactor)
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, r)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(w, id.String()); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// unwrapIdentity unwraps a scrypt-wrapped identity blob — the expensive
// step, shared by the vault's identity file and a config file's own
// [credentials] block, so both pay the same pinned work factor and answer a
// typo the same way. ErrBadPassphrase distinguishes a wrong passphrase
// (re-askable) from a corrupt or oversize blob.
func unwrapIdentity(wrapped []byte, passphrase string) (*age.X25519Identity, error) {
	sid, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, err
	}
	sid.SetMaxWorkFactor(scryptMaxWorkFactor)
	rd, err := age.Decrypt(bytes.NewReader(wrapped), sid)
	if err != nil {
		return nil, passphraseOrCause(err)
	}
	idStr, err := io.ReadAll(io.LimitReader(rd, identityReadCap))
	if err != nil {
		return nil, passphraseOrCause(err)
	}
	return age.ParseX25519Identity(strings.TrimSpace(string(idStr)))
}

// passphraseOrCause maps age's no-matching-identity answer to
// ErrBadPassphrase and passes anything else through as the real cause.
func passphraseOrCause(err error) error {
	var noMatch *age.NoIdentityMatchError
	if errors.As(err, &noMatch) {
		return ErrBadPassphrase
	}
	return err
}

// ValidateValue holds a value to its kind's constraints: an env-kind value is
// NUL-free and capped at MaxEnvValue, because an environment variable cannot
// carry NUL through the launcher's export and MAX_ARG_STRLEN bounds the rest;
// a file-kind value is arbitrary bytes up to the per-value ceiling.
func ValidateValue(value []byte, kind Kind) error {
	if kind == KindEnv {
		if bytes.IndexByte(value, 0) >= 0 {
			return errors.New("an env credential cannot contain NUL bytes (use the encrypted-file: scheme for binary content)")
		}
		if len(value) > MaxEnvValue {
			return fmt.Errorf("env credential is %d bytes; the cap is %d (use the encrypted-file: scheme for large content)", len(value), MaxEnvValue)
		}
		return nil
	}
	if len(value) > MaxValue {
		return fmt.Errorf("credential is %d bytes; the cap is %d", len(value), MaxValue)
	}
	return nil
}

// StripTrailingNewline removes ONE trailing newline (and a preceding CR) —
// the entry-path courtesy for env values typed or piped in (echo without
// -n, an editor's final newline); declared file-kind values skip it.
func StripTrailingNewline(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte("\n"))
	return bytes.TrimSuffix(b, []byte("\r"))
}
