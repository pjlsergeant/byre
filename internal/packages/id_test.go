package packages

import (
	"strings"
	"testing"
)

func TestValidateID(t *testing.T) {
	ok := []struct {
		id   string
		bare bool
	}{
		{"claude", true},
		{"my-linter", true},
		{"pete/claude", true},
		{"pete/claude", false},
		{"pjlsergeant/codereview", false},
		{"a", true},
		{"a0/" + "b" + string(make([]byte, 0)), true},
	}
	// Fix the last silly case
	ok[len(ok)-1] = struct {
		id   string
		bare bool
	}{"byre/claude", false}

	for _, tc := range ok {
		if err := ValidateID(tc.id, tc.bare); err != nil {
			t.Errorf("ValidateID(%q, bareOK=%v): %v", tc.id, tc.bare, err)
		}
	}

	bad := []struct {
		id   string
		bare bool
		want string // fragment of the intended rule's message
	}{
		{"", true, "is empty"},
		{"none", true, "reserved"},
		{"!claude", true, "must not start with '!'"},
		{"Claude", true, "invalid segment"},  // uppercase
		{"has.dot", true, "invalid segment"}, // dots banned
		{"pete/claude/extra", true, "at most one '/'"},
		{"claude", false, "must be qualified"}, // bare not OK when require qualified
		{"-leading", true, "invalid segment"},
	}
	for _, tc := range bad {
		if err := ValidateID(tc.id, tc.bare); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ValidateID(%q, bareOK=%v): want error containing %q, got %v", tc.id, tc.bare, tc.want, err)
		}
	}
}

func TestExpandAliasAndBundledID(t *testing.T) {
	if got := BundledID("claude"); got != "byre/claude" {
		t.Fatalf("BundledID: %q", got)
	}
	if BareName("byre/claude") != "claude" || BareName("claude") != "claude" {
		t.Fatal("BareName")
	}
	if Owner("byre/claude") != "byre" || Owner("claude") != "" {
		t.Fatal("Owner")
	}
}

func TestEscapeTerminal(t *testing.T) {
	// CSI color sequence + NUL + newline
	in := "hello\x1b[31mred\x00world\n"
	got := EscapeTerminal(in)
	if got != "helloredworld" {
		t.Fatalf("EscapeTerminal CSI: %q", got)
	}
	// OSC (ESC ] ... BEL)
	osc := "x\x1b]0;title\x07y"
	if got := EscapeTerminal(osc); got != "xy" {
		t.Fatalf("EscapeTerminal OSC: %q", got)
	}
	// Lone ESC
	if got := EscapeTerminal("a\x1bb"); got != "ab" {
		t.Fatalf("EscapeTerminal lone ESC: %q", got)
	}
}
