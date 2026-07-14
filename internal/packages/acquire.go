package packages

import (
	"fmt"
	"strings"
)

// Acquired is a fetched, fully-verified package that has not touched the
// store yet: everything install needs to decide before landing bytes.
type Acquired struct {
	Core     Manifest
	Kind     Kind
	Primary  string
	Manifest []byte
	Files    []FileEntry
	Payloads map[string][]byte
	Exec     map[string]bool
	Digest   string
	Source   *Source
}

// Acquire fetches and verifies a package from a manifest URI without
// installing anything ("reject before anything" ordering): manifest
// core + required fields + qualified ID + kind/verb match +
// compatibility, then the exhaustive files list with every payload
// hash-verified (under the fetch containment and size limits), then the
// package digest.
func Acquire(f *Fetcher, uri string, kind Kind, compatVer string, stage2 func([]byte) error) (*Acquired, error) {
	manifest, src, err := f.FetchManifest(uri)
	if err != nil {
		return nil, err
	}
	m, ok, err := ParseManifestCore(manifest)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("manifest has no [package] block -- not a distributable package (run `byre %s pack` on the source)", kind)
	}
	if err := RequiredManifestFields(m, true); err != nil {
		return nil, err
	}
	if err := ValidateID(m.ID, false); err != nil {
		if IsBare(m.ID) {
			return nil, fmt.Errorf("id %q is not qualified (owner/name) -- ask the publisher to namespace it", m.ID)
		}
		return nil, err
	}
	if Owner(m.ID) == "byre" {
		return nil, fmt.Errorf("id %q: byre/* is reserved for bundled packages", m.ID)
	}
	if m.Kind != string(kind) {
		return nil, fmt.Errorf("package %q is a %s; use `byre %s install`", m.ID, m.Kind, m.Kind)
	}
	if err := CheckCompatibility(m, compatVer); err != nil {
		return nil, err
	}
	primary := primaryFor(kind)
	files, err := ParseManifestFiles(manifest)
	if err != nil {
		return nil, err
	}
	if err := ValidateFilesList(files, primary); err != nil {
		return nil, err
	}
	// Stage-2 strictness at the trust boundary: a package this byre cannot
	// parse must not land as an "installed" snapshot that fails at enable.
	if stage2 != nil {
		if err := stage2(manifest); err != nil {
			return nil, fmt.Errorf("package %q does not parse as a %s: %w", m.ID, kind, err)
		}
	}

	payloads := map[string][]byte{}
	exec := map[string]bool{}
	budget := int64(MaxPayloadTotal)
	for _, e := range files {
		body, err := f.FetchPayload(src, e.Src, &budget)
		if err != nil {
			return nil, err
		}
		if got := HashBytes(body); got != strings.ToLower(e.SHA256) {
			return nil, fmt.Errorf("payload %s: sha256 mismatch (manifest %s..., fetched %s...)",
				e.Dest, e.SHA256[:12], got[:12])
		}
		payloads[e.Dest] = body
		exec[e.Dest] = e.Executable
	}
	return &Acquired{
		Core:     m,
		Kind:     kind,
		Primary:  primary,
		Manifest: manifest,
		Files:    files,
		Payloads: payloads,
		Exec:     exec,
		Digest:   PackageDigest(manifest, RecordsFromEntries(files)),
		Source:   src,
	}, nil
}
