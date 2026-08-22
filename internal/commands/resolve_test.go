package commands

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

// A cross-source collision must name WHO declared each side: "collides with
// mount /x" is a riddle when one /x is the user's and the other rode in with
// a skill (the report that prompted this read the flat error as breakage).
func TestValidateAttributesCrossSourceCollisions(t *testing.T) {
	var dockerHost skills.Skill
	dockerHost.Name = "byre/docker-host"
	dockerHost.File.Runtime.Mounts = []config.Mount{{Host: "/var/run/docker.sock", Target: "/var/run/docker.sock", Mode: "rw"}}

	cfg := config.Config{Mounts: []config.Mount{{Host: "~/notes", Target: "/var/run/docker.sock", Mode: "ro"}}}
	err := combine(merged(cfg), skills.Resolved{Skills: []skills.Skill{dockerHost}}).validate()
	if err == nil || !strings.Contains(err.Error(), "collides with") {
		t.Fatalf("cross-source target collision must be rejected by the collision rule, got: %v", err)
	}
	for _, want := range []string{"config's mount", "skill byre/docker-host's mount", "/var/run/docker.sock"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}

	// Two skills claiming the same volume name attribute both skills.
	var a, b skills.Skill
	a.Name = "pete/one"
	a.File.Volumes = []config.Volume{{Name: "shared", Role: "state", Target: "/home/dev/.x"}}
	b.Name = "pete/two"
	b.File.Volumes = []config.Volume{{Name: "shared", Role: "state", Target: "/home/dev/.y"}}
	err = combine(merged(config.Config{}), skills.Resolved{Skills: []skills.Skill{a, b}}).validate()
	if err == nil || !strings.Contains(err.Error(), "collides with") {
		t.Fatalf("skill-vs-skill volume name collision must be rejected by the collision rule, got: %v", err)
	}
	for _, want := range []string{"skill pete/one's volume shared", "skill pete/two's volume shared"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}

	// No collision: the combined set still passes.
	if err := combine(merged(cfg), skills.Resolved{}).validate(); err != nil {
		t.Fatalf("collision-free combine must validate: %v", err)
	}
}
