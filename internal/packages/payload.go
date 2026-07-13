package packages

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// FileEntry is one [[package.files]] row in an installed manifest (D5a):
// every payload named with a manifest-relative source, a package-relative
// destination, and its sha256. Exhaustive by contract -- installation fetches
// exactly this list and nothing else.
type FileEntry struct {
	Src        string `toml:"src"`
	Dest       string `toml:"dest"`
	SHA256     string `toml:"sha256"`
	Executable bool   `toml:"executable,omitempty"`
}

// MaxPayloadFiles bounds a manifest's files list (D1h).
const MaxPayloadFiles = 64

// ParseManifestFiles leniently extracts the [[package.files]] list from
// manifest bytes (unknown keys elsewhere are ignored, like stage 1).
func ParseManifestFiles(content []byte) ([]FileEntry, error) {
	var root struct {
		Package struct {
			Files []FileEntry `toml:"files"`
		} `toml:"package"`
	}
	if _, err := toml.Decode(string(content), &root); err != nil {
		return nil, fmt.Errorf("parse [[package.files]]: %w", err)
	}
	return root.Package.Files, nil
}

// ValidateFilesList enforces the D5a/D5d/D1h rules that do not need I/O:
// bounded count; clean, relative, traversal-free sources and destinations;
// well-formed sha256; destinations duplicate-free including case collisions;
// no entry for the primary file (it cannot contain its own hash, D5c).
func ValidateFilesList(entries []FileEntry, primary string) error {
	if len(entries) > MaxPayloadFiles {
		return fmt.Errorf("files list has %d entries (limit %d)", len(entries), MaxPayloadFiles)
	}
	// Pre-seed with the primary so no payload can collide with the manifest
	// even on a case-insensitive filesystem (SKILL.TOML would overwrite the
	// snapshot's primary and break the digest's integrity claim).
	seenDest := map[string]string{strings.ToLower(primary): primary}
	for i, e := range entries {
		where := fmt.Sprintf("files[%d]", i)
		if err := validRelPath(e.Src); err != nil {
			return fmt.Errorf("%s src %q: %w", where, e.Src, err)
		}
		if err := validRelPath(e.Dest); err != nil {
			return fmt.Errorf("%s dest %q: %w", where, e.Dest, err)
		}
		if strings.EqualFold(e.Dest, primary) {
			return fmt.Errorf("%s dest %q: the primary file has no files entry (the manifest is the primary file)", where, e.Dest)
		}
		if !validSHA256(e.SHA256) {
			return fmt.Errorf("%s: sha256 %q is not 64 hex characters", where, e.SHA256)
		}
		lower := strings.ToLower(e.Dest)
		if prev, ok := seenDest[lower]; ok {
			return fmt.Errorf("%s dest %q collides with %q (case-insensitive)", where, e.Dest, prev)
		}
		seenDest[lower] = e.Dest
	}
	return nil
}

// validRelPath admits clean, relative, slash-separated paths that stay inside
// the package: no absolute paths, no drive/network prefixes, no traversal --
// including ENCODED traversal (D5d: '%' is rejected outright; an origin
// server or intermediary may decode %2e%2e into dots we already refused) --
// no backslashes (one canonical separator), no control characters.
func validRelPath(p string) error {
	if p == "" {
		return fmt.Errorf("empty path")
	}
	if strings.ContainsRune(p, '\\') {
		return fmt.Errorf("use '/' separators")
	}
	if strings.ContainsRune(p, '%') {
		return fmt.Errorf("percent-encoding is rejected (encoded traversal, D5d)")
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return fmt.Errorf("must be relative")
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("control character in path")
		}
	}
	clean := path.Clean(p)
	if clean != p {
		return fmt.Errorf("not clean (want %q)", clean)
	}
	if clean == ".." || strings.HasPrefix(clean, "../") || clean == "." {
		return fmt.Errorf("escapes the package")
	}
	return nil
}

func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// HashBytes returns the lowercase hex sha256 of b.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// PayloadRecord is one payload's digest-relevant identity (D5f): destination
// path, content hash, executable bit.
type PayloadRecord struct {
	Dest       string
	SHA256     string
	Executable bool
}

// PackageDigest is THE package digest (D5f): sha256 over a domain-separated
// canonical encoding of the manifest bytes plus the sorted payload records.
// The manifest is inside the preimage -- contributions and grants live there,
// and a digest that excluded it would let a manifest change ride an unchanged
// digest. Every field is length-prefixed so no concatenation is ambiguous.
func PackageDigest(manifest []byte, payloads []PayloadRecord) string {
	sorted := append([]PayloadRecord{}, payloads...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Dest < sorted[j].Dest })

	h := sha256.New()
	fmt.Fprintf(h, "byre-package-digest-v1\x00%d\x00", len(manifest))
	h.Write(manifest)
	for _, p := range sorted {
		exec := 0
		if p.Executable {
			exec = 1
		}
		fmt.Fprintf(h, "\x00%d\x00%s\x00%s\x00%d", len(p.Dest), p.Dest, strings.ToLower(p.SHA256), exec)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// RecordsFromEntries projects a validated files list onto digest records.
func RecordsFromEntries(entries []FileEntry) []PayloadRecord {
	out := make([]PayloadRecord, 0, len(entries))
	for _, e := range entries {
		out = append(out, PayloadRecord{Dest: e.Dest, SHA256: strings.ToLower(e.SHA256), Executable: e.Executable})
	}
	return out
}
