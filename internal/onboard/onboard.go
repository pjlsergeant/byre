// Package onboard implements byre's first-run picker: when `byre develop` runs in
// a project with no byre.config, it lets the user choose a template × agent (with
// their favourites pre-selected) and writes the choice to byre.config — and,
// optionally, saves it as their default (favourites) in default.config.
package onboard

import (
	"bufio"
	"fmt"
	"github.com/pjlsergeant/byre/internal/config"
	"io"
	"strings"
)

// noneOption is the explicit "no template"/"no agent" choice (config owns the
// sentinel).
const noneOption = config.NoneLabel

// Choice is the outcome of the picker.
type Choice struct {
	Template    string // "" means none
	Agent       string // "" means none
	SaveDefault bool
	// SharedAuthCompanion is the companion skill the shared-auth offer (ADR
	// 0025) named — "" when the offer wasn't made — and SharedAuth its answer:
	// whether THIS box opts into the shared credentials (the companion goes
	// into this project's byre.config, the only grant the answer ever makes).
	// With SaveDefault set, the caller also saves the answer as the
	// preference prefilling future offers (SaveSharedAuthDefault) — a
	// favourite, not a grant.
	SharedAuthCompanion string
	SharedAuth          bool
}

// Favourite is one axis's stored default. Stored is what default.config holds
// verbatim — the basis for "would saving change anything?". Effective is the
// validated value the picker pre-selects ("" when Stored is absent or stale,
// i.e. no longer names a real template/agent). They differ exactly when the
// stored favourite is stale — and then the save offer must still appear, or
// the stale value can never be overwritten and silently resurrects if its
// name becomes valid again.
type Favourite struct {
	Stored    string
	Effective string
}

// Pick runs the interactive picker. templates and agents are the available
// options (a "none" choice is always offered); tmplFav/agentFav are the user's
// favourites — Effective pre-selected so an empty answer accepts it.
// companionFor returns the ready shared-auth companion this box could opt
// into for an agent ("" = no offer; nil disables offers) plus the saved
// preference prefilling the offer's default answer: when it names one, the
// offer is asked right after the agent question — agent questions stay
// together, and every answer is collected before the caller writes anything,
// so an EOF anywhere in the picker aborts with no side effects.
//
// The prompting functions here take a *bufio.Reader, not an io.Reader, on
// purpose: a caller asking more than one question MUST thread one shared
// reader through them, or the first question's buffering eats the later
// answers — the signature makes that invariant compile-enforced.
func Pick(out io.Writer, r *bufio.Reader, templates, agents []string, tmplFav, agentFav Favourite, companionFor func(agent string) (companion string, prefYes bool)) (Choice, error) {
	fmt.Fprintln(out, "No byre.config here — let's set one up (press Enter to accept [default]).")

	tmpl, err := ask(out, r, "Template", withNone(templates), orNone(tmplFav.Effective))
	if err != nil {
		return Choice{}, err
	}
	agent, err := ask(out, r, "Agent", withNone(agents), orNone(agentFav.Effective))
	if err != nil {
		return Choice{}, err
	}
	companion, sharedAuth, sharedPref := "", false, false
	if companionFor != nil {
		if companion, sharedPref = companionFor(fromNone(agent)); companion != "" {
			sharedAuth, err = OfferSharedAuth(out, r, fromNone(agent), companion, sharedPref)
			if err != nil {
				return Choice{}, err
			}
		}
	}
	// Choosing exactly what default.config already stores is not news:
	// offering to save it would be noise (and the save a no-op). Only ask when
	// saving would change the stored state — a template/agent differing from
	// the stored favourite (compared against Stored, not Effective: with a
	// stale favourite the two differ, saving is NOT a no-op, and the offer is
	// the user's one chance to overwrite the stale value), or a shared-auth
	// answer differing from its saved preference. One rule for all the axes
	// of "these".
	save := false
	if fromNone(tmpl) != tmplFav.Stored || fromNone(agent) != agentFav.Stored || (companion != "" && sharedAuth != sharedPref) {
		save, err = askYesNo(out, r, "Save these as your default for new projects?")
		if err != nil {
			return Choice{}, err
		}
	}

	return Choice{
		Template:            fromNone(tmpl),
		Agent:               fromNone(agent),
		SaveDefault:         save,
		SharedAuthCompanion: companion,
		SharedAuth:          sharedAuth,
	}, nil
}

// AskAxis prompts for a single axis (Template or Agent), offering a "none"
// option and pre-selecting def (the favourite). Returns "" for none. Used when a
// --template/--agent flag fixes one axis and the other still needs choosing.
func AskAxis(out io.Writer, r *bufio.Reader, label string, options []string, def string) (string, error) {
	v, err := ask(out, r, label, withNone(options), orNone(def))
	if err != nil {
		return "", err
	}
	return fromNone(v), nil
}

// OfferSharedAuth asks the shared-auth question (ADR 0025) for the chosen
// agent: whether THIS box opts into the machine's shared credentials. The
// scope in the wording is the scope of the write — a "y" puts the companion
// skill in this project's byre.config, the only thing the answer ever
// grants. prefYes is the saved preference: it prefills the default answer
// ([Y/n/i] instead of [y/N/i]) exactly as the favourites prefill
// template/agent — Enter accepts it, and only an explicit "y" or a Yes
// default grants. The question itself omits the companion's skill name (it
// is config plumbing, not part of the decision); "i" is where that detail
// lives — it prints exactly what each answer writes, then re-asks.
func OfferSharedAuth(out io.Writer, r *bufio.Reader, agent, companion string, prefYes bool) (bool, error) {
	marker := "y/N, i for info"
	if prefYes {
		marker = "Y/n, i for info"
	}
	for {
		fmt.Fprintf(out, "Opt this box into %s shared credentials? [%s]: ", agent, marker)
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, nil
		case "":
			return prefYes, nil
		case "i":
			fmt.Fprintf(out, `
  y — this box uses the machine-wide shared %s login.
      Writes one line — %q — into THIS project's byre.config
      (delete it there to undo). No other project changes.
  n — this box keeps its own separate %s login (log in inside the box).
      Writes nothing, anywhere.
  Afterwards, "Save these as your default?" only changes which answer is
  pre-selected at the NEXT project's question — saving never
  opts any box in by itself.

`, agent, companion, agent)
		default:
			// Same stance as every yes/no here: unrecognized input never
			// lands on the granting side, whatever the default.
			return false, nil
		}
	}
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
	return askYesNoDefault(out, r, label, false)
}

// askYesNoDefault prompts [Y/n] or [y/N] per def; an empty answer accepts the
// default. Everything else only counts as yes when it is an explicit y/yes —
// unrecognized input never lands on the granting side, whatever the default.
func askYesNoDefault(out io.Writer, r *bufio.Reader, label string, def bool) (bool, error) {
	marker := "y/N"
	if def {
		marker = "Y/n"
	}
	fmt.Fprintf(out, "%s [%s]: ", label, marker)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	case "":
		return def, nil
	default:
		return false, nil
	}
}

// The "none" sentinel vocabulary is config's (config.NoneLabel); these thin
// wrappers keep the picker readable.
func withNone(opts []string) []string {
	return append(append([]string{}, opts...), noneOption)
}

func orNone(v string) string   { return config.OrNone(v) }
func fromNone(v string) string { return config.FromNone(v) }
