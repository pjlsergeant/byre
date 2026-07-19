package packages

import (
	"strings"
	"testing"
)

func TestParseManifestCore(t *testing.T) {
	raw := []byte(`
[package]
id = "pete/claude"
version = "1.0.0"
kind = "skill"
package_api = 1
requires_byre = ">=0.2.0"
description = "hi"

[build]
apt = ["curl"]
unknown_future_key = true
`)
	m, ok, err := ParseManifestCore(raw)
	if err != nil || !ok {
		t.Fatalf("ParseManifestCore: ok=%v err=%v", ok, err)
	}
	if m.ID != "pete/claude" || m.PackageAPI != 1 || m.RequiresByre != ">=0.2.0" {
		t.Fatalf("manifest: %+v", m)
	}
}

func TestParseManifestCoreAbsent(t *testing.T) {
	m, ok, err := ParseManifestCore([]byte(`description = "x"`))
	if err != nil || ok || m.ID != "" {
		t.Fatalf("want absent: ok=%v m=%+v err=%v", ok, m, err)
	}
}

func TestCheckCompatibility(t *testing.T) {
	m := Manifest{PackageAPI: 1, RequiresByre: ">=0.2.0"}
	if err := CheckCompatibility(m, "0.2.1"); err != nil {
		t.Fatal(err)
	}
	if err := CheckCompatibility(m, "0.1.0"); err == nil || !strings.Contains(err.Error(), "requires byre") {
		t.Fatalf("want requires_byre failure, got %v", err)
	}
	if err := CheckCompatibility(Manifest{PackageAPI: 99}, "0.2.1"); err == nil || !strings.Contains(err.Error(), "package_api") {
		t.Fatalf("want package_api failure, got %v", err)
	}
	// Dev binary passes every requires_byre (Pete ruling, round 3).
	if err := CheckCompatibility(Manifest{RequiresByre: ">=99.0.0"}, "0.0.0-devel"); err != nil {
		t.Fatalf("devel should pass any requires_byre: %v", err)
	}
}

func TestStripPackageTable(t *testing.T) {
	raw := []byte(`description = "hi"

[package]
id = "x"
version = "1"

[build]
apt = ["a"]
`)
	out := string(StripPackageTable(raw))
	if strings.Contains(out, "[package]") || strings.Contains(out, `id = "x"`) {
		t.Fatalf("package table not stripped: %s", out)
	}
	if !strings.Contains(out, `description = "hi"`) || !strings.Contains(out, "[build]") {
		t.Fatalf("body damaged: %s", out)
	}
}

func TestGenerateBundledHeader(t *testing.T) {
	h := GenerateBundledHeader("byre/claude", "skill", "v0.2.1", "The agent")
	if !strings.Contains(h, `id = "byre/claude"`) || !strings.Contains(h, `version = "v0.2.1"`) {
		t.Fatal(h)
	}
	if !strings.Contains(h, `requires_byre = ">=0.2.1"`) {
		t.Fatal(h)
	}
}

func TestStripPackageTableMidFile(t *testing.T) {
	raw := []byte(`description = "hi"

[build]
apt = ["a"]

[package]
id = "x"
version = "1"

[runtime]
env = { K = "v" }
`)
	out := string(StripPackageTable(raw))
	if strings.Contains(out, "[package]") || strings.Contains(out, `id = "x"`) {
		t.Fatalf("package table not stripped: %s", out)
	}
	if !strings.Contains(out, "[build]") || !strings.Contains(out, "[runtime]") {
		t.Fatalf("body damaged: %s", out)
	}
}

// A `[package]`-shaped line inside a multiline string is DATA (a Dockerfile
// heredoc is the canonical case): the strip must neither truncate the string
// nor mistake it for the real table — and must still strip the real one.
func TestStripPackageTableIgnoresMultilineStrings(t *testing.T) {
	for _, c := range []struct{ name, quote string }{
		{"literal", "'''"},
		{"basic", `"""`},
	} {
		raw := []byte(`[build]
dockerfile = [` + c.quote + `
RUN cat <<'EOF'
[package]
payload text
[other]
EOF
` + c.quote + `]

[package]
id = "owner/example"
version = "1"

[runtime]
env = { K = "v" }
`)
		out := string(StripPackageTable(raw))
		for _, want := range []string{"payload text", "EOF", c.quote + "]", "[runtime]"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: string content lost (%q missing):\n%s", c.name, want, out)
			}
		}
		if strings.Contains(out, `id = "owner/example"`) {
			t.Errorf("%s: real [package] table survived:\n%s", c.name, out)
		}
	}
}

// A multiline string VALUE inside the real [package] table is stripped with
// its table, including its continuation lines.
func TestStripPackageTableStripsMultilineValueInPackage(t *testing.T) {
	raw := []byte(`[package]
id = "x"
description = """
multi [build]
line"""

[build]
apt = ["a"]
`)
	out := string(StripPackageTable(raw))
	if strings.Contains(out, "multi") || strings.Contains(out, `id = "x"`) {
		t.Fatalf("package table's multiline value survived:\n%s", out)
	}
	if !strings.Contains(out, `apt = ["a"]`) {
		t.Fatalf("body damaged:\n%s", out)
	}
}
