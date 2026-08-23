package onboard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/tomldoc"
)

// WriteProjectConfig writes a byre.config (the host-side store path) from the
// chosen template/agent (omitting either if empty) and any skills the picker
// enabled for this box — today only the shared-auth companion when its offer
// (ADR 0025) was answered yes. It refuses to overwrite an existing config,
// and never creates the parent dir (see below).
func WriteProjectConfig(destPath, template, agent string, skills []string) error {
	var b strings.Builder
	b.WriteString("# Created by byre.\n")
	// Both axes are recorded explicitly — "none" is a real answer, stored as
	// the literal sentinel so it WINS over a template's (or any lower
	// layer's) choice in the cascade; an omitted scalar would mean "inherit"
	// and let a template silently override the user's explicit no.
	fmt.Fprintf(&b, "template = %q\n", config.OrNone(template))
	fmt.Fprintf(&b, "agent = %q\n", config.OrNone(agent))
	if len(skills) > 0 {
		quoted := make([]string, len(skills))
		for i, s := range skills {
			quoted[i] = fmt.Sprintf("%q", s)
		}
		fmt.Fprintf(&b, "skills = [%s]\n", strings.Join(quoted, ", "))
	}
	// No MkdirAll: the store dir and its path record are created together by
	// Bootstrap (develop runs it before onboarding); re-creating the dir here
	// would resurrect a store a concurrent forget deleted, without its
	// record. A vanished dir fails the publish below loudly instead.
	//
	// The exclusive publish is what keeps refuse-to-overwrite atomic: the
	// link fails if destPath exists, so there is no Stat/Write race with a
	// concurrent first-run, and an interrupted write can never leave a
	// partial byre.config — whose mere existence marks the project onboarded
	// and blocks a re-run. 0600 matches every other byre config writer;
	// byre.config is read only by byre, as this user.
	if err := hostopen.PublishFileExclusive(destPath, b.String(), 0o600); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s already exists; not overwriting", destPath)
		}
		return err
	}
	return nil
}

// SaveDefault updates the template/agent scalars in ~/.byre/default.config
// (creating it if absent), preserving any other content -- the picker never
// chose to edit the whole file, so it edits exactly two keys (tomldoc's
// style-preserving contract, ADR 0044). An empty value removes its scalar.
func SaveDefault(home, template, agent string) error {
	// default.config lives directly in home, which is not a project store —
	// creating it carries no enrollment semantics (AtomicWrite itself never
	// creates directories).
	if err := hostopen.PlainMkdirAll(home, 0o755, hostopen.StoreOwned); err != nil {
		return err
	}
	path := filepath.Join(home, "default.config")
	content, err := readDefaultConfig(home)
	if err != nil {
		return err
	}
	doc, err := tomldoc.Load([]byte(content))
	if err != nil {
		// No byre command can be the remedy: `byre config --global` parses
		// this same file before it opens, so it refuses too. The named path
		// and the parse failure are what the user takes to their own editor.
		return fmt.Errorf("%s: %w — fix it in your own editor, then re-run", path, err)
	}
	for _, kv := range []struct{ key, val string }{{"template", template}, {"agent", agent}} {
		if kv.val == "" {
			err = doc.RemoveKey(nil, kv.key)
		} else {
			err = doc.SetKey(nil, kv.key, tomldoc.String(kv.val))
		}
		if err != nil {
			return err
		}
	}
	// "The next save drops it" means THIS save too: a favourites-only save
	// still cleans legacy shared_auth state out of the file it rewrites
	// (top-level spelling migrated, incomplete answers dropped) — without
	// this, an onboarding that saved favourites with no shared-auth offer
	// preserved the state its own warning said the save would clean. Gated
	// on presence so a canonical file's bytes stay untouched (ADR 0044),
	// and degraded on a config.Parse refusal (a file the strict parser
	// refuses still gets its two scalars; the cleanup lands on a later
	// save once the file parses).
	if cfg, perr := config.Parse([]byte(content)); perr == nil {
		stored := cfg.StoredSharedAuth()
		if !cfg.SharedAuthLegacy.Empty() || len(stored.Incomplete()) > 0 {
			if err := canonicalizeSharedAuthDoc(doc, stored.Saveable()); err != nil {
				return err
			}
		}
	}
	// Atomic write, so a crash or concurrent save can't truncate the favourites.
	return config.AtomicWrite(path, string(doc.Bytes()))
}

// Favourites reads the template/agent scalars from ~/.byre/default.config (the
// user's pre-selected defaults) via a real TOML parse — the regex scraper it
// replaced broke on literal ('single-quoted') strings. Missing or unparsable
// values come back empty (the picker just starts without favourites).
func Favourites(home string) (template, agent string) {
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"), true)
	if err != nil {
		return "", ""
	}
	return cfg.Template, cfg.Agent
}

// SharedAuthAlreadyOn reports whether companion is already enabled
// machine-wide (in ~/.byre/default.config's `skills` — hand-edited, `byre
// config --global`, or a v0.1.7 machine-wide yes). Then the cascade grants
// every box shared credentials regardless of any per-box answer, so the
// per-box offer (ADR 0025) is skipped: asking [Y/n] would imply an "n" that
// does nothing. This is the ONLY suppression; the picker itself never writes
// `skills` here. An unreadable/unparsable file counts as on — the picker
// must not offer through (or, on save, edit) a file it can't read.
func SharedAuthAlreadyOn(home, companion string) bool {
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"), true)
	if err != nil {
		return true
	}
	return slices.Contains(cfg.Skills, companion)
}

