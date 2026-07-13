package commands

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

func proposeConfig(t *testing.T, projectDir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(projectDir, "byre.config"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptYesCopiesToStore(t *testing.T) {
	p, proj := onboardPaths(t)
	proposeConfig(t, proj, "agent = \"codex\"\nrun_args = [\"--privileged\"]\n")

	s, _, out := testStreams("y\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	// The grant summary must surface the dangerous run_args.
	if !strings.Contains(out.String(), "--privileged") {
		t.Errorf("adopt prompt should surface run_args grant:\n%s", out.String())
	}
	// Copied into the store + record written.
	b, err := os.ReadFile(filepath.Join(p.Dir, "byre.config"))
	if err != nil || !strings.Contains(string(b), "codex") {
		t.Fatalf("config not adopted into the store: %v / %s", err, b)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "adopted")); err != nil {
		t.Errorf("adoption record not written: %v", err)
	}

	// Second call with the same proposal is a no-op (unchanged): no prompt output.
	s2, _, out2 := testStreams("", true)
	if err := adoptIfProposed(s2, proj, p); err != nil {
		t.Fatal(err)
	}
	if out2.Len() != 0 {
		t.Errorf("unchanged proposal should not re-prompt: %s", out2.String())
	}
}

// mutateOnRead runs fn just before the first Read — the moment adopt reads the
// confirmation — modeling a proposal edited while the human was reviewing.
type mutateOnRead struct {
	r    io.Reader
	fn   func()
	once sync.Once
}

func (m *mutateOnRead) Read(p []byte) (int, error) {
	m.once.Do(m.fn)
	return m.r.Read(p)
}

// Consent is to the bytes that were reviewed: if the proposal changes between
// the review and the under-lock write, adopt must abort, not adopt bytes the
// human never saw.
func TestAdoptAbortsWhenProposalChangesUnderReview(t *testing.T) {
	p, proj := onboardPaths(t)
	proposeConfig(t, proj, "agent = \"codex\"\n")
	in := &mutateOnRead{r: strings.NewReader("y\n"), fn: func() {
		proposeConfig(t, proj, "agent = \"codex\"\nrun_args = [\"--privileged\"]\n")
	}}
	var out bytes.Buffer
	s := Streams{Out: &out, Err: &out, In: in, TTY: true}
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); !os.IsNotExist(err) {
		t.Fatalf("a proposal that changed under review must not be adopted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "adopted")); !os.IsNotExist(err) {
		t.Fatalf("no adoption record for an aborted adoption: %v", err)
	}
	if !strings.Contains(out.String(), "changed while you were reviewing") {
		t.Errorf("abort should say why:\n%s", out.String())
	}
}

// A proposal that only SELECTS a template must still surface the grants that
// template contributes — the adoption summary reflects the effective config.
func TestAdoptShowsTemplateContributedGrants(t *testing.T) {
	p, proj := onboardPaths(t)
	tmplDir := filepath.Join(p.Home, "templates", "danger")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "template.config"), []byte("run_args = [\"--privileged\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proposeConfig(t, proj, "template = \"danger\"\n") // proposal itself looks innocent

	s, _, out := testStreams("n\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--privileged") {
		t.Errorf("adoption must surface template-contributed grants:\n%s", out.String())
	}
}

func TestAdoptNoLeavesStoreUntouched(t *testing.T) {
	p, proj := onboardPaths(t)
	proposeConfig(t, proj, "agent = \"codex\"\n")

	s, _, out := testStreams("n\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); !os.IsNotExist(err) {
		t.Errorf("declined proposal must not be written to the store")
	}
	// The no sticks: the user is told it won't ask again, and how to change
	// their mind.
	if !strings.Contains(out.String(), "won't ask again") {
		t.Errorf("decline should say it sticks:\n%s", out.String())
	}
	if got := proposalState(proj, p); got != "declined" {
		t.Errorf("proposalState after decline = %q, want declined", got)
	}
}

// Saying no sticks until the proposal's bytes change: the same version never
// re-prompts, an edited one does, and adopting the new version clears the
// stale decline.
func TestAdoptDeclineSticksUntilProposalChanges(t *testing.T) {
	p, proj := onboardPaths(t)
	proposeConfig(t, proj, "agent = \"codex\"\n")
	s, _, _ := testStreams("n\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}

	// Same bytes again: silent, no prompt.
	s2, _, out2 := testStreams("", true)
	if err := adoptIfProposed(s2, proj, p); err != nil {
		t.Fatal(err)
	}
	if out2.Len() != 0 {
		t.Errorf("declined proposal must not re-prompt while unchanged: %s", out2.String())
	}

	// An edit re-prompts; adopting clears the decline record.
	proposeConfig(t, proj, "agent = \"claude\"\n")
	s3, _, out3 := testStreams("y\n", true)
	if err := adoptIfProposed(s3, proj, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out3.String(), "Adopt this config?") {
		t.Fatalf("changed proposal should prompt again:\n%s", out3.String())
	}
	if _, err := os.Stat(filepath.Join(p.Dir, declinedRecord)); !os.IsNotExist(err) {
		t.Errorf("adoption should clear the stale decline record")
	}
	if got := proposalState(proj, p); got != "adopted" {
		t.Errorf("proposalState after re-adopt = %q, want adopted", got)
	}
}

// With a store config already in place, the prompt reviews the DELTA: adoption
// replaces the whole file, so lines only in the store (host-local extras)
// must read as removals — that's the wholesale-replace footgun made legible.
func TestAdoptChangedShowsDiffAgainstStore(t *testing.T) {
	p, proj := onboardPaths(t)
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, "byre.config"),
		[]byte("agent = \"codex\"\napt = [\"ripgrep\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proposeConfig(t, proj, "agent = \"claude\"\napt = [\"ripgrep\"]\n")

	s, _, out := testStreams("n\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "-agent = \"codex\"") || !strings.Contains(got, "+agent = \"claude\"") {
		t.Errorf("prompt should diff proposal against the store config:\n%s", got)
	}
	// Unchanged nearby lines print as CONTEXT (leading space) — they anchor
	// the change to its block, which is why the unified differ is here.
	if !strings.Contains(got, "\n apt = [\"ripgrep\"]") {
		t.Errorf("context line missing from the diff view:\n%s", got)
	}
	if !strings.Contains(got, "replaces the whole file") {
		t.Errorf("diff view should name the wholesale replace:\n%s", got)
	}
}

// "Identical" is a byte claim: a proposal differing only in its final newline
// must show that edit, not be presented as identical.
func TestAdoptDiffNamesTrailingNewlineOnlyChange(t *testing.T) {
	p, proj := onboardPaths(t)
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, "byre.config"), []byte("agent = \"codex\""), 0o644); err != nil {
		t.Fatal(err)
	}
	proposeConfig(t, proj, "agent = \"codex\"\n")

	s, _, out := testStreams("n\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "identical") {
		t.Errorf("newline-only change must not claim identical:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "No newline at end of file") {
		t.Errorf("newline-only change should be visible in the diff:\n%s", out.String())
	}
}

