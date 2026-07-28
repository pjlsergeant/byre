package commands

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/deliver"
	"github.com/pjlsergeant/byre/internal/project"
)

// exclusivevolume.go enforces one volume's declared single-writer contract.
//
// Volume names are project-scoped by construction (byre-<id>-<name>), and
// worktrees of a project are DESIGNED to run concurrently (ADR 0009), so
// sibling boxes mount the identical volume set. That is the feature for a
// cache and for an agent's state directory -- the case ADR 0009's safety
// argument was written about. It is not safe for every volume a skill or a
// config can now declare: a single-writer database in a volume two boxes hold
// at once is corrupted, not raced.
//
// `sharing = "exclusive"` is how an author says so, and this is where byre
// honors it. The check reads sibling boxes' LAUNCH RECORDS (ADR 0053) for the
// volumes they actually mounted, never their current config: what a live box
// is holding is a launch-time fact, and re-resolving today's config to guess
// at it is the exact error the record exists to end.
//
// It BLOCKS. Byre's degrade-never-block rule governs byre's claims about
// ITSELF -- a bookkeeping failure must never cost a session. This is not that:
// it is byre honoring a contract the volume's author declared, against data
// loss that no later disclosure undoes. Same family as the one-session-per-
// workdir refusal and ADR 0004's cross-engine refusal, both of which block.

// exclusiveRefusal is the shared head of every single-writer refusal: the rule
// that fired, and the volume (or volumes) it fired for. One place, because the
// refusal has six arms and they must be recognisable as one rule.
func exclusiveRefusal(decl ...string) string {
	quoted := make([]string, len(decl))
	for i, d := range decl {
		quoted[i] = fmt.Sprintf("%q", d)
	}
	noun, verb := "volume", "declares"
	if len(decl) > 1 {
		noun, verb = "volumes", "declare"
	}
	return fmt.Sprintf("byre: refusing to start — %s %s %s sharing = \"exclusive\" (at most one live box may mount it)",
		noun, strings.Join(quoted, ", "), verb)
}

// exclusiveRemedy is the way out, printed under every arm. Stopping the holder
// is the fix when there is one; dropping the declaration is always available,
// because byre reports and never gates -- the contract is the author's claim
// about their data, and the user may overrule it.
const exclusiveRemedy = "  • stop the other session, or drop `sharing = \"exclusive\"` from the volume declaration if concurrent access is safe after all\n"

