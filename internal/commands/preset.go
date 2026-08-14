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
	"github.com/pjlsergeant/byre/internal/hostopen"
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
// errReviewDiffRead is the shared spelling for both preset paths that need
// the stored config to build a review diff.
const errReviewDiffRead = "cannot read this project's byre.config for the review diff: %w"

func PresetApply(s Streams, projectDir, arg string) error {
	// Non-TTY apply refuses -- the review is the point.
	if !s.TTY {
		return fmt.Errorf("preset apply is interactive (the review is the point) -- run it on a TTY")
	}
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	// Collision check loudly up front; the enrolling Bootstrap waits for the
	// confirmed write below — declining the review is a first-class outcome
	// ("not applied; nothing written") and must leave a never-seen project
	// un-enrolled in ~/.byre/projects.
	if err := paths.ValidateExisting(); err != nil {
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
		dataf(s.Err, "byre: %s is the legacy name -- the convention is now %s (rename when convenient; both apply the same way).\n", config.ProjectConfigName, PresetName)
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
			dataf(s.Err, "byre: %s %q is not installed and the preset carries no [sources] hint -- install it yourself (byre %s install <manifest-url>) or continue without it.\n", m.Kind, m.Name, m.Kind)
			continue
		}
		dataf(s.Err, "\nbyre: the preset references %s %q, not installed. Its hint:\n", m.Kind, m.Name)
		if err := installForKind(s, m.Kind, m.Hint.URI, m.Hint.Digest); err != nil {
			// Declining (or a failed fetch) still completes the apply
			// honestly: the reference stays in the written config, marked in
			// the review, and the box fails loudly at develop with the
			// reinstall remedy.
			dataf(s.Err, "byre: %q not installed (%v) -- continuing; the review marks it.\n", m.Name, err)
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
	// paths.Dir is the store dir the --self-edit mount exposes: fd-judged,
	// no-follow, bounded (the hostopen rule) -- same trust class as loadFile.
	reviewedStore, reviewedStoreErr := hostopen.ReadFileBounded(storePath, false, config.MaxConfigBytes)
	if reviewedStoreErr != nil && !os.IsNotExist(reviewedStoreErr) {
		return fmt.Errorf(errReviewDiffRead, reviewedStoreErr)
	}
	hasStore := reviewedStoreErr == nil
	renderPresetReview(s, paths, preset, content, still, "Apply", reviewedStore, hasStore)

	// Step 6: confirm; write the reviewed bytes as the project's byre.config
	// and record the applied marker. Same discipline as every store write:
	// under the setup lock, re-read, and only land the bytes just reviewed.
	if !confirmed(s.Err, s.In, "Apply this preset? byre.config will be replaced. [y/N] ") {
		fmt.Fprintln(s.Err, "byre: not applied; nothing written.")
		return nil
	}
	// The write was just confirmed — enroll (dir + path record) before taking
	// the setup lock, which lives in the store. Accepted cost: if the locked
	// re-checks below abort (source or store changed mid-review), the project
	// stays enrolled despite no write landing — consent to the write was
	// given, and the lock cannot exist without the store.
	if err := paths.Bootstrap(); err != nil {
		return err
	}
	h := packages.HashBytes(content)
	return withSetupLockProject(s.Err, paths, func() error {
		// Bootstrap preceded the lock. A concurrent forget that won the lock is
		// cancellation, not permission to recreate config in a recordless store.
		if err := requireRecorded(paths); err != nil {
			return err
		}
		if cur, _, _, rerr := readPreset(projectDir, arg); rerr == nil && packages.HashBytes(cur) != h {
			// Only re-checkable for path sources that still exist; a changed
			// file must not land bytes the human did not review.
			return fmt.Errorf("%s changed while you were reviewing; re-run preset apply", source)
		}
		// The reviewed diff must still be true: consent was to replacing THAT
		// config, not whatever landed since (config editor, --self-edit,
		// another byre process). Any read failure here -- including a config
		// that appeared or vanished -- aborts.
		curStore, curErr := hostopen.ReadFileBounded(storePath, false, config.MaxConfigBytes)
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
		dataf(s.Err, "byre: applied %s into %s\n", source, storePath)
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
	// Read-only, but collision-checked like status: the drift verdict and
	// review diff must be against THIS project's store, not a collider's.
	if err := paths.ValidateExisting(); err != nil {
		return err
	}
	content, source, legacyName, err := readPreset(projectDir, arg)
	if err != nil {
		return err
	}
	if legacyName {
		dataf(s.Err, "byre: %s is the legacy name -- the convention is now %s.\n", config.ProjectConfigName, PresetName)
	}
	preset, err := parsePreset(content, source)
	if err != nil {
		return err
	}
	missing, err := missingRefs(paths.Home, preset)
	if err != nil {
		return err
	}
	inspStore, inspErr := hostopen.ReadFileBounded(filepath.Join(paths.Dir, config.ProjectConfigName), false, config.MaxConfigBytes)
	if inspErr != nil && !os.IsNotExist(inspErr) {
		// Only absence means "no current config" -- a permission or I/O
		// failure must not silently omit the promised diff.
		return fmt.Errorf(errReviewDiffRead, inspErr)
	}
	renderPresetReview(s, paths, preset, content, missing, "Inspect", inspStore, inspErr == nil)
	// Reports and exact commands, never prompts: a third party's document
	// introducing references gets a report, not a walk-through.
	for _, m := range missing {
		if m.Hint != nil {
			// The hint escapes the URI and digest it quotes; the funnel covers the
			// rest of the line, attribution included.
			dataf(s.Out, "  install it: %s\n", m.Hint.InstallHint(string(m.Kind)))
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
		// Conventional discovery: byre DERIVED this path from the cwd, so
		// nobody named it, and it gets the passive probe's no-follow read
		// rather than the fetcher's follow. Following here contradicted the
		// drift probe -- which refuses a symlinked byre.preset and then
		// prints "run byre preset apply to review it", steering the user
		// into the one flow that would follow the link. An explicit path
		// argument below still follows: there the user really did name it.
		// Only a PROVABLE absence may fall through to the next candidate: the
		// tail of this branch tells the user there is no preset here, and a
		// probe that could not look has not established that.
		p := filepath.Join(projectDir, PresetName)
		ok, perr := hostopen.ExistsNoFollow(p)
		if perr != nil {
			return nil, "", false, fmt.Errorf("cannot tell whether %s is here: %w", p, perr)
		}
		if ok {
			b, err := readPresetDerived(p)
			return b, p, false, err
		}
		legacy := filepath.Join(projectDir, config.ProjectConfigName)
		ok, perr = hostopen.ExistsNoFollow(legacy)
		if perr != nil {
			return nil, "", false, fmt.Errorf("cannot tell whether %s is here: %w", legacy, perr)
		}
		if ok {
			b, err := readPresetDerived(legacy)
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

// readPresetDerived reads a preset byre found ITSELF (no path argument),
// under the same no-follow bound the passive probe uses. A symlink gets the
// explicit remedy rather than a bare refusal: naming the path is exactly
// what turns this into a followable source.
func readPresetDerived(p string) ([]byte, error) {
	b, err := readPresetBounded(p)
	if err == nil {
		return b, nil
	}
	// Classify for the MESSAGE only -- the refusal itself is the no-follow
	// open above, which holds whatever this Lstat says. A symlink surfaces as
	// ELOOP rather than ErrNotRegular, so the errno is not the thing to read.
	if fi, lerr := hostopen.StatNoFollow(p); lerr == nil && (fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular()) {
		return nil, fmt.Errorf("%s is not a regular file (a symlink, FIFO, or device) -- byre found this path itself, so it will not follow it; to use it anyway, name it: byre preset apply %s", p, p)
	}
	return nil, err
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
		if name == "" || name == config.NoneLabel || config.IsRemoval(name) {
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
		return PackageInstall(s, packages.KindTemplate, uri, digest, false)
	}
	return PackageInstall(s, packages.KindSkill, uri, digest, false)
}

// renderPresetReview is apply step 5: the grant summary of every key and every
// referenced package, provenance-labeled; still-missing references are marked
// "not installed -- grants unknown" (the review never claims completeness it
// does not have); against an existing byre.config the review shows the diff.
func renderPresetReview(s Streams, paths project.Paths, preset config.Config, content []byte, missing []missingRef, verb string, store []byte, hasStore bool) {
	cfg, grants := effectiveReview(paths, preset)
	// The credential annotation is a DIFF, so it cannot come off the resolved
	// proposal the rest of the summary is built from: what a reader needs is
	// which values and which identity moved, and both sides' raw bytes are the
	// only place that is legible. A missing store is the first apply -- every
	// credential in the preset is new, which is exactly what the lines say.
	grants = sortGrantLines(append(grants, credentialReviewLines(store, content)...))
	dataf(s.Err, "\n%s preset -- the box this composes:\n", verb)
	// Every rendered field below can carry preset-controlled bytes: the funnel
	// renders them as data so hostile run_args/mount paths/skill names cannot
	// forge grant rows or extra lines in the consent review.
	dataf(s.Err, "  base=%s  agent=%s  template=%s\n",
		config.OrNone(cfg.Base), config.OrNone(cfg.Agent), config.OrNone(preset.Template))
	if preset.Extends != "" {
		// The resolved chain, root-first, the project last (merge order).
		// Best-effort here: apply hard-failed on a broken chain already, and
		// inspect's review carries the walk error in its cascade fallback.
		cat, _ := builtins.LoadCatalogRaw(paths.Home)
		if chain, cerr := config.LoadExtendsChain(paths.Home, cat, preset.Extends); cerr == nil {
			dataf(s.Err, "  extends: %s -> project\n", strings.Join(config.ChainNames(chain), " -> "))
		}
	}
	for _, g := range grants {
		// Escaped BEFORE byre's own styling, and passed as escaped() so the
		// funnel keeps the highlight: this is the one report line in the
		// package that is MEANT to carry an escape sequence.
		line := packages.EscapeTerminal(g.Text)
		if (g.Containment || g.CrossProject || g.Credential) && s.TTY {
			line = "\x1b[1;33m" + line + "\x1b[0m"
		}
		dataf(s.Err, "  ⚠ %s\n", escaped(line))
	}
	for _, m := range missing {
		dataf(s.Err, "  ⚠ %s %s: not installed -- grants unknown\n", m.Kind, m.Name)
	}
	if hasStore {
		if bytes.Equal(store, content) {
			fmt.Fprintf(s.Err, "--- identical to your current byre.config (applying just records that) ---\n")
		} else {
			fmt.Fprintln(s.Err, "Changes vs your current byre.config -- applying replaces the whole file:")
			for _, l := range unifiedDiff("your current config", "preset", string(store), string(content)) {
				// Diff lines carry hostile preset bytes too.
				dataf(s.Err, "%s\n", l)
			}
			fmt.Fprintln(s.Err, "------")
		}
	} else {
		dataf(s.Err, "--- preset ---\n%s\n------\n", escaped(EscapeMultiline(string(content))))
	}
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
	rec, err := hostopen.ReadFileBounded(filepath.Join(paths.Dir, appliedRecord), false, config.MaxConfigBytes)
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
	// Only a PROVABLE absence earns "unapplied": that word claims the preset
	// was never applied here, and a probe that merely could not look has not
	// established it. Both states print a passive note, so degrading to
	// "diverged" costs the user nothing and asserts nothing false.
	ok, err := hostopen.ExistsNoFollow(filepath.Join(paths.Dir, appliedRecord))
	if ok || err != nil {
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
	// The path is the agent-writable repo root and this runs UNSOLICITED on
	// every develop/status, so the open rides hostopen with follow=false: a
	// FIFO, device node, or symlink planted as byre.preset degrades to
	// stateSansContent instead of hanging the CLI (a /dev/tty symlink also
	// ate terminal input while blocked — external report, 2026-07-18). The
	// solicited apply/inspect path is user-NAMED and rides the fetcher,
	// which follows symlinks; this passive probe deliberately does not.
	f, fi, err := hostopen.OpenRegular(p, false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi.Size() > packages.MaxManifestBytes {
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
	full, _ := presetNotes(projectDir, paths)
	return full
}

// presetNotes renders the note in two lengths. The short one is status's
// default tier: same fact, same command, without the sentence explaining
// what a preset is -- which every reader of this row past the first already
// knows, and which crowds the exposure rows the page exists for.
func presetNotes(projectDir string, paths project.Paths) (full, short string) {
	state, legacyName := presetState(projectDir, paths)
	name := PresetName
	renameHint := ""
	if legacyName {
		name = config.ProjectConfigName
		renameHint = " (legacy name; the convention is " + PresetName + ")"
	}
	switch state {
	case "unapplied":
		return fmt.Sprintf("this repo ships a %s%s (not applied); `byre preset apply` to review it", name, renameHint),
			fmt.Sprintf("%s not applied  (`byre preset apply`)", name)
	case "diverged":
		return fmt.Sprintf("the repo's %s%s differs from the version you applied; `byre preset apply` to review the changes", name, renameHint),
			fmt.Sprintf("%s differs from what you applied  (`byre preset apply`)", name)
	}
	return "", ""
}
