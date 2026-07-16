package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
)

// PresetName is the conventional in-repo preset filename. byre.config
// is reserved for the box's live consent document and nothing else wears its
// name; a legacy-named repo byre.config is accepted as a preset with the
// rename note.
const PresetName = "byre.preset"

// appliedRecord is the per-project marker `preset apply` writes (apply step
// 6): line 1 = sha256 of the applied preset bytes, line 2 = its source
// (URI/path). The drift states derive from it. Presets have no package
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

// PresetApply implements `byre preset apply [<uri>|<path>]`: fetch and
// validate the preset, chauffeur installs for missing packages (each its own
// consent; declining any is allowed), recompute, review, confirm, write.
func PresetApply(s Streams, projectDir, arg string) error {
	// Non-TTY apply refuses -- the review is the point.
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

	// A preset's extends chain must resolve BEFORE anything else: the layers
	// feed the grant review, so a missing one can't be a warn-and-continue —
	// the review would vouch for a box it hasn't seen. Layers aren't
	// packages: no chauffeured install, just the exact path to create (the
	// chain walk's own error).
	if preset.Extends != "" {
		cat, cerr := builtins.LoadCatalogRaw(paths.Home)
		if cerr != nil {
			return cerr
		}
		if _, cerr := config.LoadExtendsChain(paths.Home, cat, preset.Extends); cerr != nil {
			return fmt.Errorf("this preset extends a layer this machine doesn't have: %w", cerr)
		}
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
	// trip install-as-activation inside the normal install flow, correctly).
	for _, m := range missing {
		if m.Hint == nil {
			fmt.Fprintf(s.Err, "byre: %s %q is not installed and the preset carries no [sources] hint -- install it yourself (byre %s install <manifest-url>) or continue without it.\n", m.Kind, m.Name, m.Kind)
			continue
		}
		fmt.Fprintf(s.Err, "\nbyre: the preset references %s %q, not installed. Its hint:\n", m.Kind, m.Name)
		if err := installForKind(s, m.Kind, m.Hint.URI, m.Hint.Digest); err != nil {
			// Declining (or a failed fetch) still completes the apply
			// honestly: the reference stays in the written config, marked in
			// the review, and the box fails loudly at develop with the
			// reinstall remedy.
			fmt.Fprintf(s.Err, "byre: %q not installed (%v) -- continuing; the review marks it.\n", m.Name, err)
		}
	}

	// Steps 4-5: rebuild the catalog, recompute, show the final review.
	still, err := missingRefs(paths.Home, preset)
	if err != nil {
		return err
	}
	// The diff the user reviews is against THIS store config; capture ONE
	// snapshot that feeds both the renderer and the locked compare, so the
	// landing step can prove the review is still true (the config editor,
	// --self-edit, or another byre may write it meanwhile). Unreadable-but-
	// present is an abort, not "no config": replacing a file we could not
	// show the user is exactly the unseen overwrite this flow forbids.
	storePath := filepath.Join(paths.Dir, config.ProjectConfigName)
	reviewedStore, reviewedStoreErr := os.ReadFile(storePath)
	if reviewedStoreErr != nil && !os.IsNotExist(reviewedStoreErr) {
		return fmt.Errorf("cannot read this project's byre.config for the review diff: %w", reviewedStoreErr)
	}
	hasStore := reviewedStoreErr == nil
	renderPresetReview(s, paths, preset, content, still, "Apply", reviewedStore, hasStore)

	// Step 6: confirm; write the reviewed bytes as the project's byre.config
	// and record the applied marker. Same discipline as every store write:
	// under the setup lock, re-read, and only land the bytes just reviewed.
	fmt.Fprint(s.Err, "Apply this preset? byre.config will be replaced. [y/N] ")
	if !confirmed(s.In) {
		fmt.Fprintln(s.Err, "byre: not applied; nothing written.")
		return nil
	}
	h := packages.HashBytes(content)
	return withSetupLock(s.Err, paths.LockFile, func() error {
		if cur, _, _, rerr := readPreset(projectDir, arg); rerr == nil && packages.HashBytes(cur) != h {
			// Only re-checkable for path sources that still exist; a changed
			// file must not land bytes the human did not review.
			return fmt.Errorf("%s changed while you were reviewing; re-run preset apply", source)
		}
		// The reviewed diff must still be true: consent was to replacing THAT
		// config, not whatever landed since (config editor, --self-edit,
		// another byre process). Any read failure here -- including a config
		// that appeared or vanished -- aborts.
		curStore, curErr := os.ReadFile(storePath)
		if curErr != nil && !os.IsNotExist(curErr) {
			return fmt.Errorf("cannot re-read this project's byre.config under the lock: %w", curErr)
		}
		if hasStore != (curErr == nil) || (curErr == nil && !bytes.Equal(curStore, reviewedStore)) {
			return fmt.Errorf("this project's byre.config changed while you were reviewing; re-run preset apply to review against the current config")
		}
		if err := config.AtomicWrite(storePath, string(content)); err != nil {
			return err
		}
		if err := config.AtomicWrite(filepath.Join(paths.Dir, appliedRecord), h+"\n"+source); err != nil {
			// The config landed; only the marker failed. Say exactly that --
			// drift will read "unapplied/diverged" until a re-apply records it.
			return fmt.Errorf("byre.config was applied, but recording the applied marker failed (%w) -- re-run preset apply to record it", err)
		}
		fmt.Fprintf(s.Err, "byre: applied %s into %s\n", source, storePath)
		return nil
	})
}

// PresetInspect implements `byre preset inspect [<uri>|<path>]`: the apply
// review without the chauffeur and without the write. GENUINELY read-only --
// no store-ensure (which would regenerate the mirror and run the record
// sweep); the catalog is built from what exists -- so "Nothing written" is
// true and a piped inspection mutates nothing.
func PresetInspect(s Streams, projectDir, arg string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
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
	inspStore, inspErr := os.ReadFile(filepath.Join(paths.Dir, config.ProjectConfigName))
	if inspErr != nil && !os.IsNotExist(inspErr) {
		// Only absence means "no current config" -- a permission or I/O
		// failure must not silently omit the promised diff.
		return fmt.Errorf("cannot read this project's byre.config for the review diff: %w", inspErr)
	}
	renderPresetReview(s, paths, preset, content, missing, "Inspect", inspStore, inspErr == nil)
	// Reports and exact commands, never prompts: a third party's document
	// introducing references gets a report, not a walk-through.
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
// bounds.
func readPreset(projectDir, arg string) (content []byte, source string, legacyName bool, err error) {
	if arg == "" {
		// Conventional discovery still rides the hardened fetcher below --
		// a cloned repo's preset is third-party input and gets the same
		// 256KiB bound as an explicit source.
		p := filepath.Join(projectDir, PresetName)
		if _, statErr := os.Stat(p); statErr == nil {
			var f packages.Fetcher
			b, _, err := f.FetchManifest(p)
			return b, p, false, err
		}
		legacy := filepath.Join(projectDir, config.ProjectConfigName)
		if _, statErr := os.Stat(legacy); statErr == nil {
			var f packages.Fetcher
			b, _, err := f.FetchManifest(legacy)
			return b, legacy, true, err
		}
		return nil, "", false, fmt.Errorf("no %s here (and no legacy %s); pass a path or URI", PresetName, config.ProjectConfigName)
	}
	// Every explicit source rides the hardened package fetcher: https gets
	// the fetcher's bounds and origin rules; file:/paths get the real file-URI
	// parse (localhost-only) and the same size bound -- never a raw
	// prefix-trimmed ReadFile.
	if _, err := packages.ParseSourceURI(arg); err != nil {
		return nil, "", false, err
	}
	var f packages.Fetcher
	b, _, err := f.FetchManifest(arg)
	if err != nil {
		return nil, "", false, err
	}
	return b, arg, filepath.Base(arg) == config.ProjectConfigName, nil
}

// parsePreset strict-parses preset bytes as one config layer. A preset is a
// byre.config-format file, not a package: no [package] header.
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
// catalog cannot resolve -- skills, the selected template, the agent (apply
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

// installForKind runs the normal, kind-specific install flow (apply step 3):
// manifest fetched, its own grant summary, its own confirm, digest verified.
func installForKind(s Streams, kind packages.Kind, uri, digest string) error {
	if kind == packages.KindTemplate {
		return TemplateInstall(s, uri, digest, false)
	}
	return SkillInstall(s, uri, digest, false)
}

// renderPresetReview is apply step 5: the grant summary of every key and every
// referenced package, provenance-labeled; still-missing references are marked
// "not installed -- grants unknown" (the review never claims completeness it
// does not have); against an existing byre.config the review shows the diff.
func renderPresetReview(s Streams, paths project.Paths, preset config.Config, content []byte, missing []missingRef, verb string, store []byte, hasStore bool) {
	cfg, grants := effectiveReview(paths, preset)
	fmt.Fprintf(s.Err, "\n%s preset -- the box this composes:\n", verb)
	// Every rendered field below can carry preset-controlled bytes:
	// escape BEFORE byre's own styling so hostile run_args/mount paths/skill
	// names cannot forge grant rows or extra lines in the consent review.
	fmt.Fprintf(s.Err, "  base=%s  agent=%s  template=%s\n",
		packages.EscapeTerminal(config.OrNone(cfg.Base)),
		packages.EscapeTerminal(config.OrNone(cfg.Agent)),
		packages.EscapeTerminal(config.OrNone(preset.Template)))
	if preset.Extends != "" {
		// The resolved chain, root-first, the project last (merge order).
		// Best-effort here: apply hard-failed on a broken chain already, and
		// inspect's review carries the walk error in its cascade fallback.
		cat, _ := builtins.LoadCatalogRaw(paths.Home)
		if chain, cerr := config.LoadExtendsChain(paths.Home, cat, preset.Extends); cerr == nil {
			fmt.Fprintf(s.Err, "  extends: %s -> project\n",
				packages.EscapeTerminal(strings.Join(config.ChainNames(chain), " -> ")))
		}
	}
	for _, g := range grants {
		line := packages.EscapeTerminal(g.Text)
		if (g.Containment || g.CrossProject) && s.TTY {
			line = "\x1b[1;33m" + line + "\x1b[0m"
		}
		fmt.Fprintf(s.Err, "  ⚠ %s\n", line)
	}
	for _, m := range missing {
		fmt.Fprintf(s.Err, "  ⚠ %s %s: not installed -- grants unknown\n", m.Kind, packages.EscapeTerminal(m.Name))
	}
	if hasStore {
		if bytes.Equal(store, content) {
			fmt.Fprintf(s.Err, "--- identical to your current byre.config (applying just records that) ---\n")
		} else {
			fmt.Fprintln(s.Err, "Changes vs your current byre.config -- applying replaces the whole file:")
			for _, l := range unifiedDiff("your current config", "preset", string(store), string(content)) {
				// Diff lines carry hostile preset bytes too.
				fmt.Fprintln(s.Err, packages.EscapeTerminal(l))
			}
			fmt.Fprintln(s.Err, "------")
		}
	} else {
		fmt.Fprintf(s.Err, "--- preset ---\n%s\n------\n", escapeMultiline(string(content)))
	}
}

// escapeMultiline terminal-escapes hostile text LINE BY LINE -- EscapeTerminal
// strips every control character including newlines, which would collapse a
// rendered file body into one unreadable run.
func escapeMultiline(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, l := range lines {
		lines[i] = packages.EscapeTerminal(l)
	}
	return strings.Join(lines, "\n")
}

// presetState reports the drift state of a repo-shipped preset relative
// to the applied marker: "" (no preset file), "unapplied" (state 1),
// "applied" (state 2, steady -- no noise), "diverged" (state 3). legacyName
// is true when the repo file wears the legacy byre.config name. The wording
// claims only what the marker proves: the preset file versus the version you
// applied. Live-config edits are yours, not drift.
func presetState(projectDir string, paths project.Paths) (state string, legacyName bool) {
	p := filepath.Join(projectDir, PresetName)
	content, err := readPresetBounded(p)
	if err != nil {
		if !os.IsNotExist(err) {
			return stateSansContent(paths), false
		}
		p = filepath.Join(projectDir, config.ProjectConfigName)
		content, err = readPresetBounded(p)
		if err != nil {
			if !os.IsNotExist(err) {
				return stateSansContent(paths), true
			}
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

// stateSansContent is the drift state for a preset that exists but whose
// bytes cannot be inspected (unreadable, or over the manifest bound): whatever
// it holds provably is not the version any marker recorded -- apply enforces
// the same bound -- but an existing marker still proves an application
// happened, so the honest state is diverged, not never-applied.
func stateSansContent(paths project.Paths) string {
	if _, err := os.Stat(filepath.Join(paths.Dir, appliedRecord)); err == nil {
		return "diverged"
	}
	return "unapplied"
}

// readPresetBounded reads a local preset file under the same size bound the
// fetcher applies to manifests. The PASSIVE drift check runs on every
// develop/status -- before anyone asked byre to read the repo's preset -- so
// a cloned repository must not make it allocate an arbitrarily large file.
// The stat gate is advisory; the limited read is what actually bounds it.
func readPresetBounded(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil {
		return nil, err
	} else if fi.Size() > packages.MaxManifestBytes {
		return nil, fmt.Errorf("%s is %d bytes (limit %d)", p, fi.Size(), packages.MaxManifestBytes)
	}
	b, err := io.ReadAll(io.LimitReader(f, packages.MaxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > packages.MaxManifestBytes {
		return nil, fmt.Errorf("%s exceeds the %d byte limit", p, packages.MaxManifestBytes)
	}
	return b, nil
}

// presetNote renders the passive develop-preamble / status note for states 1
// and 3 (state 2 is silent). Never a question: a third party's document
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
