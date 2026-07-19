// effective.go builds the list screens' EFFECTIVE rows (ADR 0018): the merged
// view of lower layers, this layer, and skill contributions, each row
// attributed to its source. Rendering and interaction live in listitem.go;
// this file is pure projection from the model's working state.
package configui

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
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
	rowHostEnv                    // env_from_host passthrough (ADR 0026); read-only here
	rowEnvDoc                     // a skill-documented consumed var nothing provides; suggestion only
)

// listRow is one row of a list screen's effective view. idx points into the
// field's LOCAL backing slice (the entry or marker this layer owns); -1 for
// rows this layer doesn't own (inherited, skill).
type listRow struct {
	kind     rowKind
	text     string   // display form of the value
	ident    string   // removal identity: package, env key, mount target, container port
	source   string   // "default", "template:go", "skill:x"; "" for pure local
	also     bool     // local entry duplicating an inherited one (union dedups)
	disabled bool     // mounts only: present but switched off — no bind
	idx      int      // index into the local slice, or -1
	vals     []string // inherited raw values, for prefilling an override editor
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
	case fMounts:
		return m.mountRows()
	case fPorts:
		return m.portRows()
	case fEgress:
		return m.egressRows()
	case fMCP:
		return m.mcpRows()
	case fClaudeSkills:
		return m.claudeSkillRows()
	}
	return nil
}

// declRowItem is one declaration adapted for the shared named-declaration row
// builder: the raw name (markers keep their "!" spelling), a stable display
// line (removed/inherited/skill rows — it feeds the dirty signature, so no
// live notes), and the override editor's prefill values.
type declRowItem struct {
	name string
	line string
	vals []string // inherited-row prefill
}

