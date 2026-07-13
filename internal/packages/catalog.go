package packages

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Entry is one row in the catalog: a resolvable package, or a scoped problem
// row (INVALID / conflict / LEGACY) that list surfaces and resolve rejects
// only when referenced (D1e).
type Entry struct {
	ID          string
	Alias       string // bare name when this is a bundled package (D1c)
	Version     string
	Kind        Kind
	Provenance  Provenance
	Description string
	// Reason explains INVALID / LEGACY / conflict rows.
	Reason string
	// ConflictWith names the other location when Provenance == ProvConflict.
	ConflictWith string

	// Loading: either Dir (local/legacy on disk) or FS+Sub (bundled embed).
	// Installed snapshots (phase 2) will also use Dir under packages/<digest>.
	Dir string
	FS  fs.FS  // non-nil for bundled
	Sub string // path within FS to the package root

	// Primary is the primary-file basename: "skill.toml" or "template.config".
	Primary string

	// Manifest holds the stage-1 [package] core when present.
	Manifest Manifest

	// LooksLikeAgent is set for local skill problem rows when the primary
	// contains an [agent] table (so the agent picker can list them disabled).
	LooksLikeAgent bool
}

// Catalog is the multi-provider package index for one store (D1).
type Catalog struct {
	Home string
	// DisplayVer is the human-facing byre version (version.String): bundled
	// Manifest.Version, provenance labels, mirror stamp alignment (D4d).
	DisplayVer string
	// CompatVer is the parseable semver for requires_byre only (version.Semver).
	CompatVer string

	// entries keyed by canonical ID. Aliases are not keys -- Resolve expands.
	byID map[string]*Entry
	// bare alias -> canonical ID (bundled only, D1c/D1f).
	aliases map[string]string
	// protected bare names: bundled bare + retired (D1c, D15).
	protected map[string]string // bare -> reason
	// ordered for stable List.
	order []string

	// Stage2Skill / Stage2Template run eager stage-2 classification on local
	// packages at ingest (Pete ruling, round 3). Taken from package-level
	// Stage2Skill/Stage2Template hooks at LoadCatalog time. Bundled packages
	// never use these.
	Stage2Skill    func(primary []byte) error
	Stage2Template func(primary []byte) error
}

// Stage2 hooks for eager local classification. Wired by skills/config init
// so packages does not import them (would cycle).
var (
	Stage2Skill    func(primary []byte) error
	Stage2Template func(primary []byte) error
)

// LoadCatalog builds the catalog from bundled embed content and the local
// store under home. It does not mutate the store (EnsureStore does that).
// bundled is an fs.FS whose top-level dirs are "skills" and "templates".
// displayVer is shown to humans; compatVer feeds requires_byre only.
func LoadCatalog(home string, bundled fs.FS, displayVer, compatVer string) (*Catalog, error) {
	if displayVer == "" {
		displayVer = compatVer
	}
	if compatVer == "" {
		compatVer = displayVer
	}
	c := &Catalog{
		Home:           home,
		DisplayVer:     displayVer,
		CompatVer:      compatVer,
		byID:           map[string]*Entry{},
		aliases:        map[string]string{},
		protected:      map[string]string{},
		Stage2Skill:    Stage2Skill,
		Stage2Template: Stage2Template,
	}
	// Retired names are protected even when not currently bundled (D15).
	for bare, tomb := range RetiredNames {
		c.protected[bare] = "retired: " + tomb
	}
	if err := c.loadBundled(bundled); err != nil {
		return nil, err
	}
	if err := c.loadLocal(filepath.Join(home, "skills"), KindSkill); err != nil {
		return nil, err
	}
	if err := c.loadLocal(filepath.Join(home, "templates"), KindTemplate); err != nil {
		return nil, err
	}
	c.rebuildOrder()
	return c, nil
}

