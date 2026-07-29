// Package build assembles the docker build context for a project: the generated
// Dockerfile, the launcher script, and any skill/agent files COPYed by the
// generated build. Keeping context assembly here keeps the generator (text) and
// the runner (exec) free of filesystem layout concerns.
package build

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// fileCopy is one staged copy job: a source, its destination inside the build
// context, and a description for error messages.
type fileCopy struct {
	// srcRoot, when non-empty, is a TRUSTED root directory that src is resolved
	// relative to via openat (os.Root), so an intermediate component an agent
	// swaps for an escaping symlink after validation cannot redirect the open.
	// planFiles sets it (sources are always project-root relative). When empty,
	// src is an ABSOLUTE pathname; stageCopy still routes it through the project
	// root if it lands inside the agent-writable project, and only uses the
	// by-pathname copyPath for a source genuinely outside it.
	srcRoot string
	src     string
	staged  string
	what    string
	// budget, when set, re-enforces a validated bound AT the copy: the
	// source is agent-writable and the project stays live between the
	// validation walk and this staging, so the walk's verdict can be
	// stale -- the bound must hold where the bytes actually move.
	budget *copyBudget
	// skill+dest are set on skill-file jobs only: the image destination this
	// job's COPY will claim and the skill claiming it, so Assemble can judge
	// cross-skill same-dest claims over the STAGED bytes (see
	// refuseDivergentSkillDests).
	skill string
	dest  string
}

// copyBudget is a cumulative files/bytes allowance charged per staged
// regular file, at the fd's own fstat size. Exhaustion refuses the stage
// with attribution; nil charges nothing (`files` sources carry no bound
// today -- theirs is the per-file torn-read check).
type copyBudget struct {
	files int
	bytes int64
	what  string
}

func (b *copyBudget) charge(size int64) error {
	if b == nil {
		return nil
	}
	b.files--
	b.bytes -= size
	if b.files < 0 || b.bytes < 0 {
		return fmt.Errorf("%s: grew past its validated bound between validation and staging — refusing to stage a torn set", b.what)
	}
	return nil
}

// buildInput computes the generator input for a project WITHOUT writing anything
// (it reads and validates sources, but stages no bytes) and returns the copy
// jobs that would populate the context. Assemble and Render share it so the
// rendered Dockerfile always matches what a build would actually use.
func buildInput(paths project.Paths, cfg config.Config, res skills.Resolved) (gen.Input, []fileCopy, error) {
	// `files` copies host paths into the image: map each source to its staged
	// context path (so the generated COPY can find it) and record the copy job.
	genFiles, fileJobs, err := planFiles(paths, cfg.Files)
	if err != nil {
		return gen.Input{}, nil, err
	}
	// Skills can ship files from their own dir into the image: map each skill's
	// build block to the generator's, filling its COPY map, and record the jobs.
	genSkills, skillJobs, err := planSkillBlocks(res.BuildBlocks())
	if err != nil {
		return gen.Input{}, nil, err
	}
	// The declared Claude Skill set: validate each source dir as a Claude
	// Skill and stage it under the canonical context tree (the COPY itself is
	// unconditional — gen always emits it, Assemble always creates the tree).
	claudeSkillJobs, err := planClaudeSkills(cfg, res)
	if err != nil {
		return gen.Input{}, nil, err
	}
	in := gen.Input{
		Base:         cfg.Base,
		Env:          cfg.Env,
		Files:        genFiles,
		Apt:          cfg.Apt,
		Skills:       genSkills,
		AgentCmd:     res.AgentCommand() != "",
		AgentContext: true, // the chassis paragraph makes context non-empty on every box
		// The self-edit note rides any agent: the launcher folds it into the
		// per-session context (BYRE_SESSION_CONTEXT) when the grant is live.
		SelfEditDoc:    res.AgentCommand() != "",
		VolumeDirs:     volumeDirs(cfg.Volumes, res.Volumes()),
		DockerfilePre:  cfg.DockerfilePre,
		DockerfilePost: cfg.DockerfilePost,
		Guard:          planGuard(genSkills, res),
	}
	return in, append(append(fileJobs, skillJobs...), claudeSkillJobs...), nil
}

// planGuard derives the security-critical files gen re-asserts at the Dockerfile
// tail (beyond the launcher, which gen re-COPYs unconditionally). Scope: only a
// network-posture skill contributes here — its launch gate and netns
// enforcement script, which a project `files` clobber could otherwise empty or
// stub while status still reads deny-by-default. The set is DERIVED from the
// resolved skills (res.NetnsInits gives the script path(s); the gate path is the
// launcher's constant), so a future posture skill is covered without editing a
// hardcoded list. Each guarded dest's staged source is looked up from the skill
// files already planned — byre only re-asserts a file it itself staged.
func planGuard(genSkills []gen.SkillBlock, res skills.Resolved) []gen.GuardFile {
	hooks := res.NetnsInits()
	if len(hooks) == 0 {
		return nil
	}
	// Resolve the security-critical staged sources from the NETNS-POSTURE skill's
	// OWN blocks only -- never a global last-wins map over every skill. By ADR
	// 0041 provenance order (bundled -> installed -> local) a later block could
	// otherwise map a stub onto the gate/netns dest and win, making the guard
	// re-assert a foreign file as the "protected" content (fail-open while status
	// still reads deny-by-default). A foreign block can no longer shadow it.
	posture := map[string]bool{}
	for _, h := range hooks {
		posture[h.Skill] = true
	}
	byDest := map[string]string{} // image dest -> staged context path (posture skill only)
	for _, s := range genSkills {
		if !posture[s.Name] {
			continue
		}
		for staged, dest := range s.Files {
			byDest[dest] = staged
		}
	}
	var guard []gen.GuardFile
	add := func(dest string, exec bool) {
		if staged, ok := byDest[dest]; ok {
			guard = append(guard, gen.GuardFile{Staged: staged, Dest: dest, Exec: exec})
		}
	}
	// The gate the launcher waits on: re-assert so an empty-file clobber can't
	// make the launcher skip the wait (a `-s` test that then fails open).
	add(gen.LaunchGatePath, false)
	// The netns enforcement script(s) the helper runs as its entrypoint from THIS
	// image: re-assert so a clobber can't swap in a rules-free stub. Resolution
	// rejects a second netns_init, so this stays deterministic (single hook).
	for _, h := range hooks {
		add(h.Path, true)
	}
	return guard
}

