package commands

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
)

// PresetName is the conventional in-repo preset filename (D16a). byre.config
// is reserved for the box's live consent document and nothing else wears its
// name; a legacy-named repo byre.config is accepted as a preset with the
// rename note (D17).
const PresetName = "byre.preset"

// appliedRecord is the per-project marker `preset apply` writes (D16c step
// 6): line 1 = sha256 of the applied preset bytes, line 2 = its source
// (URI/path). The D17 drift states derive from it. Presets have no package
// identity or install lifecycle -- the project remembering what it applied is
// ordinary store state.
const appliedRecord = "applied"

// missingRef is one package reference a preset names that the catalog cannot
// resolve, with its kind-correct verb and any [sources] hint.
type missingRef struct {
	Name string
	Kind packages.Kind
	Hint *config.SourceHint
}

// PresetApply implements `byre preset apply [<uri>|<path>]` (D16c): fetch and
// validate the preset, chauffeur installs for missing packages (each its own
// consent; declining any is allowed), recompute, review, confirm, write.
func PresetApply(s Streams, projectDir, arg string) error {
	// Non-TTY apply refuses -- the review is the point (D16c).
	if !s.TTY {
		return fmt.Errorf("preset apply is interactive (the review is the point) -- run it on a TTY")
	}
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if err := paths.Bootstrap(); err != nil {
		return err
	}
	if err := builtins.EnsureStoreOut(paths.Home, s.Err); err != nil {
		return err
	}

	content, source, legacyName, err := readPreset(projectDir, arg)
	if err != nil {
		return err
	}
	if legacyName {
		fmt.Fprintf(s.Err, "byre: %s is the legacy name -- the convention is now %s (rename when convenient; both apply the same way).\n", config.ProjectConfigName, PresetName)
	}
	preset, err := parsePreset(content, source)
	if err != nil {
		return err
	}

	// Step 2: every missing package reference of any kind, with hints.
	missing, err := missingRefs(paths.Home, preset)
	if err != nil {
		return err
	}

	// Step 3: the chauffeur. Not the banned transitive install (which is
	// SILENT fetching); this is byre walking the user through N explicit
	// consents they solicited by invoking apply. Installs come BEFORE the
	// write so the preset's own not-yet-written references never make a
	// chauffeured install "activating" (other stored configs may still
	// trip D9b' inside the normal install flow, correctly).
	for _, m := range missing {
		if m.Hint == nil {
			fmt.Fprintf(s.Err, "byre: %s %q is not installed and the preset carries no [sources] hint -- install it yourself (byre %s install <manifest-url>) or continue without it.\n", m.Kind, m.Name, m.Kind)
			continue
		}
		fmt.Fprintf(s.Err, "\nbyre: the preset references %s %q, not installed. Its hint:\n", m.Kind, m.Name)
		if err := installForKind(s, m.Kind, m.Hint.URI, m.Hint.Digest); err != nil {
			// Declining (or a failed fetch) still completes the apply
			// honestly: the reference stays in the written config, marked in
			// the review, and the box fails loudly at develop with the D9e
			// remedy.
			fmt.Fprintf(s.Err, "byre: %q not installed (%v) -- continuing; the review marks it.\n", m.Name, err)
		}
	}

	// Steps 4-5: rebuild the catalog, recompute, show the final review.
	still, err := missingRefs(paths.Home, preset)
	if err != nil {
		return err
	}
	renderPresetReview(s, paths, projectDir, preset, content, still, "Apply")

	// Step 6: confirm; write the reviewed bytes as the project's byre.config
	// and record the applied marker. Same discipline as every store write:
	// under the setup lock, re-read, and only land the bytes just reviewed.
	fmt.Fprint(s.Err, "Apply this preset? byre.config will be replaced. [y/N] ")
	if !confirmed(s.In) {
		fmt.Fprintln(s.Err, "byre: not applied; nothing written.")
		return nil
	}
	h := packages.HashBytes(content)
	storePath := filepath.Join(paths.Dir, config.ProjectConfigName)
	return withSetupLock(s.Err, paths.LockFile, func() error {
		if cur, _, _, rerr := readPreset(projectDir, arg); rerr == nil && packages.HashBytes(cur) != h {
			// Only re-checkable for path sources that still exist; a changed
			// file must not land bytes the human did not review.
			return fmt.Errorf("%s changed while you were reviewing; re-run preset apply", source)
		}
		if err := config.AtomicWrite(storePath, string(content)); err != nil {
			return err
		}
		if err := config.AtomicWrite(filepath.Join(paths.Dir, appliedRecord), h+"\n"+source); err != nil {
			return err
		}
		fmt.Fprintf(s.Err, "byre: applied %s into %s\n", source, storePath)
		return nil
	})
}

