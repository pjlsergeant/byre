// Package treecopytest holds the shared adversarial expectation DATA for
// byre's two race-hardened rooted tree copies: internal/build's staging
// (context.go) and internal/deliver's local transport (transport.go). The two
// answer the SAME threats under DIFFERENT contracts -- build refuses and fails
// the whole operation, deliver skips-and-reports per entry, and they disagree
// deliberately about a top-level symlink the user named. This package states,
// per route and per case, which of those answers is the right one, so a change
// to one copier that silently drifts from the other's stated policy fails a
// test rather than waiting for someone to notice the two files no longer read
// alike.
//
// It is DECLARATIVE and nothing else. Contents: the route enumeration
// (Routes), the case table (Cases), and one pure interpreter of typed
// outcomes (Case.CheckOutcome). It performs no filesystem traversal, no
// copying, no containment check and no classification -- those live in the
// production packages, and a second implementation of a rule here could not
// testify about the first one anyway.
//
// It also builds no fixtures, and that is a hard constraint rather than a
// preference: planting a symlink needs os.Symlink, a watched callee of the
// plain-os arm in internal/hostopen's TestHostOpenConformance (an unrelated
// arm -- it bans plain os filesystem calls outside hostopen), and this is a
// non-test package, so a fixture builder here would fail that walk. Fixtures
// belong to the consumer harnesses, which are _test.go files and exempt.
//
// The consumers -- one per production copier, each iterating the FULL case
// slice for its own routes and sub-testing every entry:
//
//   - internal/build/treecopy_test.go   (build.stageCopy, build.copyPath)
//   - internal/deliver/treecopy_test.go (deliver.local)
//
// Accepted residuals, named so they stay known rather than assumed away: a
// consumer that DELETES its harness leaves the table unread; a harness that
// maps a case onto the wrong fixture tests the wrong thing under the right
// name; and the harnesses build their fixtures independently, so "the same
// case" is the same case only as far as two fixture bodies agree. Review
// catches those three. This table does not.
package treecopytest

import "fmt"

// Route is one production tree-copy entry path. The three are distinct
// contracts, not three spellings of one.
type Route string

const (
	// BuildStageCopy is internal/build's `files` staging: a source named
	// relative to the project root and copied through an os.Root anchored
	// there (stageCopy -> copyRootedEntry with topLevel=true). The
	// user-named top-level source may itself be a symlink and is FOLLOWED;
	// everything interior to it is agent territory and rejects symlinks.
	BuildStageCopy Route = "build.stageCopy"
	// BuildCopyPath is internal/build's by-pathname staging for sources
	// genuinely OUTSIDE the agent-writable tree (skill files, an absolute
	// [[claude_skills]].path). Its contract stages no symlinks at all --
	// the top level included.
	BuildCopyPath Route = "build.copyPath"
	// DeliverLocal is internal/deliver's local transport. It is ONE route
	// with TWO entry points: deliverPath handles the top-level source the
	// user named, deliverDir the interior of a delivered directory. Every
	// case declares which one its deliver harness calls (Expect.Entry).
	DeliverLocal Route = "deliver.local"
)

// Routes is the full route enumeration. Every case's expectation set must be
// total over it -- that totality is what stops a case from quietly covering
// two copiers and leaving the third unexamined.
func Routes() []Route { return []Route{BuildStageCopy, BuildCopyPath, DeliverLocal} }

// Outcome is what a route did with one adversarial fixture, in the vocabulary
// the two contracts share. The point of typing it is that "build refused" and
// "deliver skipped" are both correct answers to the same input, and only a
// named outcome can say so without either one looking like a bug.
type Outcome string

const (
	// Success: the route copied/delivered the named source. For a route
	// that follows a user-named top-level symlink, that includes the
	// symlink's target.
	Success Outcome = "success"
	// Refusal: the WHOLE operation failed. Nothing of the offending entry
	// landed, and the caller gets an error rather than a partial result it
	// might mistake for a complete one.
	Refusal Outcome = "whole-operation refusal"
	// SkipEntry: the entry was passed over with a note and the operation
	// carried on and succeeded. Deliver's contract; build has no such
	// answer.
	SkipEntry Outcome = "benign skip"
	// CountedFailure: the entry failed, the walk continued, and the failure
	// is carried by a COUNT in the operation's summary plus a non-nil
	// error. Deliver's contract for an entry that should have been
	// deliverable.
	CountedFailure Outcome = "counted per-entry failure"
	// NotApplicable: the case cannot be posed to this route. Always carries
	// a reason; the harness skips the sub-test rather than inventing one.
	NotApplicable Outcome = "n/a"
)

