# Network: open, with a denylist (byre firewall-open skill)

This box's outbound network is OPEN — except for a short list of hosts the
user has deliberately blocked (`!host` entries in the byre config). Their IPs
were resolved when the session started and every packet to them is dropped: a
connection to one that hangs and times out is the block working, not an
outage.

- The drops live in the box's network namespace and were applied from
  outside; nothing inside the box can change them. `byre status` on the host
  lists the blocked hosts (the `Closed:` rows).
- The blocked hosts are the user's explicit choice — typically telemetry or
  metrics endpoints. Treat a blocked destination as "the user said no": work
  without it rather than looking for another route to the same service.
- A blocked host still RESOLVES (DNS is open; only packets to the resolved
  IPs are dropped). A tool that hangs on startup may be phoning one of the
  blocked hosts — let its timeout run or disable its telemetry knob rather
  than treating the hang as a broken network.
- Everything else is reachable normally: installs, git, arbitrary APIs. If a
  NON-blocked host misbehaves, that's a real network problem, not this skill.
- To tell blocked from down, this box has `ping`, `traceroute`,
  `dig`/`nslookup`, `curl`, `telnet`, and `nc`. Cross-check the host against
  the user's blocked list before reporting a problem.
