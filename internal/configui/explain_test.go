package configui

import "testing"

// The explainer shares a line with nothing, but it does share a TERMINAL with
// the rows: at 80 columns a long one wraps and pushes the list down. The
// harness defaults to 100 cols, which is exactly why this is asserted rather
// than eyeballed.
func TestExplainersFitAnEightyColumnTerminal(t *testing.T) {
	const budget = 80 - 2 // the two-space indent viewList renders them with
	for _, info := range fieldInfos {
		if len([]rune(info.explain)) > budget {
			t.Errorf("%s explainer is %d runes, over the %d budget: %q",
				info.label, len([]rune(info.explain)), budget, info.explain)
		}
	}
}
