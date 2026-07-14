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
	// SharedAuthOffered is whether the offer was actually made. The saved
	// preference is only touched when it was: a save after a no-offer onboard
	// must not delete a stored favourite for a question never asked.
	SharedAuthOffered bool
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

// SharedAuthOffer is what the caller passes for one agent's shared-auth
// decision: zero or more provenance-labeled claimants, a
// yes-inclination prefill (legacy array), an optional saved companion pick,
// and a notice when the saved pick is no longer available.
type SharedAuthOffer struct {
	// Claimants are display names of companions to offer (already filtered for
	// machine-wide enablement). Labels[i] is the provenance label for
	// Claimants[i] (e.g. "bundled, byre's"). VolumeNotes[i] is that claimant's
	// own machine-volume disclosure (may be empty).
	Claimants   []string
	Labels      []string // same length as Claimants
	VolumeNotes []string // same length as Claimants; per-claimant
	// PrefYes is a legacy yes-inclination with no pick (array shape).
	PrefYes bool
	// PrefPick is a saved companion display name to preselect in the picker
	// ("" = none). When non-empty and still among Claimants, multi-claim
	// prefills that row; single-claim prefills Yes.
	PrefPick string
	// StalePickNotice is printed once when a stored pick is missing/INVALID
	// (the stored entry is left untouched until the next save).
	StalePickNotice string
}

