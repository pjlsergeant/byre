package config

// warning.go is the parse-time compat-warning channel (ADR 0049, amended
// 2026-08-23): legacy spellings warn and keep working, and removal is a
// per-path operator call once a path's warnings go quiet. Warnings are
// computed per cascade LAYER — resolution strips the picker-owned state
// they describe (ADR 0025), so the resolved Config cannot carry them —
// and they are legibility only, never a gate (P1): a broken cascade
// degrades to no warnings and fails wherever it already fails.

import "fmt"

// Warning is one compat-path warning a cascade layer carries: legacy
// state that still works but is slated for retirement. Text is the whole
// user-facing sentence (finding + remedy); Kind is the stable code a
// machine surface keys on; Layer/Path say which file to fix, in
// CascadeFile's attribution vocabulary.
type Warning struct {
	Kind  string `json:"kind"`
	Layer string `json:"layer"`
	Path  string `json:"path,omitempty"`
	Text  string `json:"text"`
}

// SharedAuthReRecordRemedy is the one owner of the re-record remedy the
// array warning prints (the channel text, and the editor row that restates
// it): it must name `byre config --global` because under skip_questions no
// onboard question ever comes — "your next onboard" alone is a door that
// never opens there.
const SharedAuthReRecordRemedy = "answer the shared-auth question again (byre config --global, or your next onboard) to re-record"

// Warning kinds, one per inventoried compat path (ADR 0049).
const (
	// WarnSharedAuthTopLevel: the pre-2026-07-28 top-level shared_auth
	// spelling (canonical home is [defaults]).
	WarnSharedAuthTopLevel = "shared-auth-top-level"
	// WarnSharedAuthArray: a yes-without-pick entry (the legacy array
	// shape) — parse-only state since 2026-08-23; saves drop it.
	WarnSharedAuthArray = "shared-auth-array"
)

// LayerWarnings returns the compat warnings one parsed cascade layer
// carries. label and path attribute the layer (CascadeFile vocabulary).
func LayerWarnings(label, path string, cfg Config) []Warning {
	var out []Warning
	if !cfg.SharedAuthLegacy.Empty() {
		out = append(out, Warning{
			Kind:  WarnSharedAuthTopLevel,
			Layer: label,
			Path:  path,
			Text: "legacy top-level shared_auth — the next save moves it " +
				"under [defaults]",
		})
	}
	if yes := cfg.StoredSharedAuth().Yes; len(yes) > 0 {
		out = append(out, Warning{
			Kind:  WarnSharedAuthArray,
			Layer: label,
			Path:  path,
			Text: fmt.Sprintf("legacy shared_auth entry for %s (yes with no "+
				"companion package recorded) — the next save drops it; "+
				SharedAuthReRecordRemedy,
				quotedList(yes)),
		})
	}
	return out
}

// FileWarnings collects the compat warnings an already-walked cascade
// carries, in cascade order — the entry point for surfaces that hold a
// CascadeFiles result already (status), so warnings and the rows beside
// them describe the same read.
func FileWarnings(files []CascadeFile) []Warning {
	var out []Warning
	for _, f := range files {
		out = append(out, LayerWarnings(f.Label, f.Path, f.Cfg)...)
	}
	return out
}

// CascadeWarnings walks projectDir's cascade files and returns every
// compat warning, in cascade order. It degrades to nil on a broken
// cascade: the warning channel never blocks or fails a command that would
// otherwise run (P1) — a genuinely broken cascade fails loudly wherever
// it already fails.
func CascadeWarnings(projectDir string) []Warning {
	files, err := CascadeFiles(projectDir)
	if err != nil {
		return nil
	}
	return FileWarnings(files)
}

// Attribution names the carrying file the way a warning should: the
// cascade label a user recognizes, and the path to edit when there is one
// (CascadeFile.attribution's rule).
func (w Warning) Attribution() string {
	if w.Path == "" {
		return w.Layer
	}
	return w.Layer + " (" + w.Path + ")"
}

func quotedList(ss []string) string {
	s := ""
	for i, v := range ss {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%q", v)
	}
	return s
}
