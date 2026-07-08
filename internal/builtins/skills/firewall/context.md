# Network: deny-by-default egress (byre firewall skill)

This box's outbound network is firewalled: only an allowlist of hosts is
reachable (agent APIs, github, common package registries), resolved to IPs
when the session started. Everything else is dropped — a connection that
hangs then times out is the wall, not a network outage.

- The rules live in the box's network namespace and were applied from
  outside; nothing inside the box can change them. Don't try.
- If a host you legitimately need is blocked, tell the user: they can extend
  the allowlist with the `egress` config key -- `byre config` → Egress
  (GRANTS), or `egress = ["host", "host:port"]` in `byre.config` (port
  defaults to 443) -- and restart the session. Allowed hosts are reachable ONLY on their listed
  port — `https://host` working while `ssh host` hangs is the port scoping,
  not a bug.
- DNS resolution works for all names (only connecting is restricted). A host
  whose IPs rotated mid-session (CDNs) may start failing; a session restart
  re-resolves the allowlist.
- To diagnose the wall, this box has `ping`, `traceroute`, `dig`/`nslookup`,
  `curl`, `telnet`, and `nc`: an allowlisted host answers, a blocked one
  hangs/times out. Use them to tell "the wall is blocking this" apart from
  "the service is down" before reporting a problem to the user.
