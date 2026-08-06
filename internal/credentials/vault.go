// Package credentials is the host-side project-credentials vault
// (wip/secure-credentials.md): named values age-encrypted at rest under the
// project store, unlocked per launch by a passphrase, delivered into the box
// by the launch flow. The vault's security content is confidentiality at
// rest against off-box disk access; it claims no integrity against its own
// store (a store writer can roll back, forge, or delete — the disclosed
// store-integrity residual). The name/project-id stamps inside each entry
// are accident guards (cross-project copy, wrong-project restore), not
// integrity mechanisms.
//
// Layout under <store>/credentials/:
//
//	identity.age      scrypt-wrapped X25519 identity — the only object the
//	                  passphrase unlocks
//	entries/<name>.age one ciphertext per credential, encrypted to the
//	                  recipient (cold staged writes need no passphrase)
//	index.toml        {recipient, project-id, display cache} — machine-
//	                  authored whole-file, outside ADR 0044's tomldoc scope
//
// Callers hold the project setup lock around anything that WRITES the vault
// or reads entries for delivery (the read-once decrypt); the expensive
// scrypt unwrap (Unlock) deliberately runs WITHOUT the lock — holding it
// there would stall sibling worktrees for an authentication cost.
//
// All I/O rides hostopen (the standing repo rule: the store is
// agent-authorable under --self-edit, so reads are fd-judged, bounded, and
// nofollow — robustness, not a credential-specific defense).
package credentials

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"filippo.io/age"
	toml "github.com/pelletier/go-toml/v2"

	"github.com/pjlsergeant/byre/internal/hostopen"
)

const (
	// DirName is the vault directory under the project store.
	DirName = "credentials"

	identityName = "identity.age"
	indexName    = "index.toml"
	entriesDir   = "entries"

	// scryptMaxWorkFactor bounds the unwrap so a corrupt or
	// absurdly-parameterised header cannot stall the launch — a liveness
	// bound, per the brief.
	scryptMaxWorkFactor = 20

	// The pre-launch read caps (every pre-launch read is bounded — brief).
	// Each is a generous multiple of the largest legitimate file: an
	// identity.age is ~400 bytes, an index.toml a few KiB even with many
	// entries, an entry ciphertext the value cap plus age overhead.
	identityReadCap = 16 << 10
	indexReadCap    = 256 << 10
	entryReadCap    = MaxFileValue + (64 << 10)

	// MaxEnvValue caps an env-kind value (headroom under MAX_ARG_STRLEN;
	// brief pins 64 KiB). MaxFileValue caps a file-kind value, generously.
	MaxEnvValue  = 64 << 10
	MaxFileValue = 4 << 20

	// payloadHeader is the entry payload format line. Version bumps make an
	// old byre report unsupported-format rather than misparse.
	payloadHeader = "byre-credential 1"
)

// NameRe is the credential name grammar the brief pins. Tighter than the
// named-declaration genus grammar (must start with a letter): the name is a
// filename under entries/ and a tmpfs filename in the box.
var NameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// scryptWorkFactor is the pinned creation work factor (age's own default
// tier). A var only for the test seam: the suite lowers it so every unlock
// doesn't pay production's deliberate hundreds of milliseconds.
var scryptWorkFactor = 18

// SetWorkFactorForTesting lowers the identity-wrap work factor so suites in
// OTHER packages (commands' launch wiring) don't pay production's deliberate
// unlock cost on every test vault. Test harness only; production never
// calls it.
func SetWorkFactorForTesting(logN int) { scryptWorkFactor = logN }

// ErrVaultExists is Create's refusal when a vault directory already exists
// (--replace is the explicit discard-and-recreate).
var ErrVaultExists = errors.New("a credentials vault already exists for this project (byre credentials init --replace discards and recreates it)")

// ErrNoVault marks operations that need a vault none exists for.
var ErrNoVault = errors.New("no credentials vault exists for this project (create one: byre credentials init)")

// ErrBadPassphrase is Unlock's wrong-passphrase answer, distinguished from
// a corrupt/oversize identity so the prompt can re-ask (bounded) on a typo
// but not on a damaged file.
var ErrBadPassphrase = errors.New("wrong passphrase")

// Vault is a project's credential store handle. Zero I/O at construction;
// every method touches the filesystem when called.
type Vault struct {
	storeDir  string // ~/.byre/projects/<id>
	projectID string
}

// Open returns the vault handle for a project store. storeDir is
// project.Paths.Dir; projectID is Paths.ID (stamped into entry payloads as
// the wrong-project accident guard).
func Open(storeDir, projectID string) *Vault {
	return &Vault{storeDir: storeDir, projectID: projectID}
}

