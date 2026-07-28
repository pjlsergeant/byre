package onboard

import (
	"bufio"
	"bytes"
	"io"
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
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("\n\n\n")), Options("go", "node"), Options("claude", "codex"), fav("go"), fav("claude"), nil)
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
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("go\nclaude\n")), Options("go", "node"), Options("claude", "codex"), fav("go"), fav("claude"), nil)
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
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("\ncodex\ny\n")), Options("go", "node"), Options("claude", "codex"), fav("go"), fav("claude"), nil)
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
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("\n\ny\n")), Options("go", "node"), Options("claude", "codex"),
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
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("node\ncodex\ny\n")), Options("go", "node"), Options("claude", "codex"), fav("go"), fav("claude"), nil)
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
	v, err := AskAxis(&out, bufio.NewReader(strings.NewReader("\n")), "Template", Options("go", "node"), "node")
	if err != nil {
		t.Fatal(err)
	}
	if v != "node" {
		t.Fatalf("empty should accept favourite, got %q", v)
	}
	// Explicit "none" returns "".
	v, err = AskAxis(&out, bufio.NewReader(strings.NewReader("none\n")), "Template", Options("go", "node"), "node")
	if err != nil {
		t.Fatal(err)
	}
	if v != "" {
		t.Fatalf("none should be empty, got %q", v)
	}
}

func TestPickReprompsOnInvalid(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("rust\ngo\nclaude\n\n")), Options("go"), Options("claude"), fav("go"), fav("claude"), nil)
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

// A broken package is LISTED with its reason and cannot be chosen. Before
// this the picker took a bare name list, so a broken agent was simply absent
// from first-run -- the user's own agent missing, with nothing said.
func TestPickShowsDisabledRowsAndRefusesThem(t *testing.T) {
	var out bytes.Buffer
	agents := append(Options("claude"),
		Option{Name: "brokenmount", Label: "INVALID", Disabled: `mount target "relative": must be an absolute path`},
		Option{Name: "badposture", Disabled: `network_posture "Deny-Default": must match`},
	)
	// Try both broken rows, then accept the working one.
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("\nbrokenmount\nbadposture\nclaude\n\n")),
		Options("go"), agents, fav("go"), fav(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Agent != "claude" {
		t.Fatalf("a working pick must still land, got %+v", c)
	}
	s := out.String()
	// Listed with the reason, before the prompt.
	for _, want := range []string{"brokenmount", "mount target", "badposture", "Deny-Default"} {
		if !strings.Contains(s, want) {
			t.Errorf("the broken rows and their reasons must be shown, missing %q:\n%s", want, s)
		}
	}
	// A disabled row is never in the offered set.
	if strings.Contains(s, "Agent — claude brokenmount") || strings.Contains(s, "badposture none") {
		t.Errorf("a disabled row must not be offered as a choice:\n%s", s)
	}
	// Naming one reprompts WITH the reason -- never "unknown", which reads as
	// a typo in a name the user spelled right.
	if n := strings.Count(s, "is unavailable:"); n != 2 {
		t.Errorf("each attempt at a disabled row must repeat its reason (got %d):\n%s", n, s)
	}
	if strings.Contains(s, `"brokenmount" is not one of`) {
		t.Errorf("a broken row must not be reported as unknown:\n%s", s)
	}
}

