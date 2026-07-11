package onboard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/pjlsergeant/byre/internal/config"
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

// SharedAuthAnswered reports whether the shared-auth offer (ADR 0023) for
// agent is already answered in ~/.byre/default.config: yes = the companion
// skill is in `skills`; no = the agent is in `shared_auth_declined`. An
// unreadable/unparsable file counts as answered — the picker must not nag
// through (or surgically edit) a file it can't read.
func SharedAuthAnswered(home, agent, companion string) bool {
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"))
	if err != nil {
		return true
	}
	return slices.Contains(cfg.Skills, companion) || slices.Contains(cfg.SharedAuthDeclined, agent)
}

// EnableSharedAuth records a "yes" to the shared-auth offer: it surgically
// appends companion to the top-level `skills` list in ~/.byre/default.config
// (creating file or list as needed) — the same representation a hand-enabled
// companion skill uses, so there is no second source of truth. No-op if
// already present. Same surgical philosophy as SaveDefault.
func EnableSharedAuth(home, companion string) error {
	return appendDefaultListEntry(home, "skills", companion,
		func(c config.Config) bool { return slices.Contains(c.Skills, companion) })
}

// DeclineSharedAuth records a "no" to the shared-auth offer for agent in the
// picker-owned `shared_auth_declined` list in ~/.byre/default.config, so the
// offer is made at most once per agent. No-op if already recorded.
func DeclineSharedAuth(home, agent string) error {
	return appendDefaultListEntry(home, "shared_auth_declined", agent,
		func(c config.Config) bool { return slices.Contains(c.SharedAuthDeclined, agent) })
}

// appendDefaultListEntry surgically appends value to the top-level `key` list
// in ~/.byre/default.config, creating the file or the list if absent. has
// reports whether a parsed config already contains the value — checked before
// editing (idempotence) and again on the edited text (the textual edit must
// prove, by re-parse, that it produced exactly the intended config before
// anything is written).
func appendDefaultListEntry(home, key, value string, has func(config.Config) bool) error {
	path := filepath.Join(home, "default.config")
	// Parse first: never textually edit a file we can't read, and skip the
	// edit when the value is already there.
	cfg, err := config.ParseFile(path)
	if err != nil {
		return fmt.Errorf("%s: %w — add %q to `%s` there by hand", path, err, value, key)
	}
	if has(cfg) {
		return nil
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(existing)
	if content == "" {
		content = "# byre default.config — your favourites for new projects.\n"
	}
	edited, err := appendToTopLevelList(content, key, value)
	if err == nil {
		// Verify the edit SEMANTICALLY: the result must parse and contain the
		// value. A surgical text edit that can't prove itself is refused.
		var check config.Config
		if md, derr := toml.Decode(edited, &check); derr != nil || len(md.Undecoded()) > 0 || !has(check) {
			err = fmt.Errorf("edit did not verify")
		}
	}
	if err != nil {
		return fmt.Errorf("could not update %s (%v) — add %q to `%s` there by hand", path, err, value, key)
	}
	// Atomic write, so a crash or concurrent save can't truncate the file.
	return config.AtomicWrite(path, edited)
}

// appendToTopLevelList appends a quoted string to the top-level `key = [...]`
// assignment, or adds the assignment (in the top-level region, like setScalar)
// when the key is absent. The existing array may span lines and carry
// comments; brackets inside strings and comments are ignored. Any shape it
// can't follow is an error — the caller refuses rather than guesses.
func appendToTopLevelList(content, key, value string) (string, error) {
	lines := strings.Split(content, "\n")
	i := findTopLevelScalar(lines, key)
	if i < 0 {
		newline := fmt.Sprintf("%s = [%q]", key, value)
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
		return strings.Join(out, "\n"), nil
	}

	// Scan from the `=` for the array's matching `]`, tracking string and
	// comment state so brackets inside them don't count. lastToken remembers
	// the last significant char inside the array, deciding whether the
	// insertion needs a separating comma.
	depth := 0
	var lastToken byte
	for li := i; li < len(lines); li++ {
		l := lines[li]
		start := 0
		if li == i {
			eq := strings.IndexByte(l, '=')
			if eq < 0 {
				break
			}
			start = eq + 1
		}
		var inStr byte // active quote char, 0 outside strings
		for ci := start; ci < len(l); ci++ {
			c := l[ci]
			switch {
			case inStr != 0:
				if inStr == '"' && c == '\\' {
					ci++ // escaped char in a basic string
				} else if c == inStr {
					inStr = 0
				}
			case c == '"' || c == '\'':
				inStr = c
				lastToken = c
			case c == '#':
				ci = len(l) // comment runs to end of line
			case c == '[':
				depth++
				if depth == 1 {
					lastToken = c
				}
			case c == ']':
				depth--
				if depth == 0 {
					sep := ", "
					if lastToken == '[' || lastToken == ',' {
						sep = "" // empty array, or a trailing comma already there
					}
					lines[li] = l[:ci] + sep + fmt.Sprintf("%q", value) + l[ci:]
					return strings.Join(lines, "\n"), nil
				}
			default:
				if depth >= 1 && c != ' ' && c != '\t' {
					lastToken = c
				}
			}
		}
		// inStr resets per line: TOML values on a `key =` line only continue
		// across lines inside an ARRAY, and multi-line strings aren't part of
		// the shapes this editor follows (the caller verifies and refuses).
		if depth == 0 {
			break // assignment ended without an array we could follow
		}
	}
	return "", fmt.Errorf("could not find the end of the `%s` list", key)
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
