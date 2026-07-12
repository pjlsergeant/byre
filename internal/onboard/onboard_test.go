package onboard

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// fav is the common case: the stored favourite is valid, so it is also the
// effective (pre-selected) one.
func fav(v string) Favourite { return Favourite{Stored: v, Effective: v} }

func TestPickAcceptsDefaultsOnEmpty(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("\n\n\n")), []string{"go", "node"}, []string{"claude", "codex"}, fav("go"), fav("claude"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "go" || c.Agent != "claude" || c.SaveDefault {
		t.Fatalf("empty input should accept favourites, got %+v", c)
	}
	// Choosing what's already the default must not offer to save it as such.
	if strings.Contains(out.String(), "Save these") {
		t.Fatalf("save-as-default offered for a choice that IS the default:\n%s", out.String())
	}
}

// Retyping the favourites (rather than accepting them with Enter) is still the
// same choice — no save offer.
func TestPickRetypedDefaultsSkipSaveOffer(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("go\nclaude\n")), []string{"go", "node"}, []string{"claude", "codex"}, fav("go"), fav("claude"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "go" || c.Agent != "claude" || c.SaveDefault {
		t.Fatalf("retyped favourites wrong: %+v", c)
	}
	if strings.Contains(out.String(), "Save these") {
		t.Fatalf("save-as-default offered for retyped favourites:\n%s", out.String())
	}
}

// One axis differing is enough to make the offer (the save updates both
// scalars; the matching one is idempotent).
func TestPickOneAxisDifferingStillOffers(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("\ncodex\ny\n")), []string{"go", "node"}, []string{"claude", "codex"}, fav("go"), fav("claude"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "go" || c.Agent != "codex" || !c.SaveDefault {
		t.Fatalf("one-axis change should offer and save: %+v", c)
	}
	if !strings.Contains(out.String(), "Save these") {
		t.Fatalf("save offer missing for a differing choice:\n%s", out.String())
	}
}

// A STALE stored favourite (Effective dropped to "") must still get the save
// offer even when the user accepts the presented defaults: what's stored
// differs from the choice, so saving is NOT a no-op — and skipping it would
// leave the stale value to silently resurrect if its name turns valid again.
func TestPickStaleFavouriteStillOffers(t *testing.T) {
	var out bytes.Buffer
	// Stored template "old" no longer exists; the picker presents none.
	// The user accepts none + the existing agent, and answers y.
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("\n\ny\n")), []string{"go", "node"}, []string{"claude", "codex"},
		Favourite{Stored: "old", Effective: ""}, fav("claude"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Save these") {
		t.Fatalf("save offer missing with a stale stored favourite:\n%s", out.String())
	}
	if c.Template != "" || c.Agent != "claude" || !c.SaveDefault {
		t.Fatalf("stale-favourite choice wrong: %+v", c)
	}
}

func TestPickChoosesAndSaves(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("node\ncodex\ny\n")), []string{"go", "node"}, []string{"claude", "codex"}, fav("go"), fav("claude"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "node" || c.Agent != "codex" || !c.SaveDefault {
		t.Fatalf("explicit choices wrong: %+v", c)
	}
}

func TestAskAxisPromptsOneAxis(t *testing.T) {
	var out bytes.Buffer
	// Empty input accepts the favourite.
	v, err := AskAxis(&out, bufio.NewReader(strings.NewReader("\n")), "Template", []string{"go", "node"}, "node")
	if err != nil {
		t.Fatal(err)
	}
	if v != "node" {
		t.Fatalf("empty should accept favourite, got %q", v)
	}
	// Explicit "none" returns "".
	v, err = AskAxis(&out, bufio.NewReader(strings.NewReader("none\n")), "Template", []string{"go", "node"}, "node")
	if err != nil {
		t.Fatal(err)
	}
	if v != "" {
		t.Fatalf("none should be empty, got %q", v)
	}
}

func TestPickReprompsOnInvalid(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("rust\ngo\nclaude\n\n")), []string{"go"}, []string{"claude"}, fav("go"), fav("claude"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "go" {
		t.Fatalf("should reprompt past invalid, got %+v", c)
	}
	if !strings.Contains(out.String(), "not one of") {
		t.Errorf("expected an invalid-choice message: %s", out.String())
	}
}