func (c *Catalog) loadBundled(bundled fs.FS) error {
	if bundled == nil {
		return nil
	}
	for _, kind := range []struct {
		sub  string
		kind Kind
		prim string
	}{
		{"skills", KindSkill, "skill.toml"},
		{"templates", KindTemplate, "template.config"},
	} {
		entries, err := fs.ReadDir(bundled, kind.sub)
		if err != nil {
			// Missing top-level is fine (tests may ship a partial FS).
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			bare := e.Name()
			if err := ValidateID(bare, true); err != nil {
				// A bad bundled name is a byre bug; surface as INVALID so the
				// binary still runs, but never alias-protect garbage.
				c.addProblem(BundledID(bare), kind.kind, ProvInvalid, "bundled name invalid: "+err.Error(), "")
				continue
			}
			id := BundledID(bare)
			sub := filepath.ToSlash(filepath.Join(kind.sub, bare))
			primaryPath := filepath.ToSlash(filepath.Join(sub, kind.prim))
			raw, err := fs.ReadFile(bundled, primaryPath)
			if err != nil {
				c.addProblem(id, kind.kind, ProvInvalid, "bundled primary missing: "+err.Error(), "")
				continue
			}
			// Bundled manifests are synthesized at load (D4d) — id/version/
			// kind/api/requires always take the generated values; only the
			// description may come from the file. The mirror's on-disk header
			// is written separately by EnsureStore (mirrorPrimary).
			desc := peekDescription(raw)
			m := Manifest{
				ID:           id,
				Version:      c.DisplayVer, // D4d: equals the byre release string
				Kind:         string(kind.kind),
				PackageAPI:   PackageAPI,
				RequiresByre: ">=" + trimV(c.CompatVer),
				Description:  desc,
			}
			if core, ok, _ := ParseManifestCore(raw); ok {
				if core.Description != "" {
					m.Description = core.Description
				}
			}
			ent := &Entry{
				ID:          id,
				Alias:       bare,
				Version:     m.Version,
				Kind:        kind.kind,
				Provenance:  ProvBundled,
				Description: m.Description,
				FS:          bundled,
				Sub:         sub,
				Primary:     kind.prim,
				Manifest:    m,
			}
			c.protected[bare] = "bundled as " + id
			c.aliases[bare] = id
			if err := c.put(ent); err != nil {
				return err
			}
		}
	}
	return nil
}

// peekDescription pulls a top-level description = "..." from a primary file
// without a full parse (bundled skill.tomls put description outside [package]
// today). Best-effort; empty on failure.
func peekDescription(raw []byte) string {
	if m, ok, err := ParseManifestCore(raw); err == nil && ok && m.Description != "" {
		return m.Description
	}
	var root struct {
		Description string `toml:"description"`
	}
	if _, err := toml.Decode(string(raw), &root); err == nil {
		return root.Description
	}
	return ""
}