func TestAdoptNonTTYNeverAdopts(t *testing.T) {
	p, proj := onboardPaths(t)
	proposeConfig(t, proj, "agent = \"codex\"\n")

	s, _, out := testStreams("y\n", false)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); !os.IsNotExist(err) {
		t.Errorf("non-TTY must never adopt, even with 'y' piped in")
	}
	if !strings.Contains(out.String(), "interactively") {
		t.Errorf("non-TTY should tell the user to run interactively:\n%s", out.String())
	}
}

func TestAdoptChangedReprompts(t *testing.T) {
	p, proj := onboardPaths(t)
	proposeConfig(t, proj, "agent = \"codex\"\n")
	s, _, _ := testStreams("y\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	// Change the proposal: it must prompt again (hash differs).
	proposeConfig(t, proj, "agent = \"claude\"\n")
	s, _, out := testStreams("y\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "changed") {
		t.Errorf("a changed proposal should re-prompt as changed:\n%s", out.String())
	}
	b, _ := os.ReadFile(filepath.Join(p.Dir, "byre.config"))
	if !strings.Contains(string(b), "claude") {
		t.Errorf("re-adopt should update the store: %s", b)
	}
}

// TestAdoptRejectsInvalidLayer pins the adoption gate: a proposal that parses
// but fails the per-layer rules (here: two mounts on one target) must not be
// copied into the store — Load would reject that same file on the very next
// develop, bricking a byre-owned path.
func TestAdoptRejectsInvalidLayer(t *testing.T) {
	p, proj := onboardPaths(t)
	proposeConfig(t, proj, "[[mounts]]\nhost = \"/a\"\ntarget = \"/x\"\n[[mounts]]\nhost = \"/b\"\ntarget = \"/x\"\n")

	s, _, errBuf := testStreams("y\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); !os.IsNotExist(err) {
		t.Error("an invalid proposal must never reach the store")
	}
	if !strings.Contains(errBuf.String(), "invalid") {
		t.Errorf("the user should be told why the proposal was ignored: %q", errBuf.String())
	}
}

// TestAdoptRejectsHostCascadeConflict pins the second adoption gate: a
// proposal that is fine as a single layer but collides with THIS host's
// default.config (here: its volume targets a default-layer mount's path) must
// not be adopted — the next develop's Load would fail on the store copy.
func TestAdoptRejectsHostCascadeConflict(t *testing.T) {
	p, proj := onboardPaths(t)
	if err := os.WriteFile(filepath.Join(p.Home, "default.config"),
		[]byte("[[mounts]]\nhost = \"/data\"\ntarget = \"/x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proposeConfig(t, proj, "[[volumes]]\nname = \"v\"\nrole = \"cache\"\ntarget = \"/x\"\n")

	s, _, errBuf := testStreams("y\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); !os.IsNotExist(err) {
		t.Error("a proposal that can't resolve on this host must not reach the store")
	}
	if !strings.Contains(errBuf.String(), "doesn't resolve against this host") {
		t.Errorf("the user should be pointed at the host-side conflict: %q", errBuf.String())
	}
}

// TestAdoptBuiltinTemplateOnFreshHome pins the gate ordering: on a fresh
// ~/.byre, a proposal naming a BUILT-IN template must materialize the builtins
// before the cascade gate, not be rejected as "template not found".
func TestAdoptBuiltinTemplateOnFreshHome(t *testing.T) {
	p, proj := onboardPaths(t)
	proposeConfig(t, proj, "template = \"go\"\nagent = \"claude\"\n")

	s, _, errBuf := testStreams("y\n", true)
	if err := adoptIfProposed(s, proj, p); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), "doesn't resolve") {
		t.Fatalf("built-in template must not fail the cascade gate on a fresh home: %q", errBuf.String())
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); err != nil {
		t.Errorf("valid proposal should adopt: %v", err)
	}
}