// refuseExclusiveVolumeHolders refuses the launch when a volume this session
// would mount declares single-writer and byre cannot establish that no live
// box of this project is holding it.
//
// Every uncertainty refuses, and the asymmetry is why: a wrong refusal costs a
// command (stop the sibling, or edit one key) while a wrong launch costs the
// volume's contents, irreversibly. So an engine byre cannot query, a sibling
// whose labels byre cannot read, and a sibling with no usable launch record
// all land in the same arm -- byre cannot prove the sibling is NOT holding the
// volume, and for a volume declared single-writer that is not a fact byre may
// assume. A sibling with no record at all is the same cannot-rule-out: its
// set is whatever it resolved at launch, unknowable now -- and volume names
// are DERIVED, not chosen, so nothing about its config would have kept it off
// this volume if its resolution included one by this name.
//
// The whole check is gated on this session declaring at least one exclusive
// volume. Nothing bundled does, so the common launch pays nothing and gains no
// new way to fail; a config that opts in is asking byre for the guarantee.
func refuseExclusiveVolumeHolders(w io.Writer, paths project.Paths, uid int, vols []config.Volume, engines []sessionRunner, declined []declinedEngine) error {
	want := map[string]string{} // engine volume name -> the name the config declares
	for _, v := range vols {
		if v.Exclusive() {
			want[scopedVolumeName(paths.ID, uid, v)] = v.Name
		}
	}
	if len(want) == 0 {
		return nil
	}
	// The arms that report an uncertainty rather than a collision name every
	// exclusive volume, sorted: byre could not look, so it cannot say which of
	// them is at risk, and picking one would imply it had. The conflict arm
	// below names the volume that actually collided.
	all := slices.Sorted(maps.Values(want))

	// Every arm prints through dataf: the engine's own error text, a
	// container's labels and a volume target are all strings byre did not
	// author, and a refusal is exactly the surface an attacker would want to
	// dress up with control sequences (P4).
	for _, d := range declined {
		dataf(w, "%s, and byre will not run %s to look for a box holding it: %v\n", exclusiveRefusal(all...), d.Engine, d)
		fmt.Fprint(w, exclusiveRemedy)
		return ExitError{Code: ExitRefused}
	}

	for _, rr := range engines {
		// RUNNING boxes only: a single-writer contract is about who has the
		// volume open, and a stopped container holds nothing. (A stopped box
		// someone starts by hand during this launch is outside byre's lock,
		// and is recorded as a residual in the ADR rather than chased.)
		ids, err := rr.RunningContainersByLabel(projectLabel(paths))
		if err != nil {
			why := firstLine(err.Error())
			if deliver.IsUnreachable(err) {
				why = fmt.Sprintf("%s isn't reachable", rr.Engine())
			}
			dataf(w, "%s, and byre could not list this project's boxes on %s (%s) — a session there could be holding it.\n", exclusiveRefusal(all...), rr.Engine(), why)
			fmt.Fprint(w, exclusiveRemedy)
			return ExitError{Code: ExitRefused}
		}
		for _, id := range ids {
			labels, lerr := rr.ContainerLabels(id)
			if lerr != nil {
				dataf(w, "%s, and byre could not read the labels of %s box %s (%v) — it could be holding it.\n", exclusiveRefusal(all...), rr.Engine(), shortID(id), firstLine(lerr.Error()))
				fmt.Fprint(w, exclusiveRemedy)
				return ExitError{Code: ExitRefused}
			}
			// This worktree's own box is not a sibling. A live one already
			// refuses upstream with the session-already-live report, which
			// says more useful things than this rule can; a stopped one holds
			// no volume open.
			if labels[workdirKey] == paths.WorktreeID {
				continue
			}
			rec, st := readLaunchRecord(paths, labels)
			if st != launchRecordOK {
				dataf(w, "%s, and byre cannot tell what %s is holding: %s.\n", exclusiveRefusal(all...), siblingLabel(labels, id), exclusiveUnknownReason(st))
				fmt.Fprint(w, exclusiveRemedy)
				return ExitError{Code: ExitRefused}
			}
			for _, held := range rec.Volumes {
				if decl, ok := want[held.Name]; ok {
					dataf(w, "%s, and %s is holding it (mounted at %s).\n", exclusiveRefusal(decl), siblingLabel(labels, id), held.Target)
					dataf(w, "  • stop it:             %s stop %s\n", rr.Engine(), shortID(id))
					fmt.Fprint(w, exclusiveRemedy)
					return ExitError{Code: ExitRefused}
				}
			}
		}
	}
	return nil
}

// siblingLabel names another box the way the Worktrees status row does: the
// worktree it belongs to, then the id to act on. The workdir label is the
// CONTAINER's, and a container carrying byre's project label need not be one
// byre created -- so the composed string goes through dataf at every call
// site rather than being trusted here.
func siblingLabel(labels map[string]string, id string) string {
	if wd := labels[workdirKey]; wd != "" {
		return fmt.Sprintf("the session in %s (%s)", wd, shortID(id))
	}
	return fmt.Sprintf("the session %s", shortID(id))
}

// exclusiveUnknownReason says why a sibling's launch record could not answer.
// Each arm names the state rather than a generic "unknown": the remedies
// differ (an older byre wants the box stopped; a tampered record wants
// looking at), and under --self-edit a box can arrange several of these
// deliberately -- reading any of them as "not holding it" would hand an agent
// a way to talk byre into a second writer.
func exclusiveUnknownReason(st launchState) string {
	switch st {
	case launchPreRecord:
		return "it carries no launch record (an older byre started it), so there is nothing that says what it mounted — and volume names are derived from the project, not chosen, so nothing about its config would have kept it off this one"
	case launchMissing:
		return "its launch record is no longer in the store"
	case launchTampered:
		return "its launch record does not match its own address, so byre will not read it"
	case launchNewer:
		return "its launch record was written by a newer byre (a schema this build does not know)"
	default:
		return "its launch record is present but unreadable"
	}
}
