package commands

import "testing"

func TestShellQuoteJoin(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"byre", "deliver", "--boxes"}, "'byre' 'deliver' '--boxes'"},
		{[]string{"/opt/my byre/byre"}, "'/opt/my byre/byre'"},
		{[]string{"--box", "it's"}, `'--box' 'it'\''s'`},
		{[]string{"a$b`c\"d"}, "'a$b`c\"d'"}, // single quotes neutralize everything else
	}
	for _, tc := range cases {
		if got := shellQuoteJoin(tc.in); got != tc.want {
			t.Errorf("shellQuoteJoin(%v) = %s, want %s", tc.in, got, tc.want)
		}
	}
}
