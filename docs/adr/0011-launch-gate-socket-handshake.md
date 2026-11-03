# Launch gate: loopback socket handshake, fail closed

> **Amended by ADR 0057** (2026-08-13): the credential delivery wait takes
> this gate's SHAPE -- bounded and FAIL-CLOSED -- sitting after the gate in the
> launcher. Credentials are blocking: no sentinel, a manifest line the
> launcher cannot honor, or a bare restart with credentials scheduled, and the
> agent never runs.

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
design exists to kill. With the socket, a restarted launcher poll-connects
afresh, no helper is listening, and the box times out closed.

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
- The gate is only load-bearing if its baked content is byre's: the
  launcher waits **only** when the gate file is non-empty (`[ -s ]`), so an
  emptied gate would make it skip the wait and launch open. The gate file
  (and the netns script, and the launcher itself) are therefore re-COPY'd
  by the **security guard** at the Dockerfile tail, after the project
  block, so a project `files` entry targeting those paths can't leave its
  own content there (see ARCHITECTURE, "security guard").
- **The guard restores what project `files` entries clobber; it does not
  cover raw build lines.** A raw line need not write the guarded path to
  beat the re-COPY, it can re-point it
  (`RUN rm -f /etc/byre/launch-gate && ln -s /dev/null /etc/byre/launch-gate`),
  and `COPY` then writes THROUGH the destination symlink -- the gate stays
  a symlink to `/dev/null`, is empty, and the launcher's `[ -s ]` test
  skips the wait (measured 2026-07-28, docker/buildkit). The raw tier is
  covered the way P3 says it is -- byre never parses inside a raw block, it
  degrades every posture claim a raw block could undermine (`networkLine`'s
  BuildRaw arm) -- so the disclosure holds either way. Pinned by
  TestIntegrationRawLineRepointingTheGateIsDisclosed.
