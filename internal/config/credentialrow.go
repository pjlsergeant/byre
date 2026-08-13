package config

// Credential rows: an env row whose value is an age ciphertext instead of a
// literal, and the file-local [credentials] block that can decrypt it.
//
//	[env]
//	STRIPE_KEY = "encrypted:<base64>"        # delivered as an env var
//	TLS_CERT   = "encrypted-file:<base64>"   # delivered as a tmpfs file
//
//	[credentials]
//	identity  = "<passphrase-wrapped age identity, base64>"
//	recipient = "age1…"                      # the cleartext public half
//
// Resolution is the ORDINARY cascade merge: the winning row is the value, and
// a nearer layer overrides or empties it exactly as it would a literal.
//
// The [credentials] block is FILE-LOCAL and never merges. That is a semantic,
// so it is enforced structurally: the block is not a Config field at all —
// nothing can merge what the merged type cannot hold. It is parsed out of one
// file's bytes, and a row decrypts only against the block of the file that
// contributed it. A project's identity must never be reached for to open a
// layer's row.
//
// The scheme spelling lives here, not in internal/credentials: that package
// deals in raw bytes and knows nothing about config syntax.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/pjlsergeant/byre/internal/credentials"
)

const (
	// EncryptedScheme marks an env row delivered as an environment variable;
	// EncryptedFileScheme one written to the session tmpfs with its path
	// carried in the variable. The kind rides the scheme, so a row states
	// what it becomes in the box without a second key.
	EncryptedScheme     = "encrypted:"
	EncryptedFileScheme = "encrypted-file:"
)

// EncryptedRow is one env row carrying a credential: the key, the kind its
// scheme names, and the age ciphertext its payload carries (base64-decoded).
type EncryptedRow struct {
	Key  string
	Kind credentials.Kind
	Blob []byte
}

// ParseEncryptedRow decodes one env row value. ok is false for an ordinary
// literal (including "", the idiomatic disable) — every other env value is
// still just a value. An error means the row NAMES a credential scheme and
// its payload is unusable, which is a stop, not a fallback to literal: a
// value silently delivered as "encrypted:AAAA" would be a credential leak
// spelled as a typo.
func ParseEncryptedRow(key, value string) (EncryptedRow, bool, error) {
	var kind credentials.Kind
	var scheme, payload string
	if p, ok := strings.CutPrefix(value, EncryptedScheme); ok {
		kind, scheme, payload = credentials.KindEnv, EncryptedScheme, p
	} else if p, ok := strings.CutPrefix(value, EncryptedFileScheme); ok {
		kind, scheme, payload = credentials.KindFile, EncryptedFileScheme, p
	} else {
		return EncryptedRow{}, false, nil
	}
	if payload == "" {
		return EncryptedRow{}, false, fmt.Errorf("env %s: the %s scheme carries no ciphertext", key, scheme)
	}
	blob, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return EncryptedRow{}, false, fmt.Errorf("env %s: the encrypted value is not valid base64 (%s)", key, echo(payload))
	}
	return EncryptedRow{Key: key, Kind: kind, Blob: blob}, true, nil
}

// FormatEncryptedRow spells a ciphertext as the row value a config file
// carries.
func FormatEncryptedRow(kind credentials.Kind, blob []byte) (string, error) {
	switch kind {
	case credentials.KindEnv:
		return EncryptedScheme + base64.StdEncoding.EncodeToString(blob), nil
	case credentials.KindFile:
		return EncryptedFileScheme + base64.StdEncoding.EncodeToString(blob), nil
	}
	return "", fmt.Errorf("credential kind %q invalid (want %s|%s)", echo(string(kind)), credentials.KindEnv, credentials.KindFile)
}

// CredentialsBlock is one physical config file's [credentials] table: the
// passphrase-wrapped identity that opens the file's rows, and the cleartext
// recipient new values are encrypted to (which is why setting one never
// prompts).
type CredentialsBlock struct {
	Identity  []byte // the wrapped identity blob, base64 in the file
	Recipient string
}

