package commands

// The `byre credentials` verbs — set, unset, rekey, list — over the
// credential rows in a config file's env_from_host table and that file's own
// [credentials] identity. The editor's masked entry is the north-star path;
// these are the terminal-native equivalents.
//
// The one contract every value path here keeps: a value NEVER arrives as a
// command-line argument (argv lands in shell history and the process list) —
// it is read masked from the terminal or piped on stdin.
//
// Every write is compare-and-swap under a lock: the file is re-read after the
// lock is taken and must be byte-identical to what the operation based its
// edit on. The race that closes is not hypothetical — `set` reads recipient
// R, a concurrent identity replacement lands R2, and the R-encrypted blob
// sitting beside R2 is permanently undecryptable though both writes
// "succeeded".
//
// The COMPARE is the guarantee; the lock is what makes it exclusive rather
// than merely detected. The lock a write takes belongs to the FILE it writes,
// not to the caller: a project config is written under the project setup lock
// (shared by sibling worktree sessions, which share one store), and a layer
// file under a lock in the layer's own directory — a layer is reachable from
// every project that extends it, so a project lock would leave two projects
// writing one file unserialized, and the loser's edit would be refused after
// the fact instead of waiting its turn.

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/configui"
	"github.com/pjlsergeant/byre/internal/credentials"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/tomldoc"
)

// credentialsTable / envFromHostTable / envTable are the tables every verb
// here edits, as tomldoc addresses them. [env] is edited only to REMOVE the
// literal a credential converts from (see credRowRemovals) — no verb here
// writes an [env] value.
var (
	credentialsTable = []string{"credentials"}
	envFromHostTable = []string{config.EnvFromHostTable}
	envTable         = []string{"env"}
)

// credTarget is the physical file a verb writes: credential rows and the
// identity that opens them belong to ONE file, never to the merged cascade.
type credTarget struct {
	path     string
	follow   bool   // the target's trust class (see configui.Save)
	label    string // "project config", "layer acme"
	lockFile string
	// prepare is the store enrollment a write needs (Paths.Bootstrap for the
	// project target, nothing for a layer). writeCredTarget runs it BEFORE
	// taking the lock — see the note there.
	prepare func() error
	// disclosure is the cross-project warning a layer target carries, empty
	// for the project's own config.
	disclosure string
}

// credentialTarget resolves --layer (or its absence) to the file a verb
// writes. A layer target carries the write-target disclosure: layer changes
// propagate live (ADR 0035), so the cross-project effect must be
// unmistakable BEFORE a value is accepted.
func credentialTarget(projectDir, layer string) (credTarget, error) {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return credTarget{}, err
	}
	if layer == "" {
		return projectCredTarget(paths), nil
	}
	if err := config.ValidateLayerName(layer); err != nil {
		return credTarget{}, err
	}
	if ok, err := hostopen.ExistsNoFollow(config.LayerPath(paths.Home, layer)); err != nil || !ok {
		return credTarget{}, fmt.Errorf("layer %s does not exist (create it: byre layer new %s)", layer, layer)
	}
	return layerCredTarget(paths.Home, layer), nil
}

// projectCredTarget and layerCredTarget are the two write targets themselves,
// separate from the --layer resolution above because the editor reaches them
// from what IT knows: a layer editor has a home and a layer name and no
// resolvable project at all, and re-deriving the target from a project would
// refuse to open the credential path for exactly the file it is editing.
func projectCredTarget(paths project.Paths) credTarget {
	return credTarget{
		path:     filepath.Join(paths.Dir, config.ProjectConfigName),
		follow:   false, // the store project dir is what --self-edit mounts
		label:    "project config",
		lockFile: paths.LockFile,
		prepare:  paths.Bootstrap,
	}
}

func layerCredTarget(home, layer string) credTarget {
	path := config.LayerPath(home, layer)
	return credTarget{
		// follow=true: a named layer is host-owned (never inside a box
		// mount), so a dotfiles symlink there is the user's own arrangement.
		// The lock is the LAYER's, not this project's: every project
		// extending the layer writes this same file, and their project locks
		// differ, so only a lock beside the file itself makes the
		// compare-and-swap exclusive across them.
		path:       path,
		follow:     true,
		label:      "layer " + layer,
		lockFile:   config.LayerLockPath(home, layer),
		disclosure: layerWriteDisclosure(home, layer, path),
	}
}

