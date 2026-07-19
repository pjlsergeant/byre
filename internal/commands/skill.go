package commands

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// SkillList prints catalog rows for skills.
func SkillList(s Streams) error { return pkgList(s, packages.KindSkill) }

// TemplateList prints catalog rows for templates.
func TemplateList(s Streams) error { return pkgList(s, packages.KindTemplate) }

func pkgList(s Streams, kind packages.Kind) error {
	home, err := project.Home()
	if err != nil {
		return err
	}
	if err := builtins.EnsureStoreOut(home, s.Err); err != nil {
		return err
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		return err
	}
	for _, ent := range cat.List(kind) {
		id := packages.EscapeTerminal(ent.ID)
		if ent.Alias != "" {
			id = packages.EscapeTerminal(ent.Alias) + " (" + packages.EscapeTerminal(ent.ID) + ")"
		}
		label := packages.EscapeTerminal(ent.ProvenanceLabel())
		desc := packages.EscapeTerminal(ent.Description)
		switch ent.Provenance {
		case packages.ProvInvalid, packages.ProvConflict, packages.ProvLegacy:
			fmt.Fprintf(s.Out, "%-28s  %-16s  %s\n", id, label, packages.EscapeTerminal(ent.Reason))
		default:
			if desc != "" {
				fmt.Fprintf(s.Out, "%-28s  %-16s  %s\n", id, label, desc)
			} else {
				fmt.Fprintf(s.Out, "%-28s  %s\n", id, label)
			}
		}
	}
	return nil
}

// SkillInspect prints package metadata for a skill ID (phase 1: IDs only).
func SkillInspect(s Streams, id string) error {
	return pkgInspect(s, packages.KindSkill, id)
}

// TemplateInspect prints package metadata for a template ID.
func TemplateInspect(s Streams, id string) error {
	return pkgInspect(s, packages.KindTemplate, id)
}

func pkgInspect(s Streams, kind packages.Kind, id string) error {
	home, err := project.Home()
	if err != nil {
		return err
	}
	if err := builtins.EnsureStoreOut(home, s.Err); err != nil {
		return err
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		return err
	}
	ent, ok := cat.Lookup(id)
	if !ok {
		// Not a catalog ID: a URI/path inspects the remote manifest without
		// installing anything. IDs always win -- this is only reached
		// for names the catalog does not know.
		if looksLikeURI(id) {
			return inspectURI(s, kind, id)
		}
		if _, err := cat.ResolveName(id); err != nil {
			return err
		}
		return fmt.Errorf("package %q not found", id)
	}
	if ent.Kind != kind && ent.Kind != "" {
		return fmt.Errorf("package %q is a %s; use `byre %s inspect`", ent.ID, ent.Kind, ent.Kind)
	}
	fmt.Fprintf(s.Out, "ID:          %s\n", packages.EscapeTerminal(ent.ID))
	if ent.Alias != "" {
		fmt.Fprintf(s.Out, "Alias:       %s\n", packages.EscapeTerminal(ent.Alias))
	}
	fmt.Fprintf(s.Out, "Kind:        %s\n", ent.Kind)
	fmt.Fprintf(s.Out, "Version:     %s\n", packages.EscapeTerminal(ent.Version))
	switch ent.Provenance {
	case packages.ProvInstalled:
		fmt.Fprintf(s.Out, "Digest:      sha256:%s\n", packages.EscapeTerminal(ent.Digest))
		if ent.SourceURI != "" {
			// Provenance of acquisition, never an instruction byre follows.
			fmt.Fprintf(s.Out, "Acquired:    %s\n", packages.EscapeTerminal(ent.SourceURI))
		}
	case packages.ProvBundled:
		// Display digest computed from the embedded bytes (ADR 0029): inspect
		// parity with installed rows, never an integrity claim.
		if d, err := packages.DisplayDigest(ent); err == nil {
			fmt.Fprintf(s.Out, "Digest:      sha256:%s\n", d)
		} else {
			// Our own embedded bytes failing to digest is a byre bug; degrade
			// the claim loudly rather than blocking inspect.
			fmt.Fprintf(s.Err, "byre: display digest unavailable for %s: %v\n", packages.EscapeTerminal(ent.ID), err)
		}
	}
	fmt.Fprintf(s.Out, "Provenance:  %s\n", packages.EscapeTerminal(ent.ProvenanceLabel()))
	if ent.Description != "" {
		fmt.Fprintf(s.Out, "Description: %s\n", packages.EscapeTerminal(ent.Description))
	}
	if ent.Reason != "" {
		fmt.Fprintf(s.Out, "Status:      %s\n", packages.EscapeTerminal(ent.Reason))
	}
	switch {
	case kind == packages.KindSkill && (ent.Provenance == packages.ProvBundled || ent.Provenance == packages.ProvLocal || ent.Provenance == packages.ProvInstalled):
		if sk, err := skills.Load(cat, ent.ID); err == nil {
			printSkillInspect(s.Out, sk)
		}
	case kind == packages.KindTemplate && (ent.Provenance == packages.ProvBundled || ent.Provenance == packages.ProvLocal || ent.Provenance == packages.ProvInstalled):
		printTemplateInspect(s.Out, ent)
	}
	// Source path for full review: local dir or ~/.byre/bundled mirror.
	srcPath := inspectSourcePath(home, ent)
	if srcPath != "" {
		fmt.Fprintf(s.Out, "\nSource: %s\n", srcPath)
	}
	if ent.Provenance == packages.ProvBundled || ent.Provenance == packages.ProvInstalled {
		fmt.Fprintln(s.Out, "This package is immutable. To edit: byre", kind, "fork", ent.DisplayName(), "<new-id>")
	}
	return nil
}

