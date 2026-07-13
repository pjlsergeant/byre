package commands

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
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
func onboardIfNeeded(s Streams, projectDir string, paths project.Paths, flagTemplate, flagAgent string, flagSharedAuth *bool) error {
	anyFlag := flagTemplate != "" || flagAgent != "" || flagSharedAuth != nil

	// The project's config lives in the host-side store, NOT the project tree, so
	// the (rw-mounted) project can't define its own sandbox.
	cfgPath := filepath.Join(paths.Dir, config.ProjectConfigName)

	if _, err := os.Stat(cfgPath); err == nil {
		// Already configured. --template/--agent only configure a NEW project, so
		// don't silently ignore them on an existing one — point at the file.
		if anyFlag {
			cur := ""
			if c, e := config.Load(projectDir); e == nil && c.Agent != "" {
				cur = fmt.Sprintf(" (currently agent=%s)", c.Agent)
			}
			return fmt.Errorf("this project is already configured%s — --template/--agent/--shared-auth only apply when creating a config.\nReconfigure by editing %s, or run 'byre forget' then re-run.", cur, cfgPath)
		}
		return nil
	}

	// Catalog lists options / resolves flags (bundled from embed.FS). Notices
	// on stderr so a first post-upgrade onboard surfaces LEGACY rows (D10).
	if err := builtins.EnsureStoreOut(paths.Home, s.Err); err != nil {
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

	// Shared-auth offer (ADR 0025 / D2): all catalog claimants with provenance
	// labels; multi-claim -> numbered picker; single-claim keeps [y/N].
	sharedAuthFor := func(agent string) onboard.SharedAuthOffer {
		return buildSharedAuthOffer(paths.Home, cat, agent)
	}

	// No flags at all: full picker on a TTY; on a non-TTY, don't prompt — develop
	// proceeds from the cascade.
	if !anyFlag {
		if !s.TTY {
			return nil
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
			// removes the agent's entry (no stored "no").
			if choice.Agent != "" {
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
			if companion = skills.SharedAuthCompanion(cat, a); companion == "" {
				return fmt.Errorf("--shared-auth: %s has no ready shared-auth companion skill", config.OrNone(a))
			}
			sharedAuth = true
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

// buildSharedAuthOffer assembles the D2 offer for agent: live claimants with
// provenance labels, saved pick prefill, and a stale-pick notice when needed.
func buildSharedAuthOffer(home string, cat *packages.Catalog, agent string) onboard.SharedAuthOffer {
	var offer onboard.SharedAuthOffer
	if agent == "" || cat == nil {
		return offer
	}
	claimants := skills.SharedAuthClaimants(cat, agent)
	for _, c := range claimants {
		display := c.Name
		label := "local"
		if ent, ok := cat.Lookup(c.Name); ok {
			if ent.Alias != "" {
				display = ent.Alias
			}
			switch ent.Provenance {
			case packages.ProvBundled:
				label = "bundled, byre's"
			case packages.ProvInstalled:
				label = "installed, third-party"
				if ent.Version != "" {
					label = "installed " + ent.Version + ", third-party"
				}
			case packages.ProvLocal:
				label = "local"
			}
		}
		if onboard.SharedAuthAlreadyOn(home, display) || onboard.SharedAuthAlreadyOn(home, c.Name) {
			continue
		}
		offer.Claimants = append(offer.Claimants, display)
		offer.Labels = append(offer.Labels, label)
		// Machine-volume note once, from the first claimant that has one.
		if offer.VolumeNote == "" {
			for _, v := range c.File.Volumes {
				if v.MachineScoped() {
					offer.VolumeNote = fmt.Sprintf("Note: companion mounts machine-scoped volume %q (shared credentials).", v.Name)
					break
				}
			}
		}
	}
	offer.PrefYes = onboard.SharedAuthPreference(home, agent)
	pick := onboard.SharedAuthPick(home, agent)
	if pick != "" {
		// Prefill only if the pick is still among live claimants.
		for _, c := range offer.Claimants {
			if c == pick {
				offer.PrefPick = pick
				break
			}
		}
		if offer.PrefPick == "" {
			// Saved pick missing/INVALID: no prefill + notice; leave store alone.
			offer.StalePickNotice = fmt.Sprintf("your saved pick %q is no longer installed", pick)
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