// Render returns the generated Dockerfile text WITHOUT touching the build
// context on disk. `byre dockerfile` is informational and side-effect-free, so
// it must not clear-and-restage the context (which Assemble does) — that would
// race a concurrent `byre develop` build sharing the same context dir.
func Render(paths project.Paths, cfg config.Config, res skills.Resolved) (string, error) {
	in, _, err := buildInput(paths, cfg, res)
	if err != nil {
		return "", err
	}
	return gen.Dockerfile(in), nil
}

// AssembleWarn writes the build context (Dockerfile + launcher + agent files
// + any `files`) and returns the generated Dockerfile text, with the
// operator's stderr attached: size-tier notes about [[context]] prose land on
// warn as the context is staged. Callers with no operator pass io.Discard.
func AssembleWarn(paths project.Paths, cfg config.Config, res skills.Resolved, warn io.Writer) (string, error) {
	// Every mutation below is staged through a descriptor confined to the REAL
	// context dir, so a `develop --self-edit` agent that swapped context/ (or an
	// interior staging dir) for a symlink cannot redirect byre's host-side
	// RemoveAll / writes elsewhere. First ensure the context component exists as
	// a real directory: Mkdir through a root at paths.Dir — the self-edit mount
	// ROOT, which the container cannot retarget (it is the bind-mount point) —
	// creates it when missing and never follows a planted symlink of that name
	// (Mkdir returns EEXIST on any existing name, symlink included). Then open it
	// no-follow: OpenDirRootNoFollow Lstat-rejects a symlinked context and
	// SameFile-checks after open. This step is load-bearing, not cosmetic:
	// os.Root's child ops refuse an ABSOLUTE/escaping symlink target ("path
	// escapes") but FOLLOW a RELATIVE in-root one (verified on go1.26 —
	// storeRoot.OpenRoot("context") where context -> "sibling" opens the
	// sibling), so relying on os.Root alone would let a self-edit agent redirect
	// these writes onto another store subdir. Every op below rides ctxRoot with a
	// context-relative name. The engine's later by-pathname READ of the finished
	// context is the disclosed byre-wide check-to-mount residual (ADR 0009), a
	// different surface but the same class; a rebuild concurrent with a live
	// self-edit session is where it applies.
	ctxName := filepath.Base(paths.ContextDir)
	storeRoot, err := hostopen.OpenDirRootNoFollow(paths.Dir)
	if err != nil {
		return "", err
	}
	if err := storeRoot.Mkdir(ctxName, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		storeRoot.Close()
		return "", err
	}
	storeRoot.Close()
	ctxRoot, err := hostopen.OpenDirRootNoFollow(paths.ContextDir)
	if err != nil {
		if errors.Is(err, hostopen.ErrSymlinkRoot) {
			return "", fmt.Errorf("build context %s is no longer the directory byre checked — a symlink or a swapped dir (a self-edit agent may have planted it); remove it and rebuild", paths.ContextDir)
		}
		return "", err
	}
	defer ctxRoot.Close()

	// Re-stage from scratch: clear the staging subtrees so a file removed from
	// `files`/skills since the last build can't linger in the context and make the
	// build nondeterministic (or get swept into the image).
	for _, d := range []string{"files", "skills", gen.ClaudeSkillsDirName} {
		if err := ctxRoot.RemoveAll(d); err != nil {
			return "", err
		}
	}
	// Same for the conditional context files: each is written only when its
	// condition holds, so a condition that turned false since the last build
	// (agent removed, context emptied) would otherwise leave a stale file behind.
	// "agent-context-target" is the RETIRED placement pointer (pre-ADR 0046
	// stores may still carry one) — removed like the live conditional files.
	for _, name := range []string{gen.AgentCmdName, gen.AgentContextName, "agent-context-target", gen.SelfEditDocName, gen.ConfigRefName} {
		if err := ctxRoot.Remove(name); err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}

	in, jobs, err := buildInput(paths, cfg, res)
	if err != nil {
		return "", err
	}
	for _, j := range jobs {
		if err := ctxRoot.MkdirAll(filepath.Dir(j.staged), 0o755); err != nil {
			return "", err
		}
		if err := stageCopy(ctxRoot, agentWritableRoots(paths, cfg, res), j); err != nil {
			return "", fmt.Errorf("%s: %w", j.what, err)
		}
	}
	if err := refuseDivergentSkillDests(ctxRoot, jobs); err != nil {
		return "", err
	}
	df := gen.Dockerfile(in)

	if err := ctxRoot.WriteFile(filepath.Base(paths.Dockerfile), []byte(df), 0o644); err != nil {
		return "", err
	}
	if err := ctxRoot.WriteFile(gen.LauncherName, gen.LauncherScript(), 0o755); err != nil {
		return "", err
	}
	// The /etc/profile.d shim that sources env.d for login shells (COPYed by the
	// core block); 0644, sourced not executed.
	if err := ctxRoot.WriteFile(gen.ProfileEnvName, gen.ProfileEnvScript(), 0o644); err != nil {
		return "", err
	}
	if cmd := res.AgentCommand(); cmd != "" {
		if err := ctxRoot.WriteFile(gen.AgentCmdName, agentScript(cmd), 0o755); err != nil {
			return "", err
		}
	}
	// The canonical Claude Skill tree root — created on every assemble even
	// when the declared set is empty (the COPY is unconditional, and claude's
	// --add-dir tolerates an empty skills dir; spike-verified), so the baked
	// path exists in every box. The skill dirs themselves were staged as jobs.
	if err := ctxRoot.MkdirAll(filepath.Join(gen.ClaudeSkillsDirName, ".claude", "skills"), 0o755); err != nil {
		return "", err
	}
	// The canonical declared MCP set — written on every assemble (the COPY is
	// unconditional), empty set included, so /etc/byre/mcp.json exists in
	// every box and an agent command can inject it unconditionally. resolve()
	// already rejected cross-source duplicates; recomputing here keeps
	// Assemble correct for callers that didn't.
	mcps, err := skills.MCPSet(cfg, res)
	if err != nil {
		return "", err
	}
	if err := ctxRoot.WriteFile(gen.MCPConfigName, config.MCPConfigJSON(skills.MCPList(mcps)), 0o644); err != nil {
		return "", err
	}
	// The chassis speaks first: mechanism facts every box carries (today, the
	// deliver inbox), then what the config provisioned, then the skills'
	// opinions in enable order. Chassis text is a mechanism description like
	// /workspace — not a skill's opinion — so it rides every box, not a skill
	// toggle (ADR 0021).
	ctx := chassisContext
	// The base image is the box's biggest unannounced surface (toolchains ride
	// it): one bake-time line closes that. Bake-time is accurate here — a base
	// change always forces a rebuild, unlike egress (announced at launch).
	// Announce the EFFECTIVE base: an unset config falls back to gen's default
	// (gen.Dockerfile does the same), so the line never claims less than FROM.
	base := cfg.Base
	if base == "" {
		base = gen.DefaultBase
	}
	ctx += "\n\nBox base image: " + base + "."
	if p := provisionedContext(cfg); p != "" {
		ctx += "\n\n" + p
	}
	if sc := res.Context(); sc != "" {
		ctx += "\n\n" + sc
	}
	// The operator's standing instructions ([[context]] declarations) speak
	// last: cascade order after the skills' opinions — the voice closest to
	// the user closes the file.
	cc, err := configContext(warn, agentWritableRoots(paths, cfg, res), cfg.Contexts)
	if err != nil {
		return "", err
	}
	if cc != "" {
		ctx += "\n\n" + cc
	}
	// The per-declaration tiers can't see a CUMULATIVE crossing (two 60 KiB
	// snippets, or prose stacked on chassis + skill context), and the argv
	// transport caps the COMBINED text — warn on the composed total too, so
	// the size-aware surface covers every truncation the wrappers disclose.
	if len(ctx) > argvChannelBudget {
		fmt.Fprintf(warn, "byre: ⚠ the composed instructions total %s — agents on argument-based injection channels truncate near 100 KiB, with an in-session disclosure; file-channel agents carry the full text.\n", fmtSize(len(ctx)))
	}
	if err := ctxRoot.WriteFile(gen.AgentContextName, []byte(ctx), 0o644); err != nil {
		return "", err
	}
	if res.AgentCommand() != "" {
		// The --self-edit note the launcher folds into the per-session context
		// (BYRE_SESSION_CONTEXT) when the self-edit mount (this project's store
		// at /home/dev/.byre-self) is present.
		if err := ctxRoot.WriteFile(gen.SelfEditDocName, []byte(selfEditDoc), 0o644); err != nil {
			return "", err
		}
		// The full config reference the note points at -- baked, never
		// injected, so its size never competes with standing instructions on
		// argv-budgeted agents.
		if err := ctxRoot.WriteFile(gen.ConfigRefName, []byte(configRefDoc), 0o644); err != nil {
			return "", err
		}
	}
	return df, nil
}

