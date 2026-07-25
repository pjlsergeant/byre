package configui

// reconcile applies the difference between a layer file's current content
// and the desired Config as targeted tomldoc edits -- the preservation
// half of Save (ADR 0044). Untouched fields produce NO edit, so their
// bytes (comments, hand formatting, exotic spellings) survive identically;
// a changed field rewrites only its own construct, which comes out in
// byre's house shape. On a fresh document the same walk emits the full
// house layout, so a new file is canonical by construction.
//
// Every toml-visible Config field must be handled here;
// TestReconcileCoversEveryField holds that line (the Merge guard's
// sibling). Cascade-internal fields (toml:"-") never appear in a layer
// file and are skipped.

import (
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/tomldoc"
)

func reconcile(doc *tomldoc.Doc, cur, want config.Config) error {
	// Scalars, in house order for a fresh file.
	scalars := []struct{ key, cur, want string }{
		{"engine", cur.Engine, want.Engine},
		{"template", cur.Template, want.Template},
		{"agent", cur.Agent, want.Agent},
		{"extends", cur.Extends, want.Extends},
		{"base", cur.Base, want.Base},
		{"worktree_base", cur.WorktreeBase, want.WorktreeBase},
	}
	for _, s := range scalars {
		if s.cur == s.want {
			continue
		}
		if s.want == "" {
			if err := doc.RemoveKey(nil, s.key); err != nil {
				return err
			}
			continue
		}
		if err := doc.SetKey(nil, s.key, tomldoc.String(s.want)); err != nil {
			return err
		}
	}

	// seed_prefs: tri-state (ADR 0045) — unset removes the key (inherit),
	// an explicit true or false is written as such.
	if !boolPtrEqual(cur.SeedPrefs, want.SeedPrefs) {
		if want.SeedPrefs == nil {
			if err := doc.RemoveKey(nil, "seed_prefs"); err != nil {
				return err
			}
		} else if err := doc.SetKey(nil, "seed_prefs", tomldoc.Bool(*want.SeedPrefs)); err != nil {
			return err
		}
	}

	// shared_auth: canonical inline value; a table-form spelling is
	// normalized only when the preference actually changed.
	if !cur.SharedAuth.Equal(want.SharedAuth) {
		if err := doc.RemoveTable([]string{"shared_auth"}); err != nil {
			return err
		}
		if want.SharedAuth.Empty() {
			if err := doc.RemoveKey(nil, "shared_auth"); err != nil {
				return err
			}
		} else if err := doc.SetKey(nil, "shared_auth", want.SharedAuth.EncodeTOMLValue()); err != nil {
			return err
		}
	}

	// Plain string lists (single-line house shape).
	lists := []struct {
		key       string
		cur, want []string
	}{
		{"apt", cur.Apt, want.Apt},
		{"npm_global", cur.NpmGlobal, want.NpmGlobal},
		{"skills", cur.Skills, want.Skills},
		{"egress", cur.Egress, want.Egress},
		{"egress_offered", cur.EgressOffered, want.EgressOffered},
	}
	for _, l := range lists {
		if err := reconcileList(doc, l.key, l.cur, l.want, tomldoc.StringArray); err != nil {
			return err
		}
	}

	// String maps ([env], [env_from_host], [files]).
	strMaps := []struct {
		key       string
		cur, want map[string]string
	}{
		{"env", cur.Env, want.Env},
		{"env_from_host", cur.EnvFromHost, want.EnvFromHost},
		{"files", cur.Files, want.Files},
	}
	for _, m := range strMaps {
		if err := reconcileStringMap(doc, m.key, m.cur, m.want); err != nil {
			return err
		}
	}

	// [sources]: id -> hint, inline-table values. From is resolve-time
	// attribution (toml:"-"), never in a layer file.
	if err := reconcileSources(doc, cur.Sources, want.Sources); err != nil {
		return err
	}

	// Structured blocks, replace/append/remove by identity.
	if err := reconcileBlocks(doc, "mounts", cur.Mounts, want.Mounts,
		func(m config.Mount) string { return m.Target }, renderMount); err != nil {
		return err
	}
	if err := reconcileBlocks(doc, "volumes", cur.Volumes, want.Volumes,
		func(v config.Volume) string { return v.Name }, renderVolume); err != nil {
		return err
	}
	if err := reconcileBlocks(doc, "ports", cur.Ports, want.Ports,
		func(p config.Port) string { return fmt.Sprintf("%d", p.Container) }, renderPort); err != nil {
		return err
	}
	if err := reconcileBlocks(doc, "mcp", cur.MCPs, want.MCPs,
		func(m config.MCP) string { return m.Name }, renderMCP); err != nil {
		return err
	}
	if err := reconcileBlocks(doc, "claude_skills", cur.ClaudeSkills, want.ClaudeSkills,
		func(cs config.ClaudeSkill) string { return cs.Name }, renderClaudeSkill); err != nil {
		return err
	}
	if err := reconcileBlocks(doc, "context", cur.Contexts, want.Contexts,
		func(cd config.ContextDecl) string { return cd.Name }, renderContext); err != nil {
		return err
	}

	// Raw blocks: element-per-line house shape (long commands).
	raw := []struct {
		key       string
		cur, want []string
	}{
		{"dockerfile_pre", cur.DockerfilePre, want.DockerfilePre},
		{"dockerfile_post", cur.DockerfilePost, want.DockerfilePost},
		{"run_args", cur.RunArgs, want.RunArgs},
	}
	for _, l := range raw {
		if err := reconcileList(doc, l.key, l.cur, l.want, tomldoc.Lines); err != nil {
			return err
		}
	}
	return nil
}