// credentialAdmin is the editor's configui.CredentialAdmin: the credential
// write path, aimed at the file `byre config` has open. Same target, same
// disclosure, same compare-and-swap under the same lock as the CLI verb — the
// editor supplies the masked value and the passphrase, and owns nothing else.
type credentialAdmin struct {
	s Streams
	t credTarget
}

var _ configui.CredentialAdmin = (*credentialAdmin)(nil)

func (a *credentialAdmin) Disclosure() string { return a.t.disclosure }

func (a *credentialAdmin) HasIdentity() (bool, error) {
	f, err := readCredTarget(a.t)
	if err != nil {
		return false, err
	}
	return f.hasBlock, nil
}

// Set applies one editor accept. The file is re-read HERE, per set: those
// bytes are what the compare-and-swap holds the write to, so a snapshot taken
// when the editor opened would base a write on a file that has since moved.
//
// The removals the accept carries ride the SAME mutation as the row, and the
// bytes that mutation wrote come back with it — the editor's save baseline is
// then its own write and not a re-read taken after the lock let go.
func (a *credentialAdmin) Set(w configui.CredentialWrite) (configui.CredentialResult, error) {
	if err := config.ValidateEnvFromHostKey(w.Key); err != nil {
		return configui.CredentialResult{}, err
	}
	if err := config.ValidateCredentialKey(w.Key); err != nil {
		return configui.CredentialResult{}, err
	}
	f, err := readCredTarget(a.t)
	if err != nil {
		return configui.CredentialResult{}, err
	}
	block, newIdentity := f.block, []byte(nil)
	if !f.hasBlock {
		if w.Passphrase == "" {
			// The editor asked HasIdentity at accept and was told yes, so it
			// collected no passphrase; the block is gone now. Name that, rather
			// than the empty-passphrase refusal — nobody chose an empty
			// passphrase here, and being told one is worthless explains nothing
			// about what happened.
			return configui.CredentialResult{}, fmt.Errorf("%s (%s) had a credentials identity when this value was accepted and has none now — nothing was written; close the form, re-open the row, and enter the value again", a.t.label, a.t.path)
		}
		wrapped, recipient, err := credentials.NewIdentity(w.Passphrase)
		if err != nil {
			return configui.CredentialResult{}, err
		}
		newIdentity = wrapped
		block = config.CredentialsBlock{Identity: wrapped, Recipient: recipient}
	}
	row, after, err := writeCredentialRow(a.s, a.t, f, w.Key, w.Kind, w.Value, block, newIdentity,
		credRowRemovals{env: w.RemoveEnv, envFromHost: w.RemoveEnvFromHost})
	if err != nil {
		return configui.CredentialResult{}, err
	}
	return configui.CredentialResult{Row: row, File: after}, nil
}

// layerWriteDisclosure states what writing to a layer means: every project
// extending it takes the new value on its next launch. The count DEGRADES —
// an unreadable store costs the number, never the warning.
func layerWriteDisclosure(home, layer, path string) string {
	n, ok := layerProjectUsers(home, layer)
	if !ok {
		return fmt.Sprintf("writes to layer %s (%s) — every project extending it takes this value; byre could not count them", layer, path)
	}
	return fmt.Sprintf("writes to layer %s (%s), used by %d %s — this changes the value for every project extending it",
		layer, path, n, credPlural("project", n))
}

// orphanCredentialRows counts one FILE's credential rows. Asked only where
// that file has no [credentials] block, which makes every one of them an
// orphan: nothing on this machine can open it.
func orphanCredentialRows(envFromHost map[string]string) int {
	n := 0
	for _, src := range envFromHost {
		if config.IsCredentialSource(src) {
			n++
		}
	}
	return n
}

// credPlural is the count-agreeing noun these messages need.
func credPlural(noun string, n int) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

// layerProjectUsers counts the stored projects whose extends chain reaches
// layer. ok=false when the store cannot be walked — the caller says so
// rather than printing a number it did not establish.
func layerProjectUsers(home, layer string) (int, bool) {
	entries, err := hostopen.PlainReadDir(filepath.Join(home, "projects"), hostopen.StoreOwned)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, true // no projects stored yet
		}
		return 0, false
	}
	cat, _ := builtins.LoadCatalogRaw(home)
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cfg, err := config.ParseFile(filepath.Join(home, "projects", e.Name(), config.ProjectConfigName), false)
		if err != nil {
			continue // a project byre cannot read extends nothing it can name
		}
		chain, err := config.LoadExtendsChain(home, cat, cfg.Extends)
		if err != nil {
			continue
		}
		for _, nl := range chain {
			if nl.Name == layer {
				n++
				break
			}
		}
	}
	return n, true
}

