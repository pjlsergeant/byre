# Egress closures reach past the cascade; open-denylist is a posture

Decided 2026-07-14. The `egress` key's `!host[:port]`
markers become **closures**: they survive the cascade and subtract from the
*derived* allowlist -- after skill egress unions in -- and a new builtin,
**firewall-open**, enforces them as drops on an otherwise-open network (the
`open-denylist` posture). A denial-visibility command (`byre denials`) was
designed and deliberately cut.

## The problem

Two gaps shared one root. First, "claude minus statsig" was unsayable: skill
egress unions into the allowlist *after* the cascade merges, so a config
`!statsig.anthropic.com` -- consumed at merge time like every other ADR 0018
marker -- could never reach a skill-declared entry. Second, the only
restrictive posture was the full deny-by-default wall; a user who just wants
an open dev box that refuses telemetry had no vehicle. Both need the same
substrate: closures that outlive the merge.

## Decision

**Closures survive the cascade.** `Merge` extracts egress `!` markers into a
non-TOML `EgressClosed` field instead of consuming them; the derived
allowlist (`resolvedEgress`) subtracts them LAST, after the skill union.
Everything else about the cascade is unchanged: precedence stays ordered (a
later layer's plain entry re-opens a closure, deleting every closure it
matches whole -- no partial narrowing), and within one layer a closure beats
a plain entry, mirroring `mergeStrings`.

**Portless closes every port.** `!host` drops every port and protocol;
`!host:port` drops that TCP/UDP port. This is deliberately asymmetric with
the open grammar (where portless means :443): addition is never greedy,
subtraction may be -- and there is otherwise no way to say "every port".
Matching is on the parsed grammar, not raw strings, owned by one parser
(`EgressClosureMatches`).

**Never invisible.** Status prints closures as `Closed:` rows under every
posture (inert-but-shown with none, matching ADR 0019's no-invisible-teeth
rule), and an allowlist entry a closure subtracts renders closed-by, not
vanished. The config UI matches on the same parser: a marker that closes a
skill endpoint is load-bearing, not "stale", and a skill row closed by this
file's own marker offers Restore.

**firewall-open, the enforcement sibling.** Same vehicle as the firewall
(netns_init helper, launch gate, zero grants to the box), opposite default:
policy stays ACCEPT and the closures become per-IP DROPs, passed as
`BYRE_EGRESS_DENY`. It declares `network_posture = "open-denylist"` -- core
vocabulary (`skills.PostureOpenDenylist`), so status can render the claim
honestly: `open-denylist (open network, N hosts blocked)`, with allowlist
rows getting the same suppressed/unenforced treatment as the open default.
No offered doors (there is no wall to open holes in). Mutual exclusion with
the firewall comes free from the single-posture rule.

**Fail closed, including resolution.** The posture is best-effort blocking
-- an IP snapshot aimed at well-behaved clients (telemetry SDKs); rotation
and determined processes can slip it, and the docs say so. But "best-effort"
is about evasion, never about byre shrugging: any helper failure kills the
launch, and an *unresolvable closure is fatal* -- under deny-by-default an
unresolved host stays safely blocked, here it would stay silently reachable
under an "N hosts blocked" claim. No safe direction exists, so the box does
not launch.

## Cut: `byre denials`

A counter-based denial view (per-host tables from comment-labeled iptables
rules, read post-hoc by a root+NET_ADMIN helper) was designed and rejected.
Under deny-by-default it could only ever show an aggregate (denials land on
the policy drop -- no per-destination rule exists to count against), and
recovering *names* requires sitting in the DNS path. The honest interim was
most of the machinery -- recorded-ID targeting so a later invocation can
prove box ownership, privileged reads behind passive commands -- for packet
counts with no names or timestamps, and it was scaffolding toward the
companion-service architecture byre deliberately doesn't run. Denial
visibility lands with the filtering-resolver sidecar (TODO, Maybe someday),
where names and timestamps come for free. Don't rebuild the counter tier.
