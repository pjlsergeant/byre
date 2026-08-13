package config

// Credential rows: an env_from_host row whose source is an age ciphertext
// instead of a host-value scheme, and the file-local [credentials] block that
// can decrypt it.
//
//	[env_from_host]
//	STRIPE_KEY = "encrypted:<base64>"        # delivered as an env var
//	TLS_CERT   = "encrypted-file:<base64>"   # delivered as a tmpfs file
//
//	[credentials]
//	identity  = "<passphrase-wrapped age identity, base64>"
//	recipient = "age1…"                      # the cleartext public half
//
// env_from_host, not [env]: it is already a closed scheme set (git:/env:/tz:)
// gaining two members, its "" disable is already the per-project override
// idiom, and its values are resolved at RUNTIME — an [env] row rides the
// Dockerfile ENV bake, which would put ciphertext in the image and force a
// rebuild on every re-set. [env] literals stay unrestricted, so a literal
// beginning "encrypted:" is still a literal.
//
// Resolution is the ORDINARY cascade merge: the winning row is the value, and
// a nearer layer overrides or empties it exactly as it would any other source.
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

// ReservedCredentialItem is the one name the delivery stream reserves: the
// receiver writes the launcher's export manifest under it. It is also a legal
// environment variable name, so nothing about the env grammar keeps a
// credential off it — the reservation has to be stated.
const ReservedCredentialItem = "manifest"

// ValidateCredentialKey refuses the one env key a credential may not use.
// Every other env_from_host rule is ValidateEnvFromHostKey's; this is the
// extra one a CREDENTIAL carries, because a credential value travels to the
// box under its config key and the manifest travels under this name.
func ValidateCredentialKey(key string) error {
	if key == ReservedCredentialItem {
		return fmt.Errorf("%s %s: %q is reserved — a credential travels to the box under its config key, and byre's own export manifest travels under that name; rename the row (byre credentials unset %s, then set it under another key)",
			EnvFromHostTable, key, ReservedCredentialItem, key)
	}
	return nil
}

// ParseEncryptedRow decodes one row value. ok is false for any other source
// (including "", the idiomatic disable). An error means the row NAMES a
// credential scheme and its payload is unusable, which is a stop, not a
// fallback: a value silently delivered as "encrypted:AAAA" would be a
// credential leak spelled as a typo.
//
// The reserved-key check lives here because this is the one gate every reader
// of a credential row passes — the launch-time collection included — so a row
// hand-written into a file cannot reach the delivery stream either.
//
// Table-agnostic on purpose — the CALLER decides which table the schemes are
// legal in, which is how [env] keeps accepting "encrypted:…" as a literal.
func ParseEncryptedRow(key, value string) (EncryptedRow, bool, error) {
	kind, scheme, payload, ok := cutEncryptedScheme(value)
	if !ok {
		return EncryptedRow{}, false, nil
	}
	if err := ValidateCredentialKey(key); err != nil {
		return EncryptedRow{}, false, err
	}
	blob, err := decodeEncryptedPayload(scheme, payload)
	if err != nil {
		return EncryptedRow{}, false, fmt.Errorf("%s %s: %w", EnvFromHostTable, key, err)
	}
	return EncryptedRow{Key: key, Kind: kind, Blob: blob}, true, nil
}

// EnvFromHostTable is the table credential rows live in, named in every
// refusal so a user knows which block to edit.
const EnvFromHostTable = "env_from_host"

// cutEncryptedScheme splits a value on the two credential schemes; the kind
// rides the scheme, so a row states what it becomes in the box.
func cutEncryptedScheme(value string) (kind credentials.Kind, scheme, payload string, ok bool) {
	// encrypted-file: is tested first — "encrypted:" is not a prefix of it,
	// but keeping the longer scheme first makes that independent of spelling.
	if p, found := strings.CutPrefix(value, EncryptedFileScheme); found {
		return credentials.KindFile, EncryptedFileScheme, p, true
	}
	if p, found := strings.CutPrefix(value, EncryptedScheme); found {
		return credentials.KindEnv, EncryptedScheme, p, true
	}
	return "", "", "", false
}

