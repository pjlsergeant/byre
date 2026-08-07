package config

// Credential declarations ([[credentials]] blocks): byre's vocabulary for
// named project credentials (ADR 0057). The declaration is
// the standing, cascade-visible consent to the SET — which names exist,
// what kind each is, and which variable carries it in the box. Values never
// appear here: they live age-encrypted in the host-side vault
// (internal/credentials) and are decrypted only at launch after the
// per-launch passphrase.
//
// One home only — config layers, like [[context]]: no skill.toml twin, so a
// `!name` closure is fully spent removing the inherited declaration during
// the merge. Rides the shared named-declaration machinery (nameddecl.go).
//
// A declaration is a GRANT surface in the tally sense (a named host-value
// channel beside env_from_host, ADR 0026 amendment pending), but confers
// nothing by itself: without a vault value and an unlocked launch the box
// simply runs without it.

import (
	"fmt"
	"strings"

	"github.com/pjlsergeant/byre/internal/credentials"
)

// CredentialDecl is one declared project credential.
type CredentialDecl struct {
	Name string `toml:"name"`
	// Kind is how the value lands in the box: "env" (exported as an
	// environment variable) or "file" (written under the session tmpfs,
	// with Target holding the path).
	Kind string `toml:"kind"`
	// Target is the environment variable: for env-kind the variable that
	// carries the value byte-exact; for file-kind the variable that holds
	// the byre-owned tmpfs path (/run/byre/credentials/<name> — there are
	// no free filesystem targets).
	Target string `toml:"target"`
}

// ValidCredentialName reports whether s satisfies the credential name
// grammar — for callers (the credentials verbs) validating a bare name.
// The rule's owner is the vault package (names are its entry filenames).
func ValidCredentialName(s string) bool { return credentials.ValidName(s) }

// ValidateCredentialDecl checks one declaration's own shape.
func ValidateCredentialDecl(cd CredentialDecl) error {
	if err := credentials.ValidateName(cd.Name); err != nil {
		return err
	}
	switch cd.Kind {
	case "env", "file":
	case "":
		return fmt.Errorf("credential %s: kind is required (env | file)", cd.Name)
	default:
		return fmt.Errorf("credential %s: kind %q invalid (want env|file)", cd.Name, cd.Kind)
	}
	if cd.Target == "" {
		return fmt.Errorf("credential %s: target is required (the environment variable that carries it)", cd.Name)
	}
	if !envKeyRe.MatchString(cd.Target) {
		return fmt.Errorf("credential %s: target %q is not a valid environment variable name", cd.Name, cd.Target)
	}
	// The same reservation [env] and env_from_host carry: a BYRE_ target
	// would let a credential value switch byre's own runtime vocabulary.
	if strings.HasPrefix(cd.Target, "BYRE_") {
		return fmt.Errorf("credential %s: target %s: the BYRE_ namespace is byre's runtime vocabulary and can't carry a credential", cd.Name, cd.Target)
	}
	return nil
}

// credentialDeclOps plugs [[credentials]] into the shared named-declaration
// machinery (nameddecl.go). nameRe is the genus grammar for CLOSURE names;
// open declarations are held to the (tighter) credential rule in validate.
var credentialDeclOps = namedDeclOps[CredentialDecl]{
	label:        "credential",
	markerNoun:   "a real declaration",
	nameNoun:     "credential name",
	nameRe:       declNameRe,
	name:         func(cd CredentialDecl) string { return cd.Name },
	markerExtras: func(cd CredentialDecl) bool { return cd.Kind != "" || cd.Target != "" },
	validate:     ValidateCredentialDecl,
}

// validateCredentialsLayer / validateCredentialsResolved check the
// [[credentials]] list per the shared lifecycle split, plus the one
// cross-entry invariant the genus machinery doesn't know: duplicate
// TARGETS. Two credentials exporting one variable is ambiguous the same
// way two mounts on one container target are.
func (c Config) validateCredentialsLayer() error {
	if err := validateNamedDeclsLayer(credentialDeclOps, c.Credentials, c.CredentialsClosed); err != nil {
		return err
	}
	return validateCredentialTargets(c.Credentials)
}

func (c Config) validateCredentialsResolved() error {
	if err := validateNamedDeclsResolved(credentialDeclOps, c.Credentials, c.CredentialsClosed); err != nil {
		return err
	}
	return validateCredentialTargets(c.Credentials)
}

func validateCredentialTargets(decls []CredentialDecl) error {
	targets := map[string]string{}
	for _, cd := range decls {
		if IsRemoval(cd.Name) {
			continue
		}
		if prev := targets[cd.Target]; prev != "" {
			return fmt.Errorf("credential %s: target %s collides with credential %s", cd.Name, cd.Target, prev)
		}
		targets[cd.Target] = cd.Name
	}
	return nil
}

// mergeCredentials folds one cascade step of the [[credentials]] list into
// (open, closed) per the shared genus taxonomy. With no second home the
// closure's work is done by the merge itself; survivors in
// CredentialsClosed are inert.
func mergeCredentials(base, over Config) (open []CredentialDecl, closed []string) {
	return mergeNamedDecls(base.Credentials, base.CredentialsClosed, over.Credentials, over.CredentialsClosed, credentialDeclOps.name)
}
