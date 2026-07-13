# docker-host skill -- design of record (rev 4, BUILT)

Status: design settled with Pete via /grilling 2026-07-13; revised through two
rounds of independent design review (codex + grok), Desktop + Linux
host-verified, then BUILT (grok single-shot on branch docker-host-skill) and
build-reviewed (codex + claude). Build-review dispositions are at the very end.
This document is the design of record. Reviewers: challenge the decisions on
their merits; nothing here is sacred except where a standing principle or ADR
is cited.

Working-doc lifecycle (deliver precedent): absorbed into an ADR +
docs/docker-host.md when built, then deleted; git history is the archive.

## What it is

`docker-host` is a builtin skill granting the box access to the **host's
Docker daemon** via its socket. It is the ergonomic form of a grant already
expressible by hand (mount + CLI + group); the skill's value is bundling it
with honest, loud legibility.

Explicitly NOT: docker-in-docker (no nested daemon), not a Podman variant
(a Podman host would want a sibling `podman-host` skill with its own
verified facts, out of scope), not the service-sidecars feature (which
covers the compose-deps case WITHOUT this grant; see TODO).

## The warranty model (governs D5, settled round 1)

Pete's ruling: **byre warrants its own construction, never the consequences
of your hole.**

byre's isolation is not one thing; it is FIVE independent walls:

| Wall | What it isolates |
| --- | --- |
| Filesystem | box sees /workspace + /home/dev volumes + granted mounts only |
| Network | deny-by-default egress (firewall) or open |
| Privilege | unprivileged dev, no runtime root, caps only if a skill grants |
| Cross-project | a project's boxes see only their own state volumes; machine-scoped shared-auth is the deliberate exception (ADR 0025) |
| Host control | no host process/daemon control |

The docker socket breaches ALL FIVE at once, and does so INVISIBLY --
through the daemon, not through any surface byre renders:
- Filesystem: `docker run -v /:/host` -> full host FS (exposure line still
  says "1 host mount").
- Network: `docker run --network host`, or daemon-side pulls -> full egress
  (Network row still says deny-by-default). A sibling container with
  `--network container:<box>` + `--cap-add NET_ADMIN` can even flush the
  box's own firewall rules from inside its netns (codex round 1).
- Privilege: containers run as root; `-v /:/host` + chroot = host root
  (ADR 0008 "unprivileged" is true in the box, false for the host).
- Cross-project: `docker volume ls` + mount `byre-machine-u<uid>-*` reads
  another project's shared-auth token WITHOUT touching that box (grok+codex
  round 1) -- a quieter path than `/:/host`, and it defeats ADR 0025's
  per-box scoping machine-wide.
- Host control: exec into any box, drive the daemon. The grant itself.

So docker-host is the MAXIMAL grant -- the one thing that falsifies every
per-wall claim simultaneously.

The honesty response is NOT to qualify each per-wall row (arbitrary: why
degrade the Network row and not the filesystem exposure line?). Every row
keeps describing WHAT BYRE BUILT -- and byre really did build them: the
netns rules are applied, the mounts are what they are, the rows still hold
FOR THE BOX. A separate Containment line describes THE HOLE and voids the
warranty for anything done through it:

> byre warrants its own construction, never the consequences of your hole.
> The guarantees still describe the box and hold for it; byre makes no
> warranty for anything done through this hole.

