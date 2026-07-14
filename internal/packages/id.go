// Package packages is the skill/template package model: identity, manifests,
// the multi-provider catalog, and the store-ensure path (bundled mirror +
// legacy migration). Decision record: docs/adr/0029-skills-are-packages.md;
// user guide: docs/SKILLS.md.
package packages

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Kind discriminates package kinds. One package = one kind.
type Kind string

const (
	KindSkill    Kind = "skill"
	KindTemplate Kind = "template"
)

// Provenance is how a package entered the catalog.
type Provenance string

const (
	ProvBundled   Provenance = "bundled"
	ProvInstalled Provenance = "installed"
	ProvLocal     Provenance = "local"
	ProvLegacy    Provenance = "legacy"
	ProvInvalid   Provenance = "invalid"
	ProvConflict  Provenance = "conflict"
)

// ID grammar: segment(/segment)? where segment = [a-z0-9][a-z0-9-]{0,63}.
// Lowercase only; no dots; no leading '!'; the literal "none" is reserved.
var (
	segmentRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
)

// ValidateID checks a canonical package ID against the grammar above. bareOK allows a single
// segment (local packages may be bare; installed must be qualified).
func ValidateID(id string, bareOK bool) error {
	if id == "" {
		return fmt.Errorf("package id is empty")
	}
	if id == "none" {
		return fmt.Errorf("package id %q is reserved (config sentinel)", id)
	}
	if strings.HasPrefix(id, "!") {
		return fmt.Errorf("package id %q must not start with '!'", id)
	}
	parts := strings.Split(id, "/")
	switch len(parts) {
	case 1:
		if !bareOK {
			return fmt.Errorf("package id %q must be qualified (owner/name)", id)
		}
		if !segmentRe.MatchString(parts[0]) {
			return fmt.Errorf("package id %q: invalid segment %q (want [a-z0-9][a-z0-9-]{0,63})", id, parts[0])
		}
	case 2:
		for _, p := range parts {
			if !segmentRe.MatchString(p) {
				return fmt.Errorf("package id %q: invalid segment %q (want [a-z0-9][a-z0-9-]{0,63})", id, p)
			}
		}
		// byre/* is permanently reserved for bundled-in-this-binary.
		// Claiming it is only legal for the bundled provider; local/installed
		// paths reject it after ValidateID via Owner checks.
	default:
		return fmt.Errorf("package id %q: at most one '/' (owner/name)", id)
	}
	return nil
}

// IsBare reports whether id has no owner segment.
func IsBare(id string) bool {
	return id != "" && !strings.Contains(id, "/")
}

// BareName returns the final segment of an ID (claude from byre/claude).
func BareName(id string) string {
	if i := strings.LastIndex(id, "/"); i >= 0 {
		return id[i+1:]
	}
	return id
}

// Owner returns the owner segment, or "" for bare IDs.
func Owner(id string) string {
	if i := strings.Index(id, "/"); i >= 0 {
		return id[:i]
	}
	return ""
}

// BundledID is the canonical ID for a bundled bare name.
func BundledID(bare string) string {
	return "byre/" + bare
}

// LocalDir maps a package ID to its store-relative directory path:
// bare my-linter -> my-linter; qualified pete/claude -> pete/claude.
func LocalDir(id string) string {
	return id // nested path IS the id for local packages
}

// ShellArg single-quotes an argument for a printed, copy-pasteable command
// when it contains shell-significant characters (same rule as the develop
// eject path's shellArg). Remedy text embeds hint-controlled URIs -- a
// hostile hint must buy an install review, not command injection on paste.
func ShellArg(s string) string {
	const unsafe = " \t\n\"'$\\|&;<>*?(){}[]~!#"
	if s != "" && !strings.ContainsAny(s, unsafe) && !strings.ContainsRune(s, '`') {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// EscapeTerminal strips control characters and ANSI CSI/OSC sequences from a
// string that will be printed as DATA on a terminal surface. Keeps
// printable runes only.
func EscapeTerminal(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == 0x1b {
			// CSI: ESC [ ... final-byte in @-~
			if i+1 < len(runes) && runes[i+1] == '[' {
				i += 2
				for i < len(runes) {
					if runes[i] >= 0x40 && runes[i] <= 0x7e {
						break
					}
					i++
				}
				continue
			}
			// OSC: ESC ] ... BEL or ST
			if i+1 < len(runes) && runes[i+1] == ']' {
				i += 2
				for i < len(runes) {
					if runes[i] == 0x07 {
						break
					}
					if runes[i] == 0x1b && i+1 < len(runes) && runes[i+1] == '\\' {
						i++
						break
					}
					i++
				}
				continue
			}
			// Lone ESC: drop it.
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
