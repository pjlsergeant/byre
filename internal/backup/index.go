// Package backup is the byre backup file: the index, the outer gzip tar with
// its read budget, the nested-tar contract every volume payload must meet,
// and the staging the verbs produce and verify payloads in. Command handlers
// in internal/commands drive it; nothing here touches an engine.
package backup

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/tomldoc"
)

// Format is the backup file format this byre writes and reads. Any new
// field bumps it; an unknown key at this format is a refusal.
const Format = 1

// MinByreVersion is the oldest byre that reads Format: a constant of the
// format, not the running version, so a file written years later still names
// the same floor. A file whose index names a NEWER minimum is refused naming
// it, which is the only sentence a byre too old to read a format can say.
// Set to the release this unit ships in (v1.11.0 is the last one out).
const MinByreVersion = "v1.12.0"

// IndexName, ConfigName and VolumesDir are the outer archive's member names,
// in the order they must appear.
const (
	IndexName  = "backup.toml"
	ConfigName = "byre.config"
	VolumesDir = "volumes"
)

// Credential states the index records for the carried config and the review
// derives from the verified bytes.
const (
	CredRows           = "rows"             // encrypted rows and a [credentials] identity
	CredRowsNoIdentity = "rows-no-identity" // rows with no identity block
	CredIdentityOnly   = "identity-only"    // an identity block with no rows
	CredNone           = "none"
)

// Index is the file's table of contents and a note from the source. Every
// value the review prints as a fact about THIS restore is derived from the
// verified payloads; the index is checked against them, and its references
// table is printed as what the source saw and asserted as nothing more.
type Index struct {
	Format         int        `toml:"format"`
	ByreVersion    string     `toml:"byre_version"`
	MinByreVersion string     `toml:"min_byre_version"`
	Folder         string     `toml:"folder"`
	Engine         string     `toml:"engine"`
	Config         ConfigRow  `toml:"config"`
	Volumes        []Volume   `toml:"volumes"`
	References     References `toml:"references"`
}

// ConfigRow describes the carried config member.
type ConfigRow struct {
	Bytes       int64  `toml:"bytes"`
	SHA256      string `toml:"sha256"`
	Credentials string `toml:"credentials"`
}

// Volume describes one carried volume payload: volumes/<name>.tar.
type Volume struct {
	Name    string `toml:"name"`
	Bytes   int64  `toml:"bytes"`
	SHA256  string `toml:"sha256"`
	Entries int64  `toml:"entries"`
}

// References is what backup saw in the source's resolved set, for the human
// at restore: what this machine will have to satisfy.
type References struct {
	Layers       []string `toml:"layers"`
	Template     string   `toml:"template"`
	Agent        string   `toml:"agent"`
	Skills       []string `toml:"skills"`
	Mounts       []string `toml:"mounts"`
	Context      []string `toml:"context"`
	ClaudeSkills []string `toml:"claude_skills"`
	Seeds        []string `toml:"seeds"`
	Files        []string `toml:"files"`
	Engine       string   `toml:"engine"`
	Base         string   `toml:"base"`
	WorktreeBase string   `toml:"worktree_base"`
}