// chassisContext is the byre-mechanism paragraph every box's agent context
// carries — facts about the box itself, not workflow opinions (those belong
// to skills). One sentence per mechanism; keep it lean.
const chassisContext = "Files the user delivers from the host land in /inbox, owned by you. The inbox is ephemeral (it dies with the container) — treat it as a hand-off point, not storage."

// provisionedContext tells the agent what the CONFIG put in the box. Skills
// document their own tools via [context]; a plain `apt` entry
// otherwise leaves no in-box trace, and the agent should not have to discover
// provisioned tools by probing (legibility runs inward too). One sentence,
// emitted only when the config actually provisions something. Raw dockerfile
// lines stay unmentioned — passed through, not introspected, same posture as
// status.
func provisionedContext(cfg config.Config) string {
	var parts []string
	if len(cfg.Apt) > 0 {
		parts = append(parts, strings.Join(cfg.Apt, ", ")+" (apt)")
	}
	if len(parts) == 0 {
		return ""
	}
	return "The box config additionally provisions: " + strings.Join(parts, "; ") + "."
}

// selfEditDoc is placed into the agent's memory only when a session is started
// with --self-edit, so the agent knows it can edit its own byre sandbox config —
// including the actual config-key vocabulary so it doesn't have to guess.
//
//go:embed self-edit.md
var selfEditDoc string

// volumeDirs returns the mount-point dirs of all named volumes (config-declared
// and skill-contributed), so gen can pre-create them owned by the baked UID/GID —
// a fresh Docker named volume inherits the image dir's ownership at its mount
// point. gen de-dups, so overlap between the two sources is fine.
func volumeDirs(volSets ...[]config.Volume) []string {
	var dirs []string
	for _, vols := range volSets {
		for _, v := range vols {
			if v.Target != "" {
				dirs = append(dirs, v.Target)
			}
		}
	}
	return dirs
}

// agentScript wraps the agent's launch command in an executable shell script, so
// the launcher execs it (preserving quoting) rather than word-splitting text.
// The command is DELIBERATELY an unvalidated shell fragment (flags ride in it:
// "claude --dangerously-skip-permissions"): it comes only from a skill.toml the
// user enabled, and an enabled skill already runs anything it likes via raw
// [build].dockerfile lines and launch hooks. The typed-field allowlists are
// legibility, not containment (skills.go, docs/SECURITY.md "A skill is trusted
// code"); quoting this field would contain nothing.
func agentScript(command string) []byte {
	return []byte("#!/bin/sh\nexec " + command + " \"$@\"\n")
}

