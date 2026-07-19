package config

// The named-declaration genus: the shared mechanics behind [[mcp]] and
// [[claude_skills]] — lists of named declarations where config layers replace
// by name, skill contributions union AFTER the merge, and a `!name` closure
// marker survives the cascade (never consumed) so it can subtract the same
// name from the EFFECTIVE set after the skill union (ADR 0030 semantics,
// adopted wholesale by ADR 0033 and the Claude Skills vocabulary). Each
// vocabulary keeps its own declaration type, validation, and rendering; the
// split/merge/list-validation state machine lives here, once. A new
// vocabulary of this genus plugs in a namedDeclOps and gets the whole
// taxonomy — it must not re-implement these rules.

import (
	"fmt"
	"regexp"
	"slices"
)

// namedDeclOps describes one named-declaration vocabulary to the shared
// machinery: how to read a declaration's name, its name grammar, and the
// vocabulary-specific words its shared error messages print.
type namedDeclOps[T any] struct {
	// label prefixes error messages: "mcp", "claude skill".
	label string
	// markerNoun is what a marker-with-extra-fields probably is: "a real
	// server", "a real declaration".
	markerNoun string
	// nameNoun is the name's noun in closure errors: "server name",
	// "claude skill name".
	nameNoun string
	nameRe   *regexp.Regexp
	name     func(T) string
	// markerExtras reports whether a marker-shaped entry carries fields
	// beyond its name (a real declaration with a mistyped name — refused
	// rather than silently discarded).
	markerExtras func(T) bool
	// validate checks one open declaration's own shape.
	validate func(T) error
}

// The list validators check one vocabulary's declarations per the shared
// lifecycle split, differing only in marker policy:
//
// validateNamedDeclsLayer permits `name = "!x"` closure markers (name-only —
// other fields set suggest a real declaration with a mistyped name, refused
// rather than silently discarded) and rejects in-layer duplicate names
// (merge would silently replace).
func validateNamedDeclsLayer[T any](ops namedDeclOps[T], decls []T, closed []string) error {
	return validateNamedDecls(ops, decls, closed, func(d T, name string) error {
		if ops.markerExtras(d) {
			return fmt.Errorf("%s %s: a closure marker takes only a name — other fields here suggest %s with a mistyped name", ops.label, name, ops.markerNoun)
		}
		if !ops.nameRe.MatchString(name[1:]) {
			return fmt.Errorf("%s closure %q: %q is not a valid %s", ops.label, name, name[1:], ops.nameNoun)
		}
		return nil
	})
}

// validateNamedDeclsResolved rejects markers (Merge extracts them into the
// closed list, so one surviving to a resolved config is a bug) and
// duplicates alike.
func validateNamedDeclsResolved[T any](ops namedDeclOps[T], decls []T, closed []string) error {
	return validateNamedDecls(ops, decls, closed, func(d T, name string) error {
		return fmt.Errorf("%s %s: a closure marker is only meaningful in a cascade layer", ops.label, name)
	})
}

// validateNamedDecls is the shared body; marker is the caller's policy for a
// `!name` entry (nil error = tolerate and skip the open-declaration checks).
// The closed list's names are checked against the grammar in both modes.
func validateNamedDecls[T any](ops namedDeclOps[T], decls []T, closed []string, marker func(d T, name string) error) error {
	seen := map[string]bool{}
	for _, d := range decls {
		name := ops.name(d)
		if isRemoval(name) {
			if err := marker(d, name); err != nil {
				return err
			}
			continue
		}
		if err := ops.validate(d); err != nil {
			return err
		}
		if seen[name] {
			return fmt.Errorf("%s %s appears twice in this file; merge would keep only the last one", ops.label, name)
		}
		seen[name] = true
	}
	for _, cl := range closed {
		if !ops.nameRe.MatchString(cl) {
			return fmt.Errorf("%s closure %q: not a valid %s", ops.label, cl, ops.nameNoun)
		}
	}
	return nil
}

// splitNamedDecls separates a declaration list into real declarations and the
// stripped names of its `!name` closure markers, folding an already-populated
// closed list (a previously merged config re-entering Merge) into the latter.
func splitNamedDecls[T any](decls []T, alreadyClosed []string, name func(T) string) (open []T, closed []string) {
	for _, d := range decls {
		if n := name(d); isRemoval(n) {
			closed = append(closed, n[1:])
			continue
		}
		open = append(open, d)
	}
	for _, c := range alreadyClosed {
		if !slices.Contains(closed, c) {
			closed = append(closed, c)
		}
	}
	return open, closed
}

// mergeNamedDecls folds one cascade step of a declaration list into
// (open, closed): a `!name` closure is NOT consumed when it removes a
// declaration — it survives the cascade so it can subtract the same name from
// the EFFECTIVE set after skill contributions union in. Precedence stays
// cascade-ordered: a later layer's plain declaration re-opens an earlier
// layer's closure; within one layer a closure beats a plain declaration (adds
// fold first, closures after). Open declarations replace by name (structured
// cascade, like volumes); closures match by exact name.
func mergeNamedDecls[T any](baseDecls []T, baseClosed []string, overDecls []T, overClosed []string, name func(T) string) (open []T, closed []string) {
	open, closed = splitNamedDecls(baseDecls, baseClosed, name)
	overOpen, overClosedAll := splitNamedDecls(overDecls, overClosed, name)
	for _, d := range overOpen {
		n := name(d)
		closed = filter(closed, func(c string) bool { return c != n })
		replaced := false
		for i := range open {
			if name(open[i]) == n {
				open[i] = d
				replaced = true
				break
			}
		}
		if !replaced {
			open = append(open, d)
		}
	}
	for _, c := range overClosedAll {
		open = filter(open, func(d T) bool { return name(d) != c })
		if !slices.Contains(closed, c) {
			closed = append(closed, c)
		}
	}
	return open, closed
}