// Render spells the index as the bytes the file carries. Hand-rendered
// through tomldoc's value renderers (the ONE TOML library, ADR 0044) so the
// shape is fixed by this function and pinned by a golden: every key present
// even when empty, so a reader never guesses.
func (ix Index) Render() []byte {
	var b strings.Builder
	kv := func(k, v string) { b.WriteString(tomldoc.KV(k, v)) }
	num := func(k string, v int64) { b.WriteString(tomldoc.KV(k, strconv.FormatInt(v, 10))) }
	kv("format", tomldoc.Int(ix.Format))
	kv("byre_version", tomldoc.String(ix.ByreVersion))
	kv("min_byre_version", tomldoc.String(ix.MinByreVersion))
	kv("folder", tomldoc.String(ix.Folder))
	kv("engine", tomldoc.String(ix.Engine))
	b.WriteString("\n[config]\n")
	num("bytes", ix.Config.Bytes)
	kv("sha256", tomldoc.String(ix.Config.SHA256))
	kv("credentials", tomldoc.String(ix.Config.Credentials))
	for _, v := range ix.Volumes {
		b.WriteString("\n[[volumes]]\n")
		kv("name", tomldoc.String(v.Name))
		num("bytes", v.Bytes)
		kv("sha256", tomldoc.String(v.SHA256))
		num("entries", v.Entries)
	}
	r := ix.References
	b.WriteString("\n[references]\n")
	kv("layers", tomldoc.StringArray(r.Layers))
	kv("template", tomldoc.String(r.Template))
	kv("agent", tomldoc.String(r.Agent))
	kv("skills", tomldoc.StringArray(r.Skills))
	kv("mounts", tomldoc.StringArray(r.Mounts))
	kv("context", tomldoc.StringArray(r.Context))
	kv("claude_skills", tomldoc.StringArray(r.ClaudeSkills))
	kv("seeds", tomldoc.StringArray(r.Seeds))
	kv("files", tomldoc.StringArray(r.Files))
	kv("engine", tomldoc.String(r.Engine))
	kv("base", tomldoc.String(r.Base))
	kv("worktree_base", tomldoc.String(r.WorktreeBase))
	return []byte(b.String())
}

// ErrNewerFormat reports a file written in a format this byre does not read.
var ErrNewerFormat = errors.New("this backup was written in a newer format")

// ParseIndex is the two-pass read: a lenient first pass takes only the
// format and the minimum version, so a newer file's unknown keys still reach
// the version sentence; the strict format-1 decode follows, refusing any
// unknown key.
func ParseIndex(raw []byte) (Index, error) {
	var head struct {
		Format         int    `toml:"format"`
		MinByreVersion string `toml:"min_byre_version"`
	}
	if err := toml.Unmarshal(raw, &head); err != nil {
		return Index{}, fmt.Errorf("%s: %w", IndexName, tomldoc.Positioned(err))
	}
	if head.Format != Format {
		min := head.MinByreVersion
		if min == "" {
			min = "(not stated)"
		}
		return Index{}, fmt.Errorf("%w (format %d; it needs byre %s or newer, and this byre reads format %d)", ErrNewerFormat, head.Format, min, Format)
	}
	var ix Index
	d := toml.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&ix); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			var keys []string
			for _, de := range strict.Errors {
				keys = append(keys, strings.Join(de.Key(), "."))
			}
			return Index{}, fmt.Errorf("%s: unknown key(s) at format %d: %s", IndexName, Format, strings.Join(keys, ", "))
		}
		return Index{}, fmt.Errorf("%s: %w", IndexName, tomldoc.Positioned(err))
	}
	// Lists print as what the source saw; nil and empty are one value.
	return ix, nil
}

// MaxPhysicalName bounds a physical volume name (the destination's project
// prefix joined to the logical name): a design bound, not an engine fact in
// the tree.
const MaxPhysicalName = 255

