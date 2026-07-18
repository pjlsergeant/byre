package configui

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// An engine degrade note (unreachable engine — its copies invisible) must
// survive into the rendered screen, loudly, alongside the reachable rows.
func TestVolumesScreenRendersDegradeNotes(t *testing.T) {
	fv := &fakeVols{
		vols:  []VolumeStatus{{Name: ".claude", Role: "state", Target: "/home/dev/.claude", Exists: true, Engine: "docker"}},
		notes: []string{"podman unreachable — its volume copies aren't shown and can't be cleared here (exit status 125 …)"},
	}
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, fv, TargetProject)
	m = m.openVolumes()
	out := m.viewVolumes()
	if !strings.Contains(out, "podman unreachable") || !strings.Contains(out, "⚠") {
		t.Fatalf("degrade note missing from the volumes screen:\n%s", out)
	}
	if !strings.Contains(out, ".claude") {
		t.Fatalf("reachable engine's row missing:\n%s", out)
	}

	// Every engine down: an empty list proves nothing about declarations,
	// so the "(no volumes declared)" empty-state must NOT contradict the
	// unreachable notes — the notes alone tell the story.
	fv2 := &fakeVols{notes: []string{"docker unreachable — its volume copies aren't shown and can't be cleared here (…)"}}
	m2 := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, fv2, TargetProject)
	m2 = m2.openVolumes()
	out2 := m2.viewVolumes()
	if strings.Contains(out2, "no volumes declared") {
		t.Fatalf("all-engines-down screen claims 'no volumes declared':\n%s", out2)
	}
	if !strings.Contains(out2, "docker unreachable") {
		t.Fatalf("all-engines-down screen lost its note:\n%s", out2)
	}
}

// Column widths derive from content, so the state column aligns even when
// names/targets vary wildly (identity volumes, target-less orphan rows).
func TestVolumesTableAligns(t *testing.T) {
	fv := &fakeVols{vols: []VolumeStatus{
		{Name: ".codex", Role: "state", Target: "/home/dev/.codex-home", Exists: true, Engine: "docker"},
		{Name: "opencode-identity", Exists: true, Machine: true, Orphan: true, Engine: "docker"},
		{Name: "claude-identity", Role: "state", Target: "/home/dev/.byre-identity/claude", Exists: true, Machine: true, Engine: "docker"},
		// Mixed state across engines: the [engine] suffix must not drift
		// between a 5-cell "empty" and a 7-cell "present".
		{Name: ".codex", Role: "state", Target: "/home/dev/.codex-home", Exists: false, Engine: "podman"},
	}}
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, fv, TargetProject)
	m = m.openVolumes()
	m.width = 200 // no clipping — alignment is what's under test
	col, engCol := -1, -1
	for _, line := range strings.Split(m.viewVolumes(), "\n") {
		// Byte offsets lie about columns: the ▸ cursor is 3 bytes for 1 cell.
		line = strings.ReplaceAll(line, "▸ ", "  ")
		if i := strings.Index(line, "present"); i >= 0 {
			if col == -1 {
				col = i
			} else if i != col {
				t.Fatalf("'present' drifts between columns %d and %d:\n%s", col, i, m.viewVolumes())
			}
		}
		// The [engine] suffix must sit in one column across mixed
		// empty/present rows (the state cell is padded).
		if i := strings.Index(line, " ["); i >= 0 {
			if engCol == -1 {
				engCol = i
			} else if i != engCol {
				t.Fatalf("'[engine]' drifts between columns %d and %d:\n%s", engCol, i, m.viewVolumes())
			}
		}
	}
	if col == -1 || engCol == -1 {
		t.Fatal("expected both present rows and engine suffixes")
	}
}