// PresetInspect implements `byre preset inspect [<uri>|<path>]`: the D16c
// review without the chauffeur and without the write. Read-only, so it works
// in a pipe.
func PresetInspect(s Streams, projectDir, arg string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if err := builtins.EnsureStoreOut(paths.Home, s.Err); err != nil {
		return err
	}
	content, source, legacyName, err := readPreset(projectDir, arg)
	if err != nil {
		return err
	}
	if legacyName {
		fmt.Fprintf(s.Err, "byre: %s is the legacy name -- the convention is now %s.\n", config.ProjectConfigName, PresetName)
	}
	preset, err := parsePreset(content, source)
	if err != nil {
		return err
	}
	missing, err := missingRefs(paths.Home, preset)
	if err != nil {
		return err
	}
	renderPresetReview(s, paths, projectDir, preset, content, missing, "Inspect")
	// Reports and exact commands, never prompts: a third party's document
	// introducing references gets a report, not a walk-through (D16c).
	for _, m := range missing {
		if m.Hint != nil {
			fmt.Fprintf(s.Out, "  install it: %s\n", m.Hint.InstallHint(string(m.Kind)))
		}
	}
	fmt.Fprintln(s.Out, "Nothing written. `byre preset apply` reviews again and writes byre.config on confirm.")
	return nil
}

// readPreset locates and fetches preset bytes: an explicit path/URI argument,
// or the conventional ./byre.preset, or (legacy, with the rename note) a repo
// ./byre.config. https fetches ride the hardened package fetcher and its
// bounds (D1h).
func readPreset(projectDir, arg string) (content []byte, source string, legacyName bool, err error) {
	if arg == "" {
		p := filepath.Join(projectDir, PresetName)
		if _, statErr := os.Stat(p); statErr == nil {
			b, err := os.ReadFile(p)
			return b, p, false, err
		}
		legacy := filepath.Join(projectDir, config.ProjectConfigName)
		if _, statErr := os.Stat(legacy); statErr == nil {
			b, err := os.ReadFile(legacy)
			return b, legacy, true, err
		}
		return nil, "", false, fmt.Errorf("no %s here (and no legacy %s); pass a path or URI", PresetName, config.ProjectConfigName)
	}
	kind, err := packages.ParseSourceURI(arg)
	if err != nil {
		return nil, "", false, err
	}
	if kind == "https" {
		var f packages.Fetcher
		b, _, err := f.FetchManifest(arg)
		if err != nil {
			return nil, "", false, err
		}
		return b, arg, false, nil
	}
	b, err := os.ReadFile(strings.TrimPrefix(arg, "file://"))
	if err != nil {
		return nil, "", false, err
	}
	return b, arg, strings.HasSuffix(arg, config.ProjectConfigName), nil
}

// parsePreset strict-parses preset bytes as one config layer. A preset is a
// byre.config-format file, not a package (D16a): no [package] header.
func parsePreset(content []byte, source string) (config.Config, error) {
	c, err := config.Parse(content)
	if err != nil {
		return config.Config{}, fmt.Errorf("%s: %w", packages.EscapeTerminal(source), err)
	}
	if err := c.ValidateLayer(); err != nil {
		return config.Config{}, fmt.Errorf("%s: %w", packages.EscapeTerminal(source), err)
	}
	return c, nil
}

// missingRefs collects every package reference the preset names that the
// catalog cannot resolve -- skills, the selected template, the agent (D16c
// step 2) -- with their [sources] hints. Removal markers are skipped:
// removing something absent is a no-op, not an acquisition.
func missingRefs(home string, preset config.Config) ([]missingRef, error) {
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		return nil, err
	}
	hintFor := func(canon string) *config.SourceHint {
		if h, ok := preset.Sources[cat.ExpandAlias(canon)]; ok {
			h.From = "preset"
			return &h
		}
		return nil
	}
	var out []missingRef
	check := func(name string, kind packages.Kind) {
		name = strings.TrimSpace(name)
		if name == "" || name == config.NoneLabel || strings.HasPrefix(name, "!") {
			return
		}
		if _, err := cat.ResolveName(name); err != nil {
			out = append(out, missingRef{Name: cat.ExpandAlias(name), Kind: kind, Hint: hintFor(name)})
		}
	}
	check(preset.Template, packages.KindTemplate)
	check(preset.Agent, packages.KindSkill)
	for _, sk := range preset.Skills {
		check(sk, packages.KindSkill)
	}
	return out, nil
}