func inspectSourcePath(home string, ent *packages.Entry) string {
	if ent.Dir != "" {
		return ent.Dir
	}
	if ent.Provenance == packages.ProvBundled && ent.Sub != "" {
		return filepath.Join(home, "bundled", filepath.FromSlash(ent.Sub))
	}
	return ""
}

// printSkillInspect renders the full pre-trust contribution set: structured
// grants one line each; freeform build as counts + names, not inline dumps.
func printSkillInspect(w io.Writer, sk skills.Skill) {
	printSkillContributions(w, sk.File)
}

// printSkillContributions is printSkillInspect over the declared schema alone
// -- install's grant summary renders a manifest that has no loaded Skill yet.
func printSkillContributions(w io.Writer, f skills.File) {
	rt := f.Runtime
	fmt.Fprintln(w, "\nContributions:")
	if f.Agent != nil && f.Agent.Command != "" {
		fmt.Fprintf(w, "  agent command: %s\n", packages.EscapeTerminal(f.Agent.Command))
		if f.Agent.State != "" {
			fmt.Fprintf(w, "  agent state:   %s\n", packages.EscapeTerminal(f.Agent.State))
		}
	}
	for _, m := range rt.Mounts {
		mode := m.Mode
		if mode == "" {
			mode = "ro"
		}
		if m.Disabled {
			mode += ", disabled"
		}
		fmt.Fprintf(w, "  mount: %s -> %s (%s)\n", packages.EscapeTerminal(m.Host), packages.EscapeTerminal(m.Target), mode)
	}
	for _, v := range f.Volumes {
		scope := v.Scope
		if scope == "" {
			scope = "project"
		}
		fmt.Fprintf(w, "  volume: %s (%s, %s) -> %s\n", packages.EscapeTerminal(v.Name), v.Role, scope, packages.EscapeTerminal(v.Target))
	}
	for _, c := range rt.Caps {
		fmt.Fprintf(w, "  cap: %s\n", packages.EscapeTerminal(c))
	}
	for _, a := range rt.RunArgs {
		fmt.Fprintf(w, "  run_arg: %s\n", packages.EscapeTerminal(a))
	}
	if rt.NetnsInit != "" {
		fmt.Fprintf(w, "  netns_init: %s\n", packages.EscapeTerminal(rt.NetnsInit))
	}
	if rt.NetworkPosture != "" {
		fmt.Fprintf(w, "  network_posture: %s\n", packages.EscapeTerminal(rt.NetworkPosture))
	}
	for _, p := range rt.SockGroups {
		fmt.Fprintf(w, "  sock_groups: %s\n", packages.EscapeTerminal(p))
	}
	if rt.Containment != "" {
		fmt.Fprintf(w, "  containment: %s\n", packages.EscapeTerminal(rt.Containment))
	}
	for _, e := range rt.Egress {
		fmt.Fprintf(w, "  egress: %s\n", packages.EscapeTerminal(e))
	}
	for _, e := range rt.EgressOffered {
		fmt.Fprintf(w, "  egress_offered: %s\n", packages.EscapeTerminal(e))
	}
	// MCP declarations: wiring, but part of the trust surface — a remote URL
	// implies egress and the env list names what the server will consume.
	for _, m := range f.MCPs {
		if m.Remote() {
			fmt.Fprintf(w, "  mcp: %s (remote: %s)\n", packages.EscapeTerminal(m.Name), packages.EscapeTerminal(m.URL))
		} else {
			fmt.Fprintf(w, "  mcp: %s (local: %s)\n", packages.EscapeTerminal(m.Name), packages.EscapeTerminal(strings.Join(m.Command, " ")))
		}
		for _, k := range m.Env {
			fmt.Fprintf(w, "    consumes env: %s\n", packages.EscapeTerminal(k))
		}
		for _, e := range m.Egress {
			fmt.Fprintf(w, "    egress: %s\n", packages.EscapeTerminal(e))
		}
		// Headers with VALUES: inspect is the pre-enable trust surface, and a
		// template (or a literal a manifest smuggles) is exactly what the
		// reviewer must see.
		for _, k := range m.HeaderNames() {
			fmt.Fprintf(w, "    header: %s: %s\n", packages.EscapeTerminal(k), packages.EscapeTerminal(m.Headers[k]))
		}
	}
	for _, k := range sortedMapKeys(rt.Env) {
		fmt.Fprintf(w, "  env: %s=%s\n", packages.EscapeTerminal(k), packages.EscapeTerminal(rt.Env[k]))
	}
	for _, k := range sortedMapKeys(rt.EnvDocs) {
		fmt.Fprintf(w, "  env consumed: %s -- %s\n", packages.EscapeTerminal(k), packages.EscapeTerminal(rt.EnvDocs[k]))
	}
	if f.CompanionFor != "" {
		fmt.Fprintf(w, "  companion_for: %s\n", packages.EscapeTerminal(f.CompanionFor))
	}
	if f.SharedAuthFor != "" {
		fmt.Fprintf(w, "  shared_auth_for: %s\n", packages.EscapeTerminal(f.SharedAuthFor))
	}
	// Build summary: counts + names, not inline dumps.
	var buildParts []string
	if n := len(f.Build.Apt); n > 0 {
		buildParts = append(buildParts, fmt.Sprintf("%d apt", n))
	}
	if n := len(f.Build.NpmGlobal); n > 0 {
		buildParts = append(buildParts, fmt.Sprintf("%d npm_global", n))
	}
	if n := len(f.Build.Dockerfile); n > 0 {
		buildParts = append(buildParts, fmt.Sprintf("%d dockerfile lines", n))
	}
	if len(buildParts) > 0 {
		fmt.Fprintf(w, "  build: %s\n", strings.Join(buildParts, ", "))
	}
	if n := len(f.Build.Files); n > 0 {
		names := sortedMapKeys(f.Build.Files)
		shown := names
		if len(shown) > 8 {
			shown = append(shown[:8], "...")
		}
		fmt.Fprintf(w, "  files: %d (%s)\n", n, strings.Join(shown, ", "))
	}
	if f.Context.Text != "" || f.Context.File != "" {
		src := "inline"
		if f.Context.File != "" {
			src = f.Context.File
		}
		fmt.Fprintf(w, "  context: present (%s)\n", packages.EscapeTerminal(src))
	}
}

