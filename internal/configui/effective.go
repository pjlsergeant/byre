// effective.go builds the list screens' EFFECTIVE rows (ADR 0018): the merged
// view of lower layers, this layer, and skill contributions, each row
// attributed to its source. Rendering and interaction live in listitem.go;
// this file is pure projection from the model's working state.
package configui

import (
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
)

// listRow is one row of a list screen's effective view. idx points into the
// field's LOCAL backing slice (the entry or marker this layer owns); -1 for
// rows this layer doesn't own (inherited, skill).
type listRow struct {
	kind   rowKind
	text   string   // display form of the value
	ident  string   // removal identity: package, env key, mount target, container port
	source string   // "default", "template:go", "skill:x"; "" for pure local
	also   bool     // local entry duplicating an inherited one (union dedups)
	idx    int      // index into the local slice, or -1
	vals   []string // inherited raw values, for prefilling an override editor
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
	}
	return nil
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
		rows = append(rows, listRow{kind: rowLocal, text: p, idx: i})
	}
	for i, p := range m.apt {
		if n, ok := strings.CutPrefix(p, "!"); ok && !lower[n] {
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
			rows = append(rows, listRow{kind: rowOverride, text: mountLine(m.mounts[localIdx[t]]), source: src, idx: localIdx[t]})
		default:
			mode := mt.Mode
			if mode == "" {
				mode = "ro"
			}
			if mt.Disabled {
				mode = "disabled"
			}
			rows = append(rows, listRow{kind: rowInherited, text: mountLine(mt), ident: mt.Target, source: src, vals: []string{mt.Host, mt.Target, mode}})
		}
	}
	for i, mt := range m.mounts {
		if isRemovalName(mt.Target) || lower[mt.Target] {
			continue
		}
		rows = append(rows, listRow{kind: rowLocal, text: mountLine(mt), idx: i})
	}
	for i, mt := range m.mounts {
		if n, ok := strings.CutPrefix(mt.Target, "!"); ok && !lower[n] {
			rows = append(rows, listRow{kind: rowStaleMarker, text: n, idx: i})
		}
	}
	for _, sk := range m.effectiveSkills() {
		for _, mt := range m.inh.Skills[sk].Mounts {
			rows = append(rows, listRow{kind: rowSkill, text: mountLine(mt), source: "skill:" + sk})
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
		src := m.lowerSource(func(cf config.Config) bool { return hasContainerPort(cf.Ports, c) })
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
	for i, p := range m.ports {
		if p.Remove || lowerKeys[portKey(p)] {
			continue
		}
		rows = append(rows, listRow{kind: rowLocal, text: portLine(p), idx: i})
	}
	for i, p := range m.ports {
		if p.Remove && !lowerByContainer[p.Container] {
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
		if rt, ok := m.inh.Skills[e.name]; ok && (len(rt.Mounts) > 0 || len(rt.Env) > 0) {
			out = append(out, e.name)
		}
	}
	sort.Strings(out)
	return out
}

// rowCounts tallies a field's effective rows for the form summary line.
func rowCounts(rows []listRow) (effective, inherited, fromSkills int) {
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

func hasContainerPort(ps []config.Port, container int) bool {
	for _, p := range ps {
		if !p.Remove && p.Container == container {
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
	iface := p.Interface
	if iface == "" {
		iface = "127.0.0.1"
	}
	host := p.Host
	if host == 0 {
		host = p.Container
	}
	return iface + ":" + strconv.Itoa(host) + ":" + strconv.Itoa(p.Container)
}
