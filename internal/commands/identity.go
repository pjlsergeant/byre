package commands

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/pjlsergeant/byre/internal/runner"
)

// genericUID/genericGID are the ids baked into the image on the rootless-
// Podman keep-id path (ADR 0032): every host user maps to the same
// in-container identity, so the image is host-uid-independent and
// --userns=keep-id:uid=1000,gid=1000 does the host↔container alignment the
// rootful path gets from baking the host uid (ADR 0008). 1000 matches the
// generated Dockerfile's ARG defaults, so an ejected `byre dockerfile` built
// with no --build-arg bakes this same identity.
const (
	genericUID = 1000
	genericGID = 1000
)

// hostIdentity is the rootful-path identity: the invoking host user, baked at
// build time so build-UID == run-UID by construction (ADR 0008).
func hostIdentity() runner.Identity {
	return runner.Identity{UID: os.Getuid(), GID: os.Getgid()}
}

// rootlessPodmanUnsupported explains the remaining unsupported case: rootless
// Podman WITHOUT the explicit keep-id mapping form (pre-4.3). Plain keep-id
// doesn't help — it only aligns when the host uid already equals the image
// uid — so without the mapping the userns remap makes files land owned by
// the wrong id.
const rootlessPodmanUnsupported = "rootless Podman detected, but this Podman lacks the keep-id mapping byre needs (--userns=keep-id:uid=,gid= — Podman 4.3+). Without it the userns remap makes files in the project and volumes land owned by the wrong id. Upgrade Podman, or use rootful Docker/Podman."

// resolveIdentity picks the identity a session is built and run with — the
// mode-select of ADR 0032, replacing the old blanket rootless refusal:
//
//   - rootful engine (or a rootless-detection error — better to run than to
//     refuse on a guess): the invoking host user, quietly (ADR 0008);
//   - rootless Podman with keep-id mapping support: the generic identity,
//     run under --userns=keep-id — announced once, because the box's dev uid
//     stops matching the host uid and that should be legible;
//   - rootless Podman without it: the old detect-and-refuse, with
//     BYRE_ALLOW_ROOTLESS_PODMAN=1 proceeding on the host identity, warned
//     (the same escape-hatch shape as requireNonRootHost/BYRE_ALLOW_ROOT).
func resolveIdentity(warn io.Writer, r sessionRunner) (runner.Identity, error) {
	rootless, err := r.IsRootlessPodman()
	if err != nil || !rootless {
		return hostIdentity(), nil
	}
	if ok, kerr := r.SupportsKeepIDMapping(); kerr == nil && ok {
		ident := runner.Identity{UID: genericUID, GID: genericGID, KeepID: true}
		fmt.Fprintf(warn, "byre: rootless Podman — running with --userns=%s (the box's dev user is uid %d, mapped to you; files it writes land owned by you).\n", ident.Userns(), ident.UID)
		return ident, nil
	}
	if os.Getenv("BYRE_ALLOW_ROOTLESS_PODMAN") == "1" {
		fmt.Fprintln(warn, "byre: WARNING: running anyway (BYRE_ALLOW_ROOTLESS_PODMAN=1). "+rootlessPodmanUnsupported)
		return hostIdentity(), nil
	}
	return runner.Identity{}, errors.New("refusing to run under rootless Podman: " + rootlessPodmanUnsupported + " Set BYRE_ALLOW_ROOTLESS_PODMAN=1 to proceed anyway.")
}

// engineIdentity is resolveIdentity for passive/lifecycle paths (status,
// forget, rehome, the dockerrun printers) — same mode-select, but never a
// refusal and never output: an engine this command can only inspect or clean
// up after shouldn't block on the develop-time support question. Unsupported
// rootless falls back to the host identity (uid/gid, threaded for tests),
// which is what those paths always used.
func engineIdentity(r sessionRunner, uid, gid int) runner.Identity {
	if rootless, err := r.IsRootlessPodman(); err == nil && rootless {
		if ok, kerr := r.SupportsKeepIDMapping(); kerr == nil && ok {
			return runner.Identity{UID: genericUID, GID: genericGID, KeepID: true}
		}
	}
	return runner.Identity{UID: uid, GID: gid}
}

// imageTagCandidates lists every image tag this project may have built on
// engine r, the engine's current identity first: the keep-id generic tag
// when the engine is rootless Podman, the host-identity tag, and the legacy
// unqualified byre-<id> (pre-UID-bake builds) last. uid/gid are the host
// user's ids (threaded for tests).
//
// The generic tag rides confirmed ROOTLESSNESS alone, not the keep-id
// version gate: a box built while keep-id was supported must stay
// discoverable by forget/rehome after a Podman downgrade or a flaky version
// probe. It stays engine-gated, though — on a SHARED rootful daemon the
// u1000-g1000 tag may be a real uid-1000 user's image, which lifecycle
// commands must never capture; rootless storage is per-user, so there the
// capture is impossible.
func imageTagCandidates(r sessionRunner, projectID string, uid, gid int) []string {
	ident := engineIdentity(r, uid, gid)
	tags := []string{imageTag(projectID, ident.UID, ident.GID)}
	add := func(tag string) {
		for _, t := range tags {
			if t == tag {
				return
			}
		}
		tags = append(tags, tag)
	}
	if rootless, err := r.IsRootlessPodman(); err == nil && rootless {
		add(imageTag(projectID, genericUID, genericGID))
	}
	add(imageTag(projectID, uid, gid))
	return append(tags, "byre-"+projectID)
}