// credFile is one target's content as an operation read it: the bytes the
// compare-and-swap holds the write to, and the file's own identity block.
type credFile struct {
	raw      []byte
	readErr  error
	cfg      config.Config
	block    config.CredentialsBlock
	hasBlock bool
}

// readCredTarget reads the target file, its layer, and its [credentials]
// block. A file that does not exist yet is an empty one — the first `set`
// creates it.
func readCredTarget(t credTarget) (credFile, error) {
	raw, err := hostopen.ReadFileBounded(t.path, t.follow, config.MaxConfigBytes)
	if err != nil && !os.IsNotExist(err) {
		return credFile{}, err
	}
	f := credFile{raw: raw, readErr: err}
	if err != nil {
		return f, nil
	}
	if f.cfg, err = config.Parse(raw); err != nil {
		return credFile{}, fmt.Errorf("%s (%s): %w", t.label, t.path, err)
	}
	f.block, f.hasBlock, err = config.ParseCredentialsBlock(raw)
	if err != nil {
		return credFile{}, fmt.Errorf("%s (%s): %w", t.label, t.path, err)
	}
	return f, nil
}

// ErrCredentialFileChanged is the compare-and-swap refusal: the file moved
// between the read this operation planned against and the write.
var ErrCredentialFileChanged = errors.New("the config file changed while this command was working — nothing was written; run the command again against the current file")

