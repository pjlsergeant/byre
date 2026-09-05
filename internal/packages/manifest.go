package packages

import (
	"fmt"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/pjlsergeant/byre/internal/tomldoc"
)

// PackageAPI is the manifest-format contract byre currently understands.
// Bump only when the frozen [package] core itself changes.
const PackageAPI = 1

// packageTree is the root key whose whole subtree is the manifest core:
// [package], [package.x], [[package.files]], `package.id = ...`, and every
// quoted spelling of those. Structural judgments about it ride tomldoc's
// parse -- never a text search, which cannot tell ["package"] from a
// `[package]` line inside a Dockerfile heredoc.
const packageTree = "package"

// Manifest is the frozen [package] core, shared by skills and templates.
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
// zero Manifest and ok=false -- local packages may omit it.
//
// Malformed TOML is an ERROR, never "absent": a manifest byre cannot parse is
// a manifest byre must not silently treat as a package-less local directory.
// Presence, when the decoded core is all-zero, is the PARSED document's answer
// -- any expression defining the package tree, empty and quoted tables
// included -- so `["package"]` with nothing under it reads as present.
func ParseManifestCore(content []byte) (m Manifest, ok bool, err error) {
	var root packageRoot
	// Lenient: do NOT check Undecoded. Stage 1 must survive a newer package
	// that carries keys this byre does not yet know.
	if err := toml.Unmarshal(content, &root); err != nil {
		return Manifest{}, false, fmt.Errorf("parse [package]: %w", err)
	}
	if root.Package == (Manifest{}) {
		// An all-zero core is either "no package tree at all" or "a tree that
		// declares no fields byre reads", and those answer differently: only
		// the first is ABSENT. A manifest carrying `["package"]` or a files
		// list and nothing else has declared itself a package, and the callers
		// that branch on ok -- local ingest, Acquire -- must see it as one.
		doc, derr := tomldoc.Load(content)
		if derr != nil {
			// The strict decoder above accepted these bytes, so this is byre's
			// own parser disagreeing with itself -- report it rather than
			// guessing at absence.
			return Manifest{}, false, fmt.Errorf("parse [package]: %w", derr)
		}
		if !doc.HasTableTree(packageTree) {
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
// requires_byre). Local packages may omit them -- they do not reach this check.
func RequiredManifestFields(m Manifest) error {
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

// knownManifestKeys are the keys the [package] table defines: the frozen
// core (Manifest) and the pack tool's [[package.files]] list.
var knownManifestKeys = map[string]bool{
	"id": true, "version": true, "kind": true, "package_api": true,
	"requires_byre": true, "description": true, "files": true,
}

// CheckPackageScoping refuses a LOCAL package whose [package] table carries
// keys it does not define. TOML scopes a bare key to the most recent table
// header, so a body key written below the [package] header -- a
// `files = {...}` meant for [build], an `apt` list meant for the template
// body -- is package.<key>: StripPackageTable removes it with the tree and
// the strict stage-2 parse never sees it, so the contribution vanished
// while validate said ok. `files` counts as defined only in the pack tool's
// shape, an array of tables; an inline table there is [build]'s map,
// misplaced.
//
// Local only. Stage 1 is lenient for installed and bundled packages by
// decision (ParseManifestCore: a newer publisher may add [package] fields
// this byre does not know), and pack never emits a stray, so the author of
// a local dir is the one person holding the pen that wrote one. Bytes that
// do not parse are not this check's to report: stage 1 already did.
func CheckPackageScoping(kind Kind, content []byte) error {
	var root struct {
		Package map[string]any `toml:"package"`
	}
	if err := toml.Unmarshal(content, &root); err != nil {
		return nil
	}
	var stray []string
	for k, v := range root.Package {
		if !knownManifestKeys[k] {
			stray = append(stray, k)
			continue
		}
		if k == "files" && !isArrayOfTables(v) {
			stray = append(stray, k)
		}
	}
	if len(stray) == 0 {
		return nil
	}
	sort.Strings(stray)
	where := "or under the table it belongs to ([build] files, [runtime] env, ...)"
	if kind == KindTemplate {
		where = "where template.config keys live"
	}
	return fmt.Errorf("[package] carries key(s) it does not define: %s -- TOML scopes a bare key to the most recent table header, so a key written below [package] is package.%s, not the %s's, and is dropped with the header; move it above [package], %s",
		strings.Join(stray, ", "), stray[0], kind, where)
}

// isArrayOfTables reports whether a generically decoded value is the shape
// [[package.files]] produces: an array whose every element is a table. A
// bare array of strings decodes as []any too, and would otherwise pass as
// the pack tool's list while carrying nothing it reads.
func isArrayOfTables(v any) bool {
	arr, ok := v.([]any)
	if !ok {
		return false
	}
	for _, e := range arr {
		if _, ok := e.(map[string]any); !ok {
			return false
		}
	}
	return true
}

// StripPackageTable returns content with the whole package tree removed so
// the remainder can be strict-parsed as a skill.toml body or a cascade Config
// (template.config). It is a compatibility wrapper over
// tomldoc.RemoveTableTree, which owns the operation: [package], nested
// [package.x], [[package.files]] blocks wherever they resume, dotted and
// inline spellings, every quoted variant, and the glued comments that describe
// them. Bytes outside those constructs -- including a comment attached to the
// header that FOLLOWS a removed block -- come through identically.
//
// The signature never errors, so it cannot report a document it could not
// parse; it returns such input UNCHANGED. That is not a repair: the caller's
// stage-2 parse then meets the same bytes and fails loudly with a position,
// which is where an unparseable manifest belongs.
func StripPackageTable(content []byte) []byte {
	d, err := tomldoc.Load(content)
	if err != nil {
		return append([]byte(nil), content...)
	}
	if err := d.RemoveTableTree(packageTree); err != nil {
		return append([]byte(nil), content...)
	}
	return d.Bytes()
}

// GenerateBundledHeader returns the [package] TOML block injected into bundled
// manifests at load/mirror time. version equals the byre release.
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