// Entry names which of deliver.local's two entry points a case's deliver
// harness drives. The two have different policies at the same threat (a
// top-level symlink is the user's choice; an interior one is not), so a case
// that did not say which one it posed would be untestable.
type Entry string

const (
	// NoEntry is the value for routes other than deliver.local, and for a
	// deliver.local cell that is NotApplicable.
	NoEntry Entry = ""
	// DeliverPath is the top-level source the user named.
	DeliverPath Entry = "deliverPath"
	// DeliverDir is the interior of a delivered directory tree.
	DeliverDir Entry = "deliverDir"
)

// Expect is one cell of the table: what one route does with one case, and
// why. Why is required on every cell, NotApplicable included -- a cell with
// no stated reason is a guess, and this table's whole value is that it is not
// guessing.
type Expect struct {
	Outcome Outcome
	Entry   Entry // deliver.local only; NoEntry everywhere else
	Why     string
}

// Case is one adversarial fixture posed to every route.
type Case struct {
	// Name identifies the case; the harnesses key their fixtures on it and
	// fail loudly on a name they have no fixture for, so a case added here
	// cannot go unrun.
	Name string
	// Threat is what an agent gains if a route gets this wrong.
	Threat string
	// Invariant is the containment property, asserted by the harnesses
	// SEPARATELY from the outcome and always on state (what landed, what
	// the victim still holds) rather than on message prose.
	Invariant string
	// Expect is total over Routes().
	Expect map[Route]Expect
}

// For returns the case's expectation for one route. A missing cell is a bug
// in the table, not a caller's problem, so it returns a NotApplicable cell
// saying exactly that -- and the package's own totality test fails first.
func (c Case) For(r Route) Expect {
	e, ok := c.Expect[r]
	if !ok {
		return Expect{Outcome: NotApplicable, Why: fmt.Sprintf("case %q declares no expectation for route %s", c.Name, r)}
	}
	return e
}

// CheckOutcome interprets one observed outcome against the table. It is the
// one piece of logic the harnesses share, and it is pure: the harness decides
// what it observed, this decides whether that was allowed.
func (c Case) CheckOutcome(r Route, observed Outcome) error {
	e, ok := c.Expect[r]
	if !ok {
		return fmt.Errorf("case %q declares no expectation for route %s -- the table must be total over Routes()", c.Name, r)
	}
	if e.Outcome == NotApplicable {
		return fmt.Errorf("case %q is n/a on route %s (%s) -- the harness must skip it, not run it (it observed %s)", c.Name, r, e.Why, observed)
	}
	if observed == NotApplicable {
		return fmt.Errorf("case %q on route %s: the harness reported n/a, but the table expects %s -- %s", c.Name, r, e.Outcome, e.Why)
	}
	if observed != e.Outcome {
		return fmt.Errorf("case %q on route %s: observed %s, want %s.\n  why: %s\n  threat: %s\n  containment invariant: %s",
			c.Name, r, observed, e.Outcome, e.Why, c.Threat, c.Invariant)
	}
	return nil
}