// Pick runs the interactive picker. templates and agents are the available
// options (a "none" choice is always offered); tmplFav/agentFav are the user's
// favourites — Effective pre-selected so an empty answer accepts it.
// sharedAuthFor returns the shared-auth offer for an agent (zero Claimants =
// no offer). Every answer is collected before the caller writes anything.
//
// The prompting functions here take a *bufio.Reader, not an io.Reader, on
// purpose: a caller asking more than one question MUST thread one shared
// reader through them, or the first question's buffering eats the later
// answers — the signature makes that invariant compile-enforced.
func Pick(out io.Writer, r *bufio.Reader, templates, agents []string, tmplFav, agentFav Favourite, sharedAuthFor func(agent string) SharedAuthOffer) (Choice, error) {
	fmt.Fprintln(out, "No byre.config here — let's set one up (press Enter to accept [default]).")

	tmpl, err := ask(out, r, "Template", withNone(templates), orNone(tmplFav.Effective))
	if err != nil {
		return Choice{}, err
	}
	agent, err := ask(out, r, "Agent", withNone(agents), orNone(agentFav.Effective))
	if err != nil {
		return Choice{}, err
	}
	companion, sharedAuth := "", false
	// prefWouldYes is whether Enter (or accepting the default) yields yes —
	// used to decide if the answer is "news" vs the stored favourite.
	prefWouldYes := false
	prefPick := ""
	hadOffer := false
	if sharedAuthFor != nil {
		offer := sharedAuthFor(fromNone(agent))
		if len(offer.Claimants) > 0 {
			hadOffer = true
			prefWouldYes = offer.PrefYes || offer.PrefPick != ""
			prefPick = offer.PrefPick
			companion, sharedAuth, err = OfferSharedAuthChoice(out, r, fromNone(agent), offer)
			if err != nil {
				return Choice{}, err
			}
		}
	}
	// Choosing exactly what default.config already stores is not news:
	// offering to save it would be noise (and the save a no-op). Only ask when
	// saving would change the stored state.
	save := false
	wantSaveNews := fromNone(tmpl) != tmplFav.Stored || fromNone(agent) != agentFav.Stored
	if hadOffer {
		if sharedAuth != prefWouldYes {
			wantSaveNews = true
		} else if sharedAuth && companion != "" && prefPick != "" && companion != prefPick {
			// Multi-claim: accepted a different pick than the saved one.
			wantSaveNews = true
		}
	}
	if wantSaveNews {
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
		SharedAuthOffered:   hadOffer,
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

// OfferSharedAuth is the single-claimant form (kept for tests and flag paths).
// Prefer OfferSharedAuthChoice when provenance labels or multi-claim apply.
func OfferSharedAuth(out io.Writer, r *bufio.Reader, agent, companion string, prefYes bool) (bool, error) {
	_, yes, err := OfferSharedAuthChoice(out, r, agent, SharedAuthOffer{
		Claimants: []string{companion},
		Labels:    []string{""},
		PrefYes:   prefYes,
	})
	return yes, err
}

// OfferSharedAuthChoice runs the shared-auth offer: single claimant keeps
// [y/N] (plus provenance line and optional volume note); multi-claim is a
// numbered picker (bundled-first already sorted by the caller), N = none.
// Returns the chosen companion display name ("" on decline) and whether the
// answer was yes.
func OfferSharedAuthChoice(out io.Writer, r *bufio.Reader, agent string, offer SharedAuthOffer) (companion string, yes bool, err error) {
	if len(offer.Claimants) == 0 {
		return "", false, nil
	}
	if offer.StalePickNotice != "" {
		fmt.Fprintln(out, offer.StalePickNotice)
	}
	volNote := func(i int) string {
		if i >= 0 && i < len(offer.VolumeNotes) {
			return offer.VolumeNotes[i]
		}
		return ""
	}

	if len(offer.Claimants) == 1 {
		c := offer.Claimants[0]
		label := ""
		if len(offer.Labels) > 0 && offer.Labels[0] != "" {
			label = offer.Labels[0]
		}
		prefYes := offer.PrefYes || offer.PrefPick == c
		marker := "y/N, i for info"
		if prefYes {
			marker = "Y/n, i for info"
		}
		if vn := volNote(0); vn != "" {
			fmt.Fprintf(out, "  %s\n", vn)
		}
		for {
			if label != "" {
				fmt.Fprintf(out, "Opt this box into %s shared credentials? (%s, %s) [%s]: ", agent, c, label, marker)
			} else {
				fmt.Fprintf(out, "Opt this box into %s shared credentials? [%s]: ", agent, marker)
			}
			line, rerr := r.ReadString('\n')
			if rerr != nil && line == "" {
				return "", false, rerr
			}
			switch strings.ToLower(strings.TrimSpace(line)) {
			case "y", "yes":
				return c, true, nil
			case "":
				if prefYes {
					return c, true, nil
				}
				return "", false, nil
			case "i":
				fmt.Fprintf(out, `
  y — this box uses the machine-wide shared %s login via %q.
      Writes one line — skills = [%q] — into THIS project's byre.config
      (delete it there to undo). No other project changes.
  n — this box keeps its own separate %s login (log in inside the box).
      Writes nothing, anywhere.
  Afterwards, "Save these as your default?" only changes which answer is
  pre-selected at the NEXT project's question — saving never
  opts any box in by itself.

`, agent, c, c, agent)
				if vn := volNote(0); vn != "" {
					fmt.Fprintf(out, "  %s\n", vn)
				}
			default:
				return "", false, nil
			}
		}
	}

	// Multi-claim picker: per-claimant volume notes under each row.
	fmt.Fprintf(out, "Several shared-auth companions claim %s:\n", agent)
	pre := 0 // 1-based prefill index; 0 = none
	for i, c := range offer.Claimants {
		label := ""
		if i < len(offer.Labels) && offer.Labels[i] != "" {
			label = "  (" + offer.Labels[i] + ")"
		}
		fmt.Fprintf(out, "  %d) %s%s\n", i+1, c, label)
		if vn := volNote(i); vn != "" {
			fmt.Fprintf(out, "      %s\n", vn)
		}
		if offer.PrefPick != "" && c == offer.PrefPick {
			pre = i + 1
		}
	}
	fmt.Fprintln(out, "  N) none")
	def := "N"
	if pre > 0 {
		def = fmt.Sprintf("%d", pre)
	}
	for {
		fmt.Fprintf(out, "Pick a companion for this box [%s]: ", def)
		line, rerr := r.ReadString('\n')
		if rerr != nil && line == "" {
			return "", false, rerr
		}
		ans := strings.TrimSpace(line)
		if ans == "" {
			ans = def
		}
		switch strings.ToLower(ans) {
		case "n", "none":
			return "", false, nil
		}
		// Numbered pick.
		var n int
		if _, perr := fmt.Sscanf(ans, "%d", &n); perr == nil && n >= 1 && n <= len(offer.Claimants) {
			return offer.Claimants[n-1], true, nil
		}
		fmt.Fprintf(out, "  enter 1-%d or N\n", len(offer.Claimants))
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
