package configui

import "testing"

// The growth guard for fieldInfos: a new fieldID missing its metadata row
// would render a blank label, a blank crumb, and a kindScalar
// misclassification SILENTLY (map zero value) — this makes it a test
// failure instead. Relies on fExtends being the last fieldID; moving it
// breaks this loop loudly.
func TestFieldInfosCoverEveryField(t *testing.T) {
	for f := fBase; f <= fExtends; f++ {
		info, ok := fieldInfos[f]
		if !ok {
			t.Errorf("fieldID %d has no fieldInfos row — its label, kind, title, and noun all silently zero", f)
			continue
		}
		if info.label == "" {
			t.Errorf("fieldID %d: empty label", f)
		}
		if info.kind == kindList {
			if info.item == "" {
				t.Errorf("%s: list field with no item-editor title", info.label)
			}
			if info.noun == "" {
				t.Errorf("%s: list field with no summary noun", info.label)
			}
		}
		if info.kind == kindText && info.tomlKey == "" {
			t.Errorf("%s: raw text field with no TOML key hint", info.label)
		}
	}
	if len(fieldInfos) != int(fExtends)+1 {
		t.Errorf("fieldInfos has %d rows for %d fieldIDs — a row names a nonexistent field", len(fieldInfos), int(fExtends)+1)
	}
}
