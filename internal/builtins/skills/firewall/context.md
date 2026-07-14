# Network: deny-by-default egress (byre firewall skill)

This box's outbound network is firewalled: only an allowlist of hosts is
reachable — by default just the agent's own API endpoints, plus whatever
doors the user has opened — resolved to IPs when the session started.
Everything else is dropped — a connection that hangs then times out is
the wall, not a network outage.

- The rules live in the box's network namespace and were applied from
  outside; nothing inside the box can change them. Don't try.
- The wall opens ONLY the agent's own API endpoints by default. Common
  doors -- git hosting, apt, language registries -- are offered-but-closed:
  the user opens each in `byre config` → Egress (one press per door). So if
  git/apt/package installs hang, that is expected on a fresh firewalled box,
  not a bug: tell the user which host you need and point them at the Egress
  screen (or `egress = ["host", "host:port"]` in `byre.config`, port
  defaulting to 443), then have them restart the session. Allowed hosts are reachable ONLY on their listed
  port — `https://host` working while `ssh host` hangs is the port scoping,
  not a bug.
- DNS resolution works for all names (only connecting is restricted). But
  connecting is allowed per-IP, snapshotted at launch: a host whose DNS
  answer has rotated since (CDNs; some cloud resolvers rotate on every
  query) starts failing even though it is allowlisted — closed, never open,
  and on a per-query resolver possibly seconds after launch. A session
  restart re-resolves. If an allowlisted host flaps or times out, report
  THIS as the likely cause rather than a network outage.
- To diagnose the wall, this box has `ping`, `traceroute`, `dig`/`nslookup`,
  `curl`, `telnet`, and `nc`: an allowlisted host answers, a blocked one
  hangs/times out. Use them to tell "the wall is blocking this" apart from
  "the service is down" before reporting a problem to the user.