This covers all five walls in one sentence by construction, including
attacks not yet enumerated (the sibling-container firewall flush is "done
through the hole" -- already disclaimed). No per-row qualifier creep, no
arbitrary patching of one wall.

ROUND-2 CHALLENGE, CONSCIOUSLY DECLINED (Pete, both reviewers): byre already
degrades the Network row for raw `run_args` ("deny-by-default (declared; raw
run_args present -- not guaranteed)"), so a box with a harmless stray flag
reads as LESS locked-down than a docker-host box whose Network row stays
clean -- an inconsistency the reviewers wanted closed by degrading Network
for containment skills too. DECLINED, because the two cases are different
epistemics, not the same rule applied unevenly:
- Raw run_args degrade the claim because byre CANNOT SEE what they do -- it
  declines to vouch for its OWN construction under an unauditable layer
  ("wall's up, but someone stapled argv I can't read to the launch").
- docker-host is the opposite: byre's construction IS intact and byre CAN
  see exactly what the skill does. The firewall rules are applied and
  correct; there is a DECLARED DOOR beside a sound wall.
Degrading the Network row for docker-host would collapse that distinction
and make byre introspect a skill to re-score an unrelated claim -- exactly
the "policing skills" role byre refuses (deny-by-default doctrine: byre is
not complicit in pretending it audits skill behavior). The 🛑 HOLE line does
the honest work: the wall is real, the escape is real, both are visible. The
Network row keeps describing what byre built, which is TRUE. Codex's stronger
"say the guarantees below are bypassable" is also declined for the same
reason -- it invites reading the disclaimer as an admission byre policed the
skill and found it wanting, rather than the skill declaring its own hole.

## Decisions

### D1. Name: `docker-host` (held, round 1)

"Access to the host's Docker socket." Rejected: anything DinD-flavored
(misdescribes the mechanism where precision matters most); `docker-on-docker`
(same). GLOSSARY gains the entry.

### D2. CLI install: docker-ce-cli + plugins, from Docker's apt repo (held)

`[build]` installs `docker-ce-cli`, `docker-compose-plugin`,
`docker-buildx-plugin` from Docker's official apt repository (~4 Dockerfile
lines: keyring fetch, sources list with ID/VERSION_CODENAME read from
/etc/os-release, apt-get update + install). Works across Debian- and
Ubuntu-family bases (default base debian:bookworm; templates ride
Debian-family images).

Rejected: `docker.io` (Debian archive) pulls the whole engine -- containerd,
runc -- and plants a daemon in a box whose point is no daemon; `docker-cli`
(split package) absent from bookworm; static tgz needs hand-pinned version +
arch mapping + no plugin story. Compose/buildx are client-side plugins on
the same socket: capability identical with or without them, so bundling is
ergonomics. Build-time fetch from download.docker.com is the same class as
codex's vendor-installer curl.

### D3. Socket access: runner-side numeric `--group-add` (REVISED round 1)

