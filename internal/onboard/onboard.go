// Package onboard implements byre's first-run picker: when `byre develop` runs in
// a project with no byre.config, it lets the user choose a template × agent (with
// their favourites pre-selected) and writes the choice to byre.config — and,
// optionally, saves it as their default (favourites) in default.config.
package onboard

import (
	"bufio"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
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
	// preference prefilling future offers (SaveSharedAuthDefaultPick) — a
	// favourite, not a grant.
	SharedAuthCompanion string
	SharedAuth          bool
	// SharedAuthOffered is whether the offer was actually made. The saved
	// preference is only touched when it was: a save after a no-offer onboard
	// must not delete a stored favourite for a question never asked.
	SharedAuthOffered bool
}

// Option is one row in an axis picker (Template or Agent). Name is what the
// user types; Label is provenance worth naming (empty for the unremarkable
// bundled case); Disabled, when non-empty, is WHY the package cannot be
// chosen -- an INVALID catalog row's reason.
//
// A disabled row is shown, not hidden: a broken package the user is looking
// for is the one they most need an answer about, and dropping it silently
// left first-run showing an agent list with their agent simply missing. It is
// never selectable -- naming one reprompts with the reason, the same
// never-land-on-either-side discipline ClassifyAnswer enforces for y/n.
type Option struct {
	Name     string
	Label    string
	Disabled string
}

// Options builds a plain selectable row set (no problems, no labels) -- for
// callers with nothing but names.
func Options(names ...string) []Option {
	out := make([]Option, 0, len(names))
	for _, n := range names {
		out = append(out, Option{Name: n})
	}
	return out
}

// Selectable is the names a user may actually choose. Callers that validate a
// favourite or a flag value read THIS, so the set the picker offers and the
// set a flag accepts can never disagree.
func Selectable(opts []Option) []string {
	var out []string
	for _, o := range opts {
		if o.Disabled == "" {
			out = append(out, o.Name)
		}
	}
	return out
}

// shown renders one externally-authored string as DATA on the picker's
// prompts. Every field of an Option arrives from somewhere byre does not
// write -- a package's declared id, a catalog reason quoting a mount target,
// a posture value, a path out of somebody's skill.toml -- and this is a
// reporting surface, so none of it may reach the terminal as control (P4).
// Display only: matching always compares the real value.
func shown(s string) string { return packages.EscapeTerminal(s) }

func shownAll(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, shown(s))
	}
	return out
}