// planFiles maps each `files` source (a path relative to the project dir) to its
// staged context path under "files/<src>", returning the COPY map the generator
// emits (staged-context-path -> image destination) and the copy jobs to realize
// it. It validates sources (no absolute paths, no "../" or symlink escapes) and
// destinations (absolute image paths) but writes nothing — the caller stages the
// jobs (Assemble) or discards them (Render).
func planFiles(paths project.Paths, files map[string]string) (map[string]string, []fileCopy, error) {
	if len(files) == 0 {
		return nil, nil, nil
	}
	out := make(map[string]string, len(files))
	var jobs []fileCopy
	// Two TOML keys can name ONE source: "seed.txt" and "./seed.txt" clean to
	// the same relative path, so they collapse to one staged entry and one
	// destination -- and which survives is map-iteration order, i.e. random
	// per process. That breaks gen's byte-identical Dockerfile (ADR 0001's
	// cache sharing rests on it) and silently drops a declared build input.
	// Refuse, naming both spellings; the file set is walked in sorted key
	// order so the refusal itself is deterministic too.
	claimed := map[string]string{}
	for _, src := range slices.Sorted(maps.Keys(files)) {
		dest := files[src]
		if !filepath.IsAbs(dest) {
			return nil, nil, fmt.Errorf("files: destination %q must be an absolute path in the image", dest)
		}
		_, rel, err := safeProjectPath(paths.Canonical, src)
		if err != nil {
			return nil, nil, fmt.Errorf("files: %w", err)
		}
		if prev, dup := claimed[rel]; dup {
			return nil, nil, fmt.Errorf("files: %q and %q name the same source file (%s) -- two spellings of one path; keep one", prev, src, rel)
		}
		claimed[rel] = src
		// staged is relative to the build-context root (ctxRoot in Assemble),
		// so every destination write is confined beneath the real context dir.
		staged := filepath.Join("files", rel)
		// Anchor at the project root and stage rel through it (openat), rather
		// than by the resolved absolute path: the ancestors of a `files` source
		// are agent-writable, so a by-pathname reopen could be redirected by an
		// ancestor swapped to a symlink after safeProjectPath validated it.
		jobs = append(jobs, fileCopy{srcRoot: paths.Canonical, src: rel, staged: staged, what: "files: copying " + src})
		out[filepath.ToSlash(filepath.Join("files", rel))] = dest
	}
	return out, jobs, nil
}

// provenanceRank orders skill blocks by volatility class for layer-cache
// locality (ADR 0041): bundled skills change only with the byre binary,
// installed packages change on install events, local packages are
// working-tree-editable. Emitting stable-before-volatile means editing an
// installed skill no longer re-runs every bundled installer behind it.
// Unknown provenances (legacy/conflict/invalid never reach build; resolve
// rejects them) sort last.
func provenanceRank(p packages.Provenance) int {
	switch p {
	case packages.ProvBundled:
		return 0
	case packages.ProvInstalled:
		return 1
	case packages.ProvLocal:
		return 2
	default:
		return 3
	}
}

// planSkillBlocks maps each skill's build block onto the generator's, filling
// its COPY map (staged-context-path -> image dest) for files the skill ships
// under "skills/<skill>/<rel>", and returns the copy jobs. Sources were
// already validated for containment by skills.Resolve; this writes nothing.
func planSkillBlocks(blocks []skills.BuildBlock) ([]gen.SkillBlock, []fileCopy, error) {
	if len(blocks) == 0 {
		return nil, nil, nil
	}
	// Layer order is a build decision, made here at the skills->gen seam: sort
	// by volatility class, stable within one (enable order still breaks ties,
	// and the agent-facing enable order -- context composition, status -- is
	// untouched; only image layers move).
	blocks = append([]skills.BuildBlock(nil), blocks...)
	sort.SliceStable(blocks, func(i, j int) bool {
		return provenanceRank(blocks[i].Provenance) < provenanceRank(blocks[j].Provenance)
	})
	out := make([]gen.SkillBlock, 0, len(blocks))
	var jobs []fileCopy
	for _, b := range blocks {
		gb := gen.SkillBlock{Name: b.Name, Apt: b.Apt, Dockerfile: b.Dockerfile}
		for _, sf := range b.Files {
			ctxRel := filepath.ToSlash(filepath.Join("skills", b.Name, sf.Rel))
			// staged is relative to the build-context root; ctxRoot (an os.Root)
			// refuses any component that escapes it, but keep the explicit check
			// so a malformed name fails with a legible message, not a raw openat.
			staged := filepath.FromSlash(ctxRel)
			if staged == ".." || strings.HasPrefix(staged, ".."+string(filepath.Separator)) || filepath.IsAbs(staged) {
				return nil, nil, fmt.Errorf("skill %q: staged file path escapes the build context", b.Name)
			}
			jobs = append(jobs, fileCopy{src: sf.Src, staged: staged, what: fmt.Sprintf("skill %q files: copying %s", b.Name, sf.Rel), skill: b.Name, dest: sf.Dest})
			if gb.Files == nil {
				gb.Files = make(map[string]string)
			}
			gb.Files[ctxRel] = sf.Dest
		}
		out = append(out, gb)
	}
	return out, jobs, nil
}

// refuseDivergentSkillDests extends the one-dest-one-source rule across the
// composed skill set: skills.Resolve refuses two sources for one destination
// WITHIN a manifest, but two different skills claiming the same image dest
// resolved by COPY order, last writer wins, silently — the exact "silent
// shadowing" that intra-skill check exists to prevent. Byte-identical claims
// are ALLOWED: the dual-ship pattern (two skills each carrying the same lib
// so either works alone) is legitimate, and identical bytes make the COPY
// order irrelevant. The judgment runs over the STAGED trees — the bytes that
// actually ship, post symlink-refusal and bounds — never a second read of
// the sources. Granularity matches the intra-skill rule: same dest STRING;
// overlapping directory contents under different dest strings merge by COPY
// semantics as before.
func refuseDivergentSkillDests(ctxRoot *os.Root, jobs []fileCopy) error {
	claims := map[string]fileCopy{} // dest -> first claimant
	for _, j := range jobs {
		if j.dest == "" {
			continue
		}
		first, ok := claims[j.dest]
		if !ok {
			claims[j.dest] = j
			continue
		}
		diff, err := diffStagedTrees(ctxRoot, first, j, "")
		if err != nil {
			return fmt.Errorf("comparing staged claims on %s: %w", j.dest, err)
		}
		if diff != "" {
			return fmt.Errorf("skills %q and %q both install to %s but their copies differ (%s); the build-order winner would silently replace the loser — ship identical bytes or distinct destinations", first.skill, j.skill, j.dest, diff)
		}
	}
	return nil
}