func TestPickNone(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("none\nnone\n\n")), []string{"go"}, []string{"claude"}, fav(""), fav(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "" || c.Agent != "" {
		t.Fatalf("none should map to empty, got %+v", c)
	}
	// With no stored favourites, none/none IS the stored state — saving would
	// be a no-op, so the offer must not appear.
	if c.SaveDefault || strings.Contains(out.String(), "Save these") {
		t.Fatalf("save offer must not appear for none/none with no favourites:\n%s", out.String())
	}
}

func TestWriteProjectConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store", "byre.config") // parent created by WriteProjectConfig
	if err := WriteProjectConfig(path, "go", "claude", nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	if !strings.Contains(s, `template = "go"`) || !strings.Contains(s, `agent = "claude"`) {
		t.Fatalf("byre.config content: %s", s)
	}
	if strings.Contains(s, "skills") {
		t.Fatalf("no opted skills — no skills key: %s", s)
	}
	// Refuses to overwrite.
	if err := WriteProjectConfig(path, "node", "codex", nil); err == nil {
		t.Fatal("should refuse to overwrite an existing byre.config")
	}
}

// A yes to the shared-auth offer (ADR 0025) rides into THIS box's config as a
// plain skills entry — the same representation a hand-enabled skill uses.
func TestWriteProjectConfigWritesOptedSkills(t *testing.T) {
	path := filepath.Join(t.TempDir(), "byre.config")
	if err := WriteProjectConfig(path, "go", "claude", []string{"claude-shared-auth"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `skills = ["claude-shared-auth"]`) {
		t.Fatalf("byre.config content: %s", b)
	}
	cfg, err := config.ParseFile(path)
	if err != nil {
		t.Fatalf("written config must parse: %v", err)
	}
	if len(cfg.Skills) != 1 || cfg.Skills[0] != "claude-shared-auth" {
		t.Fatalf("skills = %v", cfg.Skills)
	}
}

func TestWriteProjectConfigOmitsNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "byre.config")
	if err := WriteProjectConfig(path, "", "claude", nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "template") {
		t.Errorf("empty template should be omitted: %s", b)
	}
}

func TestSaveDefaultPreservesOtherKeys(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "default.config"), []byte("base = \"debian:bookworm\"\nagent = \"claude\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveDefault(home, "go", "codex"); err != nil {
		t.Fatal(err)
	}
	tmpl, agent := Favourites(home)
	if tmpl != "go" || agent != "codex" {
		t.Fatalf("favourites not updated: %q %q", tmpl, agent)
	}
	b, _ := os.ReadFile(filepath.Join(home, "default.config"))
	if !strings.Contains(string(b), `base = "debian:bookworm"`) {
		t.Errorf("should preserve base: %s", b)
	}
}

func TestScalarEditingIsTopLevelOnly(t *testing.T) {
	// A nested key with the same name in a [section] must not be edited.
	content := "agent = \"claude\"\n\n[env]\nagent = \"nested-should-be-ignored\"\n"
	out := setScalar(content, "agent", "codex")
	if !strings.Contains(out, `agent = "codex"`) || strings.Contains(out, `agent = "claude"`) {
		t.Fatalf("top-level agent not updated:\n%s", out)
	}
	if !strings.Contains(out, `agent = "nested-should-be-ignored"`) {
		t.Fatalf("nested key was corrupted:\n%s", out)
	}
}

func TestFavouritesReadsLiteralStrings(t *testing.T) {
	// TOML literal (single-quoted) strings are valid; the old regex reader
	// silently returned "" for them. A real parse must not.
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "default.config"), []byte("template = 'go'\nagent = 'claude'\n"), 0o644)
	tmpl, agent := Favourites(home)
	if tmpl != "go" || agent != "claude" {
		t.Fatalf("literal-string favourites misread: %q %q", tmpl, agent)
	}
}