// ParseCredentialsBlock reads the [credentials] table out of one config
// file's bytes. ok is false when the file carries no block.
//
// Deliberately not a Config field and not part of Parse: a file-local table
// that reached the merged Config could be merged, and a project block
// decrypting a layer's row is precisely the accident this design forbids.
// The decode is strict within the table (an unknown key is a typo, not a
// silent default), which is also why it cannot ride Config's strict decode —
// that one would reject every other key in the file.
func ParseCredentialsBlock(raw []byte) (CredentialsBlock, bool, error) {
	var doc struct {
		Credentials any `toml:"credentials"`
	}
	if err := toml.Unmarshal(raw, &doc); err != nil {
		return CredentialsBlock{}, false, err
	}
	if doc.Credentials == nil {
		return CredentialsBlock{}, false, nil
	}
	table, ok := doc.Credentials.(map[string]any)
	if !ok {
		return CredentialsBlock{}, false, errors.New("credentials: must be a [credentials] table holding identity and recipient")
	}
	var b CredentialsBlock
	var identity string
	for _, k := range slices.Sorted(maps.Keys(table)) {
		s, ok := table[k].(string)
		if !ok {
			return CredentialsBlock{}, false, fmt.Errorf("credentials: %s must be a string", k)
		}
		switch k {
		case "identity":
			identity = s
		case "recipient":
			b.Recipient = s
		default:
			return CredentialsBlock{}, false, fmt.Errorf("credentials: unknown key %q (want identity, recipient)", echo(k))
		}
	}
	if identity == "" || b.Recipient == "" {
		return CredentialsBlock{}, false, errors.New("credentials: needs both identity (the wrapped key, base64) and recipient (age1…)")
	}
	wrapped, err := base64.StdEncoding.DecodeString(identity)
	if err != nil {
		return CredentialsBlock{}, false, fmt.Errorf("credentials: identity is not valid base64 (%s)", echo(identity))
	}
	if err := credentials.ValidateRecipient(b.Recipient); err != nil {
		return CredentialsBlock{}, false, fmt.Errorf("credentials: %w", err)
	}
	b.Identity = wrapped
	return b, true, nil
}

// CredentialFile is the winning encrypted rows ONE physical cascade file
// contributed, with that file's own credentials block — the grouping the
// unlock flow prompts over (one passphrase per contributing file, root-most
// first).
type CredentialFile struct {
	Label string // "default", "template:go", "layer:acme", "project"
	Path  string
	// Block opens this file's rows, and only this file's rows. HasBlock is
	// false when the file carries encrypted rows but no block at all — a row
	// copied without its identity. It is reported as that, never repaired
	// from a neighbouring file.
	Block    CredentialsBlock
	HasBlock bool
	// Rows are this file's winning encrypted rows, sorted by key.
	Rows []EncryptedRow
}

// EncryptedRows resolves the cascade's winning encrypted env rows and groups
// them by the file that contributed each, in merge order (root-most first —
// the order the unlock prompts in). A key a nearer file overrides with a
// literal, or empties, is not a credential row and does not appear.
//
// Files carrying no winning encrypted row are absent from the result,
// including files that carry a [credentials] block: an identity nothing needs
// is never unlocked and never prompted for.
func EncryptedRows(files []CascadeFile) ([]CredentialFile, error) {
	winner := map[string]int{}
	for i, f := range files {
		for k := range f.Cfg.Env {
			winner[k] = i
		}
	}
	var out []CredentialFile
	for i, f := range files {
		var rows []EncryptedRow
		for _, k := range slices.Sorted(maps.Keys(f.Cfg.Env)) {
			if winner[k] != i {
				continue
			}
			row, ok, err := ParseEncryptedRow(k, f.Cfg.Env[k])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f.attribution(), err)
			}
			if ok {
				rows = append(rows, row)
			}
		}
		if len(rows) == 0 {
			continue
		}
		block, has, err := ParseCredentialsBlock(f.Raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.attribution(), err)
		}
		out = append(out, CredentialFile{Label: f.Label, Path: f.Path, Block: block, HasBlock: has, Rows: rows})
	}
	return out, nil
}

// attribution names a cascade file the way a refusal should: the label a user
// recognizes, and the path to edit when there is one.
func (f CascadeFile) attribution() string {
	if f.Path == "" {
		return f.Label
	}
	return f.Label + " (" + f.Path + ")"
}

// echo bounds a rejected value so a pasted wall of ciphertext cannot become
// the message.
func echo(s string) string {
	r := []rune(s)
	if len(r) > 32 {
		return string(append(r[:32], '…'))
	}
	return s
}
