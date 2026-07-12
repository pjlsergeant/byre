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

// SharedAuthAnswered reports whether the shared-auth question is already
// settled machine-wide for agent, making the per-box offer (ADR 0025) moot:
// the companion in default.config's `skills` (a saved yes, or hand-enabled)
// means every box gets shared credentials from the cascade; the agent in
// `shared_auth_declined` (a saved no) means the user asked new boxes not to
// be offered. Either entry is removable there to change the default. An
// unreadable/unparsable file counts as answered — the picker must not nag
// through (or surgically edit, if the answer is then saved) a file it can't
// read.
func SharedAuthAnswered(home, agent, companion string) bool {
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"))
	if err != nil {
		return true
	}
	return slices.Contains(cfg.Skills, companion) || slices.Contains(cfg.SharedAuthDeclined, agent)
}

// SaveSharedAuthDefault records the shared-auth answer as the machine default
// when the user said "save these as your default" (ADR 0025): a yes appends
// companion to default.config's `skills` — the same representation a
// hand-enabled companion uses, so new boxes get shared credentials from the
// cascade and the offer stops appearing; a no appends agent to the
// picker-owned `shared_auth_declined`, so new boxes simply aren't offered.
// Only the save-default consent ever writes these; the offer alone never
// touches machine-level state. Both writes are surgical and idempotent.
func SaveSharedAuthDefault(home, agent, companion string, yes bool) error {
	if yes {
		return appendDefaultListEntry(home, "skills", companion,
			func(c config.Config) []string { return c.Skills })
	}
	return appendDefaultListEntry(home, "shared_auth_declined", agent,
		func(c config.Config) []string { return c.SharedAuthDeclined })
}

// appendDefaultListEntry surgically appends value to the top-level `key` list
// in ~/.byre/default.config, creating the file or the list if absent. field
// projects the Config field the key decodes into, so one containment check
// serves both the idempotence pre-check and the post-edit verification: the
// textual edit must prove, by re-parsing through config's own parser, that it
// produced exactly the intended config before anything is written.
func appendDefaultListEntry(home, key, value string, field func(config.Config) []string) error {
	path := filepath.Join(home, "default.config")
	// One read feeds the pre-check, the edit, and the verify, so they can
	// never disagree about the file's content.
	content, err := readDefaultConfig(home)
	if err != nil {
		return err
	}
	cfg, err := config.Parse([]byte(content))
	if err != nil {
		// Never textually edit a file we can't read.
		return fmt.Errorf("%s: %w — add %q to `%s` there by hand", path, err, value, key)
	}
	if slices.Contains(field(cfg), value) {
		return nil
	}

	edited, err := appendToTopLevelList(content, key, value)
	if err == nil {
		// Verify the edit SEMANTICALLY: the result must parse and contain the
		// value. A surgical text edit that can't prove itself is refused.
		if check, perr := config.Parse([]byte(edited)); perr != nil || !slices.Contains(field(check), value) {
			err = fmt.Errorf("edit did not verify")
		}
	}
	if err == nil {
		// Atomic write, so a crash or concurrent save can't truncate the file.
		err = config.AtomicWrite(path, edited)
	}
	if err != nil {
		return fmt.Errorf("could not update %s (%v) — add %q to `%s` there by hand", path, err, value, key)
	}
	return nil
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
		return strings.Join(insertTopLevel(lines, fmt.Sprintf("%s = [%q]", key, value)), "\n"), nil
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

// defaultConfigStub heads a default.config the surgical writers create from
// nothing — SaveDefault and appendDefaultListEntry must stamp the same one.
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
// first section header, or at the end when there is none. Shared by the
// surgical writers so new scalars and new lists land in the same place.
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
