package commands

import (
	"fmt"
	"io"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

// resolved is the fully-loaded view of a project: the config cascade, the
// enabled skills, and — because every consumer wants them — the combined
// (config + skill) mount and volume sets, formed in one place.
type resolved struct {
	cfg     config.Config
	skills  skills.Resolved
	mounts  []config.Mount  // config mounts, then skill contributions
	volumes []config.Volume // config volumes, then skill contributions
	// mcps is the effective declared MCP set (config ∪ skills, minus config
	// closures — skills.MCPSet); mcpErr is its cross-source duplicate reject,
	// carried so validate() can surface it (combine stays error-free).
	mcps   []skills.MCPDecl
	mcpErr error
	// claudeSkills / claudeSkillsErr: the same pair for the effective Claude
	// Skill set (skills.ClaudeSkillSet).
	claudeSkills    []skills.ClaudeSkillDecl
	claudeSkillsErr error
	// cat is the package catalog this resolution read, carried so the launch
	// record can name each skill's provenance AS ACQUIRED at launch. Looking it
	// up again at status time would answer with today's catalog, which is the
	// re-derivation the record exists to abolish. nil in combine() (and thus in
	// unit tests, which drive develop directly) drops the provenance column.
	cat *packages.Catalog
	// otherEngines are session runners for installed engines OTHER than the
	// configured one, so develop can enforce single-session across an engine
	// switch (ADR 0004). Set by Develop; nil in combine() (and thus in unit
	// tests, which drive develop directly) means "no cross-engine check".
	otherEngines []sessionRunner
	// declinedEngines are the OTHER engines byre found but will not run.
	// Distinct from otherEngines being short by one: these have to reach the
	// single-session check as UNCHECKABLE, not as absent, or the engine record
	// advances into a silence it hasn't earned. Set by Develop; nil elsewhere.
	declinedEngines []declinedEngine
	// gitExe is the host git this invocation runs (hostGit): an absolute path
	// pinned for the process, or "" when there is none to run -- not
	// installed, or resolved out of a directory the box writes. Set by
	// Develop; "" in combine() (and thus in unit tests, which drive develop
	// directly) means the git-backed probes degrade, which is the same shape
	// a host with no git already produces.
	gitExe string
	// credFiles are the cascade's winning credential rows grouped by the
	// file that contributed each (root-most first) — the unlock flow's
	// whole input. It rides the resolved view so the under-lock re-read
	// refreshes it with everything else: a save that lands while develop
	// waits for the lock must not deliver the rows it replaced. credErr is
	// a cascade whose rows cannot be read at all; credentials are blocking,
	// so a launch surfaces it rather than proceeding without them. nil/nil
	// in combine() (and thus in unit tests) means no credentials.
	credFiles []config.CredentialFile
	credErr   error
	// reread re-runs the very call that produced this view, so a setup writer
	// can take its authoritative read INSIDE the setup lock (see refresh).
	// Set by resolve(); nil in combine() (and thus in unit tests, which drive
	// develop directly) means the view is used as it was handed over.
	reread func() (resolved, error)
	// agentOverride is `develop --agent` on an already-configured project: the
	// agent FOR THIS RUN, resolved exactly as if the config's `agent` key said
	// so (the skill enabled implicitly, its contributions riding the normal
	// path) but never written anywhere. Carried on the view so the under-lock
	// re-read keeps it and the launch paths can say the box runs an override.
	// "" means no override (the config's own agent); the canonical skill id or
	// the "none" sentinel (an agentless run) otherwise.
	agentOverride string
}

// refresh re-reads the project and returns the fresh view, carrying forward
// the host-side pins the first pass established: the peer engines' runners,
// the engines byre declined to run, and the pinned host git. Those come from
// probing the HOST (what is installed, what PATH resolves to) rather than from
// the project's files, so the setup lock neither guards them nor can change
// them -- re-running them would spend host probes on an answer that cannot
// have moved. Everything the config and the skill set decide comes from the
// fresh read.
//
// A view with no reread (a hand-built one) is returned unchanged: there is no
// second resolution path here, only the same resolve run again or nothing.
func (rv resolved) refresh() (resolved, error) {
	if rv.reread == nil {
		return rv, nil
	}
	fresh, err := rv.reread()
	if err != nil {
		return resolved{}, err
	}
	fresh.otherEngines = rv.otherEngines
	fresh.declinedEngines = rv.declinedEngines
	fresh.gitExe = rv.gitExe
	return fresh, nil
}

// refuseEngineChangedUnderLock names the one drift the re-read above cannot
// absorb. Every setup writer detects the engine BEFORE taking the lock, and
// the runner, the identity mode (ADR 0032) and the image tag all descend from
// that detection — so a save that renames the engine while the writer waits
// can only be refused: building or launching on the engine the config just
// stopped naming would report success for an image the next develop never
// runs, under an identity mode that engine may not even use. `""`/`auto` is
// not a change: it names whatever byre found, which is what is running.
// Called under the lock, on the fresh read, before anything is built.
func refuseEngineChangedUnderLock(cfg config.Config, running runner.Engine, command string) error {
	e := cfg.Engine
	if e == "" || e == "auto" || e == string(running) {
		return nil
	}
	return fmt.Errorf("the configured engine changed to %q while %s waited for the setup lock (this session resolved %s); nothing was built — re-run the command", e, command, running)
}

// combine forms the resolved view from a loaded config and its skills — the
// single place the config+skill mount/volume union and the effective MCP set
// are built.
func combine(cfg config.Config, res skills.Resolved) resolved {
	mcps, mcpErr := skills.MCPSet(cfg, res)
	claudeSkills, claudeSkillsErr := skills.ClaudeSkillSet(cfg, res)
	return resolved{
		cfg:             cfg,
		skills:          res,
		mounts:          append(append([]config.Mount{}, cfg.Mounts...), res.Mounts()...),
		volumes:         append(append([]config.Volume{}, cfg.Volumes...), res.Volumes()...),
		mcps:            mcps,
		mcpErr:          mcpErr,
		claudeSkills:    claudeSkills,
		claudeSkillsErr: claudeSkillsErr,
	}
}

// validate re-checks the combined mount/volume set for target/name collisions
// across config and skills (each side is already valid on its own), and the
// cross-source MCP name collisions MCPSet rejected. The attributed scan runs
// first: a cross-source collision names WHO declared each side — the flat
// invariant check can't (it sees one list), and "collides with mount X" is a
// riddle when one X is yours and the other rode in with a skill.
func (rv resolved) validate() error {
	if err := rv.attributedCollisions(); err != nil {
		return fmt.Errorf("config + skills: %w", err)
	}
	if err := (config.Config{Mounts: rv.mounts, Volumes: rv.volumes}).Validate(); err != nil {
		return fmt.Errorf("config + skills: %w", err)
	}
	if rv.mcpErr != nil {
		return fmt.Errorf("config + skills: %w", rv.mcpErr)
	}
	if rv.claudeSkillsErr != nil {
		return fmt.Errorf("config + skills: %w", rv.claudeSkillsErr)
	}
	return nil
}

// attributedCollisions mirrors Validate's mount/volume uniqueness invariants
// (targets unique across both kinds, volume names unique) over the combined
// set WITH provenance labels, so the error can say which source owns each
// side. Anything it misses still lands on the flat Validate behind it.
func (rv resolved) attributedCollisions() error {
	targets := map[string]string{} // container target -> "config's mount /x" etc.
	names := map[string]string{}   // volume name -> claimant
	claim := func(m map[string]string, key, who string) error {
		if prev := m[key]; prev != "" {
			return fmt.Errorf("%s collides with %s (skill grants ride the skill — remove the skill or your own entry)", who, prev)
		}
		m[key] = who
		return nil
	}
	walk := func(source string, mounts []config.Mount, volumes []config.Volume) error {
		for _, m := range mounts {
			if err := claim(targets, m.Target, fmt.Sprintf("%s's mount %s", source, m.Target)); err != nil {
				return err
			}
		}
		for _, v := range volumes {
			if err := claim(names, v.Name, fmt.Sprintf("%s's volume %s", source, v.Name)); err != nil {
				return err
			}
			if err := claim(targets, v.Target, fmt.Sprintf("%s's volume %s (target %s)", source, v.Name, v.Target)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk("config", rv.cfg.Mounts, rv.cfg.Volumes); err != nil {
		return err
	}
	for _, sk := range rv.skills.Skills {
		if err := walk("skill "+sk.Name, sk.File.Runtime.Mounts, sk.File.Volumes); err != nil {
			return err
		}
	}
	return nil
}

// resolve loads the config cascade and the enabled skills for a project, and
// re-validates the combined mount/volume set (config + skill contributions).
// notices receives store-ensure human lines (mirror regen, LEGACY) — pass the
// caller's s.Err; the once-per-process gate in builtins keeps develop's
// earlier noticed call from doubling. nil = silent (tests).
func resolve(paths project.Paths, projectDir string, notices io.Writer) (resolved, error) {
	return resolveWithAgent(paths, projectDir, notices, "")
}

// resolveWithAgent is resolve with `develop --agent`'s run-scoped override
// applied: the loaded config's `agent` is replaced BEFORE skill resolution, so
// the override's skill is enabled implicitly and every downstream contribution
// (Dockerfile, volumes, egress, injection adapters) follows the same path a
// written key would. agentOverride "" is plain resolve.
func resolveWithAgent(paths project.Paths, projectDir string, notices io.Writer, agentOverride string) (resolved, error) {
	if err := builtins.EnsureStoreOut(paths.Home, notices); err != nil {
		return resolved{}, err
	}
	cat, err := builtins.LoadCatalogRaw(paths.Home)
	if err != nil {
		return resolved{}, err
	}
	cfg, err := config.Load(projectDir)
	if err != nil {
		return resolved{}, err
	}
	if agentOverride != "" {
		// Same canonicalization the cascade applies to the key: aliases
		// expand, the "none" sentinel means agentless. The override itself
		// stays on the view in canonical spelling so launch and status can
		// name it.
		agentOverride = cat.ExpandAlias(agentOverride)
		cfg.Agent = config.FromNone(agentOverride)
	}
	res, err := skills.Resolve(cfg, cat)
	if err != nil {
		return resolved{}, err
	}
	rv := combine(cfg, res)
	rv.cat = cat
	// The FILE view of the same cascade: credential rows resolve per
	// contributing file (the [credentials] block is file-local and never
	// merges), which the merged config cannot answer for.
	if files, ferr := config.CascadeFiles(projectDir); ferr != nil {
		rv.credErr = ferr
	} else {
		rv.credFiles, rv.credErr = config.EncryptedRows(files)
	}
	if err := rv.validate(); err != nil {
		return resolved{}, err
	}
	// The view carries the means to take itself again: setup writers read the
	// cascade once here to decide what precedes the lock (which engine, which
	// prompts), then re-read under the lock, where a concurrent save is
	// serialized against them. The agent override rides the re-read: a save
	// landing while develop waits must not resurrect the config's agent for a
	// launch the user pointed elsewhere.
	rv.agentOverride = agentOverride
	rv.reread = func() (resolved, error) { return resolveWithAgent(paths, projectDir, notices, agentOverride) }
	return rv, nil
}
