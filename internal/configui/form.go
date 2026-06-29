package configui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"

	"byre/internal/config"
)

// Run shows the interactive editor for cfg and returns the edited config plus
// whether the user submitted (false = aborted, leave the file alone). templates
// and agents populate the selects. filePath is shown so the user knows where it
// writes and where the read-only raw fields live.
func Run(title, filePath string, cfg config.Config, templates, agents []string) (config.Config, bool, error) {
	base := cfg.Base
	engine := orDefault(cfg.Engine, "auto")
	template := orNone(cfg.Template)
	agent := orNone(cfg.Agent)
	apt := formatList(cfg.Apt)
	env := formatEnv(cfg.Env)
	mounts := formatMounts(cfg.Mounts)

	fields := []huh.Field{
		huh.NewNote().Title(title).Description("Edits save to:\n" + filePath),
		huh.NewInput().Title("Base image").Value(&base),
		huh.NewSelect[string]().Title("Template").Options(noneOptions(templates)...).Value(&template),
		huh.NewSelect[string]().Title("Agent").Options(noneOptions(agents)...).Value(&agent),
		huh.NewSelect[string]().Title("Engine").Options(huh.NewOptions("auto", "docker", "podman")...).Value(&engine),
		huh.NewText().Title("apt packages (one per line)").Value(&apt),
		huh.NewText().Title("Env (KEY=VALUE per line)").Value(&env).
			Validate(func(s string) error { _, err := parseEnv(s); return err }),
		huh.NewText().Title("Mounts (host -> target (ro|rw), one per line)").Value(&mounts).
			Validate(func(s string) error { _, err := parseMounts(s); return err }),
	}
	if note := rawFieldsNote(cfg, filePath); note != "" {
		fields = append(fields, huh.NewNote().Title("Advanced (read-only here)").Description(note))
	}

	form := huh.NewForm(huh.NewGroup(fields...))
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return cfg, false, nil
		}
		return cfg, false, err
	}

	// Apply onto a copy of cfg so untouched fields (raw blocks, volumes, files)
	// are preserved exactly.
	out := cfg
	out.Base = strings.TrimSpace(base)
	out.Engine = fromAuto(engine)
	out.Template = fromNone(template)
	out.Agent = fromNone(agent)
	out.Apt = parseList(apt)
	e, err := parseEnv(env)
	if err != nil {
		return cfg, false, err
	}
	out.Env = e
	m, err := parseMounts(mounts)
	if err != nil {
		return cfg, false, err
	}
	out.Mounts = m
	return out, true, nil
}

// rawFieldsNote describes the powerful fields the form shows but does not edit,
// pointing the user at the file to change them by hand.
func rawFieldsNote(cfg config.Config, filePath string) string {
	var b strings.Builder
	add := func(label string, vals []string) {
		if len(vals) > 0 {
			fmt.Fprintf(&b, "%s: %s\n", label, strings.Join(vals, " | "))
		}
	}
	add("run_args", cfg.RunArgs)
	add("dockerfile_pre", cfg.DockerfilePre)
	add("dockerfile_post", cfg.DockerfilePost)
	if cfg.Dockerfile != "" {
		fmt.Fprintf(&b, "dockerfile (full opt-out): %s\n", cfg.Dockerfile)
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String() + "\nTo change these, edit " + filePath + " directly."
}

const noneOption = "none"

func noneOptions(opts []string) []huh.Option[string] {
	return huh.NewOptions(append(append([]string{}, opts...), noneOption)...)
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

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// fromAuto maps the "auto" engine selection back to "" so it's omitted from the
// written config (auto is the default).
func fromAuto(v string) string {
	if v == "auto" {
		return ""
	}
	return v
}
