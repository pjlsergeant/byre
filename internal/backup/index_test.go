package backup

import (
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/credentials"
)

const (
	shaA = "1111111111111111111111111111111111111111111111111111111111111111"
	shaB = "2222222222222222222222222222222222222222222222222222222222222222"
	shaC = "3333333333333333333333333333333333333333333333333333333333333333"
)

// fullIndex is every field of format 1 populated, so the golden below pins
// the whole schema and not a subset of it.
func fullIndex() Index {
	return Index{
		Format:         Format,
		ByreVersion:    "v1.12.0",
		MinByreVersion: MinByreVersion,
		Folder:         "demo",
		Engine:         "docker",
		Config:         ConfigRow{Bytes: 412, SHA256: shaA, Credentials: CredRows},
		Volumes: []Volume{
			{Name: ".claude", Bytes: 2048, SHA256: shaB, Entries: 7},
			{Name: ".grok", Bytes: 1024, SHA256: shaC, Entries: 3},
		},
		References: References{
			Layers:       []string{"work", "base"},
			Template:     "go",
			Agent:        "claude",
			Skills:       []string{"codex", "pjlsergeant/devlog"},
			Mounts:       []string{"/host/shared"},
			Context:      []string{"docs/CONTEXT.md"},
			ClaudeSkills: []string{"/host/skills/review"},
			Seeds:        []string{"/host/seed"},
			Files:        []string{"secrets/.netrc"},
			Engine:       "docker",
			Base:         "debian:bookworm",
			WorktreeBase: "/host/worktrees",
		},
	}
}

// The index is a CONTRACT: another byre reads these bytes. Pinned byte-exact,
// every key present.
const fullIndexGolden = `format = 1
byre_version = "v1.12.0"
min_byre_version = "v1.12.0"
folder = "demo"
engine = "docker"

[config]
bytes = 412
sha256 = "1111111111111111111111111111111111111111111111111111111111111111"
credentials = "rows"

[[volumes]]
name = ".claude"
bytes = 2048
sha256 = "2222222222222222222222222222222222222222222222222222222222222222"
entries = 7

[[volumes]]
name = ".grok"
bytes = 1024
sha256 = "3333333333333333333333333333333333333333333333333333333333333333"
entries = 3

[references]
layers = ["work", "base"]
template = "go"
agent = "claude"
skills = ["codex", "pjlsergeant/devlog"]
mounts = ["/host/shared"]
context = ["docs/CONTEXT.md"]
claude_skills = ["/host/skills/review"]
seeds = ["/host/seed"]
files = ["secrets/.netrc"]
engine = "docker"
base = "debian:bookworm"
worktree_base = "/host/worktrees"
`

// A project with nothing to name still writes every key: a reader never
// guesses, and an empty list is spelled [], an unset string "".
const emptyIndexGolden = `format = 1
byre_version = "(devel)"
min_byre_version = "v1.12.0"
folder = "proj"
engine = "podman"

[config]
bytes = 0
sha256 = "1111111111111111111111111111111111111111111111111111111111111111"
credentials = "none"

[references]
layers = []
template = ""
agent = ""
skills = []
mounts = []
context = []
claude_skills = []
seeds = []
files = []
engine = ""
base = "debian:bookworm"
worktree_base = ""
`

func TestIndexGolden(t *testing.T) {
	if got := string(fullIndex().Render()); got != fullIndexGolden {
		t.Fatalf("Render() mismatch\n--- got ---\n%s\n--- want ---\n%s", got, fullIndexGolden)
	}
	empty := Index{
		Format:         Format,
		ByreVersion:    "(devel)",
		MinByreVersion: MinByreVersion,
		Folder:         "proj",
		Engine:         "podman",
		Config:         ConfigRow{SHA256: shaA, Credentials: CredNone},
		References:     References{Base: "debian:bookworm"},
	}
	if got := string(empty.Render()); got != emptyIndexGolden {
		t.Fatalf("Render() of an empty index mismatch\n--- got ---\n%s\n--- want ---\n%s", got, emptyIndexGolden)
	}
}

func TestIndexRoundTrips(t *testing.T) {
	want := fullIndex()
	got, err := ParseIndex(want.Render())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip\n got %#v\nwant %#v", got, want)
	}
	// An empty list comes back as an empty list, not as a missing key the
	// reader would have to guess about.
	raw := []byte(emptyIndexGolden)
	ix, err := ParseIndex(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ix.References.Layers == nil || len(ix.References.Layers) != 0 {
		t.Fatalf("layers = %#v, want an empty list", ix.References.Layers)
	}
	if string(ix.Render()) != emptyIndexGolden {
		t.Fatal("an empty index does not re-render to its own bytes")
	}
}

