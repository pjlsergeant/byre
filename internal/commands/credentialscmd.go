package commands

// The `byre credentials` verbs — CLI shortcuts over the vault
// (internal/credentials) and the [[credentials]] declarations. The editor's
// masked staged entry is the north-star path; these are the terminal-native
// equivalents. The one contract every value path here keeps: a value NEVER
// arrives as a command-line argument (argv lands in shell history and the
// process list) — it is read masked from the terminal or piped on stdin.

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/credentials"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// credentialVerbs plugs the [[credentials]] vocabulary into the shared
// layer-edit lifecycle (nameddecl.go) for declare/undeclare.
var credentialVerbs = declVerbs[config.CredentialDecl]{
	kind:   "credential",
	name:   func(cd config.CredentialDecl) string { return cd.Name },
	marker: func(name string) config.CredentialDecl { return config.CredentialDecl{Name: name} },
	list:   func(c *config.Config) *[]config.CredentialDecl { return &c.Credentials },
	effectiveHas: func(effective config.Config, res skills.Resolved, name string) (bool, error) {
		for _, cd := range effective.Credentials {
			if cd.Name == name {
				return true, nil
			}
		}
		return false, nil
	},
}

// CredentialsDeclare implements `byre credentials declare <name> --kind ... --target ...`:
// add-or-update the declaration in the target layer (the consent surface;
// no value involved). The next develop's prompt picks it up.
func CredentialsDeclare(s Streams, projectDir string, global bool, name, kind, target string) error {
	decl := config.CredentialDecl{Name: name, Kind: kind, Target: target}
	if err := config.ValidateCredentialDecl(decl); err != nil {
		return err
	}
	return addNamedDecl(s, projectDir, global, credentialVerbs, name, decl)
}

// CredentialsUndeclare implements `byre credentials undeclare <name>`
// (closure-smart, like every named-declaration remove). The stored VALUE is
// untouched — `byre credentials unset` discards it.
func CredentialsUndeclare(s Streams, projectDir string, global bool, name string) error {
	if err := credentials.ValidateName(name); err != nil {
		return err
	}
	return removeNamedDecl(s, projectDir, global, credentialVerbs, name)
}

// credProjectVault resolves and bootstraps the project store and returns
// its vault handle plus paths (the lock file lives in the store).
func credProjectVault(projectDir string) (*credentials.Vault, project.Paths, error) {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return nil, project.Paths{}, err
	}
	if err := paths.Bootstrap(); err != nil {
		return nil, project.Paths{}, err
	}
	return credentials.Open(paths.Dir, paths.ID), paths, nil
}

// CredentialsInit implements `byre credentials init [--replace]`: create the
// project vault under a fresh passphrase (masked, confirmed; TTY-only — a
// passphrase never rides argv or a pipe). --replace is the explicit
// discard-and-recreate, which is also the remedy after a suspected identity
// leak (rekey rotates the passphrase, not the identity).
func CredentialsInit(s Streams, projectDir string, replace bool) error {
	if !s.TTY {
		return errors.New("credentials init needs a terminal for the masked passphrase prompt")
	}
	v, paths, err := credProjectVault(projectDir)
	if err != nil {
		return err
	}
	if v.Exists() && !replace {
		return credentials.ErrVaultExists
	}
	pw, err := readPassphrase(s.Err, "new vault passphrase: ")
	if err != nil {
		return err
	}
	if pw == "" {
		return errors.New(credentials.EmptyPassphraseWorthless + " — aborted (nothing created)")
	}
	confirm, err := readPassphrase(s.Err, "confirm passphrase: ")
	if err != nil {
		return err
	}
	if pw != confirm {
		return errors.New("passphrases do not match — aborted (nothing created)")
	}
	if err := withSetupLock(s.Err, paths.LockFile, func() error {
		if replace {
			return v.Replace(pw)
		}
		return v.Create(pw)
	}); err != nil {
		return err
	}
	verb := "created"
	if replace {
		verb = "replaced"
	}
	fmt.Fprintf(s.Err, "byre: credentials vault %s. Values are age-encrypted at rest; the passphrase unlocks them per launch.\n", verb)
	return nil
}