ROUND-1 KILL: the original design had the launcher grant dev group
membership "while still root at PID 1, before dropping to dev." That root
phase does not exist -- launcher.sh:4 is explicit ("no root phase and no
gosu drop"; PID 1 is already dev), ADR 0008 bakes USER dev with gosu
build-only. Both reviewers flagged this as build-blocking. The mechanism was
at the wrong layer.

REVISED mechanism (grok's fix, Pete-accepted): keep the skill.toml key
`[runtime] sock_groups = ["/var/run/docker.sock"]`, but ACT ON IT IN THE
RUNNER at `docker create` time -- inject numeric `--group-add <gid>` where
caps and mounts already land. A numeric gid needs no `/etc/group` entry in
the container, root stays OUTSIDE the box (the netns-helper pattern), and
the launcher is untouched. Generic, opinion-free core mechanism ("make this
path's owning group reachable to dev"); any future socket-ish grant reuses
it.

`sock_groups` IS ITSELF A GRANT (round-2 codex): `--group-add <gid>` grants
dev access to EVERY inode carrying that gid in the box, not just the named
socket -- so it must be rendered as an attributed grant in status/adoption
(a `Grant.SockGroups`-style field beside Mounts/Caps/RunArgs), showing the
declared PATH and that the access is wider than that path. The numeric gid is
deliberately NOT surfaced (build-review disposition, finding 4): status and
adoption run before any probe, so the number isn't even known there, and
"gid 0 vs 989" changes no user decision -- the collateral MEANING ("wider than
the named path") is the honest legibility; the digits are debug detail carried
only on the probe's failure path. For docker-host the daemon grant dominates
it, but the mechanism is generic (a future `pcscd`/lower-power skill's
collateral group access matters). Each entry must resolve to an active bind
target; a discovery failure is attributed, never silently skipped.

Gid discovery (REVISED round 2 -- both reviewers: host-OS classification is
the wrong boundary):
- The discriminator is NOT Linux-vs-macOS. Docker Desktop FOR LINUX also runs
  the engine in a VM with a per-user socket, and remote Docker contexts have
  the same split -- so a host-side `stat` can report a gid the in-container
  socket does not carry, on Linux too.
- Discovery is therefore ENGINE-SIDE for every case: a one-shot probe
  container with the same bind runs `stat -c %g` on the target and reports
  the gid the box will actually see; byre injects that. Uses the box's own
  just-built image; no host-OS branching. A probe failure is attributed, not
  silently defaulted.
- HOST-VERIFIED 2026-07-13 on Pete's Mac (Docker Desktop 4.37.2, engine
  27.4.0, apple silicon). Results, gate CLEARED:
  - The probe (`docker run -v /var/run/docker.sock:/var/run/docker.sock
    alpine stat -c %g <sock>`) returned gid **0** -- Desktop's VM serves the
    daemon socket root-OWNED, so the presented group is root (0).
  - `docker run --user 501:501 --group-add 0 -v <sock>:<sock> docker:cli
    docker version` printed a full Server: section -> access granted.
  - Negative control (`--user 501:501`, NO group-add) -> "permission denied"
    -> plain non-root access is closed; the group-add is what opens it.
  - So on Desktop byre injects `--group-add 0`; on native Linux the probe
    will instead return the `docker` group gid (e.g. 999) with the socket
    0660 root:docker. Different value, same mechanism -- vindicates the
    engine-side probe over any hardcoded/host-classified gid.
  - COLLATERAL NOTE: gid 0 means dev joins GROUP 0 (root group) in the box,
    gaining group-read to root-group files there. Benign (the daemon grant is
    already total), but it makes the "sock_groups is itself a grant" rendering
    (above) concrete -- on Desktop the collateral group IS group 0; say so.
  - NATIVE LINUX ALSO VERIFIED 2026-07-13 (Pete, Debian trixie, native
    dockerd, engine 28.5.1, amd64; context `default` ->
    unix:///var/run/docker.sock): probe gid = **989** (the docker group,
    NON-zero); `--user 1000:1000 --group-add 989` -> full Server access;
    negative control (no group-add) -> permission denied. Collateral is
    NARROW here (dev joins only the docker group, not group 0 as on Desktop).
  - CROSS-PLATFORM PAYOFF: the SAME probe returned 0 (Desktop) and 989
    (native Linux) -- two different gids, one mechanism. This is the concrete
    proof that engine-side probing beats any hardcoded gid or host-OS
    branch. Gate CLEARED on both platforms; D3 is sound as designed.

### D4. Missing socket = attributed WARNING, not refusal (REVISED round 1)

ROUND-1 CORRECTION: the original hard-refuse was justified by "Docker
creates a root-owned dir at a missing bind source and bricks the socket
path." FALSE for byre: runargs.go:84 uses `--mount type=bind`, which ERRORS
on a missing source (the create-a-dir behavior is `-v`, which byre does not
use). The engine already fails closed. Also, on Docker Desktop a host-side
`os.Stat` is not authoritative -- bind sources resolve inside the VM, where
the socket exists whenever Desktop runs even if the mac-side path is absent;
a host stat could wrongly refuse a launch that would work.

HOST-CONFIRMED 2026-07-13: on Pete's Mac `ls -l /var/run/docker.sock` -> "No
such file" (active context is `desktop-linux` -> ~/.docker/run/docker.sock;
the "allow default socket" setting is off), YET
`-v /var/run/docker.sock:/var/run/docker.sock` mounted a live socket (reached
"permission denied", not "cannot connect"). So (a) a host-stat refusal WOULD
have wrongly blocked a working launch -- the warning-not-refusal call is
vindicated; and (b) `/var/run/docker.sock` is the correct mount SOURCE on
Desktop (the VM serves the canonical path) -- no need to special-case
~/.docker/run/docker.sock as the source.

REVISED (Pete-accepted): byre stats the source and, if missing or not a
socket, prints an ATTRIBUTED WARNING naming the skill and likely cause
("Docker not running? Podman-only host?") but STILL ATTEMPTS THE LAUNCH.
The engine remains the authority (missing source -> create error -> box
doesn't start; fail-closed preserved). byre's job shrinks to attribution,
which is the footgun doctrine's actual lane -- legibility over the engine's
opaque error, never a gate. Codex's socket-TYPE check (source exists but is
a dir/file) folds into the same warning.

DESKTOP FALSE-NEGATIVE (round-2, both): host `stat` is NOT authoritative
under Docker Desktop -- the bind resolves in the VM, so the mac-side path can
be absent/not-a-socket while the launch works. A naive warning would fire on
every successful Desktop launch and train users to ignore it. Mitigation:
suppress/soften the warning when the engine is Docker Desktop (detectable via
`docker context`/`docker info`), or only surface it AFTER a create failure so
the message attributes a launch that actually failed. Do not treat host
`stat` as truth on Desktop.

### D5. Warnings: declarative `containment` key, skills-only (REVISED render)

New `[runtime] containment = "<skill-owned one-liner>"`. Purely declarative,
mirroring `network_posture`: the skill declares the hole; byre prints it,
attributed, never inspects or enforces. Skills-only (no project-config
equivalent) until a second consumer; proportionality.

MULTI-DECLARER RULE (grok round 1): unlike `network_posture` (single
declarer, regex-shaped), `containment` may be declared by several skills
(docker-host + a future podman-host). Render ALL, each attributed -- never
last-wins, never silently drop one.

OUTPUT-SAFETY VALIDATION (round-2 codex): `containment` is arbitrary
skill-owned text rendered on four surfaces; an unvalidated newline or control
sequence (`containment = "hole\nNetwork: deny-by-default"`) could FORGE
adjacent rows. Skills are trusted code, but typed fields are still validated
so they stay legible DATA (that is why `network_posture` is regex-bounded).
Validate each declarer at load: single line, no control chars, bounded
length; render each as its own attributed record.

Rendering surfaces (all rendering, no gates -- the adoption prompt is the
consent gate):
- `byre status`: a Containment row beside Network -- but framed as the
  warranty disclaimer (per the warranty model above), not a degraded row.
  Every other row stays unqualified.
- Launch: a LOUD line matching self-edit's `🛑` weight (round-1 point, both
  reviewers: docker-host is a STANDING, host-wide, root-equivalent grant --
  strictly worse than self-edit's one-session store mount -- so its signal
  must be at LEAST as loud; my "worth a skim" draft was wrong).
- Adoption warning summary: a NEW top-sorted warning class, above machine
  volumes, when an incoming config enables a containment-declaring skill.
- Config UI: same line in the skills/GRANTS view.

The Network posture claim is NOT surgically degraded (round-1 reviewers
proposed this; Pete's warranty model supersedes it -- the row describes what
byre built and still holds for the box; the hole is disclaimed once, wider
than any single wall).

Wording (skill-owned strings; tunable at review):
- Adoption/config UI: "docker-host opens a containment hole -- it can drive
  the host's Docker daemon. The guarantees below still describe the box;
  byre makes no warranty for what's done through this hole. At least skim
  docs/docker-host.md before enabling."
- Status: "🛑 HOLE -- docker-host can drive the host's Docker daemon; no
  warranty for what's done through it  (skill: docker-host -- skim
  docs/docker-host.md)"