func TestSaveDefaultCreatesWhenAbsent(t *testing.T) {
	home := t.TempDir()
	if err := SaveDefault(home, "node", "claude"); err != nil {
		t.Fatal(err)
	}
	tmpl, agent := Favourites(home)
	if tmpl != "node" || agent != "claude" {
		t.Fatalf("favourites = %q %q", tmpl, agent)
	}
}

func TestSaveDefaultRemovesOnEmpty(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "default.config"), []byte("template = \"go\"\nagent = \"claude\"\n"), 0o644)
	if err := SaveDefault(home, "", "claude"); err != nil { // none template
		t.Fatal(err)
	}
	if tmpl, _ := Favourites(home); tmpl != "" {
		t.Fatalf("empty template should be removed, got %q", tmpl)
	}
}

func TestOfferSharedAuth(t *testing.T) {
	var out bytes.Buffer
	yes, err := OfferSharedAuth(&out, bufio.NewReader(strings.NewReader("y\n")), "claude", "claude-shared-auth", false)
	if err != nil || !yes {
		t.Fatalf("yes = %v, err = %v", yes, err)
	}
	// The wording must carry the real scope of the write: this box, opting
	// into an existing shared mechanism — never "all projects".
	if !strings.Contains(out.String(), "Opt this box into claude shared credentials? [y/N/i]") {
		t.Fatalf("offer must be the per-box question, defaulting No:\n%s", out.String())
	}
	// No preference: an empty answer declines.
	yes, err = OfferSharedAuth(&out, bufio.NewReader(strings.NewReader("\n")), "claude", "claude-shared-auth", false)
	if err != nil || yes {
		t.Fatalf("empty answer must decline, got yes = %v, err = %v", yes, err)
	}
}

