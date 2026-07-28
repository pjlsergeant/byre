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

// Replacing or removing the LAST block must not consume a trailing comment
// separated by a blank line — while a
// blank-separated comment BETWEEN a block's key-values stays block content.
func TestBlockEndLeavesDetachedTrailingComment(t *testing.T) {
	src := `[[mcp]]
name = "solo"

# note between kvs, blank-separated

command = ["x"]
# glued after last kv

# detached trailing file comment
`
	d := load(t, src)
	ok, err := d.ReplaceArrayTable("mcp", "name", "solo", KV("name", String("solo"))+KV("url", String("https://x")))
	if err != nil || !ok {
		t.Fatalf("replace: ok=%v err=%v", ok, err)
	}
	out := string(d.Bytes())
	if !strings.Contains(out, "# detached trailing file comment") {
		t.Fatalf("detached trailing comment consumed:\n%s", out)
	}
	// Interior + glued comments belonged to the replaced construct.
	if strings.Contains(out, "between kvs") || strings.Contains(out, "glued after last kv") {
		t.Fatalf("interior comments must go with the replaced construct:\n%s", out)
	}
	mustParse(t, d)

	d2 := load(t, src)
	ok, err = d2.RemoveArrayTable("mcp", "name", "solo")
	if err != nil || !ok {
		t.Fatalf("remove: ok=%v err=%v", ok, err)
	}
	out = string(d2.Bytes())
	if !strings.Contains(out, "# detached trailing file comment") {
		t.Fatalf("remove consumed the detached comment:\n%s", out)
	}
	if strings.Contains(out, "solo") || strings.Contains(out, "command") {
		t.Fatalf("block not fully removed:\n%s", out)
	}
}

// A table that exists only through dotted spellings gains new keys in the
// same spelling — emitting a [table] header would redefine the implicit
// table (invalid TOML). Removal matches dotted entries by full path.
func TestDottedSpellingSetAndRemove(t *testing.T) {
	d := load(t, "env.FOO = \"bar\" # keep my comment\nbase = \"node:22\"\n")
	if err := d.SetKey([]string{"env"}, "BAR", String("baz")); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if !strings.Contains(out, "env.BAR = \"baz\"") || strings.Contains(out, "[env]") {
		t.Fatalf("new key should join its dotted kin, not open a header:\n%s", out)
	}
	m := mustParse(t, d)
	env := m["env"].(map[string]any)
	if env["FOO"] != "bar" || env["BAR"] != "baz" {
		t.Fatalf("env = %v", env)
	}

	if err := d.RemoveKey([]string{"env"}, "FOO"); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveKey([]string{"env"}, "BAR"); err != nil {
		t.Fatal(err)
	}
	out = string(d.Bytes())
	if strings.Contains(out, "env") || strings.Contains(out, "keep my comment") {
		t.Fatalf("dotted entries (and their line comments) must remove by full path:\n%s", out)
	}
	mustParse(t, d)
}

// A descendant subtable ([mcp.headers] under [[mcp]]) is block content:
// replace/remove must take it, and it must not migrate to
// a preceding peer; a same-named key INSIDE the subtable is not the block's
// identity.
func TestBlockIncludesDescendantSubtables(t *testing.T) {
	src := `[[mcp]]
name = "first"

[[mcp]]
name = "github"

[mcp.headers]
Authorization = "token"
name = "decoy-not-identity"

[env]
FOO = "bar"
`
	d := load(t, src)
	ok, err := d.ReplaceArrayTable("mcp", "name", "github", KV("name", String("github"))+KV("url", String("https://x")))
	if err != nil || !ok {
		t.Fatalf("replace: ok=%v err=%v", ok, err)
	}
	out := string(d.Bytes())
	if strings.Contains(out, "Authorization") || strings.Contains(out, "decoy") {
		t.Fatalf("descendant subtable must go with the replaced block:\n%s", out)
	}
	if !strings.Contains(out, "[env]\nFOO = \"bar\"") {
		t.Fatalf("following unrelated table damaged:\n%s", out)
	}
	mustParse(t, d)

	// The decoy name inside the subtable must never match as identity.
	d2 := load(t, src)
	if ok, _ := d2.RemoveArrayTable("mcp", "name", "decoy-not-identity"); ok {
		t.Fatal("a subtable key must not match as block identity")
	}
	ok, err = d2.RemoveArrayTable("mcp", "name", "github")
	if err != nil || !ok {
		t.Fatalf("remove: ok=%v err=%v", ok, err)
	}
	out = string(d2.Bytes())
	if strings.Contains(out, "Authorization") {
		t.Fatalf("subtable left behind on remove (would migrate to peer):\n%s", out)
	}
	if !strings.Contains(out, "\"first\"") || !strings.Contains(out, "[env]") {
		t.Fatalf("neighbors damaged:\n%s", out)
	}
	mustParse(t, d2)
}

