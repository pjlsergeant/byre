package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"byre/internal/builtins"
	"byre/internal/config"
	"byre/internal/project"
	"byre/internal/skills"
)

// adoptedRecord is the file under the store holding the sha256 of the last
// adopted <project>/byre.config, so a changed proposal re-prompts.
const adoptedRecord = "adopted"

// proposalHash is the identity of a proposal's bytes — what the adoption
// record stores and every "has it changed?" check compares.
func proposalHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// adoptIfProposed handles a committed <project>/byre.config. That file is a
// PROPOSAL, never live config (byre only runs the host-side store, which the
// rw-mounted project can't write). If the proposal is new or changed since the
// last adoption, prompt the human — outside the box — to review its grants and
// copy it into the store. Declining (or a non-TTY) leaves the store untouched.
func adoptIfProposed(s Streams, projectDir string, paths project.Paths) error {
	proposed := filepath.Join(projectDir, config.ProjectConfigName)
	content, err := os.ReadFile(proposed)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	h := proposalHash(content)
	recordPath := filepath.Join(paths.Dir, adoptedRecord)
	if prev, e := os.ReadFile(recordPath); e == nil && strings.TrimSpace(string(prev)) == h {
		return nil // unchanged since last adoption — already reflected in the store
	}

	// Parse for the grant summary; never adopt something that doesn't parse.
	proposal, perr := config.ParseFile(proposed)
	if perr != nil {
		fmt.Fprintf(s.Err, "byre: %s ships a byre.config but it doesn't parse (%v); ignoring it.\n", projectDir, perr)
		return nil
	}
	// And never adopt what the next develop would reject. Two gates: the file
	// itself must pass the per-layer rules (a same-layer collision — ParseFile
	// is lenient by design — would fail loadLayer on a byre-owned file), and
	// the EFFECTIVE cascade must resolve on THIS host (a missing template, or a
	// collision with this machine's default.config, fails resolveWith the same
	// way). The second failure can be host-specific, so the message says where
	// to look rather than blaming the proposal alone.
	if verr := proposal.ValidateLayer(); verr != nil {
		fmt.Fprintf(s.Err, "byre: %s ships a byre.config but it is invalid (%v); ignoring it.\n", projectDir, verr)
		return nil
	}
	// Materialize the built-ins BEFORE the cascade gate: on a fresh ~/.byre a
	// proposal naming a built-in template (template = "go") must not be
	// rejected as missing when the normal resolve path would materialize it.
	// (adoptionView repeats these calls; they're idempotent.)
	_ = builtins.MaterializeTemplates(filepath.Join(paths.Home, "templates"))
	_ = builtins.MaterializeSkills(filepath.Join(paths.Home, "skills"))
	if _, rerr := config.ResolveProposed(proposal); rerr != nil {
		fmt.Fprintf(s.Err, "byre: %s ships a byre.config, but it doesn't resolve against this host's config (%v); not adopting. Fix the conflict (your ~/.byre/default.config or the named template may contribute to it) and re-run develop.\n", projectDir, rerr)
		return nil
	}
	// Summarize the EFFECTIVE config the human is consenting to — the full
	// cascade (default ⊕ template ⊕ proposal) PLUS the skills it enables — not
	// just the raw proposal file, so a bare `template=x`/`agent=x` can't smuggle
	// in template/skill mounts, caps, or run_args unseen.
	cfg, grants := adoptionView(paths, proposal)

	storePath := filepath.Join(paths.Dir, config.ProjectConfigName)
	storeExists := false
	if _, e := os.Stat(storePath); e == nil {
		storeExists = true
	}

	// Never adopt unattended — adoption is a deliberate, human, host-side act.
	if !s.TTY {
		fmt.Fprintf(s.Err, "byre: %s ships a byre.config; run develop interactively to review and adopt it (ignored for now).\n", projectDir)
		return nil
	}

	// With a store config already present, this proposal supersedes an earlier
	// adopted (or hand-written) one — say "changed", not "ships".
	headline := "ships a byre.config"
	if storeExists {
		headline = "has a changed byre.config"
	}
	fmt.Fprintf(s.Err, "\nThis project %s — review it before byre runs with it:\n  %s\n", headline, proposed)
	fmt.Fprintf(s.Err, "  base=%s  agent=%s  template=%s\n", config.OrNone(cfg.Base), config.OrNone(cfg.Agent), config.OrNone(proposal.Template))
	for _, g := range grants {
		fmt.Fprintf(s.Err, "  ⚠ %s\n", g)
	}
	fmt.Fprintf(s.Err, "--- %s ---\n%s\n------\n", config.ProjectConfigName, strings.TrimRight(string(content), "\n"))
	fmt.Fprint(s.Err, "Adopt this config? byre will build & run with it. [y/N] ")
	if !confirmed(s.In) {
		fmt.Fprintln(s.Err, "byre: not adopted; leaving the existing config in place.")
		return nil
	}

	if err := os.MkdirAll(paths.Dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(storePath, content, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(recordPath, []byte(h), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(s.Err, "byre: adopted into %s\n", storePath)
	return nil
}

// proposalState reports the state of a committed <project>/byre.config relative
// to the host-side store: "" (none), "adopted" (matches the record), or
// "pending" (new/changed, awaiting review). Used by status.
func proposalState(projectDir string, paths project.Paths) string {
	content, err := os.ReadFile(filepath.Join(projectDir, config.ProjectConfigName))
	if err != nil {
		return ""
	}
	rec, _ := os.ReadFile(filepath.Join(paths.Dir, adoptedRecord))
	if strings.TrimSpace(string(rec)) == proposalHash(content) {
		return "adopted"
	}
	return "pending"
}

// adoptionView resolves what a proposal will EFFECTIVELY run as — the cascade
// (default ⊕ template ⊕ proposal) plus the skills it enables — and returns that
// config with the full grant summary. Best-effort: if the cascade or skills
// can't be expanded, it falls back to the raw proposal and says so, so a failure
// to expand never hides grants behind an empty summary.
func adoptionView(paths project.Paths, proposal config.Config) (config.Config, []string) {
	_ = builtins.MaterializeTemplates(filepath.Join(paths.Home, "templates"))
	skillsDir := filepath.Join(paths.Home, "skills")
	_ = builtins.MaterializeSkills(skillsDir)

	effective, err := config.ResolveProposed(proposal)
	if err != nil {
		return proposal, append(grantSummary(proposal),
			"could not expand the cascade ("+err.Error()+") — grants shown are from the raw file only")
	}
	grants := grantSummary(effective)
	res, rerr := skills.Resolve(effective, skillsDir)
	if rerr != nil {
		return effective, append(grants,
			"could not expand skills ("+rerr.Error()+") — their grants are NOT shown")
	}
	return effective, append(grants, skillGrantSummary(res)...)
}

// skillGrantSummary lists the runtime grants the enabled skills contribute, so
// they're shown at adoption time alongside the config-level grants.
func skillGrantSummary(res skills.Resolved) []string {
	var s []string
	for _, g := range res.Grants() {
		for _, m := range g.Mounts {
			s = append(s, fmt.Sprintf("skill %q mounts %s -> %s (%s)", g.Skill, m.Host, m.Target, orDefault(m.Mode, "ro")))
		}
		if len(g.Caps) > 0 {
			s = append(s, fmt.Sprintf("skill %q adds capabilities: %s", g.Skill, strings.Join(g.Caps, ", ")))
		}
		if len(g.RunArgs) > 0 {
			s = append(s, fmt.Sprintf("skill %q adds raw docker run args (can grant --privileged, the docker socket, host net): %s", g.Skill, strings.Join(g.RunArgs, " ")))
		}
	}
	for _, v := range res.Volumes() {
		if v.Seed != nil && v.Seed.Host != "" {
			s = append(s, fmt.Sprintf("skill volume %q seeds from host path: %s", v.Name, v.Seed.Host))
		}
	}
	n := 0
	for _, b := range res.BuildBlocks() {
		n += len(b.Dockerfile)
	}
	if n > 0 {
		s = append(s, fmt.Sprintf("skills inject %d raw Dockerfile line(s)", n))
	}
	return s
}

// grantSummary lists the parts of a proposed config that grant power — the
// things a reviewer must see before adopting, since they can widen the sandbox.
func grantSummary(c config.Config) []string {
	var s []string
	if c.Dockerfile != "" {
		s = append(s, "FULL build opt-out — runs a hand-written Dockerfile: "+c.Dockerfile)
	}
	if len(c.Mounts) > 0 {
		var m []string
		for _, x := range c.Mounts {
			m = append(m, fmt.Sprintf("%s->%s(%s)", x.Host, x.Target, orDefault(x.Mode, "ro")))
		}
		s = append(s, "mounts host paths: "+strings.Join(m, ", "))
	}
	if len(c.RunArgs) > 0 {
		s = append(s, "raw docker run args (can grant --privileged, the docker socket, host net): "+strings.Join(c.RunArgs, " "))
	}
	if n := len(c.DockerfilePre) + len(c.DockerfilePost); n > 0 {
		s = append(s, fmt.Sprintf("injects %d raw Dockerfile line(s) (arbitrary build commands)", n))
	}
	for _, v := range c.Volumes {
		if v.Seed != nil && v.Seed.Host != "" {
			s = append(s, "seeds a volume from a host path: "+v.Seed.Host)
		}
	}
	if len(c.Skills) > 0 {
		s = append(s, "enables skills (each can add mounts/caps/run_args): "+strings.Join(c.Skills, ", "))
	}
	return s
}
