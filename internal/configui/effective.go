// effective.go builds the list screens' EFFECTIVE rows (ADR 0018): the merged
// view of lower layers, this layer, and skill contributions, each row
// attributed to its source. Rendering and interaction live in listitem.go;
// this file is pure projection from the model's working state.
package configui

import (
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

// rowKind classifies one effective-list row by where its value comes from and
// what this layer says about it.
type rowKind int

const (
	rowLocal       rowKind = iota // this layer's own entry
	rowOverride                   // this layer's entry shadowing an inherited one
	rowInherited                  // a lower layer's entry, untouched here
	rowRemoved                    // an inherited entry this layer removes
	rowStaleMarker                // a removal marker matching nothing inherited
	rowSkill                      // skill-contributed; read-only here
	rowOffered                    // a declared-but-closed egress door (ADR 0020)
	rowHostEnv                    // env_from_host passthrough (ADR 0026); editable via the Source picker
	rowEnvDoc                     // a skill-documented consumed var nothing provides; suggestion only
)

// listRow is one row of a list screen's effective view. idx points into the
// field's LOCAL backing slice (the entry or marker this layer owns); -1 for
// rows this layer doesn't own (inherited, skill).
type listRow struct {
	kind   rowKind
	text   string // display form of the value
	ident  string // removal identity: package, env key, mount target, container port
	source string // "default", "template:go", "skill:x"; "" for pure local
	also   bool   // local entry duplicating an inherited one (union dedups)
	// disabled: present but switched off, and so granting nothing -- a mount
	// with no bind, or an env_from_host passthrough with an empty source. The
	// row stays visible and actionable (that is the point: an entry with no
	// row cannot be switched back on), and every tally skips it. Distinct
	// from closed: nothing overrode this, it was turned off.
	disabled bool
	idx      int      // index into the local slice, or -1
	vals     []string // inherited raw values, for prefilling an override editor
	// closed: a LOWER layer's '!' closure subtracts this entry after the
	// union, or the row IS a closure marker. Effectiveness is its own
	// property, never inferred from kind -- the row keeps its kind (menu
	// semantics) but tallies as a closed door, not a live grant.
	closed bool
	// skews: the claims this row's value can stop byre asserting, phrased by
	// skills.ReservedEnvSkew. Set only on a skill env row holding one of
	// byre's reserved BYRE_ keys (ADR 0050 tier 2), empty everywhere else:
	// the attribution says WHO set the key, and this says what it affects --
	// which was the one thing status printed and no editor screen did.
	skews string
}

// fieldRows builds the effective rows for a list field: inherited entries in
// lower-layer order (overridden/removed in place), then this layer's own
// additions in file order, then stale markers, then skill contributions --
// cascade order, so the list reads as what the box actually gets.
func (m model) fieldRows(f fieldID) []listRow {
	switch f {
	case fApt:
		return m.aptRows()
	case fEnv:
		return m.envRows()
	case fFiles:
		return m.filesRows()
	case fSkillFiles:
		return m.skillFilesRows()
	case fSources:
		return m.sourceRows()
	case fMounts:
		return m.mountRows()
	case fVolumes:
		return m.volumeRows()
	case fPorts:
		return m.portRows()
	case fEgress:
		return m.egressRows()
	case fMCP:
		return m.mcpRows()
	case fClaudeSkills:
		return m.claudeSkillRows()
	case fContext:
		return m.contextRows()
	}
	return nil
}

// declRowItem is one declaration adapted for the shared named-declaration row
// builder: the raw name (markers keep their "!" spelling) and its display
// line. Anything richer — live notes, override prefills — arrives through
// the namedDeclField callbacks, computed only for the rows that use it.
type declRowItem struct {
	name string
	line string
}

// declRowItems adapts one vocabulary's declaration slice for the row builder.
func declRowItems[T any](decls []T, name, line func(T) string) []declRowItem {
	out := make([]declRowItem, len(decls))
	for i, d := range decls {
		out[i] = declRowItem{name: name(d), line: line(d)}
	}
	return out
}

// namedDeclField is one vocabulary's view for namedDeclRows. The callbacks
// are lazy on purpose: localText may probe the disk (the Claude Skills
// build-will-fail note) and lowerVals allocates prefill slices — both run
// only for the row kinds that surface them, never for every entry per
// render.
type namedDeclField struct {
	local []declRowItem
	// localText renders a local entry's local/override row; nil = the
	// entry's own line.
	localText func(i int) string
	lower     []declRowItem
	// lowerVals is the override editor's prefill for an INHERITED row; nil =
	// no prefill. Only the rowInherited branch calls it.
	lowerVals   func(i int) []string
	lowerClosed []string
	skillDecls  func(sk string) []declRowItem
	// lowerHas reports whether one lower layer declares rawName (markers
	// keep their "!" spelling) — the lowerSource attribution probe.
	lowerHas func(c config.Config, rawName string) bool
}

// namedDeclRows is the named-declaration genus's effective-row state machine
// (ADR 0033), shared by the MCP and Claude Skills screens. Identity is the
// exact name: config layers replace by name, skill declarations union after,
// and a `!name` marker is a CLOSURE — it survives the cascade and subtracts a
// same-named declaration from ANY source, skills included, which is why a
// skill row here is closable (unlike every other field's read-only skill
// rows). A marker is only stale when it matches nothing anywhere; lower-layer
// closures that closed nothing still render (config, never invisible),
// menu-less because they live in a lower file.
func (m model) namedDeclRows(f namedDeclField) []listRow {
	local, lowerDecls := f.local, f.lower
	localText := func(i int) string {
		if f.localText != nil {
			return f.localText(i)
		}
		return local[i].line
	}
	localIdx := map[string]int{}  // name -> index of a real local entry
	markerIdx := map[string]int{} // name -> index of a !name marker
	for i, it := range local {
		if n, ok := config.CutRemoval(it.name); ok {
			markerIdx[n] = i
		} else {
			localIdx[it.name] = i
		}
	}
	// Lower-layer closures still active here: a local plain declaration of
	// the name re-opens (deletes) the closure, same as the merge.
	var lowerClosures []string
	for _, c := range f.lowerClosed {
		if !hasKey(localIdx, c) {
			lowerClosures = append(lowerClosures, c)
		}
	}
	lowerClosureUsed := map[string]bool{}
	lowerClosedBy := func(name string) bool {
		if slices.Contains(lowerClosures, name) {
			lowerClosureUsed[name] = true
			return true
		}
		return false
	}
	markerMatched := map[int]bool{}

	lower := map[string]bool{}
	var rows []listRow
	for i, it := range lowerDecls {
		it := it
		lower[it.name] = true
		src := m.lowerSource(func(c config.Config) bool { return f.lowerHas(c, it.name) })
		switch {
		case hasKey(markerIdx, it.name):
			markerMatched[markerIdx[it.name]] = true
			rows = append(rows, listRow{kind: rowRemoved, text: it.line, source: src, idx: markerIdx[it.name]})
		case hasKey(localIdx, it.name):
			// Replace-by-name: this layer's declaration shadows the inherited one.
			rows = append(rows, listRow{kind: rowOverride, text: localText(localIdx[it.name]), source: src, idx: localIdx[it.name]})
		default:
			var vals []string
			if f.lowerVals != nil {
				vals = f.lowerVals(i)
			}
			rows = append(rows, listRow{kind: rowInherited, text: it.line, ident: it.name, source: src, vals: vals})
		}
	}
	for i, it := range local {
		if config.IsRemoval(it.name) || lower[it.name] {
			continue
		}
		// Same-layer marker beats the same-layer declaration (closures fold last).
		if hasKey(markerIdx, it.name) {
			markerMatched[markerIdx[it.name]] = true
			rows = append(rows, listRow{kind: rowRemoved, text: it.line, idx: markerIdx[it.name]})
			continue
		}
		rows = append(rows, listRow{kind: rowLocal, text: localText(i), idx: i})
	}
	for _, sk := range m.effectiveSkills() {
		for _, it := range f.skillDecls(sk) {
			if i, ok := markerIdx[it.name]; ok {
				// Closed by this file's own marker: Restore works.
				markerMatched[i] = true
				rows = append(rows, listRow{kind: rowRemoved, text: it.line, source: "skill:" + sk, idx: i})
				continue
			}
			if lowerClosedBy(it.name) {
				rows = append(rows, listRow{kind: rowSkill, closed: true, text: it.line, source: "skill:" + sk + " — closed by '!" + it.name + "'"})
				continue
			}
			// Closable (ident set): "Remove in this project" writes the closure.
			rows = append(rows, listRow{kind: rowSkill, text: it.line, ident: it.name, source: "skill:" + sk})
		}
	}
	for i, it := range local {
		if n, ok := config.CutRemoval(it.name); ok && !markerMatched[i] {
			rows = append(rows, listRow{kind: rowStaleMarker, text: n, idx: i})
		}
	}
	for _, c := range lowerClosures {
		if !lowerClosureUsed[c] {
			c := c
			src := m.lowerSource(func(cf config.Config) bool { return f.lowerHas(cf, "!"+c) })
			// A closure marker is a subtraction, never a grant: closed
			// keeps it out of the effective/fromSkills tallies.
			rows = append(rows, listRow{kind: rowSkill, closed: true, text: "!" + c, source: src})
		}
	}
	return rows
}

func mcpName(mc config.MCP) string                 { return mc.Name }
func claudeSkillName(cs config.ClaudeSkill) string { return cs.Name }

// mcpRows builds the MCP screen's effective view — the shared genus state
// machine (namedDeclRows) over [[mcp]] declarations.
func (m model) mcpRows() []listRow {
	lowerCfg := m.lowerNow()
	return m.namedDeclRows(namedDeclField{
		local:       declRowItems(m.mcps, mcpName, mcpLine),
		lower:       declRowItems(lowerCfg.MCPs, mcpName, mcpLine),
		lowerVals:   func(i int) []string { return mcpVals(lowerCfg.MCPs[i]) },
		lowerClosed: lowerCfg.MCPClosed,
		skillDecls: func(sk string) []declRowItem {
			return declRowItems(m.inh.Skills[sk].MCPs, mcpName, mcpLine)
		},
		lowerHas: func(c config.Config, rawName string) bool { return hasMCPName(c.MCPs, rawName) },
	})
}

// claudeSkillRows builds the Claude Skills screen's effective view — the same
// genus state machine over [[claude_skills]] declarations. Local/override
// rows carry the live build-will-fail note (claudeSkillRowText, a disk
// probe — which is why localText is lazy); everything else renders the
// stable claudeSkillLine. (The dirty signature is computed separately, from
// claudeSkillLine directly — row text never feeds it.)
func (m model) claudeSkillRows() []listRow {
	lowerCfg := m.lowerNow()
	return m.namedDeclRows(namedDeclField{
		local:       declRowItems(m.claudeSkills, claudeSkillName, claudeSkillLine),
		localText:   func(i int) string { return claudeSkillRowText(m.claudeSkills[i]) },
		lower:       declRowItems(lowerCfg.ClaudeSkills, claudeSkillName, claudeSkillLine),
		lowerVals:   func(i int) []string { return claudeSkillVals(lowerCfg.ClaudeSkills[i]) },
		lowerClosed: lowerCfg.ClaudeSkillsClosed,
		skillDecls: func(sk string) []declRowItem {
			return declRowItems(m.inh.Skills[sk].ClaudeSkills, claudeSkillName, claudeSkillLine)
		},
		lowerHas: func(c config.Config, rawName string) bool { return hasClaudeSkillName(c.ClaudeSkills, rawName) },
	})
}

// claudeSkillRowText is the DISPLAY text for a config-declared Claude Skill
// row: the line plus the live build-will-fail note. Kept out of
// claudeSkillLine, which feeds the dirty signature —
// a filesystem-tracking suffix there would flip dirty with no edit.
func claudeSkillRowText(cs config.ClaudeSkill) string {
	line := claudeSkillLine(cs)
	if cs.Path == "" {
		return line
	}
	if n := claudeSkillDirNote(cs.Name, cs.Path); n != "" {
		return line + "  (" + n + ")"
	}
	return line
}

func hasClaudeSkillName(cs []config.ClaudeSkill, name string) bool {
	for _, c := range cs {
		if c.Name == name {
			return true
		}
	}
	return false
}

// claudeSkillVals flattens a declaration for the override editor's prefill,
// in the item editor's input order (name, source path).
func claudeSkillVals(cs config.ClaudeSkill) []string {
	return []string{cs.Name, cs.Path}
}

func contextDeclName(cd config.ContextDecl) string { return cd.Name }

func hasContextName(cds []config.ContextDecl, name string) bool {
	for _, cd := range cds {
		if cd.Name == name {
			return true
		}
	}
	return false
}

// contextVals flattens a declaration for the override editor's prefill, in
// the item editor's shape (name, file, text).
func contextVals(cd config.ContextDecl) []string {
	return []string{cd.Name, cd.File, cd.Text}
}

// contextRows builds the Instructions screen's effective view — the shared
// genus state machine over [[context]] declarations. Config-only vocabulary
// (ADR 0043): skills have their own prose channel, so the skill-contribution
// arm is empty.
func (m model) contextRows() []listRow {
	lowerCfg := m.lowerNow()
	return m.namedDeclRows(namedDeclField{
		local:       declRowItems(m.contexts, contextDeclName, contextDeclLine),
		lower:       declRowItems(lowerCfg.Contexts, contextDeclName, contextDeclLine),
		lowerVals:   func(i int) []string { return contextVals(lowerCfg.Contexts[i]) },
		lowerClosed: lowerCfg.ContextsClosed,
		skillDecls:  func(sk string) []declRowItem { return nil },
		lowerHas:    func(c config.Config, rawName string) bool { return hasContextName(c.Contexts, rawName) },
	})
}

func hasMCPName(ms []config.MCP, name string) bool {
	for _, mc := range ms {
		if mc.Name == name {
			return true
		}
	}
	return false
}

// mcpVals flattens a declaration for the override editor's prefill, in the
// item editor's input order (name, url, command, env, egress, headers). The
// command and headers use the reversible argv form so spaced values survive
// the prefill-and-commit round trip.
func mcpVals(mc config.MCP) []string {
	return []string{mc.Name, mc.URL, joinArgv(mc.Command), strings.Join(mc.Env, " "), strings.Join(mc.Egress, " "), joinHeaders(mc.Headers)}
}

// egressRows mirrors aptRows in shape, but egress `!entry` markers are
// CLOSURES, not plain removals: they survive the cascade and subtract from
// the derived allowlist including skill-declared endpoints, matching on the
// parsed grammar (a portless `!host` closes every port). The rows must tell
// that story: a skill endpoint a closure reaches shows closed (with Restore
// when the marker is this file's own), and a marker is only "stale" when it
// matches nothing anywhere — lower entries, this file's entries, or skills.
func (m model) egressRows() []listRow {
	localIdx := map[string]int{}
	for i, e := range m.egress {
		if !config.IsRemoval(e) {
			localIdx[e] = i
		}
	}
	// localMarkerFor finds this file's own closure matching an open entry.
	localMarkerFor := func(entry string) (idx int, name string, ok bool) {
		for i, e := range m.egress {
			if n, isM := config.CutRemoval(e); isM && config.EgressClosureMatches(n, entry) {
				return i, n, true
			}
		}
		return 0, "", false
	}
	// Lower-layer closures still active at this layer: a local plain entry
	// re-opens (deletes) every closure it matches, same as the merge.
	var lowerClosures []string
	for _, c := range m.lowerNow().EgressClosed {
		reopened := false
		for e := range localIdx {
			if config.EgressClosureMatches(c, e) {
				reopened = true
				break
			}
		}
		if !reopened {
			lowerClosures = append(lowerClosures, c)
		}
	}
	lowerClosureUsed := map[string]bool{}
	lowerClosureFor := func(entry string) (name string, ok bool) {
		for _, c := range lowerClosures {
			if config.EgressClosureMatches(c, entry) {
				lowerClosureUsed[c] = true
				return c, true
			}
		}
		return "", false
	}
	markerMatched := map[int]bool{} // marker idx -> matched something (not stale)

	lower := map[string]bool{}
	var rows []listRow
	for _, e := range m.lowerNow().Egress {
		if config.IsRemoval(e) || lower[e] {
			continue
		}
		lower[e] = true
		e := e
		src := m.lowerSource(func(c config.Config) bool { return slices.Contains(c.Egress, e) })
		if i, _, ok := localMarkerFor(e); ok {
			markerMatched[i] = true
			rows = append(rows, listRow{kind: rowRemoved, text: e, source: src, idx: i})
			continue
		}
		if hasKey(localIdx, e) {
			rows = append(rows, listRow{kind: rowLocal, text: e, source: src, also: true, idx: localIdx[e]})
			continue
		}
		rows = append(rows, listRow{kind: rowInherited, text: e, ident: e, source: src})
	}
	for i, e := range m.egress {
		if config.IsRemoval(e) || lower[e] {
			continue
		}
		if mi, _, ok := localMarkerFor(e); ok {
			markerMatched[mi] = true
			rows = append(rows, listRow{kind: rowRemoved, text: e, idx: mi})
			continue
		}
		rows = append(rows, listRow{kind: rowLocal, text: e, idx: i})
	}
	for _, sk := range m.effectiveSkills() {
		for _, e := range m.inh.Skills[sk].Egress {
			host, port, err := config.ParseEgress(e)
			if err != nil {
				continue
			}
			hp := host + ":" + strconv.Itoa(port)
			if i, _, ok := localMarkerFor(hp); ok {
				// Closed by this file's own marker: Restore (clear it) works.
				markerMatched[i] = true
				rows = append(rows, listRow{kind: rowRemoved, text: hp, source: "skill:" + sk, idx: i})
				continue
			}
			if c, ok := lowerClosureFor(hp); ok {
				// Closed by a lower layer's closure: nothing in THIS file to
				// act on, so the row stays the menu-less rowSkill kind and
				// the attribution carries the truth. closed keeps it out of
				// the effective/exposure tallies -- a closed door is not a
				// live grant (ADR 0018's "must agree with the launch tally").
				rows = append(rows, listRow{kind: rowSkill, closed: true, text: hp, source: "skill:" + sk + " — closed by '!" + c + "'"})
				continue
			}
			rows = append(rows, listRow{kind: rowSkill, text: hp, source: "skill:" + sk})
		}
	}
	// A marker that matched nothing: under an allowlist posture (or none) it
	// truly is stale — it subtracts nothing. Under open-denylist EVERY
	// closure is load-bearing (the host gets blocked whether or not anything
	// declared it), so an unmatched one is this file's own live entry, never
	// "removes nothing".
	openDenylist := m.postureNow() == config.PostureOpenDenylist
	for i, e := range m.egress {
		n, ok := config.CutRemoval(e)
		if !ok || markerMatched[i] {
			continue
		}
		if openDenylist {
			rows = append(rows, listRow{kind: rowLocal, text: e, idx: i})
			continue
		}
		rows = append(rows, listRow{kind: rowStaleMarker, text: n, idx: i})
	}
	// Lower-layer closures that closed nothing shown above: still config, and
	// under open-denylist still enforced — never invisible. Menu-less (they
	// live in a lower file; this editor has nothing to act on).
	for _, c := range lowerClosures {
		if lowerClosureUsed[c] {
			continue
		}
		c := c
		src := m.lowerSource(func(cf config.Config) bool { return slices.Contains(cf.Egress, "!"+c) })
		// Subtraction, not a grant -- see the closed field's comment.
		rows = append(rows, listRow{kind: rowSkill, closed: true, text: "!" + c, source: src})
	}

	// Offered doors (ADR 0020): declared-but-closed entries from lower layers,
	// this layer's own file, and effective skills -- suppressed once the door
	// is already open (the open row tells that story), deduped across sources
	// (first offerer wins the credit). Open/offered comparison is on the
	// NORMALIZED host:port ("github.com" == "github.com:443" at enforcement
	// time), and skill egress counts as open too -- an offered row claiming a
	// reachable host is closed would be a lie.
	normalize := func(e string) string {
		host, port, err := config.ParseEgress(e)
		if err != nil {
			return ""
		}
		return host + ":" + strconv.Itoa(port)
	}
	open := map[string]bool{}
	addOpen := func(e string) {
		if config.IsRemoval(e) {
			return
		}
		n := normalize(e)
		if n == "" {
			return
		}
		// An entry an active closure reaches is NOT open: the offered row may
		// print (truthfully closed), and opening it writes a plain entry that
		// re-opens per the cascade rules.
		if _, _, ok := localMarkerFor(n); ok {
			return
		}
		if _, ok := lowerClosureFor(n); ok {
			return
		}
		open[n] = true
	}
	for _, e := range m.lowerNow().Egress {
		addOpen(e)
	}
	for _, e := range m.egress {
		addOpen(e)
	}
	for _, sk := range m.effectiveSkills() {
		for _, e := range m.inh.Skills[sk].Egress {
			addOpen(e)
		}
	}
	offered := map[string]bool{}
	addOffered := func(e, source string) {
		n := normalize(e)
		if config.IsRemoval(e) || n == "" || open[n] || offered[n] {
			return
		}
		offered[n] = true
		rows = append(rows, listRow{kind: rowOffered, text: e, ident: e, source: source})
	}
	for _, e := range m.lowerNow().EgressOffered {
		e := e
		src := m.lowerSource(func(c config.Config) bool { return slices.Contains(c.EgressOffered, e) })
		addOffered(e, src)
	}
	for _, e := range m.base.EgressOffered {
		addOffered(e, "")
	}
	for _, sk := range m.effectiveSkills() {
		for _, e := range m.inh.Skills[sk].Offered {
			addOffered(e, "skill:"+sk)
		}
	}
	return rows
}

// postureNow returns the network posture a currently-effective skill declares
// ("" = nothing will actually enforce the egress allowlist).
func (m model) postureNow() string {
	for _, e := range m.skillEntries() {
		if p := m.inh.Skills[e.name].Posture; e.on() && p != "" {
			return p
		}
	}
	return ""
}

func (m model) aptRows() []listRow {
	localIdx := map[string]int{}  // real entry -> index in m.apt
	markerIdx := map[string]int{} // marker name -> index in m.apt
	for i, p := range m.apt {
		if n, ok := config.CutRemoval(p); ok {
			markerIdx[n] = i
		} else {
			localIdx[p] = i
		}
	}
	lower := map[string]bool{}
	var rows []listRow
	for _, p := range m.lowerNow().Apt {
		if config.IsRemoval(p) || lower[p] {
			continue // a marker in the base layer removes nothing; ignore
		}
		lower[p] = true
		p := p
		src := m.lowerSource(func(c config.Config) bool { return contains(c.Apt, p) })
		switch {
		case hasKey(markerIdx, p):
			rows = append(rows, listRow{kind: rowRemoved, text: p, source: src, idx: markerIdx[p]})
		case hasKey(localIdx, p):
			rows = append(rows, listRow{kind: rowLocal, text: p, source: src, also: true, idx: localIdx[p]})
		default:
			rows = append(rows, listRow{kind: rowInherited, text: p, ident: p, source: src})
		}
	}
	for i, p := range m.apt {
		if config.IsRemoval(p) || lower[p] {
			continue
		}
		// Merge applies removals after additions, so a same-layer marker turns
		// this layer's own entry off too — the row must not read as effective.
		if hasKey(markerIdx, p) {
			rows = append(rows, listRow{kind: rowRemoved, text: p, idx: markerIdx[p]})
			continue
		}
		rows = append(rows, listRow{kind: rowLocal, text: p, idx: i})
	}
	for i, p := range m.apt {
		if n, ok := config.CutRemoval(p); ok && !lower[n] && !hasKey(localIdx, n) {
			rows = append(rows, listRow{kind: rowStaleMarker, text: n, idx: i})
		}
	}
	return rows
}

// filesRows renders THIS config's [files]: what it stages for the build, and
// what it inherits from a lower layer. Skill payloads are deliberately absent
// -- they are a different question with their own screen (skillFilesRows).
func (m model) filesRows() []listRow {
	localIdx := map[string]int{}
	for i, kv := range m.files {
		localIdx[kv.Key] = i
	}
	var rows []listRow
	lower := m.lowerNow().Files
	for _, src := range slices.Sorted(maps.Keys(lower)) {
		src := src
		from := m.lowerSource(func(c config.Config) bool { _, ok := c.Files[src]; return ok })
		if i, ok := localIdx[src]; ok {
			rows = append(rows, listRow{kind: rowOverride, text: fileLine(m.files[i], false), source: from, idx: i})
		} else {
			rows = append(rows, listRow{kind: rowInherited, text: fileLine(kvItem{src, lower[src]}, false), source: from, vals: []string{src, lower[src]}})
		}
	}
	for i, kv := range m.files {
		if _, inherited := lower[kv.Key]; !inherited {
			rows = append(rows, listRow{kind: rowLocal, text: fileLine(kv, false), idx: i})
		}
	}
	return rows
}

// skillFilesRows is the read-only half: what the enabled skills bake into the
// image. Its own screen because it is a different question from Build files
// -- "where did this come from", not "what am I staging for the build" -- and
// one screen answering both read as though the user had written every line of
// a list that is almost entirely package payloads.
func (m model) skillFilesRows() []listRow {
	var rows []listRow
	for _, sk := range m.effectiveSkills() {
		fs := m.inh.Skills[sk].Files
		for _, src := range slices.Sorted(maps.Keys(fs)) {
			rows = append(rows, listRow{kind: rowSkill, text: fileLine(kvItem{src, fs[src]}, true), source: "skill:" + sk})
		}
	}
	return rows
}

// sourceRows is the [sources] view: package id -> where to install it from,
// this layer's entries and the ones it inherits, each attributed. READ-ONLY,
// and the label says who writes it: `byre preset apply` records a preset's
// hints here after the human accepts them, so an add row would offer a second,
// consent-free way to author acquisition instructions for packages byre will
// later print as commands. Shown rather than hidden, because a hint is what
// turns "skill acme/tool is missing" into a copyable command, and the answer to
// "where would that command send me" must not require opening the file.
func (m model) sourceRows() []listRow {
	local := m.base.Sources
	var rows []listRow
	lower := m.lowerNow().Sources
	for _, id := range slices.Sorted(maps.Keys(lower)) {
		id := id
		if _, overridden := local[id]; overridden {
			continue // the local row below carries it, marked as the override
		}
		src := m.lowerSource(func(c config.Config) bool { _, ok := c.Sources[id]; return ok })
		rows = append(rows, listRow{kind: rowInherited, text: sourceLine(id, lower[id]), ident: id, source: src, idx: -1})
	}
	for _, id := range slices.Sorted(maps.Keys(local)) {
		id := id
		kind, src := rowLocal, ""
		if _, inherited := lower[id]; inherited {
			kind = rowOverride
			src = m.lowerSource(func(c config.Config) bool { _, ok := c.Sources[id]; return ok })
		}
		rows = append(rows, listRow{kind: kind, text: sourceLine(id, local[id]), ident: id, source: src, idx: -1})
	}
	return rows
}

// sourceLine renders one hint: the package id, where it comes from, and
// whether the hint pins a digest -- an unpinned hint installs whatever the URI
// serves today, which is the one property of a hint worth reading at a glance.
func sourceLine(id string, h config.SourceHint) string {
	line := id + " — " + h.URI
	if h.Digest != "" {
		return line + "  (pinned " + h.Digest + ")"
	}
	return line + "  (unpinned)"
}

// fileLine renders one baked file. The arrow reads left-to-right as the copy
// it is -- source into image destination -- and matches the direction mount
// rows already use.
//
// fromSkill changes what the SOURCE means, which is the whole reason this
// takes a flag: a local entry's source is project-relative, a skill's is
// relative to the skill's own directory.
func fileLine(kv kvItem, fromSkill bool) string {
	return fileSource(kv.Key, fromSkill) + " → " + kv.Value
}

// fileSource renders a source for a human. "." is a legal source meaning the
// WHOLE directory -- skillRelPath permits it, and packages that ship a tree
// (a claude-skills bundle) use it -- but a row reading ". → /etc/byre/x" says
// nothing at all to a reader, which is how it looked when this screen first
// shipped.
func fileSource(src string, fromSkill bool) string {
	if filepath.Clean(src) != "." {
		return src
	}
	if fromSkill {
		return "(the whole skill directory)"
	}
	return "(the whole project directory)"
}

func (m model) envRows() []listRow {
	localIdx := map[string]int{}
	for i, kv := range m.env {
		localIdx[kv.Key] = i
	}
	var rows []listRow
	lowerEnv := m.lowerNow().Env
	for _, k := range slices.Sorted(maps.Keys(lowerEnv)) {
		k := k
		src := m.lowerSource(func(c config.Config) bool { _, ok := c.Env[k]; return ok })
		if i, ok := localIdx[k]; ok {
			rows = append(rows, listRow{kind: rowOverride, text: m.env[i].Key + "=" + m.env[i].Value, source: src, idx: i})
		} else {
			rows = append(rows, listRow{kind: rowInherited, text: k + "=" + lowerEnv[k], source: src, vals: []string{k, lowerEnv[k]}})
		}
	}
	for i, kv := range m.env {
		if _, inherited := lowerEnv[kv.Key]; !inherited {
			rows = append(rows, listRow{kind: rowLocal, text: kv.Key + "=" + kv.Value, idx: i})
		}
	}
	for _, sk := range m.effectiveSkills() {
		env := m.inh.Skills[sk].Env
		// Which keys are byre's own is skills' question, not a prefix test
		// restated here -- the same owner reservedEnvNow and status consult.
		reserved := map[string]bool{}
		for _, e := range skills.ReservedEnvOf(sk, env) {
			reserved[e.Key] = true
		}
		for _, k := range slices.Sorted(maps.Keys(env)) {
			r := listRow{kind: rowSkill, text: k + "=" + env[k], source: "skill:" + sk}
			if reserved[k] {
				r.skews = skills.ReservedEnvSkew(k)
			}
			rows = append(rows, r)
		}
	}
	// The env_from_host passthrough (ADR 0026). It lands in the box's env, so
	// it belongs wherever env is inspected — byre's own shipped git-identity
	// defaults included — and it is EDITABLE here: a key set in this file
	// carries an idx and gets Edit/Delete, an inherited one gets Override,
	// and the picker's Inherit option is how a local pin comes back off.
	localHostIdx := map[string]int{}
	for i, kv := range m.hostEnv {
		localHostIdx[kv.Key] = i
	}
	// An explicit [env] KEY beats the passthrough (ADR 0026), so a key set
	// both ways has a DEAD passthrough row. The same fact `byre status`
	// already reports as hostEnvOverridden -- the editor was the surface not
	// showing it, which on a screen whose whole question is "where does this
	// value come from" answered with two rows for one name and no hint that
	// one of them does nothing.
	// Only an [env] LITERAL shadows, at any layer -- that is the whole of
	// resolveHostEnv's override rule. A skill's [runtime].env does NOT: the
	// runner writes skill env first and lets addEnvFromHost overwrite it, so
	// a passthrough colliding with a skill key is the LIVE one. Counting
	// skill env here said the opposite on the one screen whose question is
	// where a value comes from, and hid an active host->box grant behind a
	// row marked dead (gemini's TERM against byre's shipped TERM passthrough
	// is the live case; it is also the only skill/core collision that
	// exists, and the passthrough winning is what makes it right -- the skill
	// hardcodes xterm-256color to escape docker's TERM=xterm default, and the
	// host's real value beats a guess).
	shadowed := map[string]bool{}
	for _, kv := range m.env {
		shadowed[kv.Key] = true
	}
	for k := range lowerEnv {
		shadowed[k] = true
	}
	hostEnv := m.hostEnvNow()
	for _, k := range slices.Sorted(maps.Keys(hostEnv)) {
		k := k
		// disabled: switched off HERE or by a lower layer. The row stays --
		// hostEnvLine renders it "KEY <- disabled", the menu still reaches it
		// (Edit re-picks a scheme, Delete drops back to the cascade), and the
		// tallies skip it. A disabled mount reads the same way.
		// Disabled OUTRANKS shadowed, because resolveHostEnv tests the empty
		// source before the [env] override: a key that is both switched off
		// and shadowed resolves hostEnvDisabled, not hostEnvOverridden. Left
		// to itself `closed` would annotate that row "overridden by [env],
		// not passed" -- a claim about a key nothing had to override.
		off := hostEnv[k] == ""
		if i, ok := localHostIdx[k]; ok {
			rows = append(rows, listRow{kind: rowHostEnv, text: hostEnvLine(k, m.hostEnv[i].Value), source: "env_from_host", idx: i, ident: k, closed: shadowed[k] && !off, disabled: off})
			continue
		}
		from := m.lowerSource(func(c config.Config) bool { _, ok := c.EnvFromHost[k]; return ok })
		if from == "" || from == "inherited" {
			// CoreEnvFromHost is a real cascade layer merged UNDER
			// default.config, so it is not in any chain lowerSource walks --
			// and "inherited" would leave the user with no idea who to argue
			// with about the six keys byre ships.
			from = "byre default"
		}
		rows = append(rows, listRow{kind: rowHostEnv, text: hostEnvLine(k, hostEnv[k]), source: from, idx: -1, ident: k, vals: []string{k, hostEnv[k]}, closed: shadowed[k] && !off, disabled: off})
	}
	// Skill-documented consumed vars (env_docs): a dim suggestion row per
	// declared var NOTHING above provides — once any layer, skill, or the
	// passthrough supplies the key, the suggestion's job is done and it
	// disappears. Pure documentation: never counted, never warned about.
	provided := map[string]bool{}
	for _, r := range rows {
		switch r.kind {
		case rowLocal, rowOverride, rowInherited, rowSkill:
			if k, _, ok := strings.Cut(r.text, "="); ok {
				provided[k] = true
			}
		}
	}
	for k, src := range hostEnv {
		// A switched-off passthrough supplies nothing, so it cannot retire a
		// skill's env_docs suggestion -- the suggestion's whole condition is
		// that NOTHING provides the variable. hostEnvNow keeps disabled keys
		// now (the rows need them); this consumer wants only the live ones.
		if src == "" {
			continue
		}
		provided[k] = true
	}
	for _, sk := range m.effectiveSkills() {
		docs := m.inh.Skills[sk].EnvDocs
		for _, k := range slices.Sorted(maps.Keys(docs)) {
			if !provided[k] {
				rows = append(rows, listRow{kind: rowEnvDoc, text: k, ident: k, source: "skill:" + sk, vals: []string{docs[k]}})
			}
		}
	}
	return rows
}

func (m model) mountRows() []listRow {
	localIdx := map[string]int{}  // target -> index of a real local entry
	markerIdx := map[string]int{} // target -> index of a !target marker
	for i, mt := range m.mounts {
		if n, ok := config.CutRemoval(mt.Target); ok {
			markerIdx[n] = i
		} else {
			localIdx[mt.Target] = i
		}
	}
	lower := map[string]bool{}
	var rows []listRow
	for _, mt := range m.lowerNow().Mounts {
		if config.IsRemoval(mt.Target) || lower[mt.Target] {
			continue
		}
		lower[mt.Target] = true
		t := mt.Target
		src := m.lowerSource(func(c config.Config) bool { return hasMountTarget(c.Mounts, t) })
		switch {
		case hasKey(markerIdx, t):
			rows = append(rows, listRow{kind: rowRemoved, text: mountLine(mt), source: src, idx: markerIdx[t]})
		case hasKey(localIdx, t):
			rows = append(rows, listRow{kind: rowOverride, text: mountLine(m.mounts[localIdx[t]]), source: src, disabled: m.mounts[localIdx[t]].Disabled, idx: localIdx[t]})
		default:
			mode := mt.Mode
			if mode == "" {
				mode = "ro"
			}
			if mt.Disabled {
				mode = "disabled"
			}
			rows = append(rows, listRow{kind: rowInherited, text: mountLine(mt), ident: mt.Target, source: src, disabled: mt.Disabled, vals: []string{mt.Host, mt.Target, mode}})
		}
	}
	for i, mt := range m.mounts {
		if config.IsRemoval(mt.Target) || lower[mt.Target] {
			continue
		}
		// Same-layer marker beats the same-layer entry (removals apply last).
		if hasKey(markerIdx, mt.Target) {
			rows = append(rows, listRow{kind: rowRemoved, text: mountLine(mt), idx: markerIdx[mt.Target]})
			continue
		}
		rows = append(rows, listRow{kind: rowLocal, text: mountLine(mt), disabled: mt.Disabled, idx: i})
	}
	for i, mt := range m.mounts {
		if n, ok := config.CutRemoval(mt.Target); ok && !lower[n] && !hasKey(localIdx, n) {
			rows = append(rows, listRow{kind: rowStaleMarker, text: n, idx: i})
		}
	}
	for _, sk := range m.effectiveSkills() {
		for _, mt := range m.inh.Skills[sk].Mounts {
			rows = append(rows, listRow{kind: rowSkill, text: mountLine(mt), source: "skill:" + sk, disabled: mt.Disabled})
		}
	}
	return rows
}

// volumeRows is mountRows over [[volumes]]: identity is the NAME (that is what
// `!name` removes and what merge replaces by), and skills are the dominant
// declarer, so their contributions show read-only with the skill that brought
// them. The engine-side question -- which of these exist on disk, and clear one
// -- is the Volume data screen's; this one is about what the config SAYS.
func (m model) volumeRows() []listRow {
	localIdx := map[string]int{}  // name -> index of a real local entry
	markerIdx := map[string]int{} // name -> index of a !name marker
	for i, v := range m.volumes {
		if n, ok := config.CutRemoval(v.Name); ok {
			markerIdx[n] = i
		} else {
			localIdx[v.Name] = i
		}
	}
	lower := map[string]bool{}
	var rows []listRow
	for _, v := range m.lowerNow().Volumes {
		if config.IsRemoval(v.Name) || lower[v.Name] {
			continue
		}
		lower[v.Name] = true
		n := v.Name
		src := m.lowerSource(func(c config.Config) bool { return hasVolumeName(c.Volumes, n) })
		switch {
		case hasKey(markerIdx, n):
			rows = append(rows, listRow{kind: rowRemoved, text: volumeLine(v), source: src, idx: markerIdx[n]})
		case hasKey(localIdx, n):
			rows = append(rows, listRow{kind: rowOverride, text: volumeLine(m.volumes[localIdx[n]]), source: src, idx: localIdx[n]})
		default:
			rows = append(rows, listRow{kind: rowInherited, text: volumeLine(v), ident: n, source: src, vals: volumeVals(v)})
		}
	}
	for i, v := range m.volumes {
		if config.IsRemoval(v.Name) || lower[v.Name] {
			continue
		}
		// Same-layer marker beats the same-layer entry (removals apply last).
		if hasKey(markerIdx, v.Name) {
			rows = append(rows, listRow{kind: rowRemoved, text: volumeLine(v), idx: markerIdx[v.Name]})
			continue
		}
		rows = append(rows, listRow{kind: rowLocal, text: volumeLine(v), idx: i})
	}
	for i, v := range m.volumes {
		if n, ok := config.CutRemoval(v.Name); ok && !lower[n] && !hasKey(localIdx, n) {
			rows = append(rows, listRow{kind: rowStaleMarker, text: n, idx: i})
		}
	}
	for _, sk := range m.effectiveSkills() {
		for _, v := range m.inh.Skills[sk].Volumes {
			rows = append(rows, listRow{kind: rowSkill, text: volumeLine(v), source: "skill:" + sk})
		}
	}
	return rows
}

func hasVolumeName(vs []config.Volume, name string) bool {
	for _, v := range vs {
		if v.Name == name {
			return true
		}
	}
	return false
}

// volumeVals flattens a declaration for the override editor's prefill, in the
// item editor's order (name, target, role, sharing).
func volumeVals(v config.Volume) []string {
	role := v.Role
	if role == "" {
		role = "state"
	}
	sharing := v.Sharing
	if sharing == "" {
		sharing = "shared"
	}
	return []string{v.Name, v.Target, role, sharing}
}

func (m model) portRows() []listRow {
	markerIdx := map[int]int{} // container -> index of a remove marker
	for i, p := range m.ports {
		if p.Remove {
			markerIdx[p.Container] = i
		}
	}
	localKeys := map[string]bool{}
	for _, p := range m.ports {
		if !p.Remove {
			localKeys[portKey(p)] = true
		}
	}
	// lastLocalFor is the index of the LAST plain binding this file gives a
	// container port -- the one that survives, since a binding replaces every
	// accumulated binding of its container port, this file's own included.
	lastLocalFor := map[int]int{}
	for i, p := range m.ports {
		if !p.Remove {
			lastLocalFor[p.Container] = i
		}
	}
	lowerByContainer := map[int]bool{}
	lowerKeys := map[string]bool{}
	var rows []listRow
	for _, p := range m.lowerNow().Ports {
		if p.Remove || lowerKeys[portKey(p)] {
			continue
		}
		lowerKeys[portKey(p)] = true
		lowerByContainer[p.Container] = true
		c := p.Container
		// Attribute by the full effective identity, not container alone: a raw
		// layer may bind the same container port more than once, and each row
		// must name the layer that actually declares the binding shown.
		k := portKey(p)
		src := m.lowerSource(func(cf config.Config) bool { return hasPortKey(cf.Ports, k) })
		switch {
		case hasKey(markerIdx, c):
			rows = append(rows, listRow{kind: rowRemoved, text: portLine(p), source: src, idx: markerIdx[c]})
		case localKeys[portKey(p)]:
			// The same effective binding restated locally: merge dedups them.
			for i, lp := range m.ports {
				if !lp.Remove && portKey(lp) == portKey(p) {
					rows = append(rows, listRow{kind: rowLocal, text: portLine(lp), source: src, also: true, idx: i})
					break
				}
			}
		case hasKey(lastLocalFor, c):
			// Replaced: this file binds the same container port differently, so
			// the inherited binding is gone from the resolved set (ADR 0018's
			// replace-by-container-port). The row stays -- it is config, and the
			// menu's Remove still writes a marker -- but closed keeps it out of
			// every tally, since a replaced binding publishes nothing.
			rows = append(rows, listRow{kind: rowInherited, closed: true, text: portLine(p), ident: strconv.Itoa(c), source: src})
		default:
			rows = append(rows, listRow{kind: rowInherited, text: portLine(p), ident: strconv.Itoa(p.Container), source: src})
		}
	}
	localByContainer := map[int]bool{}
	for _, p := range m.ports {
		if !p.Remove {
			localByContainer[p.Container] = true
		}
	}
	for i, p := range m.ports {
		if p.Remove || lowerKeys[portKey(p)] {
			continue
		}
		// Same-layer marker beats the same-layer binding (removals apply last).
		if hasKey(markerIdx, p.Container) {
			rows = append(rows, listRow{kind: rowRemoved, text: portLine(p), idx: markerIdx[p.Container]})
			continue
		}
		// A file's own earlier binding of a container port is replaced by its
		// later one, same rule, so only the last of them is effective.
		rows = append(rows, listRow{kind: rowLocal, closed: lastLocalFor[p.Container] != i, text: portLine(p), idx: i})
	}
	for i, p := range m.ports {
		if p.Remove && !lowerByContainer[p.Container] && !localByContainer[p.Container] {
			rows = append(rows, listRow{kind: rowStaleMarker, text: strconv.Itoa(p.Container), idx: i})
		}
	}
	return rows
}

// effectiveSkills is the skill set currently in effect in the form (lower
// layers + this layer's list + the primary agent), sorted for stable display.
// Only skills with a known runtime contribution are returned.
func (m model) effectiveSkills() []string {
	var out []string
	for _, e := range m.skillEntries() {
		if !e.on() {
			continue
		}
		if rt, ok := m.inh.Skills[e.name]; ok && (len(rt.Mounts) > 0 || len(rt.Volumes) > 0 || len(rt.Env) > 0 || len(rt.EnvDocs) > 0 || len(rt.Egress) > 0 || len(rt.Offered) > 0 || len(rt.MCPs) > 0 || len(rt.ClaudeSkills) > 0 || len(rt.Files) > 0) {
			out = append(out, e.name)
		}
	}
	sort.Strings(out)
	return out
}

// reservedEnvNow is the editor's view of the skills holding byre's own BYRE_
// knobs (ADR 0050 tier 2), built through the same owner status and the launch
// banner read -- skills.ReservedEnvOf decides which keys count, and the claim
// mapping decides what each skews.
//
// It reads the LIVE effective skill set, not a set resolved when the editor
// opened, because that is what the rest of this file does and what the screen
// promises: ticking a skill that sets BYRE_LAUNCH_GATE_FILE has to make the
// exposure line stop asserting the posture, in the same keystroke it makes
// its mounts and env appear. Same resolution as the other two surfaces (each
// skill's declared [runtime].env), evaluated at this editor's current state
// rather than at launch.
func (m model) reservedEnvNow() []skills.ReservedEnvSet {
	var out []skills.ReservedEnvSet
	for _, sk := range m.effectiveSkills() {
		out = append(out, skills.ReservedEnvOf(sk, m.inh.Skills[sk].Env)...)
	}
	return out
}

// envCounts tallies the Env screen by distinct KEY rather than by row: one
// variable in the box is one count, however many layers name it. rowCounts
// cannot do this -- it is per-row and field-agnostic -- and the two summaries
// have to agree, because exposureNow's Env counts keys and develop's launch
// line counts the same way.
//
// [env]-vs-passthrough collisions need no dedupe here: the losing passthrough
// row is already closed, so it never reaches the tally. Skill env is the
// collision that does, in both directions -- a skill restating an [env] key,
// and (since skill env stopped shadowing) a skill restating a passthrough.
//
// The rank picks which contributor the share labels attribute a key to, and
// it follows who actually WINS in the box, which is not config-cascade
// order: [env] bakes as image ENV (gen.writeEnv), skill env and the
// delivered passthrough ride `-e` (runParams), and the engine's -e
// overrides image ENV. So a delivered passthrough beats skill env (written
// after it in the -e map), and skill env beats a baked [env] literal. An
// [env] literal still beats the PASSTHROUGH -- not by value but by blocking
// its delivery (resolveHostEnv) -- which needs no rank arm here: the losing
// passthrough row arrives closed and is excluded above. First review of
// this function ranked [env] on top by cascade instinct; the -e-over-ENV
// fact is the correction.
func (m model) envCounts() (effective, inherited, fromSkills int) {
	rank := func(k rowKind) int {
		switch k {
		case rowHostEnv:
			return 3
		case rowSkill:
			return 2
		case rowLocal, rowOverride, rowInherited:
			return 1
		}
		return 0
	}
	best := map[string]listRow{}
	for _, r := range m.fieldRows(fEnv) {
		if r.closed || r.disabled {
			continue
		}
		var key string
		switch r.kind {
		case rowLocal, rowOverride, rowInherited, rowSkill:
			k, _, ok := strings.Cut(r.text, "=")
			if !ok {
				continue
			}
			key = k
		case rowHostEnv:
			key = r.ident
		default:
			continue
		}
		if cur, ok := best[key]; !ok || rank(r.kind) > rank(cur.kind) {
			best[key] = r
		}
	}
	for _, r := range best {
		effective++
		switch r.kind {
		case rowInherited:
			inherited++
		case rowSkill:
			fromSkills++
		case rowHostEnv:
			// A passthrough this file did not pin comes from below it --
			// byre's core layer at the deepest.
			if r.idx < 0 {
				inherited++
			}
		}
	}
	return
}

// egressCounts tallies the Egress screen by distinct NORMALIZED door rather
// than by row: "github.com" and "github.com:443" are one enforced door, which
// is exactly how the exposure line and the launch tally (resolvedEgress)
// count -- so a spelling restated by a skill must not read as two doors in
// the field summary and one everywhere else (the envCounts sibling; found by
// review hunting envCounts' peers). Unlike env there is no runtime winner to
// attribute a shared door to -- doors union -- so the share label follows
// who the door is YOURS through: a door the user's own layer opens is never
// "from skills", however many skills restate it.
func (m model) egressCounts() (effective, inherited, fromSkills int) {
	rank := func(k rowKind) int {
		switch k {
		case rowLocal, rowOverride:
			return 3
		case rowInherited:
			return 2
		case rowSkill:
			return 1
		}
		return 0
	}
	best := map[string]listRow{}
	for _, r := range m.fieldRows(fEgress) {
		if r.closed || r.disabled {
			continue
		}
		switch r.kind {
		case rowLocal, rowOverride, rowInherited, rowSkill:
		default:
			continue
		}
		host, port, err := config.ParseEgress(r.text)
		if err != nil {
			continue
		}
		door := host + ":" + strconv.Itoa(port)
		if cur, ok := best[door]; !ok || rank(r.kind) > rank(cur.kind) {
			best[door] = r
		}
	}
	for _, r := range best {
		effective++
		switch r.kind {
		case rowInherited:
			inherited++
		case rowSkill:
			fromSkills++
		}
	}
	return
}

// exposureNow tallies the effective GRANTS rows into the shared one-line
// summary (config.Exposure — the same words develop's launch lines use).
// Counts are the effective view (all layers + skills), like the per-field
// summaries, with mounts split by disabled: a disabled mount produces no
// bind, so it must not count as exposure. Workspace stays false — this
// editor summarizes config, and the project mount isn't a config row.
func (m model) exposureNow() config.Exposure {
	var e config.Exposure
	for _, r := range m.fieldRows(fMounts) {
		if r.closed {
			continue
		}
		switch r.kind {
		case rowLocal, rowOverride, rowInherited, rowSkill:
			if r.disabled {
				e.DisabledMounts++
			} else {
				e.Mounts++
			}
		}
	}
	e.Ports, _, _, _ = rowCounts(m.fieldRows(fPorts))
	// Env counts distinct keys, not sources: a skill restating a config key is
	// one variable in the box — the launch tally (exposureOf) counts the same
	// way, and the two surfaces must agree.
	envKeys := map[string]bool{}
	for k := range m.lowerNow().Env {
		envKeys[k] = true
	}
	for _, kv := range m.env {
		envKeys[kv.Key] = true
	}
	for _, sk := range m.effectiveSkills() {
		for k := range m.inh.Skills[sk].Env {
			envKeys[k] = true
		}
	}
	for k, src := range m.hostEnvNow() {
		// "" is switched off: it reaches no box, so it is not a variable in
		// one. byre status omits it for the same reason, and these two
		// tallies have to agree.
		if src == "" {
			continue
		}
		// A credential row is counted as a credential, not as env: the launch
		// tally splits them the same way (an encrypted row never joins the
		// -e export), and a key counted twice would make the two lines
		// disagree over one grant. IsCredentialSource, so a damaged or
		// reserved-key row counts where the list already renders it.
		if config.IsCredentialSource(src) {
			e.Credentials++
			continue
		}
		envKeys[k] = true
	}
	e.Env = len(envKeys)
	e.Posture = m.postureNow()
	// A skill holding byre's own network knobs degrades the posture claim here
	// exactly as it does on status and at launch: this line is the same claim,
	// so it cannot be the one surface that still asserts it.
	e.SkillNetControls = skills.ReservedEnvTouches(m.reservedEnvNow(), skills.ClaimNetwork)
	// The allowlist size only means something under a posture that arms it
	// (open-denylist's network is open — counting doors in a wall that isn't
	// there would be noise); otherwise the per-field summary carries the
	// unenforced caveat. Deduped on the NORMALIZED host:port — "github.com"
	// and "github.com:443" are one enforced door, and the launch tally
	// (resolvedEgress) dedupes the same way.
	if config.PostureEnforcesAllowlist(e.Posture) {
		seen := map[string]bool{}
		for _, r := range m.fieldRows(fEgress) {
			if r.closed {
				continue // a closed door is not an enforced allowlist entry
			}
			switch r.kind {
			case rowLocal, rowOverride, rowInherited, rowSkill:
				if host, port, err := config.ParseEgress(r.text); err == nil {
					seen[host+":"+strconv.Itoa(port)] = true
				}
			}
		}
		e.Egress = len(seen)
	}
	// Closures in effect at this editor — the count NetworkLine renders under
	// open-denylist: this file's own markers plus lower-layer closures no
	// local plain entry re-opened (mirroring egressRows' matching).
	var localPlain []string
	for _, en := range m.egress {
		if config.IsRemoval(en) {
			e.Closed++
		} else {
			localPlain = append(localPlain, en)
		}
	}
	for _, c := range m.lowerNow().EgressClosed {
		reopened := false
		for _, p := range localPlain {
			if config.EgressClosureMatches(c, p) {
				reopened = true
				break
			}
		}
		if !reopened {
			e.Closed++
		}
	}
	lower := m.lowerNow()
	e.RawRunArgs = len(nonEmptyLines(m.textValue(fRunArgs)))+len(lower.RunArgs) > 0
	e.RawBuild = len(nonEmptyLines(m.textValue(fDockerfilePre)))+len(nonEmptyLines(m.textValue(fDockerfilePost)))+
		len(lower.DockerfilePre)+len(lower.DockerfilePost) > 0
	return e
}

// rowCounts tallies a field's effective rows for the form summary line.
// Offered rows are counted separately: they are closed doors, not effective
// state.
func rowCounts(rows []listRow) (effective, inherited, fromSkills, offered int) {
	for _, r := range rows {
		// Effectiveness is the closed field's, not the kind's: a skill row a
		// lower layer closed renders (attributed) but tallies as no grant.
		if r.closed {
			continue
		}
		switch r.kind {
		case rowLocal, rowOverride:
			effective++
		case rowInherited:
			effective++
			inherited++
		case rowSkill:
			effective++
			fromSkills++
		case rowHostEnv:
			// A switched-off passthrough grants nothing, so it is shown but
			// never counted. Otherwise it is effective env, and inherited
			// only when this file did not set it -- idx >= 0 is the same
			// discriminator the row's own "(set here)" annotation uses, and
			// counting a local pin as inherited made the summary read
			// "6 vars (6 inherited)" with one of them set here.
			if r.disabled {
				break
			}
			effective++
			if r.idx < 0 {
				inherited++
			}
		case rowOffered:
			offered++
		}
	}
	return
}

func hasKey[K comparable, V any](m map[K]V, k K) bool { _, ok := m[k]; return ok }

func hasMountTarget(ms []config.Mount, target string) bool {
	for _, mt := range ms {
		if mt.Target == target {
			return true
		}
	}
	return false
}

func hasPortKey(ps []config.Port, key string) bool {
	for _, p := range ps {
		if !p.Remove && portKey(p) == key {
			return true
		}
	}
	return false
}

// portKey is a port's effective identity (interface:host:container), matching
// mergePorts' dedup key.
func portKey(p config.Port) string {
	iface, host := config.PortEffective(p)
	return iface + ":" + strconv.Itoa(host) + ":" + strconv.Itoa(p.Container)
}