// Integer identities normalize numerically: `container = 3_000` and hex
// spellings match the canonical decimal identity. Raw-token comparison
// silently missed them, so a port edit appended a duplicate instead of
// replacing.
func TestIntegerIdentityNormalizes(t *testing.T) {
	d := load(t, "[[ports]]\ncontainer = 3_000\n\n[[ports]]\ncontainer = 0xBB8 # 3000 in hex? no — 0xBB8 is 3000\n")
	ok, err := d.RemoveArrayTable("ports", "container", "3000")
	if err != nil || !ok {
		t.Fatalf("remove by canonical identity: ok=%v err=%v", ok, err)
	}
	// The first spelled match went; the second (also 3000) is now the match.
	ok, err = d.RemoveArrayTable("ports", "container", "3000")
	if err != nil || !ok {
		t.Fatalf("second spelling must also match: ok=%v err=%v", ok, err)
	}
	if strings.Contains(string(d.Bytes()), "container") {
		t.Fatalf("ports not fully removed:\n%s", d.Bytes())
	}
	mustParse(t, d)
}

// An edit that targets a member INSIDE an inline table rewrites that
// construct in house shape: the inline line goes, a [table] block carries the
// members plus the edit. Appending a second definition instead produced valid
// syntax that the strict decoder refuses -- the file byre had just written was
// unloadable.
func TestSetKeyInsideInlineTableRewritesConstruct(t *testing.T) {
	src := "base = \"node:22\"\n" +
		"# what the defaults are for\n" +
		"defaults = { skip_questions = true, template = 'go' } # trailing\n" +
		"engine = \"docker\"\n"
	d := load(t, src)
	if err := d.SetKey([]string{"defaults"}, "skip_questions", Bool(false)); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if strings.Contains(out, "defaults = {") {
		t.Fatalf("inline construct should be gone:\n%s", out)
	}
	if n := strings.Count(out, "[defaults]"); n != 1 {
		t.Fatalf("want exactly one [defaults] header, got %d:\n%s", n, out)
	}
	m := mustParse(t, d)
	def := m["defaults"].(map[string]any)
	if def["skip_questions"] != false {
		t.Fatalf("skip_questions = %v", def["skip_questions"])
	}
	// Sibling members ride the rewrite, spelling intact.
	if def["template"] != "go" {
		t.Fatalf("sibling member lost: %v", def)
	}
	if !strings.Contains(out, "template = 'go'") {
		t.Fatalf("sibling member should keep its spelling:\n%s", out)
	}
	// The rest of the document is untouched, and a following root key does
	// NOT get swallowed into the new table.
	if m["engine"] != "docker" {
		t.Fatalf("following root key swallowed by the new table: %v", m)
	}
	if !strings.Contains(out, "# what the defaults are for\n[defaults]") {
		t.Fatalf("the comment attached to the construct should follow it:\n%s", out)
	}
}

