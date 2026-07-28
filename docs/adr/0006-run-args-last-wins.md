# run_args is last-wins, byre's own labels excepted

byre builds its own `docker run` flags first and appends the raw
`run_args` block last, so a raw flag can override byre's own (`--user`,
`--network`, even `--rm`). That is the point of the raw block, per
PRINCIPLES.md #1 and #3 -- the risk is the author's. The single exception:
byre re-asserts its own `--label` set *after* `run_args`, because lifecycle
and `byre status` must always find the session (ADR 0004).

Consequences: raw `run_args` can undermine any protective posture
(`--network host`, `--cap-add`, `--entrypoint`, ...), so their presence
degrades status claims (see ADR 0010's honesty rules) -- byre reports,
it never refuses.

## Addendum 2026-07-29: the exception is the LABEL SET, not a fixed pair

As written this ADR named the exception as the `byre.project`/`byre.workdir`
identity pair. That was true when it was decided and had quietly stopped
being true: `runner.RunArgs` re-asserts the whole of `RunParams.Labels`, and
that slice has grown three times since.

The rule is therefore restated as the mechanism rather than an inventory:
**every label byre puts on `RunParams.Labels` is re-asserted after
`run_args`, and none of them can be overridden by a raw `--label`.** The set
today:

- `byre.project`, `byre.workdir` -- identity; the original pair (ADR 0004).
- `byre.client=<pid>` -- the host byre that started the session, so status
  can tell an orphaned box from one with a live byre attached.
- `byre.run=<nonce>` -- the per-invocation ownership proof for netns-init
  hooks. Protection is load-bearing here rather than merely useful: the
  project and workdir label values are derivable from the project path, so
  a container carrying a forged one could otherwise capture a root +
  NET_ADMIN helper.
- `byre.launch=<sha256>` -- the launch record's content address (ADR 0053).
  Protection is what stops a raw `--label byre.launch=...` pointing `byre
  status` at a record of the author's choosing, which would make the page
  describe a box that never ran.

Naming the mechanism rather than the members is the point of the amendment:
an inventory in an ADR goes stale silently the next time a label is added,
and this one already had, twice, before anyone noticed. The DECISION is
unchanged -- raw `run_args` win over every byre flag except byre's own
labels -- and no behavior changes with this text.