// CredentialsSet implements `byre credentials set <name>`: stage a value
// into the vault — masked prompt on a TTY, or the whole of stdin when
// piped (`op read ... | byre credentials set stripe`). Never argv. A cold
// write: no passphrase needed (the value encrypts to the vault recipient).
func CredentialsSet(s Streams, projectDir string, name string) error {
	if err := credentials.ValidateName(name); err != nil {
		return err
	}
	v, paths, err := credProjectVault(projectDir)
	if err != nil {
		return err
	}
	var value []byte
	if s.TTY {
		pw, err := readPassphrase(s.Err, fmt.Sprintf("value for %s (input hidden): ", name))
		if err != nil {
			return err
		}
		value = []byte(pw)
	} else {
		// Piped: the value is stdin whole, bounded a byte over the cap so an
		// oversize pipe is refused rather than silently truncated.
		b, err := io.ReadAll(io.LimitReader(s.In, credentials.MaxFileValue+1))
		if err != nil {
			return err
		}
		value = b
	}
	// The declared kind, when the name is declared, so env constraints catch
	// at save (where re-entry is cheap) rather than at launch.
	kind := ""
	if cfg, err := config.Load(projectDir); err == nil {
		for _, d := range cfg.Credentials {
			if d.Name == name {
				kind = d.Kind
			}
		}
	}
	// The entry-path courtesy: one trailing newline stripped (echo without
	// -n, an editor's final newline) — for env-kind and undeclared names,
	// where a stray newline corrupts the exported token byte-exactly. A
	// declared FILE value is arbitrary bytes and stays untouched: a PEM's
	// final newline is part of the file.
	if kind != "file" {
		value = credentials.StripTrailingNewline(value)
	}
	if len(value) == 0 {
		return errors.New("refusing to store an empty value (unset removes a value)")
	}
	if err := withSetupLock(s.Err, paths.LockFile, func() error {
		return v.Set(name, value, kind)
	}); err != nil {
		return err
	}
	fmt.Fprintf(s.Err, "byre: credential %s set (%d bytes, encrypted at rest).\n", name, len(value))
	if kind == "" {
		fmt.Fprintf(s.Err, "byre: %s is not declared yet — declare it to deliver it: byre credentials declare %s --kind env --target %s\n", name, name, strings.ToUpper(strings.ReplaceAll(name, "-", "_")))
	}
	return nil
}

// CredentialsUnset implements `byre credentials unset <name>`: discard the
// stored value (the declaration, if any, stays — undeclare removes that).
func CredentialsUnset(s Streams, projectDir string, name string) error {
	if err := credentials.ValidateName(name); err != nil {
		return err
	}
	v, paths, err := credProjectVault(projectDir)
	if err != nil {
		return err
	}
	if !v.Exists() {
		return credentials.ErrNoVault
	}
	if err := withSetupLock(s.Err, paths.LockFile, func() error {
		return v.Unset(name)
	}); err != nil {
		return err
	}
	fmt.Fprintf(s.Err, "byre: credential %s unset.\n", name)
	return nil
}

// CredentialsRekey implements `byre credentials rekey`: re-wrap the
// identity under a new passphrase (single-file replace). It rotates the
// PASSPHRASE, not the identity — after a suspected identity leak the
// remedy is `init --replace` (a new vault), and this says so.
func CredentialsRekey(s Streams, projectDir string) error {
	if !s.TTY {
		return errors.New("credentials rekey needs a terminal for the masked passphrase prompts")
	}
	v, paths, err := credProjectVault(projectDir)
	if err != nil {
		return err
	}
	pw, err := readPassphrase(s.Err, "current passphrase: ")
	if err != nil {
		return err
	}
	u, err := v.Unlock(pw)
	if err != nil {
		return err
	}
	newPw, err := readPassphrase(s.Err, "new passphrase: ")
	if err != nil {
		return err
	}
	if newPw == "" {
		return errors.New(credentials.EmptyPassphraseWorthless + " — aborted (nothing changed)")
	}
	confirm, err := readPassphrase(s.Err, "confirm new passphrase: ")
	if err != nil {
		return err
	}
	if newPw != confirm {
		return errors.New("passphrases do not match — aborted (nothing changed)")
	}
	if err := withSetupLock(s.Err, paths.LockFile, func() error {
		return u.Rekey(newPw)
	}); err != nil {
		return err
	}
	fmt.Fprintln(s.Err, "byre: passphrase rotated. (The vault identity is unchanged — after a suspected leak of the vault files themselves, recreate with: byre credentials init --replace)")
	return nil
}

// CredentialsList implements `byre credentials list`: the declared set with
// kind, target, and value-state, plus stored values no declaration names.
// Values render nowhere.
func CredentialsList(s Streams, projectDir string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	v := credentials.Open(paths.Dir, paths.ID)
	cfg, cfgErr := config.Load(projectDir)
	if cfgErr != nil {
		fmt.Fprintf(s.Err, "byre: config unreadable (%v) — showing the vault side only.\n", cfgErr)
	}
	stored := map[string]bool{}
	for _, n := range v.EntryNames() {
		stored[n] = true
	}
	if len(cfg.Credentials) == 0 && len(stored) == 0 {
		fmt.Fprintln(s.Out, "no credentials declared or stored.")
		if !v.Exists() {
			fmt.Fprintln(s.Out, "start: byre credentials init && byre credentials declare <name> --kind env --target VAR && byre credentials set <name>")
		}
		return nil
	}
	for _, d := range cfg.Credentials {
		fmt.Fprintf(s.Out, "%s\t%s → %s\t%s\n", d.Name, d.Kind, d.Target, credentials.ValueState(stored[d.Name]))
		delete(stored, d.Name)
	}
	for _, n := range v.EntryNames() {
		if stored[n] {
			fmt.Fprintf(s.Out, "%s\t(stored, not declared — declare it to deliver it)\n", n)
		}
	}
	if !v.Exists() && len(cfg.Credentials) > 0 {
		fmt.Fprintln(s.Out, "no vault exists yet — create one: byre credentials init")
	}
	return nil
}
