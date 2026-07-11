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
// has no byre.config. With flags it's non-interactive; on a TTY it prompts with
// favourites pre-selected; on a non-TTY with no flags it does nothing (develop
// proceeds from the cascade defaults).
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

	// No flags at all: full picker on a TTY; on a non-TTY, don't prompt — develop
	// proceeds from the cascade.
	if !anyFlag {
		if !s.TTY {
			return nil
		}
		choice, err := onboard.Pick(s.Err, in, templates, agents,
			onboard.Favourite{Stored: rawT, Effective: defT},
			onboard.Favourite{Stored: rawA, Effective: defA})
		if err != nil {
			return err
		}
		if err := writeAndReport(s.Err, cfgPath, choice.Template, choice.Agent); err != nil {
			return err
		}
		if choice.SaveDefault {
			if err := onboard.SaveDefault(paths.Home, choice.Template, choice.Agent); err != nil {
				return err
			}
			fmt.Fprintln(s.Err, "byre: saved as your default for new projects.")
		}
		return offerSharedAuth(s.Err, in, paths.Home, skillsDir, choice.Agent)
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
	if err := writeAndReport(s.Err, cfgPath, t, a); err != nil {
		return err
	}
	if !s.TTY {
		return nil
	}
	return offerSharedAuth(s.Err, in, paths.Home, skillsDir, a)
}

// offerSharedAuth asks the one-time shared-auth question (ADR 0023) when the
// chosen agent has a READY companion skill (one declaring shared_auth_for)
// and the user hasn't answered before. Yes enables the companion in
// ~/.byre/default.config — machine-wide, the honest scope of a shared login;
// no is recorded there too (shared_auth_declined), so the offer never nags.
// Only called on a TTY.
func offerSharedAuth(w io.Writer, in io.Reader, home, skillsDir, agent string) error {
	if agent == "" {
		return nil
	}
	companion := skills.SharedAuthCompanion(skillsDir, agent)
	if companion == "" || onboard.SharedAuthAnswered(home, agent, companion) {
		return nil
	}
	yes, err := onboard.OfferSharedAuth(w, in, agent, companion)
	if err != nil {
		return err
	}
	if yes {
		if err := onboard.EnableSharedAuth(home, companion); err != nil {
			return err
		}
		fmt.Fprintf(w, "byre: enabled %q for every project on this machine (skills in ~/.byre/default.config; remove it there to undo).\n", companion)
		return nil
	}
	if err := onboard.DeclineSharedAuth(home, agent); err != nil {
		return err
	}
	fmt.Fprintf(w, "byre: okay — you won't be asked again for %s (shared_auth_declined in ~/.byre/default.config; remove it there to be re-asked).\n", agent)
	return nil
}

func writeAndReport(w io.Writer, configPath, template, agent string) error {
	if err := onboard.WriteProjectConfig(configPath, template, agent); err != nil {
		return err
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