func (v *Vault) dir() string          { return filepath.Join(v.storeDir, DirName) }
func (v *Vault) identityPath() string { return filepath.Join(v.dir(), identityName) }
func (v *Vault) indexPath() string    { return filepath.Join(v.dir(), indexName) }
func (v *Vault) entryPath(name string) string {
	return filepath.Join(v.dir(), entriesDir, name+".age")
}

// Exists reports whether a vault directory is present. A directory with a
// missing identity is still "exists" — Create must refuse it (never silently
// overwrite) and Unlock reports it as corrupt.
func (v *Vault) Exists() bool {
	ok, err := hostopen.ExistsNoFollow(v.dir())
	return err == nil && ok
}

// Index is index.toml: the recipient cold staged writes encrypt to, the
// project id, and a display-only cache of names/kinds (repaired from decrypt
// results at unlock; never load-bearing).
type Index struct {
	Recipient string            `toml:"recipient"`
	ProjectID string            `toml:"project_id"`
	Kinds     map[string]string `toml:"kinds,omitempty"` // name -> declared kind at save time ("" unknown)
}

// ReadIndex loads index.toml (bounded). A missing vault returns ErrNoVault;
// a missing or unparsable index inside an existing vault returns a zero
// Index with the error — callers degrade (the index is a display cache plus
// the cold-write recipient, not the vault's source of truth).
func (v *Vault) ReadIndex() (Index, error) {
	if !v.Exists() {
		return Index{}, ErrNoVault
	}
	b, err := hostopen.ReadFileBounded(v.indexPath(), false, indexReadCap)
	if err != nil {
		return Index{}, fmt.Errorf("credentials index: %w", err)
	}
	var idx Index
	if err := toml.Unmarshal(b, &idx); err != nil {
		return Index{}, fmt.Errorf("credentials index: %w", err)
	}
	return idx, nil
}

// writeIndex publishes index.toml whole-file (temp+rename via hostopen).
// Machine-authored: outside ADR 0044's user-config tomldoc scope.
func (v *Vault) writeIndex(idx Index) error {
	b, err := toml.Marshal(idx)
	if err != nil {
		return err
	}
	return hostopen.PublishFile(v.indexPath(), string(b), 0o600)
}

// Create makes a fresh vault: generate an X25519 identity, wrap it under
// the passphrase at the pinned work factor, and publish identity + index as
// ONE recoverable step — both staged in a temp directory under the store
// and renamed into place, refused if a vault already exists. An
// interruption leaves only a sweepable temp directory, never a wedged
// identity-without-index state. Caller holds the setup lock.
func (v *Vault) Create(passphrase string) error {
	if v.Exists() {
		return ErrVaultExists
	}
	return v.createAt(passphrase)
}

// Replace is the explicit discard-and-recreate (--replace): the old vault —
// identity AND entries — is removed first. After a suspected identity leak
// this is the remedy (rekey rotates the passphrase, not the identity).
// Caller holds the setup lock.
func (v *Vault) Replace(passphrase string) error {
	if v.Exists() {
		// The vault dir was store-created by byre; removing it whole is the
		// declared point of --replace.
		if err := hostopen.PlainRemoveAll(v.dir(), hostopen.ByreCreated); err != nil {
			return fmt.Errorf("discarding the old vault: %w", err)
		}
	}
	return v.createAt(passphrase)
}

func (v *Vault) createAt(passphrase string) error {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return fmt.Errorf("generating the vault identity: %w", err)
	}
	wrapped, err := wrapIdentity(id, passphrase)
	if err != nil {
		return err
	}
	idx := Index{Recipient: id.Recipient().String(), ProjectID: v.projectID}
	idxBytes, err := toml.Marshal(idx)
	if err != nil {
		return err
	}

	// Stage both files in a byre-minted temp dir under the store (same
	// filesystem, so the rename below is a real single step), then rename
	// into place. Sweep any prior interrupted staging first — those dirs
	// are creation debris, never live state.
	v.sweepStaging()
	tmpName, err := stagingName()
	if err != nil {
		return err
	}
	if err := hostopen.MkdirAllIn(v.storeDir, tmpName, 0o700); err != nil {
		return fmt.Errorf("staging the vault: %w", err)
	}
	tmp := filepath.Join(v.storeDir, tmpName)
	// Writes inside the staging dir byre just created, completed within this
	// same operation.
	if err := hostopen.PlainWriteFile(filepath.Join(tmp, identityName), wrapped, 0o600, hostopen.ByreCreated); err != nil {
		return fmt.Errorf("staging the vault identity: %w", err)
	}
	if err := hostopen.PlainWriteFile(filepath.Join(tmp, indexName), idxBytes, 0o600, hostopen.ByreCreated); err != nil {
		return fmt.Errorf("staging the vault index: %w", err)
	}
	if err := hostopen.PlainMkdir(filepath.Join(tmp, entriesDir), 0o700, hostopen.ByreCreated); err != nil {
		return fmt.Errorf("staging the vault: %w", err)
	}
	// The publish: one rename, anchored at the store dir so no interior
	// component resolves through a link. The exists-check ran under the
	// caller's lock; rename onto an existing (empty) dir is the store-writer
	// residual, not fought here.
	root, err := hostopen.OpenDirRootNoFollow(v.storeDir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Rename(tmpName, DirName); err != nil {
		return fmt.Errorf("publishing the vault: %w", err)
	}
	return nil
}

