# Launch gate: loopback socket handshake, fail closed

When a network-posture skill is enabled, the launcher waits **at its very
top** -- before agent-context placement and before first-run hooks, which
are skill code that does network I/O (codex device-auth, literally) --
until the firewall helper signals ready. The signal is a **loopback
socket handshake**: the helper listens on `lo` once its rules verify; the
launcher poll-connects. Timeout (~30s) means the box exits without ever
running its command: it dies offline, never launches open.

Why a socket and not a marker file: the handshake must have **no
persistent state**. `/run` is NOT tmpfs in Docker containers by default,
so a marker file would survive `docker restart` into a freshly-recreated,
rule-less netns and silently fail **open** -- the classic trap this
design exists to kill. With the socket, a restarted launcher listens
afresh, nobody connects, and the box dies closed.

Consequences:

- Direction is launcher-connects/helper-listens (bash can be a `/dev/tcp`
  client but not a listener, and the launcher must stay dependency-free
  on arbitrary bases; the helper side ships `nc` via the skill). In the
  pre-gate window only byre's own launcher exists in the netns, so
  nothing else can open the gate.
- The chassis strips inherited `HEALTHCHECK`s (`HEALTHCHECK NONE`): the
  engine runs health probes in the netns independently of the
  ENTRYPOINT, so a base image's probe could do network I/O before the
  rules land.
- The startup window between container start and rules landing is benign:
  the gate sits above hooks and agent alike -- only the idling launcher
  has run.
