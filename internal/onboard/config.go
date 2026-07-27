package onboard

import (
	"fmt"
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
// (ADR 0025) was answered yes. It refuses to overwrite an existing config and
// creates the parent dir if needed.
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
	// record. A vanished dir fails the CreateTemp below loudly instead.
	// Sibling temp file, then link(2) into place: the link fails if destPath
	// exists, keeping the refuse-to-overwrite guarantee atomic (no Stat/Write
	// race with a concurrent first-run) — and an interrupted write can never
	// leave a partial byre.config, whose mere existence marks the project as
	// onboarded and blocks a re-run.
	tmp, err := hostopen.PlainCreateTemp(filepath.Dir(destPath), ".byre-onboard-*", hostopen.StoreOwned)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer hostopen.PlainRemove(tmpName, hostopen.ByreCreated)
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// The file keeps CreateTemp's private 0600 — the same mode every other
	// byre config writer (config.AtomicWrite) produces, and byre.config is
	// read only by byre as this user.
	if err := hostopen.PlainLink(tmpName, destPath, hostopen.StoreOwned); err != nil {
		if os.IsExist(err) {
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
		return fmt.Errorf("%s: %w — fix it (byre config --global opens it), then re-run", path, err)
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
// table-shape pick; yes with empty companion writes a legacy-style
// yes-inclination (array) only when no picks exist at all; no removes the
// agent from both shapes. companion is ignored when yes is false.
// Surgical, idempotent, and refused when the file can't be parsed.
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
		return fmt.Errorf("%s: %w — fix it (byre config --global opens it), then answer again", path, err)
	}
	want := cfg.StoredSharedAuth().Clone()
	if yes {
		if companion != "" {
			if want.Pick == nil {
				want.Pick = map[string]string{}
			}
			want.Pick[agent] = companion
			// Drop any legacy Yes entry for this agent.
			want.Yes = removeString(want.Yes, agent)
		} else if len(want.Pick) == 0 && !slices.Contains(want.Yes, agent) {
			// Yes-inclination only, the legacy array shape. Guarded to the
			// no-picks case: EncodeTOMLValue renders picks-only whenever any
			// pick exists, so a Yes entry appended beside picks would be
			// dropped at encode and fail the semantic verify below. With any
			// pick stored, yes-without-a-new-pick keeps what's stored.
			want.Yes = append(append([]string{}, want.Yes...), agent)
		}
	} else {
		want.Yes = removeString(want.Yes, agent)
		if want.Pick != nil {
			delete(want.Pick, agent)
			if len(want.Pick) == 0 {
				want.Pick = nil
			}
		}
	}
	// No-op when the stored preference already matches.
	if want.Equal(cfg.StoredSharedAuth()) {
		return nil
	}

	doc, err := tomldoc.Load([]byte(content))
	if err != nil {
		return err
	}
	// Canonical inline value under [defaults]; a hand-written [shared_auth]
	// table spelling is one construct, normalized only now that the
	// preference itself changed. The pre-2026-07-28 TOP-LEVEL spelling is
	// migrated away here rather than left to rot in two homes.
	if err := doc.RemoveTable([]string{"shared_auth"}); err != nil {
		return err
	}
	if err := doc.RemoveKey(nil, "shared_auth"); err != nil {
		return err
	}
	if want.Empty() {
		err = doc.RemoveKey([]string{"defaults"}, "shared_auth")
	} else {
		err = doc.SetKey([]string{"defaults"}, "shared_auth", want.EncodeTOMLValue())
	}
	if err != nil {
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

func removeString(ss []string, x string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s != x {
			out = append(out, s)
		}
	}
	return out
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