- Launch: "byre: 🛑 containment hole: docker-host can drive the host's
  Docker daemon -- no warranty for what's done through it. docs/docker-host.md"

"Root-equivalent" stays OUT of the one-liners (accurate but uninformative
without the reasoning chain, which is the doc's job); the one-liner is a
loud signpost.

### D6. The discussion doc: standalone docs/docker-host.md (held + expanded)

User-facing, born with the feature (deliver.md precedent; future site page).
Linked from the skill.toml header, the containment strings, and a pointer
section in SECURITY.md. Contents:
1. What the grant is, plainly: socket access is root-equivalent on the host
   (`docker run -v /:/host` is a two-line takeover) -- the grant working as
   designed, not a flaw. PLATFORM NUANCE (round-2 codex): this is exact on
   native Linux. On Docker Desktop (mac, and Linux Desktop) the daemon and
   container root live in a VM, so `-v /:/host` exposes the VM's `/`, not the
   host `/`; native host files are reachable only through Desktop's configured
   file sharing and keep native permissions. Still a full compromise of Docker
   state, every byre volume, and shared native paths -- but the doc must say
   "VM root + Docker assets + shared paths" for Desktop, not "your whole Mac."
2. What survives vs what's gone: accident-scale isolation of the BOX'S OWN
   filesystem intact; containment of host state and other boxes GONE (see
   the honesty note below -- do not oversell "accidents stay in the box").
3. Firewall interaction: daemon-side actions (pulls, --network host) ride
   the HOST's network; the box's egress rules govern only the box's netns;
   a sibling container can flush those rules.
4. CROSS-BOX / SHARED-AUTH (round-1 M1, first-class): `docker volume ls` +
   mount `byre-machine-u<uid>-*` reads other projects' state and every
   shared-auth token on the machine WITHOUT touching those boxes. This
   weakens ADR 0025's per-box scoping machine-wide -- say so.
5. Host state that outlives the box: images/containers/volumes the agent
   creates; byre's teardown knows nothing of them.
6. When to grant it vs the alternative: service sidecars (TODO, L) cover
   compose-deps WITHOUT this grant; docker-host is for when the agent's job
   IS containers.

HONESTY NOTE (round-1 M/high, both reviewers): the round-1 phrase
"a confused agent still trashes only the box" is FALSE -- ordinary Docker
mistakes (`docker compose down -v`, addressing a container by a plausible
name, an unintended bind, removing a volume mid-cleanup) hit host state and
other boxes with no adversary. The doc must frame accident-scale isolation
as "the box's own filesystem," not "everything you do."

### D7. Agent-facing context.md (held + hardened)

The skill ships a `[context]` snippet (firewall precedent) against the
ACCIDENT class:
- You drive the HOST's Docker daemon, not a private one; what you create is
  host state that outlives this box; clean up what you create.
- Containers AND VOLUMES visible to you -- including byre boxes (one is YOU)
  and other projects' state/identity volumes -- belong to the user. Do not
  stop/remove/prune anything you didn't create, and DO NOT MOUNT foreign
  volumes (round-1 M1: mounting `byre-machine-*` or another project's state
  volume is off-limits, same weight as prune -- it exfiltrates credentials).
  `docker system prune` is off-limits.
- Compose: your project name is preset (COMPOSE_PROJECT_NAME, see D-M2);
  don't override it -- unrelated byre boxes share the daemon and a bare name
  collides.
- Label what you create so it's attributable and cleanable.
- The box's network rules do not apply to containers you launch; security
  discussion: docs/docker-host.md.

### D8. Egress: none (held)

The CLI speaks to a unix socket; the skill declares `egress = []` and opens
zero network doors. Daemon-side network activity is the host's, covered by
the warranty model, not by egress declarations.

### D-M2. Compose project-name collision (NEW, round-1 codex)

Every box's cwd is /workspace, so `docker compose` derives the same default
project name for every byre box on the machine -- two worktrees or projects
collide on host containers/networks/volumes; one box's `compose down` tears
down another's stack. Ordinary accident class.

ROUND-2 CORRECTION (both reviewers, verified in project.go:134/145): keying
compose on the PROJECT id is WRONG -- `Paths.ID` is SHARED across worktrees
(config/volumes/image identity, ADR 0009); only `Paths.WorktreeID` is
per-worktree. Two sibling worktrees would get the same COMPOSE_PROJECT_NAME
and one's `compose down` still tears down the other's stack -- the exact race
D-M2 targets. Compose must key on WorktreeID.

Fix (Pete-accepted): a small generic core addition + a skill hook.
- Core passes TWO env vars into every box, same class as BYRE_UID (plumbing,
  not grants; generally-useful legibility): `BYRE_PROJECT=<Paths.ID>` (the
  shared project identity) and `BYRE_WORKTREE=<Paths.WorktreeID>` (the
  per-worktree id; equals ID for a plain project).
- docker-host ships an env.d launch hook (claude-shared-auth precedent):
  `export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-byre-$BYRE_WORKTREE}"`
  -- stable per project for a plain checkout, DISTINCT per worktree, user
  override respected. Keyed on WorktreeID, NOT the container hostname
  (= container id, changes every rebuild, would orphan stacks).
- context.md mentions it (D7) so the agent doesn't "fix" it away.

## Verification plan

- Design review round 2 (this rev) by codex + grok BEFORE building; new big
  findings re-grilled with Pete.
- HOST-VERIFY (Pete), gating the build:
  - Docker Desktop/macOS: DONE 2026-07-13 (see D3) -- probe gid = 0,
    `--group-add 0` grants access, negative control denied, `/var/run/
    docker.sock` is a valid Desktop mount source. Gate CLEARED.
  - sock_groups `--group-add` mechanism on native Linux: DONE 2026-07-13
    (see D3) -- probe gid = 989, `--group-add 989` grants access, negative
    control denied. Gate CLEARED on both platforms.
  - docker / compose / buildx function in-box: at build time.
- Unit: skill.toml parse (sock_groups, containment incl. single-line/control
  -char/length validation); runner `--group-add` injection + gid-probe seam;
  sock_groups grant rendering in status/adoption; missing/not-a-socket source
  warning incl. Desktop suppression; containment rendering on all four
  surfaces incl. multi-declarer; BYRE_PROJECT + BYRE_WORKTREE plumbing +
  COMPOSE_PROJECT_NAME hook (worktree distinctness pinned); golden Dockerfile
  output for the apt-repo lines.

## Round-1 review dispositions (codex + grok, 2026-07-13)

Both reviewers agreed to a striking degree; both called finding 1 build-blocking.

- HIGH D3 (both): launcher has no root phase -> mechanism impossible.
  ACCEPTED -> D3 revised to runner-side numeric `--group-add`.
- HIGH D5 network honesty (both): unqualified deny-by-default is false under
  docker-host. RESOLVED DIFFERENTLY -> Pete's warranty model (rows describe
  the box and hold; the hole is disclaimed once, wider than any wall). Loud
  `🛑` launch/status signal accepted.