func TestParseIndexRefusesANewerFormat(t *testing.T) {
	raw := []byte(`format = 2
min_byre_version = "v2.4.0"
folder = "demo"
whatever = { shape = "the future's" }
`)
	_, err := ParseIndex(raw)
	if !errors.Is(err, ErrNewerFormat) {
		t.Fatalf("err = %v, want ErrNewerFormat", err)
	}
	wantRule(t, err, "v2.4.0", "format 2")
}

func TestParseIndexNamesAMissingMinimumVersion(t *testing.T) {
	_, err := ParseIndex([]byte("format = 7\n"))
	if !errors.Is(err, ErrNewerFormat) {
		t.Fatalf("err = %v, want ErrNewerFormat", err)
	}
	wantRule(t, err, "(not stated)", "format 7")
}

func TestParseIndexRefusesAnUnknownKeyAtFormat1(t *testing.T) {
	raw := append([]byte(fullIndexGolden), []byte("\nsurprise = true\n")...)
	_, err := ParseIndex(raw)
	wantRule(t, err, "unknown key", "surprise")
}

func TestCheckIndexRefusals(t *testing.T) {
	longName := strings.Repeat("v", 250)
	cases := []struct {
		name      string
		mutate    func(*Index)
		prefix    string
		fragments []string
	}{
		{"bad credential state", func(ix *Index) { ix.Config.Credentials = "maybe" }, "byre-p-", []string{`"maybe"`, "rows-no-identity"}},
		{"dot name", func(ix *Index) { ix.Volumes[0].Name = "." }, "byre-p-", []string{`"."`, "is not a name"}},
		{"dotdot name", func(ix *Index) { ix.Volumes[0].Name = ".." }, "byre-p-", []string{`".."`, "is not a name"}},
		{"name outside the grammar", func(ix *Index) { ix.Volumes[0].Name = "has/slash" }, "byre-p-", []string{`"has/slash"`, "not allowed in a volume name"}},
		{"duplicate name", func(ix *Index) { ix.Volumes[1].Name = ix.Volumes[0].Name }, "byre-p-", []string{`".claude"`, "listed twice"}},
		{"over-long once joined", func(ix *Index) { ix.Volumes[0].Name = longName }, "byre-abcdef123456-", []string{longName, "too long once joined", "limit 255"}},
		{"too many entries", func(ix *Index) { ix.Volumes[0].Entries = MaxEntries + 1 }, "byre-p-", []string{`".claude"`, "1000001 entries", "limit 1000000"}},
		{"volume sha is not hex", func(ix *Index) { ix.Volumes[0].SHA256 = "zz" }, "byre-p-", []string{`".claude"`, `"zz"`, "not a hex digest"}},
		{"config sha is not hex", func(ix *Index) { ix.Config.SHA256 = strings.Repeat("g", 64) }, "byre-p-", []string{"config sha256", "not a hex digest"}},
		{"config over the bound", func(ix *Index) { ix.Config.Bytes = 1 << 21 }, "byre-p-", []string{"2097152 bytes", "limit 1048576"}},
		{"negative volume bytes", func(ix *Index) { ix.Volumes[0].Bytes = -1 }, "byre-p-", []string{`".claude"`, "negative byte count"}},
		{"wrong format", func(ix *Index) { ix.Format = 0 }, "byre-p-", []string{"format 0 is not 1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ix := fullIndex()
			c.mutate(&ix)
			wantRule(t, CheckIndex(ix, c.prefix), c.fragments...)
		})
	}
}

func TestCheckIndexAcceptsAWellFormedIndex(t *testing.T) {
	if err := CheckIndex(fullIndex(), "byre-demo-abc123-"); err != nil {
		t.Fatal(err)
	}
}

