package commands

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/onboard"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// onboardIfNeeded runs the first-run picker (or applies flags) when a project
// has no byre.config. With BOTH axis flags it's non-interactive (no prompts at
// all, including the shared-auth offer); on a TTY it prompts for whatever the
// flags left open, favourites pre-selected; on a non-TTY with no flags it does
// nothing (develop proceeds from the cascade defaults). A given --shared-auth
// IS the offer's answer (either way), so the question is never asked; a
// non-TTY partially-flagged run errors instead of guessing the open axis from
// a favourite — favourites answer prompts, they don't consent for a new
// project, and there is no prompt to answer on a pipe.
// skipQuestions reads the machine-scoped picker preference. A missing or
// unparsable default.config reads as false: never skip on a guess.
func skipQuestions(home string) bool {
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"), true)
	if err != nil {
		return false
	}
	return cfg.Defaults.SkipQuestions
}

func onboardIfNeeded(s Streams, projectDir string, paths project.Paths, flagTemplate, flagAgent string, flagSharedAuth *bool) error {
	anyFlag := flagTemplate != "" || flagAgent != "" || flagSharedAuth != nil

	// The project's config lives in the host-side store, NOT the project tree, so
	// the (rw-mounted) project can't define its own sandbox.
	cfgPath := filepath.Join(paths.Dir, config.ProjectConfigName)

	// A probe that could not look is not proof the project is unconfigured:
	// falling through would run the first-run picker over an existing config
	// and write the answers on top of it. Say which path could not be read.
	ok, perr := hostopen.ExistsNoFollow(cfgPath)
	if perr != nil {
		return fmt.Errorf("cannot tell whether %s exists (%v) — fix the store's permissions, or run 'byre forget' to clear it", cfgPath, perr)
	}
	if ok {
		// Already configured. --template/--agent only configure a NEW project, so
		// don't silently ignore them on an existing one — point at the file.
		if anyFlag {
			cur := ""
			if c, e := config.Load(projectDir); e == nil && c.Agent != "" {
				cur = fmt.Sprintf(" (currently agent=%s)", c.Agent)
			}
			return fmt.Errorf("this project is already configured%s — --template/--agent/--shared-auth only apply when creating a config.\nReconfigure with 'byre config' (or edit %s), or run 'byre forget' then re-run.", cur, cfgPath)
		}
		return nil
	}

	// Catalog lists options / resolves flags. Silent EnsureStore: develop
	// already ran EnsureStoreOut so a second noticed call would double-print
	// LEGACY lines. Standalone paths without a prior notice still
	// prepare the store; they just skip the human lines here.
	if err := builtins.EnsureStoreOut(paths.Home, nil); err != nil {
		return err
	}
	cat, err := builtins.LoadCatalogRaw(paths.Home)
	if err != nil {
		return err
	}
	templates := config.ListTemplatesCatalog(cat)
	agents := skills.ListAgentSkills(cat)

	// Drop stale favourites that no longer name a real template/agent, so
	// accepting the default can't write an invalid byre.config.
	rawT, rawA := onboard.Favourites(paths.Home)
	defT := keepIfIn(rawT, templates)
	defA := keepIfIn(rawA, agents)

	// One buffered reader for ALL onboarding prompts: a fresh bufio per
	// question would drop whatever the previous one buffered ahead.
	in := bufio.NewReader(s.In)

	// Shared-auth offer: all catalog claimants with provenance labels;
	// multi-claim -> numbered picker; single-claim keeps [y/N]. (The offer
	// itself is per-box, ADR 0025.)
	sharedAuthFor := func(agent string) onboard.SharedAuthOffer {
		return buildSharedAuthOffer(paths.Home, cat, agent)
	}

	// No flags at all: full picker on a TTY; on a non-TTY, don't prompt — develop
	// proceeds from the cascade.
	if !anyFlag {
		if !s.TTY {
			return nil
		}
		// defaults.skip_questions: a standing instruction, set at machine
		// scope, that new projects take their stored answers unasked. Honour
		// it -- including the shared-auth pick, which GRANTS (the companion
		// goes into the new project's config). ADR 0025 records this as its
		// SECOND suppression, alongside the companion already sitting in
		// default.config's skills.
		//
		// It does not meet the first suppression's bar (skip only a question
		// whose answer could not matter), so it is allowed on a different
		// one: an explicit standing instruction, and never silent -- develop
		// says what it configured and which companion it enabled. NOT
		// hand-written, whatever an earlier version of this comment claimed:
		// the usual way to set it is a checkbox in `byre config --global`,
		// which is why that checkbox names the credential consequence before
		// it is ticked rather than after.
		if skipQuestions(paths.Home) {
			// The stored pick gets the SAME live-claimants check the offer
			// applies (buildSharedAuthOffer's Claimants loop). Without it this
			// path took a stored name on faith and wrote it into the new
			// project's skills: a companion since uninstalled failed the next
			// develop on an unknown skill, and a DIFFERENT package that took the
			// id since got the machine-wide credential grant silently -- the
			// stored answer consented to a skill, not to a name.
			companion, stale := "", ""
			if defA != "" && onboard.SharedAuthPreference(paths.Home, defA) {
				if pick := onboard.SharedAuthPick(paths.Home, defA); pick != "" {
					if skills.SharedAuthPickLive(cat, defA, pick) {
						companion = pick
					} else {
						stale = onboard.StalePickNotice(pick)
					}
				}
			}
			fmt.Fprintln(s.Err, "byre: configuring from your defaults without asking (defaults.skip_questions in ~/.byre/default.config).")
			if companion != "" {
				fmt.Fprintf(s.Err, "byre: shared credentials enabled for %s via %s — your stored answer.\n", defA, companion)
			}
			if stale != "" {
				fmt.Fprintf(s.Err, "byre: %s — shared credentials NOT enabled for %s; run `byre config --global` or re-onboard a project to answer again.\n", stale, defA)
			}
			return writeAndReport(s.Err, cfgPath, defT, defA, optedSkills(companion, companion != ""))
		}
		choice, err := onboard.Pick(s.Err, in, templates, agents,
			onboard.Favourite{Stored: rawT, Effective: defT},
			onboard.Favourite{Stored: rawA, Effective: defA},
			sharedAuthFor)
		if err != nil {
			return err
		}
		// Machine-level records first, the project's byre.config LAST: once
		// byre.config exists this project never onboards again, so a failed
		// default.config write must abort while onboarding can still re-run
		// (the recorded answers are idempotent and skip their prompts on the
		// re-run).
		if choice.SaveDefault {
			if err := onboard.SaveDefault(paths.Home, choice.Template, choice.Agent); err != nil {
				return err
			}
			// Shared-auth: yes+companion writes table-shape pick; decline
			// removes the agent's entry (no stored "no"). Only when the offer
			// was made — a no-offer save must not touch the stored favourite.
			if choice.SharedAuthOffered && choice.Agent != "" {
				if err := onboard.SaveSharedAuthDefaultPick(paths.Home, choice.Agent, choice.SharedAuthCompanion, choice.SharedAuth); err != nil {
					return err
				}
			}
			fmt.Fprintln(s.Err, "byre: saved as your default for new projects.")
		}
		return writeAndReport(s.Err, cfgPath, choice.Template, choice.Agent, optedSkills(choice.SharedAuthCompanion, choice.SharedAuth))
	}

	// Resolve explicitly-flagged axes first, so a bad flag value fails fast —
	// before we prompt for the other axis.
	t, tFixed := defT, false
	if flagTemplate != "" {
		v, err := resolveFlag(flagTemplate, defT, templates, "template")
		if err != nil {
			return err
		}
		t, tFixed = v, true
	}
	a, aFixed := defA, false
	if flagAgent != "" {
		v, err := resolveFlag(flagAgent, defA, agents, "agent")
		if err != nil {
			return err
		}
		a, aFixed = v, true
	}

	// An un-flagged axis needs an answer, and on a non-TTY nobody can give
	// one: refuse rather than guess. A favourite is what Enter means at a
	// prompt — there is no Enter on a pipe, and silently writing it into a
	// NEW project's config would turn a preference into an unconsented,
	// persistent choice.
	if !s.TTY && !(tFixed && aFixed) {
		return fmt.Errorf("non-interactive onboarding needs both --template and --agent (pass %q to skip one) — run on a TTY to be asked for the rest", "none")
	}

	// Choose any un-flagged axis: prompt for it on a TTY (the picker, just
	// that axis). We never silently inherit the favourite for an un-flagged
	// axis.
	if s.TTY && (!tFixed || !aFixed) {
		fmt.Fprintln(s.Err, "byre: no byre.config — choosing the rest interactively (Enter accepts [default]).")
	}
	if !tFixed && s.TTY {
		v, err := onboard.AskAxis(s.Err, in, "Template", templates, defT)
		if err != nil {
			return err
		}
		t = v
	}
	if !aFixed && s.TTY {
		v, err := onboard.AskAxis(s.Err, in, "Agent", agents, defA)
		if err != nil {
			return err
		}
		a = v
	}
	// A given --shared-auth IS the answer: apply it (loudly refusing a yes
	// the chosen agent has no ready companion for) and never ask. Otherwise
	// the offer joins the other prompts, BEFORE anything is written (an EOF
	// mid-prompting aborts with no side effects). Both axes flag-fixed = the
	// caller asked for a fully non-interactive onboarding (scripts,
	// wrappers): no prompts means no offer either; a partially-flagged TTY
	// run was already interactive, so it rides along.
	companion, sharedAuth := "", false
	if flagSharedAuth != nil {
		if *flagSharedAuth {
			c, n := skills.SharedAuthCompanion(cat, a)
			switch {
			case n == 0:
				return fmt.Errorf("--shared-auth: %s has no ready shared-auth companion skill", config.OrNone(a))
			case n > 1:
				// Several claim the pairing and byre picks between rivals for
				// nobody -- least of all for a credential grant. Name them, and
				// name both routes out.
				var names []string
				for _, cl := range skills.SharedAuthClaimants(cat, a) {
					names = append(names, cl.Name)
				}
				return fmt.Errorf("--shared-auth: %d skills claim to be %s's shared-auth companion (%s) — byre won't choose between them; disable all but one, or drop --shared-auth and pick at the prompt", n, config.OrNone(a), strings.Join(names, ", "))
			}
			companion, sharedAuth = c, true
		}
	} else if s.TTY && !(tFixed && aFixed) {
		offer := sharedAuthFor(a)
		if len(offer.Claimants) > 0 {
			var err error
			companion, sharedAuth, err = onboard.OfferSharedAuthChoice(s.Err, in, a, offer)
			if err != nil {
				return err
			}
		}
	}
	return writeAndReport(s.Err, cfgPath, t, a, optedSkills(companion, sharedAuth))
}