- HIGH/M D6 "accidents stay in the box" is false (both): ACCEPTED -> D6
  honesty note; D7 hardened.
- HIGH D7/M2 Compose collision (codex): ACCEPTED -> D-M2 (BYRE_PROJECT +
  COMPOSE_PROJECT_NAME hook).
- MED D4 stale-dir / existence check insufficient + `-v` premise false
  (both): ACCEPTED -> D4 revised to attributed warning, engine is authority,
  socket-type check folded in.
- MED D3/D4/D6 Docker Desktop socket PATH (not just gid) (both): ACCEPTED ->
  called out as the primary host-verify gate; D4 warning-not-refusal avoids
  wrongly blocking Desktop.
- MED D5/D6/D7 shared-auth volume exfil under-specified (both): ACCEPTED ->
  first-class in D6 (item 4) and D7 (no foreign volume mounts).
- MED D5 raw run_args can breach containment but can't declare it (codex):
  NOTED -- consistent with existing doctrine (raw blocks degrade claims byre
  can't stand behind; that's a status.go rule, not this skill's job). The
  warranty model's disclaimer is skill-declared; raw-block honesty stays
  where PRINCIPLES puts it. Flag for the ADR, not a blocker.
- MED D4 refusal vs footgun (codex): MOOTED by D4 becoming a warning.
- LOW D5 multi-declarer merge (grok): ACCEPTED -> D5 render-all rule.

Held with no change: D1, D2, D8, and the use of context.md (D7 shape).

## Round-2 review dispositions (codex + grok, 2026-07-13, same day)

Both confirmed the round-1 blockers resolved (launcher-root, `-v` premise,
accident wording, shared-auth disclosure, multi-declarer composition).
Round-2 findings:

- HIGH D-M2 (both; verified project.go:134/145): BYRE_PROJECT does not
  distinguish worktrees -- `Paths.ID` is shared, only `WorktreeID` differs;
  same-project worktrees still collided. ACCEPTED -> compose keys on
  `BYRE_WORKTREE` (= WorktreeID); BYRE_PROJECT kept as plumbing.
- HIGH D5 warranty model (codex "redefines status away from its contract";
  grok "raw run_args asymmetry -- a stray flag reads less trusted than a
  known hole"): CONSCIOUSLY DECLINED by Pete; rationale recorded in the
  warranty-model section. The two cases are different epistemics: raw
  run_args = byre can't see what they do (declines to vouch for its OWN
  construction under an unauditable layer); docker-host = construction
  intact, declared door beside a sound wall. Degrading Network for a skill
  would mean introspecting skills to re-score unrelated claims -- the
  "policing skills" role byre refuses. The 🛑 HOLE line carries the signal.
  DO NOT RE-RAISE without new facts.
- MED D3/D4 gid discovery boundary (both): host-OS classification wrong
  (Docker Desktop for Linux + remote contexts also split host/VM). ACCEPTED
  -> engine-side probe for every case; no OS branching.
- MED D6 Desktop root-equivalence overstated (codex): `-v /:/host` on
  Desktop exposes the VM's /, not the native /. ACCEPTED -> platform nuance
  in D6 item 1.
- MED D5 containment string output-safety (codex): unvalidated text could
  forge status rows. ACCEPTED -> single-line/control-char/length validation
  at load.
- MED D3 sock_groups is itself a grant wider than the socket (codex):
  ACCEPTED -> rendered as an attributed grant with path + derived gid.
- LOW D4 Desktop false-negative warning (grok): ACCEPTED -> suppress/soften
  on Desktop or warn only after create failure.

## Build-review dispositions (grok single-shot build; codex + claude review, /grilling)

grok built the whole skill in one pass on branch docker-host-skill; suite green,
faithful to the design. codex's build review found 5 edges (none architectural);
all five /grilled with Pete:

- HIGH: `COMPOSE_PROJECT_NAME` (and any env.d export) never reaches `byre shell`
  -- env.d is sourced only by the launcher; `byre shell` does `docker exec`
  bypassing it. A pre-existing byre-wide gap docker-host exposed. FIXED, and the
  fix uncovered a deeper wart: `claude-shared-auth/env.sh` (a "sourced" env
  hook) smuggled in an interactive `read` + credential-file `mv`, so blanket-
  sourcing env.d into every login shell would re-fire a prompt. Resolution
  (Pete-ruled): (1) env.d hooks are PURE env-setters by contract; the
  claude-shared-auth remediation moved to its firstrun.sh (executed every
  launch, the right home for a command); (2) a baked `/etc/profile.d/byre-env.sh`
  shim sources env.d for login shells; (3) `byre shell` passes the container's
  `BYRE_*` plumbing through the exec so the shim's hooks have their inputs. The
  launcher keeps its own guarded sourcing (belt-and-suspenders); no shared
  snippet needed once hooks are pure.
- MED: missing-socket warning false-fires on remote Docker contexts. WON'T-FIX
  -- the false warning needs byre's OWN engine pointed at a remote/Desktop
  context; native + Desktop (already suppressed) are correct. The agent driving
  a remote daemon via its own context is downstream of the grant and doesn't
  touch byre's stat; one-line context.md note that it can.
- MED: containment validation missed Unicode C1 controls (U+0085, U+009B).
  FIXED -- swapped the hand-rolled ASCII check to `unicode.IsControl` (simpler +
  complete). Reframed as a footgun guard for the skill AUTHOR, not a security
  boundary (skills are trusted code).
- MED: probed gid computed then discarded; nothing renders the number.
  WON'T-FIX -- status/adopt run before any probe (can't know it), and 0-vs-989
  changes no user decision; the grant line's "wider than the named path"
  already carries the meaning. The digits are debug detail (D3 reconciled).
- LOW: the docker-host Dockerfile "test" was a tautological substring grep of
  the skill's own text. FIXED -- replaced with a real gen golden that pins the
  apt-repo RUN's ordering/continuations and the env.d COPY placement.

## Related decisions banked during the same grilling (separate TODOs)

- `!host` egress closures (S): subtract from the derived allowlist under
  deny-by-default, INCLUDING skill-declared entries.
- Open-denylist firewall mode (M): otherwise-open network with `!host`
  closures enforced; best-effort IP-snapshot blocking (telemetry), worded so.
- Observed-open ("logging") firewall: raised and WITHDRAWN -- deny-by-default
  already fails loudly and legibly; observation is weaker legibility.
