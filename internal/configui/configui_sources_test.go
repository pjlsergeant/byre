package configui

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

func sourcesModel() model {
	inh := Inherited{
		HasLower: true,
		Default: config.Config{Sources: map[string]config.SourceHint{
			"acme/lint": {URI: "https://example.invalid/lint.tgz"},
			"acme/tool": {URI: "https://example.invalid/old.tgz"},
		}},
	}
	cfg := config.Config{Sources: map[string]config.SourceHint{
		"acme/tool": {URI: "https://example.invalid/tool.tgz", Digest: "sha256:" + strings.Repeat("a", 64)},
	}}
	return newModel("t", "/tmp/x", cfg, nil, nil, nil, nil, inh, nil, TargetProject)
}

// [sources] is shown, attributed, and says whether each hint pins a digest --
// the property that decides what a printed install command will actually fetch.
func TestSourceRowsShowOriginAndPinning(t *testing.T) {
	m := sourcesModel()
	rows := m.fieldRows(fSources)
	if len(rows) != 2 {
		t.Fatalf("want one row per package id, got %d: %+v", len(rows), rows)
	}
	var local, inherited listRow
	for _, r := range rows {
		switch r.ident {
		case "acme/tool":
			local = r
		case "acme/lint":
			inherited = r
		}
	}
	if local.kind != rowOverride || local.source != "default" {
		t.Errorf("a hint this file restates should read as an override of the layer below: %+v", local)
	}
	if !strings.Contains(local.text, "tool.tgz") || !strings.Contains(local.text, "pinned sha256:") {
		t.Errorf("local row must name the uri and the pin: %q", local.text)
	}
	if inherited.kind != rowInherited || inherited.source != "default" {
		t.Errorf("inherited hint misattributed: %+v", inherited)
	}
	if !strings.Contains(inherited.text, "unpinned") {
		t.Errorf("a hint with no digest must say so: %q", inherited.text)
	}
}

// The screen answers a question; it does not author consent. Every write key
// is refused, and the refusal names the flow that does write these.
func TestSourceScreenRefusesEveryWriteAndNamesTheWriter(t *testing.T) {
	m := sourcesModel()
	m.listField = fSources
	m.mode = modeList

	if !isReadOnlyField(fSources) {
		t.Fatal("[sources] must be a read-only screen")
	}
	for _, k := range []string{"a", "e", "d", "x", "o"} {
		mm, _ := m.updateList(key(k))
		next := mm.(model)
		if next.mode != modeList {
			t.Fatalf("%q left the read-only screen (mode=%v)", k, next.mode)
		}
		if !strings.Contains(next.status, "preset apply") {
			t.Errorf("%q should be answered with who writes [sources]: %q", k, next.status)
		}
	}
	// Enter on a row must not open the action menu either: the rows carry
	// ordinary local/override kinds, which rowChoices would gladly offer
	// Edit/Delete for.
	mm, _ := m.updateList(key("enter"))
	if next := mm.(model); next.mode == modeMenu {
		t.Error("a read-only row must not open an editing menu")
	}
	// Nothing typed at the screen may change what a save would write.
	if got := m.assemble().Sources; len(got) != 1 || got["acme/tool"].Digest == "" {
		t.Errorf("the screen must round-trip [sources] untouched: %+v", got)
	}
}

// The row is reachable in every editor: a hint may be recorded in the global
// default or a layer just as legitimately as in a project.
func TestSourcesRowReachableInEveryTarget(t *testing.T) {
	for _, target := range []Target{TargetProject, TargetGlobal, TargetLayer} {
		m := newModel("t", "/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, target)
		if indexOfField(m.order, fSources) < 0 {
			t.Errorf("target %v has no [sources] row", target)
		}
	}
}