// diffStagedTrees compares two staged claims inside the context root and
// returns a description of the first difference found, "" when the trees are
// identical: directories by entry set and recursion, regular files by
// permission bits, size, and bytes. Staging normalizes modes
// (stageRegularFromFD), so a mode difference between claims is the exec bit —
// which diverges in the image even over identical bytes, since COPY preserves
// the staged mode. rel is the path under the claimed destination, "" at the
// top; the description names it so a divergence deep in a directory claim
// points at the file, not just the dest.
func diffStagedTrees(ctxRoot *os.Root, a, b fileCopy, rel string) (string, error) {
	at := func(msg string) string {
		if rel == "" {
			return msg
		}
		return fmt.Sprintf("%s: %s", rel, msg)
	}
	fa, err := ctxRoot.Open(filepath.Join(a.staged, rel))
	if err != nil {
		return "", err
	}
	defer fa.Close()
	fb, err := ctxRoot.Open(filepath.Join(b.staged, rel))
	if err != nil {
		return "", err
	}
	defer fb.Close()
	ia, err := fa.Stat()
	if err != nil {
		return "", err
	}
	ib, err := fb.Stat()
	if err != nil {
		return "", err
	}
	if ia.IsDir() != ib.IsDir() {
		dir, file := a.skill, b.skill
		if ib.IsDir() {
			dir, file = b.skill, a.skill
		}
		return at(fmt.Sprintf("a directory in %q, a file in %q", dir, file)), nil
	}
	if ia.IsDir() {
		ea, err := fa.ReadDir(-1)
		if err != nil {
			return "", err
		}
		eb, err := fb.ReadDir(-1)
		if err != nil {
			return "", err
		}
		names := map[string][2]bool{}
		for _, e := range ea {
			names[e.Name()] = [2]bool{true, names[e.Name()][1]}
		}
		for _, e := range eb {
			names[e.Name()] = [2]bool{names[e.Name()][0], true}
		}
		for _, name := range slices.Sorted(maps.Keys(names)) {
			in := names[name]
			entry := path.Join(rel, name)
			switch {
			case !in[1]:
				return fmt.Sprintf("%s exists only in %q's copy", entry, a.skill), nil
			case !in[0]:
				return fmt.Sprintf("%s exists only in %q's copy", entry, b.skill), nil
			}
			diff, err := diffStagedTrees(ctxRoot, a, b, entry)
			if err != nil || diff != "" {
				return diff, err
			}
		}
		return "", nil
	}
	if ia.Mode().Perm() != ib.Mode().Perm() {
		return at(fmt.Sprintf("mode %#o in %q, %#o in %q", ia.Mode().Perm(), a.skill, ib.Mode().Perm(), b.skill)), nil
	}
	if ia.Size() != ib.Size() {
		return at("content differs"), nil
	}
	const chunk = 64 * 1024
	bufA, bufB := make([]byte, chunk), make([]byte, chunk)
	for {
		na, errA := io.ReadFull(fa, bufA)
		nb, errB := io.ReadFull(fb, bufB)
		if na != nb || !bytes.Equal(bufA[:na], bufB[:nb]) {
			return at("content differs"), nil
		}
		if errA == io.EOF || errA == io.ErrUnexpectedEOF {
			if errB == io.EOF || errB == io.ErrUnexpectedEOF {
				return "", nil
			}
			return at("content differs"), nil
		}
		if errA != nil {
			return "", errA
		}
		if errB != nil {
			return "", errB
		}
	}
}

// planClaudeSkills stages the effective Claude Skill set under the canonical
// context tree ("claude-skills/.claude/skills/<name>"), validating each source
// dir as a Claude Skill first (skills.ValidateClaudeSkillDir — SKILL.md,
// frontmatter, bounds; one owner for both homes). A skill contribution's
// source dir was already resolved and containment-checked by Resolve; a
// config declaration's `path` expands here (`~`-anchored or absolute — config
// vocabulary is deliberately wider than the project-relative `files` key, see
// config/claudeskills.go). Staging itself rejects symlinks (copyPath).
func planClaudeSkills(cfg config.Config, res skills.Resolved) ([]fileCopy, error) {
	set, err := skills.ClaudeSkillSet(cfg, res)
	if err != nil {
		return nil, err
	}
	var jobs []fileCopy
	for _, d := range set {
		src := d.SrcDir
		if src == "" { // a config declaration: expand its host path
			if src, err = expandHome(d.CS.Path); err != nil {
				return nil, fmt.Errorf("claude skill %s: %w", d.CS.Name, err)
			}
		}
		if err := skills.ValidateClaudeSkillDir(src, d.CS.Name); err != nil {
			return nil, err
		}
		staged := filepath.Join(gen.ClaudeSkillsDirName, ".claude", "skills", d.CS.Name)
		jobs = append(jobs, fileCopy{src: src, staged: staged, what: fmt.Sprintf("claude skill %s: copying %s", d.CS.Name, src),
			// The bound ValidateClaudeSkillDir just walked, re-enforced at
			// the copy itself: the dir is agent-writable, so the walk's
			// verdict can be stale by staging time (the review's
			// bound-exists-one-field-over sweep).
			budget: &copyBudget{files: skills.MaxClaudeSkillFiles, bytes: skills.MaxClaudeSkillBytes, what: "claude skill " + d.CS.Name}})
	}
	return jobs, nil
}

// [[context]] prose size tiers: prose is NEVER capped at the bake —
// escalating disclosures instead, because standing instructions this big
// almost always want to be a skill (prose + tooling,
// toggled per project) rather than unconditional instructions. One
// TRANSPORT truth rides every tier (ADR 0046 merge): agents whose
// injection channel is a command-line argument truncate near 100 KiB (the
// per-argument exec limit), with an in-session disclosure — file-channel
// agents carry the full text. The tiers disclose that too, so the
// develop-time report and the delivery never tell different stories. The tiers apply to inline
// text and file sources alike, so moving the same prose between forms never
// changes the outcome. The only refusal
// is contextReadCeiling, far above the loudest tier: the same
// read-boundedness every host read in byre obeys — a fat-fingered `file`
// target (a VM image, a tarball) must not balloon the develop — judged from
// fstat before a byte is read, never a prose budget.
const (
	contextNoteBytes   = 100 << 10 // sizeable: worth a mention
	contextWarnBytes   = 500 << 10 // a lot: suggest a skill
	contextShoutBytes  = 1 << 20   // wrong home: say so loudly
	contextReadCeiling = 16 << 20  // file reads only: not agent-memory-sized
	// argvChannelBudget mirrors the wrappers' whole-argument byte budget
	// (100000, under the ~131072 per-string exec limit): past this, the
	// argv-channel agents truncate — the composed-total warning fires here
	// so the develop report covers cumulative crossings the per-declaration
	// tiers can't see.
	argvChannelBudget = 100000
)

