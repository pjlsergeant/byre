package treecopytest

import (
	"strings"
	"testing"
)

// The table's exhaustiveness arm. A case that covers two routes and leaves the
// third unstated is the failure mode this package exists to prevent: it reads
// like coverage and is not, and the missing cell is precisely where two
// copiers are free to drift. Totality is asserted on the DATA, so a harness
// cannot restore it by filtering.
func TestEveryCaseIsTotalOverRoutes(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Cases() {
		if c.Name == "" {
			t.Fatal("a case has no name -- the harnesses key their fixtures on it")
		}
		if seen[c.Name] {
			t.Errorf("duplicate case name %q -- a harness fixture map would silently lose one", c.Name)
		}
		seen[c.Name] = true
		if strings.TrimSpace(c.Threat) == "" {
			t.Errorf("case %q states no threat", c.Name)
		}
		if strings.TrimSpace(c.Invariant) == "" {
			t.Errorf("case %q states no containment invariant -- the harnesses assert it separately from the outcome", c.Name)
		}
		if len(c.Expect) != len(Routes()) {
			t.Errorf("case %q declares %d cells for %d routes -- the table must be total, with no extras", c.Name, len(c.Expect), len(Routes()))
		}
		for _, r := range Routes() {
			e, ok := c.Expect[r]
			if !ok {
				t.Errorf("case %q has no cell for route %s", c.Name, r)
				continue
			}
			switch e.Outcome {
			case Success, Refusal, SkipEntry, CountedFailure, NotApplicable:
			default:
				t.Errorf("case %q route %s: unknown outcome %q", c.Name, r, e.Outcome)
			}
			if strings.TrimSpace(e.Why) == "" {
				t.Errorf("case %q route %s: no reason given -- every cell, n/a included, says why", c.Name, r)
			}
			switch {
			case r != DeliverLocal && e.Entry != NoEntry:
				t.Errorf("case %q route %s: Entry is deliver.local's field only, got %q", c.Name, r, e.Entry)
			case r == DeliverLocal && e.Outcome != NotApplicable && e.Entry == NoEntry:
				t.Errorf("case %q route %s: must declare which entry point the deliver harness calls (deliverPath or deliverDir)", c.Name, r)
			case r == DeliverLocal && e.Outcome != NotApplicable && e.Entry != DeliverPath && e.Entry != DeliverDir:
				t.Errorf("case %q route %s: unknown entry point %q", c.Name, r, e.Entry)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("the case table is empty")
	}
}

func TestRoutesAreDistinct(t *testing.T) {
	seen := map[Route]bool{}
	for _, r := range Routes() {
		if seen[r] {
			t.Errorf("duplicate route %q", r)
		}
		seen[r] = true
	}
	if len(seen) < 2 {
		t.Fatal("the route enumeration needs at least the two copiers it compares")
	}
}

// CheckOutcome is the one shared piece of logic, so its own rules are pinned:
// a match passes, a mismatch reports both sides, and an n/a cell run anyway is
// an error rather than a silent pass.
func TestCheckOutcome(t *testing.T) {
	c := Case{
		Name:      "example",
		Threat:    "t",
		Invariant: "i",
		Expect: map[Route]Expect{
			BuildStageCopy: {Outcome: Refusal, Why: "w"},
			BuildCopyPath:  {Outcome: NotApplicable, Why: "no seam"},
		},
	}
	if err := c.CheckOutcome(BuildStageCopy, Refusal); err != nil {
		t.Errorf("a matching outcome must pass: %v", err)
	}
	err := c.CheckOutcome(BuildStageCopy, Success)
	if err == nil {
		t.Fatal("a mismatched outcome must fail")
	}
	if !strings.Contains(err.Error(), string(Success)) || !strings.Contains(err.Error(), string(Refusal)) {
		t.Errorf("the mismatch must name both the observed and the wanted outcome: %v", err)
	}
	if err := c.CheckOutcome(BuildCopyPath, Refusal); err == nil {
		t.Error("an n/a cell that was RUN anyway must fail -- the harness has to skip it")
	}
	if err := c.CheckOutcome(BuildStageCopy, NotApplicable); err == nil {
		t.Error("a harness reporting n/a where the table expects an outcome must fail")
	}
	if err := c.CheckOutcome(DeliverLocal, Refusal); err == nil {
		t.Error("a missing cell must fail rather than pass by absence")
	}
	if got := c.For(DeliverLocal); got.Outcome != NotApplicable {
		t.Errorf("For on a missing cell = %q, want the n/a placeholder", got.Outcome)
	}
}
