# Network: deny-by-default egress (byre firewall skill)

This box's outbound network is firewalled: only an allowlist of hosts is
reachable — by default just the agent's own API endpoints, plus whatever
doors the user has opened — resolved to IPs when the session started.
When byre launched this box (the normal path), the enforced allowlist is
appended at the end of this file under "This session's egress allowlist";
if that section is missing (the image was run outside byre), probe with
the diagnostics below instead.
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
- To diagnose the wall, probe a PORT, not a host: `curl -sS https://host`,
  `nc -vz host 443`, `telnet host 443`. The rules accept TCP to allowlisted
  (host, port) pairs only, so an allowlisted host answers on its listed port
  and hangs on every other one, and a blocked host hangs on all of them. Use
  that to tell "the wall is blocking this" apart from "the service is down"
  before reporting a problem to the user.
- `ping` and `traceroute` are NOT installed, and would not help: ICMP is not
  allowlistable here — the policy drops it to every destination, allowlisted
  or not — so both would time out identically for an open door and a shut
  one. `dig`/`nslookup` are installed, but answer a different question: DNS
  resolves for all names, so a name resolving proves nothing about whether
  you can connect to it.
