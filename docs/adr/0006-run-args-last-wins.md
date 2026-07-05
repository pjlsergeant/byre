# run_args is last-wins, identity label excepted

byre builds its own `docker run` flags first and appends the raw
`run_args` block last, so a raw flag can override byre's own (`--user`,
`--network`, even `--rm`). That is the point of the escape hatch, per
PRINCIPLES.md #1 and #3 -- the risk is the author's. The single exception:
byre re-asserts the `byre.project`/`byre.workdir` identity labels *after*
`run_args`, because lifecycle and `byre status` must always find the
session (ADR 0004).

Consequences: raw `run_args` can undermine any protective posture
(`--network host`, `--cap-add`, `--entrypoint`, ...), so their presence
degrades status claims (see ADR 0010's honesty rules) -- byre reports,
it never refuses.