// Removing one member of an inline table rewrites the construct too -- the
// silent byte-identical no-op left the removed key live in the file.
func TestRemoveKeyInsideInlineTable(t *testing.T) {
	d := load(t, "defaults = { skip_questions = true, template = 'go' }\nbase = \"node:22\"\n")
	if err := d.RemoveKey([]string{"defaults"}, "skip_questions"); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if strings.Contains(out, "skip_questions") {
		t.Fatalf("member not removed:\n%s", out)
	}
	m := mustParse(t, d)
	def := m["defaults"].(map[string]any)
	if _, ok := def["skip_questions"]; ok {
		t.Fatalf("defaults = %v", def)
	}
	if def["template"] != "go" {
		t.Fatalf("surviving member lost: %v", def)
	}
	if m["base"] != "node:22" {
		t.Fatalf("unrelated key damaged: %v", m)
	}
}

// Removing the LAST member takes the whole construct: an empty [defaults]
// block would keep asserting a table the caller asked to drop, which is what
// reconcile's remove-when-inherit means.
func TestRemoveKeyLastInlineMemberRemovesConstruct(t *testing.T) {
	d := load(t, "base = \"node:22\"\n\n# describes defaults\ndefaults = { skip_questions = true } # trailing\n\n[env]\nFOO = \"bar\"\n")
	if err := d.RemoveKey([]string{"defaults"}, "skip_questions"); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	for _, gone := range []string{"defaults", "skip_questions", "describes defaults", "trailing"} {
		if strings.Contains(out, gone) {
			t.Fatalf("construct not fully removed (%q survives):\n%s", gone, out)
		}
	}
	m := mustParse(t, d)
	if _, ok := m["defaults"]; ok {
		t.Fatalf("defaults survives: %v", m)
	}
	if m["base"] != "node:22" || m["env"].(map[string]any)["FOO"] != "bar" {
		t.Fatalf("neighbours damaged: %v", m)
	}
}

// Nesting inside the construct rides the rewrite as dotted keys under the one
// header, and a member with no leaves of its own still declares its table.
func TestInlineTableNestedMembersSurviveRewrite(t *testing.T) {
	d := load(t, "d = { a = { b = 1 }, c = {}, z = 0xBB8 }\n")
	if err := d.SetKey([]string{"d"}, "z", Int(7)); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if !strings.Contains(out, "a.b = 1") {
		t.Fatalf("nested member should spell dotted under the header:\n%s", out)
	}
	m := mustParse(t, d)
	dm := m["d"].(map[string]any)
	if dm["a"].(map[string]any)["b"] != int64(1) {
		t.Fatalf("nested member lost: %v", dm)
	}
	if _, ok := dm["c"].(map[string]any); !ok {
		t.Fatalf("empty nested table lost: %v", dm)
	}
	if dm["z"] != int64(7) {
		t.Fatalf("z = %v", dm["z"])
	}

	// Addressing INTO the nesting works too, and the edit displaces the
	// member it lands on rather than spelling the key twice.
	if err := d.SetKey([]string{"d", "a"}, "b", Int(2)); err != nil {
		t.Fatal(err)
	}
	if err := d.SetKey([]string{"d"}, "c", String("scalar-now")); err != nil {
		t.Fatal(err)
	}
	m = mustParse(t, d)
	dm = m["d"].(map[string]any)
	if dm["a"].(map[string]any)["b"] != int64(2) || dm["c"] != "scalar-now" {
		t.Fatalf("d = %v", dm)
	}
}