// disabledReason returns why name cannot be chosen ("" when it can, or when
// the name is not a row at all).
func disabledReason(opts []Option, name string) string {
	for _, o := range opts {
		if o.Name == name {
			return o.Disabled
		}
	}
	return ""
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
	// Claimants[i] (e.g. "bundled with byre"). Foreign[i] is true when the
	// claimant is NOT byre's own (installed/local): a third party asking to
	// hold credentials machine-wide is the one provenance that must sit on
	// the question line itself, unhidden — bundled skills keep the line bare
	// and their provenance in the i-text.
	// VolumeNames[i] is that claimant's machine-scoped volume name (may be
	// empty), disclosed in the i-text.
	Claimants   []string
	Labels      []string // same length as Claimants
	Foreign     []bool   // same length as Claimants
	VolumeNames []string // same length as Claimants; per-claimant
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

// StalePickNotice is the ONE definition of what byre says about a stored
// shared-auth pick that no live skill claims any more — a companion
// uninstalled, renamed, or replaced by a different package taking its id.
// Exported because three surfaces have to say the same thing about the same
// fact: the interactive offer, the apply path that skips the offer
// (defaults.skip_questions), and the config editor's read-only row.
func StalePickNotice(pick string) string {
	return fmt.Sprintf("your saved pick %q is no longer installed", pick)
}

// SharedAuthPrompt is the shared-auth offer's question line for agent — the
// ONE definition of that prose. Exported for tests: they assert the offer
// was (or wasn't) shown against the string the product actually prints, so
// the wording can change in one place. The prose is not a contract; its
// presence at the right moment is.
func SharedAuthPrompt(agent string) string {
	return fmt.Sprintf("Use machine-wide credentials to log in to %s?", agent)
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
func Pick(out io.Writer, r *bufio.Reader, templates, agents []Option, tmplFav, agentFav Favourite, sharedAuthFor func(agent string) SharedAuthOffer) (Choice, error) {
	fmt.Fprintln(out, "No byre.config here — let's set one up (press Enter to accept [default]).")

	tmpl, err := ask(out, r, "Template", withNone(templates), config.OrNone(tmplFav.Effective))
	if err != nil {
		return Choice{}, err
	}
	agent, err := ask(out, r, "Agent", withNone(agents), config.OrNone(agentFav.Effective))
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
		offer := sharedAuthFor(config.FromNone(agent))
		if len(offer.Claimants) > 0 {
			hadOffer = true
			prefWouldYes = offer.PrefYes || offer.PrefPick != ""
			prefPick = offer.PrefPick
			companion, sharedAuth, err = OfferSharedAuthChoice(out, r, config.FromNone(agent), offer)
			if err != nil {
				return Choice{}, err
			}
		}
	}
	// Choosing exactly what default.config already stores is not news:
	// offering to save it would be noise (and the save a no-op). Only ask when
	// saving would change the stored state.
	save := false
	wantSaveNews := config.FromNone(tmpl) != tmplFav.Stored || config.FromNone(agent) != agentFav.Stored
	if hadOffer {
		if sharedAuth != prefWouldYes {
			wantSaveNews = true
		} else if sharedAuth && companion != "" && prefPick != "" && companion != prefPick {
			// Multi-claim: accepted a different pick than the saved one.
			wantSaveNews = true
		}
	}
	if wantSaveNews {
		save, err = askYesNoDefault(out, r, "Save these as your default for new projects?", false)
		if err != nil {
			return Choice{}, err
		}
	}

	return Choice{
		Template:            config.FromNone(tmpl),
		Agent:               config.FromNone(agent),
		SaveDefault:         save,
		SharedAuthCompanion: companion,
		SharedAuth:          sharedAuth,
		SharedAuthOffered:   hadOffer,
	}, nil
}

// AskAxis prompts for a single axis (Template or Agent), offering a "none"
// option and pre-selecting def (the favourite). Returns "" for none. Used when a
// --template/--agent flag fixes one axis and the other still needs choosing.
func AskAxis(out io.Writer, r *bufio.Reader, label string, options []Option, def string) (string, error) {
	v, err := ask(out, r, label, withNone(options), config.OrNone(def))
	if err != nil {
		return "", err
	}
	return config.FromNone(v), nil
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
	volName := func(i int) string {
		if i >= 0 && i < len(offer.VolumeNames) {
			return offer.VolumeNames[i]
		}
		return ""
	}
	foreign := func(i int) bool {
		return i >= 0 && i < len(offer.Foreign) && offer.Foreign[i]
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
		// The question is self-disclosing ("machine-wide" IS the scope) and
		// bare when the claimant is byre's own; a foreign claimant's
		// provenance rides the question line, deliberately loud.
		q := SharedAuthPrompt(agent)
		if foreign(0) && label != "" {
			// A third party asking to hold machine-wide credentials is the
			// loud case (the ruling's whole point); a local skill is the
			// user's own work and stays lowercase.
			q += fmt.Sprintf(" (via %s — %s)", c, strings.Replace(label, "third-party", "THIRD-PARTY", 1))
		}
		for {
			fmt.Fprintf(out, "%s [%s]: ", q, marker)
			line, rerr := r.ReadString('\n')
			if rerr != nil && line == "" {
				return "", false, rerr
			}
			// `i` is this prompt's one extra key; everything else takes the
			// shared y/n reading (ClassifyAnswer), so this prompt can never
			// drift from the others' classification.
			if strings.ToLower(strings.TrimSpace(line)) == "i" {
				prov := ""
				if label != "" {
					prov = " (" + label + ")"
				}
				vol := "a machine-wide volume"
				if vn := volName(0); vn != "" {
					vol = fmt.Sprintf("the machine-wide volume %q", vn)
				}
				fmt.Fprintf(out, `
  This uses the skill %q%s to store one %s login
  in %s that every opted-in project's box mounts.
  y — this box uses the machine-wide shared %s login.
      Writes one line — skills = [%q] — into THIS project's byre.config
      (delete it there to undo). No other project changes.
  n — this box keeps its own separate %s login (log in inside the box).
      Writes nothing, anywhere.
  Afterwards, "Save these as your default for new projects?" only changes which answer is
  pre-selected at the NEXT project's question — saving never
  opts any box in by itself.

`, c, prov, agent, vol, agent, c, agent)
				continue
			}
			switch ClassifyAnswer(line) {
			case AnswerYes:
				return c, true, nil
			case AnswerNo:
				return "", false, nil
			case AnswerDefault:
				if prefYes {
					return c, true, nil
				}
				return "", false, nil
			default:
				// Unrecognized input reprompts — an `i` typo used to read as
				// a silent decline. EOF terminates via the empty
				// read at the top of the next pass.
				fmt.Fprintln(out, "unrecognized — y, n, i, or Enter for the default.")
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
		if vn := volName(i); vn != "" {
			fmt.Fprintf(out, "      machine-wide volume %q (shared credentials)\n", vn)
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
// accepts def; an invalid answer re-prompts. Disabled rows print above the
// prompt with their reason and are never in the offered set: byre says what is
// broken and why, then asks only about what works.
func ask(out io.Writer, r *bufio.Reader, label string, options []Option, def string) (string, error) {
	// The broken rows go above the prompt under their axis's own heading --
	// they print between two prompts, so an unheaded list reads as belonging
	// to the question above it.
	var broken []Option
	for _, o := range options {
		if o.Disabled != "" {
			broken = append(broken, o)
		}
	}
	if len(broken) > 0 {
		fmt.Fprintf(out, "%s — %d unavailable, not offered below:\n", label, len(broken))
		for _, o := range broken {
			name, reason := shown(o.Name), shown(o.Disabled)
			if o.Label != "" {
				fmt.Fprintf(out, "  %s — %s: %s\n", name, shown(o.Label), reason)
				continue
			}
			fmt.Fprintf(out, "  %s — %s\n", name, reason)
		}
	}
	// Escape for DISPLAY only; matching below compares the real values. The
	// default is a stored favourite -- a name out of default.config, byre's
	// file but the user's bytes -- so it rides the same rule as the rest.
	offered := Selectable(options)
	offeredShown := strings.Join(shownAll(offered), " ")
	defShown := shown(def)
	for {
		fmt.Fprintf(out, "%s — %s [%s]: ", label, offeredShown, defShown)
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		ans := strings.TrimSpace(line)
		if ans == "" {
			return def, nil
		}
		if d := disabledReason(options, ans); d != "" {
			// Named a row byre listed as broken: repeat the reason rather than
			// "not one of", which would read as a typo the user cannot find.
			fmt.Fprintf(out, "  %q is unavailable: %s\n", ans, shown(d))
			continue
		}
		if slices.Contains(offered, ans) {
			return ans, nil
		}
		fmt.Fprintf(out, "  %q is not one of: %s\n", ans, offeredShown)
	}
}

// AnswerClass is the one shared reading of a line typed at a yes/no prompt.
// Every interactive y/N prompt in byre classifies the same way: an explicit
// yes or no, an empty accept-the-default, and everything else REPROMPTS —
// unrecognized input never silently lands on either side: typing "banana" at
// the shared-auth offer used to read as a decline.
type AnswerClass int

const (
	AnswerYes AnswerClass = iota
	AnswerNo
	AnswerDefault
	AnswerRetry
)

// ClassifyAnswer maps one prompt line to its AnswerClass (case-insensitive,
// whitespace-trimmed; y/yes, n/no, empty, or retry).
func ClassifyAnswer(line string) AnswerClass {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return AnswerYes
	case "n", "no":
		return AnswerNo
	case "":
		return AnswerDefault
	}
	return AnswerRetry
}

// askYesNoDefault prompts [Y/n] or [y/N] per def; an empty answer accepts the
// default, an explicit y/n answers, and anything else reprompts. Exhausted
// input (EOF) surfaces as the read error on the next pass, so a garbage-only
// pipe terminates instead of granting or spinning.
func askYesNoDefault(out io.Writer, r *bufio.Reader, label string, def bool) (bool, error) {
	marker := "y/N"
	if def {
		marker = "Y/n"
	}
	for {
		fmt.Fprintf(out, "%s [%s]: ", label, marker)
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return false, err
		}
		switch ClassifyAnswer(line) {
		case AnswerYes:
			return true, nil
		case AnswerNo:
			return false, nil
		case AnswerDefault:
			return def, nil
		}
		fmt.Fprintln(out, "unrecognized — y, n, or Enter for the default.")
	}
}

// The "none" sentinel vocabulary is config's (config.NoneLabel); these thin
// wrappers keep the picker readable.
func withNone(opts []Option) []Option {
	return append(append([]Option{}, opts...), Option{Name: noneOption})
}