// offeredPickRow finds the DISPLAYED claimant row a stored pick names, under
// the shared spelling rule. Separate from liveness on purpose: liveness asks
// whether the package still exists, this asks whether the picker has a row to
// preselect, and the two answers differ for a claimant filtered out as already
// granted machine-wide.
func offeredPickRow(cat *packages.Catalog, claimants []string, pick string) (string, bool) {
	for _, c := range claimants {
		if skills.SameSkillRef(cat, c, pick) {
			return c, true
		}
	}
	return "", false
}

// buildSharedAuthOffer assembles the shared-auth offer for agent: live claimants with
// provenance labels, saved pick prefill, and a stale-pick notice when needed.
func buildSharedAuthOffer(home string, cat *packages.Catalog, agent string) onboard.SharedAuthOffer {
	var offer onboard.SharedAuthOffer
	if agent == "" || cat == nil {
		return offer
	}
	claimants := skills.SharedAuthClaimants(cat, agent)
	for _, c := range claimants {
		display := c.Name
		label, foreign := "local skill", true
		if ent, ok := cat.Lookup(c.Name); ok {
			if ent.Alias != "" {
				display = ent.Alias
			}
			switch ent.Provenance {
			case packages.ProvBundled:
				label, foreign = "bundled with byre", false
			case packages.ProvInstalled:
				label = "third-party, installed"
				if ent.Version != "" {
					label = "third-party, installed " + ent.Version
				}
			case packages.ProvLocal:
				label = "local skill"
			}
		}
		if onboard.SharedAuthAlreadyOn(home, display) || onboard.SharedAuthAlreadyOn(home, c.Name) {
			continue
		}
		offer.Claimants = append(offer.Claimants, display)
		offer.Labels = append(offer.Labels, label)
		offer.Foreign = append(offer.Foreign, foreign)
		// Per-claimant machine-volume disclosure rides the i-text and the
		// picker rows; the question itself says "machine-wide".
		vol := ""
		for _, v := range c.File.Volumes {
			if v.MachineScoped() {
				vol = v.Name
				break
			}
		}
		offer.VolumeNames = append(offer.VolumeNames, vol)
	}
	offer.PrefYes = onboard.SharedAuthPreference(home, agent)
	if pick := onboard.SharedAuthPick(home, agent); pick != "" {
		// One rule, three outcomes, because a stored pick can be any of three
		// things and each prefills differently.
		//
		// The pick is resolved against the rows this offer will SHOW, using
		// the same alias equality the liveness check uses (SameSkillRef): a
		// pick stored canonically whose claimant displays as its alias is the
		// same package, and a byte comparison made it live-but-unselectable --
		// which then defaulted the multi-claim picker to N despite a valid
		// stored preference.
		switch row, shown := offeredPickRow(cat, offer.Claimants, pick); {
		case shown:
			offer.PrefPick = row
		case skills.SharedAuthPickLive(cat, agent, pick):
			// Live, but not on offer: already enabled machine-wide, so it was
			// filtered out. Nothing is wrong, so no notice -- but the yes must
			// NOT stand. A prefilled Yes with the picked companion absent hands
			// Enter to whichever rival remains, granting machine-wide
			// credentials to a package the user never chose.
			offer.PrefYes = false
		default:
			// Missing/INVALID: no prefill + notice; leave the store alone.
			offer.StalePickNotice = onboard.StalePickNotice(pick)
			offer.PrefYes = false
		}
	}
	return offer
}

