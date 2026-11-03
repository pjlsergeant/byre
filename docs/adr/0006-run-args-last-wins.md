# run_args is last-wins, byre's own labels excepted

byre builds its own `docker run` flags first and appends the raw
`run_args` block last, so a raw flag can override byre's own (`--user`,
`--network`, even `--rm`). That is the point of the raw block, per
PRINCIPLES.md #1 and #3 -- the risk is the author's. The single exception
is byre's own label set: **every label byre puts on `RunParams.Labels` is
re-asserted after `run_args`, and no raw `--label` can override one.** The
rule names the mechanism, not the members, because the set grows. Those
labels carry session identity, so lifecycle and `byre status` always find
the session (ADR 0004); the per-invocation ownership proof that keeps a
forged container from capturing a root + NET_ADMIN netns-init helper
(ADR 0010); and the launch record's content address, so `byre status`
cannot be pointed at a record of the author's choosing (ADR 0053).

Consequences: raw `run_args` can undermine any protective posture
(`--network host`, `--cap-add`, `--entrypoint`, ...), so their presence
degrades status claims (see ADR 0010's honesty rules) -- byre reports,
it never refuses.