// namedDeclRows is the named-declaration genus's effective-row state machine
// (ADR 0033), shared by the MCP and Claude Skills screens. Identity is the
// exact name: config layers replace by name, skill declarations union after,
// and a `!name` marker is a CLOSURE — it survives the cascade and subtracts a
// same-named declaration from ANY source, skills included, which is why a
// skill row here is closable (unlike every other field's read-only skill
// rows). A marker is only stale when it matches nothing anywhere; lower-layer
// closures that closed nothing still render (config, never invisible),
// menu-less because they live in a lower file. localText renders a local
// entry's local/override row — kept a callback (not precomputed) so a live
// note that probes the disk runs only for rows that show it.
func (m model) namedDeclRows(local, lowerDecls []declRowItem, localText func(i int) string, lowerClosed []string, skillDecls func(sk string) []declRowItem, lowerHas func(c config.Config, rawName string) bool) []listRow {
	localIdx := map[string]int{}  // name -> index of a real local entry
	markerIdx := map[string]int{} // name -> index of a !name marker
	for i, it := range local {
		if n, ok := strings.CutPrefix(it.name, "!"); ok {
			markerIdx[n] = i
		} else {
			localIdx[it.name] = i
		}
	}
	// Lower-layer closures still active here: a local plain declaration of
	// the name re-opens (deletes) the closure, same as the merge.
	var lowerClosures []string
	for _, c := range lowerClosed {
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
	for _, it := range lowerDecls {
		it := it
		lower[it.name] = true
		src := m.lowerSource(func(c config.Config) bool { return lowerHas(c, it.name) })
		switch {
		case hasKey(markerIdx, it.name):
			markerMatched[markerIdx[it.name]] = true
			rows = append(rows, listRow{kind: rowRemoved, text: it.line, source: src, idx: markerIdx[it.name]})
		case hasKey(localIdx, it.name):
			// Replace-by-name: this layer's declaration shadows the inherited one.
			rows = append(rows, listRow{kind: rowOverride, text: localText(localIdx[it.name]), source: src, idx: localIdx[it.name]})
		default:
			rows = append(rows, listRow{kind: rowInherited, text: it.line, ident: it.name, source: src, vals: it.vals})
		}
	}
	for i, it := range local {
		if isRemovalName(it.name) || lower[it.name] {
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
		for _, it := range skillDecls(sk) {
			if i, ok := markerIdx[it.name]; ok {
				// Closed by this file's own marker: Restore works.
				markerMatched[i] = true
				rows = append(rows, listRow{kind: rowRemoved, text: it.line, source: "skill:" + sk, idx: i})
				continue
			}
			if lowerClosedBy(it.name) {
				rows = append(rows, listRow{kind: rowSkill, text: it.line, source: "skill:" + sk + " — closed by '!" + it.name + "'"})
				continue
			}
			// Closable (ident set): "Remove in this project" writes the closure.
			rows = append(rows, listRow{kind: rowSkill, text: it.line, ident: it.name, source: "skill:" + sk})
		}
	}
	for i, it := range local {
		if n, ok := strings.CutPrefix(it.name, "!"); ok && !markerMatched[i] {
			rows = append(rows, listRow{kind: rowStaleMarker, text: n, idx: i})
		}
	}
	for _, c := range lowerClosures {
		if !lowerClosureUsed[c] {
			c := c
			src := m.lowerSource(func(cf config.Config) bool { return lowerHas(cf, "!"+c) })
			rows = append(rows, listRow{kind: rowSkill, text: "!" + c, source: src})
		}
	}
	return rows
}

// mcpRows builds the MCP screen's effective view — the shared genus state
// machine (namedDeclRows) over [[mcp]] declarations.
func (m model) mcpRows() []listRow {
	local := make([]declRowItem, len(m.mcps))
	for i, mc := range m.mcps {
		local[i] = declRowItem{name: mc.Name, line: mcpLine(mc)}
	}
	lowerCfg := m.lowerNow()
	lowerDecls := make([]declRowItem, len(lowerCfg.MCPs))
	for i, mc := range lowerCfg.MCPs {
		lowerDecls[i] = declRowItem{name: mc.Name, line: mcpLine(mc), vals: mcpVals(mc)}
	}
	return m.namedDeclRows(local, lowerDecls,
		func(i int) string { return local[i].line },
		lowerCfg.MCPClosed,
		func(sk string) []declRowItem {
			decls := m.inh.Skills[sk].MCPs
			out := make([]declRowItem, len(decls))
			for i, mc := range decls {
				out[i] = declRowItem{name: mc.Name, line: mcpLine(mc)}
			}
			return out
		},
		func(c config.Config, rawName string) bool { return hasMCPName(c.MCPs, rawName) })
}

// claudeSkillRows builds the Claude Skills screen's effective view — the same
// genus state machine over [[claude_skills]] declarations. Local/override
// rows carry the live build-will-fail note (claudeSkillRowText); the stable
// line (claudeSkillLine) feeds everything signature-sensitive.
func (m model) claudeSkillRows() []listRow {
	local := make([]declRowItem, len(m.claudeSkills))
	for i, cs := range m.claudeSkills {
		local[i] = declRowItem{name: cs.Name, line: claudeSkillLine(cs)}
	}
	lowerCfg := m.lowerNow()
	lowerDecls := make([]declRowItem, len(lowerCfg.ClaudeSkills))
	for i, cs := range lowerCfg.ClaudeSkills {
		lowerDecls[i] = declRowItem{name: cs.Name, line: claudeSkillLine(cs), vals: claudeSkillVals(cs)}
	}
	return m.namedDeclRows(local, lowerDecls,
		func(i int) string { return claudeSkillRowText(m.claudeSkills[i]) },
		lowerCfg.ClaudeSkillsClosed,
		func(sk string) []declRowItem {
			decls := m.inh.Skills[sk].ClaudeSkills
			out := make([]declRowItem, len(decls))
			for i, cs := range decls {
				out[i] = declRowItem{name: cs.Name, line: claudeSkillLine(cs)}
			}
			return out
		},
		func(c config.Config, rawName string) bool { return hasClaudeSkillName(c.ClaudeSkills, rawName) })
}

// claudeSkillRowText is the DISPLAY text for a config-declared Claude Skill
// row: the line plus the live build-will-fail note (field-QA 2026-07-17,
// finding 4). Kept out of claudeSkillLine, which feeds the dirty signature —
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
		if !isRemovalName(e) {
			localIdx[e] = i
		}
	}
	// localMarkerFor finds this file's own closure matching an open entry.
	localMarkerFor := func(entry string) (idx int, name string, ok bool) {
		for i, e := range m.egress {
			if n, isM := strings.CutPrefix(e, "!"); isM && config.EgressClosureMatches(n, entry) {
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
		if isRemovalName(e) || lower[e] {
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
		if isRemovalName(e) || lower[e] {
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
				// the attribution carries the truth.
				rows = append(rows, listRow{kind: rowSkill, text: hp, source: "skill:" + sk + " — closed by '!" + c + "'"})
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
		n, ok := strings.CutPrefix(e, "!")
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
		rows = append(rows, listRow{kind: rowSkill, text: "!" + c, source: src})
	}

	// Offered doors (ADR 0020): declared-but-closed entries from lower layers,
	// this layer's own file, and effective skills -- suppressed once the door
	// is already open (the open row tells that story), deduped across sources
	// (first offerer wins the credit). Open/offered comparison is on the
	// NORMALIZED host:port ("github.com" == "github.com:443" at enforcement
	// time), and skill egress counts as open too -- an offered row claiming a
	// reachable host is closed would be a lie (review finding).
	normalize := func(e string) string {
		host, port, err := config.ParseEgress(e)
		if err != nil {
			return ""
		}
		return host + ":" + strconv.Itoa(port)
	}
	open := map[string]bool{}
	addOpen := func(e string) {
		if isRemovalName(e) {
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
		if isRemovalName(e) || n == "" || open[n] || offered[n] {
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
		if n, ok := strings.CutPrefix(p, "!"); ok {
			markerIdx[n] = i
		} else {
			localIdx[p] = i
		}
	}
	lower := map[string]bool{}
	var rows []listRow
	for _, p := range m.lowerNow().Apt {
		if isRemovalName(p) || lower[p] {
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
		if isRemovalName(p) || lower[p] {
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
		if n, ok := strings.CutPrefix(p, "!"); ok && !lower[n] && !hasKey(localIdx, n) {
			rows = append(rows, listRow{kind: rowStaleMarker, text: n, idx: i})
		}
	}
	return rows
}

func (m model) envRows() []listRow {
	localIdx := map[string]int{}
	for i, kv := range m.env {
		localIdx[kv.Key] = i
	}
	var rows []listRow
	lowerEnv := m.lowerNow().Env
	for _, k := range sortedKeys(lowerEnv) {
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
		for _, k := range sortedKeys(env) {
			rows = append(rows, listRow{kind: rowSkill, text: k + "=" + env[k], source: "skill:" + sk})
		}
	}
	// The env_from_host passthrough (ADR 0026), read-only with its source: it
	// lands in the box's env, so it must be visible wherever env is
	// inspected — byre's own shipped git-identity defaults included.
	hostEnv := m.hostEnvNow()
	for _, k := range sortedKeys(hostEnv) {
		rows = append(rows, listRow{kind: rowHostEnv, text: k + " <- host " + hostEnv[k], source: "env_from_host"})
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
	for k := range hostEnv {
		provided[k] = true
	}
	for _, sk := range m.effectiveSkills() {
		docs := m.inh.Skills[sk].EnvDocs
		for _, k := range sortedKeys(docs) {
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
		if n, ok := strings.CutPrefix(mt.Target, "!"); ok {
			markerIdx[n] = i
		} else {
			localIdx[mt.Target] = i
		}
	}
	lower := map[string]bool{}
	var rows []listRow
	for _, mt := range m.lowerNow().Mounts {
		if isRemovalName(mt.Target) || lower[mt.Target] {
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
		if isRemovalName(mt.Target) || lower[mt.Target] {
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
		if n, ok := strings.CutPrefix(mt.Target, "!"); ok && !lower[n] && !hasKey(localIdx, n) {
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
		// Attribute by the full effective identity, not container alone: two
		// layers may bind the same container port on different interfaces/host
		// ports, and each row must name its own layer (review finding).
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
		rows = append(rows, listRow{kind: rowLocal, text: portLine(p), idx: i})
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
		if rt, ok := m.inh.Skills[e.name]; ok && (len(rt.Mounts) > 0 || len(rt.Env) > 0 || len(rt.EnvDocs) > 0 || len(rt.Egress) > 0 || len(rt.Offered) > 0 || len(rt.MCPs) > 0 || len(rt.ClaudeSkills) > 0) {
			out = append(out, e.name)
		}
	}
	sort.Strings(out)
	return out
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
	for k := range m.hostEnvNow() {
		envKeys[k] = true
	}
	e.Env = len(envKeys)
	e.Posture = m.postureNow()
	// The allowlist size only means something under a posture that arms it
	// (open-denylist's network is open — counting doors in a wall that isn't
	// there would be noise); otherwise the per-field summary carries the
	// unenforced caveat. Deduped on the NORMALIZED host:port — "github.com"
	// and "github.com:443" are one enforced door, and the launch tally
	// (resolvedEgress) dedupes the same way.
	if config.PostureEnforcesAllowlist(e.Posture) {
		seen := map[string]bool{}
		for _, r := range m.fieldRows(fEgress) {
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
		if isRemovalName(en) {
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
	e.RawRunArgs = len(splitLines(m.textValue(fRunArgs)))+len(lower.RunArgs) > 0
	e.RawBuild = len(splitLines(m.textValue(fDockerfilePre)))+len(splitLines(m.textValue(fDockerfilePost)))+
		len(lower.DockerfilePre)+len(lower.DockerfilePost) > 0
	return e
}

// rowCounts tallies a field's effective rows for the form summary line.
// Offered rows are counted separately: they are closed doors, not effective
// state.
func rowCounts(rows []listRow) (effective, inherited, fromSkills, offered int) {
	for _, r := range rows {
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
			// Host passthrough is effective env inherited from below this
			// file (byre's core layer at the deepest).
			effective++
			inherited++
		case rowOffered:
			offered++
		}
	}
	return
}

func isRemovalName(s string) bool { return strings.HasPrefix(s, "!") }

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

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// portKey is a port's effective identity (interface:host:container), matching
// mergePorts' dedup key.
func portKey(p config.Port) string {
	iface, host := config.PortEffective(p)
	return iface + ":" + strconv.Itoa(host) + ":" + strconv.Itoa(p.Container)
}