// The picker is a reporting surface, and every field of a row comes from
// somewhere byre does not write -- a declared package id, a catalog reason
// quoting a mount target out of somebody's skill.toml. None of it may reach
// the terminal as control (P4).
func TestPickRendersHostileRowsAsData(t *testing.T) {
	var out bytes.Buffer
	agents := append(Options("claude"), Option{
		Name:     "ev\x1b[2Jil",
		Label:    "INV\x1b]0;pwned\x07ALID",
		Disabled: "mount target \x1b[31m\"relative\"\x1b[0m: must be an absolute path",
	})
	// Name it once (the reprompt reprints the reason), then take the real one.
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("\nev\x1b[2Jil\nclaude\n\n")),
		Options("go"), agents, fav("go"), fav(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Agent != "claude" {
		t.Fatalf("a working pick must still land, got %+v", c)
	}
	s := out.String()
	if strings.ContainsRune(s, 0x1b) {
		t.Errorf("no escape byte may reach the terminal from a row's fields:\n%q", s)
	}
	// Stripped of control, not of meaning: the reason still reads.
	for _, want := range []string{"mount target", "must be an absolute path", "evil", "INVALID"} {
		if !strings.Contains(s, want) {
			t.Errorf("the row must still say what it says, missing %q:\n%s", want, s)
		}
	}
	// Both print paths are covered: the listing above the prompt, and the
	// reprompt that repeats the reason.
	if !strings.Contains(s, "is unavailable:") {
		t.Errorf("the reprompt path must also have run:\n%s", s)
	}
}

// Selectable is what favourite/flag validation reads, so it must agree with
// what the picker offers: disabled rows are in neither.
func TestSelectableExcludesDisabledRows(t *testing.T) {
	opts := append(Options("claude"), Option{Name: "broken", Disabled: "why"})
	got := Selectable(opts)
	if len(got) != 1 || got[0] != "claude" {
		t.Fatalf("Selectable = %v, want [claude]", got)
	}
}

func TestPickNone(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("none\nnone\n\n")), Options("go"), Options("claude"), fav(""), fav(""), nil)
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
	path := filepath.Join(t.TempDir(), "store", "byre.config")
	// Parent dir is the caller's job (in the product, develop's Bootstrap
	// creates it with the path record before onboarding runs).
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
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
	if err := WriteProjectConfig(path, "node", "codex", nil); err == nil || !strings.Contains(err.Error(), "already exists; not overwriting") {
		t.Fatalf("must refuse to overwrite an existing byre.config by the no-overwrite rule, got: %v", err)
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
	cfg, err := config.ParseFile(path, true)
	if err != nil {
		t.Fatalf("written config must parse: %v", err)
	}
	if len(cfg.Skills) != 1 || cfg.Skills[0] != "claude-shared-auth" {
		t.Fatalf("skills = %v", cfg.Skills)
	}
}