// sweepStaging removes leftover interrupted-creation staging dirs.
// Best-effort: staging debris never blocks a launch or a create.
func (v *Vault) sweepStaging() {
	ents, err := hostopen.ReadDirNoFollow(v.storeDir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), stagingPrefix) && e.IsDir() {
			_ = hostopen.PlainRemoveAll(filepath.Join(v.storeDir, e.Name()), hostopen.ByreCreated)
		}
	}
}

const stagingPrefix = ".credentials-staging-"

// stagingName mints an unguessable staging dir name. No randomness, no
// create — same stance as the netns helper name.
func stagingName() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("no randomness to stage the vault: %w", err)
	}
	return stagingPrefix + hex.EncodeToString(b), nil
}

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

// Unlock unwraps the identity under the passphrase — the expensive scrypt
// step, deliberately run BEFORE any lock is taken (pre-lock prompt; brief
// launch step 1). A wrong passphrase returns ErrBadPassphrase (the prompt
// re-asks, bounded); a missing vault ErrNoVault; anything else is a
// corrupt/oversize identity (unlock-failed, no re-ask).
func (v *Vault) Unlock(passphrase string) (*Unlocked, error) {
	if !v.Exists() {
		return nil, ErrNoVault
	}
	b, err := hostopen.ReadFileBounded(v.identityPath(), false, identityReadCap)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("the vault has no identity file — it is damaged or was never fully created; recreate it: byre credentials init --replace")
		}
		return nil, fmt.Errorf("credentials identity: %w", err)
	}
	sid, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, err
	}
	sid.SetMaxWorkFactor(scryptMaxWorkFactor)
	rd, err := age.Decrypt(bytes.NewReader(b), sid)
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return nil, ErrBadPassphrase
		}
		return nil, fmt.Errorf("credentials identity: %w", err)
	}
	idStr, err := io.ReadAll(io.LimitReader(rd, identityReadCap))
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return nil, ErrBadPassphrase
		}
		return nil, fmt.Errorf("credentials identity: %w", err)
	}
	id, err := age.ParseX25519Identity(strings.TrimSpace(string(idStr)))
	if err != nil {
		return nil, fmt.Errorf("credentials identity: %w", err)
	}
	return &Unlocked{v: v, id: id, wrapped: b}, nil
}

// ErrVaultChanged is Rekey's refusal when the on-disk identity is no longer
// the one this Unlocked unwrapped: a concurrent `init --replace` (or
// another rekey) replaced the vault between the pre-lock unlock and the
// under-lock write, and publishing the OLD identity would silently corrupt
// the new vault (its entries would stop decrypting under an identity that
// "unlocks" fine). Nothing is written; re-running picks up the new vault.
var ErrVaultChanged = errors.New("the vault changed while rekeying (replaced or rekeyed concurrently) — nothing was written; re-run against the current vault")

// Unlocked is an unwrapped vault identity. It lives in host process memory
// for the launch and is never persisted (transient memory residency is the
// disclosed swap/core residual). wrapped is the identity file exactly as
// unlocked — Rekey's is-it-still-this-vault check.
type Unlocked struct {
	v       *Vault
	id      *age.X25519Identity
	wrapped []byte
}

// Recipient returns the unlocked identity's public recipient — what the
// index should carry for cold writes (RepairIndex uses it).
func (u *Unlocked) Recipient() string { return u.id.Recipient().String() }

