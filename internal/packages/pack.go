package packages

import (
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// Pack emits the distribution manifest for a LOCAL package: the primary
// file's body with a normalized [package] header and a generated, exhaustive
// [[package.files]] list -- every file in the package directory except the
// primary itself, hashes computed from disk. Returns the manifest bytes and
// the package digest over them.
//
// Pack refuses rather than inventing identity: the author must have declared
// a qualified id, a version, and a requires_byre constraint in
// [package] -- those are publishing decisions. kind and package_api are
// mechanical and filled in.
func Pack(ent *Entry) ([]byte, string, error) {
	if ent.Provenance != ProvLocal {
		return nil, "", fmt.Errorf("pack works on local packages; %q is %s (fork it first)", ent.ID, ent.Provenance)
	}
	if ent.Dir == "" {
		return nil, "", fmt.Errorf("package %q has no directory", ent.ID)
	}
	raw, err := ent.ReadPrimary()
	if err != nil {
		return nil, "", err
	}
	m, _, err := ParseManifestCore(raw)
	if err != nil {
		return nil, "", err
	}
	var missing []string
	if m.ID == "" || IsBare(m.ID) {
		missing = append(missing, `a qualified id (id = "owner/name")`)
	}
	if m.Version == "" {
		missing = append(missing, `a version (version = "1.0.0")`)
	}
	if m.RequiresByre == "" {
		missing = append(missing, `a byre constraint (requires_byre = ">=0.2.0")`)
	}
	if len(missing) > 0 {
		return nil, "", fmt.Errorf("declare in [package] before packing: %s", strings.Join(missing, "; "))
	}
	if m.ID != ent.ID {
		return nil, "", fmt.Errorf("declared id %q does not match catalog id %q", m.ID, ent.ID)
	}

	entries, err := enumeratePayloads(os.DirFS(ent.Dir), ent.Primary)
	if err != nil {
		return nil, "", err
	}
	if err := ValidateFilesList(entries, ent.Primary); err != nil {
		return nil, "", err
	}

	manifest := assembleManifest(m, ent.Kind, raw, entries)
	digest := PackageDigest(manifest, RecordsFromEntries(entries))
	return manifest, digest, nil
}

// DisplayDigest computes the digest a pack of a bundled package's bytes would
// emit: the manifest is synthesized from the entry's generated [package] core
// (the same fields the mirror header shows), payloads enumerated from the
// embedded filesystem. Bundled bytes never cross a trust boundary, so this is
// a display digest for inspect parity with installed rows -- never an
// integrity claim (ADR 0029).
func DisplayDigest(ent *Entry) (string, error) {
	if ent.Provenance != ProvBundled {
		return "", fmt.Errorf("display digest is computed for bundled packages; %q is %s", ent.ID, ent.Provenance)
	}
	raw, err := ent.ReadPrimary()
	if err != nil {
		return "", err
	}
	root, err := ent.OpenRoot()
	if err != nil {
		return "", err
	}
	entries, err := enumeratePayloads(root, ent.Primary)
	if err != nil {
		return "", err
	}
	if err := ValidateFilesList(entries, ent.Primary); err != nil {
		return "", err
	}
	manifest := assembleManifest(ent.Manifest, ent.Kind, raw, entries)
	return PackageDigest(manifest, RecordsFromEntries(entries)), nil
}

// assembleManifest renders the distribution manifest: normalized [package]
// header, body, generated files list. Shared by Pack and DisplayDigest so a
// bundled digest and a pack of the same bytes agree byte for byte.
func assembleManifest(m Manifest, kind Kind, raw []byte, entries []FileEntry) []byte {
	// Normalized header: author-declared identity + mechanical fields.
	var hdr strings.Builder
	hdr.WriteString("[package]\n")
	fmt.Fprintf(&hdr, "id = %q\n", m.ID)
	fmt.Fprintf(&hdr, "version = %q\n", m.Version)
	fmt.Fprintf(&hdr, "kind = %q\n", kind)
	fmt.Fprintf(&hdr, "package_api = %d\n", PackageAPI)
	fmt.Fprintf(&hdr, "requires_byre = %q\n", m.RequiresByre)
	if m.Description != "" {
		fmt.Fprintf(&hdr, "description = %q\n", m.Description)
	}
	hdr.WriteString("\n")

	// The source is often a previous pack's output (the README publishing flow
	// writes pack over the primary in place). StripPackageTable drops the old
	// [[package.files]] blocks but the marker comment sits BEFORE the first
	// files header, so it survives -- without stripping it here every re-pack
	// accretes another copy. Trailing-blank normalization then makes pack
	// output a fixed point: packing a packed manifest reproduces it (and its
	// digest) byte for byte.
	body := strings.TrimLeft(string(StripPackageTable([]byte(stripPackMarkers(string(raw))))), "\n")
	if trimmed := strings.TrimRight(body, " \t\r\n"); trimmed != "" {
		body = trimmed + "\n"
	} else {
		body = ""
	}

	var files strings.Builder
	if len(entries) > 0 {
		files.WriteString("\n" + packMarker(kind) + "\n")
		for _, e := range entries {
			files.WriteString("[[package.files]]\n")
			fmt.Fprintf(&files, "src = %q\n", e.Src)
			fmt.Fprintf(&files, "dest = %q\n", e.Dest)
			fmt.Fprintf(&files, "sha256 = %q\n", e.SHA256)
			if e.Executable {
				files.WriteString("executable = true\n")
			}
			files.WriteString("\n")
		}
	}

	return []byte(hdr.String() + body + files.String())
}

// packMarker is the comment pack writes above the generated files list.
func packMarker(kind Kind) string {
	return "# Distribution payload list -- generated by `byre " + string(kind) + " pack`."
}

// stripPackMarkers removes marker comments a previous pack emitted (either
// kind: a body could have been pasted across kinds). It runs on the raw
// manifest, and only drops markers ATTACHED to a [[package.files]] header
// (blank lines and stacked markers in between allowed) -- a matching line
// elsewhere, say inside a multiline string, is data, not a stale marker.
//
// The scan is line-based, like StripPackageTable: a multiline string that
// embeds BOTH a marker and a bare [[package.files]] header line loses its
// tail to the table strip regardless (pre-existing), so at worst this drops
// one more line from a body that was already corrupted. Full TOML awareness
// is that function's rewrite, not this one's job.
func stripPackMarkers(content string) string {
	isMarker := func(l string) bool {
		t := strings.TrimSpace(l)
		return t == packMarker(KindSkill) || t == packMarker(KindTemplate)
	}
	lines := strings.Split(content, "\n")
	drop := make([]bool, len(lines))
	for i := range lines {
		if !isMarker(lines[i]) {
			continue
		}
		j := i + 1
		for j < len(lines) && (isMarker(lines[j]) || strings.TrimSpace(lines[j]) == "") {
			j++
		}
		if j < len(lines) && strings.TrimSpace(lines[j]) == "[[package.files]]" {
			drop[i] = true
		}
	}
	out := lines[:0]
	for i, l := range lines {
		if !drop[i] {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// enumeratePayloads walks a package root and returns one FileEntry per
// regular file (primary excluded), src == dest (package-relative), sorted.
// Symlinks are refused: a payload that points elsewhere is a trap, not a file.
func enumeratePayloads(root fs.FS, primary string) ([]FileEntry, error) {
	var out []FileEntry
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; packages carry files, not links", p)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		if p == primary {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		out = append(out, FileEntry{
			Src:        p,
			Dest:       p,
			SHA256:     HashBytes(b),
			Executable: info.Mode().Perm()&0o111 != 0,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dest < out[j].Dest })
	return out, nil
}
