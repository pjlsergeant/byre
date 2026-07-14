package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

// seedVolumes seeds any fresh state volume that declares a host-path seed.
// Seeding is one-time: an existing volume has already diverged and is left
// alone. A failed seed rolls back (removes the volume) so a half-seeded volume
// isn't later mistaken for "already seeded".
func seedVolumes(s volumeRunner, log io.Writer, paths project.Paths, image string, vols []config.Volume, ident runner.Identity) error {
	for _, v := range vols {
		if v.Role != "state" || v.Seed == nil {
			continue
		}
		name := volumeName(paths.ID, v.Name)
		exists, err := s.VolumeExists(name)
		if err != nil {
			return err
		}
		if exists {
			continue // already seeded; it has diverged — leave it alone
		}

		// Config-literal seed: write the inline (non-secret) content to a file in
		// a fresh volume. Loud, like host-path seeding.
		if v.Seed.Host == "" {
			fmt.Fprintf(log, "byre: seeding %s — writing literal content to %s in volume %s\n", v.Name, v.Seed.Path, name)
			if err := s.VolumeCreate(name); err != nil {
				return err
			}
			if err := s.SeedLiteral(name, v.Seed.Path, v.Seed.Literal, image, ident); err != nil {
				if rmErr := s.VolumeRemove(name); rmErr != nil {
					return fmt.Errorf("literal-seeding %s failed (%w); rollback of %s also failed (%v)", v.Name, err, name, rmErr)
				}
				return fmt.Errorf("literal-seeding %s: %w", v.Name, err)
			}
			fmt.Fprintf(log, "byre: seeded %s\n", v.Name)
			continue
		}

		host, err := expandHostPath(v.Seed.Host)
		if err != nil {
			return err
		}
		// A missing seed source is not an error: start the volume empty so the
		// agent authenticates on first launch (docker auto-creates it on run).
		if _, serr := os.Stat(host); serr != nil {
			if os.IsNotExist(serr) {
				fmt.Fprintf(log, "byre: seed source %s not found; %s starts empty\n", host, v.Name)
				continue
			}
			return serr
		}
		// Seeding copies host data into a container volume — never silent. This
		// only runs because the user explicitly configured a seed source.
		fmt.Fprintf(log, "byre: seeding %s — copying the full contents of host %s into volume %s (one-way). Configured by a seed in your byre config/skill.\n", v.Name, host, name)
		if err := s.VolumeCreate(name); err != nil {
			return err
		}
		if err := s.SeedVolume(name, host, image, ident); err != nil {
			if rmErr := s.VolumeRemove(name); rmErr != nil {
				return fmt.Errorf("seeding %s from %s failed (%w); rollback of volume %s also failed (%v) — remove it manually before retrying", v.Name, host, err, name, rmErr)
			}
			return fmt.Errorf("seeding %s from %s: %w", v.Name, host, err)
		}
		fmt.Fprintf(log, "byre: seeded %s\n", v.Name)
	}
	return nil
}

// seedPrefs copies the selected agent's curated, non-secret pref files (theme,
// keybindings) from the host into its FRESH state volume — one-time and opt-in
// (config seed_prefs). An existing volume has already diverged and is left
// alone. A missing host source dir is not an error (the box just starts without
// seeded prefs). A failed seed rolls back (removes the volume) so a half-seeded
// volume isn't later mistaken for "already seeded".
func seedPrefs(s volumeRunner, log io.Writer, paths project.Paths, image, agentState, from string, files []string, ident runner.Identity) error {
	if agentState == "" || from == "" || len(files) == 0 {
		return nil // nothing to seed (skill declares no prefs / no state volume)
	}
	name := volumeName(paths.ID, agentState)
	exists, err := s.VolumeExists(name)
	if err != nil {
		return err
	}
	if exists {
		return nil // already seeded; it has diverged — leave it alone
	}
	host, err := expandHostPath(from)
	if err != nil {
		return err
	}
	if _, serr := os.Stat(host); serr != nil {
		if os.IsNotExist(serr) {
			fmt.Fprintf(log, "byre: prefs source %s not found; %s starts without seeded prefs\n", host, agentState)
			return nil
		}
		return serr
	}
	// Seeding copies host data into a container volume — never silent. Only the
	// curated, non-secret files the skill vouches for are copied (missing ones are
	// skipped); this runs only because the user set seed_prefs.
	fmt.Fprintf(log, "byre: seeding prefs into %s — copying %v from host %s (one-time, non-secret prefs only; enabled by seed_prefs).\n", name, files, host)
	if err := s.VolumeCreate(name); err != nil {
		return err
	}
	if err := s.SeedFiles(name, host, files, image, ident); err != nil {
		if rmErr := s.VolumeRemove(name); rmErr != nil {
			return fmt.Errorf("seeding prefs into %s failed (%w); rollback of volume %s also failed (%v) — remove it manually before retrying", agentState, err, name, rmErr)
		}
		return fmt.Errorf("seeding prefs into %s: %w", agentState, err)
	}
	fmt.Fprintf(log, "byre: seeded prefs into %s\n", agentState)
	return nil
}