// Decrypt reads one entry ONCE (bounded) and returns its value bytes —
// exactly the bytes present at decrypt time (no snapshot, no hashes; plain
// correctness against a concurrent cooperating worktree). The caller holds
// the setup lock across the per-entry decrypts. The non-nil error carries
// the per-name notice text for the outcome.
func (u *Unlocked) Decrypt(name string) ([]byte, Outcome, error) {
	if !NameRe.MatchString(name) {
		return nil, OutcomeMissingValue, fmt.Errorf("credential %q: invalid name", name)
	}
	b, err := hostopen.ReadFileBounded(u.v.entryPath(name), false, entryReadCap)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, OutcomeMissingValue, fmt.Errorf("credential %s: no value in the vault", name)
		}
		// Oversize, FIFO, unreadable: undecryptable, with the cause.
		return nil, OutcomeEntryUndecryptable, fmt.Errorf("credential %s: %w", name, err)
	}
	rd, err := age.Decrypt(bytes.NewReader(b), u.id)
	if err != nil {
		return nil, OutcomeEntryUndecryptable, fmt.Errorf("credential %s: corrupt, oversize, or encrypted to a different recipient: %v", name, err)
	}
	payload, err := io.ReadAll(io.LimitReader(rd, entryReadCap))
	if err != nil {
		return nil, OutcomeEntryUndecryptable, fmt.Errorf("credential %s: corrupt, oversize, or encrypted to a different recipient: %v", name, err)
	}
	gotProject, gotName, value, err := parsePayload(payload)
	if err != nil {
		return nil, OutcomeUnsupportedFormat, fmt.Errorf("credential %s: %w", name, err)
	}
	if gotName != name || gotProject != u.v.projectID {
		// The accident guard: a re-labelled or wrong-project file decrypts
		// to a mismatched stamp and is skipped loudly rather than silently
		// delivering the wrong value.
		return nil, OutcomeEntryMismatch, fmt.Errorf("credential %s: the stored value is stamped %q for project %q — a cross-project copy or wrong-project restore? Not delivering it", name, gotName, gotProject)
	}
	return value, OutcomeDelivered, nil
}

// RepairIndex refreshes the index's display cache and recipient from what
// an unlock actually established (brief: "repaired from decrypt results at
// unlock"). kinds carries the declared kind per delivered name; unknown
// names keep their cached kind. Best-effort — the index is display cache,
// and a failure must not cost the launch.
func (u *Unlocked) RepairIndex(kinds map[string]string) {
	idx, err := u.v.ReadIndex()
	if err != nil && !errors.Is(err, ErrNoVault) {
		idx = Index{}
	}
	idx.Recipient = u.Recipient()
	idx.ProjectID = u.v.projectID
	if idx.Kinds == nil {
		idx.Kinds = map[string]string{}
	}
	for n, k := range kinds {
		idx.Kinds[n] = k
	}
	_ = u.v.writeIndex(idx)
}

// Rekey re-wraps the identity under a new passphrase — a single-file atomic
// replace. It rotates the PASSPHRASE, not the identity: entries stay
// encrypted to the same recipient (after a suspected identity leak the
// remedy is --replace, a new vault). Caller holds the setup lock; the
// unlock itself ran BEFORE the lock (the scrypt cost never stalls
// siblings), so this confirms under the lock that the on-disk identity is
// still the one that was unlocked — a vault replaced in the window gets
// ErrVaultChanged, never a stale identity written over it.
func (u *Unlocked) Rekey(newPassphrase string) error {
	cur, err := hostopen.ReadFileBounded(u.v.identityPath(), false, identityReadCap)
	if err != nil {
		return fmt.Errorf("credentials identity: %w", err)
	}
	if !bytes.Equal(cur, u.wrapped) {
		return ErrVaultChanged
	}
	wrapped, err := wrapIdentity(u.id, newPassphrase)
	if err != nil {
		return err
	}
	if err := hostopen.PublishFile(u.v.identityPath(), string(wrapped), 0o600); err != nil {
		return err
	}
	// The published file is now what this Unlocked stands for — a second
	// Rekey on the same handle must compare against it, not the original.
	u.wrapped = wrapped
	return nil
}

