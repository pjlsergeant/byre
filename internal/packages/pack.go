package packages

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/tomldoc"
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
		return nil, "", fmt.Errorf("pack works on local packages; %q is %s — `byre %s adopt <dir>` makes a directory the local source for this id, `fork` copies it to a NEW id", ent.ID, ent.Provenance, ent.Kind)
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

	entries, err := enumeratePayloads(hostopen.PlainDirFS(ent.Dir, hostopen.StoreOwned), ent.Primary)
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

	return append(HeaderAfterPreamble(hdr.String(), []byte(body)), files.String()...)
}

// HeaderAfterPreamble writes a [package] header over body the one way that
// keeps every body key the body's: below the leading top-level lines (bare
// keys like companion_for / base and their comments) and above the first
// table header. TOML scopes a bare key to the most recent table header, so
// the same line under [package] is package.<key>, which StripPackageTable
// removes before the stage-2 parse ever sees it -- and CheckPackageScoping
// refuses on a local dir. Every byre writer that lays a header over a body
// (pack, fork) goes through here; header should end with a blank line. A
// body with no table header is all preamble.
func HeaderAfterPreamble(header string, body []byte) []byte {
	pre, tables := splitLeadingTopLevel(string(body))
	pre = strings.TrimRight(pre, " \t\r\n")
	if pre != "" {
		pre += "\n\n"
	}
	return []byte(pre + header + tables)
}

// splitLeadingTopLevel splits body at its first REAL table header -- the
// parser's answer, so header-shaped text inside a multiline string or a
// continuation line of a multiline array (`  [1, 2],`) is data and not a split
// point. A body with no header at all is all preamble: bare keys are the only
// thing it can contain, and they must land before the [package] header like
// any other preamble.
//
// A body that will not parse gets no split, for the same reason: pack's stage-1
// decode already refused those bytes on every load path, and inventing a
// boundary in a document byre cannot read would move keys across a header
// whose position it is guessing at.
func splitLeadingTopLevel(body string) (preamble, tables string) {
	d, err := tomldoc.Load([]byte(body))
	if err != nil {
		return body, ""
	}
	at, ok := d.FirstTableHeaderOffset()
	if !ok {
		return body, ""
	}
	return body[:at], body[at:]
}

// packMarker is the comment pack writes above the generated files list.
func packMarker(kind Kind) string {
	return "# Distribution payload list -- generated by `byre " + string(kind) + " pack`."
}

// packageFilesPath is the array-of-tables a pack marker attaches to.
var packageFilesPath = []string{"package", "files"}

// stripPackMarkers removes marker comments a previous pack emitted (either
// kind: a body could have been pasted across kinds). It runs on the raw
// manifest, and only drops markers ATTACHED to a [[package.files]] header
// (blank lines and stacked markers in between allowed) -- a matching line
// elsewhere, say inside a multiline string or above a foreign table, is data,
// not a stale marker.
//
// Attachment is still judged line by line, because a marker IS a line and the
// run between it and the header is a run of lines. What the parser supplies is
// the truth those lines are judged against: which offsets hold a real
// [[package.files]] header (so `[["package".files]]` answers too, where a
// literal-text match saw nothing and let markers accrete on every re-pack),
// and which offsets are inside a value (so a marker-shaped line in a """ block
// is data).
func stripPackMarkers(content string) string {
	d, err := tomldoc.Load([]byte(content))
	if err != nil {
		return content
	}
	heads := map[int]bool{}
	for _, off := range d.ArrayTableHeaderOffsets(packageFilesPath) {
		heads[off] = true
	}
	if len(heads) == 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	offset := make([]int, len(lines))
	marker := make([]bool, len(lines))
	blank := make([]bool, len(lines))
	for i, at := 0, 0; i < len(lines); i++ {
		offset[i] = at
		at += len(lines[i]) + 1 // the separator this split consumed
		t := strings.TrimSpace(lines[i])
		data := d.InValueSpan(offset[i])
		marker[i] = !data && (t == packMarker(KindSkill) || t == packMarker(KindTemplate))
		blank[i] = !data && t == ""
	}
	drop := make([]bool, len(lines))
	for i := range lines {
		if !marker[i] {
			continue
		}
		j := i + 1
		for j < len(lines) && (marker[j] || blank[j]) {
			j++
		}
		if j < len(lines) && heads[offset[j]] {
			drop[i] = true
		}
	}
	out := make([]string, 0, len(lines))
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
		// d.Type() can be unknown (zero) on some filesystems; only the full
		// lstat mode classifies reliably, and misreading a FIFO as regular
		// would block the read below.
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if mode&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; packages carry files, not links", p)
		}
		if mode.IsDir() {
			return nil
		}
		if !mode.IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		if p == primary {
			return nil
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