// SharedAuthPreference reports the saved shared-auth preference for agent:
// whether the per-box offer should prefill Yes. Missing or unparsable file =
// no preference (the offer defaults No). Covers both dual-shape forms.
func SharedAuthPreference(home, agent string) bool {
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"), true)
	if err != nil {
		return false
	}
	return cfg.StoredSharedAuth().HasYes(agent)
}

// SharedAuthPick returns the saved companion pick for agent, or "" when the
// preference is a legacy yes-inclination with no pick (or absent).
func SharedAuthPick(home, agent string) string {
	cfg, err := config.ParseFile(filepath.Join(home, "default.config"), true)
	if err != nil {
		return ""
	}
	return cfg.StoredSharedAuth().CompanionPick(agent)
}

// SaveSharedAuthDefaultPick records the shared-auth answer as agent's saved
// preference (ADR 0025). yes with a non-empty companion writes the
// table-shape pick; yes with empty companion records nothing —
// yes-without-pick is parse-only legacy state since the 2026-08-23 ADR 0049
// amendment, so there is nothing true to write. no removes the agent's
// entry. companion is ignored when yes is false. Every write persists the
// Saveable projection, so a save also drops any legacy yes-only entries the
// file still carried (presence-triggered cleanup, same rule as the
// top-level-spelling migration below). Surgical, idempotent, and refused
// when the file can't be parsed.
func SaveSharedAuthDefaultPick(home, agent, companion string, yes bool) error {
	// Same as SaveDefault: home is not a store; creating it enrolls nothing.
	if err := hostopen.PlainMkdirAll(home, 0o755, hostopen.StoreOwned); err != nil {
		return err
	}
	path := filepath.Join(home, "default.config")
	content, err := readDefaultConfig(home)
	if err != nil {
		return err
	}
	cfg, err := config.Parse([]byte(content))
	if err != nil {
		// Same as SaveDefault: the editor byre would point at refuses this
		// file too. config.Parse's error carries the line and column.
		return fmt.Errorf("%s: %w — fix it in your own editor, then answer again", path, err)
	}
	want := cfg.StoredSharedAuth().Clone()
	if yes && companion != "" {
		if want.Pick == nil {
			want.Pick = map[string]string{}
		}
		want.Pick[agent] = companion
	} else if !yes {
		if want.Pick != nil {
			delete(want.Pick, agent)
			if len(want.Pick) == 0 {
				want.Pick = nil
			}
		}
	}
	// A yes with no companion falls through: yes-without-pick is not a
	// saveable state, so there is nothing to record (the offer re-asks).
	want = want.Saveable()
	// No-op only when there is nothing to write AND nothing to move or
	// clean: the legacy top-level spelling's PRESENCE triggers migration,
	// and a legacy yes-only entry's PRESENCE triggers its cleanup (want is
	// the Saveable projection, so any stored Yes makes the compare unequal).
	// Gating either on a CHANGED answer instead leaves the legacy state in
	// place for as long as the user keeps answering the same way, which
	// makes "the next save cleans it" false -- the rule configui's
	// reconciler states.
	if want.Equal(cfg.StoredSharedAuth()) && cfg.SharedAuthLegacy.Empty() {
		return nil
	}

	doc, err := tomldoc.Load([]byte(content))
	if err != nil {
		return err
	}
	if err := canonicalizeSharedAuthDoc(doc, want); err != nil {
		return err
	}
	// Verify the edit SEMANTICALLY before it lands.
	check, perr := config.Parse(doc.Bytes())
	if perr != nil || !check.StoredSharedAuth().Equal(want) {
		return fmt.Errorf("could not update %s (edit did not verify) — answer again via byre config --global", path)
	}
	if err := config.AtomicWrite(path, string(doc.Bytes())); err != nil {
		return fmt.Errorf("could not update %s (%v)", path, err)
	}
	return nil
}

// canonicalizeSharedAuthDoc rewrites doc's shared_auth construct to the
// canonical [defaults] pick-table spelling of want (already the Saveable
// projection), removing the retired top-level construct — a hand-written
// [shared_auth] table spelling is one construct, rewritten where it
// stands; the pre-2026-07-28 TOP-LEVEL spelling is migrated away rather
// than left to rot in two homes. The ONE owner both default.config
// writers share, so ANY save of the file performs the presence-triggered
// cleanup the compat warnings promise.
func canonicalizeSharedAuthDoc(doc *tomldoc.Doc, want config.SharedAuthPref) error {
	if err := doc.RemoveTable([]string{"shared_auth"}); err != nil {
		return err
	}
	if err := doc.RemoveKey(nil, "shared_auth"); err != nil {
		return err
	}
	if want.Empty() {
		return doc.RemoveKey([]string{"defaults"}, "shared_auth")
	}
	return doc.SetKey([]string{"defaults"}, "shared_auth", want.EncodeTOMLValue())
}

// defaultConfigStub heads a default.config the surgical writers create from
// nothing — SaveDefault and SaveSharedAuthDefaultPick must stamp the same one.
const defaultConfigStub = "# byre default.config — your favourites for new projects.\n"

// readDefaultConfig returns ~/.byre/default.config's content, or the stub for
// a file that doesn't exist (or is empty) yet.
func readDefaultConfig(home string) (string, error) {
	b, err := hostopen.PlainReadFile(filepath.Join(home, "default.config"), hostopen.StoreOwned)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if len(b) == 0 {
		return defaultConfigStub, nil
	}
	return string(b), nil
}