// Cases is the shared table. It is deliberately compact and driven by POLICY
// DIFFERENCES rather than filled out as a matrix: a row earns its place by
// making two routes answer differently, or by pinning an answer all three
// have to keep giving.
func Cases() []Case {
	return []Case{
		{
			Name:   "top-level in-root symlink to a regular file",
			Threat: "none by itself -- the row exists because the three routes answer it differently, and a copier that adopted its neighbour's answer would either break a documented user affordance or silently widen what it stages.",
			Invariant: "only the content the user actually named reaches the destination; " +
				"a route that refuses leaves nothing behind.",
			Expect: map[Route]Expect{
				BuildStageCopy: {Outcome: Success, Why: "safeProjectPath resolved the user's own `files` entry inside the project, and stageCopy anchors at the project root with topLevel=true, so os.Root follows the in-root link. Pinned on its own by TestAssembleFilesFollowsUserNamedTopLevelSymlink."},
				BuildCopyPath:  {Outcome: Refusal, Why: "copyPath's contract stages no symlinks at all: its first StatNoFollow refuses one standing at the top level, because the ancestors of a by-pathname source are not the user's project and byre will not dereference into them."},
				DeliverLocal:   {Outcome: Success, Entry: DeliverPath, Why: "deliverPath treats a symlink the USER named as their explicit choice and opens it with follow=true."},
			},
		},
		{
			Name:      "top-level in-root symlink to a directory",
			Threat:    "a link the user names that expands to a whole tree -- and, unbounded, to a cycle.",
			Invariant: "either the linked tree's contents land under the name the user gave, or nothing does.",
			Expect: map[Route]Expect{
				BuildStageCopy: {Outcome: Success, Why: "the same follow as the file case: os.Root resolves the in-root link and copyRootedEntry recurses the directory. The tree's INTERIOR is then agent territory (topLevel=false), which is where the symlink rejection starts."},
				BuildCopyPath:  {Outcome: Refusal, Why: "same StatNoFollow refusal as the file case; a directory symlink is where following would be most expensive, and is pinned on its own by TestCopyPathRejectsTopLevelDirSymlink."},
				DeliverLocal:   {Outcome: SkipEntry, Entry: DeliverPath, Why: "deliverPath routes a symlink-to-DIRECTORY out with a note where it follows the same link to a FILE -- deliver's one asymmetry inside its own top-level policy, and what kills symlink cycles."},
			},
		},
		{
			Name:      "interior in-root symlink",
			Threat:    "an agent plants a link inside a tree the user copies as a unit; even contained, it decides what content lands under a name the user never wrote.",
			Invariant: "nothing lands under the link's own name unless the route's contract says a contained interior link is content the user already owns.",
			Expect: map[Route]Expect{
				BuildStageCopy: {Outcome: Refusal, Why: "copyRootedEntry Lstats every interior entry through the root and rejects a symlink outright, contained or not -- os.Root would otherwise silently dereference an in-root link into the image."},
				BuildCopyPath:  {Outcome: Refusal, Why: "the same copyRootedEntry rejection: copyPath hands its directory interior to it with topLevel=false. Pinned on its own by TestCopyPathRejectsInteriorSymlink."},
				DeliverLocal:   {Outcome: Success, Entry: DeliverDir, Why: "deliverDir opens interior entries through hostRoot, so a CONTAINED link resolves inside the tree the user named -- content they already selected -- and is delivered."},
			},
		},
		{
			Name:      "escaping symlink (top-level)",
			Threat:    "the classic exfiltration shape, pointed at the source's own top level.",
			Invariant: "outside content reaches the destination only on a route whose contract follows a user-named top-level symlink; where the route refuses, nothing outside lands.",
			Expect: map[Route]Expect{
				BuildStageCopy: {Outcome: Refusal, Why: "safeProjectPath EvalSymlinks the `files` source and rejects a resolution outside the project dir before any copy starts."},
				BuildCopyPath:  {Outcome: Refusal, Why: "refused for being a symlink at all, one step before the question of where it points."},
				DeliverLocal:   {Outcome: Success, Entry: DeliverPath, Why: "deliverPath has no containment root at the top level -- the path IS the user's argument, so there is nothing for it to escape from, and their link is followed like any other. Containment begins at deliverDir's interior."},
			},
		},
		{
			Name:      "escaping symlink (interior)",
			Threat:    "the exfiltration shape where it is actually reachable: an agent-planted link inside a tree the user copies wholesale.",
			Invariant: "the outside target's content never reaches the destination, on any route, and the tree's honest entries are unaffected.",
			Expect: map[Route]Expect{
				BuildStageCopy: {Outcome: Refusal, Why: "the interior symlink rejection fires first; os.Root's openat would refuse the escape regardless."},
				BuildCopyPath:  {Outcome: Refusal, Why: "same rejection through the anchored root; pinned on its own by TestCopyPathRejectsEscapingSymlinkComponents."},
				DeliverLocal:   {Outcome: SkipEntry, Entry: DeliverDir, Why: "os.Root refuses the escaping open, and deliverDir reads a failed open of a SYMLINK entry as a benign skip -- it kept delivering the rest of the tree."},
			},
		},
		{
			Name:      "broken symlink (top-level)",
			Threat:    "low on its own; it is the row that shows refusal-vs-skip is decided by WHO named the entry, not by how the open failed.",
			Invariant: "nothing lands for the dangling name.",
			Expect: map[Route]Expect{
				BuildStageCopy: {Outcome: Refusal, Why: "safeProjectPath's EvalSymlinks fails and the missing `files` source is refused by name, with the remedy in the message."},
				BuildCopyPath:  {Outcome: Refusal, Why: "StatNoFollow sees a symlink and refuses before the target is resolved at all -- refused for being a symlink, not for being broken."},
				DeliverLocal:   {Outcome: Refusal, Entry: DeliverPath, Why: "deliverPath Stats the user's symlink to route it and reports the dangling target as a delivery failure: the user named exactly this path, so there is nothing to skip past."},
			},
		},
		{
			Name:      "broken symlink (interior)",
			Threat:    "the same dangling link one level in, where deliver's answer flips and build's does not.",
			Invariant: "nothing lands for the dangling name, and the tree's honest entries still land.",
			Expect: map[Route]Expect{
				BuildStageCopy: {Outcome: Refusal, Why: "the interior Lstat rejects it as a symlink; build never reaches the question of whether the target exists."},
				BuildCopyPath:  {Outcome: Refusal, Why: "same interior symlink rejection."},
				DeliverLocal:   {Outcome: SkipEntry, Entry: DeliverDir, Why: "a failed open of a symlink entry is indistinguishable from an escape at this layer and gets the same benign skip -- deliberately, since both mean 'this link points at nothing deliverable'."},
			},
		},
		{
			Name:      "FIFO",
			Threat:    "a FIFO stats as size 0, so any size preflight waves it through, and then a plain open blocks forever -- a host-side hang an agent can plant with one mkfifo.",
			Invariant: "the attempt returns promptly, and no FIFO is copied or delivered as if it were a file.",
			Expect: map[Route]Expect{
				BuildStageCopy: {Outcome: Refusal, Why: "the open is O_NONBLOCK so the FIFO returns instead of hanging, and the type is judged from the fd's own fstat -- never a pathname stat -- which refuses it as non-regular."},
				BuildCopyPath:  {Outcome: Refusal, Why: "same nonblocking open and fd-judged refusal; pinned on its own by TestCopyPathRejectsTopLevelFIFO and TestCopyPathRejectsInteriorFIFO."},
				DeliverLocal:   {Outcome: SkipEntry, Entry: DeliverPath, Why: "deliverPath's Lstat routes a source that is neither regular nor a directory to a skip note. The open would have refused it too (hostopen.ErrNotRegular) -- the same rule one layer down, kept there so a swapped-in FIFO cannot hang either."},
			},
		},
		{
			Name:      "device node",
			Threat:    "a character device reads unbounded, so a copier that trusts a size preflight streams forever instead of blocking forever.",
			Invariant: "no device content reaches the destination.",
			Expect: map[Route]Expect{
				BuildStageCopy: {Outcome: Refusal, Why: "the fd's fstat says character device, and only a regular file may be staged."},
				BuildCopyPath:  {Outcome: Refusal, Why: "same fd-judged refusal."},
				DeliverLocal:   {Outcome: SkipEntry, Entry: DeliverPath, Why: "the same skip note as a FIFO: deliverPath's classification is regular-file-or-directory, and everything else is passed over."},
			},
		},
		{
			Name:      "mid-walk symlink swap",
			Threat:    "the check/open race itself: an entry classified as a regular file is swapped for an escaping symlink before the copier opens it.",
			Invariant: "the swapped-in target's content never reaches the destination, and the entries the walk already handled are unaffected.",
			Expect: map[Route]Expect{
				BuildStageCopy: {Outcome: NotApplicable, Why: "build has no seam to mutate a source mid-copy, and adding one would be a production seam this table is forbidden to introduce. The openat anchoring that closes the race is pinned statically instead, by TestCopyRootedEntryRefusesEscapingAncestor."},
				BuildCopyPath:  {Outcome: NotApplicable, Why: "same absent seam; the static half is TestCopyPathRejectsEscapingSymlinkComponents."},
				DeliverLocal:   {Outcome: CountedFailure, Entry: DeliverDir, Why: "an interior REGULAR file swapped for an escaping symlink after fs.WalkDir enumerated it: the open rides hostRoot, so os.Root refuses the escape, and a real-file entry that fails to open is a counted delivery failure rather than a skip. The sibling case -- the source directory itself swapped before the walk anchors -- is a whole-operation refusal and stays pinned by TestDirectorySwappedToSymlinkMidDeliveryRefused."},
			},
		},
		{
			Name:      "growth during copy",
			Threat:    "an agent appending to a file while it is being staged: an unbounded copy chases the writer, and a bounded one bakes a torn read into the image as if it were whole.",
			Invariant: "a source whose size moved is refused, not staged short or staged chasing.",
			Expect: map[Route]Expect{
				BuildStageCopy: {Outcome: Refusal, Why: "every staged regular file rides stageRegularFromFD -> copyExactly, which copies exactly the fd's observed size and then probes one byte past it, so growth and shrinkage are both refused. A real mid-copy write cannot be staged deterministically, so the harness poses the same question the production code asks: a size that disagrees with the source."},
				BuildCopyPath:  {Outcome: Refusal, Why: "the same copyExactly funnel -- both build routes converge on it."},
				DeliverLocal:   {Outcome: NotApplicable, Why: "deliver.local streams the open descriptor into the box with no size promise to break: the landed file is whatever the stream carried, and the summary reports the bytes that actually moved. The send-time size check on the REMOTE leg (internal/deliver/remote.go) is a different transport and out of this table's scope."},
			},
		},
	}
}
