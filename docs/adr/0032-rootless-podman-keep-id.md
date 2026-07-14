# Rootless Podman runs a generic-UID image under keep-id

Decided 2026-07-14. Rootless Podman graduates from detect-and-refuse to a
first-class path: byre builds a **generic-UID image** (dev at 1000:1000,
the generated Dockerfile's own ARG defaults) and runs the box with
`--userns=keep-id:uid=1000,gid=1000`, mapping the invoking host user onto
the baked dev uid. Same outcome as the rootful bake (ADR 0008): every file
the box writes lands host-side owned by the invoker -- here by way of the
user-namespace mapping instead of an id match. The mode-select is
per-engine at session start (`resolveIdentity`): rootful engines keep the
ADR 0008 host-identity bake unchanged.

The explicit `uid=`/`gid=` form is required, not plain `keep-id`: plain
keep-id maps the host user to its own uid, which only aligns with the image
when the host uid already happens to be 1000. That form ships in Podman
4.3; **older rootless Podman keeps the old refusal**
(`BYRE_ALLOW_ROOTLESS_PODMAN=1` still overrides, warned, on the host
identity), selected by a version probe (`SupportsKeepIDMapping`). An
unparsable version refuses rather than launching a run the engine would
reject.

## Consequences

- **Identity is a value, not an ambient fact.** `runner.Identity`
  (UID/GID/KeepID) is resolved once per session and threaded everywhere the
  old code read `os.Getuid()`: image tag (`byre-<id>-u1000-g1000` on this
  path -- the tag stays truthful, it names the ids baked in), build args,
  volume-seed chown targets, and the runtime `BYRE_UID`/`BYRE_GID`.
- **`BYRE_UID`/`BYRE_GID` mean the box's IN-CONTAINER dev identity**, which
  under keep-id is 1000, not the host uid. `byre shell` and deliver exec by
  those values, so they are correct by construction. Deliver's cross-user
  uid accident-guard would misread 1000 as foreign, so engines now report
  `CallerScoped`: rootless Podman's per-user storage means everything
  visible is the caller's, and the guard skips those engines instead of
  comparing ids.
- **Every helper that fills a box's volumes runs inside the box's mapping**
  (`--userns=keep-id:...` on seed/migrate one-shots and the sock-group gid
  probe): a chown target or a probed gid only means the same thing inside
  one userns. The netns-init helper (firewall) is different in kind: it
  needs privilege OVER the box's netns, and a namespace is owned by the
  userns that CREATED it -- an identical sibling mapping still gets EPERM
  from iptables. It therefore joins the box's own namespace
  (`--userns=container:<box>`); `byre ejectfirewall` mirrors this.
- **Volume NAMES are unchanged** (project volumes unqualified,
  machine volumes `byre-machine-u<host-uid>-`): they are host-side
  identifiers, and renaming them would orphan agent auth state (the ADR
  0008 lesson). Only chown targets moved to the identity.
- **forget/rehome candidate tags grow the generic tag per engine**, gated
  on that engine being rootless Podman -- on a shared ROOTFUL daemon the
  `u1000-g1000` tag may belong to a real uid-1000 user, and lifecycle
  commands must never capture it. Rootless storage is per-user, so the gate
  makes the capture impossible rather than unlikely.
- Podman re-chowns image/volume content per mapping ("user namespace not
  shared" copies or idmapped mounts); first keep-id run of an image pays a
  one-time layer-chown cost. Accepted.
- Switching one project between rootful and rootless Podman builds two
  distinct images (host tag vs generic tag) and two volume stores (system
  vs per-user) -- podman's own storage split, not byre's. Nothing migrates
  automatically; both sides clean up via forget on their own engine.
