package tomldoc

import (
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

// nasty is the corpus file: hand-formatting, comments of every attachment
// kind, exotic-but-legal constructs. The preservation contract is asserted
// against it throughout: bytes outside an edit's span survive identically.
const nasty = `# Top-of-file banner comment.
# Second banner line.

base = "node:22" # inline: why this base
engine = 'docker'

# glued to apt
apt = [
  "jq", # why jq
  "git",
]

sneaky.dotted = "root-dotted"

# glued comment for env
[env]
FOO = "bar"

[unknown_things]
weird = { inline = "table" }

[[mcp]]
# glued inside block
name = "github"
command = ["gh", "stdio"]

[[mcp]]
name = "linear"
url = "https://mcp.linear.app/mcp"

# trailing file comment
`

func load(t *testing.T, src string) *Doc {
	t.Helper()
	d, err := Load([]byte(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return d
}

// reparses under the strict product decoder shape: the edit engine must
// never emit TOML the parser refuses.
func mustParse(t *testing.T, d *Doc) map[string]any {
	t.Helper()
	var m map[string]any
	if err := toml.Unmarshal(d.Bytes(), &m); err != nil {
		t.Fatalf("edited document does not parse: %v\n%s", err, d.Bytes())
	}
	return m
}

func TestSetKeyReplacesValueInPlaceKeepingInlineComment(t *testing.T) {
	d := load(t, nasty)
	if err := d.SetKey(nil, "base", String("golang:1.26-bookworm")); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if !strings.Contains(out, `base = "golang:1.26-bookworm" # inline: why this base`) {
		t.Fatalf("value not replaced in place with inline comment kept:\n%s", out)
	}
	// Single-quoted exotic spelling untouched elsewhere.
	if !strings.Contains(out, `engine = 'docker'`) {
		t.Fatalf("unrelated exotic construct was touched:\n%s", out)
	}
	if got := mustParse(t, d)["base"]; got != "golang:1.26-bookworm" {
		t.Fatalf("base = %v", got)
	}
}

func TestSetKeyReplacesMultilineArrayValue(t *testing.T) {
	d := load(t, nasty)
	if err := d.SetKey(nil, "apt", StringArray([]string{"jq", "git", "curl"})); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if !strings.Contains(out, `apt = ["jq", "git", "curl"]`) {
		t.Fatalf("array not rewritten in house shape:\n%s", out)
	}
	// The glued comment above apt survives; the element comment inside the
	// OLD value is part of the edited span and goes with it (the edit
	// targeted the construct).
	if !strings.Contains(out, "# glued to apt\n") {
		t.Fatalf("glued comment lost:\n%s", out)
	}
}

func TestSetKeyAddsRootKeyBeforeFirstHeader(t *testing.T) {
	d := load(t, nasty)
	if err := d.SetKey(nil, "agent", String("claude")); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if strings.Index(out, `agent = "claude"`) > strings.Index(out, "[env]") {
		t.Fatalf("root key landed after a table header (belongs to that table):\n%s", out)
	}
	m := mustParse(t, d)
	if m["agent"] != "claude" {
		t.Fatalf("agent = %v", m["agent"])
	}
	// It must land after the LAST root key, keeping root keys grouped.
	if strings.Index(out, `agent = "claude"`) < strings.Index(out, "sneaky.dotted") {
		t.Fatalf("new root key should follow existing root keys:\n%s", out)
	}
}

func TestSetKeyInTableAndAbsentTable(t *testing.T) {
	d := load(t, nasty)
	if err := d.SetKey([]string{"env"}, "BAR", String("baz")); err != nil {
		t.Fatal(err)
	}
	if err := d.SetKey([]string{"sources"}, "acme/tool", String("https://x")); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if !strings.Contains(out, "FOO = \"bar\"\nBAR = \"baz\"\n") {
		t.Fatalf("env.BAR should follow env.FOO:\n%s", out)
	}
	if !strings.Contains(out, "[sources]\n\"acme/tool\" = \"https://x\"\n") {
		t.Fatalf("absent table should append with quoted key:\n%s", out)
	}
	m := mustParse(t, d)
	env := m["env"].(map[string]any)
	if env["BAR"] != "baz" {
		t.Fatalf("env = %v", env)
	}
}

func TestRemoveKeyTakesGluedCommentAndInlineComment(t *testing.T) {
	d := load(t, nasty)
	if err := d.RemoveKey(nil, "apt"); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if strings.Contains(out, "glued to apt") || strings.Contains(out, "why jq") {
		t.Fatalf("removal must take glued + interior comments:\n%s", out)
	}
	// The blank-line-separated banner above stays.
	if !strings.Contains(out, "# Second banner line.") {
		t.Fatalf("banner comment wrongly removed:\n%s", out)
	}
	mustParse(t, d)
}

func TestArrayTableReplaceByIdentity(t *testing.T) {
	d := load(t, nasty)
	body := KV("name", String("github")) + KV("command", StringArray([]string{"gh-mcp", "serve"}))
	ok, err := d.ReplaceArrayTable("mcp", "name", "github", body)
	if err != nil || !ok {
		t.Fatalf("replace: ok=%v err=%v", ok, err)
	}
	out := string(d.Bytes())
	if !strings.Contains(out, "[[mcp]]\nname = \"github\"\ncommand = [\"gh-mcp\", \"serve\"]\n") {
		t.Fatalf("block not replaced in house shape:\n%s", out)
	}
	// The other block and its position are untouched.
	if !strings.Contains(out, "url = \"https://mcp.linear.app/mcp\"") {
		t.Fatalf("sibling block damaged:\n%s", out)
	}
	// An INTERIOR comment belongs to the edited construct: replacing the
	// block re-renders it in house shape, interior comments included-out
	// (the approved normalization default).
	if strings.Contains(out, "# glued inside block") {
		t.Fatalf("interior comment should go with the replaced construct:\n%s", out)
	}
	mustParse(t, d)
}

func TestArrayTableAppendAndRemove(t *testing.T) {
	d := load(t, nasty)
	if err := d.AppendArrayTable("mcp", KV("name", String("sentry"))+KV("url", String("https://mcp.sentry.dev"))); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	// Appended after the LAST [[mcp]] block, before the trailing comment...
	// no: the trailing comment is not a header; append goes at the last
	// block's end which extends to it. Assert order github < linear < sentry.
	gi, li, si := strings.Index(out, `"github"`), strings.Index(out, `"linear"`), strings.Index(out, `"sentry"`)
	if !(gi < li && li < si) {
		t.Fatalf("append order wrong (%d %d %d):\n%s", gi, li, si, out)
	}

	ok, err := d.RemoveArrayTable("mcp", "name", "linear")
	if err != nil || !ok {
		t.Fatalf("remove: ok=%v err=%v", ok, err)
	}
	out = string(d.Bytes())
	if strings.Contains(out, "linear") {
		t.Fatalf("linear block not removed:\n%s", out)
	}
	m := mustParse(t, d)
	mcps := m["mcp"].([]any)
	if len(mcps) != 2 {
		t.Fatalf("mcp blocks = %d", len(mcps))
	}
}

func TestUntouchedBytesAreIdentical(t *testing.T) {
	d := load(t, nasty)
	if err := d.SetKey(nil, "base", String("debian:bookworm")); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	// Everything after the edited line is byte-identical to the original.
	tail := nasty[strings.Index(nasty, "engine = 'docker'"):]
	if !strings.HasSuffix(out, tail) {
		t.Fatalf("bytes after the edit drifted")
	}
	head := nasty[:strings.Index(nasty, "base = ")]
	if !strings.HasPrefix(out, head) {
		t.Fatalf("bytes before the edit drifted")
	}
}

func TestNoOpsAndAbsentTargets(t *testing.T) {
	d := load(t, nasty)
	if err := d.RemoveKey(nil, "not-there"); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.RemoveArrayTable("mcp", "name", "nope"); err != nil || ok {
		t.Fatalf("remove absent: ok=%v err=%v", ok, err)
	}
	if err := d.RemoveTable([]string{"nope"}); err != nil {
		t.Fatal(err)
	}
	if string(d.Bytes()) != nasty {
		t.Fatalf("no-ops must not modify the document")
	}
}

func TestRemoveTableTakesBlock(t *testing.T) {
	d := load(t, nasty)
	if err := d.RemoveTable([]string{"unknown_things"}); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if strings.Contains(out, "unknown_things") || strings.Contains(out, "inline = ") {
		t.Fatalf("table block not fully removed:\n%s", out)
	}
	// The [[mcp]] blocks after it are untouched.
	if !strings.Contains(out, "[[mcp]]\n# glued inside block\nname = \"github\"") {
		t.Fatalf("following blocks damaged:\n%s", out)
	}
	mustParse(t, d)
}

func TestEmptyAndFreshDocuments(t *testing.T) {
	d := load(t, "")
	if err := d.SetKey(nil, "base", String("node:22")); err != nil {
		t.Fatal(err)
	}
	if err := d.SetKey([]string{"env"}, "FOO", String("1")); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendArrayTable("context", KV("name", String("rules"))+KV("text", String("Line one.\nLine two.\n"))); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	want := "base = \"node:22\"\n\n[env]\nFOO = \"1\"\n\n[[context]]\nname = \"rules\"\ntext = '''\nLine one.\nLine two.\n'''\n"
	if out != want {
		t.Fatalf("fresh document shape:\n got: %q\nwant: %q", out, want)
	}
	m := mustParse(t, d)
	ctx := m["context"].([]any)
	if ctx[0].(map[string]any)["text"] != "Line one.\nLine two.\n" {
		t.Fatalf("multiline prose round-trip: %q", ctx[0])
	}
}

// The prose renderer's fallback: text a literal string cannot carry still
// round-trips exactly, just escaped.
func TestProseRendererFallback(t *testing.T) {
	cases := []string{
		"contains ''' delimiter\nsecond line",
		"control\x01char",
		"ends with quote'",
		"tab\tand\nnewline mix",
		`backslash \n literal survives`,
	}
	for _, tc := range cases {
		d := load(t, "")
		if err := d.SetKey(nil, "text", String(tc)); err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := toml.Unmarshal(d.Bytes(), &m); err != nil {
			t.Fatalf("%q: %v\n%s", tc, err, d.Bytes())
		}
		if m["text"] != tc {
			t.Fatalf("round-trip %q -> %q", tc, m["text"])
		}
	}
}