// Inside an [[array]] element, position IS identity: a member edited in the
// FIRST element must stay in the first element. Promoting the construct to a
// [mcp.headers] block appends it after the LAST element, which is where TOML
// then says it lives -- a header silently moved to another server, with both
// spellings parsing cleanly.
func TestInlineTableInArrayElementKeepsItsElement(t *testing.T) {
	src := "[[mcp]]\nname = \"first\"\nheaders = { Authorization = \"tok\", Accept = \"json\" } # keep\n\n[[mcp]]\nname = \"second\"\n"
	d := load(t, src)
	if err := d.SetKey([]string{"mcp", "headers"}, "Authorization", String("rotated")); err != nil {
		t.Fatal(err)
	}
	m := mustParse(t, d)
	blocks := m["mcp"].([]any)
	first := blocks[0].(map[string]any)
	second := blocks[1].(map[string]any)
	h, ok := first["headers"].(map[string]any)
	if !ok {
		t.Fatalf("the member left the element that declared it: %v", m)
	}
	if h["Authorization"] != "rotated" || h["Accept"] != "json" {
		t.Fatalf("first element's headers = %v", h)
	}
	if _, stray := second["headers"]; stray {
		t.Fatalf("the second element gained a member it never declared: %v", second)
	}
	out := string(d.Bytes())
	if strings.Contains(out, "[mcp.headers]") {
		t.Fatalf("the construct must stay inline where it stands:\n%s", out)
	}
	if !strings.Contains(out, "} # keep") {
		t.Fatalf("in-place rewrite should keep the trailing comment:\n%s", out)
	}

	// Emptying it takes the member's whole line, and still only its own.
	if err := d.RemoveKey([]string{"mcp", "headers"}, "Authorization"); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveKey([]string{"mcp", "headers"}, "Accept"); err != nil {
		t.Fatal(err)
	}
	m = mustParse(t, d)
	blocks = m["mcp"].([]any)
	if _, ok := blocks[0].(map[string]any)["headers"]; ok {
		t.Fatalf("emptied construct survives: %v", blocks[0])
	}
	if blocks[0].(map[string]any)["name"] != "first" || blocks[1].(map[string]any)["name"] != "second" {
		t.Fatalf("elements damaged: %v", blocks)
	}
	if strings.Contains(string(d.Bytes()), "keep") {
		t.Fatalf("the member's line should be gone whole:\n%s", d.Bytes())
	}
}

// The in-place treatment follows the descendant rule, not just the header
// line: a [mcp.extra] subtable is its element's content, so a construct
// inside it belongs to that element too.
func TestInlineTableInDescendantSubtableKeepsItsElement(t *testing.T) {
	src := "[[mcp]]\nname = \"first\"\n\n[mcp.extra]\nheaders = { A = \"1\", B = \"2\" }\n\n[[mcp]]\nname = \"second\"\n"
	d := load(t, src)
	if err := d.SetKey([]string{"mcp", "extra", "headers"}, "A", String("edited")); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if strings.Contains(out, "[mcp.extra.headers]") {
		t.Fatalf("a construct under an element's subtable must stay inline:\n%s", out)
	}
	blocks := mustParse(t, d)["mcp"].([]any)
	h := blocks[0].(map[string]any)["extra"].(map[string]any)["headers"].(map[string]any)
	if h["A"] != "edited" || h["B"] != "2" {
		t.Fatalf("first element's subtable construct = %v", h)
	}
	if _, stray := blocks[1].(map[string]any)["extra"]; stray {
		t.Fatalf("the second element gained a subtable it never declared: %v", blocks[1])
	}
}

// Creating structure on a path that runs through an array of tables is
// refused: whichever element the new key landed in, the caller never named
// it. Editing an existing construct there is a different question, answered
// above.
func TestSetKeyRefusesCreationUnderArrayOfTables(t *testing.T) {
	src := "[[mcp]]\nname = \"first\"\nheaders = { A = \"1\" }\n\n[[mcp]]\nname = \"second\"\n"
	for _, tc := range []struct {
		what  string
		table []string
		key   string
	}{
		{"a new key under the array itself", []string{"mcp"}, "url"},
		{"a new subtable under an element", []string{"mcp", "auth"}, "token"},
		{"a construct no element declares", []string{"mcp", "extra", "deep"}, "x"},
	} {
		d := load(t, src)
		err := d.SetKey(tc.table, tc.key, String("v"))
		if err == nil {
			t.Fatalf("%s: accepted", tc.what)
		}
		if !strings.Contains(err.Error(), "array of tables [[mcp]]") {
			t.Fatalf("%s: error should name the rule that fired: %v", tc.what, err)
		}
		if !strings.Contains(err.Error(), encodeKeyPath(fullPath(tc.table, tc.key))) {
			t.Fatalf("%s: error should name the offending path: %v", tc.what, err)
		}
		if string(d.Bytes()) != src {
			t.Fatalf("%s: the document must be left as it was:\n%s", tc.what, d.Bytes())
		}
	}

	// The same paths, when they name something that already exists, still
	// resolve -- first match, edited where it lives.
	d := load(t, src)
	if err := d.SetKey([]string{"mcp"}, "name", String("renamed")); err != nil {
		t.Fatalf("editing an existing key: %v", err)
	}
	if err := d.SetKey([]string{"mcp", "headers"}, "A", String("2")); err != nil {
		t.Fatalf("editing an existing inline member: %v", err)
	}
	blocks := mustParse(t, d)["mcp"].([]any)
	if blocks[0].(map[string]any)["name"] != "renamed" {
		t.Fatalf("first match should be the one edited: %v", blocks)
	}
	if blocks[1].(map[string]any)["name"] != "second" {
		t.Fatalf("second element damaged: %v", blocks)
	}
}

