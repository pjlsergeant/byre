package runner

import (
	"fmt"
	"strconv"
	"strings"
)

// Identity is the dev-user identity a box is built and run with — the uid/gid
// baked into the image (the `dev` user, /home/dev, the volume mount points)
// and the chown target for every helper that fills the box's volumes.
//
// On a rootful engine it is the invoking host user's uid/gid: build and run
// happen as the same user in one invocation, so files land correctly owned
// with no runtime chown (ADR 0008). Under ROOTLESS Podman the host↔container
// boundary is a user-namespace remap, so the bake pivots: the image uses a
// GENERIC uid/gid and the box runs with --userns=keep-id:uid=<uid>,gid=<gid>,
// mapping the invoking host user to the baked id — same outcome, files land
// host-user-owned (ADR 0032).
type Identity struct {
	UID, GID int
	KeepID   bool // run with --userns=keep-id:uid=UID,gid=GID (rootless Podman)
}

// Userns is the --userns value for containers run under this identity; empty
// means no flag. Every container byre runs against a box's volumes — the box
// itself, seeding/migration helpers, the sock-group probe — must carry the
// SAME value: chown targets and probed gids only mean the same thing inside
// one mapping. (Helpers that join a RUNNING box's namespaces — NetnsInit —
// join the box's own userns instead; see netnsInitArgs.) The explicit
// uid=/gid= form is required: plain keep-id only aligns when the host uid
// already equals the image uid (ADR 0008).
func (i Identity) Userns() string {
	if !i.KeepID {
		return ""
	}
	return fmt.Sprintf("keep-id:uid=%d,gid=%d", i.UID, i.GID)
}

// SupportsKeepIDMapping reports whether the engine supports the explicit
// --userns=keep-id:uid=,gid= mapping form (Podman ≥ 4.3). The plain keep-id
// older Podman ships doesn't count — it only aligns when the host uid already
// equals the image uid. Non-Podman engines report false. A query error is
// returned so the caller can fall back to detect-and-refuse rather than
// guess.
func (r *Runner) SupportsKeepIDMapping() (bool, error) {
	if r.engine != Podman {
		return false, nil
	}
	out, err := r.capture(string(r.engine), "--version")
	if err != nil {
		return false, err
	}
	return podmanVersionAtLeast(out, 4, 3), nil
}

// podmanVersionAtLeast parses the trailing version out of `podman --version`
// output ("podman version 4.9.3") and compares major.minor. Unparsable
// reports false — the caller then refuses loudly instead of launching a run
// the engine would reject.
func podmanVersionAtLeast(out string, major, minor int) bool {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) == 0 {
		return false
	}
	parts := strings.SplitN(fields[len(fields)-1], ".", 3)
	if len(parts) < 2 {
		return false
	}
	maj, err1 := strconv.Atoi(parts[0])
	mnr, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return maj > major || (maj == major && mnr >= minor)
}

// appendUserns appends the --userns flag when value is non-empty (single
// --userns=v token, so run_args last-wins overriding stays a clean replace).
func appendUserns(args []string, value string) []string {
	if value != "" {
		return append(args, "--userns="+value)
	}
	return args
}