// warnContextSize emits the tier note for one snippet's prose size.
func warnContextSize(warn io.Writer, name string, n int) {
	switch {
	case n >= contextShoutBytes:
		fmt.Fprintf(warn, "byre: 🛑 context %s is %s of standing instructions — injected instructions are the WRONG HOME for prose this size; package it as a skill (prose + tooling, per-project toggle), or mount the file into the box. Agents on argument-based injection channels truncate near 100 KiB either way, with an in-session disclosure.\n", name, fmtSize(n))
	case n >= contextWarnBytes:
		fmt.Fprintf(warn, "byre: ⚠ context %s is %s — that is a lot of standing prose; consider packaging it as a skill. Agents on argument-based injection channels truncate near 100 KiB, with an in-session disclosure.\n", name, fmtSize(n))
	case n >= contextNoteBytes:
		fmt.Fprintf(warn, "byre: context %s: %s of prose joins the injected instructions; agents on argument-based injection channels truncate near 100 KiB, with an in-session disclosure.\n", name, fmtSize(n))
	}
}

// fmtSize renders a byte count the way the tier notes speak: KiB below a
// mebibyte, MiB with one decimal above.
func fmtSize(n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KiB", n>>10)
}

// configContext concatenates the resolved [[context]] declarations in
// cascade order — the operator's standing instructions (contextdecl.go).
// Inline text is used as written; a `file` source is a declared build input:
// it is read here, and a missing or unreadable file fails the develop loudly
// (nothing to degrade — the operator asked for exactly this content). Reads
// follow the stageCopy routing: a path inside the agent-writable tree is
// opened through an os.Root anchored there (openat, so an agent-swapped
// ancestor or escaping symlink is refused rather than followed into
// arbitrary host content), while a path genuinely outside it is a user-named
// host file, opened following their symlink if they made one. Size is
// disclosed per snippet (warnContextSize), never capped short of the read
// ceiling.
func configContext(warn io.Writer, agentRoots []string, decls []config.ContextDecl) (string, error) {
	var b strings.Builder
	for _, cd := range decls {
		content := cd.Text
		if cd.File != "" {
			path, err := expandHome(cd.File)
			if err != nil {
				return "", fmt.Errorf("context %s: %w", cd.Name, err)
			}
			data, err := readBoundedHostFile(agentRoots, path)
			if err != nil {
				return "", fmt.Errorf("context %s: %s: %w", cd.Name, cd.File, err)
			}
			content = string(data)
		}
		warnContextSize(warn, cd.Name, len(content))
		if content == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(content)
	}
	return b.String(), nil
}

// readBoundedHostFile reads one host file under the technical read ceiling
// (fstat-judged before reading — see the tier constants), routed per
// configContext's containment rule.
func readBoundedHostFile(agentRoots []string, path string) ([]byte, error) {
	var f *os.File
	var fi os.FileInfo
	if agentRoot, rel, ok := anchorAgentWritable(agentRoots, path); ok {
		root, err := hostopen.PlainOpenRoot(agentRoot, hostopen.MountPoint)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		f, fi, err = hostopen.OpenRegularIn(root, rel)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		f, fi, err = hostopen.OpenRegular(path, true)
		if err != nil {
			return nil, err
		}
	}
	defer f.Close()
	if fi.Size() > contextReadCeiling {
		return nil, fmt.Errorf("%s is not agent-memory-sized — refusing to read it into the box's context (mount it into the box, or package a skill)", fmtSize(int(fi.Size())))
	}
	data, err := io.ReadAll(io.LimitReader(f, contextReadCeiling+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > contextReadCeiling {
		// The file grew past the ceiling between fstat and the read.
		return nil, fmt.Errorf("exceeds %d bytes (the read ceiling)", contextReadCeiling)
	}
	return data, nil
}

// expandHome expands a leading ~ against the current user's home and requires
// the result to be absolute (the shape config validation promised).
func expandHome(p string) (string, error) {
	p, err := config.ExpandTilde(p)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("path must be absolute or ~/…: %q", p)
	}
	return p, nil
}

// safeProjectPath resolves src (relative to projectDir) and confirms — after
// symlink resolution — that it stays within projectDir. Returns the real source
// path and the cleaned relative path.
func safeProjectPath(projectDir, src string) (real, rel string, err error) {
	if filepath.IsAbs(src) {
		return "", "", fmt.Errorf("source %q must be relative to the project dir", src)
	}
	clean := filepath.Clean(src)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("source %q escapes the project dir", src)
	}
	realDir, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return "", "", err
	}
	real, err = filepath.EvalSymlinks(filepath.Join(realDir, clean))
	if err != nil {
		// A missing source is overwhelmingly the real cause here, and the
		// raw lstat error named neither the entry nor a way out -- the
		// editor warns at declaration time, and the build failure owes the
		// same legibility to configs written by hand.
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("source %q is not in the project (create it, or remove the entry)", src)
		}
		return "", "", err
	}
	within, err := filepath.Rel(realDir, real)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("source %q escapes the project dir via symlink", src)
	}
	return real, clean, nil
}

// stageCopy realizes one copy job, anchoring the source at the agent-writable
// tree (agentRoot = WorkDir) whenever it lives inside it, so no agent-swappable
// ancestor is followed by pathname. A job may declare srcRoot itself (planFiles,
// always project-root relative); otherwise an ABSOLUTE src is routed here: if it
// resolves inside agentRoot (e.g. a project-local `[[claude_skills]].path`), it
// is anchored there too; only a source genuinely OUTSIDE the agent-writable tree
// (skills shipped from elsewhere on the host — the main worktree of a linked
// worktree is not agent-writable) falls through to the by-pathname copyPath.
// This ENFORCES — rather than assumes — that no by-pathname reopen happens for
// an agent-writable source.
func stageCopy(dstRoot *os.Root, agentRoots []string, j fileCopy) error {
	root, src := j.srcRoot, j.src
	if root == "" {
		if r, rel, ok := anchorAgentWritable(agentRoots, j.src); ok {
			root, src = r, rel
		}
	}
	if root == "" {
		return copyPath(j.src, dstRoot, j.staged, j.budget)
	}
	r, err := hostopen.PlainOpenRoot(root, hostopen.MountPoint)
	if err != nil {
		return err
	}
	defer r.Close()
	// The configured source itself (top level) may be a symlink the USER named;
	// safeProjectPath / validation already resolved it within the project, and
	// os.Root follows it while refusing escapes. Its interior is agent territory:
	// symlinks there are rejected (copyRootedEntry with topLevel=false).
	return copyRootedEntry(r, src, dstRoot, j.staged, true, j.budget)
}