// A brand-new member joins the construct rather than opening a rival
// definition of the same table.
func TestSetKeyAddsNewMemberToInlineTable(t *testing.T) {
	d := load(t, "defaults = { skip_questions = true }\n")
	if err := d.SetKey([]string{"defaults"}, "shared_auth", InlineStringMap(map[string]string{"claude": "peer"})); err != nil {
		t.Fatal(err)
	}
	def := mustParse(t, d)["defaults"].(map[string]any)
	if def["skip_questions"] != true {
		t.Fatalf("existing member lost: %v", def)
	}
	if def["shared_auth"].(map[string]any)["claude"] != "peer" {
		t.Fatalf("new member = %v", def)
	}
}

// A document that was ALREADY semantically broken stays editable: the strict
// re-read exists to catch what an edit breaks, and refusing here would strand
// the file at exactly the moment the user is trying to repair it.
func TestBrokenDocumentStaysEditable(t *testing.T) {
	// Two definitions of d -- the expression parser accepts it, the strict
	// decoder does not.
	src := "d = { x = 1 }\n[d]\ny = 2\n"
	if err := toml.Unmarshal([]byte(src), &map[string]any{}); err == nil {
		t.Fatal("fixture is not actually broken")
	}
	d := load(t, src)
	if err := d.SetKey(nil, "base", String("node:22")); err != nil {
		t.Fatalf("an already-broken document must stay editable: %v", err)
	}
	if !strings.Contains(string(d.Bytes()), `base = "node:22"`) {
		t.Fatalf("edit not applied:\n%s", d.Bytes())
	}
}

// An EMPTY block is one line long: removing it must not take the header that
// follows, and a key added to it must not land in the next table (fuzz).
func TestEmptyBlockDoesNotReachIntoTheNextLine(t *testing.T) {
	src := "[env]\n[defaults]\nskip_questions = true\n"
	d := load(t, src)
	if err := d.RemoveTable([]string{"env"}); err != nil {
		t.Fatal(err)
	}
	m := mustParse(t, d)
	def, ok := m["defaults"].(map[string]any)
	if !ok || def["skip_questions"] != true {
		t.Fatalf("the following table was damaged: %v\n%s", m, d.Bytes())
	}

	d2 := load(t, src)
	if err := d2.SetKey([]string{"env"}, "FOO", String("bar")); err != nil {
		t.Fatal(err)
	}
	m = mustParse(t, d2)
	if m["env"].(map[string]any)["FOO"] != "bar" {
		t.Fatalf("key did not land in the empty table it names: %v\n%s", m, d2.Bytes())
	}
	if _, stray := m["defaults"].(map[string]any)["FOO"]; stray {
		t.Fatalf("key landed in the next table: %v\n%s", m, d2.Bytes())
	}
}

// A quoted key is spelled in TOML's escape language, not Go's (fuzz).
func TestQuotedKeyUsesTOMLEscapes(t *testing.T) {
	d := load(t, "")
	key := "bell\aand\vvertical"
	if err := d.SetKey([]string{"env"}, key, String("x")); err != nil {
		t.Fatal(err)
	}
	m := mustParse(t, d)
	if got := m["env"].(map[string]any)[key]; got != "x" {
		t.Fatalf("key did not round-trip: %v\n%s", m, d.Bytes())
	}
}