func printTemplateInspect(w io.Writer, ent *packages.Entry) {
	raw, err := ent.ReadPrimary()
	if err != nil {
		return
	}
	printTemplateShape(w, raw)
}

// printTemplateShape renders a template's shape keys from primary bytes --
// shared by inspect and install's grant summary.
func printTemplateShape(w io.Writer, raw []byte) {
	cfg, err := config.ParseTemplateBody(raw)
	if err != nil {
		// Still show what we can from a lenient strip+parse for broken templates.
		body := packages.StripPackageTable(raw)
		cfg, _ = config.Parse(body)
	}
	fmt.Fprintln(w, "\nShape:")
	if cfg.Base != "" {
		fmt.Fprintf(w, "  base: %s\n", packages.EscapeTerminal(cfg.Base))
	}
	if cfg.Engine != "" {
		fmt.Fprintf(w, "  engine: %s\n", packages.EscapeTerminal(cfg.Engine))
	}
	// Templates are cascade LAYERS: `!name` entries, `target = "!x"` mounts,
	// and `remove = true` ports subtract from lower layers. Render them as
	// removals, never as grants — the trust surface must agree with the merge.
	for _, a := range cfg.Apt {
		fmt.Fprintf(w, "  %s\n", listLine("apt", a))
	}
	for _, a := range cfg.NpmGlobal {
		fmt.Fprintf(w, "  %s\n", listLine("npm_global", a))
	}
	for _, e := range cfg.EgressOffered {
		fmt.Fprintf(w, "  %s\n", listLine("egress_offered", e))
	}
	for _, e := range cfg.Egress {
		fmt.Fprintf(w, "  %s\n", listLine("egress", e))
	}
	for _, m := range cfg.Mounts {
		if name, ok := strings.CutPrefix(m.Target, "!"); ok && name != "" {
			fmt.Fprintf(w, "  removes mount: %s\n", packages.EscapeTerminal(name))
			continue
		}
		mode := m.Mode
		if mode == "" {
			mode = "ro"
		}
		if m.Disabled {
			mode += ", disabled"
		}
		fmt.Fprintf(w, "  mount: %s -> %s (%s)\n", packages.EscapeTerminal(m.Host), packages.EscapeTerminal(m.Target), mode)
	}
	for _, v := range cfg.Volumes {
		if name, ok := strings.CutPrefix(v.Name, "!"); ok && name != "" {
			fmt.Fprintf(w, "  removes volume: %s\n", packages.EscapeTerminal(name))
			continue
		}
		scope := v.Scope
		if scope == "" {
			scope = "project"
		}
		line := fmt.Sprintf("  volume: %s (%s, %s) -> %s", packages.EscapeTerminal(v.Name), v.Role, scope, packages.EscapeTerminal(v.Target))
		if v.Seed != nil {
			if v.Seed.Host != "" {
				line += fmt.Sprintf(" [seed host=%s]", packages.EscapeTerminal(v.Seed.Host))
			} else if v.Seed.Literal != "" {
				line += " [seed literal]"
			}
		}
		fmt.Fprintln(w, line)
	}
	for _, p := range cfg.Ports {
		if p.Remove {
			fmt.Fprintf(w, "  removes port: container %d\n", p.Container)
			continue
		}
		iface, host := config.PortEffective(p)
		fmt.Fprintf(w, "  port: %s:%d -> container %d\n", packages.EscapeTerminal(iface), host, p.Container)
	}
	for _, k := range sortedMapKeys(cfg.Env) {
		fmt.Fprintf(w, "  env: %s=%s\n", packages.EscapeTerminal(k), packages.EscapeTerminal(cfg.Env[k]))
	}
	for _, k := range sortedMapKeys(cfg.EnvFromHost) {
		fmt.Fprintf(w, "  env_from_host: %s <- %s\n", packages.EscapeTerminal(k), packages.EscapeTerminal(cfg.EnvFromHost[k]))
	}
	if n := len(cfg.RunArgs); n > 0 {
		fmt.Fprintf(w, "  run_args: %d (raw docker flags)\n", n)
	}
	if n := len(cfg.Files); n > 0 {
		names := sortedMapKeys(cfg.Files)
		shown := names
		if len(shown) > 8 {
			shown = append(shown[:8], "...")
		}
		fmt.Fprintf(w, "  files: %d (%s)\n", n, strings.Join(shown, ", "))
	}
	if n := len(cfg.DockerfilePre) + len(cfg.DockerfilePost); n > 0 {
		fmt.Fprintf(w, "  dockerfile lines: %d (pre+post)\n", n)
	}
	if cfg.WorktreeBase != "" {
		fmt.Fprintf(w, "  worktree_base: %s\n", packages.EscapeTerminal(cfg.WorktreeBase))
	}
	if cfg.SeedPrefs {
		fmt.Fprintln(w, "  seed_prefs: true")
	}
}