// Set is the cold staged write: encrypt value to the index's recipient and
// publish entries/<name>.age — no passphrase needed (the recipient model's
// whole point; the editor's ^s path). kind is the declared kind if the
// caller knows it ("" otherwise): env values are validated against the env
// constraints here, at save, where re-entry is cheap. Caller holds the
// setup lock.
func (v *Vault) Set(name string, value []byte, kind string) error {
	if !NameRe.MatchString(name) {
		return fmt.Errorf("credential name %q: must be lowercase [a-z0-9-], starting with a letter (max 63 chars)", name)
	}
	if err := ValidateValue(value, kind); err != nil {
		return fmt.Errorf("credential %s: %w", name, err)
	}
	idx, err := v.ReadIndex()
	if err != nil {
		return err
	}
	rcp, err := age.ParseX25519Recipient(idx.Recipient)
	if err != nil {
		return fmt.Errorf("credentials index: recipient unusable (%v) — unlock once to repair it, or recreate the vault", err)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, rcp)
	if err != nil {
		return err
	}
	if _, err := w.Write(payloadBytes(v.projectID, name, value)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if err := hostopen.MkdirAllIn(v.dir(), entriesDir, 0o700); err != nil {
		return err
	}
	if err := hostopen.PublishFile(v.entryPath(name), buf.String(), 0o600); err != nil {
		return err
	}
	// Cache upkeep, best-effort: reflect the new name/kind for display.
	if idx.Kinds == nil {
		idx.Kinds = map[string]string{}
	}
	if idx.Kinds[name] != kind {
		idx.Kinds[name] = kind
		_ = v.writeIndex(idx)
	}
	return nil
}

// Unset removes a stored value (and its cache row). Removing a name that
// holds no value is a no-op, not an error. Caller holds the setup lock.
func (v *Vault) Unset(name string) error {
	if !NameRe.MatchString(name) {
		return fmt.Errorf("credential name %q: must be lowercase [a-z0-9-], starting with a letter (max 63 chars)", name)
	}
	// Anchored removal: a rename of entries/ (or an interior link under
	// --self-edit) must not redirect the delete elsewhere on the host.
	root, err := hostopen.OpenDirRootNoFollow(filepath.Join(v.dir(), entriesDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // no entries dir: nothing stored, nothing to remove
		}
		return err
	}
	defer root.Close()
	if err := root.Remove(name + ".age"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if idx, err := v.ReadIndex(); err == nil && idx.Kinds[name] != "" {
		delete(idx.Kinds, name)
		_ = v.writeIndex(idx)
	}
	return nil
}

// EntryNames lists the names holding stored values (the value-state column:
// set/unset), from the entries directory itself — the index cache is
// display-only and never consulted for truth. A missing vault or entries
// dir is an empty list.
func (v *Vault) EntryNames() []string {
	ents, err := hostopen.ReadDirNoFollow(filepath.Join(v.dir(), entriesDir))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		n, ok := strings.CutSuffix(e.Name(), ".age")
		if ok && NameRe.MatchString(n) && e.Type().IsRegular() {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// ValidateValue holds a value to its declared kind's constraints: env-kind
// values are NUL-free and capped at MaxEnvValue (headroom under
// MAX_ARG_STRLEN); file-kind (and unknown-kind) values are arbitrary bytes
// capped at MaxFileValue.
func ValidateValue(value []byte, kind string) error {
	if kind == "env" {
		if bytes.IndexByte(value, 0) >= 0 {
			return errors.New("an env value cannot contain NUL bytes (use kind = \"file\" for binary content)")
		}
		if len(value) > MaxEnvValue {
			return fmt.Errorf("env value is %d bytes; the cap is %d (use kind = \"file\" for large content)", len(value), MaxEnvValue)
		}
		return nil
	}
	if len(value) > MaxFileValue {
		return fmt.Errorf("value is %d bytes; the cap is %d", len(value), MaxFileValue)
	}
	return nil
}

// StripTrailingNewline removes ONE trailing newline (and a preceding CR) —
// the entry-path courtesy the brief pins for env values typed or piped in
// (echo without -n, an editor's final newline).
func StripTrailingNewline(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte("\n"))
	return bytes.TrimSuffix(b, []byte("\r"))
}

// payloadBytes composes the entry payload: a version line, the project-id
// and name stamps (the accident guard), then the raw value bytes.
func payloadBytes(projectID, name string, value []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\n%s\n%s\n", payloadHeader, projectID, name)
	b.Write(value)
	return b.Bytes()
}

// parsePayload splits a decrypted payload. A header line this byre does not
// know is unsupported-format (a future version), never a misparse.
func parsePayload(p []byte) (projectID, name string, value []byte, err error) {
	rest, ok := bytes.CutPrefix(p, []byte(payloadHeader+"\n"))
	if !ok {
		return "", "", nil, errors.New("the stored value's format is not one this byre understands (written by a newer byre?)")
	}
	i := bytes.IndexByte(rest, '\n')
	if i < 0 {
		return "", "", nil, errors.New("the stored value is truncated")
	}
	projectID = string(rest[:i])
	rest = rest[i+1:]
	j := bytes.IndexByte(rest, '\n')
	if j < 0 {
		return "", "", nil, errors.New("the stored value is truncated")
	}
	name = string(rest[:j])
	return projectID, name, rest[j+1:], nil
}