func (c *Catalog) loadLocal(root string, kind Kind) error {
	prim := "skill.toml"
	if kind == KindTemplate {
		prim = "template.config"
	}
	// Two-level walk (D1a): root/<name>/prim or root/<owner>/<name>/prim.
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// Skip backup / non-package dirs.
		level1 := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(level1, prim)); err == nil {
			// Package root at level 1 (bare id).
			if err := c.ingestLocal(e.Name(), level1, kind, prim); err != nil {
				return err
			}
			continue
		}
		// Maybe owner/name nesting.
		sub, err := os.ReadDir(level1)
		if err != nil {
			continue
		}
		for _, s := range sub {
			if !s.IsDir() || strings.HasPrefix(s.Name(), ".") {
				continue
			}
			level2 := filepath.Join(level1, s.Name())
			if _, err := os.Stat(filepath.Join(level2, prim)); err == nil {
				id := e.Name() + "/" + s.Name()
				if err := c.ingestLocal(id, level2, kind, prim); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (c *Catalog) ingestLocal(id, dir string, kind Kind, prim string) error {
	raw, err := os.ReadFile(filepath.Join(dir, prim))
	if err != nil {
		c.addProblem(id, kind, ProvInvalid, err.Error(), dir)
		return nil
	}

	// Legacy: bare dir name matches protected/retired -> never load (D10).
	if IsBare(id) && c.IsProtected(id) {
		reason := c.protected[id]
		c.addProblemAgent(id, kind, ProvLegacy,
			"legacy materialized copy of "+reason+"; never loaded. Fork to keep edits, or archive via store-ensure offer.",
			dir, kind == KindSkill && looksLikeAgent(raw))
		return nil
	}

	m, hasPkg, err := ParseManifestCore(raw)
	if err != nil {
		c.addProblem(id, kind, ProvInvalid, err.Error(), dir)
		return nil
	}
	// Problem rows for local dirs are ALWAYS keyed by the store-path id (D1e
	// scoped failure). A hostile declared id must never displace a bundled
	// catalog entry (D1b).
	if hasPkg {
		if err := CheckCompatibility(m, c.CompatVer); err != nil {
			c.addProblem(id, kind, ProvInvalid, err.Error(), dir)
			return nil
		}
		if m.ID != "" && m.ID != id {
			c.addProblem(id, kind, ProvInvalid,
				fmt.Sprintf("declared id %q does not match store path %q", m.ID, id), dir)
			return nil
		}
		if m.Kind != "" && m.Kind != string(kind) {
			c.addProblem(id, kind, ProvInvalid,
				fmt.Sprintf("declared kind %q does not match store location (%s)", m.Kind, kind), dir)
			return nil
		}
	}
	// ID defaults to store-relative path (D1a).
	canon := id
	if hasPkg && m.ID != "" {
		canon = m.ID
	}
	if err := ValidateID(canon, true); err != nil {
		c.addProblem(id, kind, ProvInvalid, err.Error(), dir)
		return nil
	}
	// Protected bare names cannot be claimed by local packages (D1c).
	if IsBare(canon) && c.IsProtected(canon) {
		c.addProblem(id, kind, ProvInvalid,
			fmt.Sprintf("%q is protected (%s); pick another id or fork under a qualified name", canon, c.protected[canon]),
			dir)
		return nil
	}
	if Owner(canon) == "byre" {
		// byre/* is bundled-only (D1b). Key by store path, not the claimed id.
		c.addProblem(id, kind, ProvInvalid, "byre/* is reserved for bundled packages", dir)
		return nil
	}

	// Eager stage-2 classification (round 3): local packages that fail the
	// same strict parse as Load/validate become INVALID rows. Primary only —
	// no payload/context file I/O. Bundled packages skip this.
	if kind == KindSkill && c.Stage2Skill != nil {
		if err := c.Stage2Skill(raw); err != nil {
			c.addProblemAgent(id, kind, ProvInvalid, err.Error(), dir, looksLikeAgent(raw))
			return nil
		}
	}
	if kind == KindTemplate && c.Stage2Template != nil {
		if err := c.Stage2Template(raw); err != nil {
			c.addProblem(id, kind, ProvInvalid, err.Error(), dir)
			return nil
		}
	}

	desc := m.Description
	if desc == "" {
		desc = peekDescription(raw)
	}
	ent := &Entry{
		ID:          canon,
		Version:     m.Version,
		Kind:        kind,
		Provenance:  ProvLocal,
		Description: desc,
		Dir:         dir,
		Primary:     prim,
		Manifest:    m,
	}
	return c.put(ent)
}

// looksLikeAgent reports whether a skill primary contains an [agent] table
// (cheap string scan; used for agent-picker inclusion of INVALID rows).
func looksLikeAgent(raw []byte) bool {
	return strings.Contains(string(raw), "[agent]")
}

// addProblemAgent is addProblem with LooksLikeAgent set on the row.
func (c *Catalog) addProblemAgent(id string, kind Kind, prov Provenance, reason, dir string, agent bool) {
	c.addProblem(id, kind, prov, reason, dir)
	// Find the row we just wrote (path key or sibling of bundled).
	if ent, ok := c.byID[id]; ok && ent.Provenance == prov {
		ent.LooksLikeAgent = agent
		return
	}
	if ent, ok := c.byID[id+"#"+string(prov)]; ok {
		ent.LooksLikeAgent = agent
	}
	if ent, ok := c.byID[id+"#legacy"]; ok && prov == ProvLegacy {
		ent.LooksLikeAgent = agent
	}
}

func (c *Catalog) put(ent *Entry) error {
	if prev, ok := c.byID[ent.ID]; ok {
		// Scoped conflict (D1e): replace both with conflict rows.
		reason := fmt.Sprintf("duplicate id %q: %s and %s", ent.ID, locationOf(prev), locationOf(ent))
		prev.Provenance = ProvConflict
		prev.Reason = reason
		prev.ConflictWith = locationOf(ent)
		// Keep the first as the conflict row; drop the new as a loadable entry
		// but record its location on the first.
		ent.Provenance = ProvConflict
		ent.Reason = reason
		ent.ConflictWith = locationOf(prev)
		// Only one row in byID; the list surface shows the conflict once.
		c.byID[ent.ID] = prev
		return nil
	}
	c.byID[ent.ID] = ent
	return nil
}

func locationOf(e *Entry) string {
	if e.Dir != "" {
		return e.Dir
	}
	if e.FS != nil {
		return "bundled:" + e.Sub
	}
	return string(e.Provenance)
}

func (c *Catalog) addProblem(id string, kind Kind, prov Provenance, reason, dir string) {
	// Never displace a bundled entry (D1b/D1e). Problem rows that would collide
	// with a live bundled id are stored under a sibling key so list can still
	// show them and ResolveName keeps returning the bundled package.
	if prev, ok := c.byID[id]; ok && prev.Provenance == ProvBundled {
		key := id + "#" + string(prov)
		if prov == ProvLegacy {
			key = id + "#legacy"
		}
		c.byID[key] = &Entry{
			ID:         id,
			Kind:       kind,
			Provenance: prov,
			Reason:     reason,
			Dir:        dir,
			Primary:    primaryFor(kind),
		}
		return
	}
	c.byID[id] = &Entry{
		ID:         id,
		Kind:       kind,
		Provenance: prov,
		Reason:     reason,
		Dir:        dir,
		Primary:    primaryFor(kind),
	}
}

func primaryFor(kind Kind) string {
	if kind == KindTemplate {
		return "template.config"
	}
	return "skill.toml"
}

func (c *Catalog) rebuildOrder() {
	c.order = c.order[:0]
	for id := range c.byID {
		c.order = append(c.order, id)
	}
	sort.Strings(c.order)
}

// IsProtected reports whether a bare name is reserved (bundled or retired).
func (c *Catalog) IsProtected(bare string) bool {
	_, ok := c.protected[bare]
	return ok
}

// ExpandAlias returns the canonical ID for a name: if it is a bundled bare
// alias, the byre/<name> id; otherwise the name unchanged (D1f). Does not
// check existence.
func (c *Catalog) ExpandAlias(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "none" {
		return name
	}
	// Removal markers: expand the name after '!'.
	if strings.HasPrefix(name, "!") {
		inner := name[1:]
		if canon, ok := c.aliases[inner]; ok {
			return "!" + canon
		}
		return name
	}
	if canon, ok := c.aliases[name]; ok {
		return canon
	}
	return name
}

// ResolveName is the one resolution function every name surface uses (D1g).
// It expands aliases and looks up the catalog. Missing, INVALID, conflict,
// and LEGACY entries return an error. The returned Entry is always loadable
// (bundled or local or installed).
func (c *Catalog) ResolveName(name string) (*Entry, error) {
	if name == "" {
		return nil, fmt.Errorf("empty package name")
	}
	if name == "none" {
		return nil, fmt.Errorf("%q is the config sentinel, not a package", name)
	}
	canon := c.ExpandAlias(name)
	ent, ok := c.byID[canon]
	if !ok {
		// Also try the raw name in case someone used a key we stored differently.
		ent, ok = c.byID[name]
	}
	if !ok {
		return nil, missingErr(canon, c)
	}
	switch ent.Provenance {
	case ProvInvalid:
		return nil, fmt.Errorf("package %q is invalid: %s", canon, ent.Reason)
	case ProvConflict:
		return nil, fmt.Errorf("package %q conflicts: %s", canon, ent.Reason)
	case ProvLegacy:
		return nil, fmt.Errorf("package %q is a legacy materialized copy and is never loaded: %s", canon, ent.Reason)
	}
	return ent, nil
}

func missingErr(id string, c *Catalog) error {
	bare := BareName(id)
	if tomb := RetiredTombstone(bare); tomb != "" && (IsBare(id) || Owner(id) == "byre") {
		return fmt.Errorf("package %q not found: %s", id, tomb)
	}
	return fmt.Errorf("package %q not found", id)
}

// Lookup returns the entry for a canonical ID without erroring on problem
// rows (for list/inspect). ok is false when absent.
func (c *Catalog) Lookup(id string) (*Entry, bool) {
	canon := c.ExpandAlias(id)
	ent, ok := c.byID[canon]
	if !ok {
		ent, ok = c.byID[id]
	}
	return ent, ok
}

// List returns all catalog rows, sorted by ID, optionally filtered by kind.
// kind == "" returns both. Includes INVALID/conflict/LEGACY rows.
func (c *Catalog) List(kind Kind) []*Entry {
	var out []*Entry
	for _, key := range c.order {
		ent := c.byID[key]
		if kind != "" && ent.Kind != kind {
			continue
		}
		out = append(out, ent)
	}
	return out
}

// ListProblemRows returns INVALID/conflict/LEGACY entries for kind (for
// pickers that must show disabled-with-reason rows, D13).
func (c *Catalog) ListProblemRows(kind Kind) []*Entry {
	var out []*Entry
	for _, ent := range c.List(kind) {
		switch ent.Provenance {
		case ProvInvalid, ProvConflict, ProvLegacy:
			out = append(out, ent)
		}
	}
	return out
}

// ListSkills returns loadable skill IDs (and aliases for bundled) for pickers.
// Prefer alias when present so UIs keep writing friendly bare names (D1c).
func (c *Catalog) ListSkills() []string {
	return c.listNames(KindSkill, false)
}

// ListAgentSkills returns IDs of loadable skills that provide an [agent]
// command. The agent check is done by the skills package (needs full parse);
// this returns all loadable skill IDs for the caller to filter, OR we accept
// a keep callback. For now return all loadable skills -- caller filters.
func (c *Catalog) ListLoadable(kind Kind) []*Entry {
	var out []*Entry
	for _, ent := range c.List(kind) {
		switch ent.Provenance {
		case ProvBundled, ProvLocal, ProvInstalled:
			out = append(out, ent)
		}
	}
	return out
}

func (c *Catalog) listNames(kind Kind, canonical bool) []string {
	var out []string
	for _, ent := range c.ListLoadable(kind) {
		if !canonical && ent.Alias != "" {
			out = append(out, ent.Alias)
		} else {
			out = append(out, ent.ID)
		}
	}
	sort.Strings(out)
	return out
}

// ReadPrimary returns the primary file bytes for an entry. Bundled entries
// read from embed.FS; disk entries from Dir. The returned bytes are the
// on-disk/embed content WITHOUT a synthetic [package] header (callers that
// need the header use Manifest / GenerateBundledHeader).
func (e *Entry) ReadPrimary() ([]byte, error) {
	if e.Dir != "" {
		return os.ReadFile(filepath.Join(e.Dir, e.Primary))
	}
	if e.FS != nil {
		return fs.ReadFile(e.FS, filepath.ToSlash(filepath.Join(e.Sub, e.Primary)))
	}
	return nil, fmt.Errorf("package %q has no load location", e.ID)
}

// OpenRoot returns an fs.FS rooted at the package directory for walking
// payloads. For disk packages this is os.DirFS(Dir); for bundled, a sub-FS.
func (e *Entry) OpenRoot() (fs.FS, error) {
	if e.Dir != "" {
		return os.DirFS(e.Dir), nil
	}
	if e.FS != nil {
		sub, err := fs.Sub(e.FS, e.Sub)
		if err != nil {
			return nil, err
		}
		return sub, nil
	}
	return nil, fmt.Errorf("package %q has no load location", e.ID)
}

// DisplayName is the friendly name for pickers: alias if any, else ID.
func (e *Entry) DisplayName() string {
	if e.Alias != "" {
		return e.Alias
	}
	return e.ID
}

// ProvenanceLabel is the short status/list label (D13).
func (e *Entry) ProvenanceLabel() string {
	switch e.Provenance {
	case ProvBundled:
		if e.Version != "" {
			return "bundled " + e.Version
		}
		return "bundled"
	case ProvInstalled:
		if e.Version != "" {
			return "installed " + e.Version
		}
		return "installed"
	case ProvLocal:
		return "local"
	case ProvLegacy:
		return "LEGACY"
	case ProvInvalid:
		return "INVALID"
	case ProvConflict:
		return "conflict"
	default:
		return string(e.Provenance)
	}
}