// writeCredTarget applies mutate to the target under the setup lock,
// compare-and-swapping against the bytes the caller read. The mutation is
// re-parsed before it lands: a write that would leave the file unopenable —
// or its identity unreadable — is refused with the file untouched.
//
// It returns the bytes it WROTE, captured inside the lock. A caller that keeps
// a save baseline (the editor) must take it from here: a read after the lock
// releases can pick up a concurrent writer's bytes, and a baseline holding a
// change this session never made is a drift check that will not fire.
func writeCredTarget(s Streams, t credTarget, base credFile, mutate func(*tomldoc.Doc) error) ([]byte, error) {
	// Enrollment precedes the LOCK, not just the write: the project lock file
	// lives in the store directory and is only O_CREATEd there, so on a project
	// that has never been developed the ACQUISITION fails ENOENT before any
	// under-lock Bootstrap could run — and `byre credentials set KEY` is the
	// onramp `list` advertises. The editor already orders it this way
	// (configui's runPrepare, then the guarded write).
	//
	// Bootstrap remains the only creator of the store directory: it makes the
	// dir and its path record together, which is what keeps a write from
	// resurrecting a store `byre forget` deleted (the concern AtomicWrite
	// states). This moves WHEN it runs, not who does it.
	//
	// The residue that buys, accepted: enrollment now happens even when the
	// write is refused, so a set the compare-and-swap (or the re-parse) turns
	// down can leave a freshly created, empty store behind; and a `byre forget`
	// landing between this call and the lock leaves a config file whose project
	// has no path record until the next Bootstrap re-makes it. Both are
	// self-healing, and neither can lose a value.
	if t.prepare != nil {
		if err := t.prepare(); err != nil {
			return nil, err
		}
	}
	var written []byte
	err := withSetupLock(s.Err, t.lockFile, func() error {
		now, err := hostopen.ReadFileBounded(t.path, t.follow, config.MaxConfigBytes)
		if !sameCredFileState(now, err, base.raw, base.readErr) {
			return ErrCredentialFileChanged
		}
		src := now
		if err != nil {
			src = []byte(credentialsFileHeader)
		}
		doc, derr := tomldoc.Load(src)
		if derr != nil {
			return fmt.Errorf("%s (%s): %w", t.label, t.path, derr)
		}
		if err := mutate(doc); err != nil {
			return err
		}
		out := doc.Bytes()
		cfg, perr := config.Parse(out)
		if perr != nil {
			return fmt.Errorf("%s (%s): the edit would leave a file byre cannot read (%w) — nothing was written", t.label, t.path, perr)
		}
		if verr := cfg.ValidateLayer(); verr != nil {
			return fmt.Errorf("%s (%s): %w — nothing was written", t.label, t.path, verr)
		}
		if _, _, berr := config.ParseCredentialsBlock(out); berr != nil {
			return fmt.Errorf("%s (%s): %w — nothing was written", t.label, t.path, berr)
		}
		if werr := config.AtomicWrite(t.path, string(out)); werr != nil {
			return werr
		}
		written = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	return written, nil
}

// credentialsFileHeader fronts a config file `byre credentials` creates —
// the same one-line ownership note `byre config` writes.
const credentialsFileHeader = "# Managed by `byre config`.\n\n"

// sameCredFileState compares two (bytes, error) reads of one path. Absent on
// both sides counts as unchanged; any read failure other than absence is
// drift, because byre cannot establish the file is what it was.
func sameCredFileState(a []byte, aErr error, b []byte, bErr error) bool {
	switch {
	case aErr == nil && bErr == nil:
		return bytes.Equal(a, b)
	case os.IsNotExist(aErr) && os.IsNotExist(bErr):
		return true
	default:
		return false
	}
}

// CredentialsSet implements `byre credentials set KEY [--file] [--layer]`:
// encrypt a value to the target file's recipient and write the row. The
// value is masked from the terminal or piped on stdin, never argv.
//
// The first set on a file with no [credentials] block mints that file's
// identity, prompting for the passphrase that wraps it. Every later set is a
// COLD write: values encrypt to the cleartext recipient, so setting one
// never asks for a passphrase.
func CredentialsSet(s Streams, projectDir, key string, fileKind bool, layer string) error {
	if err := config.ValidateEnvFromHostKey(key); err != nil {
		return err
	}
	// Refused before the value prompt, not after the row is written: the key
	// the delivery stream reserves for its manifest cannot be a credential.
	if err := config.ValidateCredentialKey(key); err != nil {
		return err
	}
	kind := credentials.KindEnv
	if fileKind {
		kind = credentials.KindFile
	}
	t, err := credentialTarget(projectDir, layer)
	if err != nil {
		return err
	}
	f, err := readCredTarget(t)
	if err != nil {
		return err
	}
	// The disclosure lands BEFORE the value is accepted: a user typing a
	// production key into a shared layer must know that is what they are
	// doing while they can still stop.
	if t.disclosure != "" {
		fmt.Fprintf(s.Err, "byre: %s\n", t.disclosure)
	}

	var newIdentity []byte
	block := f.block
	if !f.hasBlock {
		// Rows with no identity to open them: minting is not refused over them
		// (they are already undecryptable and a launch already stops on them),
		// but the passphrase about to be chosen does not open them, and a
		// prompt that said only "holds no credentials yet" would be false.
		if n := orphanCredentialRows(f.cfg.EnvFromHost); n > 0 {
			fmt.Fprintf(s.Err, "byre: %s\n", credentials.OrphanRowsWarning(n))
		}
		wrapped, recipient, err := mintCredentialIdentity(s, t)
		if err != nil {
			return err
		}
		newIdentity = wrapped
		block = config.CredentialsBlock{Identity: wrapped, Recipient: recipient}
	}

	value, err := readCredentialValue(s, key, kind)
	if err != nil {
		return err
	}
	if len(value) == 0 {
		return errors.New("refusing to store an empty value (byre credentials unset removes a row)")
	}
	if _, _, err := writeCredentialRow(s, t, f, key, kind, value, block, newIdentity, credRowRemovals{}); err != nil {
		return err
	}
	fmt.Fprintf(s.Err, "byre: credential %s set in the %s (%s): %d bytes, encrypted to that file's recipient.\n",
		key, t.label, t.path, len(value))
	if newIdentity != nil {
		fmt.Fprintf(s.Err, "byre: minted this file's credentials identity — that passphrase opens every credential in %s, and byre asks for it at develop.\n", t.path)
	}
	fmt.Fprintln(s.Err, "byre: applies on the next develop.")
	return nil
}

// credRowRemovals are the OTHER rows one write removes as it lands its
// encrypted row: the [env] literal a credential is converting from, and the
// env_from_host row it replaces under a different key. Empty for the CLI verb,
// which sets a key and touches nothing else.
//
// They belong to the row's own mutation because the caller that has them (the
// editor) has already applied them to what the user is looking at. A write that
// landed the row and left the removals for the next ^s put a quit in between,
// and a converted [env] literal left behind still wins the cascade (ADR 0026):
// the box takes the old plaintext while the editor says the credential is set.
type credRowRemovals struct {
	env         string
	envFromHost string
}

// writeCredentialRow is the value half of `set`, and the ONE owner of it: hold
// the value to its kind's rules, encrypt it to the FILE's recipient, and
// compare-and-swap the row in — together with the identity, when this write is
// the one that mints it (newIdentity nil = the file already had a block), and
// together with the rows this write replaces. Returns the row source that
// landed (the editor puts it into its working state so a later whole-file save
// writes the value back unchanged) and the file bytes the write produced.
//
// Every surface that sets a credential goes through here. A second spelling of
// encrypt-and-CAS would be a second place for the identity and its rows to end
// up in different generations — the exact split the compare-and-swap exists to
// prevent — so the editor calls this rather than reimplementing it.
func writeCredentialRow(s Streams, t credTarget, f credFile, key string, kind credentials.Kind, value []byte, block config.CredentialsBlock, newIdentity []byte, rm credRowRemovals) (string, []byte, error) {
	if err := credentials.ValidateValue(value, kind); err != nil {
		return "", nil, fmt.Errorf("credential %s: %w", key, err)
	}
	blob, err := credentials.EncryptValue(block.Recipient, key, kind, value)
	if err != nil {
		return "", nil, err
	}
	row, err := config.FormatEncryptedRow(kind, blob)
	if err != nil {
		return "", nil, err
	}
	after, err := writeCredTarget(s, t, f, func(doc *tomldoc.Doc) error {
		if newIdentity != nil {
			if err := setCredentialsBlock(doc, newIdentity, block.Recipient); err != nil {
				return err
			}
		}
		// Removals first, so a row and the row it replaces cannot both be in
		// the document at any point a later edit reads it. RemoveKey is a
		// no-op on a key the file does not carry.
		if rm.env != "" {
			if err := doc.RemoveKey(envTable, rm.env); err != nil {
				return err
			}
		}
		if rm.envFromHost != "" {
			if err := doc.RemoveKey(envFromHostTable, rm.envFromHost); err != nil {
				return err
			}
		}
		return doc.SetKey(envFromHostTable, key, strconv.Quote(row))
	})
	if err != nil {
		return "", nil, err
	}
	return row, after, nil
}

// mintCredentialIdentity creates the file's identity: a fresh X25519 key
// wrapped under a passphrase confirmed twice. TTY-only — a passphrase never
// rides argv or a pipe.
func mintCredentialIdentity(s Streams, t credTarget) ([]byte, string, error) {
	if !s.TTY {
		return nil, "", fmt.Errorf("%s (%s) has no [credentials] block yet, and creating one needs a terminal for the masked passphrase prompt", t.label, t.path)
	}
	// "no credentials IDENTITY", not "no credentials": a file can carry rows
	// and no block at all (the orphan warning above), and this line printed
	// right under that one would contradict it.
	fmt.Fprintf(s.Err, "byre: %s (%s) has no credentials identity yet — choose the passphrase that will open the credentials it stores.\n", t.label, t.path)
	pw, err := readNewPassphrase(s, "new passphrase for "+t.label+": ", "confirm passphrase: ")
	if err != nil {
		return nil, "", err
	}
	return credentials.NewIdentity(pw)
}

// readNewPassphrase reads a new passphrase twice and holds it to the two
// rules a new one has: not empty, and matching its confirmation.
func readNewPassphrase(s Streams, prompt, confirmPrompt string) (string, error) {
	pw, err := readPassphrase(s.Err, prompt)
	if err != nil {
		return "", err
	}
	if pw == "" {
		return "", errors.New(credentials.EmptyPassphraseWorthless + " — aborted (nothing written)")
	}
	confirm, err := readPassphrase(s.Err, confirmPrompt)
	if err != nil {
		return "", err
	}
	if pw != confirm {
		return "", errors.New("passphrases do not match — aborted (nothing written)")
	}
	return pw, nil
}

// readCredentialValue takes the value from the terminal (masked) or from
// stdin whole when piped, bounded a byte over the per-value cap so an
// oversize pipe is refused rather than silently truncated.
func readCredentialValue(s Streams, key string, kind credentials.Kind) ([]byte, error) {
	if s.TTY {
		v, err := readPassphrase(s.Err, fmt.Sprintf("value for %s (input hidden): ", key))
		return []byte(v), err
	}
	b, err := io.ReadAll(io.LimitReader(s.In, credentials.MaxValue+1))
	if err != nil {
		return nil, err
	}
	// The typed-or-piped courtesy, env-kind only: one trailing newline
	// stripped (echo without -n, an editor's final newline), where a stray
	// newline corrupts the exported token byte-exactly. A file value is
	// arbitrary bytes and stays untouched — a PEM's final newline is part of
	// the file.
	if kind == credentials.KindEnv {
		b = credentials.StripTrailingNewline(b)
	}
	return b, nil
}

// setCredentialsBlock writes the file's identity and recipient, creating the
// [credentials] table when it is not there.
func setCredentialsBlock(doc *tomldoc.Doc, wrapped []byte, recipient string) error {
	if err := doc.SetKey(credentialsTable, "identity", strconv.Quote(base64.StdEncoding.EncodeToString(wrapped))); err != nil {
		return err
	}
	return doc.SetKey(credentialsTable, "recipient", strconv.Quote(recipient))
}

// CredentialsUnset implements `byre credentials unset KEY [--layer]`:
// remove the row. The ciphertext goes with it — there is no separate store
// keeping a value the config no longer names.
func CredentialsUnset(s Streams, projectDir, key, layer string) error {
	if err := config.ValidateEnvFromHostKey(key); err != nil {
		return err
	}
	t, err := credentialTarget(projectDir, layer)
	if err != nil {
		return err
	}
	f, err := readCredTarget(t)
	if err != nil {
		return err
	}
	src, present := f.cfg.EnvFromHost[key]
	if !present {
		return fmt.Errorf("%s (%s) has no %s row to remove", t.label, t.path, key)
	}
	// A row that NAMES a scheme and carries an unusable payload — or sits on
	// the reserved `manifest` key — is still this key's credential, and
	// removing it is exactly the repair. IsCredentialSource is the one
	// predicate every surface asks ("is this row a credential row"), so only
	// a source naming no scheme at all is refused here.
	if !config.IsCredentialSource(src) {
		return fmt.Errorf("%s (%s): %s is a %q source, not a credential — edit it in `byre config`", t.label, t.path, key, src)
	}
	if _, err := writeCredTarget(s, t, f, func(doc *tomldoc.Doc) error {
		return doc.RemoveKey(envFromHostTable, key)
	}); err != nil {
		return err
	}
	fmt.Fprintf(s.Err, "byre: credential %s removed from the %s (%s) — its ciphertext went with the row; there is no copy anywhere else.\n", key, t.label, t.path)
	fmt.Fprintln(s.Err, "byre: applies on the next develop.")
	return nil
}

// CredentialsRekey implements `byre credentials rekey [--layer]`: re-wrap
// ONE file's identity under a new passphrase. The identity itself does not
// rotate, so every value row stays byte-identical — which is what keeps drift
// comparing credentials as plain bytes.
func CredentialsRekey(s Streams, projectDir, layer string) error {
	if !s.TTY {
		return errors.New("credentials rekey needs a terminal for the masked passphrase prompts")
	}
	t, err := credentialTarget(projectDir, layer)
	if err != nil {
		return err
	}
	f, err := readCredTarget(t)
	if err != nil {
		return err
	}
	if !f.hasBlock {
		return fmt.Errorf("%s (%s) holds no credentials identity — nothing to rekey", t.label, t.path)
	}
	if t.disclosure != "" {
		fmt.Fprintf(s.Err, "byre: %s\n", t.disclosure)
	}
	old, err := readPassphrase(s.Err, "current passphrase for "+t.label+": ")
	if err != nil {
		return err
	}
	id, err := credentials.UnwrapIdentity(f.block.Identity, old)
	if err != nil {
		return err
	}
	newPw, err := readNewPassphrase(s, "new passphrase: ", "confirm new passphrase: ")
	if err != nil {
		return err
	}
	wrapped, err := id.Rewrap(newPw)
	if err != nil {
		return err
	}
	if _, err := writeCredTarget(s, t, f, func(doc *tomldoc.Doc) error {
		return setCredentialsBlock(doc, wrapped, id.Recipient())
	}); err != nil {
		return err
	}
	fmt.Fprintf(s.Err, "byre: passphrase rotated for the %s (%s). The identity is unchanged, so every value row is untouched — after a suspected leak of the file itself, re-set the values under a new identity.\n", t.label, t.path)
	return nil
}

// CredentialsList implements `byre credentials list`: the cascade's
// credential rows with kind, source file, and whether the winning row
// carries a value. Nothing is decrypted, so this never prompts.
func CredentialsList(s Streams, projectDir string) error {
	files, err := config.CascadeFiles(projectDir)
	if err != nil {
		return err
	}
	groups, gerr := config.EncryptedRows(files)
	if gerr != nil {
		// A broken row is worth saying here, where the user is looking at the
		// set: it stops the next develop, and this is where they see why.
		fmt.Fprintf(s.Err, "byre: %v\n", gerr)
	}
	rows := 0
	for _, g := range groups {
		for _, r := range g.Rows {
			fmt.Fprintf(s.Out, "%s\t%s\t%s\t%s\n", r.Key, r.Kind, credentialDisplay(g.Label), credentials.ValueState(true))
			rows++
		}
	}
	// A key some file sets encrypted that the cascade does not deliver: a
	// nearer layer emptied it, replaced it with another source, or an [env]
	// literal took it. Declared and not reaching the box is exactly what a
	// list must say out loud — and the value-state cell says SET, because it
	// is: the ciphertext sits in that file, and "unset" would send a user to
	// re-enter a value they already have instead of to the row that is
	// beating it.
	for _, d := range disabledCredentialRows(files, groups) {
		fmt.Fprintf(s.Out, "%s\t%s\t%s\t%s — not delivered: %s\n",
			d.key, d.kind, d.source, credentials.ValueState(true), d.reason)
		rows++
	}
	if rows == 0 {
		fmt.Fprintln(s.Out, "no credentials in this project's cascade.")
		fmt.Fprintln(s.Out, "start: byre credentials set STRIPE_KEY")
	}
	return nil
}

// disabledCredentialRow is a credential a file declares that the cascade
// does not deliver: where its value sits, and what takes it away.
type disabledCredentialRow struct{ key, kind, source, reason string }

func disabledCredentialRows(files []config.CascadeFile, live []config.CredentialFile) []disabledCredentialRow {
	delivered := map[string]bool{}
	for _, g := range live {
		for _, r := range g.Rows {
			delivered[r.Key] = true
		}
	}
	seen := map[string]bool{}
	var out []disabledCredentialRow
	for _, f := range files {
		for _, k := range sortedEnvKeys(f.Cfg.EnvFromHost) {
			if delivered[k] || seen[k] {
				continue
			}
			row, ok, err := config.ParseEncryptedRow(k, f.Cfg.EnvFromHost[k])
			if err != nil || !ok {
				continue
			}
			seen[k] = true
			out = append(out, disabledCredentialRow{
				key:    k,
				kind:   string(row.Kind),
				source: credentialDisplay(f.Label),
				reason: credentialNotDeliveredReason(files, k),
			})
		}
	}
	return out
}

// credentialNotDeliveredReason names what beats a declared credential row,
// searched the way the merge resolves: an explicit [env] literal anywhere
// takes the key out of env_from_host entirely (ADR 0026), and otherwise the
// nearest env_from_host row wins — "" being the idiomatic disable.
func credentialNotDeliveredReason(files []config.CascadeFile, key string) string {
	for i := len(files) - 1; i >= 0; i-- {
		if _, ok := files[i].Cfg.Env[key]; ok {
			return "overridden by an [env] literal in " + credentialDisplay(files[i].Label)
		}
	}
	for i := len(files) - 1; i >= 0; i-- {
		src, ok := files[i].Cfg.EnvFromHost[key]
		if !ok {
			continue
		}
		if src == "" {
			return `disabled by "" in ` + credentialDisplay(files[i].Label)
		}
		// RenderSource, so a winning row that is itself an unreadable
		// credential names its scheme instead of pasting a ciphertext wall
		// into the listing.
		return "replaced by " + config.RenderSource(src) + " in " + credentialDisplay(files[i].Label)
	}
	return "the cascade does not deliver it"
}

func sortedEnvKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