// installForKind runs the normal, kind-specific install flow (D16c step 3):
// manifest fetched, its own grant summary, its own confirm, digest verified.
func installForKind(s Streams, kind packages.Kind, uri, digest string) error {
	if kind == packages.KindTemplate {
		return TemplateInstall(s, uri, digest, false)
	}
	return SkillInstall(s, uri, digest, false)
}

// renderPresetReview is D16c step 5: the grant summary of every key and every
// referenced package, provenance-labeled; still-missing references are marked
// "not installed -- grants unknown" (the review never claims completeness it
// does not have); against an existing byre.config the review shows the diff.
func renderPresetReview(s Streams, paths project.Paths, projectDir string, preset config.Config, content []byte, missing []missingRef, verb string) {
	cfg, grants := effectiveReview(paths, preset)
	fmt.Fprintf(s.Err, "\n%s preset -- the box this composes:\n", verb)
	fmt.Fprintf(s.Err, "  base=%s  agent=%s  template=%s\n", config.OrNone(cfg.Base), config.OrNone(cfg.Agent), config.OrNone(preset.Template))
	for _, g := range grants {
		line := g.Text
		if (g.Containment || g.CrossProject) && s.TTY {
			line = "\x1b[1;33m" + line + "\x1b[0m"
		}
		fmt.Fprintf(s.Err, "  ⚠ %s\n", line)
	}
	for _, m := range missing {
		fmt.Fprintf(s.Err, "  ⚠ %s %s: not installed -- grants unknown\n", m.Kind, packages.EscapeTerminal(m.Name))
	}
	storePath := filepath.Join(paths.Dir, config.ProjectConfigName)
	if store, serr := os.ReadFile(storePath); serr == nil {
		if bytes.Equal(store, content) {
			fmt.Fprintf(s.Err, "--- identical to your current byre.config (applying just records that) ---\n")
		} else {
			fmt.Fprintln(s.Err, "Changes vs your current byre.config -- applying replaces the whole file:")
			for _, l := range unifiedDiff("your current config", "preset", string(store), string(content)) {
				fmt.Fprintln(s.Err, l)
			}
			fmt.Fprintln(s.Err, "------")
		}
	} else {
		fmt.Fprintf(s.Err, "--- preset ---\n%s\n------\n", strings.TrimRight(packages.EscapeTerminal(string(content)), "\n"))
	}
}

// presetState reports the D17 drift state of a repo-shipped preset relative
// to the applied marker: "" (no preset file), "unapplied" (state 1),
// "applied" (state 2, steady -- no noise), "diverged" (state 3). legacyName
// is true when the repo file wears the legacy byre.config name. The wording
// claims only what the marker proves: the preset file versus the version you
// applied. Live-config edits are yours, not drift.
func presetState(projectDir string, paths project.Paths) (state string, legacyName bool) {
	p := filepath.Join(projectDir, PresetName)
	content, err := os.ReadFile(p)
	if err != nil {
		p = filepath.Join(projectDir, config.ProjectConfigName)
		content, err = os.ReadFile(p)
		if err != nil {
			return "", false
		}
		legacyName = true
	}
	rec, err := os.ReadFile(filepath.Join(paths.Dir, appliedRecord))
	if err != nil {
		return "unapplied", legacyName
	}
	recHash, _, _ := strings.Cut(strings.TrimSpace(string(rec)), "\n")
	if strings.TrimSpace(recHash) == packages.HashBytes(content) {
		return "applied", legacyName
	}
	return "diverged", legacyName
}

// presetNote renders the passive develop-preamble / status note for states 1
// and 3 (state 2 is silent). Never a question (D17): a third party's document
// gets a report and an exact command, not a prompt.
func presetNote(projectDir string, paths project.Paths) string {
	state, legacyName := presetState(projectDir, paths)
	name := PresetName
	renameHint := ""
	if legacyName {
		name = config.ProjectConfigName
		renameHint = " (legacy name; the convention is " + PresetName + ")"
	}
	switch state {
	case "unapplied":
		return fmt.Sprintf("this repo ships a %s%s (not applied); `byre preset apply` to review it", name, renameHint)
	case "diverged":
		return fmt.Sprintf("the repo's %s%s differs from the version you applied; `byre preset apply` to review the changes", name, renameHint)
	}
	return ""
}