// optedSkills turns the shared-auth offer's outcome (ADR 0025) into the
// skills to write into this box's byre.config: the companion on a yes,
// nothing otherwise — a "no" is not recorded anywhere; the next project's
// onboarding simply asks about its own box.
func optedSkills(companion string, yes bool) []string {
	if companion == "" || !yes {
		return nil
	}
	return []string{companion}
}

func writeAndReport(w io.Writer, configPath, template, agent string, skills []string) error {
	if err := onboard.WriteProjectConfig(configPath, template, agent, skills); err != nil {
		return err
	}
	if len(skills) > 0 {
		fmt.Fprintf(w, "byre: wrote %s (template=%s, agent=%s, skills=%s)\n", configPath, config.OrNone(template), config.OrNone(agent), strings.Join(skills, ", "))
		return nil
	}
	fmt.Fprintf(w, "byre: wrote %s (template=%s, agent=%s)\n", configPath, config.OrNone(template), config.OrNone(agent))
	return nil
}

// resolveFlag maps a flag value to a config value: "" (unspecified) → favourite;
// "none" → "" (explicit none); a value in options → that value; otherwise error.
func resolveFlag(flag, fav string, options []string, label string) (string, error) {
	switch {
	case flag == "":
		return fav, nil
	case flag == "none":
		return "", nil
	case slices.Contains(options, flag):
		return flag, nil
	default:
		return "", fmt.Errorf("unknown %s %q; available: %s, none", label, flag, strings.Join(options, ", "))
	}
}

// keepIfIn returns v if it is empty or present in options, else "" (drops a
// stale favourite).
func keepIfIn(v string, options []string) string {
	if v == "" || slices.Contains(options, v) {
		return v
	}
	return ""
}
