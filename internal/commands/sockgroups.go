package commands

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

// bindHostByTarget maps bind target -> host path (first match wins; binds
// are unique by target under normal validation).
func bindHostByTarget(binds []runner.BindMount) map[string]string {
	byTarget := map[string]string{}
	for _, b := range binds {
		if _, ok := byTarget[b.Target]; !ok {
			byTarget[b.Target] = b.Host
		}
	}
	return byTarget
}

// sortSockGroups orders sock_groups declarations by skill then path, so the
// warnings both appliers print come out in a stable order.
func sortSockGroups(sgs []skills.SockGroup) {
	sort.SliceStable(sgs, func(i, j int) bool {
		if sgs[i].Skill != sgs[j].Skill {
			return sgs[i].Skill < sgs[j].Skill
		}
		return sgs[i].Path < sgs[j].Path
	})
}

// applySockGroups probes each skill-declared sock_groups path engine-side and
// injects numeric --group-add gids into params. Failures are attributed to the
// skill and never silently skipped (the box still launches; the engine is the
// authority if the bind itself is broken). Returns the path->gid map for
// grant rendering that wants the derived gid.
//
// Each sock_groups path must already match a bind target in params (Resolve
// enforced that against the skill's mounts); a missing bind is attributed.
func applySockGroups(r sessionRunner, w io.Writer, image string, params *runner.RunParams, res skills.Resolved) map[string]int {
	sgs := res.SockGroups()
	if len(sgs) == 0 {
		return nil
	}
	byTarget := bindHostByTarget(params.Binds)
	sortSockGroups(sgs)
	seenGid := map[int]bool{}
	gids := map[string]int{} // path -> gid
	for _, sg := range sgs {
		host, ok := byTarget[sg.Path]
		if !ok {
			fmt.Fprintf(w, "byre: warning: skill %q sock_groups path %q has no active bind -- skipping --group-add\n", sg.Skill, sg.Path)
			continue
		}
		// The probe runs under the box's own userns mapping (params.Userns) so
		// the gid it reports is the gid the box will actually see.
		gid, err := r.ProbeSockGroup(image, host, sg.Path, params.Userns)
		if err != nil {
			fmt.Fprintf(w, "byre: warning: skill %q could not probe gid for %q: %v -- skipping --group-add\n", sg.Skill, sg.Path, err)
			continue
		}
		gids[sg.Path] = gid
		if !seenGid[gid] {
			seenGid[gid] = true
			params.GroupAdds = append(params.GroupAdds, gid)
		}
	}
	return gids
}

// warnSockSources prints attributed warnings when a sock_groups bind source is
// missing or not a socket on the host. The launch still proceeds -- the engine
// is the authority (missing --mount source fails create closed). Under Docker
// Desktop the host path is often absent while the VM serves a live socket, so
// the warning is suppressed there to avoid training users to ignore it.
func warnSockSources(r sessionRunner, w io.Writer, params runner.RunParams, res skills.Resolved) {
	sgs := res.SockGroups()
	if len(sgs) == 0 {
		return
	}
	desktop, derr := r.IsDockerDesktop()
	if derr != nil {
		// Detection failed -- stay quiet about Desktop, still warn on host
		// facts when they look wrong (native Linux is the common case).
		desktop = false
	}
	if desktop {
		return // host stat is not authoritative under Desktop
	}
	byTarget := bindHostByTarget(params.Binds)
	sortSockGroups(sgs)
	for _, sg := range sgs {
		host, ok := byTarget[sg.Path]
		if !ok {
			continue
		}
		fi, err := os.Stat(host)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(w, "byre: warning: skill %q mounts %q but the host source is missing (Docker not running? Podman-only host?) -- launching anyway; the engine is the authority\n", sg.Skill, host)
			} else {
				fmt.Fprintf(w, "byre: warning: skill %q could not stat %q: %v -- launching anyway\n", sg.Skill, host, err)
			}
			continue
		}
		// Not a socket: exists but wrong type (dir/file left behind).
		if fi.Mode()&os.ModeSocket == 0 {
			fmt.Fprintf(w, "byre: warning: skill %q mounts %q but the host source is not a socket -- launching anyway; the engine is the authority\n", sg.Skill, host)
		}
	}
}
