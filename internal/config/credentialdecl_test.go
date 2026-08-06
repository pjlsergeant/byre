package config

import (
	"strings"
	"testing"
)

func TestValidateCredentialDecl(t *testing.T) {
	ok := CredentialDecl{Name: "stripe", Kind: "env", Target: "STRIPE_API_KEY"}
	if err := ValidateCredentialDecl(ok); err != nil {
		t.Fatalf("valid decl rejected: %v", err)
	}
	// Each rejection asserts the RULE that fired (a fragment + the offending
	// value), never full sentences.
	cases := []struct {
		name string
		decl CredentialDecl
		want string // rule fragment
	}{
		{"empty name", CredentialDecl{Kind: "env", Target: "X"}, "name"},
		{"digit lead", CredentialDecl{Name: "9x", Kind: "env", Target: "X"}, "starting with a letter"},
		{"upper name", CredentialDecl{Name: "Stripe", Kind: "env", Target: "X"}, "lowercase"},
		{"missing kind", CredentialDecl{Name: "a", Target: "X"}, "kind is required"},
		{"bad kind", CredentialDecl{Name: "a", Kind: "secret", Target: "X"}, "want env|file"},
		{"missing target", CredentialDecl{Name: "a", Kind: "env"}, "target is required"},
		{"bad target", CredentialDecl{Name: "a", Kind: "env", Target: "not a var"}, "not a valid environment variable"},
		{"reserved target", CredentialDecl{Name: "a", Kind: "env", Target: "BYRE_EGRESS"}, "BYRE_ namespace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCredentialDecl(tc.decl)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want the %q rule", err, tc.want)
			}
		})
	}
}

func TestCredentialsLayerRules(t *testing.T) {
	// Duplicate names in one layer: the genus rule fires.
	c := Config{Credentials: []CredentialDecl{
		{Name: "a", Kind: "env", Target: "A"},
		{Name: "a", Kind: "env", Target: "B"},
	}}
	if err := c.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "appears twice") {
		t.Fatalf("duplicate names: got %v, want the appears-twice rule", err)
	}
	// Duplicate targets across entries: the target-collision rule.
	c = Config{Credentials: []CredentialDecl{
		{Name: "a", Kind: "env", Target: "X"},
		{Name: "b", Kind: "file", Target: "X"},
	}}
	if err := c.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "target X collides") {
		t.Fatalf("duplicate targets: got %v, want the target-collision rule", err)
	}
	// A `!name` closure marker is legal in a layer, name-only.
	c = Config{Credentials: []CredentialDecl{{Name: "!a"}}}
	if err := c.ValidateLayer(); err != nil {
		t.Fatalf("bare closure marker in a layer: %v", err)
	}
	// A marker carrying extra fields suggests a mistyped real declaration.
	c = Config{Credentials: []CredentialDecl{{Name: "!a", Kind: "env"}}}
	if err := c.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "closure marker takes only a name") {
		t.Fatalf("marker with fields: got %v, want the marker-extras rule", err)
	}
	// Resolved configs reject surviving markers.
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "only meaningful in a cascade layer") {
		t.Fatalf("marker in resolved: got %v, want the resolved-marker rule", err)
	}
}

func TestMergeCredentials(t *testing.T) {
	base := Config{Credentials: []CredentialDecl{
		{Name: "stripe", Kind: "env", Target: "STRIPE_KEY"},
		{Name: "github", Kind: "env", Target: "GH_TOKEN"},
	}}
	over := Config{Credentials: []CredentialDecl{
		{Name: "!github"}, // closure removes the inherited decl
		{Name: "stripe", Kind: "file", Target: "STRIPE_KEY"}, // replace by name
	}}
	m := Merge(base, over)
	if len(m.Credentials) != 1 || m.Credentials[0].Kind != "file" {
		t.Fatalf("merged credentials = %+v", m.Credentials)
	}
	if len(m.CredentialsClosed) != 1 || m.CredentialsClosed[0] != "github" {
		t.Fatalf("closures = %v", m.CredentialsClosed)
	}
	// A later layer's plain declaration re-opens a closure.
	re := Merge(m, Config{Credentials: []CredentialDecl{{Name: "github", Kind: "env", Target: "GH_TOKEN"}}})
	if len(re.Credentials) != 2 || len(re.CredentialsClosed) != 0 {
		t.Fatalf("re-open: decls=%+v closed=%v", re.Credentials, re.CredentialsClosed)
	}
}

func TestCredentialsParseRoundtrip(t *testing.T) {
	src := `
[[credentials]]
name   = "stripe"
kind   = "env"
target = "STRIPE_API_KEY"
`
	c, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(c.Credentials) != 1 || c.Credentials[0].Target != "STRIPE_API_KEY" {
		t.Fatalf("parsed = %+v", c.Credentials)
	}
	if err := c.ValidateLayer(); err != nil {
		t.Fatalf("ValidateLayer: %v", err)
	}
}
