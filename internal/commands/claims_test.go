package commands

import (
	"reflect"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// The growth guard: every config.Config field must carry a claim
// classification, every classification must match a real field, and no
// entry may skip its note. This cannot prove a classification true --
// that is the review's job, made possible by the note being in the diff
// -- it proves the question was ANSWERED, which is the state the 2026-07
// review found missing four separate times.
func TestEveryConfigFieldHasClaimClassification(t *testing.T) {
	typ := reflect.TypeOf(config.Config{})
	fields := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		fields[name] = true
		cc, ok := claimSurface[name]
		if !ok {
			t.Errorf("config.Config.%s has no claimSurface entry -- classify it rendered(where) or inert(why) in claims.go", name)
			continue
		}
		if cc.note == "" {
			t.Errorf("claimSurface[%s]: the note is the reviewable content -- it can't be empty", name)
		}
	}
	for name := range claimSurface {
		if !fields[name] {
			t.Errorf("claimSurface entry %s matches no config.Config field (renamed? drop or rename the entry)", name)
		}
	}
}
