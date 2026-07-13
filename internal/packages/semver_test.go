package packages

import "testing"

func TestMatchConstraint(t *testing.T) {
	cases := []struct {
		ver, c string
		want   bool
	}{
		{"0.2.1", ">=0.2.0", true},
		{"0.2.1", ">=0.3.0", false},
		{"0.2.1", ">0.2.1", false},
		{"0.2.1", ">0.2.0", true},
		{"0.2.1", "<=0.2.1", true},
		{"0.2.1", "<0.2.1", false},
		{"0.2.1", "=0.2.1", true},
		{"0.2.1", "0.2.1", true},
		{"v0.2.1", ">=0.2.0", true},
		{"0.2.1-devel", ">=0.2.0", true},
		{"1.0.0", "", true},
		{"0.0.0-devel", ">=0.1.0", false},
	}
	for _, tc := range cases {
		got, err := MatchConstraint(tc.ver, tc.c)
		if err != nil {
			t.Errorf("MatchConstraint(%q, %q): %v", tc.ver, tc.c, err)
			continue
		}
		if got != tc.want {
			t.Errorf("MatchConstraint(%q, %q) = %v, want %v", tc.ver, tc.c, got, tc.want)
		}
	}
}

func TestMatchConstraintBad(t *testing.T) {
	if _, err := MatchConstraint("nope", ">=1.0"); err == nil {
		t.Fatal("want error for bad version")
	}
	if _, err := MatchConstraint("1.0.0", ">=x"); err == nil {
		t.Fatal("want error for bad constraint")
	}
}