// decodeEncryptedPayload is the RECOGNITION-level check both the row parse
// and env_from_host's scheme validation share: a payload is present and it is
// base64. Nothing deeper — a row whose ciphertext is damaged must still let
// the editor open, save, and repair the file it sits in.
func decodeEncryptedPayload(scheme, payload string) ([]byte, error) {
	if payload == "" {
		return nil, fmt.Errorf("the %s scheme carries no ciphertext", scheme)
	}
	blob, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		// %q, like every other echo here: the payload is file content, and an
		// unquoted one could carry ESC or CR onto the terminal this refusal
		// prints to.
		return nil, fmt.Errorf("the encrypted value is not valid base64 (%q)", echo(payload))
	}
	return blob, nil
}

// validateEncryptedSource is validateHostSource's arm for the two credential
// schemes: recognition only (see decodeEncryptedPayload). ok is false when
// the source names neither scheme, so the caller falls through to git:/env:/tz:.
func validateEncryptedSource(src string) (bool, error) {
	_, scheme, payload, ok := cutEncryptedScheme(src)
	if !ok {
		return false, nil
	}
	if _, err := decodeEncryptedPayload(scheme, payload); err != nil {
		return true, err
	}
	return true, nil
}

// IsCredentialSource reports whether an env_from_host source names one of the
// credential schemes. It is THE predicate for "is this row a credential row",
// asked by every surface that has to treat one differently: the renderer that
// must not call it a host passthrough, the editor that must refuse to open it
// in a picker, the exposure tally that counts it as a credential, and the
// unset verb that removes it.
//
// Deliberately NOT ParseEncryptedRow's ok, which those sites used to ask and
// which is false for a row that names a scheme and cannot be USED — a damaged
// payload, or one on the reserved `manifest` key. Such a row is still a
// credential row: it is what the list shows, it never joins the -e export,
// and it is the row that most needs the picker kept off it. Ask this to
// classify; ask ParseEncryptedRow only when you need the ciphertext.
func IsCredentialSource(src string) bool {
	_, _, _, ok := cutEncryptedScheme(src)
	return ok
}

// RenderSource is the one eliding renderer every surface that displays an
// env_from_host source goes through: an ordinary source is short and reads as
// written, and a credential's ciphertext collapses to its scheme. The scheme
// is the part a reader needs (what this row IS); the payload is a wall of
// base64 that would bury the rest of a consent gate or an editor row, and
// showing it buys nobody anything.
func RenderSource(src string) string {
	if _, scheme, _, ok := cutEncryptedScheme(src); ok {
		return scheme + "[…]"
	}
	return src
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
		return CredentialsBlock{}, false, fmt.Errorf("credentials: identity is not valid base64 (%q)", echo(identity))
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

// EncryptedRows resolves the cascade's winning encrypted env_from_host rows
// and groups them by the file that contributed each, in merge order
// (root-most first — the order the unlock prompts in). A key a nearer file
// overrides with another source, or empties, is not a credential row and does
// not appear.
//
// An explicit [env] key ANYWHERE in the cascade takes the row out too: that
// is env_from_host's standing precedence (ADR 0026, resolveHostEnv's
// overridden state), and a credential that quietly beat the literal would
// invert it — while still costing a passphrase prompt for a value the box
// never sees.
//
// Files carrying no winning encrypted row are absent from the result,
// including files that carry a [credentials] block: an identity nothing needs
// is never unlocked and never prompted for. An OVERRIDDEN row is not read at
// all — a broken one a nearer file already replaced is not a problem anybody
// has.
func EncryptedRows(files []CascadeFile) ([]CredentialFile, error) {
	winner := map[string]int{}
	overridden := map[string]bool{}
	for i, f := range files {
		for k := range f.Cfg.EnvFromHost {
			winner[k] = i
		}
		for k := range f.Cfg.Env {
			overridden[k] = true
		}
	}
	var out []CredentialFile
	for i, f := range files {
		var rows []EncryptedRow
		for _, k := range slices.Sorted(maps.Keys(f.Cfg.EnvFromHost)) {
			if winner[k] != i || overridden[k] {
				continue
			}
			row, ok, err := ParseEncryptedRow(k, f.Cfg.EnvFromHost[k])
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

// echo bounds a rejected value so a pasted wall of ciphertext cannot become
// the message.
func echo(s string) string {
	r := []rune(s)
	if len(r) > 32 {
		return string(append(r[:32], '…'))
	}
	return s
}
