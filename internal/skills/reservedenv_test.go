package skills

import (
	"slices"
	"strings"
	"testing"
)

// A key byre reads is a control byre can describe. A key it does not read is
// one it cannot: it wears the reserved prefix and nothing more, and the note
// must say that rather than announce a runtime control byre has never heard
// of. The DEGRADATION is identical either way (ADR 0050's conservative
// default) -- only the sentence differs.
func TestReservedEnvNoteDoesNotOverclaimAnUnknownKey(t *testing.T) {
	known := ReservedEnvNote(ReservedEnvSet{Skill: "firewall", Key: "BYRE_EGRESS"})
	if !strings.Contains(known, "byre runtime control") {
		t.Errorf("a chassis knob must be named as one: %q", known)
	}

	unknown := ReservedEnvNote(ReservedEnvSet{Skill: "pjlsergeant/devlog", Key: "BYRE_SCRATCH"})
	if !strings.Contains(unknown, "not a control this byre recognizes") {
		t.Errorf("an unrecognized key must say so: %q", unknown)
	}
	if strings.Contains(unknown, "byre runtime control") {
		t.Errorf("an unrecognized key must not be announced as a byre control: %q", unknown)
	}
	// Both name the skill, the key and the claims that ride it: the row is
	// still an attribution, not a shrug.
	for _, note := range []string{known, unknown} {
		if !strings.Contains(note, "claim(s) ride it") {
			t.Errorf("the note must name the claims that ride the key: %q", note)
		}
	}
	if !strings.Contains(unknown, "pjlsergeant/devlog") || !strings.Contains(unknown, "BYRE_SCRATCH") {
		t.Errorf("the note must attribute the key to its skill: %q", unknown)
	}
}

// The row-sized register carries the same two answers as the sentence: a
// chassis knob names the claims it skews, and a key byre does not read says
// so before naming them. A row that shortened its way into "byre runtime
// control" would put the overclaim back on a second surface.
func TestReservedEnvSkewKeepsBothRegisters(t *testing.T) {
	known := ReservedEnvSkew("BYRE_EGRESS")
	if known != "skews: network" {
		t.Errorf("a chassis knob's row note = %q, want the claims it skews", known)
	}

	unknown := ReservedEnvSkew("BYRE_SCRATCH")
	if !strings.Contains(unknown, "not a control byre recognizes") {
		t.Errorf("an unrecognized key must say so in the row note too: %q", unknown)
	}
	if !strings.Contains(unknown, "skews: network + launch") {
		t.Errorf("the row note must name the conservative claims: %q", unknown)
	}
	if strings.Contains(unknown, "runtime control") {
		t.Errorf("an unrecognized key must not be announced as a byre control: %q", unknown)
	}
}

// The conservative behavior is unchanged by the wording fix: an
// unrecognized key still degrades network AND launch, so every claim riding
// it stops asserting.
func TestReservedEnvUnknownKeyStillDegradesConservatively(t *testing.T) {
	got := ReservedEnvClaims("BYRE_SCRATCH")
	if !slices.Contains(got, ClaimNetwork) || !slices.Contains(got, ClaimLaunch) {
		t.Fatalf("unknown key claims = %v, want network + launch", got)
	}
	if ReservedEnvKnown("BYRE_SCRATCH") {
		t.Error("BYRE_SCRATCH is not a chassis knob byre reads")
	}
	if !ReservedEnvKnown("BYRE_LAUNCH_GATE_FILE") {
		t.Error("BYRE_LAUNCH_GATE_FILE is a chassis knob and must be recognized")
	}
	sets := []ReservedEnvSet{{Skill: "pjlsergeant/devlog", Key: "BYRE_SCRATCH"}}
	if !ReservedEnvTouches(sets, ClaimNetwork) {
		t.Error("an unrecognized key must still skew the network claim")
	}
}