// listLine renders one string-list entry, showing `!name` cascade markers as
// removals instead of grants.
func listLine(key, val string) string {
	if name, ok := strings.CutPrefix(val, "!"); ok && name != "" {
		return fmt.Sprintf("removes %s: %s", key, packages.EscapeTerminal(name))
	}
	return fmt.Sprintf("%s: %s", key, packages.EscapeTerminal(val))
}

func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SkillFork copies an immutable skill into the local store under newID.
func SkillFork(s Streams, id, newID string) error {
	return pkgFork(s, packages.KindSkill, id, newID)
}

// TemplateFork copies an immutable template into the local store under newID.
func TemplateFork(s Streams, id, newID string) error {
	return pkgFork(s, packages.KindTemplate, id, newID)
}

func pkgFork(s Streams, kind packages.Kind, id, newID string) error {
	home, err := project.Home()
	if err != nil {
		return err
	}
	if err := builtins.EnsureStoreOut(home, s.Err); err != nil {
		return err
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		return err
	}
	src, err := cat.ResolveName(id)
	if err != nil {
		return err
	}
	if src.Kind != kind {
		return fmt.Errorf("package %q is a %s; use `byre %s fork`", src.ID, src.Kind, src.Kind)
	}
	if err := packages.ValidateID(newID, true); err != nil {
		return fmt.Errorf("new id: %w", err)
	}
	if packages.IsBare(newID) && cat.IsProtected(newID) {
		return fmt.Errorf("%q is protected; pick a different id (e.g. yourname/%s)", newID, packages.BareName(newID))
	}
	if packages.Owner(newID) == "byre" {
		return fmt.Errorf("byre/* is reserved for bundled packages")
	}
	if _, ok := cat.Lookup(newID); ok {
		return fmt.Errorf("package %q already exists in the catalog", newID)
	}

	sub := "skills"
	prim := "skill.toml"
	if kind == packages.KindTemplate {
		sub = "templates"
		prim = "template.config"
	}
	destDir := filepath.Join(home, sub, filepath.FromSlash(newID))
	if _, err := os.Stat(destDir); err == nil {
		return fmt.Errorf("%s already exists", destDir)
	}

	hostSrc, err := src.HostDir()
	if err != nil {
		return err
	}
	// Stage the whole fork beside the destination and publish with one
	// rename at the end. Copying into the final name meant any expected
	// failure (FIFO refusal, payload budget, unreadable source, a primary
	// that won't rewrite) left a partial tree there — poisoning the retry
	// with "already exists" and, when the failure landed before the
	// rewrite, carrying the SOURCE package's identity under the fork's
	// path. The stage dir is removed on every failure path.
	parent := filepath.Dir(destDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".fork-stage-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage) // no-op once the publish rename succeeds
	if err := copyDir(hostSrc, stage); err != nil {
		return err
	}

	// Provenance comment at the top of the primary file — rewritten in
	// staging, so a published fork always carries the fork's identity.
	primPath := filepath.Join(stage, prim)
	body, err := os.ReadFile(primPath)
	if err != nil {
		return err
	}
	// Strip any existing [package] and write a local-style primary with a
	// provenance comment + declared id.
	body = packages.StripPackageTable(body)
	header := fmt.Sprintf(
		"# Forked from %s@%s\n# Informational only: byre never reads this for resolution, updates, or trust.\n\n[package]\nid = %q\nkind = %q\n\n",
		src.ID, src.Version, newID, kind,
	)
	if err := os.WriteFile(primPath, append([]byte(header), body...), 0o644); err != nil {
		return err
	}
	// Publish. Rename refuses an existing destination directory, so a
	// concurrent fork that won the race is not replaced.
	if err := os.Rename(stage, destDir); err != nil {
		return fmt.Errorf("publishing the fork: %w", err)
	}

	fmt.Fprintf(s.Err, "byre: forked %s -> %s\n", src.ID, destDir)
	key := "skills"
	if kind == packages.KindTemplate {
		key = "template"
	}
	if kind == packages.KindTemplate {
		fmt.Fprintf(s.Err, "      To use it: set template = %q in your byre.config\n", newID)
	} else {
		fmt.Fprintf(s.Err, "      To use it: add %q to %s (or set agent = %q) in your byre.config\n", newID, key, newID)
	}
	// Companion note when forking an agent skill.
	if kind == packages.KindSkill {
		if sk, err := skills.Load(cat, src.ID); err == nil && sk.File.Agent != nil {
			fmt.Fprintln(s.Err, "      Note: a fork of an agent does not bring its shared-auth companion.")
			fmt.Fprintln(s.Err, "      Fork the companion too (and set shared_auth_for) if the fork needs shared credentials.")
		}
		// Machine-volume warning.
		if sk, err := skills.Load(cat, src.ID); err == nil {
			for _, v := range sk.File.Volumes {
				if v.MachineScoped() {
					fmt.Fprintf(s.Err, "      Warning: volume %q is machine-scoped — the fork still names the same volume\n", v.Name)
					fmt.Fprintln(s.Err, "      (same credentials/identity) until you rename it.")
					break
				}
			}
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	// The walk rides one pinned root descriptor and every read is judged at
	// the descriptor (regular file only), so a FIFO or device fails the
	// fork loudly instead of hanging the copy. A symlink is the user's own
	// arrangement of their store: it is followed, and its resolved target's
	// bytes are materialized as a regular file (which is also the only
	// shape pack accepts later). One budget across the copy, aligned with
	// install's payload budget, so a growing or enormous file fails loudly
	// instead of exhausting memory.
	root, err := os.OpenRoot(src)
	if err != nil {
		return err
	}
	defer root.Close()
	remaining := int64(packages.MaxPayloadTotal)
	return fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		out := filepath.Join(dst, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		var fh *os.File
		var fi os.FileInfo
		if d.Type()&fs.ModeSymlink != 0 {
			// Out-of-tree targets are legitimate here, so the link is
			// resolved outside the root — by full path, follow=true.
			fh, fi, err = hostopen.OpenRegular(filepath.Join(src, filepath.FromSlash(rel)), true)
		} else {
			fh, fi, err = hostopen.OpenRegularIn(root, filepath.FromSlash(rel))
		}
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(fh, remaining+1))
		fh.Close()
		if err != nil {
			return err
		}
		remaining -= int64(len(b))
		if remaining < 0 {
			return fmt.Errorf("fork exceeds the %d-byte budget", packages.MaxPayloadTotal)
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, b, fi.Mode().Perm())
	})
}

// SkillInit scaffolds a new local skill.
func SkillInit(s Streams, name string) error {
	return pkgInit(s, packages.KindSkill, name)
}

// TemplateInit scaffolds a new local template.
func TemplateInit(s Streams, name string) error {
	return pkgInit(s, packages.KindTemplate, name)
}

func pkgInit(s Streams, kind packages.Kind, name string) error {
	if err := packages.ValidateID(name, true); err != nil {
		return err
	}
	home, err := project.Home()
	if err != nil {
		return err
	}
	if err := builtins.EnsureStoreOut(home, s.Err); err != nil {
		return err
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		return err
	}
	if packages.IsBare(name) && cat.IsProtected(name) {
		return fmt.Errorf("%q is protected; pick a different name", name)
	}
	if packages.Owner(name) == "byre" {
		return fmt.Errorf("byre/* is reserved for bundled packages")
	}
	sub, prim := "skills", "skill.toml"
	example := skillInitExample(name)
	if kind == packages.KindTemplate {
		sub, prim = "templates", "template.config"
		example = templateInitExample(name)
	}
	dir := filepath.Join(home, sub, filepath.FromSlash(name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, prim)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if err := os.WriteFile(path, []byte(example), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(s.Err, "byre: created %s\n", path)
	return nil
}

func skillInitExample(id string) string {
	return fmt.Sprintf(`# Local skill scaffold. [package] is optional for local packages (id
# defaults to the store path). Uncomment and edit.

[package]
id = %q
kind = "skill"
description = "TODO: one-line summary"

# [build]
# apt = []
# dockerfile = []

# [runtime]
# env = {}
# env_docs = {}   # vars the skill CONSUMES: NAME = "one-line guidance"

# MCP servers this skill wires into the box (names only in env — values
# arrive via env_from_host/[env]; a remote url implies attributed egress).
# [[mcp]]
# name = "github"
# command = ["github-mcp-server", "stdio"]
# env = ["GITHUB_TOKEN"]

# [context]
# text = """
# Workflow notes for the agent.
# """
`, id)
}

func templateInitExample(id string) string {
	return fmt.Sprintf(`# Local template scaffold. Templates are SHAPE only — no skills, agent, or
# [sources] (composition belongs in a preset).

[package]
id = %q
kind = "template"
description = "TODO: one-line summary"

base = "debian:bookworm-slim"
# egress_offered = []
`, id)
}

// SkillValidate two-stage-parses and resolve-checks a skill.
func SkillValidate(s Streams, name string) error {
	return pkgValidate(s, packages.KindSkill, name)
}

// TemplateValidate two-stage-parses a template.
func TemplateValidate(s Streams, name string) error {
	return pkgValidate(s, packages.KindTemplate, name)
}

func pkgValidate(s Streams, kind packages.Kind, name string) error {
	home, err := project.Home()
	if err != nil {
		return err
	}
	if err := builtins.EnsureStoreOut(home, s.Err); err != nil {
		return err
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		return err
	}
	if name == "" {
		// Validate every loadable package of this kind.
		var n int
		for _, ent := range cat.ListLoadable(kind) {
			if err := validateOne(s, cat, ent); err != nil {
				return err
			}
			n++
		}
		fmt.Fprintf(s.Err, "byre: %d %s package(s) ok\n", n, kind)
		return nil
	}
	ent, err := cat.ResolveName(name)
	if err != nil {
		return err
	}
	if ent.Kind != kind {
		return fmt.Errorf("package %q is a %s", ent.ID, ent.Kind)
	}
	if err := validateOne(s, cat, ent); err != nil {
		return err
	}
	fmt.Fprintf(s.Err, "byre: %s ok\n", ent.ID)
	return nil
}

func validateOne(s Streams, cat *packages.Catalog, ent *packages.Entry) error {
	_ = s
	if ent.Kind == packages.KindSkill {
		_, err := skills.Load(cat, ent.ID)
		return err
	}
	raw, err := ent.ReadPrimary()
	if err != nil {
		return err
	}
	// Same stage-2 path cascade load uses (composition ban + strict parse).
	_, err = config.ParseTemplateBody(raw)
	return err
}

// SkillArchiveLegacy moves LEGACY dirs aside.
func SkillArchiveLegacy(s Streams) error {
	home, err := project.Home()
	if err != nil {
		return err
	}
	moved, err := builtins.ArchiveLegacy(home)
	if err != nil {
		return err
	}
	if len(moved) == 0 {
		fmt.Fprintln(s.Err, "byre: no legacy package dirs to archive")
		return nil
	}
	for _, m := range moved {
		fmt.Fprintf(s.Err, "byre: archived %s\n", m)
	}
	return nil
}