func grantTexts(lines []grantLine) string {
	var out []string
	for _, l := range lines {
		out = append(out, l.Text)
	}
	return strings.Join(out, "\n")
}

func TestGrantSummaryMarksDisabledMounts(t *testing.T) {
	got := grantTexts(grantSummary(config.Config{Mounts: []config.Mount{
		{Host: "/a", Target: "/a", Mode: "rw"},
		{Host: "/b", Target: "/b", Mode: "rw", Disabled: true},
	}}))
	if !strings.Contains(got, "/a->/a(rw)") {
		t.Errorf("active mount missing: %q", got)
	}
	// Adopting a disabled mount plants an entry one flip away from a grant:
	// the reviewer must see it, marked, not have it hidden.
	if !strings.Contains(got, "/b->/b(rw, disabled)") {
		t.Errorf("disabled mount should be shown marked: %q", got)
	}
}

// The summary's charter (nothing smuggled unseen) covers every Grant class:
// machine-scoped volumes — the shared-credential shape, and the only grant
// that crosses project scope — plus ports and egress.
func TestGrantSummaryFlagsMachineVolumesPortsEgress(t *testing.T) {
	lines := grantSummary(config.Config{
		Volumes: []config.Volume{
			{Name: "claude-identity", Role: "state", Target: "/x", Scope: "machine"},
			{Name: "cache", Role: "cache", Target: "/c"}, // per-project: quiet
		},
		Ports: []config.Port{{Container: 3000}, {Container: 8080, Host: 80, Interface: "0.0.0.0"}, {Container: 9999, Remove: true}},
	})
	got := grantTexts(lines)
	if !strings.Contains(got, `machine-scoped volume "claude-identity"`) || !strings.Contains(got, "every project on this machine") {
		t.Errorf("machine-scoped volume must be flagged loudly: %q", got)
	}
	if strings.Contains(got, `"cache"`) {
		t.Errorf("per-project volumes are the sandbox model, not a grant: %q", got)
	}
	var cross bool
	for _, l := range lines {
		if strings.Contains(l.Text, "claude-identity") && l.CrossProject {
			cross = true
		}
	}
	if !cross {
		t.Error("the machine-volume line must carry the cross-project emphasis flag")
	}
	if !strings.Contains(got, "binds host ports: 127.0.0.1:3000->3000, 0.0.0.0:80->8080") {
		t.Errorf("ports must be summarized (removal markers skipped): %q", got)
	}
	if strings.Contains(got, "9999") {
		t.Errorf("a removal marker grants nothing: %q", got)
	}
}

