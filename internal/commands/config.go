package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"byre/internal/builtins"
	"byre/internal/config"
	"byre/internal/configui"
	"byre/internal/onboard"
	"byre/internal/project"
	"byre/internal/skills"
)

// Config implements `byre config` — the interactive editor for this project's
// host-side store config (~/.byre/projects/<id>/byre.config), and, with global,
// the global ~/.byre/default.config. Both are byre-owned/host-side, so editing
// them never touches the project tree.
func Config(projectDir string, global bool) error {
	home, err := project.Home()
	if err != nil {
		return err
	}
	templatesDir := filepath.Join(home, "templates")
	skillsDir := filepath.Join(home, "skills")
	_ = builtins.MaterializeTemplates(templatesDir)
	_ = builtins.MaterializeSkills(skillsDir)
	templates := onboard.ListTemplates(templatesDir)
	agents := skills.ListAgentSkills(skillsDir)

	var path, title string
	if global {
		path = filepath.Join(home, "default.config")
		title = "byre global config  (~/.byre/default.config)"
	} else {
		paths, perr := project.Resolve(projectDir)
		if perr != nil {
			return perr
		}
		if berr := paths.Bootstrap(); berr != nil {
			return berr
		}
		path = filepath.Join(paths.Dir, config.ProjectConfigName)
		title = "byre project config  (" + paths.ID + ")"
	}

	cur, err := config.ParseFile(path)
	if err != nil {
		return err
	}
	edited, ok, err := configui.Run(title, path, cur, templates, agents)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "byre: config unchanged.")
		return nil
	}
	if err := configui.Save(path, edited); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "byre: wrote %s\n", path)
	return nil
}
