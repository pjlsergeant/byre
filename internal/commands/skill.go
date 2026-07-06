package commands

import (
	"fmt"
	"path/filepath"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/project"
)

// SkillUpdate implements `byre skill update`: re-materialize byre's built-in
// skills AND templates into ~/.byre, overwriting stale copies with the shipped
// version (a differing copy is backed up under skills.bak/ / templates.bak/).
// This removes the need to hand-delete ~/.byre/skills/<name> to pick up
// shipped changes; templates ride along because they have the same problem and
// no command of their own. It updates the shared ~/.byre store, so it takes no
// project dir.
func SkillUpdate(s Streams) error {
	home, err := project.Home()
	if err != nil {
		return err
	}
	skills, err := builtins.UpdateSkills(filepath.Join(home, "skills"))
	if err != nil {
		return err
	}
	templates, err := builtins.UpdateTemplates(filepath.Join(home, "templates"))
	if err != nil {
		return err
	}
	if len(skills) == 0 && len(templates) == 0 {
		fmt.Fprintln(s.Err, "byre: built-in skills and templates already up to date.")
		return nil
	}
	report := func(kind string, changes []builtins.Change) {
		for _, u := range changes {
			if u.Backup != "" {
				fmt.Fprintf(s.Err, "byre: updated %s %q (prior copy kept at %s)\n", kind, u.Name, u.Backup)
			} else {
				fmt.Fprintf(s.Err, "byre: installed %s %q\n", kind, u.Name)
			}
		}
	}
	report("skill", skills)
	report("template", templates)
	if len(skills) > 0 {
		fmt.Fprintln(s.Err, "Run 'byre rebuild' (or 'byre develop') to apply the changes to the image.")
	}
	return nil
}