// agentWritableRoots is the definition CLAUDE.md states -- "the project tree
// AND anything a box can shape" -- as one list the build path shares, instead
// of each caller assuming WorkDir is the whole of it. Members: the project
// tree; the common git dir of a linked worktree (bound rw into every worktree
// box); and every rw mount. A host read of a path under ANY of these must be
// anchored (openat, no by-pathname reopen), because a box can swap a
// component between validation and use.
//
// Ordering matters: the most specific root wins, so a path inside a rw mount
// nested under the project anchors at the mount. Read-only mounts are absent
// deliberately -- the box cannot shape them.
func agentWritableRoots(paths project.Paths, cfg config.Config, res skills.Resolved) []string {
	roots := []string{paths.WorkDir}
	if paths.CommonGitDirHost != "" {
		roots = append(roots, paths.CommonGitDirHost)
	}
	// The COMBINED mount set: a skill's rw mount is bound into the box just
	// like a config one, so it is equally shapeable. Classifying cfg.Mounts
	// alone would leave a whole class outside the predicate -- which is the
	// same partial-definition mistake this predicate exists to end.
	for _, m := range append(append([]config.Mount{}, cfg.Mounts...), res.Mounts()...) {
		if m.Disabled || m.Mode != "rw" {
			continue
		}
		if host, err := expandHome(m.Host); err == nil {
			roots = append(roots, host)
		}
	}
	// Longest first: the innermost containing root is the right anchor.
	sort.Slice(roots, func(i, j int) bool { return len(roots[i]) > len(roots[j]) })
	return roots
}

// anchorAgentWritable finds the agent-writable root containing path, if any,
// and returns it with the relative path to open through it.
func anchorAgentWritable(roots []string, path string) (root, rel string, ok bool) {
	for _, r := range roots {
		if rel, ok := agentWritableRel(r, path); ok {
			return r, rel, true
		}
	}
	return "", "", false
}

// agentWritableRel reports whether path is inside root, returning the relative
// path to anchor it at. It tries the LEXICAL spelling first: a path already
// spelled under root is anchored (openat), so an escaping intermediate component
// is REFUSED by os.Root rather than demoted to the by-pathname route — resolving
// first would send exactly that case to copyPath, since EvalSymlinks would land
// outside root. Only if the lexical spelling misses does it EvalSymlinks, to
// still catch a source spelled through a symlink alias of root (expandHome does
// not canonicalize; root — WorkDir — is already Canonicalize'd). Either way the
// resolve only ROUTES; os.Root re-resolves at open time.
func agentWritableRel(root, path string) (string, bool) {
	if rel, ok := withinRoot(root, path); ok {
		return rel, true
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return withinRoot(root, resolved)
	}
	return "", false
}

// withinRoot reports whether path (absolute) lies inside root (absolute,
// cleaned), returning the cleaned relative path if so. Purely lexical — the
// routing decision; os.Root enforces the actual openat containment, so a
// symlink under a lexically-contained path still cannot escape.
func withinRoot(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// copyRootedEntry copies the entry at rel (relative to root) into dst, opening
// every object THROUGH root (openat per component) so no pathname is re-resolved
// between classification and use and no component can escape the root. topLevel
// is the configured source itself — a user-named symlink there is followed;
// interior entries reject symlinks (agent-planted). The fd's fstat is the only
// thing trusted for the entry's type, and opens are O_NONBLOCK so a FIFO returns
// instead of blocking.
//
// internal/deliver's transport walks a tree this same way, and the duplication
// is deliberate: the two answer to different contracts. This one rejects and
// fails the whole staging; deliver skips the entry, reports it, and carries a
// count. The top-level symlink rule differs by ROUTE too -- followed here (the
// user named the `files` source), refused outright by copyPath. Both sites take
// their dangerous primitives from internal/hostopen, so only the POLICY is
// duplicated; drift in it is caught by the shared expectation table in
// internal/treecopytest (TestTreeCopyTableStageCopy, TestTreeCopyTableCopyPath,
// TestTreeCopyTableDeliverLocal).
func copyRootedEntry(root *os.Root, rel string, dstRoot *os.Root, dst string, topLevel bool, b *copyBudget) error {
	if !topLevel {
		// Lstat through the root to reject an interior symlink WITHOUT following
		// it: os.Root silently follows an in-root symlink on Open, and copyPath's
		// contract stages no symlinks (an escaping one is refused by the root
		// regardless; this also rejects an in-root one rather than dereferencing
		// it into the image).
		//
		// Residual (accepted): between this Lstat and the open below, an agent
		// could swap the entry to an in-root symlink, which os.Root would then
		// follow. That stages a DIFFERENT in-project file — content the agent
		// already controls, going into its own image — never a host-file escape
		// (an escaping swap is still refused by os.Root's openat). No gain to the
		// agent, so it is not worth openat+O_NOFOLLOW-per-component machinery
		// that os.Root does not expose.
		li, err := root.Lstat(rel)
		if err != nil {
			return err
		}
		if li.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink %s not allowed in `files` (copy plain files/dirs)", filepath.Join(root.Name(), rel))
		}
	}
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	switch {
	case fi.IsDir():
		if err := dstRoot.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		entries, err := f.ReadDir(-1)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyRootedEntry(root, filepath.Join(rel, e.Name()), dstRoot, filepath.Join(dst, e.Name()), false, b); err != nil {
				return err
			}
		}
		return nil
	case fi.Mode().IsRegular():
		return stageRegularFromFD(f, dstRoot, dst, b)
	default:
		return fmt.Errorf("%s is not a regular file (only plain files/dirs may be staged in `files`)", filepath.Join(root.Name(), rel))
	}
}

