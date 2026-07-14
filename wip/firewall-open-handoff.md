# Handoff: default-open-firewall branch (2026-07-14)

Open items smuggled out of the worktree (its `.byre-devlog/` dies with it).
DELETE this file when both are resolved (wip/ convention — git history keeps
it; the settled decisions already live in docs/adr/0030 and TODO.md).

## 1. OPEN QUESTION (Pete's call): port the v6 fail-closed fix to firewall.sh

Codereview on this branch found (and firewall-open.sh now fixes) this hole:
closures resolving to IPv6 addresses stay silently reachable when ip6tables
is unavailable but the netns has real (non-loopback) v6 interfaces. The
shipped deny-by-default `firewall.sh` has the SAME skip with worse stakes:
ip6tables unavailable + real v6 stack = the ENTIRE v6 side stays policy-
ACCEPT (open) under a deny-by-default claim. It rests on the documented
"ip6tables failing ≈ no v6 stack ≈ nothing to leak" assumption.

The fix is the same three lines firewall-open.sh uses (see its `ip6_ok`
block): before skipping, `grep -qsv ' lo$' /proc/net/if_inet6` — non-lo v6
interfaces present + ip6tables broken => die, gate stays shut. The common
v6-less Docker bridge still skips safely (file absent or lo-only).

Held per discuss-first doctrine: it changes shipped flagship launch
behavior. If approved, mirror the firewall-open.sh block + add a note to
the "Known holes" comment in firewall.sh's header.

## 2. Pre-merge: host-side gated integration run

The branch adds three firewall-open cases to the gated suite
(`BYRE_DOCKER_TESTS=1 go test ./internal/commands/ -run Integration -v`):
open-network-reachable-without-grant, closed-host-dropped, and
unresolvable-closure-fails-closed. Written agent-side, never run against a
live engine (no daemon in the box). Run host-side (or let the CI
integration job cover it) before/with the merge to main.