// "i" prints exactly what each answer writes — scopes, the companion's name,
// the save question's prefill-only effect — then re-asks; it never consumes
// the answer itself.
func TestOfferSharedAuthInfo(t *testing.T) {
	var out bytes.Buffer
	yes, err := OfferSharedAuth(&out, bufio.NewReader(strings.NewReader("i\ny\n")), "claude", "claude-shared-auth", false)
	if err != nil || !yes {
		t.Fatalf("after info the real answer must still be read: yes = %v, err = %v", yes, err)
	}
	got := out.String()
	for _, want := range []string{
		"this project's byre.config", // y's write and scope
		`"claude-shared-auth"`,       // the mechanism, named where detail belongs
		"nothing is recorded",        // n's write
		"never enables anything",     // save-default's prefill-only effect
	} {
		if !strings.Contains(got, want) {
			t.Errorf("info must state %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "Opt this box") != 2 {
		t.Fatalf("info must re-ask the question:\n%s", got)
	}
}

// A saved yes-preference prefills the offer like a favourite: Enter accepts
// it, an explicit n overrides it, and unrecognized input never lands on the
// granting side whatever the default.
func TestOfferSharedAuthPrefilledYes(t *testing.T) {
	var out bytes.Buffer
	yes, err := OfferSharedAuth(&out, bufio.NewReader(strings.NewReader("\n")), "claude", "claude-shared-auth", true)
	if err != nil || !yes {
		t.Fatalf("Enter must accept the saved yes: yes = %v, err = %v", yes, err)
	}
	if !strings.Contains(out.String(), "[Y/n/i]") {
		t.Fatalf("a saved yes must show as the prefilled default:\n%s", out.String())
	}
	yes, err = OfferSharedAuth(&out, bufio.NewReader(strings.NewReader("n\n")), "claude", "claude-shared-auth", true)
	if err != nil || yes {
		t.Fatalf("explicit n must override the preference: yes = %v, err = %v", yes, err)
	}
	yes, err = OfferSharedAuth(&out, bufio.NewReader(strings.NewReader("wat\n")), "claude", "claude-shared-auth", true)
	if err != nil || yes {
		t.Fatalf("garbage must never grant, even under a yes default: yes = %v, err = %v", yes, err)
	}
}

// The prompting functions take one caller-supplied *bufio.Reader precisely so
// answers buffered ahead by an earlier question stay readable by a later one.
func TestPromptsShareABufferedReader(t *testing.T) {
	var out bytes.Buffer
	in := bufio.NewReader(strings.NewReader("node\ncodex\nn\ny\n"))
	c, err := Pick(&out, in, []string{"go", "node"}, []string{"claude", "codex"}, fav("go"), fav("claude"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "node" || c.Agent != "codex" || c.SaveDefault {
		t.Fatalf("choice = %+v", c)
	}
	yes, err := OfferSharedAuth(&out, in, "codex", "codex-shared-auth", false)
	if err != nil || !yes {
		t.Fatalf("the shared-auth answer was buffered by Pick's reader and must still be readable: yes = %v, err = %v", yes, err)
	}
}

// The shared-auth offer sits between the agent question and the save-default
// wrap-up (agent questions stay together; answers precede writes), and is
// skipped when companionFor names no companion.
func TestPickOffersSharedAuthBeforeSaveDefault(t *testing.T) {
	var out bytes.Buffer
	companions := func(agent string) (string, bool) {
		if agent == "codex" {
			return "codex-shared-auth", false
		}
		return "", false
	}
	// Template none, agent codex, shared auth y, save-default n.
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("\ncodex\ny\nn\n")), []string{"go"}, []string{"claude", "codex"}, fav(""), fav(""), companions)
	if err != nil {
		t.Fatal(err)
	}
	if c.SharedAuthCompanion != "codex-shared-auth" || !c.SharedAuth || c.SaveDefault {
		t.Fatalf("choice = %+v", c)
	}
	if offer, save := strings.Index(out.String(), "Opt this box into codex"), strings.Index(out.String(), "Save these"); offer < 0 || save < 0 || offer > save {
		t.Fatalf("the offer must precede the save-default question:\n%s", out.String())
	}
	// An agent without a companion gets no offer.
	out.Reset()
	c, err = Pick(&out, bufio.NewReader(strings.NewReader("\nclaude\nn\n")), []string{"go"}, []string{"claude", "codex"}, fav(""), fav(""), companions)
	if err != nil {
		t.Fatal(err)
	}
	if c.SharedAuthCompanion != "" || c.SharedAuth || strings.Contains(out.String(), "Opt this box") {
		t.Fatalf("no companion — no offer: %+v\n%s", c, out.String())
	}
}

// The save question follows one rule for every axis of "these": ask exactly
// when saving would change stored state. A shared-auth answer differing from
// its saved preference is news even when template/agent match the favourites;
// an answer matching the preference is not.
func TestPickSaveTriggerFollowsSharedAuthNews(t *testing.T) {
	companionsWithPref := func(pref bool) func(string) (string, bool) {
		return func(agent string) (string, bool) {
			if agent == "codex" {
				return "codex-shared-auth", pref
			}
			return "", false
		}
	}

	// No stored preference, answer y: news — save question appears.
	var out bytes.Buffer
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("\n\ny\ny\n")), []string{"go"}, []string{"claude", "codex"}, fav("go"), fav("codex"), companionsWithPref(false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Save these") || !c.SaveDefault || !c.SharedAuth {
		t.Fatalf("an answer differing from the stored preference is news: %+v\n%s", c, out.String())
	}

	// Stored yes-preference, Enter accepts it: everything matches stored
	// state — no save question, and the input carries no answer for one.
	out.Reset()
	c, err = Pick(&out, bufio.NewReader(strings.NewReader("\n\n\n")), []string{"go"}, []string{"claude", "codex"}, fav("go"), fav("codex"), companionsWithPref(true))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Save these") || c.SaveDefault || !c.SharedAuth {
		t.Fatalf("accepting the stored preference is not news: %+v\n%s", c, out.String())
	}

	// Stored yes-preference, explicit n: news again.
	out.Reset()
	c, err = Pick(&out, bufio.NewReader(strings.NewReader("\n\nn\nn\n")), []string{"go"}, []string{"claude", "codex"}, fav("go"), fav("codex"), companionsWithPref(true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Save these") || c.SharedAuth {
		t.Fatalf("overriding the stored preference is news: %+v\n%s", c, out.String())
	}
}