// Scope grouping: project rows render first under a "Project volumes" header,
// machine rows under a shared header that carries the all-your-projects fact
// once (no per-row tags), orphans last with a short flag. An all-project list
// gets no headers — there's no distinction to draw.
func TestVolumesGroupedByScope(t *testing.T) {
	fv := &fakeVols{vols: []VolumeStatus{
		{Name: "claude-identity", Role: "state", Target: "/x", Exists: true, Machine: true, Engine: "docker"},
		{Name: "opencode-identity", Exists: true, Machine: true, Orphan: true, Engine: "docker"},
		{Name: ".claude", Role: "state", Target: "/y", Exists: true, Engine: "docker"},
	}}
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, fv, TargetProject)
	m = m.openVolumes()
	// The model reorders on load (cursor indexes must match what's drawn):
	// project first, orphans last within the shared group.
	if m.volList[0].Name != ".claude" || m.volList[2].Name != "opencode-identity" {
		t.Fatalf("groupVolumes order wrong: %v", m.volList)
	}
	out := m.viewVolumes()
	proj := strings.Index(out, "Project volumes")
	mach := strings.Index(out, "Machine volumes — shared by all your projects")
	if proj == -1 || mach == -1 || proj > mach {
		t.Fatalf("scope headers missing or out of order (proj=%d mach=%d):\n%s", proj, mach, out)
	}
	if strings.Contains(out, "(shared: all your projects)") {
		t.Fatalf("per-row shared tag should be gone (the header carries it):\n%s", out)
	}
	if !strings.Contains(out, "(no longer declared)") {
		t.Fatalf("orphan row lost its flag:\n%s", out)
	}

	// All-project list: no headers.
	fv2 := &fakeVols{vols: []VolumeStatus{{Name: ".claude", Role: "state", Target: "/y", Exists: true, Engine: "docker"}}}
	m2 := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, fv2, TargetProject)
	m2 = m2.openVolumes()
	if out2 := m2.viewVolumes(); strings.Contains(out2, "Project volumes") {
		t.Fatalf("all-project list should render headerless:\n%s", out2)
	}
}

func TestVolumesClearFlow(t *testing.T) {
	fv := &fakeVols{vols: []VolumeStatus{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude", Exists: true},
		{Name: "node_modules", Role: "cache", Target: "/workspace/node_modules", Exists: false},
	}}
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, fv, TargetProject)

	// fVolumes must be present in the form when a VolumeAdmin is supplied.
	if !contains(fieldIDsToStrings(m.order), "Volumes") {
		t.Fatal("Volumes row missing from the form order")
	}

	m = m.openVolumes()
	if m.mode != modeVolumes || len(m.volList) != 2 {
		t.Fatalf("openVolumes: mode=%v n=%d", m.mode, len(m.volList))
	}

	// 'c' on a present volume arms the confirm; 'y' clears it.
	mm, _ := m.updateVolumes(key("c"))
	m = mm.(model)
	if m.volPendClear != 0 {
		t.Fatalf("clear should arm the confirm, volPendClear=%d", m.volPendClear)
	}
	// The armed confirm surfaces the admin's shared-volume warning (worktree blast
	// radius) so the config UI is as loud as reset/forget.
	fv.sharedNote = "Shared with ALL worktrees of /home/me/main."
	if v := m.viewVolumes(); !strings.Contains(v, "Shared with ALL worktrees") {
		t.Errorf("clear confirm should include the shared-volume note:\n%s", v)
	}
	mm, _ = m.updateVolumes(key("y"))
	m = mm.(model)
	if len(fv.cleared) != 1 || fv.cleared[0] != ".claude" {
		t.Fatalf("expected .claude cleared, got %v", fv.cleared)
	}
	if len(m.volList) != 1 {
		t.Fatalf("list should refresh after clear, n=%d", len(m.volList))
	}

	// Clearing an absent volume is refused with a message, no call made.
	fv.vols = []VolumeStatus{{Name: "node_modules", Role: "cache", Exists: false}}
	m = m.openVolumes()
	mm, _ = m.updateVolumes(key("c"))
	m = mm.(model)
	if m.volPendClear != -1 || m.volErr == "" {
		t.Fatalf("clearing an absent volume should be refused: pend=%d err=%q", m.volPendClear, m.volErr)
	}
}
