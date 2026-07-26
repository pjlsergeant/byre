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

	"github.com/pjlsergeant/byre/internal/builtins"
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
		seed := ""
		for _, cd := range cur.Contexts {
			if cd.Name == name {
				if cd.File != "" {
					return fmt.Errorf("context add: %q is file-backed (%s) — edit that file, or pass --text/--file to change the declaration", name, cd.File)
				}
				seed = cd.Text
			}
		}
		edited, err := editProse(seed)
		if err != nil {
			return err
		}
		if strings.TrimSpace(edited) == "" {
			return fmt.Errorf("context add: no text written (remove the declaration with: byre context remove %s)", name)
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
	fmt.Fprintln(s.Err, "byre: the text is injected into the agent's instructions at the next develop (rebuild).")
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

// ContextList implements `byre context list`: the resolved declared set —
// config-only vocabulary, so no skill union and no delivery verdicts, just
// each snippet's identity and source.
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
	if len(cfg.Contexts) == 0 {
		fmt.Fprintln(s.Out, "no standing instructions declared  (add one: byre context add <name>)")
		return nil
	}
	for _, cd := range cfg.Contexts {
		fmt.Fprintln(s.Out, contextLine(cd))
	}
	// The delivery verdict, by the SAME renderer status uses (the
	// claude-skill list precedent: two surfaces, one story).
	info := statusInfo{Agent: cfg.Agent, Contexts: cfg.Contexts}
	if serr := builtins.EnsureStoreOut(paths.Home, s.Err); serr != nil {
		info.SkillErr = serr.Error()
	} else if cat, _ := builtins.LoadCatalogRaw(paths.Home); cat == nil {
		info.SkillErr = "catalog unavailable"
	} else if res, rerr := skills.Resolve(cfg, cat); rerr != nil {
		info.SkillErr = rerr.Error()
	} else if res.Agent != nil {
		info.AgentContext = res.Agent.Context
	}
	fmt.Fprintln(s.Out, contextDeliveryLine(info))
	return nil
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
