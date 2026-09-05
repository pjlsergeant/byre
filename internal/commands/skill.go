package commands

import (
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// PackageList, PackageInspect, PackageInstall, PackageUninstall,
// PackageFork, PackageInit, PackageValidate and PackagePack are the
// kind-taking package operations; the CLI binds skill and template onto
// them once, so a fix lands on both nouns by construction.
func PackageList(s Streams, kind packages.Kind) error {
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
	if kind == packages.KindSkill {
		// Full-load pass first: catalog ingest judges a primary's bytes, and a
		// skill can still fail to load on its mount shape or its context file.
		// Without this the listing calls such a skill healthy -- the one place
		// a user goes to ask what is wrong with their packages.
		skills.MarkLoadFailures(cat)
	}
	for _, ent := range cat.List(kind) {
		id := ent.ID
		if ent.Alias != "" {
			id = ent.Alias + " (" + ent.ID + ")"
		}
		label := ent.ProvenanceLabel()
		switch ent.Provenance {
		case packages.ProvInvalid, packages.ProvConflict:
			dataf(s.Out, "%-28s  %-16s  %s\n", id, label, ent.Reason)
		default:
			if ent.Description != "" {
				dataf(s.Out, "%-28s  %-16s  %s\n", id, label, ent.Description)
			} else {
				dataf(s.Out, "%-28s  %s\n", id, label)
			}
		}
	}
	return nil
}

func PackageInspect(s Streams, kind packages.Kind, id string) error {
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
	dataf(s.Out, "ID:          %s\n", ent.ID)
	if ent.Alias != "" {
		dataf(s.Out, "Alias:       %s\n", ent.Alias)
	}
	dataf(s.Out, "Kind:        %s\n", ent.Kind)
	dataf(s.Out, "Version:     %s\n", ent.Version)
	switch ent.Provenance {
	case packages.ProvInstalled:
		dataf(s.Out, "Digest:      sha256:%s\n", ent.Digest)
		if ent.SourceURI != "" {
			// Provenance of acquisition, never an instruction byre follows.
			dataf(s.Out, "Acquired:    %s\n", ent.SourceURI)
		}
	case packages.ProvBundled:
		// Display digest computed from the embedded bytes (ADR 0029): inspect
		// parity with installed rows, never an integrity claim.
		if d, err := packages.DisplayDigest(ent); err == nil {
			dataf(s.Out, "Digest:      sha256:%s\n", d)
		} else {
			// Our own embedded bytes failing to digest is a byre bug; degrade
			// the claim loudly rather than blocking inspect.
			dataf(s.Err, "byre: display digest unavailable for %s: %v\n", ent.ID, err)
		}
	}
	dataf(s.Out, "Provenance:  %s\n", ent.ProvenanceLabel())
	if ent.Description != "" {
		dataf(s.Out, "Description: %s\n", ent.Description)
	}
	if ent.Reason != "" {
		dataf(s.Out, "Status:      %s\n", ent.Reason)
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
		dataf(s.Out, "\nSource: %s\n", srcPath)
	}
	if ent.Provenance == packages.ProvBundled || ent.Provenance == packages.ProvInstalled {
		dataf(s.Out, "This package is immutable. To edit: byre %s fork %s <new-id>\n", kind, ent.DisplayName())
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
		dataf(w, "  agent command: %s\n", f.Agent.Command)
		if f.Agent.State != "" {
			dataf(w, "  agent state:   %s\n", f.Agent.State)
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
		dataf(w, "  mount: %s -> %s (%s)\n", m.Host, m.Target, mode)
	}
	for _, v := range f.Volumes {
		scope := v.Scope
		if scope == "" {
			scope = "project"
		}
		dataf(w, "  volume: %s (%s, %s) -> %s\n", v.Name, v.Role, scope, v.Target)
	}
	for _, c := range rt.Caps {
		dataf(w, "  cap: %s\n", c)
	}
	for _, a := range rt.RunArgs {
		dataf(w, "  run_arg: %s\n", a)
	}
	if rt.NetnsInit != "" {
		dataf(w, "  netns_init: %s\n", rt.NetnsInit)
	}
	if rt.NetworkPosture != "" {
		dataf(w, "  network_posture: %s\n", rt.NetworkPosture)
	}
	for _, p := range rt.SockGroups {
		dataf(w, "  sock_groups: %s\n", p)
	}
	if rt.Containment != "" {
		dataf(w, "  containment: %s\n", rt.Containment)
	}
	for _, e := range rt.Egress {
		dataf(w, "  egress: %s\n", e)
	}
	for _, e := range rt.EgressOffered {
		dataf(w, "  egress_offered: %s\n", e)
	}
	// MCP declarations: wiring, but part of the trust surface — a remote URL
	// implies egress and the env list names what the server will consume.
	for _, m := range f.MCPs {
		if m.Remote() {
			dataf(w, "  mcp: %s (remote: %s)\n", m.Name, m.URL)
		} else {
			dataf(w, "  mcp: %s (local: %s)\n", m.Name, strings.Join(m.Command, " "))
		}
		for _, k := range m.Env {
			dataf(w, "    consumes env: %s\n", k)
		}
		for _, e := range m.Egress {
			dataf(w, "    egress: %s\n", e)
		}
		// Headers with VALUES: inspect is the pre-enable trust surface, and a
		// template (or a literal a manifest smuggles) is exactly what the
		// reviewer must see.
		for _, k := range m.HeaderNames() {
			dataf(w, "    header: %s: %s\n", k, m.Headers[k])
		}
	}
	for _, k := range slices.Sorted(maps.Keys(rt.Env)) {
		dataf(w, "  env: %s=%s\n", k, rt.Env[k])
	}
	for _, k := range slices.Sorted(maps.Keys(rt.EnvDocs)) {
		dataf(w, "  env consumed: %s -- %s\n", k, rt.EnvDocs[k])
	}
	if f.CompanionFor != "" {
		dataf(w, "  companion_for: %s\n", f.CompanionFor)
	}
	if f.SharedAuthFor != "" {
		dataf(w, "  shared_auth_for: %s\n", f.SharedAuthFor)
	}
	// Build summary: counts + names, not inline dumps.
	var buildParts []string
	if n := len(f.Build.Apt); n > 0 {
		buildParts = append(buildParts, fmt.Sprintf("%d apt", n))
	}
	if n := len(f.Build.Dockerfile); n > 0 {
		buildParts = append(buildParts, fmt.Sprintf("%d dockerfile lines", n))
	}
	if len(buildParts) > 0 {
		dataf(w, "  build: %s\n", strings.Join(buildParts, ", "))
	}
	if n := len(f.Build.Files); n > 0 {
		names := slices.Sorted(maps.Keys(f.Build.Files))
		shown := names
		if len(shown) > 8 {
			shown = append(shown[:8], "...")
		}
		dataf(w, "  files: %d (%s)\n", n, strings.Join(shown, ", "))
	}
	if f.Context.Text != "" || f.Context.File != "" {
		src := "inline"
		if f.Context.File != "" {
			src = f.Context.File
		}
		dataf(w, "  context: present (%s)\n", src)
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
		dataf(w, "  base: %s\n", cfg.Base)
	}
	if cfg.Engine != "" {
		dataf(w, "  engine: %s\n", cfg.Engine)
	}
	// Templates are cascade LAYERS: `!name` entries, `target = "!x"` mounts,
	// and `remove = true` ports subtract from lower layers. Render them as
	// removals, never as grants — the trust surface must agree with the merge.
	for _, a := range cfg.Apt {
		printListLine(w, "apt", a)
	}
	for _, e := range cfg.EgressOffered {
		printListLine(w, "egress_offered", e)
	}
	for _, e := range cfg.Egress {
		printListLine(w, "egress", e)
	}
	for _, m := range cfg.Mounts {
		if name, ok := config.CutRemoval(m.Target); ok {
			dataf(w, "  removes mount: %s\n", name)
			continue
		}
		mode := m.Mode
		if mode == "" {
			mode = "ro"
		}
		if m.Disabled {
			mode += ", disabled"
		}
		dataf(w, "  mount: %s -> %s (%s)\n", m.Host, m.Target, mode)
	}
	for _, v := range cfg.Volumes {
		if name, ok := config.CutRemoval(v.Name); ok {
			dataf(w, "  removes volume: %s\n", name)
			continue
		}
		scope := v.Scope
		if scope == "" {
			scope = "project"
		}
		// The seed suffix is a second write rather than a composed string, so
		// its host path rides the funnel as its own argument.
		dataf(w, "  volume: %s (%s, %s) -> %s", v.Name, v.Role, scope, v.Target)
		if v.Seed != nil {
			if v.Seed.Host != "" {
				dataf(w, " [seed host=%s]", v.Seed.Host)
			} else if v.Seed.Literal != "" {
				dataf(w, " [seed literal]")
			}
		}
		dataf(w, "\n")
	}
	for _, p := range cfg.Ports {
		if p.Remove {
			dataf(w, "  removes port: container %d\n", p.Container)
			continue
		}
		iface, host := config.PortEffective(p)
		dataf(w, "  port: %s:%d -> container %d\n", iface, host, p.Container)
	}
	for _, k := range slices.Sorted(maps.Keys(cfg.Env)) {
		dataf(w, "  env: %s=%s\n", k, cfg.Env[k])
	}
	for _, k := range slices.Sorted(maps.Keys(cfg.EnvFromHost)) {
		dataf(w, "  env_from_host: %s <- %s\n", k, cfg.EnvFromHost[k])
	}
	if n := len(cfg.RunArgs); n > 0 {
		dataf(w, "  run_args: %d (raw docker flags)\n", n)
	}
	if n := len(cfg.Files); n > 0 {
		names := slices.Sorted(maps.Keys(cfg.Files))
		shown := names
		if len(shown) > 8 {
			shown = append(shown[:8], "...")
		}
		dataf(w, "  files: %d (%s)\n", n, strings.Join(shown, ", "))
	}
	if n := len(cfg.DockerfilePre) + len(cfg.DockerfilePost); n > 0 {
		dataf(w, "  dockerfile lines: %d (pre+post)\n", n)
	}
	if cfg.WorktreeBase != "" {
		dataf(w, "  worktree_base: %s\n", cfg.WorktreeBase)
	}
	if cfg.SeedPrefsEnabled() {
		fmt.Fprintln(w, "  seed_prefs: true")
	}
}

// printListLine writes one string-list entry, showing `!name` cascade markers
// as removals instead of grants. It writes rather than returns so the entry
// stays an ARGUMENT to the funnel instead of a pre-composed line.
func printListLine(w io.Writer, key, val string) {
	if name, ok := config.CutRemoval(val); ok {
		dataf(w, "  removes %s: %s\n", key, name)
		return
	}
	dataf(w, "  %s: %s\n", key, val)
}

func PackageFork(s Streams, kind packages.Kind, id, newID string) error {
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

	sub, prim := packages.StoreSubdir(kind), packages.PrimaryName(kind)
	destDir := filepath.Join(home, sub, filepath.FromSlash(newID))
	if _, err := hostopen.PlainStat(destDir, hostopen.StoreOwned); err == nil {
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
	if err := hostopen.PlainMkdirAll(parent, 0o755, hostopen.StoreOwned); err != nil {
		return err
	}
	stage, err := hostopen.PlainMkdirTemp(parent, ".fork-stage-*", hostopen.StoreOwned)
	if err != nil {
		return err
	}
	defer hostopen.PlainRemoveAll(stage, hostopen.ByreCreated) // no-op once the publish rename succeeds
	if err := copyDir(hostSrc, stage); err != nil {
		return err
	}

	// Provenance comment at the top of the primary file — rewritten in
	// staging, so a published fork always carries the fork's identity.
	primPath := filepath.Join(stage, prim)
	body, err := hostopen.PlainReadFile(primPath, hostopen.ByreCreated)
	if err != nil {
		return err
	}
	// Strip any existing [package] and write a local-style primary with a
	// provenance comment + declared id. The strip returns unreadable input
	// UNCHANGED (every other consumer has a strict parse behind it that then
	// fails loudly) — here nothing re-parses before publishing, so verify the
	// strip took: a fork must not ship the source's [package] table under the
	// new header.
	body = packages.StripPackageTable(body)
	if _, ok, err := packages.ParseManifestCore(body); err != nil || ok {
		return fmt.Errorf("forking %s: its primary is unreadable or still declares [package] after the strip — the fork would carry the source's package table under its new header; fix the source package first", src.ID)
	}
	// The provenance comment leads the file; the [package] table goes BELOW
	// the body's leading bare keys (a template's base, a companion's
	// companion_for), or those keys would become package.* and vanish --
	// every forked bundled template silently lost its base this way.
	provenance := fmt.Sprintf(
		"# Forked from %s@%s\n# Informational only: byre never reads this for resolution, updates, or trust.\n\n",
		src.ID, src.Version,
	)
	pkg := fmt.Sprintf("[package]\nid = %q\nkind = %q\n\n", newID, kind)
	out := append([]byte(provenance), packages.HeaderAfterPreamble(pkg, body)...)
	if err := hostopen.PlainWriteFile(primPath, out, 0o644, hostopen.ByreCreated); err != nil {
		return err
	}
	// Publish. Rename refuses an existing destination directory, so a
	// concurrent fork that won the race is not replaced.
	if err := hostopen.PlainRename(stage, destDir, hostopen.StoreOwned); err != nil {
		return fmt.Errorf("publishing the fork: %w", err)
	}

	dataf(s.Err, "byre: forked %s -> %s\n", src.ID, destDir)
	key := "skills"
	if kind == packages.KindTemplate {
		key = "template"
	}
	if kind == packages.KindTemplate {
		dataf(s.Err, "      To use it: set template = %q in your byre.config\n", newID)
	} else {
		dataf(s.Err, "      To use it: add %q to %s (or set agent = %q) in your byre.config\n", newID, key, newID)
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
					dataf(s.Err, "      Warning: volume %q is machine-scoped — the fork still names the same volume\n", v.Name)
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
	root, err := hostopen.PlainOpenRoot(src, hostopen.StoreOwned)
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
			return hostopen.PlainMkdirAll(out, 0o755, hostopen.ByreCreated)
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
		if err := hostopen.PlainMkdirAll(filepath.Dir(out), 0o755, hostopen.ByreCreated); err != nil {
			return err
		}
		return hostopen.PlainWriteFile(out, b, fi.Mode().Perm(), hostopen.ByreCreated)
	})
}

func PackageInit(s Streams, kind packages.Kind, name string) error {
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
	sub, prim := packages.StoreSubdir(kind), packages.PrimaryName(kind)
	example := skillInitExample(name)
	if kind == packages.KindTemplate {
		example = templateInitExample(name)
	}
	dir := filepath.Join(home, sub, filepath.FromSlash(name))
	if err := hostopen.PlainMkdirAll(dir, 0o755, hostopen.StoreOwned); err != nil {
		return err
	}
	path := filepath.Join(dir, prim)
	if _, err := hostopen.PlainStat(path, hostopen.StoreOwned); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if err := hostopen.PlainWriteFile(path, []byte(example), 0o644, hostopen.StoreOwned); err != nil {
		return err
	}
	dataf(s.Err, "byre: created %s\n", path)
	return nil
}

func skillInitExample(id string) string {
	return fmt.Sprintf(`# Local skill scaffold. [package] is optional for local packages (id
# defaults to the store path). Uncomment and edit. Bare keys (description,
# companion_for, ...) go ABOVE [package]: TOML gives a bare key to the most
# recent table header, and byre refuses a package.* key it does not define.

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
# [sources] (composition belongs in a preset). Bare keys stay ABOVE
# [package]: TOML gives a bare key to the most recent table header, and byre
# refuses a package.* key it does not define.

base = "debian:bookworm-slim"
# egress_offered = []

[package]
id = %q
kind = "template"
description = "TODO: one-line summary"
`, id)
}

func PackageValidate(s Streams, kind packages.Kind, name string) error {
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
			if err := validateOne(cat, ent); err != nil {
				return err
			}
			n++
		}
		dataf(s.Err, "byre: %d %s package(s) ok\n", n, kind)
		return nil
	}
	ent, err := cat.ResolveName(name)
	if err != nil {
		return err
	}
	if ent.Kind != kind {
		return fmt.Errorf("package %q is a %s", ent.ID, ent.Kind)
	}
	if err := validateOne(cat, ent); err != nil {
		return err
	}
	dataf(s.Err, "byre: %s ok\n", ent.ID)
	return nil
}

func validateOne(cat *packages.Catalog, ent *packages.Entry) error {
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
