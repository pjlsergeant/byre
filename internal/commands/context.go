package commands

// `byre context` — sugar over the [[context]] config vocabulary (standing
// agent instructions, ADR 0043). add and remove edit ONE cascade layer (the
// project store config, or with --global the machine default.config) through
// the same style-preserving path as the interactive editor; list renders the
// resolved declared set. Config-only vocabulary: no skill union, so the
// effective set is exactly the resolved config's.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/editorcmd"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// contextVerbs plugs the [[context]] vocabulary into the shared layer-edit
// lifecycle (nameddecl.go).
var contextVerbs = declVerbs[config.ContextDecl]{
	kind:   "context",
	name:   func(cd config.ContextDecl) string { return cd.Name },
	marker: func(name string) config.ContextDecl { return config.ContextDecl{Name: name} },
	list:   func(c *config.Config) *[]config.ContextDecl { return &c.Contexts },
	effectiveHas: func(effective config.Config, res skills.Resolved, name string) (bool, error) {
		for _, cd := range effective.Contexts {
			if cd.Name == name {
				return true, nil
			}
		}
		return false, nil
	},
}

// editProse hands prose to $EDITOR via a temp file and returns the result —
// the `git commit` shape: `byre context add <name>` with no --text/--file
// opens an editor rather than demanding prose on a command line. The launch
// rides the shared shell-semantics launcher (editorcmd). Swapped in tests.
var editProse = func(seed string) (string, error) {
	f, err := os.CreateTemp("", "byre-context-*.md")
	if err != nil {
		return "", err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.WriteString(seed); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	editor := editorcmd.Resolve()
	if err := editorcmd.Command(editor, path).Run(); err != nil {
		return "", fmt.Errorf("%s: %w", editor, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ContextAdd implements `byre context add <name> [--text ... | --file ...]`:
// add-or-update the declaration in the target layer, re-opening a matching
// `!name` closure. With neither flag it opens $EDITOR on the current text
// (empty for a new declaration) — the terminal-native prose path.
func ContextAdd(s Streams, projectDir string, global bool, name, text, file string) error {
	if text != "" && file != "" {
		return fmt.Errorf("context add: --text and --file are exclusive (a snippet is inline or a file, not both)")
	}
	if file == "" && text == "" {
		path, _, _, err := declLayerPath(projectDir, global)
		if err != nil {
			return err
		}
		cur, err := config.ParseFile(path)
		if err != nil {
			return err
		}
		seed, existing := "", false
		for _, cd := range cur.Contexts {
			if cd.Name == name {
				if cd.File != "" {
					return fmt.Errorf("context add: %q is file-backed (%s) — edit that file, or pass --text/--file to change the declaration", name, cd.File)
				}
				seed, existing = cd.Text, true
			}
		}
		edited, err := editProse(seed)
		if err != nil {
			return err
		}
		if strings.TrimSpace(edited) == "" {
			// The remove hint only makes sense when there IS a declaration to
			// remove — for a new name, "remove it" would itself fail (QA
			// finding 2026-07-25).
			if existing {
				return fmt.Errorf("context add: no text written (remove the declaration with: byre context remove %s)", name)
			}
			return fmt.Errorf("context add: no text written — nothing added")
		}
		if existing && edited == seed {
			// The editor round-trip changed nothing; saying "updated … joins
			// at the next develop" for it would claim a write that didn't
			// happen (the configui ^q class, QA finding 2026-07-25).
			fmt.Fprintf(s.Err, "byre: context %s unchanged.\n", name)
			return nil
		}
		text = edited
	}
	if file != "" && file != "~" && !strings.HasPrefix(file, "~/") && !filepath.IsAbs(file) {
		// A bare relative path is CWD-dependent; anchor it now so the stored
		// declaration means the same file tomorrow (`~` spellings are kept as
		// typed — they expand at bake).
		abs, err := filepath.Abs(file)
		if err != nil {
			return err
		}
		file = abs
	}
	cd := config.ContextDecl{Name: name, Text: text, File: file}
	if err := config.ValidateContextDecl(cd); err != nil {
		return err
	}
	if err := addNamedDecl(s, projectDir, global, contextVerbs, name, cd); err != nil {
		return err
	}
	if file != "" {
		// Accepting a not-yet-existing file is right (it can be created
		// before the next develop) — accepting it SILENTLY is not: the
		// Claude Skills screen warns for the identical shape, and the
		// failure otherwise surfaces only at develop (QA finding 2026-07-25).
		expanded, xerr := expandHostPath(file)
		if xerr == nil {
			if _, serr := os.Stat(expanded); serr != nil {
				fmt.Fprintf(s.Err, "byre: ⚠ %s does not exist yet — the next develop will fail until it does (accepted anyway; create it before then).\n", file)
			}
		}
	}
	fmt.Fprintln(s.Err, "byre: the text joins the agent's memory at the next develop (rebuild).")
	return nil
}

// ContextRemove implements `byre context remove <name>` — the shared
// closure-smart contract (see removeNamedDecl).
func ContextRemove(s Streams, projectDir string, global bool, name string) error {
	name = strings.TrimPrefix(name, "!") // tolerate a pasted closure spelling
	if !config.ValidContextName(name) {
		return fmt.Errorf("context remove: %q is not a valid context name", name)
	}
	return removeNamedDecl(s, projectDir, global, contextVerbs, name)
}

// ContextList implements `byre context list`: the resolved set, each snippet
// attributed to the layer that speaks it, PLUS the cascade's shadows —
// overridden lower declarations and closures (Pete's ruling, 2026-07-26:
// the moment an operator runs list is usually "where did my snippet go?",
// and the confusing cases are precisely the shadowed ones). Config-only
// vocabulary, so no skill union.
func ContextList(s Streams, projectDir string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	// Read-only, but collision-checked like status: never render another
	// project's declared set as this one's.
	if err := paths.ValidateExisting(); err != nil {
		return err
	}
	cfg, err := config.Load(projectDir)
	if err != nil {
		return err
	}
	// Attribution probes; nil degrades every row to unattributed (the layers
	// are display sugar — the resolved set above is the truth).
	srcs, _ := config.ContextSources(projectDir)
	if len(cfg.Contexts) == 0 && len(contextShadowLines(cfg, srcs)) == 0 {
		fmt.Fprintln(s.Out, "no standing instructions declared  (add one: byre context add <name>)")
		return nil
	}
	for _, cd := range cfg.Contexts {
		fmt.Fprintln(s.Out, contextLine(cd)+contextAttribution(srcs, cd.Name))
	}
	for _, line := range contextShadowLines(cfg, srcs) {
		fmt.Fprintln(s.Out, line)
	}
	return nil
}

// contextDeclarers returns the labels of every raw layer declaring name
// (non-marker), in merge order — last is the winner.
func contextDeclarers(srcs []config.SourceLayer, name string) []string {
	var out []string
	for _, sl := range srcs {
		for _, cd := range sl.Cfg.Contexts {
			if cd.Name == name {
				out = append(out, sl.Label)
				break
			}
		}
	}
	return out
}

// contextAttribution renders an effective row's source tag: the winning
// layer, plus what it overrides when a lower layer also spoke.
func contextAttribution(srcs []config.SourceLayer, name string) string {
	decl := contextDeclarers(srcs, name)
	switch len(decl) {
	case 0:
		return ""
	case 1:
		return "  (" + decl[0] + ")"
	default:
		return fmt.Sprintf("  (%s — overrides %s)", decl[len(decl)-1], strings.Join(decl[:len(decl)-1], ", "))
	}
}

// contextShadowLines renders what is NOT shipping and why: `!name` closures
// (attributed to the layer holding the marker, naming what they removed) —
// including stale markers that match nothing, which stay visible rather
// than silently inert (config, never invisible).
func contextShadowLines(cfg config.Config, srcs []config.SourceLayer) []string {
	var out []string
	seen := map[string]bool{}
	for _, sl := range srcs {
		for _, cd := range sl.Cfg.Contexts {
			name, ok := strings.CutPrefix(cd.Name, "!")
			if !ok || seen[name] {
				continue
			}
			seen[name] = true
			was := contextDeclarers(srcs, name)
			switch {
			case len(was) > 0:
				out = append(out, fmt.Sprintf("%s  — removed by %s  (was %s)", name, sl.Label, strings.Join(was, ", ")))
			default:
				out = append(out, fmt.Sprintf("%s  — removed by %s  (nothing below declares it)", name, sl.Label))
			}
		}
	}
	return out
}

// contextLine renders one declaration for list: name plus its source — a
// file path, or the inline text's first line and size.
func contextLine(cd config.ContextDecl) string {
	if cd.File != "" {
		return fmt.Sprintf("%s  (file: %s)", cd.Name, cd.File)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(cd.Text), "\n")
	const max = 60
	if len(first) > max {
		first = first[:max-1] + "…"
	}
	lines := strings.Count(strings.TrimRight(cd.Text, "\n"), "\n") + 1
	if lines > 1 {
		return fmt.Sprintf("%s  %q  (+%d more lines)", cd.Name, first, lines-1)
	}
	return fmt.Sprintf("%s  %q", cd.Name, first)
}