// blockIdentity names each [[block]] vocabulary's identity key -- the field
// reconcileBlocks matches on in the document.
var blockIdentity = map[string]string{
	"mounts":        "target",
	"volumes":       "name",
	"ports":         "container",
	"mcp":           "name",
	"claude_skills": "name",
	"context":       "name",
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func reconcileList(doc *tomldoc.Doc, key string, cur, want []string, render func([]string) string) error {
	if slices.Equal(cur, want) {
		return nil
	}
	if len(want) == 0 {
		return doc.RemoveKey(nil, key)
	}
	return doc.SetKey(nil, key, render(want))
}

func reconcileStringMap(doc *tomldoc.Doc, key string, cur, want map[string]string) error {
	if maps.Equal(cur, want) {
		return nil
	}
	if len(want) == 0 {
		// Every spelling covered: the inline `key = {...}` form, then each
		// entry by full path (matches [table] and root-dotted spellings
		// alike), then a now-empty [table] header if one exists.
		if err := doc.RemoveKey(nil, key); err != nil {
			return err
		}
		for k := range cur {
			if err := doc.RemoveKey([]string{key}, k); err != nil {
				return err
			}
		}
		return doc.RemoveTable([]string{key})
	}
	// An inline `key = { ... }` spelling is one construct: a change to the
	// map rewrites it whole (the approved normalization boundary).
	if doc.HasKey(nil, key) {
		return doc.SetKey(nil, key, tomldoc.InlineStringMap(want))
	}
	// Sorted, so a fresh file's layout is canonical and identical configs
	// emit identical bytes (Go map order is unspecified).
	for _, k := range slices.Sorted(maps.Keys(want)) {
		v := want[k]
		if cur[k] != v || !doc.HasKey([]string{key}, k) {
			if err := doc.SetKey([]string{key}, k, tomldoc.String(v)); err != nil {
				return err
			}
		}
	}
	for k := range cur {
		if _, ok := want[k]; !ok {
			if err := doc.RemoveKey([]string{key}, k); err != nil {
				return err
			}
		}
	}
	return nil
}

func reconcileSources(doc *tomldoc.Doc, cur, want map[string]config.SourceHint) error {
	equal := len(cur) == len(want)
	if equal {
		for id, h := range want {
			c, ok := cur[id]
			if !ok || c.URI != h.URI || c.Digest != h.Digest {
				equal = false
				break
			}
		}
	}
	if equal {
		return nil
	}
	// A root-inline `sources = { ... }` spelling is one construct: any
	// change rewrites the whole map in house shape ([sources] + inline
	// values). Grok review find 2026-07-25: the per-id path silently missed
	// this and the subtable spelling below.
	if doc.HasKey(nil, "sources") {
		if err := doc.RemoveKey(nil, "sources"); err != nil {
			return err
		}
		cur = nil
	}
	for _, id := range slices.Sorted(maps.Keys(want)) {
		h := want[id]
		c, ok := cur[id]
		if ok && c.URI == h.URI && c.Digest == h.Digest && doc.HasKey([]string{"sources"}, id) {
			continue
		}
		// A `[sources."id"]` subtable spelling is that entry's construct:
		// changing the entry normalizes it to the house inline value. It
		// must go BEFORE the set, or the insert would collide with the
		// still-open subtable.
		if err := doc.RemoveTable([]string{"sources", id}); err != nil {
			return err
		}
		v := map[string]string{"uri": h.URI}
		if h.Digest != "" {
			v["digest"] = h.Digest
		}
		if err := doc.SetKey([]string{"sources"}, id, tomldoc.InlineStringMap(v)); err != nil {
			return err
		}
	}
	for id := range cur {
		if _, ok := want[id]; !ok {
			if err := doc.RemoveTable([]string{"sources", id}); err != nil {
				return err
			}
			if err := doc.RemoveKey([]string{"sources"}, id); err != nil {
				return err
			}
		}
	}
	if len(want) == 0 {
		return doc.RemoveTable([]string{"sources"})
	}
	return nil
}

// reconcileBlocks diffs two identity-keyed block lists: an unchanged entry
// makes no edit, a changed one is replaced in house shape, a new one appends
// after its kin, a dropped one is removed with its glued comments. An inline
// `key = [ {...} ]` spelling of the whole list is one construct and is
// normalized to blocks when anything in the list changes.
func reconcileBlocks[T any](doc *tomldoc.Doc, name string, cur, want []T, id func(T) string, render func(T) string) error {
	if reflect.DeepEqual(cur, want) {
		return nil
	}
	matchKey := blockIdentity[name]
	if doc.HasKey(nil, name) { // inline-array spelling: rewrite whole construct
		if err := doc.RemoveKey(nil, name); err != nil {
			return err
		}
		cur = nil
	}
	curBy := map[string]T{}
	for _, c := range cur {
		curBy[id(c)] = c
	}
	seen := map[string]bool{}
	for _, w := range want {
		wid := id(w)
		seen[wid] = true
		c, ok := curBy[wid]
		if ok && reflect.DeepEqual(c, w) {
			continue
		}
		if ok {
			replaced, err := doc.ReplaceArrayTable(name, matchKey, wid, render(w))
			if err != nil {
				return err
			}
			if replaced {
				continue
			}
		}
		if err := doc.AppendArrayTable(name, render(w)); err != nil {
			return err
		}
	}
	for _, c := range cur {
		if !seen[id(c)] {
			if _, err := doc.RemoveArrayTable(name, matchKey, id(c)); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- block body renderers (house shapes) -----------------------------------

func renderMount(m config.Mount) string {
	b := tomldoc.KV("target", tomldoc.String(m.Target))
	if m.Host != "" {
		b += tomldoc.KV("host", tomldoc.String(m.Host))
	}
	if m.Mode != "" {
		b += tomldoc.KV("mode", tomldoc.String(m.Mode))
	}
	if m.Disabled {
		b += tomldoc.KV("disabled", tomldoc.Bool(true))
	}
	return b
}

func renderVolume(v config.Volume) string {
	b := tomldoc.KV("name", tomldoc.String(v.Name))
	if v.Role != "" {
		b += tomldoc.KV("role", tomldoc.String(v.Role))
	}
	if v.Target != "" {
		b += tomldoc.KV("target", tomldoc.String(v.Target))
	}
	if v.Scope != "" {
		b += tomldoc.KV("scope", tomldoc.String(v.Scope))
	}
	if v.Seed != nil {
		s := map[string]string{}
		if v.Seed.Host != "" {
			s["host"] = v.Seed.Host
		}
		if v.Seed.Literal != "" {
			s["literal"] = v.Seed.Literal
		}
		if v.Seed.Path != "" {
			s["path"] = v.Seed.Path
		}
		b += tomldoc.KV("seed", tomldoc.InlineStringMap(s))
	}
	return b
}

func renderPort(p config.Port) string {
	b := tomldoc.KV("container", tomldoc.Int(p.Container))
	if p.Host != 0 {
		b += tomldoc.KV("host", tomldoc.Int(p.Host))
	}
	if p.Interface != "" {
		b += tomldoc.KV("interface", tomldoc.String(p.Interface))
	}
	if p.Remove {
		b += tomldoc.KV("remove", tomldoc.Bool(true))
	}
	return b
}

func renderMCP(m config.MCP) string {
	b := tomldoc.KV("name", tomldoc.String(m.Name))
	if len(m.Command) > 0 {
		b += tomldoc.KV("command", tomldoc.StringArray(m.Command))
	}
	if m.URL != "" {
		b += tomldoc.KV("url", tomldoc.String(m.URL))
	}
	if len(m.Env) > 0 {
		b += tomldoc.KV("env", tomldoc.StringArray(m.Env))
	}
	if len(m.Egress) > 0 {
		b += tomldoc.KV("egress", tomldoc.StringArray(m.Egress))
	}
	if len(m.Headers) > 0 {
		b += tomldoc.KV("headers", tomldoc.InlineStringMap(m.Headers))
	}
	return b
}

func renderClaudeSkill(cs config.ClaudeSkill) string {
	b := tomldoc.KV("name", tomldoc.String(cs.Name))
	if cs.Path != "" {
		b += tomldoc.KV("path", tomldoc.String(cs.Path))
	}
	if cs.From != "" {
		b += tomldoc.KV("from", tomldoc.String(cs.From))
	}
	return b
}

func renderContext(cd config.ContextDecl) string {
	b := tomldoc.KV("name", tomldoc.String(cd.Name))
	if cd.Text != "" {
		b += tomldoc.KV("text", tomldoc.String(cd.Text))
	}
	if cd.File != "" {
		b += tomldoc.KV("file", tomldoc.String(cd.File))
	}
	return b
}
