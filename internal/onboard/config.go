package onboard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// WriteProjectConfig writes a byre.config (the host-side store path) from the
// chosen template/agent (omitting either if empty). It refuses to overwrite an
// existing config and creates the parent dir if needed.
func WriteProjectConfig(destPath, template, agent string) error {
	var b strings.Builder
	b.WriteString("# Created by byre.\n")
	if template != "" {
		fmt.Fprintf(&b, "template = %q\n", template)
	}
	if agent != "" {
		fmt.Fprintf(&b, "agent = %q\n", agent)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	// O_EXCL: atomically refuse to overwrite an existing config (no Stat/Write
	// race with a concurrent first-run).
	f, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%s already exists; not overwriting", destPath)
		}
		return err
	}
	defer f.Close()
	_, err = f.WriteString(b.String())
	return err
}

// SaveDefault updates the template/agent scalars in ~/.byre/default.config
// (creating it if absent), preserving any other content. An empty value removes
// that scalar.
func SaveDefault(home, template, agent string) error {
	path := filepath.Join(home, "default.config")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(existing)
	if content == "" {
		content = "# byre default.config — your favourites for new projects.\n"
	}
	content = setScalar(content, "template", template)
	content = setScalar(content, "agent", agent)
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	// Atomic write: temp file + rename, so a crash or concurrent save can't
	// truncate/corrupt the favourites.
	tmp, err := os.CreateTemp(home, ".default.config-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// Favourites reads the template/agent scalars from ~/.byre/default.config (the
// user's pre-selected defaults). Missing values come back empty.
func Favourites(home string) (template, agent string) {
	b, err := os.ReadFile(filepath.Join(home, "default.config"))
	if err != nil {
		return "", ""
	}
	return getScalar(string(b), "template"), getScalar(string(b), "agent")
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

func getScalar(content, key string) string {
	lines := strings.Split(content, "\n")
	i := findTopLevelScalar(lines, key)
	if i < 0 {
		return ""
	}
	if m := regexp.MustCompile(`=\s*"([^"]*)"`).FindStringSubmatch(lines[i]); len(m) == 2 {
		return m[1]
	}
	return ""
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
	// Append in the top-level region: just before the first section header (or
	// at end if there are none).
	insert := len(lines)
	for j, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "[") {
			insert = j
			break
		}
	}
	out := append([]string{}, lines[:insert]...)
	out = append(out, newline)
	out = append(out, lines[insert:]...)
	return strings.Join(out, "\n")
}
