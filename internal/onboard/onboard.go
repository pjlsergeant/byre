// Package onboard implements byre's first-run picker: when `byre develop` runs in
// a project with no byre.config, it lets the user choose a template × agent (with
// their favourites pre-selected) and writes the choice to byre.config — and,
// optionally, saves it as their default (favourites) in default.config.
package onboard

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// noneOption is the explicit "no template"/"no agent" choice.
const noneOption = "none"

// Choice is the outcome of the picker.
type Choice struct {
	Template    string // "" means none
	Agent       string // "" means none
	SaveDefault bool
}

// Pick runs the interactive picker. templates and agents are the available
// options (a "none" choice is always offered); defTemplate/defAgent are the
// user's favourites, pre-selected so an empty answer accepts them.
func Pick(out io.Writer, in io.Reader, templates, agents []string, defTemplate, defAgent string) (Choice, error) {
	r := bufio.NewReader(in)
	fmt.Fprintln(out, "No byre.config here — let's set one up (press Enter to accept [default]).")

	tmpl, err := ask(out, r, "Template", withNone(templates), orNone(defTemplate))
	if err != nil {
		return Choice{}, err
	}
	agent, err := ask(out, r, "Agent", withNone(agents), orNone(defAgent))
	if err != nil {
		return Choice{}, err
	}
	save, err := askYesNo(out, r, "Save these as your default for new projects?")
	if err != nil {
		return Choice{}, err
	}

	return Choice{
		Template:    fromNone(tmpl),
		Agent:       fromNone(agent),
		SaveDefault: save,
	}, nil
}

// AskAxis prompts for a single axis (Template or Agent), offering a "none"
// option and pre-selecting def (the favourite). Returns "" for none. Used when a
// --template/--agent flag fixes one axis and the other still needs choosing.
func AskAxis(out io.Writer, in io.Reader, label string, options []string, def string) (string, error) {
	v, err := ask(out, bufio.NewReader(in), label, withNone(options), orNone(def))
	if err != nil {
		return "", err
	}
	return fromNone(v), nil
}

// ask prompts for one choice among options, pre-selecting def. An empty answer
// accepts def; an invalid answer re-prompts.
func ask(out io.Writer, r *bufio.Reader, label string, options []string, def string) (string, error) {
	for {
		fmt.Fprintf(out, "%s — %s [%s]: ", label, strings.Join(options, " "), def)
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		ans := strings.TrimSpace(line)
		if ans == "" {
			return def, nil
		}
		for _, o := range options {
			if ans == o {
				return ans, nil
			}
		}
		fmt.Fprintf(out, "  %q is not one of: %s\n", ans, strings.Join(options, " "))
	}
}

func askYesNo(out io.Writer, r *bufio.Reader, label string) (bool, error) {
	fmt.Fprintf(out, "%s [y/N]: ", label)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func withNone(opts []string) []string {
	return append(append([]string{}, opts...), noneOption)
}

func orNone(v string) string {
	if v == "" {
		return noneOption
	}
	return v
}

func fromNone(v string) string {
	if v == noneOption {
		return ""
	}
	return v
}
