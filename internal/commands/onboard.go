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
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// onboardIfNeeded runs the first-run picker (or applies flags) when a project
// has no byre.config. With BOTH flags it's non-interactive (no prompts at all,
// including the shared-auth offer); on a TTY it prompts for whatever the flags
// left open, favourites pre-selected; on a non-TTY with no flags it does
// nothing (develop proceeds from the cascade defaults).
func onboardIfNeeded(s Streams, projectDir string, paths project.Paths, flagTemplate, flagAgent string) error {
	anyFlag := flagTemplate != "" || flagAgent != ""

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
			return fmt.Errorf("this project is already configured%s — --template/--agent only apply when creating a config.\nReconfigure by editing %s, or run 'byre forget' then re-run.", cur, cfgPath)
		}
		return nil
	}

	// Need the built-ins materialized to list options / resolve flags.
	templatesDir := filepath.Join(paths.Home, "templates")
	skillsDir := filepath.Join(paths.Home, "skills")
	if err := builtins.EnsureStore(paths.Home); err != nil {
		return err
	}
	templates := config.ListTemplates(templatesDir)
	agents := skills.ListAgentSkills(skillsDir)

	// Drop stale favourites that no longer name a real template/agent, so
	// accepting the default can't write an invalid byre.config.
	rawT, rawA := onboard.Favourites(paths.Home)
	defT := keepIfIn(rawT, templates)
	defA := keepIfIn(rawA, agents)

	// One buffered reader for ALL onboarding prompts: a fresh bufio per
	// question would drop whatever the previous one buffered ahead.
	in := bufio.NewReader(s.In)

	// The shared-auth offer's gate (ADR 0025): only an agent with a ready
	// companion, and only when the companion isn't already granted
	// machine-wide (in default.config's skills, hand-made — then the cascade
	// covers every box and a per-box answer would be a lie). The second
	// return is the saved preference prefilling the offer's default answer.
	companionFor := func(agent string) (string, bool) {
		c := skills.SharedAuthCompanion(skillsDir, agent)
		if c == "" || onboard.SharedAuthAlreadyOn(paths.Home, c) {
			return "", false
		}
		return c, onboard.SharedAuthPreference(paths.Home, agent)
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
			companionFor)
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
			// "These" is every answer just given: when the shared-auth offer
			// was among them, its answer is saved as the preference that
			// prefills future offers — a favourite exactly like template and
			// agent, never a grant (each box's grant is only ever its own
			// byre.config, written below).
			if choice.SharedAuthCompanion != "" {
				if err := onboard.SaveSharedAuthDefault(paths.Home, choice.Agent, choice.SharedAuth); err != nil {
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

	// Choose any un-flagged axis: prompt for it on a TTY (the picker, just that
	// axis), or fall back to the favourite on a non-TTY. (At least one axis is
	// flag-fixed here, so at most one axis prompt happens.) We never silently
	// inherit the favourite for an un-flagged axis on a TTY.
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
	// The shared-auth offer joins the other prompts, BEFORE anything is
	// written (an EOF mid-prompting aborts with no side effects). Both axes
	// flag-fixed = the caller asked for a fully non-interactive onboarding
	// (scripts, wrappers): no prompts means no offer either; a
	// partially-flagged TTY run was already interactive, so it rides along.
	companion, sharedAuth := "", false
	if s.TTY && !(tFixed && aFixed) {
		var pref bool
		if companion, pref = companionFor(a); companion != "" {
			yes, err := onboard.OfferSharedAuth(s.Err, in, a, pref)
			if err != nil {
				return err
			}
			sharedAuth = yes
		}
	}
	return writeAndReport(s.Err, cfgPath, t, a, optedSkills(companion, sharedAuth))
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
