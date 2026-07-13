package packages

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

// PackageAPI is the manifest-format contract byre currently understands.
// Bump only when the frozen [package] core itself changes (D4b/c).
const PackageAPI = 1

// Manifest is the frozen [package] core (D4b), shared by skills and templates.
// Stage-1 parse reads only these fields, leniently, for compatibility checks.
type Manifest struct {
	ID           string `toml:"id"`
	Version      string `toml:"version"`
	Kind         string `toml:"kind"` // "skill" | "template"
	PackageAPI   int    `toml:"package_api"`
	RequiresByre string `toml:"requires_byre"`
	Description  string `toml:"description"`
}

// packageRoot is the only TOML shape stage 1 cares about.
type packageRoot struct {
	Package Manifest `toml:"package"`
}

// ParseManifestCore is stage 1: extract [package] leniently (unknown keys
// outside and inside [package] are ignored). Missing [package] returns a
// zero Manifest and ok=false -- local packages may omit it (D4a).
func ParseManifestCore(content []byte) (m Manifest, ok bool, err error) {
	var root packageRoot
	// Lenient: do NOT check Undecoded. Stage 1 must survive a newer package
	// that carries keys this byre does not yet know.
	if _, err := toml.Decode(string(content), &root); err != nil {
		return Manifest{}, false, fmt.Errorf("parse [package]: %w", err)
	}
	if root.Package == (Manifest{}) {
		// Distinguish "no [package] table" from "empty table": either way the
		// caller treats it as absent for local packages. An all-zero table is
		// not useful content.
		if !strings.Contains(string(content), "[package]") {
			return Manifest{}, false, nil
		}
	}
	return root.Package, true, nil
}

// CheckCompatibility validates stage-1 compatibility against this byre:
// package_api (when set) must equal PackageAPI; requires_byre (when set) must
// match byreVersion. byreVersion is the executable's compat semver
// (version.Semver). A devel binary (0.0.0-devel) PASSES every requires_byre
// constraint -- a dev build is newer than everything by definition (compat
// check, not security). Empty optional fields are allowed (local packages).
func CheckCompatibility(m Manifest, byreVersion string) error {
	if m.PackageAPI != 0 && m.PackageAPI != PackageAPI {
		return fmt.Errorf("package_api %d is not supported (this byre speaks package_api %d)", m.PackageAPI, PackageAPI)
	}
	if m.RequiresByre != "" {
		if isDevelCompat(byreVersion) {
			// Dev binary: skip the constraint.
		} else {
			ok, err := MatchConstraint(byreVersion, m.RequiresByre)
			if err != nil {
				return fmt.Errorf("requires_byre %q: %w", m.RequiresByre, err)
			}
			if !ok {
				return fmt.Errorf("requires byre %s; you have %s", m.RequiresByre, byreVersion)
			}
		}
	}
	if k := m.Kind; k != "" && k != string(KindSkill) && k != string(KindTemplate) {
		return fmt.Errorf("kind %q: want %q or %q", k, KindSkill, KindTemplate)
	}
	return nil
}

// isDevelCompat reports the explicit devel bypass for requires_byre.
func isDevelCompat(v string) bool {
	v = strings.TrimSpace(v)
	return v == "0.0.0-devel" || strings.HasPrefix(v, "0.0.0-devel")
}

// RequiredManifestFields reports whether a package that claims to be
// installed/bundled has the required fields (id, version, kind, package_api,
// requires_byre). Local packages may omit them.
func RequiredManifestFields(m Manifest, require bool) error {
	if !require {
		return nil
	}
	var missing []string
	if m.ID == "" {
		missing = append(missing, "id")
	}
	if m.Version == "" {
		missing = append(missing, "version")
	}
	if m.Kind == "" {
		missing = append(missing, "kind")
	}
	if m.PackageAPI == 0 {
		missing = append(missing, "package_api")
	}
	if m.RequiresByre == "" {
		missing = append(missing, "requires_byre")
	}
	if len(missing) > 0 {
		return fmt.Errorf("[package] missing required field(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// StripPackageTable returns content with the [package] table removed so the
// remainder can be strict-parsed as a skill.toml body or a cascade Config
// (template.config). Handles a trailing table at EOF and mid-file tables;
// only top-level [package] (not [package.files]) is stripped as a whole
// section including nested [[package.files]] etc. -- anything under the
// package key prefix until the next top-level non-package header.
//
// For phase 1 there are no [[package.files]] in bundled/local manifests;
// the strip still removes contiguous package.* headers so stage-2 parsers
// that reject unknown keys do not see them.
func StripPackageTable(content []byte) []byte {
	lines := strings.Split(string(content), "\n")
	var out []string
	inPackage := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			// Top-level table header?
			name := strings.TrimSuffix(strings.TrimPrefix(trim, "["), "]")
			name = strings.TrimSpace(name)
			// [package] or [package.something] or [[package.files]]
			base := strings.TrimPrefix(name, "[")
			base = strings.TrimSuffix(base, "]")
			if base == "package" || strings.HasPrefix(base, "package.") {
				inPackage = true
				continue
			}
			// Any other table ends the package section.
			inPackage = false
		}
		if inPackage {
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

// GenerateBundledHeader returns the [package] TOML block injected into bundled
// manifests at load/mirror time (D4d). version equals the byre release.
func GenerateBundledHeader(id, kind, byreVersion, description string) string {
	var b strings.Builder
	b.WriteString("[package]\n")
	fmt.Fprintf(&b, "id = %q\n", id)
	fmt.Fprintf(&b, "version = %q\n", byreVersion)
	fmt.Fprintf(&b, "kind = %q\n", kind)
	fmt.Fprintf(&b, "package_api = %d\n", PackageAPI)
	fmt.Fprintf(&b, "requires_byre = %q\n", ">="+trimV(byreVersion))
	if description != "" {
		fmt.Fprintf(&b, "description = %q\n", description)
	}
	b.WriteString("\n")
	return b.String()
}

func trimV(v string) string {
	return strings.TrimPrefix(v, "v")
}
