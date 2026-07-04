package commands

import (
	"fmt"
	"path/filepath"

	"byre/internal/builtins"
	"byre/internal/project"
)

// SkillUpdate implements `byre skill update`: re-materialize byre's built-in
// skills into ~/.byre/skills, overwriting stale copies with the shipped version
// (a differing copy is backed up to <name>.bak). This removes the need to
// hand-delete ~/.byre/skills/<name> to pick up skill changes.
func SkillUpdate(s Streams, projectDir string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	skillsDir := filepath.Join(paths.Home, "skills")
	updated, err := builtins.UpdateSkills(skillsDir)
	if err != nil {
		return err
	}
	if len(updated) == 0 {
		fmt.Fprintln(s.Err, "byre: built-in skills already up to date.")
		return nil
	}
	for _, u := range updated {
		if u.Backup != "" {
			fmt.Fprintf(s.Err, "byre: updated skill %q (prior copy kept at %s)\n", u.Name, u.Backup)
		} else {
			fmt.Fprintf(s.Err, "byre: installed skill %q\n", u.Name)
		}
	}
	fmt.Fprintln(s.Err, "Run 'byre rebuild' (or 'byre develop') to apply the changes to the image.")
	return nil
}