// An explicit "none" answer is stored as the literal sentinel — an omitted
// scalar would mean "inherit" and let a template silently override the
// user's explicit no.
func TestWriteProjectConfigStoresNoneExplicitly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "byre.config")
	if err := WriteProjectConfig(path, "", "claude", nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `template = "none"`) {
		t.Errorf("an explicit none must be stored, not omitted: %s", b)
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
	home := t.TempDir()
	orig := "agent = \"claude\"\n\n[env]\nagent = \"nested-should-be-ignored\"\n"
	if err := os.WriteFile(filepath.Join(home, "default.config"), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveDefault(home, "", "codex"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(home, "default.config"))
	out := string(b)
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
	if err := os.WriteFile(filepath.Join(home, "default.config"), []byte("template = 'go'\nagent = 'claude'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(filepath.Join(home, "default.config"), []byte("template = \"go\"\nagent = \"claude\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveDefault(home, "", "claude"); err != nil { // none template
		t.Fatal(err)
	}
	if tmpl, _ := Favourites(home); tmpl != "" {
		t.Fatalf("empty template should be removed, got %q", tmpl)
	}
}

func TestOfferSharedAuth(t *testing.T) {
	var out bytes.Buffer
	yes, err := offerSharedAuth(&out, bufio.NewReader(strings.NewReader("y\n")), "claude", "claude-shared-auth", false)
	if err != nil || !yes {
		t.Fatalf("yes = %v, err = %v", yes, err)
	}
	// The question is self-disclosing — "machine-wide" is the credential's
	// scope — while the WRITE stays this-box-only (stated in the i-text);
	// the line never claims the answer reaches all projects. Bundled
	// claimants keep the line bare: no provenance parenthetical.
	if !strings.Contains(out.String(), SharedAuthPrompt("claude")+" [y/N, i for info]") {
		t.Fatalf("offer must be the bare machine-wide question, defaulting No:\n%s", out.String())
	}
	if strings.Contains(out.String(), "(via ") {
		t.Fatalf("a bundled/unlabeled claimant must not carry a provenance suffix:\n%s", out.String())
	}
	// No preference: an empty answer declines.
	yes, err = offerSharedAuth(&out, bufio.NewReader(strings.NewReader("\n")), "claude", "claude-shared-auth", false)
	if err != nil || yes {
		t.Fatalf("empty answer must decline, got yes = %v, err = %v", yes, err)
	}
}

// "i" prints exactly what each answer writes — scopes, the companion's name,
// the save question's prefill-only effect — then re-asks; it never consumes
// the answer itself.
func TestOfferSharedAuthInfo(t *testing.T) {
	var out bytes.Buffer
	yes, err := offerSharedAuth(&out, bufio.NewReader(strings.NewReader("i\ny\n")), "claude", "claude-shared-auth", false)
	if err != nil || !yes {
		t.Fatalf("after info the real answer must still be read: yes = %v, err = %v", yes, err)
	}
	got := out.String()
	for _, want := range []string{
		"THIS project's byre.config", // y's write and scope
		`"claude-shared-auth"`,       // the mechanism, named where detail belongs
		"Writes nothing",             // n's write
		"opts any box in by itself",  // save-default's prefill-only effect
	} {
		if !strings.Contains(got, want) {
			t.Errorf("info must state %q:\n%s", want, got)
		}
	}
	if strings.Count(got, SharedAuthPrompt("claude")) != 2 {
		t.Fatalf("info must re-ask the question:\n%s", got)
	}
	if !strings.Contains(got, "machine-wide volume") {
		t.Errorf("info must disclose the machine-wide volume:\n%s", got)
	}
}

// A saved yes-preference prefills the offer like a favourite: Enter accepts
// it, an explicit n overrides it, and unrecognized input never lands on the
// granting side whatever the default.
func TestOfferSharedAuthPrefilledYes(t *testing.T) {
	var out bytes.Buffer
	yes, err := offerSharedAuth(&out, bufio.NewReader(strings.NewReader("\n")), "claude", "claude-shared-auth", true)
	if err != nil || !yes {
		t.Fatalf("Enter must accept the saved yes: yes = %v, err = %v", yes, err)
	}
	if !strings.Contains(out.String(), "[Y/n, i for info]") {
		t.Fatalf("a saved yes must show as the prefilled default:\n%s", out.String())
	}
	yes, err = offerSharedAuth(&out, bufio.NewReader(strings.NewReader("n\n")), "claude", "claude-shared-auth", true)
	if err != nil || yes {
		t.Fatalf("explicit n must override the preference: yes = %v, err = %v", yes, err)
	}
	// Garbage REPROMPTS (it used to read as a silent decline —
	// an `i` typo threw the offer away); the explicit answer after it lands.
	out.Reset()
	yes, err = offerSharedAuth(&out, bufio.NewReader(strings.NewReader("wat\nn\n")), "claude", "claude-shared-auth", true)
	if err != nil || yes {
		t.Fatalf("garbage then n must decline: yes = %v, err = %v", yes, err)
	}
	if !strings.Contains(out.String(), "unrecognized") || strings.Count(out.String(), "[Y/n, i for info]") != 2 {
		t.Fatalf("garbage must reprompt with a hint:\n%s", out.String())
	}
	// Garbage with input exhausted surfaces the read error — terminates, and
	// still never lands on the granting side, whatever the default.
	yes, err = offerSharedAuth(&out, bufio.NewReader(strings.NewReader("wat\n")), "claude", "claude-shared-auth", true)
	if err == nil || yes {
		t.Fatalf("garbage + EOF must error without granting: yes = %v, err = %v", yes, err)
	}
}

// Every y/N prompt shares one behavior: y/n answers, Enter takes the default,
// anything else reprompts (garbage used to read as the default).
func TestAskYesNoDefaultReprompts(t *testing.T) {
	var out bytes.Buffer
	yes, err := askYesNoDefault(&out, bufio.NewReader(strings.NewReader("banana\ny\n")), "Proceed?", false)
	if err != nil || !yes {
		t.Fatalf("garbage then y must land yes: yes = %v, err = %v", yes, err)
	}
	if !strings.Contains(out.String(), "unrecognized") || strings.Count(out.String(), "Proceed? [y/N]:") != 2 {
		t.Fatalf("garbage must reprompt with a hint:\n%s", out.String())
	}
	// Explicit n is recognized (it used to be lumped with garbage).
	if yes, err = askYesNoDefault(&out, bufio.NewReader(strings.NewReader("N\n")), "Proceed?", true); err != nil || yes {
		t.Fatalf("explicit n must override a yes default: yes = %v, err = %v", yes, err)
	}
	// Garbage with input exhausted errors out — never grants, never spins.
	if yes, err = askYesNoDefault(&out, bufio.NewReader(strings.NewReader("banana\n")), "Proceed?", true); err == nil || yes {
		t.Fatalf("garbage + EOF must error without granting: yes = %v, err = %v", yes, err)
	}
}

// The prompting functions take one caller-supplied *bufio.Reader precisely so
// answers buffered ahead by an earlier question stay readable by a later one.
func TestPromptsShareABufferedReader(t *testing.T) {
	var out bytes.Buffer
	in := bufio.NewReader(strings.NewReader("node\ncodex\nn\ny\n"))
	c, err := Pick(&out, in, Options("go", "node"), Options("claude", "codex"), fav("go"), fav("claude"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "node" || c.Agent != "codex" || c.SaveDefault {
		t.Fatalf("choice = %+v", c)
	}
	yes, err := offerSharedAuth(&out, in, "codex", "codex-shared-auth", false)
	if err != nil || !yes {
		t.Fatalf("the shared-auth answer was buffered by Pick's reader and must still be readable: yes = %v, err = %v", yes, err)
	}
}

// The shared-auth offer sits between the agent question and the save-default
// wrap-up (agent questions stay together; answers precede writes), and is
// skipped when companionFor names no companion.
func TestPickOffersSharedAuthBeforeSaveDefault(t *testing.T) {
	var out bytes.Buffer
	companions := func(agent string) SharedAuthOffer {
		if agent == "codex" {
			return SharedAuthOffer{Claimants: []string{"codex-shared-auth"}, Labels: []string{"bundled with byre"}}
		}
		return SharedAuthOffer{}
	}
	// Template none, agent codex, shared auth y, save-default n.
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("\ncodex\ny\nn\n")), Options("go"), Options("claude", "codex"), fav(""), fav(""), companions)
	if err != nil {
		t.Fatal(err)
	}
	if c.SharedAuthCompanion != "codex-shared-auth" || !c.SharedAuth || c.SaveDefault {
		t.Fatalf("choice = %+v", c)
	}
	if offer, save := strings.Index(out.String(), SharedAuthPrompt("codex")), strings.Index(out.String(), "Save these"); offer < 0 || save < 0 || offer > save {
		t.Fatalf("the offer must precede the save-default question:\n%s", out.String())
	}
	// An agent without a companion gets no offer.
	out.Reset()
	c, err = Pick(&out, bufio.NewReader(strings.NewReader("\nclaude\nn\n")), Options("go"), Options("claude", "codex"), fav(""), fav(""), companions)
	if err != nil {
		t.Fatal(err)
	}
	if c.SharedAuthCompanion != "" || c.SharedAuth || strings.Contains(out.String(), SharedAuthPrompt("claude")) {
		t.Fatalf("no companion — no offer: %+v\n%s", c, out.String())
	}
}

// The save question follows one rule for every axis of "these": ask exactly
// when saving would change stored state. A shared-auth answer differing from
// its saved preference is news even when template/agent match the favourites;
// an answer matching the preference is not.
func TestPickSaveTriggerFollowsSharedAuthNews(t *testing.T) {
	companionsWithPref := func(pref bool) func(string) SharedAuthOffer {
		return func(agent string) SharedAuthOffer {
			if agent == "codex" {
				return SharedAuthOffer{
					Claimants: []string{"codex-shared-auth"},
					Labels:    []string{"bundled with byre"},
					PrefYes:   pref,
				}
			}
			return SharedAuthOffer{}
		}
	}

	// No stored preference, answer y: news — save question appears.
	var out bytes.Buffer
	c, err := Pick(&out, bufio.NewReader(strings.NewReader("\n\ny\ny\n")), Options("go"), Options("claude", "codex"), fav("go"), fav("codex"), companionsWithPref(false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Save these") || !c.SaveDefault || !c.SharedAuth {
		t.Fatalf("an answer differing from the stored preference is news: %+v\n%s", c, out.String())
	}

	// Stored yes-preference, Enter accepts it: everything matches stored
	// state — no save question, and the input carries no answer for one.
	out.Reset()
	c, err = Pick(&out, bufio.NewReader(strings.NewReader("\n\n\n")), Options("go"), Options("claude", "codex"), fav("go"), fav("codex"), companionsWithPref(true))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Save these") || c.SaveDefault || !c.SharedAuth {
		t.Fatalf("accepting the stored preference is not news: %+v\n%s", c, out.String())
	}

	// Stored yes-preference, explicit n: news again.
	out.Reset()
	c, err = Pick(&out, bufio.NewReader(strings.NewReader("\n\nn\nn\n")), Options("go"), Options("claude", "codex"), fav("go"), fav("codex"), companionsWithPref(true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Save these") || c.SharedAuth {
		t.Fatalf("overriding the stored preference is news: %+v\n%s", c, out.String())
	}
}

// A FOREIGN claimant — a third-party or local skill asking to hold
// machine-wide credentials — carries its provenance on the question line
// itself, loud for third parties; bundled claimants keep the line bare.
func TestOfferSharedAuthForeignProvenanceOnQuestionLine(t *testing.T) {
	var out bytes.Buffer
	offer := SharedAuthOffer{
		Claimants:   []string{"foo-auth"},
		Labels:      []string{"third-party, installed 1.2.0"},
		Foreign:     []bool{true},
		VolumeNames: []string{"foo-identity"},
	}
	c, yes, err := OfferSharedAuthChoice(&out, bufio.NewReader(strings.NewReader("i\ny\n")), "foo", offer)
	if err != nil || !yes || c != "foo-auth" {
		t.Fatalf("c=%q yes=%v err=%v", c, yes, err)
	}
	got := out.String()
	if !strings.Contains(got, "(via foo-auth — THIRD-PARTY, installed 1.2.0)") {
		t.Fatalf("third-party provenance must sit on the question line, loud:\n%s", got)
	}
	if !strings.Contains(got, `"foo-identity"`) {
		t.Fatalf("the i-text must name the machine-wide volume:\n%s", got)
	}
}

// offerSharedAuth is the single-claimant call shape, kept test-side because
// only these tests want it; production always supplies the full offer.
func offerSharedAuth(out io.Writer, r *bufio.Reader, agent, companion string, prefYes bool) (bool, error) {
	_, yes, err := OfferSharedAuthChoice(out, r, agent, SharedAuthOffer{
		Claimants: []string{companion},
		Labels:    []string{""},
		PrefYes:   prefYes,
	})
	return yes, err
}
