package onboard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
)

// WriteProjectConfig writes a byre.config (the host-side store path) from the
// chosen template/agent (omitting either if empty) and any skills the picker
// enabled for this box — today only the shared-auth companion when its offer
// (ADR 0025) was answered yes. It refuses to overwrite an existing config and
// creates the parent dir if needed.
func WriteProjectConfig(destPath, template, agent string, skills []string) error {
	var b strings.Builder
	b.WriteString("# Created by byre.\n")
	if template != "" {
		fmt.Fprintf(&b, "template = %q\n", template)
	}
	if agent != "" {
		fmt.Fprintf(&b, "agent = %q\n", agent)
	}
	if len(skills) > 0 {
		quoted := make([]string, len(skills))
		for i, s := range skills {
			quoted[i] = fmt.Sprintf("%q", s)
		}
		fmt.Fprintf(&b, "skills = [%s]\n", strings.Join(quoted, ", "))
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	// Sibling temp file, then link(2) into place: the link fails if destPath
	// exists, keeping the refuse-to-overwrite guarantee atomic (no Stat/Write
	// race with a concurrent first-run) — and an interrupted write can never
	// leave a partial byre.config, whose mere existence marks the project as
	// onboarded and blocks a re-run.
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".byre-onboard-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// The file keeps CreateTemp's private 0600 — the same mode every other
	// byre config writer (config.AtomicWrite) produces, and byre.config is
	// read only by byre as this user.
	if err := os.Link(tmpName, destPath); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%s already exists; not overwriting", destPath)
		}
		return err
	}
	return nil
}

// SaveDefault updates the template/agent scalars in ~/.byre/default.config
// (creating it if absent), preserving any other content. An empty value removes
// that scalar.
//
// Write philosophy: this is the SURGICAL writer — it touches only its two
// top-level scalars and leaves the user's comments and hand-set fields alone,
// because it runs from the onboarding picker where the user never chose to
// edit the whole file. The full-file editor (`byre config --global`) is the
// other philosophy: it re-marshals the entire file (and warns that comments
// are lost). Keep the two roles distinct; don't grow this into a third
// general-purpose writer.
func SaveDefault(home, template, agent string) error {
	path := filepath.Join(home, "default.config")
	content, err := readDefaultConfig(home)
	if err != nil {
		return err
	}
	content = setScalar(content, "template", template)
	content = setScalar(content, "agent", agent)
	// Atomic write, so a crash or concurrent save can't truncate the favourites.
	return config.AtomicWrite(path, content)
}

// Favourites reads the template/agent scalars from ~/.byre/default.config (the
// user's pre-selected defaults) via a real TOML parse — the regex scraper it
// replaced broke on literal ('single-quoted') strings. Missing or unparsable
// values come back empty (the picker just starts without favourites).
func Favourites(home string) (template, agent string) {
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"))
	if err != nil {
		return "", ""
	}
	return cfg.Template, cfg.Agent
}

// SharedAuthAlreadyOn reports whether companion is already enabled machine-wide
// (in ~/.byre/default.config's `skills`) — then every box gets it from the
// cascade and the per-box offer (ADR 0025) would be asking about a switch
// that's already thrown. An unreadable/unparsable file reads as not-on: the
// offer's "y" writes only this project's byre.config, so there is no file
// here the picker would need to edit — and a genuinely broken default.config
// fails the develop loudly right after onboarding anyway.
func SharedAuthAlreadyOn(home, companion string) bool {
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"))
	if err != nil {
		return false
	}
	return slices.Contains(cfg.Skills, companion)
}

// defaultConfigStub heads a default.config the surgical writer creates from
// nothing.
const defaultConfigStub = "# byre default.config — your favourites for new projects.\n"

// readDefaultConfig returns ~/.byre/default.config's content, or the stub for
// a file that doesn't exist (or is empty) yet.
func readDefaultConfig(home string) (string, error) {
	b, err := os.ReadFile(filepath.Join(home, "default.config"))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if len(b) == 0 {
		return defaultConfigStub, nil
	}
	return string(b), nil
}

// findTopLevelScalar returns the line index of a top-level `key =` assignment
// (one that appears before any `[section]` header, so it isn't a nested key like
// `[env] agent = ...`), or -1.
func findTopLevelScalar(lines []string, key string) int {
	re := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*=`)
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "[") {
			return -1 // entered a section; top-level keys precede all sections
		}
		if re.MatchString(l) {
			return i
		}
	}
	return -1
}

// setScalar replaces (or, for an empty value, removes; or appends) a top-level
// `key = "value"` line, leaving sections and other content untouched.
func setScalar(content, key, value string) string {
	lines := strings.Split(content, "\n")
	i := findTopLevelScalar(lines, key)

	if value == "" {
		if i >= 0 {
			lines = append(lines[:i], lines[i+1:]...)
		}
		return strings.Join(lines, "\n")
	}

	newline := fmt.Sprintf("%s = %q", key, value)
	if i >= 0 {
		lines[i] = newline
		return strings.Join(lines, "\n")
	}
	return strings.Join(insertTopLevel(lines, newline), "\n")
}

// insertTopLevel splices newline into the top-level region: just before the
// first section header, or at the end when there is none.
func insertTopLevel(lines []string, newline string) []string {
	insert := len(lines)
	for j, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "[") {
			insert = j
			break
		}
	}
	out := append([]string{}, lines[:insert]...)
	out = append(out, newline)
	return append(out, lines[insert:]...)
}