// CheckIndex applies the bounds both verbs enforce on an index: the format,
// the credential state, every logical volume name (the config grammar, not
// "." or "..", unique, and within MaxPhysicalName once joined to prefix),
// and per-volume counts. Backup refuses before publishing on the same
// rules, so a backup byre produced is one byre reads.
func CheckIndex(ix Index, prefix string) error {
	if ix.Format != Format {
		return fmt.Errorf("format %d is not %d", ix.Format, Format)
	}
	switch ix.Config.Credentials {
	case CredRows, CredRowsNoIdentity, CredIdentityOnly, CredNone:
	default:
		return fmt.Errorf("config credentials state %q is not one of %s, %s, %s, %s", ix.Config.Credentials, CredRows, CredRowsNoIdentity, CredIdentityOnly, CredNone)
	}
	if ix.Config.Bytes < 0 || ix.Config.Bytes > config.MaxConfigBytes {
		return fmt.Errorf("config is %d bytes (limit %d)", ix.Config.Bytes, config.MaxConfigBytes)
	}
	if !validSHA(ix.Config.SHA256) {
		return fmt.Errorf("config sha256 %q is not a hex digest", ix.Config.SHA256)
	}
	seen := map[string]bool{}
	for _, v := range ix.Volumes {
		if err := CheckVolumeName(v.Name, prefix); err != nil {
			return err
		}
		if seen[v.Name] {
			return fmt.Errorf("volume %q is listed twice", v.Name)
		}
		seen[v.Name] = true
		if v.Bytes < 0 {
			return fmt.Errorf("volume %q: negative byte count", v.Name)
		}
		if v.Entries < 0 || v.Entries > MaxEntries {
			return fmt.Errorf("volume %q: %d entries (limit %d)", v.Name, v.Entries, MaxEntries)
		}
		if !validSHA(v.SHA256) {
			return fmt.Errorf("volume %q: sha256 %q is not a hex digest", v.Name, v.SHA256)
		}
	}
	return nil
}

// CheckVolumeName is the logical-name rule: the config grammar (one owner),
// not "." or "..", and within MaxPhysicalName once joined to prefix.
func CheckVolumeName(name, prefix string) error {
	if !config.ValidVolumeName(name) {
		return fmt.Errorf("volume name %q has characters not allowed in a volume name", name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("volume name %q is not a name", name)
	}
	if len(prefix)+len(name) > MaxPhysicalName {
		return fmt.Errorf("volume name %q is too long once joined to this project's prefix (%d bytes, limit %d)", name, len(prefix)+len(name), MaxPhysicalName)
	}
	return nil
}

func validSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// CredentialRows counts a config file's credential rows: env_from_host values
// naming a credential scheme, off the file alone (an [env] shadow does not
// change what the FILE holds). One owner, for the state below and for the
// backup preview's row line.
func CredentialRows(raw []byte) (int, error) {
	cfg, err := config.Parse(raw)
	if err != nil {
		return 0, err
	}
	rows := 0
	for _, src := range cfg.EnvFromHost {
		if config.IsCredentialSource(src) {
			rows++
		}
	}
	return rows, nil
}

// CredentialState classifies a config file's bytes into one of the four
// credential states: its rows against the file-local [credentials] block that
// opens them. The review derives its line from this, never from the index.
func CredentialState(raw []byte) (string, error) {
	rows, err := CredentialRows(raw)
	if err != nil {
		return "", err
	}
	_, hasBlock, err := config.ParseCredentialsBlock(raw)
	if err != nil {
		return "", err
	}
	switch {
	case rows > 0 && hasBlock:
		return CredRows, nil
	case rows > 0:
		return CredRowsNoIdentity, nil
	case hasBlock:
		return CredIdentityOnly, nil
	}
	return CredNone, nil
}

// StripCredentials returns a copy of a config file's bytes with the
// [credentials] block and every credential row deleted, through tomldoc so
// every other byte survives. The source bytes are untouched. A zero-row
// identity block goes too.
func StripCredentials(raw []byte) ([]byte, error) {
	cfg, err := config.Parse(raw)
	if err != nil {
		return nil, err
	}
	doc, err := tomldoc.Load(raw)
	if err != nil {
		return nil, err
	}
	// Sorted, not map order: removing a member of the INLINE spelling
	// rewrites the whole construct (tomldoc's house shape), so two removals
	// in different orders would publish different bytes for one input.
	for _, key := range slices.Sorted(maps.Keys(cfg.EnvFromHost)) {
		if config.IsCredentialSource(cfg.EnvFromHost[key]) {
			if err := doc.RemoveKey([]string{"env_from_host"}, key); err != nil {
				return nil, fmt.Errorf("removing credential row %s: %w", key, err)
			}
		}
	}
	if err := doc.RemoveTableTree("credentials"); err != nil {
		return nil, fmt.Errorf("removing the [credentials] block: %w", err)
	}
	return doc.Bytes(), nil
}