// copyPath copies a file or directory tree named by an ABSOLUTE pathname
// (skill sources, whose ancestors are outside the agent-writable project;
// `files` sources go through stageCopy/copyRootedEntry instead, anchored at the
// project root). Only plain files and directories are staged; symlinks and other
// non-regular files (FIFOs, devices, sockets) are rejected, so nothing can pull
// content from outside into the image or stall the rebuild.
//
// The project stays writable while a session runs, and `byre rebuild` can stage
// a context concurrently, so an agent can swap an entry between classification
// and copy (the check/open race). copyPath is race-hardened accordingly:
// interior entries of a directory are opened THROUGH an os.Root anchored at the
// directory (openat per component, never a re-walked pathname), which refuses
// any component that resolves outside the root — so a regular file swapped for
// an escaping symlink after classification cannot pull an external file in.
// Opens are O_NONBLOCK and the type is trusted only from the fd's fstat, so a
// swap to a FIFO returns instead of hanging and is rejected rather than staged.
// The duplication with internal/deliver's transport is deliberate and the two
// policies differ; copyRootedEntry states the terms. This route's share of the
// shared expectation table is posed by TestTreeCopyTableCopyPath.
func copyPath(src string, dstRoot *os.Root, dst string, b *copyBudget) error {
	info, err := hostopen.StatNoFollow(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symlink %s not allowed in `files` (copy plain files/dirs)", src)
	}
	if info.IsDir() {
		// Anchor a root at the directory (no-follow: a dir swapped to a symlink
		// after the Lstat above must not re-anchor the walk elsewhere) and copy
		// its interior through it. A concurrent swap of a deeper directory
		// component to an escaping symlink is refused by os.Root's
		// per-component openat, not re-resolved by name.
		root, err := hostopen.OpenDirRootNoFollow(src)
		if err != nil {
			if errors.Is(err, hostopen.ErrSymlinkRoot) {
				// copyPath's contract stages no symlinks; keep the `files` language.
				return fmt.Errorf("symlink %s not allowed in `files` (copy plain files/dirs)", src)
			}
			return err
		}
		defer root.Close()
		return copyRootedEntry(root, ".", dstRoot, dst, false, b)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file (only plain files/dirs may be staged in `files`)", src)
	}
	// Reopen the top-level file with O_NOFOLLOW|O_NONBLOCK and trust the fd's
	// stat, so a swap to a symlink or FIFO between the Lstat above and here is
	// rejected rather than followed or blocked on.
	in, _, err := hostopen.OpenRegular(src, false)
	if err != nil {
		return err
	}
	defer in.Close()
	return stageRegularFromFD(in, dstRoot, dst, b)
}

// stageRegularFromFD copies an already-open source file into dst with a
// NORMALIZED mode — 0644, or 0755 when the source carries any exec bit (the
// git rule). Only the exec bit is authored content; the rest of a host file's
// mode is the authoring machine's umask showing through, and letting it into
// the context makes the image vary by host and breaks dual-ship composition
// across provenances (ADR 0056 judges same-dest claims mode-exact; installed
// snapshots stage 0644 while a working-tree checkout carries whatever the
// author's umask left). Setuid/setgid/sticky are dropped with the rest.
//
// It re-checks the fd's type so no pathname is re-resolved: the
// top-level caller opens by name (a swap to a FIFO with O_NONBLOCK, or to a
// directory, opens successfully), so trusting the fd's fstat here — not a prior
// pathname Lstat — is what actually keeps a non-regular file out of the image.
//
// The copy is bounded at the size this fstat observed: the source is
// agent-writable, and an unbounded io.Copy of a file being appended to chases
// the writer indefinitely (the same stall class O_NONBLOCK closes for FIFOs).
// A source that grew or shrank mid-copy is refused rather than staged — the
// bytes would be a torn read either way, and the context must be deterministic.
func stageRegularFromFD(in *os.File, dstRoot *os.Root, dst string, b *copyBudget) error {
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file (only plain files/dirs may be staged in `files`)", in.Name())
	}
	// Charge at the fd's own fstat size -- the number copyExactly will hold
	// the copy to -- so the budget and the bytes can't disagree.
	if err := b.charge(fi.Size()); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if fi.Mode().Perm()&0o111 != 0 {
		mode = 0o755
	}
	o, err := dstRoot.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	// OpenFile's mode is filtered through this process's umask; the fchmod
	// sets the exact bits so byre's own umask can't leak into the image
	// either.
	if err := o.Chmod(mode); err != nil {
		o.Close()
		return err
	}
	return copyExactlyAndClose(o, in, fi.Size(), in.Name())
}

// copyExactlyAndClose is copyExactly plus the close of the destination, whose
// error is reported in its own right: a write-mode Close is where a failed
// final write surfaces (ENOSPC on the context dir is the everyday one), and a
// deferred, dropped one leaves a short file in the context that the build then
// bakes into the image as if it were whole. The copy's error wins when both
// fail -- it is the earlier and more specific one.
func copyExactlyAndClose(out io.WriteCloser, in io.Reader, size int64, name string) error {
	cerr := copyExactly(out, in, size, name)
	closeErr := out.Close()
	if cerr != nil {
		return cerr
	}
	if closeErr != nil {
		return fmt.Errorf("%s: writing it into the build context: %w", name, closeErr)
	}
	return nil
}

// copyExactly copies exactly size bytes from in to out, refusing a source that
// holds more or fewer. The limit makes a shrink visible (the copy falls short)
// but hides growth — one read past the promise tells them apart.
//
// deliver's REMOTE leg makes the same size promise at send time
// (internal/deliver/remote.go), separately and deliberately; deliver.local
// makes none, because it streams a descriptor into the box with nothing to
// hold the stream to. The shared expectation table (internal/treecopytest)
// marks growth N/A on every route — no route can pose a real mid-copy write
// deterministically — and the refusal here is pinned directly by
// TestCopyExactlyRefusesGrowthAndShrink and TestCopyExactlyRefusesMutatedSource.
func copyExactly(out io.Writer, in io.Reader, size int64, name string) error {
	n, err := io.Copy(out, io.LimitReader(in, size))
	if err != nil {
		return err
	}
	var extra int
	if n == size {
		// ReadFull, not a bare Read: it loops past a legal zero-byte read, and a
		// probe that fails outright must surface, not pass as "didn't grow".
		var b [1]byte
		var rerr error
		extra, rerr = io.ReadFull(in, b[:])
		if rerr != nil && rerr != io.EOF {
			return fmt.Errorf("%s: checking for growth past the observed size: %w", name, rerr)
		}
	}
	if n != size || extra > 0 {
		return fmt.Errorf("%s changed while being staged (observed %d bytes)", name, size)
	}
	return nil
}