// The credential state is read off the config bytes and nothing else. All
// four states, including the two the design calls out as legal-but-odd.
func TestCredentialStateReadsTheFile(t *testing.T) {
	block := credBlock(t)

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"none", "base = \"debian:bookworm\"\n", CredNone},
		{"identity only", "base = \"debian:bookworm\"\n" + block, CredIdentityOnly},
		{
			"rows with identity",
			"[env_from_host]\nSTRIPE = \"encrypted:AAAA\"\n\n" + block,
			CredRows,
		},
		{
			"rows without identity",
			"[env_from_host]\nSTRIPE = \"encrypted:AAAA\"\nTOKEN = \"encrypted-file:AAAA\"\n",
			CredRowsNoIdentity,
		},
		{
			// An [env] literal shadows the row at LAUNCH; it does not change
			// what the file holds, and the state describes the file.
			"a row shadowed by an env literal still counts",
			"[env]\nSTRIPE = \"plain\"\n\n[env_from_host]\nSTRIPE = \"encrypted:AAAA\"\n\n" + block,
			CredRows,
		},
		{
			"a non-credential passthrough is not a row",
			"[env_from_host]\nTZ = \"host:TZ\"\n",
			CredNone,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CredentialState([]byte(c.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("CredentialState = %q, want %q", got, c.want)
			}
		})
	}
}

func TestStripCredentialsRemovesTheTableSpelling(t *testing.T) {
	src := `# the project's own comment
base = "debian:bookworm"

[env_from_host]
# a host passthrough, not a credential
TZ = "host:TZ"
STRIPE = "encrypted:AAAA"
TOKEN = "encrypted-file:BBBB"

[env]
KEEP = "me"
` + credBlock(t)

	want := `# the project's own comment
base = "debian:bookworm"

[env_from_host]
# a host passthrough, not a credential
TZ = "host:TZ"

[env]
KEEP = "me"

`
	got, err := StripCredentials([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("StripCredentials\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if state, err := CredentialState(got); err != nil || state != CredNone {
		t.Fatalf("state after stripping = %q (%v), want %q", state, err, CredNone)
	}
	// The source bytes are untouched: backup rewrites the copy, never the
	// project's own file.
	if !strings.Contains(src, "encrypted:AAAA") {
		t.Fatal("the source document was mutated")
	}
}

func TestStripCredentialsRemovesTheDottedSpelling(t *testing.T) {
	src := `base = "debian:bookworm"
env_from_host.TZ = "host:TZ"
env_from_host.STRIPE = "encrypted:AAAA"
`
	want := `base = "debian:bookworm"
env_from_host.TZ = "host:TZ"
`
	got, err := StripCredentials([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("StripCredentials\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestStripCredentialsRemovesTheInlineSpelling(t *testing.T) {
	src := `base = "debian:bookworm"
env_from_host = { TZ = "host:TZ", STRIPE = "encrypted:AAAA" }
`
	got, err := StripCredentials([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "encrypted:") {
		t.Fatalf("the inline credential row survived:\n%s", got)
	}
	state, err := CredentialState(got)
	if err != nil {
		t.Fatal(err)
	}
	if state != CredNone {
		t.Fatalf("state = %q, want %q", state, CredNone)
	}
	// The surviving passthrough is still there, in whatever shape tomldoc's
	// house rules give the rewritten construct.
	if !strings.Contains(string(got), `TZ = "host:TZ"`) {
		t.Fatalf("the non-credential row was dropped with the credential:\n%s", got)
	}
	if !strings.Contains(string(got), `base = "debian:bookworm"`) {
		t.Fatalf("an unrelated key was dropped:\n%s", got)
	}
}

func TestStripCredentialsRemovesAZeroRowIdentityBlock(t *testing.T) {
	src := "base = \"debian:bookworm\"\n" + credBlock(t)
	if state, err := CredentialState([]byte(src)); err != nil || state != CredIdentityOnly {
		t.Fatalf("state = %q (%v), want %q", state, err, CredIdentityOnly)
	}
	got, err := StripCredentials([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	// The blank line that separated the block stays: tomldoc takes the
	// block and the comments glued to it, never the whitespace around it.
	if string(got) != "base = \"debian:bookworm\"\n\n" {
		t.Fatalf("StripCredentials left %q", got)
	}
}

// credBlock is a real file-local [credentials] identity: ParseCredentialsBlock
// validates the base64 and the age recipient, so a hand-typed fixture would
// not read as a block at all.
func credBlock(t *testing.T) string {
	t.Helper()
	credentials.SetWorkFactorForTesting(10)
	wrapped, recipient, err := credentials.NewIdentity("pw")
	if err != nil {
		t.Fatal(err)
	}
	return "\n[credentials]\nidentity = \"" + base64.StdEncoding.EncodeToString(wrapped) + "\"\nrecipient = \"" + recipient + "\"\n"
}