// A key TOML cannot spell is refused, not approximated: substituting U+FFFD
// would write a key the caller never named (fuzz).
func TestUnspellableKeyRefused(t *testing.T) {
	src := "base = \"node:22\"\n"
	d := load(t, src)
	err := d.SetKey(nil, "bad\xc2", String("x"))
	if err == nil {
		t.Fatal("an invalid-UTF-8 key was accepted")
	}
	if !strings.Contains(err.Error(), "not valid UTF-8") {
		t.Fatalf("error should name the rule that fired: %v", err)
	}
	if string(d.Bytes()) != src {
		t.Fatalf("the document must be left as it was:\n%s", d.Bytes())
	}

	// Every entry point that ENCODES a caller's key path answers the same
	// way -- one guarded call and one silent substitution is no guard.
	if err := d.AppendArrayTable("bad\xc2", KV("name", String("x"))); err == nil {
		t.Fatal("AppendArrayTable accepted an unspellable name")
	}
	if _, err := d.ReplaceArrayTable("bad\xc2", "name", "x", ""); err == nil {
		t.Fatal("ReplaceArrayTable accepted an unspellable name")
	}
	if string(d.Bytes()) != src {
		t.Fatalf("the document must be left as it was:\n%s", d.Bytes())
	}
}

// The engine's own backstop: an edit that yields syntactically valid but
// semantically illegal TOML (a key defined twice -- what the expression
// parser happily accepts) must not be handed back. Driven at the splice
// seam, since no public operation reaches this state any more.
func TestSpliceRefusesSemanticallyInvalidResult(t *testing.T) {
	src := "d = { x = 1 }\n"
	d := load(t, src)
	err := d.splice(span{len(src), len(src)}, []byte("[d]\ny = 2\n"))
	if err == nil {
		t.Fatal("splice accepted a document that defines d twice")
	}
	if !strings.Contains(err.Error(), "internal bug") {
		t.Fatalf("error should name the rule that fired: %v", err)
	}
	if string(d.Bytes()) != src {
		t.Fatalf("the document must be left as it was:\n%s", d.Bytes())
	}
}

// The SECOND edit of an inline-table value must work: the parser reports an
// InlineTable's Raw as just the opening brace, so trusting the length made
// the re-edit splice one byte into invalid TOML. byre's own house shapes are
// inline tables, so every change-after-first-write hit this.
func TestInlineTableValueSecondEdit(t *testing.T) {
	d := load(t, "shared_auth = { \"claude\" = \"old\" } # keep\nbase = \"node:22\"\n")
	if err := d.SetKey(nil, "shared_auth", `{ "claude" = "new" }`); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if !strings.Contains(out, `shared_auth = { "claude" = "new" } # keep`) {
		t.Fatalf("inline table not replaced in place:\n%s", out)
	}
	if err := d.SetKey(nil, "shared_auth", `{ "grok" = "third" }`); err != nil {
		t.Fatalf("third edit: %v", err)
	}
	m := mustParse(t, d)
	sa := m["shared_auth"].(map[string]any)
	if sa["grok"] != "third" || len(sa) != 1 {
		t.Fatalf("shared_auth = %v", sa)
	}
}

// The unstable parser reports a message and no position, and byre's first-run
// saves parse the user's default.config through this door -- so a refusal
// there said "fix this file" without saying where. Load consults the stable
// decoder as a position oracle: it is built on this same parser, so a
// document rejected here is rejected there too, with coordinates attached.
func TestLoadErrorSaysWhere(t *testing.T) {
	_, err := Load([]byte("a = 1\nb = \"unclosed\nc = 2\n"))
	if err == nil {
		t.Fatal("an unterminated string must not load")
	}
	for _, want := range []string{"line 2", "column"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("load failure must locate itself (%q), got: %v", want, err)
		}
	}
}

// The oracle only ever ADDS a position: where it cannot produce one, the
// parser's own error stands rather than a guessed location.
func TestLoadErrorSurvivesASilentOracle(t *testing.T) {
	// A document both parsers accept must not reach the error path at all.
	if _, err := Load([]byte("a = 1\n[t]\nb = 2\n")); err != nil {
		t.Fatalf("a valid document must load: %v", err)
	}
}