// Egress is summarized with its honest posture status, and never hidden even
// when the cascade can't be expanded.
func TestEgressGrantLineStatus(t *testing.T) {
	if got := grantTexts(egressGrantLine([]string{"a.com", "b.com:8443"}, "restricted", "firewall", true)); !strings.Contains(got, "live — skill \"firewall\" sets posture \"restricted\"") {
		t.Errorf("posture-live phrasing: %q", got)
	}
	if got := grantTexts(egressGrantLine([]string{"a.com"}, "", "", true)); !strings.Contains(got, "inert now") {
		t.Errorf("no-posture phrasing: %q", got)
	}
	if got := grantTexts(egressGrantLine([]string{"a.com"}, "", "", false)); !strings.Contains(got, "under a restrictive network posture") {
		t.Errorf("unknown-posture fallback phrasing: %q", got)
	}
	if lines := egressGrantLine(nil, "p", "s", true); lines != nil {
		t.Errorf("no entries — no line: %v", lines)
	}
}

func TestSkillGrantSummaryContainmentTopSorted(t *testing.T) {
	var sf skills.File
	sf.Runtime.Containment = "docker-host opens a containment hole -- skim docs"
	sf.Runtime.Mounts = []config.Mount{{Host: "/var/run/docker.sock", Target: "/var/run/docker.sock", Mode: "rw"}}
	sf.Runtime.SockGroups = []string{"/var/run/docker.sock"}
	sf.Volumes = []config.Volume{{Name: "id", Role: "state", Target: "/x", Scope: "machine"}}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "docker-host", File: sf}}}
	lines := skillGrantSummary(res)
	if len(lines) < 2 {
		t.Fatalf("expected containment + other grants: %+v", lines)
	}
	if !lines[0].Containment || !strings.Contains(lines[0].Text, "containment hole") {
		t.Fatalf("containment must be first: %+v", lines[0])
	}
	// After full sort, containment still tops cross-project.
	mixed := append([]grantLine{{Text: "plain"}, {Text: "machine", CrossProject: true}}, lines...)
	sorted := sortGrantLines(mixed)
	if !sorted[0].Containment {
		t.Fatalf("sortGrantLines containment first: %+v", sorted)
	}
	if !sorted[1].CrossProject {
		t.Fatalf("cross-project second: %+v", sorted)
	}
}
