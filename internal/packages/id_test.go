package packages

import "testing"

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
	}{
		{"", true},
		{"none", true},
		{"!claude", true},
		{"Claude", true},  // uppercase
		{"has.dot", true}, // dots banned
		{"pete/claude/extra", true},
		{"claude", false}, // bare not OK when require qualified
		{"-leading", true},
	}
	for _, tc := range bad {
		if err := ValidateID(tc.id, tc.bare); err == nil {
			t.Errorf("ValidateID(%q, bareOK=%v): want error", tc.id, tc.bare)
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
	in := "hello\x1b[31mred\x00world\n"
	got := EscapeTerminal(in)
	if got != "helloredworld" {
		t.Fatalf("EscapeTerminal: %q", got)
	}
}
